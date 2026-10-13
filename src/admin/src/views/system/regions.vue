<template>
  <n-card title="行政区划">
    <n-space vertical>
      <n-space align="center">
        <n-input
          v-model:value="keyword"
          placeholder="搜索区划代码或名称"
          clearable
          style="width: 220px"
        />
        <n-select v-model:value="levelFilter" :options="levelFilterOptions" style="width: 140px" />
        <n-select v-model:value="statusFilter" :options="statusFilterOptions" style="width: 140px" />
        <n-button :disabled="shouldAutoExpand" @click="toggleExpandAll">{{ allExpanded ? '收起全部' : '展开全部' }}</n-button>
        <n-button type="primary" :loading="checking" @click="startCheck">检查更新</n-button>
        <n-text depth="3" style="font-size: 12px">
          共 {{ regions.length }} 条<template v-if="hasFilter">，命中 {{ matchCount }} 条</template>
        </n-text>
      </n-space>
      <n-data-table
        :columns="columns"
        :data="displayTree"
        :loading="loading"
        :row-key="rowKey"
        :expanded-row-keys="effectiveExpandedKeys"
        :max-height="620"
        size="small"
        :pagination="false"
        @update:expanded-row-keys="handleExpandedChange"
      />
    </n-space>

    <!-- 更新任务弹窗：执行进度 → 差异展示 → 确认覆盖 -->
    <n-modal v-model:show="taskModal" preset="card" title="行政区划更新" style="width: 760px">
      <n-space vertical>
        <n-alert v-if="task && task.status === 5" type="error">
          更新失败：{{ task.error || '未知错误' }}
        </n-alert>
        <n-alert v-else-if="task && task.status < 4" type="info">
          {{ runningText }}
        </n-alert>

        <template v-if="task && task.status === 4">
          <n-alert v-if="task.applied" type="success">
            差异已于 {{ task.finished_at || '-' }} 覆盖入库。
          </n-alert>
          <template v-else>
            <n-descriptions :column="4" size="small" bordered>
              <n-descriptions-item label="上游总数">{{ task.total_remote }}</n-descriptions-item>
              <n-descriptions-item label="库内总数">{{ task.total_local }}</n-descriptions-item>
              <n-descriptions-item label="新增">{{ task.added_count }}</n-descriptions-item>
              <n-descriptions-item label="更名/变更">{{ task.updated_count }}</n-descriptions-item>
            </n-descriptions>
            <n-alert type="warning">
              上游撤销区划 {{ task.removed_count }} 条，确认后将标记为「停用」（不做物理删除，已引用代码不受影响）。
            </n-alert>

            <n-tabs type="line" size="small" animated>
              <n-tab-pane name="added" :tab="`新增 (${task.added_count})`">
                <n-data-table size="small" :max-height="280" :columns="addedColumns" :data="task.added || []" :pagination="false" />
              </n-tab-pane>
              <n-tab-pane name="updated" :tab="`更名/变更 (${task.updated_count})`">
                <n-data-table size="small" :max-height="280" :columns="updatedColumns" :data="task.updated || []" :pagination="false" />
              </n-tab-pane>
              <n-tab-pane name="removed" :tab="`停用 (${task.removed_count})`">
                <n-data-table size="small" :max-height="280" :columns="removedColumns" :data="task.removed || []" :pagination="false" />
              </n-tab-pane>
            </n-tabs>

            <n-space justify="end">
              <n-button @click="taskModal = false">暂不应用</n-button>
              <n-button type="primary" :loading="applying" @click="applyTask">覆盖入库</n-button>
            </n-space>
          </template>
        </template>

        <n-space justify="space-between" align="center">
          <n-text depth="3" style="font-size: 12px">数据源：{{ task?.source || '-' }}</n-text>
          <n-button size="small" @click="taskModal = false">关闭</n-button>
        </n-space>
      </n-space>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RegionList' })
import { ref, computed, onMounted, onUnmounted, h } from 'vue'
import { NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getAllRegions, createRegionUpdateTask, getRegionUpdateTask, applyRegionUpdateTask } from '@/api/region'
import type { Region, RegionDiffItem, RegionUpdateTask } from '@/api/region'

/**
 * 行政区划管理页面（树形）
 * 数据结构：省/市/区县三级，前端一次性拉全量后本地建树，
 * 关键词/层级/状态筛选全部在本地完成（不再逐次请求后端）；
 * 有筛选时自动展开命中节点的祖先链，无筛选时默认折叠省级、按需展开。
 *
 * 性能要点：
 * - 全量约 3400 条（三级区划），单次请求 ~300KB（gzip 后几十 KB），建树/过滤均为 O(n) 毫秒级
 * - 表格默认折叠，仅渲染可见行；n-data-table 树形模式只在展开时渲染子行
 * - 若未来区划扩到乡级（4 万+ 条），需改为后端建树 + 懒加载方案
 */

