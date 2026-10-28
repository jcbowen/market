<template>
  <!-- 右上角下载中心：图标（未下载完成数角标）+ 弹出面板（任务列表/状态轮询/下载/删除） -->
  <n-popover
    v-model:show="visible"
    trigger="click"
    placement="bottom-end"
    :width="680"
    :content-style="{ padding: 0 }"
    @update:show="onPopoverToggle"
  >
    <template #trigger>
      <n-badge :value="badgeCount" :max="99" :show="badgeCount > 0">
        <n-button text class="dl-trigger" title="下载中心">
          <n-icon size="20"><cloud-download-outline /></n-icon>
        </n-button>
      </n-badge>
    </template>

    <div class="dl-panel">
      <!-- 头部：标题 + 记录数；超管加「全部记录」标识（能看到所有人的任务） -->
      <div class="dl-head">
        <div class="head-left">
          <n-icon size="18" class="head-icon"><cloud-download-outline /></n-icon>
          <span class="title">下载中心</span>
          <n-tag v-if="total > 0" size="tiny" round :bordered="false">{{ total }}</n-tag>
          <n-tag v-if="isSuper" size="tiny" round :bordered="false" type="warning">全部记录</n-tag>
        </div>
        <n-text depth="3" class="head-hint">文件生成后保留 3 天</n-text>
      </div>

      <n-spin :show="loading">
        <n-empty v-if="!tasks.length && !loading" class="dl-empty" description="暂无导出记录">
          <template #icon>
            <n-icon><documents-outline /></n-icon>
          </template>
          <template #extra>
            <n-text depth="3" style="font-size: 12px">在订单列表点击「导出 CSV」后，任务会出现在这里</n-text>
          </template>
        </n-empty>

        <div v-else class="task-list">
          <div v-for="t in tasks" :key="t.id" class="task-row" :class="`is-${stateOf(t)}`">
            <div class="task-icon">
              <n-icon v-if="stateOf(t) === 'running'" size="18" class="icon-spin"><sync-outline /></n-icon>
              <n-icon v-else-if="stateOf(t) === 'fail'" size="18"><alert-circle-outline /></n-icon>
              <n-icon v-else-if="stateOf(t) === 'pending'" size="18"><time-outline /></n-icon>
              <n-icon v-else size="18"><document-text-outline /></n-icon>
            </div>

            <div class="task-main">
              <!-- 第一行：创建人（仅超管可见他人任务，需归属标识）+ 文件名 + 状态 -->
              <div class="task-top">
                <n-tag v-if="isSuper && t.admin_name" size="tiny" :bordered="false" class="owner-tag">
                  {{ t.admin_name }}
                </n-tag>
                <span class="task-name" :title="t.file_name">{{ t.file_name || '导出文件' }}</span>
                <!-- 状态：1待处理 2生成中 3已完成 4失败 -->
                <n-tag v-if="t.status === 3" type="success" size="tiny" :bordered="false">已完成</n-tag>
                <n-tooltip v-else-if="t.status === 4" trigger="hover">
                  <template #trigger>
                    <n-tag type="error" size="tiny" :bordered="false">失败</n-tag>
                  </template>
                  {{ t.error_msg || '生成失败' }}
                </n-tooltip>
                <n-tag v-else type="info" size="tiny" :bordered="false">{{ t.status === 2 ? '生成中…' : '排队中' }}</n-tag>
              </div>

              <!-- 第二行：条件摘要 · 生成时间 · 大小 · 行数 · 剩余保留时间 -->
              <div class="task-meta">
                <span class="meta-text">
                  {{ queryDescOf(t) }} · {{ formatDateTime(t.created_at) }}
                  <template v-if="t.status === 3"> · {{ formatSize(t.file_size) }} · {{ t.row_count }} 行</template>
                </span>
                <span v-if="retentionOf(t)" class="meta-retention">{{ retentionOf(t) }}</span>
              </div>

              <!-- 第三行：下载留痕（是否下载/次数所有人可见；下载人完整名单仅超管）；
                   完成态 error_msg 复用为截断提示 -->
              <div v-if="traceText(t) || (t.status === 3 && t.error_msg)" class="task-trace">
                <span class="trace-text">{{ traceText(t) }}</span>
                <n-tooltip v-if="isSuper && downloaderRecords(t).length > 1" trigger="hover">
                  <template #trigger>
                    <span class="trace-more">完整名单</span>
                  </template>
                  <div v-for="(d, i) in downloaderRecords(t)" :key="i" class="trace-row">
                    {{ d.admin_name }} · {{ d.at }}
                  </div>
                </n-tooltip>
                <span v-if="t.status === 3 && t.error_msg" class="trace-warn">{{ t.error_msg }}</span>
              </div>
            </div>

            <div class="task-ops">
              <!-- 失败重导出：按快照原条件重新发起（需求「失败可重新发起导出」） -->
              <n-button
                v-if="t.status === 4"
                size="tiny"
                type="primary"
                secondary
                :loading="retryingId === t.id"
                @click="onRetryExport(t)"
              >
                重新导出
              </n-button>
              <n-button
                v-if="t.status === 3"
                size="tiny"
                type="primary"
                secondary
                :loading="downloadingId === t.id"
                @click="onDownload(t)"
              >
                下载
              </n-button>
              <!-- 删除仅超管可见（接口侧同样仅超管放行） -->
              <n-button v-if="isSuper" size="tiny" quaternary type="error" @click="onDelete(t)">删除</n-button>
            </div>
          </div>
        </div>
      </n-spin>

      <div v-if="total > tasks.length" class="dl-foot">
        <n-pagination
          v-model:page="page"
          :item-count="total"
          :page-size="PAGE_SIZE"
          size="small"
          @update:page="loadTasks"
        />
      </div>
      <div v-if="hasRunning" class="dl-hint-bar">
        <n-icon size="14"><sync-outline /></n-icon>
        <span>有任务正在生成，完成后图标会出现角标</span>
      </div>
    </div>
  </n-popover>
