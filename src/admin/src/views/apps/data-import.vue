<template>
  <div class="data-import">
    <!-- 步骤一：包列表 -->
    <n-card v-if="step === 'packages'" title="数据导入" class="mb-16">
      <template #header-extra>
        <n-text depth="3" style="font-size: 12px">导入目标：当前选中社区（{{ communityName }}）</n-text>
      </template>
      <n-empty v-if="!packages.length" description="暂无可用数据包" class="empty" />
      <div v-for="pkg in packages" :key="pkg.key" class="pkg-card">
        <div class="pkg-head">
          <div>
            <div class="pkg-name">{{ pkg.name }}</div>
            <div class="pkg-desc">{{ pkg.description }}</div>
          </div>
          <n-button type="primary" :loading="previewing" @click="startPreview(pkg)">选择数据并导入</n-button>
        </div>
        <div class="section-tags">
          <n-tag v-for="sec in pkg.sections" :key="sec.key" size="small" :bordered="false">
            {{ sec.name }} · {{ sec.item_count }} 项
          </n-tag>
        </div>
      </div>
    </n-card>

    <!-- 步骤二：预检报告（可排除） -->
    <n-card v-if="step === 'preview'" title="预检报告" class="mb-16">
      <template #header-extra>
        <n-space align="center">
          <n-text depth="3" style="font-size: 12px">{{ communityName }}</n-text>
          <n-button size="small" @click="step = 'packages'">返回包列表</n-button>
        </n-space>
      </template>
      <n-alert type="info" :show-icon="false" class="mb-16">
        已按当前社区状态完成预检：默认勾选全部可导入项，<b>取消勾选即排除该项</b>；
        「依赖缺失」项不可导入，需先导入其依赖（分类 / 商品目录）；
        「用户券与购物车」需先在下方选择导入会员。
      </n-alert>

      <!-- 用户券/购物车必须指定导入会员（未选择时该组禁止导入） -->
      <div v-if="hasUserAssets" class="user-target-bar">
        <span class="bar-label">「用户券与购物车」导入到会员：</span>
        <n-tag v-if="targetUser" closable type="primary" @close="clearTargetUser">
          {{ targetUser.nickname || '未设置昵称' }}（{{ maskPhone(targetUser.phone) }}）
        </n-tag>
        <n-button size="small" type="primary" @click="showUserPicker = true">
          {{ targetUser ? '更换会员' : '选择会员' }}
        </n-button>
        <n-text v-if="needUserTarget" type="warning" style="font-size: 12px">
          未选择会员时该组数据禁止导入
        </n-text>
      </div>
      <UserPicker v-model:show="showUserPicker" title="选择导入会员" @select="onPickTargetUser" />

      <div v-for="group in previewGroups" :key="group.key" class="preview-group">
        <div class="group-title">
          {{ group.name }}
          <n-button text size="tiny" type="primary" @click="toggleGroup(group)">全选 / 清空本组</n-button>
        </div>
        <n-data-table
          size="small"
          :columns="previewColumns"
          :data="group.items"
          :row-key="(row: DataImportItem) => row.item_key"
          :max-height="320"
        />
      </div>

      <div class="execute-bar">
        <n-space align="center">
          <span class="exec-label">导入后商品状态</span>
          <n-radio-group v-model:value="onSale">
            <n-radio :value="true">上架（默认）</n-radio>
            <n-radio :value="false">下架（调整后再上架）</n-radio>
          </n-radio-group>
        </n-space>
        <n-space>
          <n-text depth="3" style="font-size: 12px">
            已选 {{ selectedCount }} 项 · 待新建 {{ createCount }} 项 · 已存在跳过 {{ skipCount }} 项
          </n-text>
          <n-button type="primary" :loading="importing" :disabled="selectedCount === 0" @click="execute">
            执行导入
          </n-button>
        </n-space>
      </div>
    </n-card>

    <!-- 步骤三：结果 -->
    <n-card v-if="step === 'result'" title="导入结果" class="mb-16">
      <template #header-extra>
        <n-button size="small" @click="reset">完成</n-button>
      </template>
      <n-space class="mb-16">
        <n-statistic label="成功创建" :value="result?.created ?? 0" />
        <n-statistic label="跳过（已存在）" :value="result?.skipped ?? 0" />
        <n-statistic label="失败" :value="result?.failed ?? 0" />
      </n-space>
      <n-alert v-if="result && result.failed > 0" type="warning" :show-icon="true" class="mb-16">
        存在失败项：单条失败不影响其余数据，可按下方明细排查后重试（已存在项会自动跳过）。
      </n-alert>
      <n-data-table
        size="small"
        :columns="resultColumns"
        :data="result?.details ?? []"
        :row-key="(row: DataImportItem) => row.section_key + '-' + row.item_key"
        :max-height="420"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NButton,
  NCheckbox,
  NTag,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { listDataPackages, previewDataImport, importDataPackage } from '@/api/dataImport'
