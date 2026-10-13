<template>
  <div class="migrate-page">
    <n-card title="本地附件迁移到 OSS">
      <n-space vertical>
        <!-- 概览统计 -->
        <n-space wrap :size="[8, 8]">
          <n-tag type="info">共 {{ overview?.total ?? 0 }} 个文件</n-tag>
          <n-tag type="warning">待迁移 {{ overview?.pending ?? 0 }}</n-tag>
          <n-tag type="info">已迁移 {{ overview?.migrated ?? 0 }}</n-tag>
          <n-tag type="success">验证成功 {{ overview?.verified ?? 0 }}</n-tag>
          <n-tag type="error">迁移失败 {{ overview?.failed ?? 0 }}</n-tag>
          <n-tag type="error">验证失败 {{ overview?.verify_failed ?? 0 }}</n-tag>
          <n-tag type="warning">孤儿文件 {{ overview?.orphan ?? 0 }}</n-tag>
          <n-tag>本地已删除 {{ overview?.local_deleted ?? 0 }}</n-tag>
        </n-space>

        <!-- 扫描异常提示 -->
        <n-alert v-if="lastMissing > 0" type="warning" title="本地文件缺失">
          本次扫描发现 {{ lastMissing }} 条附件记录在本地没有对应文件，这些记录未纳入迁移清单，请检查本地 attachment/ 目录是否完整。
        </n-alert>

        <!-- 工具栏 -->
        <n-space wrap align="center">
          <n-button
            type="primary" ghost
            :loading="scanning" :disabled="!canOperate || batchRunning"
            @click="onScan"
          >
            扫描生成任务
          </n-button>
          <n-button
            type="primary"
            :loading="batchKind === 'migrate'" :disabled="!canOperate || batchRunning"
            @click="runBatch('migrate')"
          >
            全部迁移
          </n-button>
          <n-button
            :loading="batchKind === 'verify'" :disabled="!canOperate || batchRunning"
            @click="runBatch('verify')"
          >
            全部验证
          </n-button>
          <n-button
            type="error" ghost
            :disabled="!canOperate || batchRunning || (overview?.verified ?? 0) === 0"
            @click="onDeleteVerified"
          >
            删除已验证的本地文件（{{ overview?.verified ?? 0 }}）
          </n-button>
          <n-select
            v-model:value="statusFilter" style="width: 150px"
            :options="statusOptions" clearable placeholder="状态筛选"
            :disabled="batchRunning" @update:value="onFilterChange"
          />
          <n-button quaternary :disabled="batchRunning" @click="loadList">刷新</n-button>
        </n-space>

        <!-- 批量进度 -->
        <n-alert v-if="batchRunning" type="info" title="批量处理中">
          正在{{ batchKind === 'migrate' ? '迁移' : '验证' }}：{{ batchDone }} / {{ batchTotal }}，请保持页面打开。
          <n-progress type="line" :percentage="batchPercent" :show-indicator="false" style="margin-top: 6px" />
        </n-alert>

        <n-data-table :columns="columns" :data="items" :loading="loading" :row-key="(r: AttachmentMigrateItem) => r.id" scroll-x="1180" />
        <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadList" />
      </n-space>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref, type VNodeChild } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag, createDiscreteApi } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import {
  listAttachmentMigrate, scanAttachmentMigrate,
  migrateAttachmentItem, verifyAttachmentItem,
  deleteAttachmentLocal, deleteAttachmentLocalVerified,
} from '@/api/attachment'
import type { AttachmentMigrateItem, AttachmentMigrateOverview, AttachmentMigrateStatus } from '@/api/attachment'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

/**
 * 本地附件迁移任务面板
 * 三段式操作：扫描生成清单 → 迁移（成功后自动验证）→ 手动删除本地文件
 * 批量操作由前端循环调用单条接口驱动，天然有进度、中断无副作用
 */
const { message, dialog } = createDiscreteApi(['message', 'dialog'])
const router = useRouter()
const authStore = useAuthStore()

const loading = ref(false)
const scanning = ref(false)
const items = ref<AttachmentMigrateItem[]>([])
const overview = ref<AttachmentMigrateOverview | null>(null)
const lastMissing = ref(0)
const statusFilter = ref<AttachmentMigrateStatus | null>(null)
const page = ref(1)
const pageCount = ref(1)
const PAGE_SIZE = 20

// 批量任务状态
const batchKind = ref<'migrate' | 'verify' | ''>('')
const batchDone = ref(0)
const batchTotal = ref(0)
const batchRunning = computed(() => batchKind.value !== '')
const batchPercent = computed(() => (batchTotal.value > 0 ? Math.round((batchDone.value / batchTotal.value) * 100) : 0))

/** 是否具备操作权限（仅查看权限时按钮禁用） */
const canOperate = computed(() => authStore.isSuperAdmin || authStore.hasPermission('settings:update'))

