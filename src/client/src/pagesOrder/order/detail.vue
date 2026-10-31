<template>
  <view class="order-detail-page">
    <view class="status-card">
      <view class="status-row">
        <text class="status-text">{{ order.status_text }}</text>
        <text v-if="isStoreOrder(order)" class="store-tag">门店单</text>
      </view>
      <text class="status-tip">{{ statusTip }}</text>
    </view>

    <view class="address-card">
      <view class="address-top">
        <text class="card-label">{{ isPickup ? '自提点' : '收货地址' }}</text>
      </view>
      <view class="address-top">
        <text class="name">{{ order.address?.contact_name }}</text>
        <text v-if="!isPickup" class="phone">{{ order.address?.contact_phone }}</text>
      </view>
      <text class="address-text">
        {{ joinRegionAddress(order.address?.province, order.address?.city, order.address?.district, order.address?.detail) }}
      </text>
    </view>

    <!-- 自提取货码：待自提（已支付/备货中）时展示，到店出示核销 -->
    <view v-if="isPickup && showPickupCode" class="pickup-card">
      <text class="pickup-title">取货码</text>
      <text class="pickup-code">{{ order.pickup_code }}</text>
      <image v-if="pickupQrcodeUrl" class="pickup-qrcode" :src="pickupQrcodeUrl" mode="aspectFit" />
      <text class="pickup-tip">到店出示此码，由工作人员扫码或输码核销</text>
    </view>

    <view class="goods-card">
      <view
        v-for="item in order.items"
        :key="`${item.product_id}-${item.sku_id}`"
        class="goods-item"
        @click="goProductDetail(item.product_id)"
      >
        <image class="goods-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
        <view class="goods-info">
          <text class="goods-name">{{ item.product_name }}</text>
          <text class="goods-sku">{{ item.spec_name }}</text>
          <view class="goods-bottom">
            <text class="goods-price">{{ formatPrice(item.price) }}</text>
            <view class="goods-quantity-wrap">
              <!-- 行级已退数量（服务端 GetOrder 回填，保留原数量展示，2026-10-31 部分退款可见性） -->
              <text v-if="item.refunded_quantity" class="goods-refunded">已退 {{ item.refunded_quantity }} 件</text>
              <text class="goods-quantity">x{{ item.quantity }}</text>
            </view>
          </view>
        </view>
      </view>
      <view class="order-info">
        <view class="info-row">
          <text>订单编号: {{ order.order_no }}</text>
          <text class="copy-btn" @click="copyOrderNo">复制</text>
        </view>
        <text>创建时间: {{ formatDate(order.created_at) }}</text>
        <text v-if="isStoreOrder(order)">订单来源: 门店（收银台代客下单）</text>
        <text>支付方式: {{ order.pay_type_text || '未支付' }}</text>
        <text>配送方式: {{ isPickup ? '社区自提' : '送货上门' }}</text>
        <text v-if="order.delivery_time">期望配送时间: {{ order.delivery_time }}</text>
        <text v-if="order.status === 6 && order.cancel_reason">取消原因: {{ order.cancel_reason }}</text>
        <text v-if="order.status === 6 && order.cancel_time">取消时间: {{ formatDate(order.cancel_time) }}</text>
      </view>
      <view class="amount-list">
        <view class="amount-row">
          <text>商品总额</text>
          <text>{{ formatPrice(order.total_amount) }}</text>
        </view>
        <view v-if="(order.discount_amount || 0) > 0" class="amount-row discount">
          <text>优惠</text>
          <text>-{{ formatPrice(order.discount_amount) }}</text>
        </view>
        <view v-if="(order.delivery_fee || 0) > 0" class="amount-row">
          <text>配送费</text>
          <text>{{ formatPrice(order.delivery_fee) }}</text>
        </view>
        <view class="amount-row">
          <text class="pay-label">实付</text>
          <text class="pay-amount">{{ formatPrice(order.pay_amount) }}</text>
        </view>
        <!-- 保留原始实付展示，另列已退金额（money 口径含运费，2026-10-31 部分退款可见性） -->
        <view v-if="(order.refunded_amount || 0) > 0" class="amount-row refunded">
          <text>已退款</text>
          <text>-{{ formatPrice(order.refunded_amount ?? 0) }}</text>
        </view>
      </view>
    </view>

    <!-- 售后记录：详情接口随单返回全部退款单，逐笔展示（部分退款可见性，2026-10-31） -->
    <view v-if="(order.refunds?.length || 0) > 0" class="refund-card">
      <view class="refund-card-title">
        <text class="card-label">售后记录</text>
        <text class="refund-card-link" @click="onShowRefundProgress">查看进度 ></text>
      </view>
      <view v-for="refund in order.refunds" :key="refund.id" class="refund-record">
        <view class="refund-record-head">
          <text class="refund-record-no">{{ refund.refund_no }}</text>
          <text class="refund-record-status" :class="refundRecordStatusClass(refund.status)">
            {{ refundRecordStatusText(refund.status) }}
          </text>
        </view>
        <view
          v-for="item in refund.items"
          :key="`${refund.id}-${item.order_item_id || item.product_name}`"
          class="refund-record-item"
        >
          <text class="refund-record-name">
            {{ item.product_name }}{{ item.spec_name ? `（${item.spec_name}）` : '' }} x{{ item.quantity }}
          </text>
          <text class="refund-record-amount">{{ formatPrice(item.refund_amount) }}</text>
        </view>
        <!-- 无商品明细：仅金额退或收银台直接收款单 -->
        <view v-if="!refund.items.length" class="refund-record-item">
          <text class="refund-record-name">无商品明细（仅金额退款）</text>
        </view>
        <view class="refund-record-meta">
          <text>{{ refundMetaText(refund) }}</text>
          <text v-if="refund.reason">原因：{{ refund.reason }}</text>
          <text v-if="refund.status === 3 && refund.reject_reason" class="refund-record-reject">
            拒绝原因：{{ refund.reject_reason }}
          </text>
          <text v-if="refund.refund_time">到账：{{ formatDate(refund.refund_time) }}</text>
        </view>
      </view>
    </view>

    <!-- 支付方式：待支付单直接在页面内渲染渠道选择（与确认订单页一致），
         默认选中第一个渠道，点底部「去支付」按选中渠道拉起 -->
    <PaymentChannelCard
      v-if="showPaymentCard"
      v-model="selectedChannel"
      class="detail-payment"
    />

    <view class="footer-bar">
      <!-- 门店单（收银台代客）C 端只读，去支付由门店处理，与列表页口径一致 -->
      <view
        v-if="order.status === 1 && !isStoreOrder(order)"
        class="action-btn"
      >
        <u-button type="success" text="去支付" :loading="paying" :disabled="paying" @click="onPay" />
      </view>
      <view
        v-if="canCancel(order)"
        class="action-btn"
      >
        <u-button plain type="info" text="取消订单" @click="onCancel" />
      </view>
      <view
        v-if="canReceive(order)"
        class="action-btn"
      >
        <u-button type="success" :text="receiveButtonText(order)" @click="onReceive" />
      </view>
      <view
        v-if="canRefund(order)"
        class="action-btn"
      >
        <u-button plain type="info" text="申请退款" @click="onRefund" />
      </view>
      <view
        v-if="showRefundProgress"
        class="action-btn"
      >
        <u-button plain type="info" text="退款进度" @click="onShowRefundProgress" />
      </view>
      <view
        v-if="canReorder(order)"
        class="action-btn"
      >
        <u-button plain type="success" text="再来一单" :loading="reordering" @click="onReorder" />
      </view>
    </view>

    <!-- 申请退款弹窗 -->
    <RefundModal v-model:show="refundVisible" :order-id="orderId" @success="loadOrder" />
    <!-- 退款进度弹窗 -->
    <RefundProgressModal v-model:show="refundProgressVisible" :order-id="orderId" />
    <!-- 取消订单弹窗（需选择原因） -->
    <CancelOrderModal v-model:show="cancelVisible" :order-id="orderId" @success="loadOrder" />
  </view>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { onLoad, onUnload, onHide, onShow } from '@dcloudio/uni-app'
