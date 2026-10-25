<template>
  <n-card title="支付异常">
    <n-space vertical :size="16">
      <!-- 说明：本页是「渠道为准」的兜底出口，回调/对账/人工同步的异常都汇到这里等人工核实 -->
      <n-alert type="info" :bordered="false">
        列表为支付回调、渠道对账与人工同步产生的异常流水。处理前先按订单号进详情核对渠道交易号，
        确认为误报或已另行退款后再「销账」；资金挂账（钱到账但订单已取消）请在详情里走退款。
        <n-text v-if="!canViewRaw" depth="3">当前账号不可查看报文原文（需「查看支付报文」权限，仅超级管理员）。</n-text>
      </n-alert>

      <n-space align="center">
        <n-select v-model:value="handled" :options="handledOptions" style="width: 130px" @update:value="onSearch" />
        <n-checkbox v-model:checked="includeOk" @update:checked="onSearch">显示正常轨迹</n-checkbox>
        <n-button type="primary" @click="onSearch">查询</n-button>
        <n-tag v-if="pending > 0" type="error" size="small" :bordered="false">待处理 {{ pending }}</n-tag>
      </n-space>

      <n-data-table
        :columns="tableColumns"
        :data="list"
        :loading="loading"
        remote
        :row-key="(row: PaymentAnomaly) => row.id"
        :scroll-x="isNarrow ? 640 : 1320"
        :pagination="pagination"
        @update:page="onPageChange"
      />
    </n-space>
  </n-card>

  <!-- 订单详情弹窗（点订单号进入，退款/同步/标记已支付都在详情内完成） -->
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" @refresh="loadData" />

  <!-- 挂账「去退款」：与订单列表同款代客退款弹窗 -->
  <refund-modal v-model:show="refundVisible" :order-id="refundOrderId" @success="loadData" />

  <!-- 销账弹窗：核实依据必填，作为事后追责凭据 -->
  <n-modal v-model:show="handleVisible" preset="card" title="支付异常销账" style="width: 460px">
    <n-form label-placement="left" label-width="90">
      <n-form-item label="核实依据">
        <n-input
          v-model:value="handleRemark"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-count
          placeholder="例如：渠道商户后台核实该笔未收款，属误报，订单已正常取消"
        />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="handleVisible = false">取消</n-button>
        <n-button type="primary" :loading="handleSubmitting" @click="submitHandle">确认销账</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'PaymentAnomalies' })
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { NButton, NSpace, NTag, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getPaymentAnomalies, handlePaymentAnomaly } from '@/api/order'
import type { PaymentAnomaly } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { reopenModal } from '@/utils/modal'
import {
  NOTIFY_HANDLED,
  NOTIFY_RESULT,
  notifyKindText,
  notifyResultTagType,
  notifyResultText,
  payChannelText,
} from '@/constants/order'
import OrderDetailModal from './detail.vue'
import RefundModal from './components/RefundModal.vue'

/**
 * 支付异常页面
 * 汇集回调失败、金额不符、对账放弃、资金挂账等待人工核实的支付流水；
 * 按社区隔离（社区管理员只见本社区，超管见全量含归属未知条目）
 */
const dialog = useDialog()
// 处理状态筛选值与后端一致：1 待处理 2 已处理 0 全部（0 时服务端口径为不筛选）
const handled = ref(1)
const includeOk = ref(false)
const list = ref<PaymentAnomaly[]>([])
const loading = ref(false)
const pending = ref(0)
const canViewRaw = ref(false)
/** 后端生效的社区范围：0=超管全量，此时额外展示社区列帮助定位 */
const communityScope = ref(0)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

// 挂账退款弹窗（与订单列表、详情页共用同一代客退款组件）
const refundVisible = ref(false)
const refundOrderId = ref<number | null>(null)

const handleVisible = ref(false)
const handleSubmitting = ref(false)
const handleId = ref(0)
const handleRemark = ref('')

const handledOptions = [
  { label: '待处理', value: 1 },
  { label: '已处理', value: 2 },
  { label: '全部', value: 0 },
]

