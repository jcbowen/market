<template>
  <n-card title="售后管理">
    <n-space vertical :size="16">
      <n-space align="center">
        <n-select v-model:value="status" :options="statusOptions" placeholder="售后状态" clearable style="width: 140px" @update:value="onSearch" />
        <n-button type="primary" @click="onSearch">查询</n-button>
      </n-space>
      <n-data-table
        :columns="tableColumns"
        :data="list"
        :loading="loading"
        remote
        :row-key="(row: Refund) => row.id"
        :scroll-x="isNarrow ? 720 : 1300"
        :pagination="pagination"
        @update:page="onPageChange"
      />
    </n-space>
  </n-card>

  <!-- 订单详情弹窗（点击订单号查看；用户点击经 view-user 转发到会员订单弹窗） -->
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" @view-user="openUserOrdersOf" />

  <!-- 会员订单弹窗（详情内用户点击打开；订单号点击经 open-order 转发到详情弹窗） -->
  <user-orders-modal v-model:show="userOrdersVisible" :user="userOrdersUser" @open-order="openOrderDetail" />

  <!-- 拒绝原因弹窗 -->
  <n-modal v-model:show="rejectVisible" preset="card" title="拒绝售后" style="width: 460px">
    <n-form label-placement="left" label-width="80">
      <n-form-item label="拒绝原因">
        <n-input
          v-model:value="rejectReason"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-count
          placeholder="请填写拒绝原因"
        />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="rejectVisible = false">取消</n-button>
        <n-button type="error" :loading="rejectSubmitting" @click="submitReject">确认拒绝</n-button>
      </n-space>
    </template>
  </n-modal>

  <!-- 恢复自动收敛弹窗：核实依据必填，落 refund_logs 供审计 -->
  <n-modal v-model:show="resetVisible" preset="card" title="恢复自动收敛" style="width: 460px">
    <n-form label-placement="left" label-width="80">
      <n-form-item label="核实依据">
        <n-input
          v-model:value="resetRemark"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-count
          placeholder="请填写渠道侧看到的退款状态，如：微信商户平台显示退款处理中"
        />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="resetVisible = false">取消</n-button>
        <n-button type="warning" :loading="resetSubmitting" @click="submitReset">确认恢复</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RefundList' })
import { ref, onActivated, onMounted, onUnmounted, computed, h } from 'vue'
import { useRoute } from 'vue-router'
import { NButton, NSpace, NTag, NDataTable, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRefundList, approveRefund, rejectRefund, retryRefund, resetRefundRetry } from '@/api/order'
import OrderDetailModal from './detail.vue'
import UserOrdersModal from '@/views/user/components/UserOrdersModal.vue'
import type { Refund, RefundItem } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { reopenModal } from '@/utils/modal'
import { REFUND_STATUS, refundStatusMap } from '@/constants/order'

/**
 * 售后管理页面（原退款管理）
 * 展示退款申请列表（订单号/类型/明细/金额），支持同意/拒绝操作；
 * 退款中(status=4) 的卡单提供两个出口：「重试退款」再次向渠道出款，
 * 「恢复自动收敛」只归零两侧重试预算让系统重新核实渠道结果（不碰钱）；展开行展示审批日志
 */
const dialog = useDialog()
const route = useRoute()
const status = ref<number | null>(null)
const list = ref<Refund[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

// 会员订单弹窗（订单详情内用户点击打开）
const userOrdersVisible = ref(false)
const userOrdersUser = ref<{ id: number; nickname?: string } | null>(null)

/**
 * 打开会员订单弹窗（含订单详情弹窗 view-user 事件转发）
 * @param user 会员 ID 与昵称
 */
function openUserOrdersOf(user: { id: number; nickname?: string }) {
  userOrdersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(userOrdersVisible)
}

const rejectVisible = ref(false)
const rejectSubmitting = ref(false)
const rejectId = ref(0)
const rejectReason = ref('')

// 恢复自动收敛弹窗（所有退款中卡单均可用：新卡单归零等效恢复，用满预算的单借此重新起扫）
const resetVisible = ref(false)
const resetSubmitting = ref(false)
const resetId = ref(0)
const resetRemark = ref('')

const statusOptions = [
  { label: '待审核', value: 1 },
  { label: '已同意', value: 2 },
  { label: '已拒绝', value: 3 },
  { label: '退款中', value: 4 },
  { label: '已退款', value: 5 },
]

const itemColumns: DataTableColumns<RefundItem> = [
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'spec_name', width: 120 },
  { title: '退货数量', key: 'quantity', width: 90 },
  {
    title: '应退金额',
    key: 'refund_amount',
    width: 110,
    render: (row) => `￥${(row.refund_amount ?? 0).toFixed(2)}`,
  },
]