import { getOrderDetail, confirmReceive, reorderOrder } from '@/api/order'
import { formatPrice, formatDate } from '@/utils/format'
import { notifyPayOutcome, onReturnToSite, payOrder } from '@/pagesOrder/common/pay'
import { goOrderList, goProductDetail } from '@/utils/navigate'
import { joinRegionAddress } from '@/utils/region'
import { canReceive, receiveButtonText, canCancel, canRefund, canReorder, isStoreOrder, DELIVERY_TYPE_PICKUP } from '@/utils/order'
import PaymentChannelCard from '@/components/PaymentChannelCard.vue'
import RefundModal from '@/components/RefundModal.vue'
import RefundProgressModal from '@/components/RefundProgressModal.vue'
import CancelOrderModal from '@/components/CancelOrderModal.vue'
import type { Order, PaymentChannel, RefundProgress } from '@/api/types'

const orderId = ref(0)
const refundVisible = ref(false)
const refundProgressVisible = ref(false)
const cancelVisible = ref(false)
/** 再来一单请求中标记（防止重复点击重复加购） */
const reordering = ref(false)
/** 倒计时当前时间（每秒刷新，页面隐藏时暂停避免后台计时偏差） */
const nowTick = ref(Date.now())
let countdownTimer: ReturnType<typeof setInterval> | null = null

