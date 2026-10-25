<template>
  <div class="orders-tab">
    <!-- 搜索栏 -->
    <div class="toolbar">
      <n-input
        v-model:value="query.order_no"
        placeholder="订单号精确搜索"
        clearable
        size="small"
        class="order-no-input"
        @keydown.enter="onSearch"
      />
      <n-select
        v-model:value="query.status"
        :options="statusOptions"
        size="small"
        class="status-select"
        clearable
        placeholder="订单状态"
      />
      <n-select
        v-model:value="query.source"
        :options="orderSourceFilterOptions"
        size="small"
        class="source-select"
        placeholder="订单来源"
      />
      <n-button type="primary" size="small" :loading="loading" @click="onSearch">查询</n-button>
    </div>

    <!-- 订单列表 -->
    <n-data-table
      :columns="columns"
      :data="orders"
      :loading="loading"
      :row-key="(row: Order) => row.id"
      size="small"
      class="table"
    />
    <div class="pagination">
      <n-pagination
        :page="page"
        :page-size="pageSize"
        :item-count="total"
        :page-sizes="[10, 20, 50]"
        show-size-picker
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      />
    </div>

    <!-- 订单详情 + 处理弹窗 -->
    <n-modal
      v-model:show="showDetail"
      preset="card"
      title="订单详情"
      style="width: 720px; max-width: 94vw"
    >
      <n-spin :show="detailLoading">
        <template v-if="detail">
          <div class="detail-grid">
            <div class="detail-item"><span class="label">订单号</span><span>{{ detail.order_no }}</span></div>
            <div class="detail-item"><span class="label">状态</span><n-tag size="small" :type="orderStatusTagType(detail.status)">{{ detail.status_text }}</n-tag></div>
            <div class="detail-item"><span class="label">来源</span><span>{{ orderSourceText(detail.source ?? 1) }}</span></div>
            <div class="detail-item">
              <span class="label">会员</span>
              <!-- 关联会员可点击查看其历史订单/常买商品 -->
              <n-button
                v-if="detail.user_id > 0"
                size="tiny"
                text
                type="primary"
                @click="openUserOrders(detail)"
              >
                {{ detail.user_name || `用户${detail.user_id}` }}
              </n-button>
              <span v-else>{{ detail.user_name || '散客' }}</span>
            </div>
            <div class="detail-item"><span class="label">配送方式</span><span>{{ deliveryTypeText(detail.delivery_type) }}</span></div>
            <div class="detail-item"><span class="label">支付方式</span><span>{{ detail.pay_type_text || (detail.pay_type ? payTypeText(detail.pay_type) : '-') }}</span></div>
            <div class="detail-item"><span class="label">下单时间</span><span>{{ formatDateTime(detail.created_at) }}</span></div>
            <div class="detail-item"><span class="label">收银员</span><span>{{ detail.operator_name || '-' }}</span></div>
            <div class="detail-item"><span class="label">班次</span><span>{{ detail.shift_id ? `#${detail.shift_id}` : '-' }}</span></div>
            <div v-if="detail.pickup_code" class="detail-item"><span class="label">取货码</span><span class="pickup-code">{{ detail.pickup_code }}</span></div>
            <div v-if="detail.remark" class="detail-item span2"><span class="label">备注</span><span>{{ detail.remark }}</span></div>
          </div>

          <!-- 商品明细（含改价标记）；直接收款单无商品，给出空状态避免空白区块 -->
          <div class="section-title">商品明细</div>
          <n-empty v-if="!detail.items?.length" description="该订单无商品明细（直接收款）" size="small" class="items-empty" />
          <div v-else class="items-table">
            <div v-for="item in detail.items" :key="item.id" class="item-row">
              <span class="item-name">
                {{ item.product_name }}
                <template v-if="item.sku_name">（{{ item.sku_name }}）</template>
                <n-tag v-if="item.price_modified" size="tiny" type="warning" :bordered="false" class="mod-tag">已改价</n-tag>
              </span>
              <span class="item-price">
                ￥{{ formatAmount(item.price) }}
                <span v-if="item.price_modified" class="origin">￥{{ formatAmount(item.original_price ?? item.price) }}</span>
              </span>
              <span class="item-qty">x{{ item.quantity }}</span>
              <span class="item-subtotal">￥{{ formatAmount(item.total_amount) }}</span>
            </div>
          </div>

          <!-- 金额构成 -->
          <div class="amount-block">
            <div class="amount-row"><span>商品总额</span><span>￥{{ formatAmount(detail.total_amount) }}</span></div>
            <div v-if="detail.discount_amount > 0" class="amount-row"><span>系统优惠</span><span>-￥{{ formatAmount(detail.discount_amount) }}</span></div>
            <div v-if="(detail.manual_discount ?? 0) > 0" class="amount-row"><span>收银优惠（改价/抹零）</span><span>-￥{{ formatAmount(detail.manual_discount) }}</span></div>
            <div v-if="detail.delivery_fee > 0" class="amount-row"><span>配送费</span><span>+￥{{ formatAmount(detail.delivery_fee) }}</span></div>
            <div class="amount-row strong"><span>实付</span><span>￥{{ formatAmount(detail.pay_amount) }}</span></div>
          </div>

          <!-- 售后记录（每次退款单：类型/金额/状态/时间） -->
          <template v-if="detail.refunds && detail.refunds.length">
            <div class="section-title">售后记录</div>
            <div class="refund-list">
              <div v-for="refund in detail.refunds" :key="refund.id" class="refund-row">
                <span class="refund-no">{{ refund.refund_no }}</span>
                <n-tag size="tiny" :type="refundTypeTagType(refund.type)" :bordered="false">{{ refundTypeText(refund.type) }}</n-tag>
                <span class="refund-amount">￥{{ formatAmount(refund.amount) }}</span>
                <n-tag size="tiny" :type="refundStatusTagType(refund.status)" :bordered="false">{{ refundStatusText(refund.status) }}</n-tag>
                <span class="refund-time">{{ formatDateTime(refund.refund_time || refund.created_at) }}</span>
              </div>
            </div>
          </template>

          <!-- 处理操作（按状态与权限显示，全部弹窗内完成） -->
          <n-space justify="end" class="detail-actions">
            <n-button
              v-if="canRepay"
              size="small"
              type="warning"
              @click="openRepay(detail)"
            >
              继续收款
            </n-button>
            <n-button
              v-if="canCancel"
              size="small"
              type="error"
              secondary
              @click="showCancelModal = true"
            >
              取消订单
            </n-button>
            <n-button
              v-if="canRefund"
              size="small"
              type="warning"
              secondary
              @click="showRefundModal = true"
            >
              代客退款
            </n-button>
            <n-button
              v-if="canVerify"
              size="small"
              type="primary"
              @click="onVerify"
              :loading="verifying"
            >
              自提核销
            </n-button>
          </n-space>
        </template>
      </n-spin>
    </n-modal>

    <!-- 继续收款弹窗（待支付单：付款码 / 现金两路） -->
    <n-modal
      v-model:show="showRepay"
      preset="card"
      title="继续收款"
      style="width: 380px; max-width: 92vw"
    >
      <div v-if="repayOrder" class="repay-wrap">
        <div class="repay-amount">￥{{ formatAmount(repayOrder.pay_amount) }}</div>
        <div class="repay-order muted">{{ repayOrder.order_no }}</div>
        <n-space vertical :size="10">
          <n-button type="primary" block size="large" :loading="repayPreparing" @click="onRepayByCode">
            扫顾客付款码收款
          </n-button>
          <n-button block size="large" type="warning" secondary @click="onRepayByCash">
            收现金（顾客改用现金）
          </n-button>
        </n-space>
        <n-text depth="3" style="font-size: 12px">付款码收款失败可在弹窗内改用现金收款</n-text>
      </div>
    </n-modal>

    <!-- 付款码扫码弹窗（待支付单继续收款，与商品下单共用） -->
    <code-pay-dialog
      v-model:show="showRepayCodePay"
      :order-id="repayOrder?.id ?? 0"
      :order-no="repayOrder?.order_no ?? ''"
      :amount="repayOrder?.pay_amount ?? 0"
      :payment-no="repayPaymentNo"
      @paid="onRepayPaid"
      @close-abandoned="onRepayAbandoned"
    />

    <!-- 取消订单弹窗 -->
    <n-modal
      v-model:show="showCancelModal"
      preset="dialog"
      type="warning"
      title="取消订单"
      positive-text="确认取消"
      negative-text="关闭"
      :loading="cancelling"
      @positive-click="onCancel"
    >
      <n-input v-model:value="cancelReason" type="textarea" placeholder="取消原因（必填）" :rows="2" />
    </n-modal>

    <!-- 代客退款弹窗（与后台订单页共用：整单/部分商品/仅金额退，金额可调低） -->
    <refund-modal v-model:show="showRefundModal" :order-id="detail?.id ?? null" @success="onRefundSuccess" />

    <!-- 会员订单弹窗：点击会员查看其历史订单与常买商品（订单号点击经 open-order 转发到详情弹窗） -->
    <user-orders-modal v-model:show="showUserOrders" :user="userOrdersUser" @open-order="openOrderDetail" />

    <!-- 订单详情弹窗（会员订单内订单号点击打开；用户点击经 view-user 回到会员订单弹窗） -->
    <order-detail-modal v-model:show="showOrderDetail" :order-id="orderDetailId" @view-user="openUserOrdersOf" />
  </div>