</template>

<script setup lang="ts">
/**
 * 下载中心组件（布局右上角）
 * 功能：导出任务列表（生成状态/文件大小/行数/创建时间/剩余保留时间）、未下载角标、
 *       生成中任务轮询刷新、下载（留痕由后端记录）、失败按原条件重新导出、删除（仅超管，文件一并删除）
 * 权限版式：普通管理员仅见自己创建的任务（不显示创建人/下载人名单，无删除入口）；
 *           超管可见全部任务，行内显示创建人、下载留痕名单（完整名单 tooltip）与删除按钮
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NBadge, NButton, NEmpty, NIcon, NPagination, NPopover, NSpin, NTag, NText, NTooltip, useDialog, useMessage } from 'naive-ui'
import { CloudDownloadOutline, DocumentTextOutline, DocumentsOutline, SyncOutline, TimeOutline, AlertCircleOutline } from '@vicons/ionicons5'
import {
  countUndownloadedExports,
  createOrderExportTask,
  deleteOrderExportTask,
  downloadOrderExportTask,
  listOrderExportTasks,
  type ExportTask,
} from '@/api/order'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import { isRealtimeConnected, onRealtime, type ExportTaskChangedInfo } from '@/utils/realtime'

const PAGE_SIZE = 20
/**
 * 生成中任务轮询间隔（ms）：
 * - 面板打开：WS 断开时 3s；WS 已连时放宽到 15s（任务完成事件会即时叫醒，轮询退为兜底）
 * - 面板关闭：30s 慢速轮询只为角标
 */
const POLL_OPEN_MS = 3000
const POLL_OPEN_WS_MS = 15000
const POLL_CLOSED_MS = 30000

/** 下载留痕记录结构（downloaders JSON 数组元素） */
interface DownloaderRecord {
  admin_id: number
  admin_name: string
  at: string
}

const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()

/** 是否超管：驱动权限版式（创建人/下载名单/删除按钮/全部记录标识） */
const isSuper = computed(() => authStore.isSuperAdmin)

const visible = ref(false)
const loading = ref(false)
const tasks = ref<ExportTask[]>([])
const total = ref(0)
const page = ref(1)
/** 后端口径的未下载完成数（真实值；角标显示用 badgeCount 派生） */
const undownloaded = ref(0)
/**
 * 角标基线：打开面板后的首次列表加载落定为当时的未下载总数——
 * 打开时刻已存在的未下载任务视为已知晓、不再计入角标（需求口径「打开面板后清除」）；
 * 之后再新完成的文件按增量重新亮起（undownloaded - baseline > 0）
 */
const baseline = ref(0)
/** 打开后待落定基线的标志（首次 loadTasks 完成后复位） */
let pendingBaseline = false

/** 角标数：未下载总数减去已知悉基线，避免历史未下载任务让角标常驻 */
const badgeCount = computed(() => Math.max(0, undownloaded.value - baseline.value))

let pollTimer: ReturnType<typeof setInterval> | null = null

