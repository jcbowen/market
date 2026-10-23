<template>
  <n-modal
    :show="show"
    preset="card"
    title="订单详情"
    class="order-detail-modal"
    style="width: min(880px, calc(100vw - 48px)); max-height: calc(100vh - 48px)"
    @update:show="onUpdateShow"
  >
    <n-spin :show="loading">
      <n-empty v-if="!detail && !loading" description="订单不存在" />
      <n-space v-else-if="detail" vertical :size="16">
        <!-- 用户已从自己的订单列表删除（软删）：后台仍全量可见，提供恢复入口 -->
        <n-alert v-if="detail.user_deleted === 1" type="warning" title="该订单已被用户从订单列表删除">
          <template #action>
            <n-button size="small" type="warning" :loading="restoring" @click="confirmRestore">恢复</n-button>
          </template>
          软删只影响用户侧可见性，订单状态、资金与售后记录均未变动。恢复后订单会重新出现在用户的「我的订单」里。
        </n-alert>
        <!-- 描述列表：窄屏自动降为单列，避免两列内容挤压换行难读 -->
        <n-descriptions label-placement="left" bordered :column="descColumn" size="small">
          <n-descriptions-item label="订单号">{{ detail.order_no }}</n-descriptions-item>
          <n-descriptions-item label="用户">
            <!-- 关联会员（user_id>0）可点击查看该会员订单（事件上抛页面层打开会员订单弹窗） -->
            <n-button
              v-if="detail.user_id > 0"
              size="tiny"
              text
              type="primary"
              @click="emitViewUser"
            >
              {{ detail.user_name || `用户${detail.user_id}` }}
            </n-button>
            <template v-else>{{ detail.user_name || '-' }}</template>
          </n-descriptions-item>
          <n-descriptions-item label="状态">
            <n-tag :type="orderStatusTagType(detail.status)" size="small">{{ detail.status_text }}</n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="配送方式">{{ deliveryTypeText(detail.delivery_type) }}</n-descriptions-item>
          <n-descriptions-item label="来源">
            <n-tag :type="orderSourceTagType(detail.source ?? ORDER_SOURCE.USER)" size="small" :bordered="false">
              {{ orderSourceText(detail.source ?? ORDER_SOURCE.USER) }}
            </n-tag>
          </n-descriptions-item>
          <n-descriptions-item v-if="detail.operator_name" label="经手人">{{ detail.operator_name }}</n-descriptions-item>
          <n-descriptions-item label="支付方式">{{ detail.pay_type_text || (detail.pay_type ? payTypeText(detail.pay_type) : '-') }}</n-descriptions-item>
          <n-descriptions-item label="订单金额">￥{{ formatAmount(detail.total_amount) }}</n-descriptions-item>
          <n-descriptions-item label="优惠金额">-￥{{ formatAmount(detail.discount_amount) }}</n-descriptions-item>
          <n-descriptions-item label="运费">￥{{ formatAmount(detail.delivery_fee) }}</n-descriptions-item>
          <n-descriptions-item label="实付金额">
            <span class="amount-strong">￥{{ formatAmount(detail.pay_amount) }}</span>
          </n-descriptions-item>
          <n-descriptions-item label="期望送达">{{ formatDateTime(detail.delivery_time) }}</n-descriptions-item>
          <n-descriptions-item label="下单时间">{{ formatDateTime(detail.created_at) }}</n-descriptions-item>
          <n-descriptions-item v-if="detail.express_company || detail.express_no" label="物流信息" :span="2">
            {{ detail.express_company }}　{{ detail.express_no }}
          </n-descriptions-item>
          <n-descriptions-item label="收货信息" :span="2">
            <template v-if="detail.address && detail.address.name">
              {{ detail.address.name }} {{ detail.address.phone }} {{ detail.address.address }}
            </template>
            <template v-else>-</template>
          </n-descriptions-item>
          <n-descriptions-item v-if="detail.remark" label="订单备注" :span="2">{{ detail.remark }}</n-descriptions-item>
          <n-descriptions-item v-if="detail.cancel_reason" label="取消原因" :span="2">
            {{ detail.cancel_reason }}
          </n-descriptions-item>
        </n-descriptions>

        <!-- 商品明细：固定列宽,窄屏横向滚动 -->
        <n-data-table :columns="itemColumns" :data="detail.items" size="small" :bordered="true" :scroll-x="620" />

        <!-- 支付记录：本单每次支付尝试（次数/金额/状态/渠道交易号/确认来源） -->
        <div>
          <div class="section-title">支付记录</div>
          <n-space vertical :size="8">
            <!-- 重复支付必须退款，这里给出合计方便核对 -->
            <n-alert v-if="isDuplicatePayment(detail.payment_summary)" type="error" :bordered="false">
              本单有 {{ detail.payment_summary?.succeeded }} 笔成功支付，合计
              ￥{{ formatAmount(detail.payment_summary?.succeeded_amount ?? 0) }}，
              超出实付金额的部分请在下方「退款」中处理。
            </n-alert>
            <!-- 待支付在线单：回调可能丢失，提示可用「同步支付状态」向渠道核实 -->
            <n-alert v-else-if="isWaitingOnlinePayment" type="warning" :bordered="false">
              订单仍处待支付且已有支付单。若顾客反馈已付款，可点「同步支付状态」直接向渠道核实，不必等回调。
            </n-alert>
            <n-text v-if="detail.payments_error" type="warning">支付记录暂时查不到：{{ detail.payments_error }}</n-text>
            <n-empty v-else-if="!detail.payments || !detail.payments.length" description="本单暂无支付记录" size="small" />
            <n-data-table
              v-else
              :columns="paymentColumns"
              :data="detail.payments"
              size="small"
              :bordered="true"
              :scroll-x="1040"
            />
          </n-space>
        </div>

        <div v-if="detail.status_logs && detail.status_logs.length">
          <div class="section-title">状态流转</div>
          <n-data-table :columns="logColumns" :data="detail.status_logs" size="small" :scroll-x="620" />
        </div>

        <!-- 售后记录：本单每次退款（类型/金额/状态/明细），待审核行内可直接同意/拒绝 -->
        <div v-if="detail.refunds && detail.refunds.length">
          <div class="section-title">售后记录</div>
          <n-data-table :columns="refundColumns" :data="detail.refunds" size="small" :scroll-x="730" />
        </div>

        <!-- 支付通知轨迹：回调/对账/人工同步的全轨迹，用于判断「到底有没有回调进来过」 -->
        <div v-if="detail.payment_logs && detail.payment_logs.length">
          <div class="section-title">支付通知轨迹</div>
          <n-timeline>
            <n-timeline-item
              v-for="log in detail.payment_logs"
              :key="log.id"
              :type="notifyResultTagType(log.result)"
              :title="`${notifyKindText(log.kind)} · ${notifyResultText(log.result)}`"
              :time="formatDateTime(log.created_at)"
            >
              <n-space vertical :size="4">
                <n-text depth="3">
                  {{ payChannelText(log.channel) }}　{{ log.payment_no || '未解析出支付单号' }}
                  <template v-if="log.amount > 0">　￥{{ formatAmount(log.amount) }}</template>
                </n-text>
                <n-text v-if="log.error_msg">{{ log.error_msg }}</n-text>
                <!-- 报文原文只对有 payment:raw:view 权限（实际仅超管）且入库时保存了原文的条目可见 -->
                <n-button
                  v-if="log.can_view_raw && log.has_raw"
                  size="tiny"
                  text
                  type="primary"
                  @click="toggleRaw(log.id)"
                >
                  {{ rawOpenIds.has(log.id) ? '收起原文' : '查看报文原文' }}
                </n-button>
                <pre v-if="rawOpenIds.has(log.id)" class="raw-body">{{ log.raw_body }}</pre>
              </n-space>
            </n-timeline-item>
          </n-timeline>
        </div>
        <n-text v-else-if="detail.payment_logs_error" type="warning">
          通知轨迹暂时查不到：{{ detail.payment_logs_error }}
        </n-text>

        <!-- 操作区：按当前状态渲染与列表一致的操作按钮 -->
        <n-space v-if="actions.length" justify="end">
          <n-button
            v-for="action in actions"
            :key="action.key"
            size="small"
            :type="action.type"
            :loading="action.loading"
            @click="action.run()"
          >
            {{ action.label }}
          </n-button>
        </n-space>
      </n-space>
    </n-spin>
  </n-modal>

  <!-- 发货 / 取消订单 / 代客退款弹窗（与列表页共用） -->
  <ship-modal v-model:show="shipVisible" :order-ids="shipOrderIds" @success="onActionSuccess" />
  <cancel-order-modal v-model:show="cancelVisible" :order-id="cancelOrderId" @success="onActionSuccess" />
  <refund-modal v-model:show="refundVisible" :order-id="refundOrderId" @success="onActionSuccess" />

  <!-- 人工标记已支付（渠道查不到但线下已核实到账的死角，四个字段均必填留痕） -->
  <mark-paid-modal
    v-model:show="markPaidVisible"
    :order-id="detail?.id ?? null"
    :payments="detail?.payments"
    @success="onActionSuccess"
  />

  <!-- 自提核销弹窗（凭取货码核销，防冒领） -->
  <n-modal
    :show="showVerifyModal"
    preset="dialog"
    title="自提核销"
    positive-text="确认核销"
    negative-text="取消"
    :loading="verifyLoading"
    @update:show="(v: boolean) => (showVerifyModal = v)"
    @positive-click="submitVerify"
  >
    <n-space vertical>
      <n-text>订单号：{{ detail?.order_no }}</n-text>
      <n-text depth="3">请输入顾客出示的取货码（订单详情页展示的 8 位数字码）</n-text>
      <n-input
        v-model:value="verifyCode"
        placeholder="8 位取货码"
        maxlength="8"
        :input-props="{ inputmode: 'numeric' }"
        @keyup.enter="submitVerify"
      />
    </n-space>
  </n-modal>

  <!-- 售后拒绝原因弹窗（售后记录行内「拒绝」入口，原因必填留痕并同步给用户） -->
  <n-modal
    :show="auditRejectVisible"
    preset="dialog"
    title="拒绝售后"
    positive-text="确认拒绝"
    negative-text="取消"
    :loading="auditRejectSubmitting"
    @update:show="(v: boolean) => (auditRejectVisible = v)"
    @positive-click="submitAuditReject"
  >
    <n-space vertical>
      <n-text depth="3">拒绝后订单回退到退款前状态，拒绝原因将展示给用户</n-text>
      <n-input
        v-model:value="auditRejectReason"
        type="textarea"
        placeholder="请填写拒绝原因（必填）"
        maxlength="200"
        show-count
      />
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面级组件：订单详情弹窗，供订单列表 / 售后管理复用
// 底部按当前状态渲染操作按钮（发货/核销/完成/取消），操作后刷新详情并通知父级刷新列表
defineOptions({ name: 'OrderDetailModal' })
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { approveRefund, completeOrder, getOrderDetail, rejectRefund, restoreOrder, syncOrderPayment, verifyPickup } from '@/api/order'
import type { OrderDetail, OrderItem, OrderPayment, OrderStatusLog, Refund } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import {
  DELIVERY_TYPE,
  ORDER_SOURCE,
  ORDER_STATUS,
  PAY_TYPE,
  REFUND_STATUS,
  canAdminRefund,
  canCancelOrder,
  deliveryTypeText,
  isDuplicatePayment,
  notifyKindText,
  notifyResultTagType,
  notifyResultText,
  orderSourceTagType,
  orderSourceText,
  orderStatusTagType,
  orderStatusText,
  payChannelText,
  payTypeText,
  paymentConfirmRefText,
  paymentConfirmSourceTagType,
  paymentConfirmSourceText,
  paymentStatusTagType,
  paymentStatusText,
  refundStatusTagType,
  refundStatusText,
  refundTypeTagType,
  refundTypeText,
} from '@/constants/order'
import ShipModal from './components/ShipModal.vue'
import CancelOrderModal from './components/CancelOrderModal.vue'
import RefundModal from './components/RefundModal.vue'
import MarkPaidModal from './components/MarkPaidModal.vue'