import type { DataImportItem, DataPackage } from '@/api/dataImport'
import { getCommunityList } from '@/api/user'
import type { User } from '@/api/types'
import UserPicker from '@/components/UserPicker.vue'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const authStore = useAuthStore()

type Step = 'packages' | 'preview' | 'result'
const step = ref<Step>('packages')
const packages = ref<DataPackage[]>([])
const currentPkg = ref<DataPackage | null>(null)
const previewItems = ref<DataImportItem[]>([])
const /** 手动取消勾选的条目 key */ uncheckedKeys = ref<Set<string>>(new Set())
const previewing = ref(false)
const importing = ref(false)
const onSale = ref(true)
const result = ref<{ created: number; skipped: number; failed: number; details: DataImportItem[] } | null>(null)

const communityName = ref('')

// --- 指定导入会员（用户券/购物车落到该会员名下） ---
const showUserPicker = ref(false)
const targetUser = ref<User | null>(null)

/** 预检结果是否包含用户侧数据段（需指定导入会员） */
const hasUserAssets = computed(() => previewItems.value.some((it) => it.section_key === 'user_assets'))
/** 未选择导入会员：用户侧数据组禁止导入 */
const needUserTarget = computed(() => hasUserAssets.value && !targetUser.value)

/**
 * 手机号脱敏展示：138****1234
 */
function maskPhone(phone: string): string {
  if (!phone || phone.length < 7) return phone
  return `${phone.slice(0, 3)}****${phone.slice(-4)}`
}

/**
 * 选中导入会员后重新预检（用户侧条目按新会员重算依赖与幂等判定）
 */
function onPickTargetUser(u: User) {
  targetUser.value = u
  void rerunPreview()
}

/**
 * 清除已选导入会员并重新预检
 */
function clearTargetUser() {
  targetUser.value = null
  void rerunPreview()
}

/**
 * 预检步骤内重跑预检（会员变更时调用）
 */
async function rerunPreview() {
  if (currentPkg.value && step.value === 'preview') await startPreview(currentPkg.value)
}

/**
 * 预检结果按条目分组（组名取条目所在 section 的展示名，回退到 section key）
 */
interface PreviewGroup {
  key: string
  name: string
  items: DataImportItem[]
}
const sectionNameOf = (key: string) =>
  currentPkg.value?.sections.find((s) => s.key === key)?.name ?? key

const previewGroups = computed<PreviewGroup[]>(() => {
  const order: string[] = []
  const map = new Map<string, PreviewGroup>()
  for (const it of previewItems.value) {
    if (!map.has(it.section_key)) {
      order.push(it.section_key)
      map.set(it.section_key, { key: it.section_key, name: sectionNameOf(it.section_key), items: [] })
    }
    map.get(it.section_key)!.items.push(it)
  }
  return order.map((k) => map.get(k)!)
})

/** 当前勾选（未被取消且非依赖缺失）的条目 */
const selectedItems = computed(() =>
  previewItems.value.filter((it) => it.selected && !uncheckedKeys.value.has(it.item_key)),
)
const selectedCount = computed(() => selectedItems.value.length)
const skipCount = computed(() => previewItems.value.filter((it) => it.action === 'skip').length)
/** 勾选条目里预检判定为「可新建」的数量（skip 项即便勾选，执行时也只会跳过不产生数据） */
const createCount = computed(() => selectedItems.value.filter((it) => it.action === 'create').length)

const previewColumns: DataTableColumns<DataImportItem> = [
  {
    title: '',
    key: 'check',
    width: 44,
    render(row) {
      if (row.action === 'depend_missing') return ''
      return h(NCheckbox, {
        checked: row.selected && !uncheckedKeys.value.has(row.item_key),
        onUpdateChecked: (checked: boolean) => {
          if (checked) uncheckedKeys.value.delete(row.item_key)
          else uncheckedKeys.value.add(row.item_key)
          // 触发 Set 响应式更新
          uncheckedKeys.value = new Set(uncheckedKeys.value)
        },
      })
    },
  },
  { title: '名称', key: 'name', width: 220, ellipsis: { tooltip: true } },
  {
    title: '判定',
    key: 'action',
    width: 100,
    render(row) {
      const map: Record<string, { label: string; type: 'success' | 'default' | 'warning' }> = {
        create: { label: '将导入', type: 'success' },
        skip: { label: '已存在', type: 'default' },
        depend_missing: { label: '依赖缺失', type: 'warning' },
      }
      const conf = map[row.action] ?? { label: row.action, type: 'default' as const }
      return h(NTag, { size: 'small', type: conf.type, bordered: false }, { default: () => conf.label })
    },
  },
  { title: '说明', key: 'detail', ellipsis: { tooltip: true } },
]