</template>

<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { NButton, NEmpty, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { cancelOrder, ensurePosCodePaySession, getOrderDetail, getOrderList, payPosOrderCash, verifyPickup } from '@/api/order'
import type { Order, OrderDetail } from '@/api/types'
import { ORDER_STATUS, QUERY_STATUS, canAdminRefund, deliveryTypeText, orderSourceFilterOptions, orderSourceText, orderStatusTagType, payTypeText, refundStatusText, refundStatusTagType, refundTypeText, refundTypeTagType } from '@/constants/order'
import { useAuthStore } from '@/stores/auth'
import { usePosStore } from '@/stores/pos'
import { formatDateTime } from '@/utils/datetime'
import RefundModal from '@/views/order/components/RefundModal.vue'
import UserOrdersModal from '@/views/user/components/UserOrdersModal.vue'
import OrderDetailModal from '@/views/order/detail.vue'
import CodePayDialog from './CodePayDialog.vue'
import { reopenModal } from '@/utils/modal'

/**
 * 订单查询 Tab（收银台内）
 * 单号精确搜索 + 状态/来源筛选 + 分页；详情弹窗内完成继续收款/取消/代客退款/自提核销，
 * 处理按钮按状态与权限显示（取消/核销=order:update，退款=refund:update），不打断收银流程跳后台；
 * 待支付单支持「继续收款」（付款码/现金两路，扣款失败可换现金）；
 * 「引用到收银台」把订单商品写入 posStore 待引用清单并切回商品下单 Tab（仅限有商品明细的订单）；
 * 代客退款与后台共用 RefundModal（整单/部分商品/仅金额退，金额可调低），详情弹窗内展示每次售后记录
 */

const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()
const posStore = usePosStore()

/** 引用到收银台事件（父组件切回商品下单 Tab） */
const emit = defineEmits<{ (e: 'import-to-cashier'): void }>()

// --- 列表查询 ---
const query = ref({ order_no: '', status: null as number | null, source: null as number | null })
const orders = ref<Order[]>([])
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const loading = ref(false)

/** 状态筛选选项（含查询聚合虚拟状态） */
const statusOptions = [
  { label: '待支付', value: ORDER_STATUS.PENDING_PAY },
  { label: '待发货/待自提', value: QUERY_STATUS.SHIPPING_AGGREGATE },
  { label: '备货中', value: ORDER_STATUS.STOCKING },
  { label: '配送中', value: ORDER_STATUS.SHIPPING },
  { label: '已完成', value: ORDER_STATUS.COMPLETED },
  { label: '已取消', value: ORDER_STATUS.CANCELLED },
  { label: '售后中', value: QUERY_STATUS.REFUND_AGGREGATE },
]

/**
 * 拉取订单列表（本社区语境，C端+收银台全量）
 */
async function loadOrders() {
  loading.value = true
  try {
    const res = await getOrderList({
      page: page.value,
      page_size: pageSize.value,
      order_no: query.value.order_no.trim() || undefined,
      status: query.value.status ?? undefined,
      source: query.value.source ?? undefined,
    })
    orders.value = res.data.data.list || []
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

/**
 * 查询（回到第一页）
 */
function onSearch() {
  page.value = 1
  void loadOrders()
}

/**
 * 翻页
 * @param p 页码
 */
function onPageChange(p: number) {
  page.value = p
  void loadOrders()
}

/**
 * 调整每页条数
 * @param size 每页条数
 */
function onPageSizeChange(size: number) {
  pageSize.value = size
  page.value = 1
  void loadOrders()
}

// --- 详情弹窗 ---
const showDetail = ref(false)
const detailLoading = ref(false)
const detail = ref<OrderDetail | null>(null)
const verifying = ref(false)
const showCancelModal = ref(false)
const cancelling = ref(false)
const cancelReason = ref('')
const showRefundModal = ref(false)

/** 是否持有订单处理权限（取消/核销接口要求 order:update） */
const hasUpdatePermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('order:update'))
/** 是否持有退款权限（代客退款接口要求 refund:update） */
const hasRefundPermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('refund:update'))

/** 可取消状态集：仅待支付（已支付订单走代客退款，避免取消/退款按钮并存歧义） */
const CANCELABLE_STATUSES: number[] = [ORDER_STATUS.PENDING_PAY]

/** 可取消：未发货且有权限 */
const canCancel = computed(
  () => !!detail.value && hasUpdatePermission.value && CANCELABLE_STATUSES.includes(detail.value.status),
)
/**
 * 可退款：状态口径统一由 canAdminRefund 判定（已支付/备货中/配送中，
 * 以及到店单的已完成——到店单收款即自动完结，门店售出后仍需能退款），与后端一致
 */
const canRefund = computed(() => !!detail.value && hasRefundPermission.value && canAdminRefund(detail.value))
/** 可核销：自提单待自提（已支付）且有权限 */
const canVerify = computed(
  () => !!detail.value && hasUpdatePermission.value && detail.value.status === ORDER_STATUS.PAID && detail.value.delivery_type === 1,
)
/** 可继续收款：待支付收银台单（与后端 pay-cash/pay-code-session 口径一致） */
const canRepay = computed(
  () => !!detail.value && detail.value.status === ORDER_STATUS.PENDING_PAY && detail.value.source === 2,
)

// --- 继续收款（待支付单：付款码/现金两路） ---
const showRepay = ref(false)
const showRepayCodePay = ref(false)
const repayOrder = ref<Order | OrderDetail | null>(null)
const repayPaymentNo = ref('')
const repayPreparing = ref(false)

/** 确认弹窗聚焦定时器（组件卸载时清理，避免已卸载实例操作 DOM） */
let dialogFocusTimer: number | undefined

/**
 * 弹窗渲染后聚焦确认按钮，回车即确认（不依赖鼠标）
 * @param selector 确认按钮选择器
 */
function focusDialogConfirm(selector = '.n-dialog__action button:last-of-type') {
  if (dialogFocusTimer !== undefined) window.clearTimeout(dialogFocusTimer)
  dialogFocusTimer = window.setTimeout(() => {
    dialogFocusTimer = undefined
    const btn = document.querySelector(selector) as HTMLButtonElement | null
    btn?.focus()
  }, 150)
}

/**
 * 打开继续收款弹窗（详情弹窗或列表行入口）
 * @param order 订单行/详情
 */
function openRepay(order: Order | OrderDetail) {
  repayOrder.value = order
  repayPaymentNo.value = ''
  showRepay.value = true
}

/**
 * 付款码路径：先向后端要收款会话（复用遗留待支付单，out_trade_no 不变），
 * 再打开扫码窗；渠道未开启时后端提示改现金
 */
async function onRepayByCode() {
  if (!repayOrder.value || repayPreparing.value) return
  repayPreparing.value = true
  try {
    const res = await ensurePosCodePaySession(repayOrder.value.id)
    if (res.data.code !== 0) return
    repayPaymentNo.value = res.data.data.payment_no
    showRepayCodePay.value = true
    showRepay.value = false
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    repayPreparing.value = false
  }
}

/**
 * 现金路径：确认后调 pay-cash（后端关遗留待支付单后收现金，订单自动完结）
 */
function onRepayByCash() {
  if (!repayOrder.value) return
  const order = repayOrder.value
  dialog.warning({
    title: '现金收款',
    content: `确认已收现金 ￥${formatAmount(order.pay_amount)}（订单 ${order.order_no}）？（回车确认）`,
    positiveText: '确认收款',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await payPosOrderCash(order.id)
      if (res.data.code === 0) {
        message.success(`订单 ${order.order_no} 现金收款成功`)
        showRepay.value = false
        showDetail.value = false
        void loadOrders()
      }
    },
  })
  // 回车确认：弹窗渲染后聚焦确认按钮
  focusDialogConfirm()
}