const columns: DataTableColumns<Refund> = [
  { type: 'expand', renderExpand: (row) => renderExpandItems(row) },
  { title: '退款单号', key: 'refund_no', width: 190 },
  {
    title: '订单号',
    key: 'order_no',
    width: 190,
    render(row) {
      return h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => openOrderDetail(row.order_id) },
        { default: () => row.order_no || `#${row.order_id}` },
      )
    },
  },
  {
    title: '类型',
    key: 'type',
    width: 90,
    render(row) {
      // 1 整单退 2 部分退 3 仅金额退（全额退完时服务端自动升级为整单退）
      const meta: Record<number, { text: string; tag: 'info' | 'warning' | 'success' }> = {
        1: { text: '整单退', tag: 'info' },
        2: { text: '部分退', tag: 'warning' },
        3: { text: '金额退', tag: 'success' },
      }
      const m = meta[row.type] ?? { text: '部分退', tag: 'warning' }
      return h(NTag, { size: 'small', type: m.tag, bordered: false }, { default: () => m.text })
    },
  },
  {
    title: '退款金额',
    key: 'amount',
    width: 130,
    render(row) {
      const text = `￥${(row.amount ?? 0).toFixed(2)}`
      if (row.freight_amount > 0) {
        return h('span', null, [text, h('span', { style: 'color:#999;font-size:12px;margin-left:4px' }, `（含运费￥${row.freight_amount.toFixed(2)}）`)])
      }
      return text
    },
  },
  {
    title: '原因',
    key: 'reason',
    ellipsis: { tooltip: true },
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      const item = refundStatusMap[row.status]
      return h(NTag, { size: 'small', type: item?.type || 'default', bordered: false }, { default: () => item?.text || '未知' })
    },
  },
  {
    title: '拒绝原因',
    key: 'reject_reason',
    width: 140,
    ellipsis: { tooltip: true },
    render: (row) => (row.status === 3 ? row.reject_reason || '-' : '-'),
  },
  { title: '申请时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 180,
    fixed: 'right',
    render(row) {
      if (row.status === REFUND_STATUS.PENDING) {
        return h(NSpace, { size: 6, wrap: false }, {
          default: () => [
            h(NButton, { size: 'small', type: 'primary', onClick: () => confirmApprove(row) }, { default: () => '同意' }),
            h(NButton, { size: 'small', type: 'error', onClick: () => openReject(row) }, { default: () => '拒绝' }),
          ],
        })
      }
      // 退款中卡单：提供重试出口，避免只能改数据库
      if (row.status === REFUND_STATUS.PROCESSING) {
        return h(NSpace, { size: 8 }, {
          default: () => [
            h(NButton, { size: 'small', type: 'warning', onClick: () => confirmRetry(row) }, { default: () => '重试退款' }),
            h(NButton, { size: 'small', onClick: () => openReset(row) }, { default: () => '恢复自动收敛' }),
          ],
        })
      }
      return null
    },
  },
]

const actionTextMap: Record<string, string> = {
  create: '后台代客退款',
  approve: '同意退款',
  reject: '拒绝退款',
  retry: '重试退款',
  // 卡单自动重试用满预算（调度器写入）：备注里给了人工处置路径，列表展开行原样展示
  give_up: '停止自动重试（转人工）',
  // 人工核实渠道后恢复自动收敛（不再次触渠道出款）
  reset: '恢复自动收敛',
}

// --- 窄屏适配（<=768px，与全局布局断点一致）---
const isNarrow = ref(window.innerWidth <= 768)

function updateNarrow() {
  isNarrow.value = window.innerWidth <= 768
}

/**
 * 表格列：窄屏隐藏次要列（退款单号/拒绝原因/申请时间），保留关键列与操作列
 */
const tableColumns = computed<DataTableColumns<Refund>>(() => {
  if (!isNarrow.value) return columns
  const hiddenKeys = new Set(['refund_no', 'reject_reason', 'created_at'])
  return columns.filter((col) => !hiddenKeys.has((col as { key?: string }).key ?? ''))
})

/**
 * 渲染展开行的售后商品明细表与审批日志
 * @param row 售后单行
 * @returns 展开内容
 */
