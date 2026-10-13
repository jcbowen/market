<template>
  <view class="order-list-page">
    <u-tabs
      :list="tabs"
      :current="currentTab"
      @change="onTabChange"
    />
    <scroll-view class="order-list" scroll-y @scrolltolower="loadMore">
      <view
        v-for="order in orders"
        :key="order.id"
        class="order-card"
        @click="goOrderDetail(order.id)"
      >
        <view class="order-header">
          <text class="order-no">订单号: {{ order.order_no }}</text>
          <text class="order-status">{{ order.status_text }}</text>
        </view>
        <view
          v-for="item in order.items"
          :key="`${item.product_id}-${item.sku_id}`"
          class="order-item"
        >
          <image class="item-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
          <view class="item-info">
            <text class="item-name">{{ item.product_name }}</text>
            <text class="item-sku">{{ item.spec_name }}</text>
            <view class="item-bottom">
              <text class="item-price">{{ formatPrice(item.price) }}</text>
              <text class="item-quantity">x{{ item.quantity }}</text>
            </view>
          </view>
        </view>
        <view class="order-total">
          <text>合计: {{ formatPrice(order.pay_amount) }}</text>
        </view>
        <!-- 待支付展示剩余支付时间，归零前提醒用户及时支付 -->
        <view v-if="order.status === 1 && remainingText(order)" class="order-expire">
          <text>{{ remainingText(order) }}后未支付将自动取消</text>
        </view>
        <view class="order-actions">
          <u-button
            v-if="order.status === 1"
            type="success"
            size="small"
            text="去支付"
            @click.stop="onPay(order)"
          />
          <u-button
            v-if="order.status === 1"
            size="small"
            text="取消订单"
            @click.stop="onCancel(order.id)"
          />
          <u-button
            v-if="canReceive(order)"
            type="success"
            size="small"
            :text="receiveButtonText(order)"
            @click.stop="onReceive(order)"
          />
          <u-button
            v-if="[2, 3, 4].includes(order.status)"
            size="small"
            text="申请退款"
            @click.stop="onRefund(order.id)"
          />
        </view>
      </view>
      <u-loadmore :status="loadStatus" />
    </scroll-view>

    <!-- 申请退款弹窗 -->
    <RefundModal v-model:show="refundVisible" :order-id="refundOrderId" @success="refreshList" />
    <!-- 取消订单弹窗（需选择原因） -->
    <CancelOrderModal v-model:show="cancelVisible" :order-id="cancelOrderId" @success="refreshList" />
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onLoad, onShow, onHide } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getOrders, confirmReceive } from '@/api/order'
import { getPaymentChannels } from '@/api/payment'
import { formatPrice } from '@/utils/format'
import { notifyPayOutcome, payOrder } from '@/utils/pay'
import { goOrderDetail, goLogin } from '@/utils/navigate'
import { canReceive, receiveButtonText } from '@/utils/order'
import RefundModal from '@/components/RefundModal.vue'
import CancelOrderModal from '@/components/CancelOrderModal.vue'
import type { Order, PaymentChannel } from '@/api/types'

const userStore = useUserStore()
const refundVisible = ref(false)
const refundOrderId = ref(0)
const cancelVisible = ref(false)
const cancelOrderId = ref(0)
// tab value 说明：0 全部；9 = 退款/售后聚合（后端翻译为 7 退款中 + 8 已退款）；
// 90 = 待发货聚合（后端翻译为 2 已支付 + 3 备货中）；均为查询专用虚拟状态，不落库
const tabs = ref([
  { name: '全部', value: 0 },
  { name: '待支付', value: 1 },
  { name: '待发货', value: 90 },
  { name: '待收货', value: 4 },
  { name: '已完成', value: 5 },
  { name: '退款/售后', value: 9 },
])
const currentTab = ref(0)
const orders = ref<Order[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')
/** 倒计时基准时间（每分钟校准一次，避免列表每秒重渲染） */
const nowTick = ref(Date.now())
let tickTimer: ReturnType<typeof setInterval> | null = null

onLoad((query) => {
  const status = Number(query?.status) || 0
  const index = tabs.value.findIndex((tab) => tab.value === status)
  currentTab.value = index >= 0 ? index : 0
})

onShow(() => {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  resetAndReload()
  // 列表待支付剩余时间每分钟刷新一次
  if (!tickTimer) {
    tickTimer = setInterval(() => {
      nowTick.value = Date.now()
    }, 60 * 1000)
  }
})

onHide(() => {
  if (tickTimer) {
    clearInterval(tickTimer)
    tickTimer = null
  }
})

/**
 * 计算待支付订单剩余支付时间的展示文案
 * @param order 订单（status=1 时 expire_at 有值）
 * @returns 如"29 分钟"；已过期或无截止时间返回空串
 */
function remainingText(order: Order): string {
  if (order.status !== 1 || !order.expire_at) return ''
  const expire = new Date(order.expire_at.replace(' ', 'T')).getTime()
  if (Number.isNaN(expire)) return ''
  const remainMs = expire - nowTick.value
  if (remainMs <= 0) return ''
  const minutes = Math.ceil(remainMs / (60 * 1000))
  if (minutes >= 60) {
    const hours = Math.floor(minutes / 60)
    return `${hours} 小时 ${minutes % 60} 分钟`
  }
  return `${minutes} 分钟`
}

/**
 * 重置分页状态并回到第 1 页刷新（切 tab / onShow 共用）
 */
function resetAndReload() {
  page.value = 1
  orders.value = []
  loadStatus.value = 'loadmore'
  loadOrders()
}

/**
 * 切换订单状态 tab
 * uview-plus u-tabs 的 change 参数为 { ...点击项, index }
 * @param item change 事件参数（含点击索引）
 */
function onTabChange(item: { index: number }) {
  if (item.index === currentTab.value) return
  currentTab.value = item.index
  resetAndReload()
}

/** 请求序号：防止快速切换 tab 时旧响应覆盖新数据 */
let requestSeq = 0

/**
 * 加载订单列表
 */
async function loadOrders() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  const seq = ++requestSeq
  try {
    const status = tabs.value[currentTab.value].value
    const res = await getOrders({
      status: status || undefined,
      page: page.value,
      page_size: pageSize,
      community_id: userStore.communityInfo?.id,
    })
    if (seq !== requestSeq) return
    const list = res.data?.list || []
    if (page.value === 1) {
      orders.value = list
    } else {
      orders.value.push(...list)
    }
    if (list.length < pageSize) {
      loadStatus.value = 'nomore'
    } else {
      loadStatus.value = 'loadmore'
      page.value += 1
    }
  } catch {
    if (seq !== requestSeq) return
    loadStatus.value = 'loadmore'
  }
}