/**
 * 待支付单付款码收款成功：刷新列表
 */
function onRepayPaid() {
  showRepayCodePay.value = false
  message.success('收款成功')
  void loadOrders()
}

/**
 * 收银员放弃扫码：订单保留待支付
 */
function onRepayAbandoned() {
  message.info(`订单 ${repayOrder.value?.order_no ?? ''} 已保留待支付，可稍后继续收款或取消`)
}

// --- 引用到收银台 ---

/**
 * 把订单商品引用到收银台：拉详情取商品快照写入 posStore 待引用清单，
 * 通知父组件切回商品下单 Tab（OrderTab 挂载时消费，追加到当前小票）
 * @param row 订单行（列表入口）
 */
async function importToCashier(row: Order) {
  try {
    const res = await getOrderDetail(row.id)
    const d = res.data.data
    const items = (d.items || []).map((it) => ({
      sku_id: it.sku_id,
      quantity: it.quantity,
      product_name: it.sku_name ? `${it.product_name}（${it.sku_name}）` : it.product_name,
      spec_name: it.sku_name || '',
      // 改价单取档案价快照，改价不带入（"再来一单"按当前档案价成交）
      price: it.price_modified ? (it.original_price ?? it.price) : it.price,
    }))
    if (!items.length) {
      message.warning('该订单没有商品明细（直接收款单无需引用）')
      return
    }
    posStore.setPendingImport(items)
    message.success(`已引用订单 ${d.order_no} 的 ${items.length} 项商品`)
    emit('import-to-cashier')
  } catch {
    // 错误提示由请求拦截器统一弹出
  }
}