/** 状态筛选选项 */
const statusOptions: { label: string; value: AttachmentMigrateStatus }[] = [
  { label: '待迁移', value: 'pending' },
  { label: '迁移中', value: 'migrating' },
  { label: '已迁移', value: 'migrated' },
  { label: '验证成功', value: 'verified' },
  { label: '迁移失败', value: 'failed' },
  { label: '验证失败', value: 'verify_failed' },
  { label: '孤儿文件', value: 'orphan' },
  { label: '本地已删除', value: 'local_deleted' },
]

/** 条目状态展示映射 */
const statusMeta: Record<AttachmentMigrateStatus, { label: string; type: 'default' | 'info' | 'success' | 'warning' | 'error' }> = {
  pending: { label: '待迁移', type: 'warning' },
  migrating: { label: '迁移中', type: 'info' },
  migrated: { label: '已迁移·待验证', type: 'info' },
  verified: { label: '验证成功', type: 'success' },
  failed: { label: '迁移失败', type: 'error' },
  verify_failed: { label: '验证失败', type: 'error' },
  local_deleted: { label: '本地已删除', type: 'default' },
  orphan: { label: '孤儿文件', type: 'warning' },
}

/** 迁移表格列 */
const columns: DataTableColumns<AttachmentMigrateItem> = [
  {
    title: '预览', key: 'preview', width: 72,
    render(row) {
      if (row.status === 'local_deleted' || row.status === 'orphan') {
        return h('div', { class: 'thumb-placeholder' }, row.status === 'orphan' ? '孤儿' : '已删除')
      }
      return h('img', {
        src: `/uploads/${row.path}`, class: 'thumb',
        onError: (e: Event) => { (e.target as HTMLImageElement).style.visibility = 'hidden' },
      })
    },
  },
  {
    title: '文件路径', key: 'path', minWidth: 240, ellipsis: { tooltip: true },
    render: (row) => h('span', null, [`${row.path}（${row.name || '-'}）`]),
  },
  { title: '大小', key: 'size', width: 90, render: (row) => formatSize(row.size) },
  {
    title: '状态', key: 'status', width: 110,
    render(row) {
      const meta = statusMeta[row.status] ?? { label: row.status, type: 'default' as const }
      return h(NTag, { type: meta.type, size: 'small', bordered: false }, { default: () => meta.label })
    },
  },
  {
    title: '失败原因', key: 'error_msg', width: 150, ellipsis: { tooltip: true },
    render: (row) => h('span', null, row.error_msg || '-'),
  },
  { title: '迁移时间', key: 'migrated_at', width: 160, render: (row) => formatDateTime(row.migrated_at) },
  { title: '验证时间', key: 'verified_at', width: 160, render: (row) => formatDateTime(row.verified_at) },
  {
    title: '操作', key: 'actions', width: 200,
    render(row) {
      const buttons: VNodeChild[] = []
      if (row.status !== 'orphan' && row.status !== 'local_deleted' && row.status !== 'verified') {
        buttons.push(h(NButton, {
          size: 'small', type: 'primary',
          disabled: batchRunning.value || row.status === 'migrating',
          onClick: () => onMigrateOne(row),
        }, { default: () => '迁移' }))
      }
      if (row.status === 'migrated' || row.status === 'verify_failed' || row.status === 'verified') {
        buttons.push(h(NButton, {
          size: 'small',
          disabled: batchRunning.value,
          onClick: () => onVerifyOne(row),
        }, { default: () => '验证' }))
      }
      if (row.status === 'verified') {
        buttons.push(h(NButton, {
          size: 'small', type: 'error', ghost: true,
          disabled: batchRunning.value,
          onClick: () => onDeleteOne(row),
        }, { default: () => '删除本地' }))
      }
      if (!buttons.length) {
        return h('span', { style: 'color: #999; font-size: 12px' }, '—')
      }
      return h(NSpace, { size: 6, wrap: false }, { default: () => buttons })
    },
  },
]

/**
 * 格式化文件大小展示
 * @param size 字节数
 * @returns 可读大小文本
 */
