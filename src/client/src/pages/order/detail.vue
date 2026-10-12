<template>
  <view class="order-detail-page">
    <view class="status-card">
      <text class="status-text">{{ order.status_text }}</text>
      <text class="status-tip">{{ statusTip }}</text>
    </view>

    <view class="address-card">
      <view class="address-top">
        <text class="name">{{ order.address?.contact_name }}</text>
        <text class="phone">{{ order.address?.contact_phone }}</text>
      </view>
      <text class="address-text">
        {{ order.address?.province }}{{ order.address?.city }}{{ order.address?.district }}{{ order.address?.detail }}
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
        <text>订单编号: {{ order.order_no }}</text>
        <text>创建时间: {{ formatDate(order.created_at) }}</text>
        <text>支付方式: {{ order.pay_type_text || '未支付' }}</text>
      </view>
      <view class="amount-row">
        <text>商品总额: {{ formatPrice(order.total_amount) }}</text>
        <text class="pay-amount">实付: {{ formatPrice(order.pay_amount) }}</text>
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
        v-if="order.status === 4"
        class="action-btn"
      >
        <u-button type="success" text="确认收货" @click="onReceive" />
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
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { getOrderDetail, cancelOrder, confirmReceive } from '@/api/order'
import { getPaymentChannels, getPaymentDetail } from '@/api/payment'
import { formatPrice, formatDate } from '@/utils/format'
import { notifyPayOutcome, payOrder } from '@/utils/pay'
import { goOrderList } from '@/utils/navigate'
import RefundModal from '@/components/RefundModal.vue'
import type { Order, PaymentChannel } from '@/api/types'

const orderId = ref(0)
const refundVisible = ref(false)
const order = ref<Order>({
  id: 0,
  order_no: '',
  status: 0,
  status_text: '',
  total_amount: 0,
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

const statusTip = computed(() => {
  const map: Record<number, string> = {
    1: '请在规定时间内完成支付',
    2: '商家正在备货中',
    4: '商品正在配送中',
    5: '订单已完成',
    9: '退款/售后处理中',
  }
  return map[order.value.status] || ''
})

onLoad((query) => {
  orderId.value = Number(query?.id) || 0
  if (orderId.value) {
    loadOrder()
  }
})

/**
 * 加载订单详情
 */
async function loadOrder() {
  try {
    const res = await getOrderDetail(orderId.value)
    if (res.data) {
      order.value = res.data
      await loadPayTypeText()
    }
  } catch {
    // ignore
  }
}

/**
 * 加载支付方式展示文本
 * 未支付订单直接显示"未支付"；已支付订单查询支付单获取真实支付渠道
 */
async function loadPayTypeText() {
  if (order.value.status === 1) {
    order.value.pay_type_text = '未支付'
    return
  }
  try {
    const res = await getPaymentDetail(order.value.id)
    const payTypeMap: Record<number, string> = {
      1: '微信支付',
      2: '支付宝支付',
      3: '货到付款',
    }
    order.value.pay_type_text = payTypeMap[res.data?.pay_type] || '在线支付'
  } catch {
    order.value.pay_type_text = '在线支付'
  }
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
 * 取消订单
 */
async function onCancel() {
  try {
    await cancelOrder(order.value.id)
    uni.showToast({ title: '已取消', icon: 'success' })
    loadOrder()
  } catch {
    // ignore
  }
}

/**
 * 确认收货
 */
async function onReceive() {
  try {
    await confirmReceive(order.value.id)
    uni.showToast({ title: '已确认收货', icon: 'success' })
    loadOrder()
  } catch {
    // ignore
  }
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
  padding-bottom: 120rpx;
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

.amount-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20rpx 0;
  font-size: 28rpx;
  color: #666666;
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
