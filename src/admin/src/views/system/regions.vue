<template>
  <n-card title="行政区划">
    <n-space vertical>
      <n-space align="center">
        <n-input
          v-model:value="keyword"
          placeholder="区划代码或名称"
          clearable
          style="width: 200px"
          @keyup.enter="handleFilterChange"
          @clear="handleFilterChange"
        />
        <n-select v-model:value="levelFilter" :options="levelFilterOptions" style="width: 140px" @update:value="handleFilterChange" />
        <n-select v-model:value="statusFilter" :options="statusFilterOptions" style="width: 140px" @update:value="handleFilterChange" />
        <n-button @click="handleFilterChange">查询</n-button>
        <n-button type="primary" :loading="checking" @click="startCheck">检查更新</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote />
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
import { ref, onMounted, onUnmounted, h } from 'vue'
import { NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRegionList, createRegionUpdateTask, getRegionUpdateTask, applyRegionUpdateTask } from '@/api/region'
import type { Region, RegionDiffItem, RegionUpdateTask } from '@/api/region'

/**
 * 行政区划管理页面
 * 展示区划代码库（省/市/区县三级），支持关键词/层级/状态筛选；
 * 「检查更新」创建异步任务拉取上游最新区划并与库内比对，
 * 任务完成后展示差异（新增/更名/停用），管理员确认后覆盖入库
 */
const dialog = useDialog()
const message = useMessage()
const list = ref<Region[]>([])
const loading = ref(false)
const pagination = ref({
  page: 1,
  pageSize: 20,
  itemCount: 0,
  onChange: (p: number) => {
    pagination.value.page = p
    loadData()
  },
})

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

const levelText: Record<number, string> = { 1: '省', 2: '市', 3: '区县' }

const columns: DataTableColumns<Region> = [
  { title: '区划代码', key: 'code', width: 120 },
  { title: '名称', key: 'name' },
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

function handleFilterChange() {
  pagination.value.page = 1
  loadData()
}

async function loadData() {
  loading.value = true
  try {
    const res = await getRegionList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      keyword: keyword.value || undefined,
      level: levelFilter.value,
      status: statusFilter.value,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
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
      task.value = res.data.data
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
          if (detail.data.code === 0) task.value = detail.data.data
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