/**
 * 打开详情弹窗并加载完整详情
 * @param row 列表行
 */
async function openDetail(row: Order) {
  showDetail.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const res = await getOrderDetail(row.id)
    detail.value = res.data.data
  } finally {
    detailLoading.value = false
  }
}

/**
 * 自提核销（详情弹窗内完成）
 */
async function onVerify() {
  if (!detail.value) return
  verifying.value = true
  try {
    await verifyPickup(detail.value.id, detail.value.pickup_code || '')
    message.success(`订单 ${detail.value.order_no} 核销成功`)
    showDetail.value = false
    void loadOrders()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifying.value = false
  }
}

/**
 * 取消订单（必填原因）
 */
async function onCancel() {
  if (!detail.value) return false
  const reason = cancelReason.value.trim()
  if (!reason) {
    message.warning('请填写取消原因')
    return false
  }
  cancelling.value = true
  try {
    await cancelOrder(detail.value.id, reason)
    message.success('订单已取消')
    showCancelModal.value = false
    cancelReason.value = ''
    showDetail.value = false
    void loadOrders()
    return true
  } catch {
    // 错误提示由请求拦截器统一弹出
    return false
  } finally {
    cancelling.value = false
  }
}

/**
 * 代客退款成功后的统一刷新（RefundModal 内完成发起，关闭详情弹窗回列表刷新）
 */