const resultColumns: DataTableColumns<DataImportItem> = [
  { title: '条目', key: 'name', width: 220, ellipsis: { tooltip: true } },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      const map: Record<string, { label: string; type: 'success' | 'default' | 'error' }> = {
        created: { label: '已创建', type: 'success' },
        skipped: { label: '已跳过', type: 'default' },
        failed: { label: '失败', type: 'error' },
      }
      const conf = map[row.status ?? ''] ?? { label: row.status ?? '-', type: 'default' as const }
      return h(NTag, { size: 'small', type: conf.type, bordered: false }, { default: () => conf.label })
    },
  },
  {
    title: '说明',
    key: 'message',
    ellipsis: { tooltip: true },
    render: (row) => row.message || row.detail,
  },
]

/**
 * 加载包列表
 */
async function loadPackages() {
  try {
    const res = await listDataPackages()
    packages.value = res.data.data?.packages ?? []
  } catch {
    message.error('加载数据包失败')
  }
}

/**
 * 进入预检：先全量预检，由用户在报告中排除
 */
async function startPreview(pkg: DataPackage) {
  currentPkg.value = pkg
  previewing.value = true
  try {
    const res = await previewDataImport(pkg.key, [], targetUser.value?.phone ?? '')
    previewItems.value = res.data.data?.items ?? []
    uncheckedKeys.value = new Set()
    step.value = 'preview'
  } catch {
    message.error('预检失败')
  } finally {
    previewing.value = false
  }
}

/**
 * 整组勾选/清空（跳过依赖缺失项）
 */
function toggleGroup(group: PreviewGroup) {
  const checkable = group.items.filter((it) => it.action !== 'depend_missing')
  const allIn = checkable.every((it) => it.selected && !uncheckedKeys.value.has(it.item_key))
  const next = new Set(uncheckedKeys.value)
  if (allIn) {
    checkable.forEach((it) => next.add(it.item_key))
  } else {
    checkable.forEach((it) => next.delete(it.item_key))
  }
  uncheckedKeys.value = next
}

/**
 * 执行导入：selected_keys 传空（全选语义）+ 排除清单，与预检报告勾选态一致
 */
async function execute() {
  if (!currentPkg.value) return
  importing.value = true
  try {
    // 依赖缺失项（含未指定会员的用户侧条目）一并排除，避免必然失败的请求
    const excluded = new Set(uncheckedKeys.value)
    previewItems.value.forEach((it) => {
      if (it.action === 'depend_missing') excluded.add(it.item_key)
    })
    const res = await importDataPackage(
      currentPkg.value.key,
      [],
      [...excluded],
      onSale.value,
      targetUser.value?.phone ?? '',
    )
    result.value = res.data.data ?? null
    step.value = 'result'
    message.success(
      `导入完成：成功 ${result.value?.created ?? 0}，跳过 ${result.value?.skipped ?? 0}，失败 ${result.value?.failed ?? 0}`,
    )
  } catch {
    message.error('导入执行失败')
  } finally {
    importing.value = false
  }
}

/** 回到包列表（保留包数据，无需重新拉取） */
function reset() {
  step.value = 'packages'
  result.value = null
}

/** 拉取社区名（仅展示用，失败不影响功能） */
async function loadCommunityName() {
  const id = authStore.currentCommunityId
  if (id <= 0) {
    communityName.value = '未选择社区'
    return
  }
  communityName.value = `社区 #${id}`
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    const hit = (res.data.data?.list ?? []).find((c) => c.id === id)
    if (hit) communityName.value = hit.name
  } catch {
    /* 展示用途，静默降级为 社区 #id */
  }
}

onMounted(async () => {
  if (!authStore.isSuperAdmin) {
    message.error('数据导入仅超级管理员可用')
    return
  }
  loadPackages()
  loadCommunityName()
})
</script>

<style lang="scss" scoped>
.mb-16 {
  margin-bottom: 16px;
}
.pkg-card {
  border: 1px solid #efeff5;
  border-radius: 10px;
  padding: 16px;
  margin-bottom: 14px;
}
.pkg-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.pkg-name {
  font-size: 15px;
  font-weight: 600;
}
.pkg-desc {
  font-size: 12px;
  color: #999;
  margin-top: 4px;
}
.section-tags {
  margin-top: 12px;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.preview-group {
  margin-bottom: 18px;
}
.user-target-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  border: 1px dashed #e0e0e6;
  border-radius: 8px;
  padding: 10px 14px;
  margin-bottom: 16px;

  .bar-label {
    font-size: 13px;
    color: #666;
  }
}
.group-title {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.execute-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid #efeff5;
  padding-top: 14px;
  margin-top: 4px;
}
.exec-label {
  font-size: 13px;
  color: #666;
}
.empty {
  padding: 40px 0;
}
</style>