/**
 * 加载更多订单
 */
function loadMore() {
  loadOrders()
}

/**
 * 支付订单
 * 选择渠道后创建支付单并拉起支付，货到付款直接完成，微信按运行端拉起，
 * 仅在支付真实完成时提示支付成功
 * @param order 订单对象
 */
async function onPay(order: Order) {
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
    const outcome = await payOrder(order.id, channel.channel)
    notifyPayOutcome(outcome)
    resetAndReload()
  } catch {
    // request 已统一提示错误
  }
}

/**
 * 取消订单：打开原因选择弹窗（不可逆操作，需明确原因）
 * @param id 订单 ID
 */
function onCancel(id: number) {
  cancelOrderId.value = id
  cancelVisible.value = true
}

/**
 * 确认收货：二次确认后再调接口
 * 完成后退款入口关闭，避免误触
 * @param order 订单对象
 */
function onReceive(order: Order) {
  const tip = order.delivery_type === 1 ? '确认已到点自提？确认后订单将完成' : '确认已收到商品？确认后订单将完成'
  uni.showModal({
    title: receiveButtonText(order),
    content: tip,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await confirmReceive(order.id)
        uni.showToast({ title: '操作成功', icon: 'success' })
        resetAndReload()
      } catch {
        // request 已统一提示错误
      }
    },
  })
}

/**
 * 申请退款：打开金额确认弹窗（选商品/数量、运费策略、可调低金额）
 * @param id 订单 ID
 */
function onRefund(id: number) {
  refundOrderId.value = id
  refundVisible.value = true
}

/**
 * 退款/取消申请提交成功后回到第 1 页刷新列表
 */
function refreshList() {
  resetAndReload()
}
</script>

<style scoped>
.order-list-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f5f5f5;
}

.order-list {
  flex: 1;
  overflow: hidden;
  /* scroll-view 默认 content-box，width:100% + padding 会横向溢出导致页面超宽 */
  box-sizing: border-box;
  padding: 20rpx;
}

.order-card {
  margin-bottom: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.order-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16rpx;
}

.order-no {
  font-size: 24rpx;
  color: #999999;
}

.order-status {
  font-size: 26rpx;
  color: #07c160;
}

.order-item {
  display: flex;
  padding: 16rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
}

.item-image {
  width: 140rpx;
  height: 140rpx;
  border-radius: 12rpx;
}

.item-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: 20rpx;
  height: 140rpx;
}

.item-name {
  font-size: 28rpx;
  color: #333333;
}

.item-sku {
  font-size: 24rpx;
  color: #999999;
}

.item-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.item-price {
  font-size: 28rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.item-quantity {
  font-size: 24rpx;
  color: #999999;
}

.order-total {
  display: flex;
  justify-content: flex-end;
  padding: 16rpx 0;
  font-size: 28rpx;
  color: #333333;
}

.order-expire {
  padding: 0 0 8rpx;
  font-size: 22rpx;
  color: #ff9800;
  text-align: right;
}

.order-actions {
  display: flex;
  justify-content: flex-end;
  gap: 16rpx;
}

/* uniapp H5 的 button 默认块级撑满整行，操作按钮需收缩为小按钮横排 */
.order-actions :deep(.u-button) {
  width: auto;
  min-width: 120rpx;
  padding: 0 28rpx;
}
</style>