const order = ref<Order>({
  id: 0,
  order_no: '',
  status: 0,
  status_text: '',
  total_amount: 0,
  discount_amount: 0,
  delivery_fee: 0,
  delivery_type: 2,
  pay_amount: 0,
  items: [],
  address: {
    id: 0,
    contact_name: '',
    contact_phone: '',
    province: '',
    city: '',
    district: '',
    detail: '',
    tag: '',
    community_id: 0,
    community_name: '',
    is_default: false,
  },
  remark: '',
  created_at: '',
  paid_at: '',
  delivered_at: '',
  received_at: '',
})

/** 是否自提单 */
const isPickup = computed(() => order.value.delivery_type === DELIVERY_TYPE_PICKUP)

/** 是否展示「退款进度」入口：退款中(7)/已退款(8)订单，或本单存在任何退款记录（含部分退款，2026-10-31 放宽） */
const showRefundProgress = computed(
  () => order.value.status === 7 || order.value.status === 8 || (order.value.refunds?.length || 0) > 0,
)

/** 是否展示支付方式卡片：待支付(1)且非门店单（门店单只读，支付由门店处理） */
const showPaymentCard = computed(() => order.value.status === 1 && !isStoreOrder(order.value))

/** 当前选中的支付渠道（支付方式卡片 v-model，组件内默认选中第一个） */
const selectedChannel = ref<PaymentChannel | null>(null)

// --- 售后记录展示（详情接口随单返回 refunds，2026-10-31 部分退款可见性） ---

/**
 * 售后记录状态文案
 * @param status 退款状态：1待审核 2已同意 3已拒绝 4退款中 5已退款
 * @returns 状态文案，未知状态返回空串
 */
function refundRecordStatusText(status: number): string {
  const map: Record<number, string> = { 1: '待审核', 2: '已同意', 3: '已拒绝', 4: '退款中', 5: '已退款' }
  return map[status] || ''
}

/**
 * 售后记录状态样式 class（按状态区分颜色，已退款沿用默认色不单列）
 * @param status 退款状态
 * @returns 状态 class 名，默认状态返回空串
 */
function refundRecordStatusClass(status: number): string {
  if (status === 3) return 'is-rejected'
  if (status === 5) return 'is-refunded'
  return ''
}

/**
 * 单笔售后记录的金额摘要文案（类型 + 退款金额 + 含运费说明）
 * @param refund 退款单
 * @returns 如「部分退 · 退款 ¥5.50」或「整单退 · 退款 ¥11.00（含运费 ¥1.00）」
 */
function refundMetaText(refund: RefundProgress): string {
  const typeMap: Record<number, string> = { 1: '整单退', 2: '部分退', 3: '仅金额退' }
  const type = typeMap[refund.type] || '退款'
  const freight = refund.freight_amount > 0 ? `（含运费 ${formatPrice(refund.freight_amount)}）` : ''
  return `${type} · 退款 ${formatPrice(refund.amount)}${freight}`
}

// --- 自提取货码 ---
// 待自提（已支付2/备货中3）时展示取货码与二维码；已完成/已取消不再展示
// @ts-ignore qrcode-generator 为无类型声明的本地库（置于分包目录，仅本分包使用）
import qrcodeLib from './qrcode.min.js'

const pickupQrcodeUrl = ref('')