function onRefundSuccess() {
  showDetail.value = false
  void loadOrders()
}

// --- 会员订单弹窗（列表/详情中的会员点击打开） ---
const showUserOrders = ref(false)
const userOrdersUser = ref<{ id: number; nickname?: string } | null>(null)

// --- 订单详情弹窗（会员订单弹窗内订单号点击打开） ---
const showOrderDetail = ref(false)
const orderDetailId = ref<number | null>(null)

/**
 * 打开会员订单弹窗（轻量用户对象；订单详情弹窗 view-user 事件也走这里）
 * @param user 会员 ID 与昵称
 */
function openUserOrdersOf(user: { id: number; nickname?: string }) {
  userOrdersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(showUserOrders)
}

/**
 * 打开订单详情弹窗（含会员订单弹窗 open-order 事件转发；已开时重挂载置顶并重载）
 * @param id 订单 ID
 */
function openOrderDetail(id: number) {
  orderDetailId.value = id
  reopenModal(showOrderDetail)
}

/**
 * 打开会员订单弹窗
 * @param row 订单行（取归属会员；散客不响应）
 */
function openUserOrders(row: Pick<Order, 'user_id' | 'user_name'>) {
  if (row.user_id <= 0) return
  openUserOrdersOf({ id: row.user_id, nickname: row.user_name })
}