/** 树形节点：Region 附加 children（无子级时为 undefined，避免渲染展开箭头） */
interface RegionTreeNode extends Region {
  children?: RegionTreeNode[]
}

const dialog = useDialog()
const message = useMessage()

/**
 * 规范化任务详情：proto3 的 json tag 带 omitempty，
 * 0 值数字（如无差异时的 updated_count/removed_count）与 null 数组
 * 不会出现在响应 JSON 中，直接渲染会显示 undefined，统一兜底
 */
function normalizeTask(t: RegionUpdateTask): RegionUpdateTask {
  return {
    ...t,
    total_remote: t.total_remote ?? 0,
    total_local: t.total_local ?? 0,
    added_count: t.added_count ?? 0,
    updated_count: t.updated_count ?? 0,
    removed_count: t.removed_count ?? 0,
    added: t.added ?? [],
    updated: t.updated ?? [],
    removed: t.removed ?? [],
  }
}

// --- 数据加载与建树 ---

const regions = ref<Region[]>([])
const loading = ref(false)

const keyword = ref('')
const levelFilter = ref(0)
const statusFilter = ref(1)

const levelFilterOptions = [
  { label: '全部层级', value: 0 },
  { label: '省', value: 1 },
  { label: '市', value: 2 },
  { label: '区县', value: 3 },
]
const statusFilterOptions = [
  { label: '仅正常', value: 1 },
  { label: '全部状态', value: 0 },
  { label: '已停用', value: 2 },
]

const hasFilter = computed(
  () => keyword.value.trim() !== '' || levelFilter.value !== 0 || statusFilter.value !== 0
)

/**
 * 需要自动展开命中路径的筛选（关键词/层级）。
 * 状态筛选只隐藏节点、不改变树的层级结构，
 * 不触发强制展开——否则默认「仅正常」时页面永远处于自动展开态，折叠操作会失效
 */
const shouldAutoExpand = computed(() => keyword.value.trim() !== '' || levelFilter.value !== 0)

/** 按 code 建树：parent_code 挂接，父级缺失（数据异常兜底）时挂为根节点，全树按 code 排序 */
function buildTree(list: Region[]): RegionTreeNode[] {
  const nodeMap = new Map<string, RegionTreeNode>()
  for (const r of list) nodeMap.set(r.code, { ...r })

  const roots: RegionTreeNode[] = []
  for (const node of nodeMap.values()) {
    const parent = node.parent_code && node.parent_code !== '0' ? nodeMap.get(node.parent_code) : undefined
    if (parent && parent !== node) {
      if (!parent.children) parent.children = []
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }
  const sortRec = (nodes: RegionTreeNode[]) => {
    nodes.sort((a, b) => a.code.localeCompare(b.code))
    for (const n of nodes) if (n.children) sortRec(n.children)
  }
  sortRec(roots)
  return roots
}

/** 单节点是否命中筛选条件（关键词 AND 层级 AND 状态） */
function matchNode(node: RegionTreeNode): boolean {
  const kw = keyword.value.trim()
  if (kw !== '' && !node.code.includes(kw) && !node.name.includes(kw)) return false
  if (levelFilter.value !== 0 && node.level !== levelFilter.value) return false
  if (statusFilter.value !== 0 && node.status !== statusFilter.value) return false
  return true
}

/**
 * 递归过滤：自身命中或存在命中后代则保留，
 * 祖先链自动保留（保证树的层级上下文），同时统计自命中条数
 */
function filterNodes(nodes: RegionTreeNode[], counter: { count: number }): RegionTreeNode[] {
  const result: RegionTreeNode[] = []
  for (const node of nodes) {
    const children = node.children ? filterNodes(node.children, counter) : []
    const selfMatch = matchNode(node)
    if (selfMatch) counter.count++
    if (selfMatch || children.length > 0) {
      result.push(children.length > 0 ? { ...node, children } : { ...node, children: undefined })
    }
  }
  return result
}

/** 收集过滤后树中所有带子级的节点 code（用于自动展开命中路径） */
function collectBranchCodes(nodes: RegionTreeNode[], acc: string[]): string[] {
  for (const n of nodes) {
    if (n.children && n.children.length > 0) {
      acc.push(n.code)
      collectBranchCodes(n.children, acc)
    }
  }
  return acc
}

const fullTree = computed<RegionTreeNode[]>(() => buildTree(regions.value))

const filteredTree = computed<RegionTreeNode[]>(() => {
  if (!hasFilter.value) return fullTree.value
  return filterNodes(fullTree.value, { count: 0 })
})

/** 命中筛选条件的节点数（不含仅作上下文保留的祖先链） */
const matchCount = computed<number>(() => {
  if (!hasFilter.value) return 0
  const counter = { count: 0 }
  filterNodes(fullTree.value, counter)
  return counter.count
})

const displayTree = computed<RegionTreeNode[]>(() =>
  hasFilter.value ? filteredTree.value : fullTree.value
)

// 无筛选时用户手动控制的展开状态；有筛选时自动展开所有命中路径
const expandedKeys = ref<string[]>([])
const allExpanded = ref(false)

const autoExpandKeys = computed<string[]>(() => collectBranchCodes(filteredTree.value, []))

const effectiveExpandedKeys = computed<string[]>(() =>
  shouldAutoExpand.value ? autoExpandKeys.value : expandedKeys.value
)

const rowKey = (row: RegionTreeNode) => row.code

function handleExpandedChange(keys: (string | number)[]) {
  expandedKeys.value = keys as string[]
  allExpanded.value = false
}

/** 展开/收起全部：作用于全量树（无筛选场景） */
function toggleExpandAll() {
  if (allExpanded.value) {
    expandedKeys.value = []
    allExpanded.value = false
  } else {
    expandedKeys.value = collectBranchCodes(fullTree.value, [])
    allExpanded.value = true
  }
}

// --- 表格列 ---

const levelText: Record<number, string> = { 1: '省', 2: '市', 3: '区县' }

const columns: DataTableColumns<RegionTreeNode> = [
  { title: '区划代码', key: 'code', width: 140 },
  { title: '名称', key: 'name', minWidth: 200 },
  { title: '层级', key: 'level', width: 90, render: (row) => levelText[row.level] ?? String(row.level) },
  { title: '父级代码', key: 'parent_code', width: 120 },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(
        NTag,
        { type: row.status === 1 ? 'success' : 'warning', size: 'small' },
        { default: () => (row.status === 1 ? '正常' : '停用') }
      )
    },
  },
]