/** 需要人工处理的异常结果（其余是正常轨迹，只在勾选「显示正常轨迹」时出现） */
const pendingResults = new Set<number>([
  NOTIFY_RESULT.VERIFY_FAILED,
  NOTIFY_RESULT.AMOUNT_MISMATCH,
  NOTIFY_RESULT.ERROR,
  NOTIFY_RESULT.STUCK,
])

const columns: DataTableColumns<PaymentAnomaly> = [
  {
    type: 'expand',
    renderExpand: (row) => renderRaw(row),
  },
  {
    title: '类别',
    key: 'kind',
    width: 100,
    render: (row) =>
      h(NTag, { size: 'small', type: notifyResultTagType(row.result), bordered: false }, { default: () => notifyKindText(row.kind) }),
  },
  {
    title: '结果',
    key: 'result',
    width: 120,
    render: (row) => notifyResultText(row.result),
  },
  { title: '渠道', key: 'channel', width: 80, render: (row) => payChannelText(row.channel) },
  {
    title: '订单号',
    key: 'order_no',
    width: 190,
    render(row) {
      // 微信回调全加密且验签失败时连支付单号都解析不出，只能留空等人工按报文比对
      if (!row.order_id) {
        return row.order_no || '-'
      }
      return h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => openOrderDetail(row.order_id) },
        { default: () => row.order_no || `#${row.order_id}` },
      )
    },
  },
  { title: '支付单号', key: 'payment_no', width: 190, ellipsis: { tooltip: true }, render: (row) => row.payment_no || '-' },
  {
    title: '社区',
    key: 'community_id',
    width: 90,
    render: (row) => (row.community_id > 0 ? `#${row.community_id}` : '未知'),
  },
  {
    title: '金额',
    key: 'amount',
    width: 100,
    render: (row) => (row.amount > 0 ? `￥${formatAmount(row.amount)}` : '-'),
  },
  { title: '说明', key: 'error_msg', ellipsis: { tooltip: true }, render: (row) => row.error_msg || '-' },
  {
    title: '留痕/依据',
    key: 'detect_remark',
    width: 180,
    ellipsis: { tooltip: true },
    // 两列分开存（B3）：留痕是「谁检出的、当时什么状态」，依据是「谁凭什么销的账」。
    // 合成一列显示，但检出留痕优先——销账之后仍要看得到当初是谁发现的
    render: (row) => row.detect_remark || row.remark || '-',
  },
  { title: '时间', key: 'created_at', width: 175, render: (row) => formatDateTime(row.created_at) },
  {
    title: '状态',
    key: 'handled',
    width: 90,
    render(row) {
      if (row.handled === NOTIFY_HANDLED.HANDLED) {
        return h(NTag, { size: 'small', type: 'default', bordered: false }, { default: () => '已销账' })
      }
      const urgent = pendingResults.has(row.result)
      return h(
        NTag,
        { size: 'small', type: urgent ? 'error' : 'warning', bordered: false },
        { default: () => (urgent ? '待处理' : '待确认') },
      )
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 160,
    fixed: 'right',
    render(row) {
      if (row.handled === NOTIFY_HANDLED.HANDLED) {
        return null
      }
      const buttons = [h(NButton, { size: 'small', onClick: () => openHandle(row) }, { default: () => '销账' })]
      // 资金挂账（钱已到账但订单已取消）只能退款，直接在队列里给入口，省一次找订单。
      // 判据取 result 而非 kind：挂账事实现在有两个检出来源（3 系统 / 8 人工核实，B3），
      // 按 kind 等值判断会让人工检出的那批丢掉「去退款」入口
      if (row.result === NOTIFY_RESULT.STUCK && row.order_id > 0) {
        buttons.push(
          h(NButton, { size: 'small', type: 'error', onClick: () => openRefund(row) }, { default: () => '去退款' }),
        )
      }
      return h(NSpace, { size: 6, wrap: false }, { default: () => buttons })
    },
  },
]