function formatSize(size: number): string {
  if (!size) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(2)} MB`
}

/**
 * 加载条目列表与概览统计
 */
async function loadList() {
  loading.value = true
  try {
    const res = await listAttachmentMigrate({
      status: statusFilter.value || undefined,
      page: page.value,
      page_size: PAGE_SIZE,
    })
    // 后端 proto List 字段带 omitempty，空列表时字段缺失，须兜底为空数组
    items.value = res.data.data.list ?? []
    overview.value = res.data.data.overview
    pageCount.value = Math.ceil(res.data.data.pagination.total / PAGE_SIZE) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 状态筛选变更后回到第一页重新加载
 */
function onFilterChange() {
  page.value = 1
  loadList()
}

/**
 * 扫描本地附件目录生成/刷新迁移任务
 */
async function onScan() {
  scanning.value = true
  try {
    const res = await scanAttachmentMigrate()
    overview.value = res.data.data
    lastMissing.value = res.data.data.missing
    message.success(`扫描完成：共 ${res.data.data.total} 个文件，待迁移 ${res.data.data.pending} 个`)
    page.value = 1
    await loadList()
  } catch {
    // 错误提示已由请求拦截器统一弹出
  } finally {
    scanning.value = false
  }
}

/**
 * 用最新条目替换列表中的旧行
 * @param item 操作后的最新条目
 */
function applyItem(item: AttachmentMigrateItem) {
  const idx = items.value.findIndex((r) => r.id === item.id)
  if (idx >= 0) items.value[idx] = item
}

/**
 * 迁移单个条目（成功后服务端自动验证）
 * @param row 条目行
 */
async function onMigrateOne(row: AttachmentMigrateItem) {
  try {
    const res = await migrateAttachmentItem(row.id)
    applyItem(res.data.data)
    message.success(`迁移完成：${row.path}`)
    await loadList()
  } catch {
    await loadList()
  }
}

/**
 * 验证单个条目
 * @param row 条目行
 */
async function onVerifyOne(row: AttachmentMigrateItem) {
  try {
    const res = await verifyAttachmentItem(row.id)
    applyItem(res.data.data)
    message.success(res.data.data.status === 'verified' ? `验证通过：${row.path}` : `验证未通过：${res.data.data.error_msg || '对象不一致'}`)
    await loadList()
  } catch {
    await loadList()
  }
}

/**
 * 删除单个条目的本地文件（确认后执行）
 * @param row 条目行
 */
function onDeleteOne(row: AttachmentMigrateItem) {
  dialog.warning({
    title: '删除本地文件',
    content: `确定删除本地文件 ${row.path} 吗？OSS 上仍保留副本，该操作不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        const res = await deleteAttachmentLocal(row.id)
        applyItem(res.data.data)
        message.success('本地文件已删除')
        await loadList()
      } catch {
        // 错误提示已由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 一键删除全部已验证条目的本地文件（确认后执行）
 */
function onDeleteVerified() {
  const count = overview.value?.verified ?? 0
  dialog.warning({
    title: '批量删除本地文件',
    content: `确定删除全部 ${count} 个已验证条目的本地文件吗？OSS 上仍保留副本，该操作不可恢复。`,
    positiveText: '全部删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        const res = await deleteAttachmentLocalVerified()
        message.success(`已删除 ${res.data.data.deleted} 个本地文件`)
        await loadList()
      } catch {
        // 错误提示已由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 按状态集合分页拉取全部条目（批量操作的数据源）
 * @param statuses 状态列表
 * @returns 全部命中条目
 */
async function fetchAllByStatuses(statuses: AttachmentMigrateStatus[]): Promise<AttachmentMigrateItem[]> {
  const out: AttachmentMigrateItem[] = []
  for (const status of statuses) {
    let p = 1
    for (;;) {
      const res = await listAttachmentMigrate({ status, page: p, page_size: 100 })
      const { list, pagination } = res.data.data
      // list 字段空列表时缺失（omitempty），spread 前必须兜底，否则抛 TypeError
      const rows = list ?? []
      out.push(...rows)
      if (rows.length === 0 || p * 100 >= pagination.total) break
      p++
    }
  }
  return out
}

/**
 * 批量迁移/验证：顺序逐条调用单条接口，实时更新进度
 * @param kind 批量类型
 */
async function runBatch(kind: 'migrate' | 'verify') {
  const statuses: AttachmentMigrateStatus[] = kind === 'migrate'
    ? ['pending', 'failed', 'verify_failed', 'migrating']
    : ['migrated', 'verify_failed']

  let targets: AttachmentMigrateItem[]
  try {
    targets = await fetchAllByStatuses(statuses)
  } catch {
    return
  }
  if (!targets.length) {
    message.info(kind === 'migrate' ? '没有待迁移的文件' : '没有待验证的文件')
    return
  }

  batchKind.value = kind
  batchTotal.value = targets.length
  batchDone.value = 0
  let ok = 0
  for (const target of targets) {
    try {
      const res = kind === 'migrate'
        ? await migrateAttachmentItem(target.id)
        : await verifyAttachmentItem(target.id)
      applyItem(res.data.data)
      if (kind === 'verify' || res.data.data.status === 'verified') ok++
    } catch {
      // 单条失败继续处理后续条目，错误提示由拦截器弹出
    }
    batchDone.value++
  }
  batchKind.value = ''
  message.success(kind === 'migrate'
    ? `批量迁移完成：${ok}/${targets.length} 条验证通过`
    : `批量验证完成：${ok}/${targets.length} 条通过`)
  page.value = 1
  await loadList()
}

onMounted(() => {
  // 从附件配置页跳转时携带 scan=1 自动扫描
  if (router.currentRoute.value.query.scan === '1') {
    onScan()
  } else {
    loadList()
  }
})
</script>

<style lang="scss" scoped>
.migrate-page {
  padding: 16px;
}

.thumb {
  width: 48px;
  height: 48px;
  object-fit: cover;
  border-radius: 4px;
  border: 1px solid #eee;
  display: block;
}

.thumb-placeholder {
  width: 48px;
  height: 48px;
  border-radius: 4px;
  border: 1px dashed #ddd;
  color: #999;
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
