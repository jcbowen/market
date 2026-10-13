<template>
  <view class="order-detail-page">
    <view class="status-card">
      <text class="status-text">{{ order.status_text }}</text>
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

    <view class="goods-card">
      <view
        v-for="item in order.items"
        :key="`${item.product_id}-${item.sku_id}`"
        class="goods-item"
      >
        <image class="goods-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
        <view class="goods-info">
          <text class="goods-name">{{ item.product_name }}</text>
          <text class="goods-sku">{{ item.spec_name }}</text>
          <view class="goods-bottom">
            <text class="goods-price">{{ formatPrice(item.price) }}</text>
            <text class="goods-quantity">x{{ item.quantity }}</text>
          </view>
        </view>
      </view>
      <view class="order-info">
        <view class="info-row">
          <text>订单编号: {{ order.order_no }}</text>
          <text class="copy-btn" @click="copyOrderNo">复制</text>
        </view>
        <text>创建时间: {{ formatDate(order.created_at) }}</text>
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
      </view>
    </view>

    <view class="footer-bar">
      <view
        v-if="order.status === 1"
        class="action-btn"
      >
        <u-button type="success" text="去支付" @click="onPay" />
      </view>
      <view
        v-if="order.status === 1"
        class="action-btn"
      >
        <u-button text="取消订单" @click="onCancel" />
      </view>
      <view
        v-if="canReceive(order)"
        class="action-btn"
      >
        <u-button type="success" :text="receiveButtonText(order)" @click="onReceive" />
      </view>
      <view
        v-if="[2, 3, 4].includes(order.status)"
        class="action-btn"
      >
        <u-button text="申请退款" @click="onRefund" />
      </view>
    </view>

    <!-- 申请退款弹窗 -->
    <RefundModal v-model:show="refundVisible" :order-id="orderId" @success="loadOrder" />
    <!-- 取消订单弹窗（需选择原因） -->
    <CancelOrderModal v-model:show="cancelVisible" :order-id="orderId" @success="loadOrder" />
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onLoad, onUnload, onHide, onShow } from '@dcloudio/uni-app'
import { getOrderDetail, confirmReceive } from '@/api/order'
import { getPaymentChannels } from '@/api/payment'
import { formatPrice, formatDate } from '@/utils/format'
import { notifyPayOutcome, payOrder } from '@/utils/pay'
import { goOrderList } from '@/utils/navigate'
import { joinRegionAddress } from '@/utils/region'
import { canReceive, receiveButtonText, DELIVERY_TYPE_PICKUP } from '@/utils/order'
import RefundModal from '@/components/RefundModal.vue'
import CancelOrderModal from '@/components/CancelOrderModal.vue'
import type { Order, PaymentChannel } from '@/api/types'

const orderId = ref(0)
const refundVisible = ref(false)
const cancelVisible = ref(false)
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

const statusTip = computed(() => {
  if (order.value.status === 1) {
    return `请在 ${remainingText.value} 内完成支付`
  }
  if (isPickup.value) {
    const map: Record<number, string> = {
      2: '商家备货中，备好后请到自提点取货',
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

onLoad((query) => {
  orderId.value = Number(query?.id) || 0
  if (orderId.value) {
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
  if (orderId.value) {
    loadOrder()
  }
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

/**
 * 支付订单
 * 获取可用支付渠道后创建支付单并拉起支付：货到付款直接完成，
 * 微信支付在小程序端调起 JSAPI、H5 端跳转支付页面，未开通渠道提示暂未开通；
 * 仅在支付真实完成时提示支付成功
 */
async function onPay() {
  try {
    const channelRes = await getPaymentChannels()
    const channels = channelRes.data?.channels || []
    if (!channels.length) {
      uni.showToast({ title: '暂无可用支付方式', icon: 'none' })
      return
    }
    let channel: PaymentChannel | null = null
    if (channels.length === 1) {
      channel = channels[0]
    } else {
      const res = await uni.showActionSheet({
        itemList: channels.map((item) => item.name),
      })
      channel = channels[res.tapIndex]
    }
    if (!channel) return
    const outcome = await payOrder(order.value.id, channel.channel)
    notifyPayOutcome(outcome)
    loadOrder()
  } catch {
    // request 已统一提示错误
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

.pay-label {
  font-size: 28rpx;
  color: #333333;
}

.pay-amount {
  font-size: 32rpx;
  font-weight: bold;
  color: #ff4d4f;
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