function renderExpandItems(row: Refund) {
  const hasItems = row.items && row.items.length > 0
  const hasLogs = row.logs && row.logs.length > 0
  const emptyText = row.type === 3 ? '仅金额退款，不退商品' : '整单退款，含订单全部商品'
  if (!hasItems && !hasLogs) {
    return h('div', { style: 'padding:8px;color:#999' }, emptyText)
  }
  const blocks: ReturnType<typeof h>[] = []
  if (hasItems) {
    blocks.push(
      h('div', { style: 'font-weight:600;margin:6px 0;font-size:13px' }, '退款商品明细'),
      h(NDataTable, {
        columns: itemColumns,
        data: row.items,
        size: 'small',
        bordered: true,
        singleLine: false,
      }),
    )
  } else {
    blocks.push(h('div', { style: 'padding:4px 0 8px;color:#999;font-size:13px' }, emptyText))
  }
  if (hasLogs) {
    blocks.push(
      h('div', { style: 'font-weight:600;margin:10px 0 6px;font-size:13px' }, '审批日志'),
      h(
        'div',
        { style: 'display:flex;flex-direction:column;gap:4px;font-size:13px' },
        (row.logs ?? []).map((log) =>
          h('div', { style: 'color:#666' }, [
            `${formatDateTime(log.created_at)}　${actionTextMap[log.action] ?? log.action}`,
            `（${log.operator_type}${log.operator_id ? ' #' + log.operator_id : ''}）`,
            log.remark ? `　${log.remark}` : '',
          ]),
        ),
      ),
    )
  }
  return h('div', { style: 'padding:4px 8px' }, blocks)
}

/**
 * 点击订单号打开订单详情弹窗
 * @param orderId 订单 ID
 */
function openOrderDetail(orderId: number) {
  detailId.value = orderId
  reopenModal(detailVisible)
}

/**
 * 点击查询回到第 1 页并刷新
 */
function onSearch() {
  pagination.value.page = 1
  loadData()
}

/**
 * 分页切换
 * @param page 目标页码
 */
function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

/**
 * 同意退款（二次确认）
 * @param row 售后单行
 */
function confirmApprove(row: Refund) {
  dialog.warning({
    title: '同意退款',
    content: `确认同意售后单 ${row.refund_no} 的退款申请？同意后将按订单原支付流水退回 ￥${(row.amount ?? 0).toFixed(2)}。`,
    positiveText: '确认同意',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await approveRefund(row.id)
        loadData()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 重试退款（退款中卡单）：以退款单记录的支付单号重新发起渠道退款
 * @param row 售后单行
 */
function confirmRetry(row: Refund) {
  dialog.warning({
    title: '重试退款',
    content: `确认对退款单 ${row.refund_no} 重新发起渠道退款？金额 ￥${(row.amount ?? 0).toFixed(2)}。`,
    positiveText: '确认重试',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await retryRefund(row.id)
        loadData()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 打开拒绝原因弹窗
 * @param row 售后单行
 */
function openReject(row: Refund) {
  rejectId.value = row.id
  rejectReason.value = ''
  rejectVisible.value = true
}

/**
 * 提交拒绝售后
 */
async function submitReject() {
  if (!rejectReason.value.trim()) {
    dialog.error({ title: '提示', content: '请填写拒绝原因', positiveText: '知道了' })
    return
  }
  rejectSubmitting.value = true
  try {
    await rejectRefund(rejectId.value, rejectReason.value.trim())
    rejectVisible.value = false
    loadData()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    rejectSubmitting.value = false
  }
}

/**
 * 打开恢复自动收敛弹窗
 * @param row 售后单行
 */
function openReset(row: Refund) {
  resetId.value = row.id
  resetRemark.value = ''
  resetVisible.value = true
}

/**
 * 提交恢复自动收敛
 * 后端会把支付流水与退款单两侧的重试预算归零，调度器下一轮重新核实渠道终态；
 * 本动作不向渠道重新发起出款，因此可与「重试退款」先后使用而不冲突
 */
async function submitReset() {
  if (!resetRemark.value.trim()) {
    dialog.error({ title: '提示', content: '请填写渠道核实依据', positiveText: '知道了' })
    return
  }
  resetSubmitting.value = true
  try {
    await resetRefundRetry(resetId.value, resetRemark.value.trim())
    resetVisible.value = false
    loadData()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    resetSubmitting.value = false
  }
}

/**
 * 加载售后单分页数据
 */
async function loadData() {
  loading.value = true
  try {
    const res = await getRefundList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      status: status.value || undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

/**
 * 应用路由 query 中的 status 预筛选（工作台待办直达入口）
 * 仅接受本页状态筛选支持的值（1-5）；无参数或非法值时回到全部
 * @returns 筛选是否发生变化（变化时调用方需重新加载列表）
 */
function applyQueryStatus(): boolean {
  const raw = Number(route.query.status)
  const allowed = new Set(statusOptions.map((s) => s.value))
  const value: number | null = Number.isInteger(raw) && allowed.has(raw) ? raw : null
  if (value === status.value) return false
  status.value = value
  pagination.value.page = 1
  return true
}

onMounted(() => {
  applyQueryStatus()
  loadData()
  window.addEventListener('resize', updateNarrow)
})

// keep-alive 缓存下再次进入（如工作台待办跳转）时，应用新的 status 查询参数重新筛选
onActivated(() => {
  if (applyQueryStatus()) {
    loadData()
  }
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
})
</script>