// --- 窄屏适配（<=768px，与全局布局断点一致）---
const isNarrow = ref(window.innerWidth <= 768)

function updateNarrow() {
  isNarrow.value = window.innerWidth <= 768
}

/**
 * 表格列：窄屏隐藏次要列（支付单号/金额/渠道/留痕），社区列仅超管全量时展示
 */
const tableColumns = computed<DataTableColumns<PaymentAnomaly>>(() => {
  const hidden = new Set<string>()
  if (communityScope.value > 0) {
    hidden.add('community_id')
  }
  if (isNarrow.value) {
    hidden.add('payment_no')
    hidden.add('amount')
    hidden.add('channel')
    hidden.add('detect_remark')
  }
  return columns.filter((col) => !hidden.has((col as { key?: string }).key ?? ''))
})

/**
 * 展开行：报文原文与请求头（仅具备 payment:raw:view 权限、且入库时保存了原文的条目可见）
 * @param row 异常流水行
 * @returns 展开内容
 */
function renderRaw(row: PaymentAnomaly) {
  if (!row.can_view_raw) {
    return h('div', { style: 'padding:8px;color:#999;font-size:13px' }, '报文原文需「查看支付报文」权限（仅超级管理员）')
  }
  if (!row.has_raw) {
    return h('div', { style: 'padding:8px;color:#999;font-size:13px' }, '该条目未保存报文原文（成功回调按约定不落原文）')
  }
  const blocks = [
    h('div', { style: 'font-weight:600;margin:6px 0;font-size:13px' }, '报文原文'),
    h('pre', { style: RAW_PRE_STYLE }, row.raw_body || '（空）'),
  ]
  if (row.headers) {
    blocks.push(h('div', { style: 'font-weight:600;margin:10px 0 6px;font-size:13px' }, '请求头（签名信息已掩码）'))
    blocks.push(h('pre', { style: RAW_PRE_STYLE }, row.headers))
  }
  return h('div', { style: 'padding:4px 8px' }, blocks)
}

/** 报文原文样式：限高滚动、保留换行，避免撑爆表格 */
const RAW_PRE_STYLE =
  'max-height:260px;overflow:auto;margin:0;padding:8px;font-size:12px;line-height:1.5;' +
  'white-space:pre-wrap;word-break:break-all;background:#f7f7f9;border-radius:4px'

/**
 * 点击订单号打开订单详情弹窗
 * @param orderId 订单 ID
 */
function openOrderDetail(orderId: number) {
  detailId.value = orderId
  reopenModal(detailVisible)
}

/**
 * 打开销账弹窗
 * @param row 异常流水行
 */
function openHandle(row: PaymentAnomaly) {
  handleId.value = row.id
  handleRemark.value = ''
  handleVisible.value = true
}

/**
 * 从挂账条目直接发起代客退款
 * @param row 异常流水行（kind=资金挂账且已定位到订单）
 */
function openRefund(row: PaymentAnomaly) {
  refundOrderId.value = row.order_id
  reopenModal(refundVisible)
}

/**
 * 提交销账：核实依据必填，销账后条目从待处理队列消失
 */
async function submitHandle() {
  if (!handleRemark.value.trim()) {
    dialog.warning({ title: '提示', content: '请填写核实依据', positiveText: '知道了' })
    return
  }
  handleSubmitting.value = true
  try {
    await handlePaymentAnomaly(handleId.value, handleRemark.value.trim())
    handleVisible.value = false
    loadData()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    handleSubmitting.value = false
  }
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
 * 加载异常流水分页数据
 */
async function loadData() {
  loading.value = true
  try {
    const res = await getPaymentAnomalies({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      // 1 待处理 / 2 已处理 / 0 全部（0 即服务端口径的「不筛选」）
      handled: handled.value,
      include_ok: includeOk.value || undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
      pending.value = res.data.data.pending
      canViewRaw.value = res.data.data.can_view_raw
      communityScope.value = res.data.data.community_scope
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadData()
  window.addEventListener('resize', updateNarrow)
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
})
</script>