/** 是否展示取货码卡片 */
const showPickupCode = computed(() => {
  const s = order.value.status
  return !!order.value.pickup_code && (s === 2 || s === 3)
})

/** 生成取货码二维码（扫码核销用；生成失败静默降级为仅数字码） */
watch(
  () => order.value.pickup_code,
  (code) => {
    pickupQrcodeUrl.value = ''
    if (!code || !showPickupCode.value) return
    try {
      const qr = qrcodeLib(0, 'M')
      qr.addData(code)
      qr.make()
      pickupQrcodeUrl.value = qr.createDataURL(6, 8)
    } catch {
      pickupQrcodeUrl.value = ''
    }
  },
  { immediate: true },
)

const statusTip = computed(() => {
  if (order.value.status === 1) {
    return `请在 ${remainingText.value} 内完成支付`
  }
  if (isPickup.value) {
    const map: Record<number, string> = {
      2: '商家备货中，备好后请到自提点取货',
      // 自提单存在「已支付 → 备货中(3) → 待自提(4)」中间态，缺 3 会让备货期间提示空白
      3: '商家备货中，备好后请到自提点取货',
      4: '商品已备好，请到自提点取货',
      5: '订单已完成',
    }
    return map[order.value.status] || ''
  }
  const map: Record<number, string> = {
    2: '商家正在备货中',
    3: '商家正在备货中',
    4: '商品正在配送中',
    5: '订单已完成',
  }
  return map[order.value.status] || ''
})

