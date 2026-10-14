<template>
  <n-card title="售后管理">
    <n-space vertical :size="16">
      <n-space align="center">
        <n-select v-model:value="status" :options="statusOptions" placeholder="售后状态" clearable style="width: 140px" @update:value="onSearch" />
        <n-button type="primary" @click="onSearch">查询</n-button>
      </n-space>
      <n-data-table
        :columns="columns"
        :data="list"
        :loading="loading"
        remote
        :row-key="(row: Refund) => row.id"
        :pagination="pagination"
        @update:page="onPageChange"
      />
    </n-space>
  </n-card>

  <!-- 订单详情弹窗（点击订单号查看） -->
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" />

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
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RefundList' })
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, NDataTable, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRefundList, approveRefund, rejectRefund, retryRefund } from '@/api/order'
import OrderDetailModal from './detail.vue'
import type { Refund, RefundItem } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { REFUND_STATUS, refundStatusMap } from '@/constants/order'

/**
 * 售后管理页面（原退款管理）
 * 展示退款申请列表（订单号/类型/明细/金额），支持同意/拒绝操作；
 * 退款中(status=4) 的卡单提供「重试退款」出口；展开行展示审批日志
 */
const dialog = useDialog()
const status = ref<number | null>(null)
const list = ref<Refund[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

const rejectVisible = ref(false)
const rejectSubmitting = ref(false)
const rejectId = ref(0)
const rejectReason = ref('')

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
      const isFull = row.type === 1
      return h(
        NTag,
        { size: 'small', type: isFull ? 'info' : 'warning', bordered: false },
        { default: () => (isFull ? '整单退' : '部分退') },
      )
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
        return h(NButton, { size: 'small', type: 'warning', onClick: () => confirmRetry(row) }, { default: () => '重试退款' })
      }
      return null
    },
  },
]

const actionTextMap: Record<string, string> = {
  approve: '同意退款',
  reject: '拒绝退款',
  retry: '重试退款',
}

/**
 * 渲染展开行的售后商品明细表与审批日志
 * @param row 售后单行
 * @returns 展开内容
 */
function renderExpandItems(row: Refund) {
  const hasItems = row.items && row.items.length > 0
  const hasLogs = row.logs && row.logs.length > 0
  if (!hasItems && !hasLogs) {
    return h('div', { style: 'padding:8px;color:#999' }, '整单退款，含订单全部商品')
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
    blocks.push(h('div', { style: 'padding:4px 0 8px;color:#999;font-size:13px' }, '整单退款，含订单全部商品'))
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
  detailVisible.value = true
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

onMounted(loadData)
</script>