/** 表格列定义（行点击打开详情；状态列彩色标签；待支付可继续收款；有商品的单可引用到收银台） */
const columns = computed<DataTableColumns<Order>>(() => [
  { title: '订单号', key: 'order_no', width: 190, ellipsis: { tooltip: true } },
  {
    title: '会员',
    key: 'user_name',
    width: 110,
    ellipsis: { tooltip: true },
    // 关联会员（user_id>0）可点击查看其历史订单/常买商品；散客展示原文
    render: (row) =>
      row.user_id > 0
        ? h(
            NButton,
            { size: 'tiny', type: 'primary', quaternary: true, onClick: () => openUserOrders(row) },
            { default: () => row.user_name || `用户${row.user_id}` },
          )
        : row.user_name || '散客',
  },
  { title: '实付', key: 'pay_amount', width: 90, render: (row) => `￥${formatAmount(row.pay_amount)}` },
  {
    title: '支付方式',
    key: 'pay_type',
    width: 90,
    render: (row) => row.pay_type_text || (row.pay_type ? payTypeText(row.pay_type) : '-'),
  },
  {
    // 支付/订单状态彩色标签：与详情弹窗同口径（待支付黄/已支付蓝/完成绿/退款红）
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      h(NTag, { size: 'small', type: orderStatusTagType(row.status), bordered: false }, { default: () => row.status_text }),
  },
  {
    title: '来源',
    key: 'source',
    width: 80,
    render: (row) => orderSourceText(row.source ?? 1),
  },
  { title: '取货码', key: 'pickup_code', width: 90, render: (row) => row.pickup_code || '-' },
  { title: '下单时间', key: 'created_at', width: 165, ellipsis: { tooltip: true }, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render: (row) => {
      const btns = [
        h(
          NButton,
          { size: 'tiny', type: 'primary', quaternary: true, onClick: () => openDetail(row) },
          { default: () => '详情' },
        ),
      ]
      // 引用按商品快照重建小票：直接收款单（item_count=0）无商品可引用，不渲染入口
      if ((row.item_count ?? 0) > 0) {
        btns.push(
          h(
            NButton,
            { size: 'tiny', type: 'info', quaternary: true, onClick: () => void importToCashier(row) },
            { default: () => '引用' },
          ),
        )
      }
      // 待支付收银台单：列表直达继续收款
      if (row.status === ORDER_STATUS.PENDING_PAY && row.source === 2) {
        btns.push(
          h(
            NButton,
            { size: 'tiny', type: 'warning', quaternary: true, onClick: () => openRepay(row) },
            { default: () => '收款' },
          ),
        )
      }
      return btns
    },
  },
])