/** 待支付剩余时间文案（每秒刷新） */
const remainingText = computed(() => {
  if (!order.value.expire_at) return ''
  const expire = new Date(order.value.expire_at.replace(' ', 'T')).getTime()
  if (Number.isNaN(expire)) return ''
  const remainSec = Math.floor((expire - nowTick.value) / 1000)
  if (remainSec <= 0) return '订单已超时'
  const h = Math.floor(remainSec / 3600)
  const m = Math.floor((remainSec % 3600) / 60)
  const s = remainSec % 60
  const mm = String(m).padStart(2, '0')
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`
})

/** 首进时 onLoad 已加载，onShow 紧随其后不应重复请求 */
let skipNextShow = false

onLoad((query) => {
  orderId.value = Number(query?.id) || 0
  if (orderId.value) {
    // 首进页面 onLoad 先于 onShow，这里请求后让紧随其后的 onShow 跳过重复拉取
    skipNextShow = true
    loadOrder()
  }
})

onUnload(() => {
  stopCountdown()
})

onHide(() => {
  stopCountdown()
})

// 页面重新可见时重新加载并重启倒计时（隐藏期间计时已暂停，需校准）
onShow(() => {
  if (!orderId.value) return
  if (skipNextShow) {
    skipNextShow = false
    return
  }
  loadOrder()
})

/** 启动待支付倒计时（页面隐藏时暂停，返回时由 loadOrder/ onShow 校准） */
function startCountdown() {
  stopCountdown()
  if (order.value.status !== 1 || !order.value.expire_at) return
  countdownTimer = setInterval(() => {
    nowTick.value = Date.now()
    // 归零后自动刷新订单状态（可能已被系统取消）
    if (isCountdownExpired()) {
      stopCountdown()
      loadOrder()
    }
  }, 1000)
}

/** 判断倒计时是否已归零 */
function isCountdownExpired(): boolean {
  if (!order.value.expire_at) return false
  const expire = new Date(order.value.expire_at.replace(' ', 'T')).getTime()
  return !Number.isNaN(expire) && Date.now() >= expire
}

/** 停止倒计时 */
function stopCountdown() {
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
}

/**
 * 加载订单详情
 */
async function loadOrder() {
  try {
    const res = await getOrderDetail(orderId.value)
    if (res.data) {
      order.value = res.data
      nowTick.value = Date.now()
      startCountdown()
    }
  } catch {
    // ignore
  }
}

/**
 * 复制订单号（客服场景高频操作）
 */
function copyOrderNo() {
  uni.setClipboardData({
    data: order.value.order_no,
    success: () => {
      uni.showToast({ title: '已复制', icon: 'success' })
    },
  })
}

/** 支付进行中标志：支付含创建支付单、拉起收银台等异步环节，防连击重复发起 */
const paying = ref(false)

/**
 * 支付订单
 * 按页面内支付方式卡片选中的渠道创建支付单并拉起支付：货到付款直接完成，
 * 微信支付在小程序端调起 JSAPI、H5 端跳转支付页面，未开通渠道提示暂未开通；
 * 仅在支付真实完成时提示支付成功
 */
async function onPay() {
  if (paying.value) return
  if (!selectedChannel.value) {
    uni.showToast({ title: '暂无可用支付方式', icon: 'none' })
    return
  }
  const channel = selectedChannel.value
  paying.value = true
  try {
    const outcome = await payOrder(order.value.id, channel.channel)
    notifyPayOutcome(outcome, channel.channel)
    // 已整页跳转第三方收银台：文档即将卸载，此刻刷新订单结果无意义，
    // 改为等用户回跳或切回标签页后再刷新订单状态
    if (outcome === 'redirected') {
      onReturnToSite(loadOrder)
      return
    }
    // 支付宝微信内引导：引导页已通过 redirectTo 接管本页，后续由结果页承接
    if (outcome === 'guided') {
      return
    }
    loadOrder()
  } catch {
    // request 已统一提示错误
  } finally {
    paying.value = false
  }
}

/**
 * 取消订单：打开原因选择弹窗（不可逆操作，需明确原因）
 */
function onCancel() {
  cancelVisible.value = true
}

/**
 * 确认收货：二次确认后再调接口，完成后退款入口关闭
 */
function onReceive() {
  const tip = isPickup.value ? '确认已到点自提？确认后订单将完成' : '确认已收到商品？确认后订单将完成'
  uni.showModal({
    title: receiveButtonText(order.value),
    content: tip,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await confirmReceive(order.value.id)
        uni.showToast({ title: '操作成功', icon: 'success' })
        loadOrder()
      } catch {
        // request 已统一提示错误
      }
    },
  })
}

/**
 * 申请退款：打开金额确认弹窗（选商品/数量、运费策略、可调低金额）
 */
function onRefund() {
  refundVisible.value = true
}

/**
 * 查看退款进度：打开退款进度弹窗（时间线展示审核/打款/到账）
 */
function onShowRefundProgress() {
  refundProgressVisible.value = true
}

/**
 * 再来一单：把订单商品按原数量重新加入订单所属社区的购物车
 * 全部/部分成功均跳转购物车页；部分商品下架或规格失效时先弹窗列出未加入清单；
 * 全部失败由后端返回业务错误，request 统一提示且停留当前页
 */
async function onReorder() {
  if (reordering.value) return
  reordering.value = true
  try {
    const res = await reorderOrder(order.value.id)
    const result = res.data
    const skipped = result?.skipped || []
    if (skipped.length) {
      const lines = skipped.map(
        (item) => `${item.product_name}${item.spec_name ? `（${item.spec_name}）` : ''}：${item.reason}`,
      )
      await new Promise<void>((resolve) => {
        uni.showModal({
          title: `已加入 ${result?.added || 0} 件商品`,
          content: `以下商品暂时无法购买：\n${lines.join('\n')}`,
          showCancel: false,
          success: () => resolve(),
          fail: () => resolve(),
        })
      })
    }
    // 购物车是 tabBar 页面，只能 switchTab；进入后 onShow 自动刷新购物车数据
    uni.switchTab({ url: '/pages/cart/cart' })
  } catch {
    // request 已统一提示错误
  } finally {
    reordering.value = false
  }
}
</script>

<style scoped>
.order-detail-page {
  min-height: 100vh;
  padding-bottom: 140rpx;
  background-color: #f5f5f5;
}

.status-card {
  padding: 40rpx 30rpx;
  background: linear-gradient(135deg, #07c160, #10b981);
}

/* 门店单标识行（收银台代客下单，展示但只读） */
.status-row {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.store-tag {
  font-size: 22rpx;
  line-height: 1;
  color: #ffffff;
  border: 1rpx solid rgba(255, 255, 255, 0.8);
  border-radius: 6rpx;
  padding: 6rpx 12rpx;
}

.status-text {
  display: block;
  font-size: 40rpx;
  font-weight: bold;
  color: #ffffff;
}

.status-tip {
  display: block;
  margin-top: 12rpx;
  font-size: 26rpx;
  color: rgba(255, 255, 255, 0.8);
}

.address-card {
  margin: 20rpx 30rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.card-label {
  font-size: 24rpx;
  color: #999999;
}

.address-top {
  display: flex;
  align-items: center;
  margin-bottom: 12rpx;
}

.address-top .name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.address-top .phone {
  margin-left: 20rpx;
  font-size: 28rpx;
  color: #666666;
}

.address-text {
  font-size: 26rpx;
  color: #666666;
  line-height: 1.4;
}

.goods-card {
  margin: 0 30rpx;
  padding: 20rpx 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

/* 支付方式卡片（PaymentChannelCard 根节点）与详情页其他卡片对齐：左右 30rpx 边距 */
.detail-payment {
  margin-left: 30rpx;
  margin-right: 30rpx;
}

.goods-item {
  display: flex;
  padding: 16rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
}

.goods-image {
  width: 160rpx;
  height: 160rpx;
  border-radius: 12rpx;
}

.goods-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: 20rpx;
  height: 160rpx;
}

.goods-name {
  font-size: 28rpx;
  color: #333333;
}

.goods-sku {
  font-size: 24rpx;
  color: #999999;
}

.goods-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.goods-price {
  font-size: 30rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.goods-quantity {
  font-size: 26rpx;
  color: #999999;
}

/* 商品行数量区：行级已退标记与原数量并排（保留原数量展示） */
.goods-quantity-wrap {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.goods-refunded {
  font-size: 22rpx;
  color: #ff4d4f;
}

.order-info {
  display: flex;
  flex-direction: column;
  gap: 12rpx;
  padding: 20rpx 0;
  font-size: 26rpx;
  color: #999999;
  border-bottom: 1rpx solid #f5f5f5;
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.copy-btn {
  font-size: 22rpx;
  color: #07c160;
  padding: 2rpx 14rpx;
  border: 1rpx solid #07c160;
  border-radius: 8rpx;
}

.amount-list {
  display: flex;
  flex-direction: column;
  gap: 12rpx;
  padding: 20rpx 0;
  font-size: 26rpx;
  color: #666666;
}

.amount-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.amount-row.discount {
  color: #ff4d4f;
}

/* 已退款金额行（与优惠行同为红色系） */
.amount-row.refunded {
  color: #ff4d4f;
}

.pay-label {
  font-size: 28rpx;
  color: #333333;
}

.pay-amount {
  font-size: 32rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.receive-button {
  flex: 1;
}

/* 自提取货码卡片 */
.pickup-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin: 20rpx 30rpx;
  padding: 36rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.pickup-title {
  font-size: 28rpx;
  color: #666666;
}

.pickup-code {
  margin: 20rpx 0;
  font-size: 64rpx;
  font-weight: bold;
  color: #07c160;
  letter-spacing: 8rpx;
}

.pickup-qrcode {
  width: 260rpx;
  height: 260rpx;
}

.pickup-tip {
  margin-top: 20rpx;
  font-size: 24rpx;
  color: #999999;
}

/* 售后记录卡片（部分退款可见性，2026-10-31） */
.refund-card {
  margin: 20rpx 30rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.refund-card-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8rpx;
}

.refund-card-link {
  font-size: 24rpx;
  color: #07c160;
}

.refund-record {
  padding: 16rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
}

.refund-record:last-child {
  border-bottom: none;
}

.refund-record-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.refund-record-no {
  font-size: 24rpx;
  color: #666666;
}

.refund-record-status {
  font-size: 24rpx;
  color: #ff9900;
}

/* 售后记录状态色（待审核/退款中为默认橙，已拒绝红，已退款绿） */
.refund-record-status.is-rejected {
  color: #ee0a24;
}

.refund-record-status.is-refunded {
  color: #07c160;
}

.refund-record-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 8rpx;
  font-size: 26rpx;
  color: #333333;
}

.refund-record-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.refund-record-amount {
  flex-shrink: 0;
  margin-left: 20rpx;
  color: #ff4d4f;
}

.refund-record-meta {
  display: flex;
  flex-direction: column;
  gap: 4rpx;
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #999999;
}

.refund-record-reject {
  color: #ee0a24;
}

.footer-bar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  justify-content: flex-end;
  gap: 16rpx;
  padding: 16rpx 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #eeeeee;
}

.action-btn .u-button {
  width: 100%;
}
</style>