/** 实时通道在线状态（由本地 'connection' 事件维护，供轮询频率自适应） */
const realtimeUp = ref(isRealtimeConnected())

/** 实时事件退订函数 */
let disposeRealtime: Array<() => void> = []

/** 是否有在途任务（面板底部提示） */
const hasRunning = computed(() => tasks.value.some((t) => t.status === 1 || t.status === 2))

/**
 * 轮询周期：面板打开时加快轮询（用户盯着看），关闭时慢速轮询只为角标；
 * WS 已连时打开面板也放宽到 15s（完成事件即时叫醒，轮询退为丢消息的兜底）
 */
const pollMs = computed(() => {
  if (!visible.value) return POLL_CLOSED_MS
  return realtimeUp.value ? POLL_OPEN_WS_MS : POLL_OPEN_MS
})

/**
 * 任务条件摘要（创建时的快照；为空降级为导出类型文案）
 * @param t 任务行
 */
function queryDescOf(t: ExportTask): string {
  return t.query_desc || (t.export_type === 'orders' ? '订单导出' : t.export_type)
}

/**
 * 任务状态归类（驱动行卡片配色与图标）
 * @param t 任务行
 * @returns done 已完成 / fail 失败 / running 生成中 / pending 排队中
 */
function stateOf(t: ExportTask): 'done' | 'fail' | 'running' | 'pending' {
  if (t.status === 3) return 'done'
  if (t.status === 4) return 'fail'
  if (t.status === 2) return 'running'
  return 'pending'
}

/**
 * 字节转人读大小
 * @param size 字节数
 */