/**
 * 订单详情弹窗组件
 * 通过弹窗加载订单基本信息、商品明细与状态流转日志
 */
const props = defineProps<{
  /** 弹窗显示状态（v-model:show） */
  show: boolean
  /** 订单 ID，弹窗打开时加载详情 */
  orderId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'refresh'): void
  /** 点击用户字段：请求页面层打开会员订单弹窗（弹窗间不互相嵌套，避免组件递归） */
  (e: 'view-user', user: { id: number; nickname?: string }): void
}>()

const dialog = useDialog()
const message = useMessage()
const loading = ref(false)
const detail = ref<OrderDetail | null>(null)

const shipVisible = ref(false)
const shipOrderIds = ref<number[]>([])
const cancelVisible = ref(false)
const cancelOrderId = ref<number | null>(null)
const refundVisible = ref(false)
const refundOrderId = ref<number | null>(null)
const markPaidVisible = ref(false)
const syncingPayment = ref(false)
const verifyingStuck = ref(false)
/** 恢复被用户删除的订单：进行中时按钮转圈，避免重复提交 */
const restoring = ref(false)

/**
 * 用户字段点击：上抛 view-user 事件，由页面层打开会员订单弹窗
 */
function emitViewUser() {
  const d = detail.value
  if (!d || d.user_id <= 0) return
  emit('view-user', { id: d.user_id, nickname: d.user_name })
}