async function loadData() {
  loading.value = true
  try {
    const list = await getAllRegions()
    regions.value = list
    expandedKeys.value = []
    allExpanded.value = false
  } catch {
    message.error('加载行政区划数据失败')
  } finally {
    loading.value = false
  }
}

// --- 更新任务 ---

const checking = ref(false)
const taskModal = ref(false)
const applying = ref(false)
const task = ref<RegionUpdateTask | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

/** 任务执行中的进度文案 */
const runningText = ref('正在创建任务…')

const statusRunningText: Record<number, string> = {
  1: '任务排队中…',
  2: '正在拉取上游最新区划数据…',
  3: '正在与库内数据比对…',
}

// 差异明细表格列定义（新增/更名/停用三种模式列集不同）
const addedColumns: DataTableColumns<RegionDiffItem> = [
  { title: '区划代码', key: 'code', width: 120 },
  { title: '名称', key: 'name' },
  { title: '父级代码', key: 'parent_code', width: 120 },
]
const updatedColumns: DataTableColumns<RegionDiffItem> = [
  { title: '区划代码', key: 'code', width: 120 },
  { title: '原名称', key: 'name' },
  { title: '新名称', key: 'new_name' },
]
const removedColumns: DataTableColumns<RegionDiffItem> = [
  { title: '区划代码', key: 'code', width: 120 },
  { title: '名称', key: 'name' },
]

function startCheck() {
  checking.value = true
  createRegionUpdateTask()
    .then((res) => {
      if (res.data.code === 0) {
        task.value = res.data.data
        taskModal.value = true
        pollTask()
      }
    })
    .finally(() => {
      checking.value = false
    })
}

/** 轮询任务详情直至完成/失败 */
function pollTask() {
  stopPolling()
  if (!task.value) return
  const id = task.value.id
  runningText.value = statusRunningText[task.value.status] ?? '任务执行中…'
  pollTimer = setInterval(async () => {
    try {
      const res = await getRegionUpdateTask(id)
      if (res.data.code !== 0) return
      task.value = normalizeTask(res.data.data)
      if (task.value.status >= 4) {
        stopPolling()
        if (
          task.value.status === 4 &&
          !task.value.applied &&
          task.value.added_count + task.value.updated_count + task.value.removed_count === 0
        ) {
          message.success('区划数据已是最新，无差异')
        }
      } else {
        runningText.value = statusRunningText[task.value.status] ?? '任务执行中…'
      }
    } catch {
      // 轮询失败忽略，下个周期重试
    }
  }, 2000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function applyTask() {
  if (!task.value) return
  const id = task.value.id
  dialog.warning({
    title: '覆盖确认',
    content: `确定将差异覆盖入库吗？新增 ${task.value.added_count} 条、更名 ${task.value.updated_count} 条、停用 ${task.value.removed_count} 条。该操作立即生效。`,
    positiveText: '确认覆盖',
    negativeText: '取消',
    onPositiveClick: async () => {
      applying.value = true
      try {
        const res = await applyRegionUpdateTask(id)
        if (res.data.code === 0) {
          message.success('差异已覆盖入库')
          const detail = await getRegionUpdateTask(id)
          if (detail.data.code === 0) task.value = normalizeTask(detail.data.data)
          loadData()
        }
      } finally {
        applying.value = false
      }
    },
  })
}

onMounted(loadData)
onUnmounted(stopPolling)
</script>