function formatSize(size: number): string {
  if (!size) return '—'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(2)} MB`
}

/**
 * 时间字符串转毫秒时间戳：兼容 RFC3339（DSN parseTime=True 下 datetime 回读形态，
 * 如 "2026-10-27T13:05:00+08:00"）与朴素格式 "YYYY-MM-DD HH:mm:ss"（Safari 需手动拆解）
 * @param v 时间字符串
 * @returns 毫秒时间戳，无法解析返回 NaN（调用方须判空兜底）
 */
function parseTimeOf(v: string): number {
  const direct = new Date(v).getTime()
  if (Number.isFinite(direct)) return direct
  const m = v.match(/^(\d{4})-(\d{1,2})-(\d{1,2})[ T](\d{1,2}):(\d{1,2})(?::(\d{1,2}))?/)
  if (m) return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +(m[6] ?? 0)).getTime()
  return NaN
}

/**
 * 剩余保留时间（人读）：文件到期前显示「剩 X 天 X 小时」，临期/过期降级提示
 * @param t 任务行
 * @returns 保留时间文案，无过期时间或解析失败返回空串
 */
function retentionOf(t: ExportTask): string {
  if (t.status !== 3 || !t.expires_at) return ''
  const diff = parseTimeOf(t.expires_at) - Date.now()
  // 解析失败（NaN）时静默隐藏，绝不输出「剩 NaN 天」
  if (!Number.isFinite(diff)) return ''
  if (diff <= 0) return '即将清理'
  const hours = diff / 3600000
  if (hours < 1) return `剩 ${Math.max(1, Math.round(diff / 60000))} 分钟`
  if (hours < 24) return `剩 ${Math.round(hours)} 小时`
  return `剩 ${Math.floor(hours / 24)} 天 ${Math.round(hours % 24)} 小时`
}

/**
 * 解析下载留痕 JSON（仅超管场景调用；普通管理员后端不下发）
 * @param raw 留痕 JSON 字符串
 * @returns 留痕记录数组，解析失败返回空数组
 */
function downloaderRecords(t: ExportTask): DownloaderRecord[] {
  if (!t.downloaders) return []
  try {
    const arr = JSON.parse(t.downloaders) as DownloaderRecord[]
    return Array.isArray(arr) ? arr : []
  } catch {
    return []
  }
}

/**
 * 下载留痕行文案：所有管理员可见「未下载/已下载 N 次」；超管额外展示最近下载人
 * @param t 任务行
 * @returns 文案，排队/生成中/失败任务返回空串
 */
function traceText(t: ExportTask): string {
  if (t.status !== 3) return ''
  if (t.download_count <= 0) return '未下载'
  const latest = downloaderRecords(t)[downloaderRecords(t).length - 1]
  const base = `已下载 ${t.download_count} 次`
  if (isSuper.value && latest) return `${base} · 最近 ${latest.admin_name} ${latest.at}`
  return base
}

/**
 * 面板开合回调：打开即刷新列表（响应内含未下载总数，打开后基线落定、角标清零）
 * 轮询周期的切换由 watch(pollMs) 统一负责
 * @param v 是否打开
 */
function onPopoverToggle(v: boolean) {
  if (!v) return
  page.value = 1
  pendingBaseline = true
  void loadTasks()
}

/**
 * 加载任务列表；打开后的首次加载把未下载总数落定为角标基线（已存在的未下载视为已知晓）
 */
async function loadTasks() {
  loading.value = true
  try {
    const res = await listOrderExportTasks({ page: page.value, page_size: PAGE_SIZE })
    if (res.data.code === 0) {
      tasks.value = res.data.data.list || []
      total.value = res.data.data.total || 0
      undownloaded.value = res.data.data.undownloaded || 0
      if (pendingBaseline && visible.value) {
        baseline.value = undownloaded.value
        pendingBaseline = false
      }
    }
  } finally {
    loading.value = false
  }
}

/**
 * 刷新未下载完成数真值（面板关闭时的慢速轮询与删除后调用；角标显示为其与基线的差值）
 */
async function refreshBadge() {
  try {
    const res = await countUndownloadedExports()
    if (res.data.code === 0) {
      undownloaded.value = res.data.data.count || 0
    }
  } catch {
    // 角标失败静默
  }
}

/** 正在下载的任务 ID（按钮 loading 用） */
const downloadingId = ref(0)

/**
 * 下载点击：blob 请求带鉴权头下载并触发浏览器保存，成功后角标即刻消减；
 * 留痕由后端 download 接口记录，列表刷新以更新留痕次数
 * @param t 任务行
 */
async function onDownload(t: ExportTask) {
  downloadingId.value = t.id
  try {
    await downloadOrderExportTask(t.id, t.file_name)
    message.success('已开始下载')
    onDownloaded(t)
    await loadTasks()
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  } finally {
    downloadingId.value = 0
  }
}

/**
 * 下载成功后的本地状态更新：标记为已下载（角标即刻消减）
 * @param t 任务行
 */
function onDownloaded(t: ExportTask) {
  if (t.download_count === 0) {
    undownloaded.value = Math.max(0, undownloaded.value - 1)
  }
}

/**
 * 失败重导出：解析任务保存的查询条件快照，还原为创建参数按原条件重发
 * （快照 pay_no 为原始入参，网关会重新反查订单号集合）
 * @param t 失败任务行
 */
async function onRetryExport(t: ExportTask) {
  retryingId.value = t.id
  try {
    const cond = (JSON.parse(t.query_cond || '{}') ?? {}) as Record<string, unknown>
    const s = (v: unknown) => (typeof v === 'string' ? v : undefined) || undefined
    const n = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : undefined)
    await createOrderExportTask({
      order_no: s(cond.order_no),
      status: n(cond.status),
      start_time: s(cond.start_time),
      end_time: s(cond.end_time),
      receiver_name: s(cond.receiver_name),
      receiver_phone: s(cond.receiver_phone),
      user_id: n(cond.user_id),
      product_id: n(cond.product_id),
      shift_id: n(cond.shift_id),
      source: n(cond.source),
      pay_no: s(cond.pay_no),
      query_desc: t.query_desc,
    })
    message.success('已按原条件重新发起导出')
    await loadTasks()
    await refreshBadge()
  } catch {
    // 错误提示由请求拦截器统一弹出（含在途任务超限）
  } finally {
    retryingId.value = 0
  }
}

/** 正在重新导出的任务 ID（按钮 loading 用） */
const retryingId = ref(0)

/**
 * 删除任务（仅超管）：确认后删除记录与文件
 * @param t 任务行
 */
function onDelete(t: ExportTask) {
  dialog.warning({
    title: '删除导出任务',
    content: `确定删除「${t.file_name}」吗？文件将被一并删除，不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      // 删除失败时弹窗保持打开（异常提示由请求拦截器统一弹出），避免误以为已删除
      try {
        await deleteOrderExportTask(t.id)
        message.success('已删除')
        await loadTasks()
        await refreshBadge()
      } catch {
        // 返回 false 阻止 naive-ui 关闭弹窗；错误提示由请求拦截器统一弹出
        return false
      }
    },
  })
}

/**
 * 重启轮询（面板开合或 WS 在线状态变化后按新周期）
 */
function restartPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(() => {
    // 打开时刷新列表+角标；关闭时仅角标（有在途任务时也拉角标以便完成后亮标）
    if (visible.value) {
      void loadTasks()
    } else {
      void refreshBadge()
    }
  }, pollMs.value)
}

// 轮询周期随面板开合 / WS 通断变化重启（自适应频率的唯一收口点）
watch(pollMs, restartPolling)

/**
 * 导出任务终态事件：本人为创建人的任务完成/失败即刻刷新，不等轮询
 * 服务端已按 admin_id 定向下发，此处再核一次只为「同账号多连接」下的自校验（成本为零）
 * @param data 事件载荷（ExportTaskChangedInfo）
 */
function onExportTaskChanged(data: ExportTaskChangedInfo) {
  if (!data || data.admin_id !== authStore.admin?.id) return
  if (visible.value) {
    void loadTasks()
  } else {
    void refreshBadge()
  }
}

onMounted(() => {
  void refreshBadge()
  restartPolling()
  disposeRealtime = [
    onRealtime('export.task_changed', (data: ExportTaskChangedInfo) => onExportTaskChanged(data)),
    // 连接建立/断开时刷新在线态，pollMs 随之重算并重启轮询（断开即回到 3s 兜底）
    onRealtime('connection', (data: { connected: boolean }) => {
      realtimeUp.value = !!data?.connected
    }),
  ]
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  disposeRealtime.forEach((dispose) => dispose())
  disposeRealtime = []
})
</script>

<style scoped>
.dl-trigger {
  padding: 4px;
}

/* 弹层内容 padding 已由 content-style 清零，间距统一由各分区管理，避免双重边距 */
.dl-panel {
  display: flex;
  flex-direction: column;
}

/* 头部：图标 + 标题 + 记录数（+超管「全部记录」标识），右侧保留期限提示 */
.dl-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid #f0f0f4;
}

.head-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.head-icon {
  color: #18a058;
}

.dl-head .title {
  font-size: 15px;
  font-weight: 600;
}

.head-hint {
  font-size: 12px;
}

/* 空态撑起面板高度，避免弹层塌陷 */
.dl-empty {
  padding: 40px 0;
}

/* 列表：卡片堆叠，最大高度内滚动 */
.task-list {
  max-height: 440px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 16px;
}

.task-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f8f9fb;
  transition: background-color 0.2s ease;
}

.task-row:hover {
  background: #f0f3f7;
}

/* 状态着色：图标底色按状态区分 */
.task-icon {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.task-row.is-done .task-icon {
  color: #18a058;
  background: #e8f7ee;
}

.task-row.is-fail .task-icon {
  color: #d03050;
  background: #fdecee;
}

.task-row.is-running .task-icon,
.task-row.is-pending .task-icon {
  color: #2080f0;
  background: #e8f3ff;
}

.icon-spin {
  animation: dl-spin 1.2s linear infinite;
}

@keyframes dl-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

.task-main {
  min-width: 0;
  flex: 1;
}

/* 第一行：创建人 tag + 文件名 + 状态 tag，同行紧凑排列 */
.task-top {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.owner-tag {
  flex-shrink: 0;
  max-width: 88px;
}

.owner-tag :deep(.n-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

.task-name {
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-top .n-tag {
  flex-shrink: 0;
}

/* 第二行：条件摘要 · 时间 · 大小 · 行数 · 剩余保留时间 */
.task-meta {
  margin-top: 4px;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.meta-text {
  font-size: 12px;
  color: #9096a3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 剩余保留时间：临期警示色（<24h 自动由临期文案覆盖，这里统一弱强调） */
.meta-retention {
  flex-shrink: 0;
  font-size: 12px;
  color: #f0a020;
}

/* 第三行：下载留痕 / 截断提示 */
.task-trace {
  margin-top: 3px;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.trace-text {
  font-size: 12px;
  color: #9096a3;
  white-space: nowrap;
}

.trace-more {
  font-size: 12px;
  color: #2080f0;
  cursor: pointer;
}

.trace-warn {
  font-size: 12px;
  color: #f0a020;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 完整名单 tooltip：逐行「姓名 · 时间」 */
.trace-row {
  font-size: 12px;
  line-height: 20px;
}

.task-ops {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.dl-foot {
  display: flex;
  justify-content: flex-end;
  padding: 4px 16px 12px;
}

/* 在途提示：浅蓝信息条 */
.dl-hint-bar {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin: 0 16px 12px;
  padding: 7px 12px;
  border-radius: 8px;
  background: #e8f3ff;
  color: #2080f0;
  font-size: 12px;
}
</style>