// --- 描述列表响应式列数 ---
// 窄屏（<=768px，与全局布局断点一致）降为单列，宽屏保持两列
const isNarrow = ref(window.innerWidth <= 768)

/** 描述列表列数：宽屏 2 列，窄屏 1 列 */
const descColumn = computed(() => (isNarrow.value ? 1 : 2))

/** 窗口尺寸变化时同步窄屏标记 */
function updateNarrow() {
  isNarrow.value = window.innerWidth <= 768
}

const itemColumns: DataTableColumns<OrderItem> = [
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'sku_name', width: 120 },
  { title: '单价', key: 'price', width: 100, render: (row) => `￥${formatAmount(row.price)}` },
  { title: '数量', key: 'quantity', width: 70 },
  { title: '小计', key: 'total_amount', width: 110, render: (row) => `￥${formatAmount(row.total_amount)}` },
]

const logColumns: DataTableColumns<OrderStatusLog> = [
  {
    title: '流转',
    key: 'transition',
    width: 160,
    render: (row) => `${orderStatusText(row.from_status)} → ${orderStatusText(row.to_status)}`,
  },
  { title: '操作方', key: 'operator_type', width: 90 },
  { title: '备注', key: 'remark' },
  { title: '时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
]

/** 售后记录列：每次退款单一行，展开/摘要展示退款原因与商品明细；待审核行内直接审核（免跳转售后管理页） */
const refundColumns: DataTableColumns<Refund> = [
  {
    title: '类型',
    key: 'type',
    width: 80,
    render: (row) =>
      h(NTag, { size: 'small', type: refundTypeTagType(row.type), bordered: false }, { default: () => refundTypeText(row.type) }),
  },
  {
    title: '退款金额',
    key: 'amount',
    width: 120,
    render: (row) =>
      row.freight_amount > 0
        ? `￥${(row.amount ?? 0).toFixed(2)}（含运费￥${row.freight_amount.toFixed(2)}）`
        : `￥${(row.amount ?? 0).toFixed(2)}`,
  },
  {
    title: '退款商品',
    key: 'items',
    render: (row) =>
      row.items && row.items.length
        ? row.items.map((it) => `${it.product_name}${it.spec_name ? `(${it.spec_name})` : ''}×${it.quantity}`).join('、')
        : '不退商品',
  },
  {
    title: '状态',
    key: 'status',
    width: 80,
    render: (row) =>
      h(NTag, { size: 'small', type: refundStatusTagType(row.status), bordered: false }, { default: () => refundStatusText(row.status) }),
  },
  { title: '原因', key: 'reason', ellipsis: { tooltip: true } },
  { title: '时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.refund_time || row.created_at) },
  {
    // 行内审核：仅待审核可操作，同意/拒绝逻辑与售后管理页一致；审核成功刷新详情并通知父级刷新列表
    title: '操作',
    key: 'audit',
    width: 110,
    render: (row) => {
      if (row.status !== REFUND_STATUS.PENDING) return null
      return h(NSpace, { size: 6, wrap: false }, {
        default: () => [
          h(NButton, { size: 'tiny', type: 'primary', onClick: () => confirmAuditApprove(row) }, { default: () => '同意' }),
          h(NButton, { size: 'tiny', type: 'error', ghost: true, onClick: () => openAuditReject(row) }, { default: () => '拒绝' }),
        ],
      })
    },
  },
]

/** 支付记录列：一次支付尝试一行，确认来源非「渠道回调」即说明回调出过问题 */
const paymentColumns: DataTableColumns<OrderPayment> = [
  { title: '支付单号', key: 'payment_no', width: 190, ellipsis: { tooltip: true } },
  { title: '支付方式', key: 'pay_type', width: 90, render: (row) => payTypeText(row.pay_type) },
  { title: '金额', key: 'amount', width: 100, render: (row) => `￥${formatAmount(row.amount)}` },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      h(NTag, { size: 'small', type: paymentStatusTagType(row.status), bordered: false }, { default: () => paymentStatusText(row.status) }),
  },
  {
    title: '确认来源',
    key: 'confirm_source',
    width: 150,
    // 来源凭据一并展示：说明这条成功是取消前置确认、后台同步还是对账捞回的
    // render() 生成的节点拿不到 scoped 样式作用域，故用内联样式
    render: (row) => {
      const refText = paymentConfirmRefText(row.confirm_ref)
      return h('div', [
        h(
          NTag,
          { size: 'small', type: paymentConfirmSourceTagType(row.confirm_source), bordered: false },
          { default: () => paymentConfirmSourceText(row.confirm_source) },
        ),
        refText ? h('div', { style: 'margin-top:2px;font-size:12px;color:#8b8b8b' }, refText) : null,
      ])
    },
  },
  { title: '渠道交易号', key: 'transaction_id', width: 180, ellipsis: { tooltip: true }, render: (row) => row.transaction_id || '-' },
  { title: '回查', key: 'reconcile_count', width: 70, render: (row) => (row.reconcile_count > 0 ? `${row.reconcile_count} 次` : '-') },
  { title: '创建时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
]

/** 待支付的在线单：回调可能丢失，详情页给出「同步支付状态」入口并提示原因 */
const isWaitingOnlinePayment = computed(() => {
  const d = detail.value
  if (!d || d.status !== ORDER_STATUS.PENDING_PAY) {
    return false
  }
  return d.pay_type === PAY_TYPE.WECHAT || d.pay_type === PAY_TYPE.ALIPAY
})

/**
 * 已取消的渠道支付单：挂账检测扫描只覆盖关闭后 120 分钟内的单，
 * 超窗后靠此入口人工核实渠道是否已收款（已收款则到商户平台按交易号退款）
 */
const isCancelledChannelPayment = computed(() => {
  const d = detail.value
  if (!d || d.status !== ORDER_STATUS.CANCELLED) {
    return false
  }
  const t = d.pay_type
  return t === PAY_TYPE.WECHAT || t === PAY_TYPE.ALIPAY || t === PAY_TYPE.WECHAT_CODE || t === PAY_TYPE.ALIPAY_CODE
})

// --- 通知报文原文按需展开（原文只在有 payment:raw:view 权限时下发，实际仅超管） ---
const rawOpenIds = ref(new Set<number>())

/**
 * 展开/收起一条通知流水的报文原文
 * @param id 流水 ID
 */
function toggleRaw(id: number) {
  const next = new Set(rawOpenIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  rawOpenIds.value = next
}

/** 操作按钮项：label 展示文案，run 执行动作，loading 让同步中的按钮自带上转圈态 */
type OrderAction = {
  key: string
  label: string
  type: 'default' | 'primary' | 'success' | 'warning' | 'error'
  loading?: boolean
  run: () => void
}

/**
 * 按当前订单状态生成可用操作按钮
 */
const actions = computed<OrderAction[]>(() => {
  const d = detail.value
  if (!d) return []
  const list: OrderAction[] = []
  // 取消订单：待支付恒可取消；货到付款单未发货（已支付/备货中）也可直接取消（与后端口径一致）
  if (canCancelOrder(d)) {
    list.push({ key: 'cancel', label: '取消订单', type: 'error', run: () => openCancel(d.id) })
  }
  // 待支付在线单：先向渠道核实一次，比等回调可靠；渠道查不到再走人工标记
  if (isWaitingOnlinePayment.value) {
    list.push({ key: 'sync', label: '同步支付状态', type: 'default', loading: syncingPayment.value, run: () => syncPayment(d) })
    list.push({ key: 'mark-paid', label: '标记已支付', type: 'default', run: () => (markPaidVisible.value = true) })
  }
  // 已取消的渠道支付单：挂账核实——渠道已收款则产出退款待办，未收款则消除疑虑
  if (isCancelledChannelPayment.value) {
    list.push({ key: 'verify-stuck', label: '渠道核实挂账', type: 'warning', loading: verifyingStuck.value, run: () => verifyStuckPayment(d) })
  }
  if (d.status === ORDER_STATUS.PAID && d.delivery_type === DELIVERY_TYPE.DELIVERY) {
    list.push({ key: 'ship', label: '发货', type: 'primary', run: () => openShip(d.id) })
  }
  if (d.status === ORDER_STATUS.PAID && d.delivery_type === DELIVERY_TYPE.SELF_PICKUP) {
    list.push({ key: 'verify', label: '核销', type: 'success', run: () => openVerify(d) })
  }
  if (d.status === ORDER_STATUS.STOCKING && d.delivery_type === DELIVERY_TYPE.SELF_PICKUP) {
    list.push({ key: 'verify', label: '核销', type: 'success', run: () => openVerify(d) })
  }
  // 到店单即买即走：收款即自动完成，正常不会停留在此；兜底提供"完成"入口（无取货码，不适用核销）
  if ((d.status === ORDER_STATUS.PAID || d.status === ORDER_STATUS.STOCKING) && d.delivery_type === DELIVERY_TYPE.STORE) {
    list.push({ key: 'complete', label: '完成', type: 'success', run: () => confirmComplete(d) })
  }
  if (d.status === ORDER_STATUS.SHIPPING) {
    list.push({ key: 'complete', label: '完成', type: 'success', run: () => confirmComplete(d) })
  }
  // 可退状态口径统一由 canAdminRefund 判定（已支付/备货中/配送中，以及到店单的已完成），与后端一致
  if (canAdminRefund(d)) {
    list.push({ key: 'refund', label: '退款', type: 'error', run: () => openRefund(d.id) })
  }
  return list
})

function openShip(id: number) {
  shipOrderIds.value = [id]
  shipVisible.value = true
}

function openCancel(id: number) {
  cancelOrderId.value = id
  cancelVisible.value = true
}

function openRefund(id: number) {
  refundOrderId.value = id
  refundVisible.value = true
}

// --- 售后行内审核（售后记录操作列，逻辑与售后管理页一致） ---
const auditRejectVisible = ref(false)
const auditRejectId = ref(0)
const auditRejectReason = ref('')
const auditRejectSubmitting = ref(false)

/**
 * 同意退款（二次确认）
 * 同意即按申请金额全额退回原支付流水（不调额：想少退走「拒绝 + 主动退款」组合）
 * @param row 售后单行
 */
function confirmAuditApprove(row: Refund) {
  dialog.warning({
    title: '同意退款',
    content: `确认同意售后单 ${row.refund_no} 的退款申请？同意后将按订单原支付流水退回 ￥${(row.amount ?? 0).toFixed(2)}。`,
    positiveText: '确认同意',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await approveRefund(row.id)
        message.success('已同意退款，正在退回原支付流水')
        onActionSuccess()
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
function openAuditReject(row: Refund) {
  auditRejectId.value = row.id
  auditRejectReason.value = ''
  auditRejectVisible.value = true
}

/**
 * 提交拒绝售后：拒绝后订单回退到退款前状态
 */
async function submitAuditReject() {
  if (!auditRejectReason.value.trim()) {
    dialog.error({ title: '提示', content: '请填写拒绝原因', positiveText: '知道了' })
    return
  }
  auditRejectSubmitting.value = true
  try {
    await rejectRefund(auditRejectId.value, auditRejectReason.value.trim())
    auditRejectVisible.value = false
    message.success('已拒绝该售后申请')
    onActionSuccess()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    auditRejectSubmitting.value = false
  }
}

/**
 * 向渠道同步支付状态：回调丢失时的人工补偿入口，走支付服务的渠道查询
 * 渠道已收款则订单流转为已支付；未收款只回报渠道状态，不改任何数据
 * @param d 订单详情
 */
async function syncPayment(d: OrderDetail) {
  syncingPayment.value = true
  try {
    const res = await syncOrderPayment(d.id)
    const r = res.data.data
    if (r.changed) {
      message.success(`渠道已收款（交易号 ${r.transaction_id}），订单已同步为已支付`)
    } else if (r.paid) {
      message.success('渠道已收款，订单此前已同步为已支付')
    } else {
      message.info(`渠道查询结果：${r.channel_state || '未支付'}${r.message ? `　${r.message}` : ''}`)
    }
    loadDetail(d.id)
    emit('refresh')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    syncingPayment.value = false
  }
}

/**
 * 渠道核实挂账：对已取消订单向渠道核实是否已收款
 * 已收款 → 后台异常页产出人工退款待办（含渠道交易号），提示到商户平台退款；
 * 未收款 → 提示核实结论消除疑虑。本入口只读渠道 + 写流水，不改任何资金状态
 * @param d 订单详情
 */
async function verifyStuckPayment(d: OrderDetail) {
  verifyingStuck.value = true
  try {
    const res = await syncOrderPayment(d.id)
    const r = res.data.data
    if (r.paid) {
      message.warning(
        `渠道已收款（交易号 ${r.transaction_id}，金额 ${r.amount}）而订单已取消，属资金挂账：` +
          `请到商户平台按交易号人工退款，退款完成后在异常页销账`,
      )
    } else if (r.channel_state === 'ERROR') {
      message.error(`渠道查询失败：${r.message || '未能确认收款状态'}`)
    } else {
      message.info(r.message || `渠道确认未收款（${r.channel_state}），无资金挂账`)
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifyingStuck.value = false
  }
}

function confirmComplete(d: OrderDetail) {
  dialog.warning({
    title: '完成订单',
    content: `确认将订单 ${d.order_no} 标记为已完成？`,
    positiveText: '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await completeOrder(d.id)
        loadDetail(d.id)
        emit('refresh')
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

// --- 自提核销（输码校验） ---
const showVerifyModal = ref(false)
const verifyCode = ref('')
const verifyLoading = ref(false)

/**
 * 打开自提核销弹窗
 * @param d 订单详情
 */
function openVerify(d: OrderDetail) {
  verifyCode.value = ''
  showVerifyModal.value = true
}

/**
 * 提交核销：校验取货码后完成订单
 */
async function submitVerify() {
  const d = detail.value
  if (!d) return
  if (!verifyCode.value.trim()) {
    dialog.warning({ title: '提示', content: '请输入顾客出示的取货码', positiveText: '知道了' })
    return
  }
  verifyLoading.value = true
  try {
    await verifyPickup(d.id, verifyCode.value.trim())
    showVerifyModal.value = false
    loadDetail(d.id)
    emit('refresh')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifyLoading.value = false
  }
}

/**
 * 操作成功后的统一刷新
 */
function onActionSuccess() {
  if (detail.value) {
    loadDetail(detail.value.id)
  }
  emit('refresh')
}

/**
 * 关闭弹窗时同步 v-model:show
 * @param value 是否显示
 */
function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

/**
 * 加载订单详情
 * @param id 订单 ID
 */
async function loadDetail(id: number) {
  loading.value = true
  try {
    const res = await getOrderDetail(id)
    if (res.data.code === 0) {
      detail.value = res.data.data
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

/**
 * 恢复被用户软删除的订单
 * 只改用户侧可见性（订单状态、资金与售后记录一律不动），成功后就地更新弹窗状态并通知列表刷新
 */
function confirmRestore() {
  const order = detail.value
  if (!order) return
  dialog.warning({
    title: '恢复订单',
    content: `确认将订单 ${order.order_no} 恢复到用户的订单列表？该操作只改可见性，不影响订单状态与资金。`,
    positiveText: '确认恢复',
    negativeText: '取消',
    onPositiveClick: async () => {
      restoring.value = true
      try {
        await restoreOrder(order.id)
        message.success('订单已恢复')
        detail.value = { ...order, user_deleted: 0 }
        emit('refresh')
      } catch {
        // 错误提示由请求拦截器统一弹出
      } finally {
        restoring.value = false
      }
    },
  })
}

/**
 * 金额格式化（保留两位小数）
 * @param value 金额
 * @returns 格式化结果
 */
function formatAmount(value: number): string {
  return (value ?? 0).toFixed(2)
}

watch(
  () => props.show,
  (visible) => {
    if (visible && props.orderId) {
      detail.value = null
      // 换单时收起上一单展开的报文原文
      rawOpenIds.value = new Set()
      loadDetail(props.orderId)
    }
  },
)

onMounted(() => {
  window.addEventListener('resize', updateNarrow)
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
})
</script>

<style scoped>
.amount-strong {
  color: #d03050;
  font-weight: 600;
}

.section-title {
  font-weight: 600;
  margin-bottom: 8px;
}

/* 通知报文原文：等宽、限高滚动，避免详情弹窗被长正文撑爆 */
.raw-body {
  max-height: 220px;
  overflow: auto;
  margin: 0;
  padding: 8px;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  background: #f7f7f9;
  border-radius: 4px;
}
</style>

<style>
/* 弹窗经 teleport 挂到 body 外层，scoped 样式（data-v 属性链）作用不到 n-modal 渲染的卡片，
   故内容区滚动用全局样式实现；宽度/限高以内联 style 保证生效（scoped 规则会失效）。
   n-card 默认 width:100% + flex 纵向布局，限高后内容区内部滚动，头部保持可见 */
.order-detail-modal > .n-card__content {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
</style>