onMounted(loadOrders)

onBeforeUnmount(() => {
  if (dialogFocusTimer !== undefined) {
    window.clearTimeout(dialogFocusTimer)
    dialogFocusTimer = undefined
  }
})
</script>

<style lang="scss" scoped>
.orders-tab {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 14px;
  box-sizing: border-box;
  gap: 10px;
}
.toolbar {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  .order-no-input {
    width: 220px;
  }
  .status-select,
  .source-select {
    width: 150px;
  }
}
.table {
  flex: 1;
  min-height: 0;
}
.pagination {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
}
.detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 20px;
  .span2 {
    grid-column: span 2;
  }
}
.detail-item {
  display: flex;
  gap: 8px;
  font-size: 13px;
  align-items: center;
  .label {
    color: #999;
    flex-shrink: 0;
    width: 62px;
  }
}
.pickup-code {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 2px;
  color: #07c160;
}
.section-title {
  font-size: 13px;
  font-weight: 600;
  color: #666;
  margin: 16px 0 8px;
}
.items-table {
  border: 1px solid #efeff5;
  border-radius: 6px;
}
.items-empty {
  padding: 12px 0;
}
.item-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  font-size: 13px;
  border-bottom: 1px solid #f3f3f7;
  &:last-child {
    border-bottom: none;
  }
  .item-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    .mod-tag {
      margin-left: 4px;
    }
  }
  .item-price {
    font-variant-numeric: tabular-nums;
    .origin {
      margin-left: 3px;
      text-decoration: line-through;
      color: #ccc;
    }
  }
  .item-qty {
    color: #999;
    width: 44px;
    text-align: right;
  }
  .item-subtotal {
    width: 84px;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
}
.amount-block {
  margin-top: 12px;
  padding: 10px 14px;
  background: #fafafa;
  border-radius: 6px;
}
.amount-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 2px 0;
  color: #666;
  &.strong {
    color: #d03050;
    font-weight: 700;
    font-size: 14px;
    margin-top: 4px;
  }
}
.detail-actions {
  margin-top: 16px;
}
.repay-wrap {
  display: flex;
  flex-direction: column;
  gap: 14px;
  text-align: center;
}
.repay-amount {
  font-size: 32px;
  font-weight: 700;
  color: #d03050;
}
.repay-order {
  font-size: 12px;
}
.muted {
  color: #999;
}
.refund-list {
  border: 1px solid #efeff5;
  border-radius: 6px;
}
.refund-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  font-size: 12px;
  border-bottom: 1px solid #f3f3f7;
  &:last-child {
    border-bottom: none;
  }
  .refund-no {
    color: #666;
    font-family: monospace;
  }
  .refund-amount {
    color: #d03050;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .refund-time {
    margin-left: auto;
    color: #999;
  }
}
</style>
