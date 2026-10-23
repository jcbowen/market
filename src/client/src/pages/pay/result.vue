<template>
  <view class="pay-result-page">
    <!-- 支付成功 -->
    <view v-if="viewState === 'paid'" class="result-card">
      <view class="result-icon">
        <u-icon name="checkmark-circle-fill" size="64" color="#07c160" />
      </view>
      <text class="result-title">支付成功</text>
      <text class="result-amount">{{ formatPrice(amount) }}</text>
      <text class="result-tip">感谢您的购买，商品将尽快为您安排</text>
      <view class="result-actions">
        <u-button
          v-if="orderId && userStore.isLogin"
          plain
          type="success"
          text="查看订单"
          @click="goOrder"
        />
        <u-button type="success" text="回到首页" @click="goHome" />
      </view>
    </view>

    <!-- 已关闭 / 已退款 -->
    <view v-else-if="viewState === 'closed' || viewState === 'refunded'" class="result-card">
      <view class="result-icon">
        <u-icon name="info-circle-fill" size="64" color="#ff9900" />
      </view>
      <text class="result-title">{{ viewState === 'refunded' ? '该笔支付已退款' : '支付单已关闭' }}</text>
      <text class="result-tip">{{ viewState === 'refunded' ? '退款将原路退回，请注意查收' : '订单超时未支付已自动取消，如需购买请重新下单' }}</text>
      <view class="result-actions">
        <u-button type="success" text="回到首页" @click="goHome" />
      </view>
    </view>

    <!-- 查询失败 / 支付单不存在 -->
    <view v-else-if="viewState === 'error'" class="result-card">
      <view class="result-icon">
        <u-icon name="close-circle-fill" size="64" color="#ff4d4f" />
      </view>
      <text class="result-title">无法获取支付结果</text>
      <text class="result-tip">{{ errorMessage || '支付单不存在或网络异常，请稍后重试' }}</text>
      <view class="result-actions">
        <u-button plain type="info" text="重新查询" @click="refresh" />
        <u-button type="success" text="回到首页" @click="goHome" />
      </view>
    </view>

    <!-- 支付失败：渠道明确返回失败（status=3），终态不再轮询 -->
    <view v-else-if="viewState === 'failed'" class="result-card">
      <view class="result-icon">
        <u-icon name="close-circle-fill" size="64" color="#ff4d4f" />
      </view>
      <text class="result-title">支付失败</text>
      <text class="result-tip">本次支付未完成。若您已被扣款，请勿重复支付，可点击重新查询或联系客服核实</text>
      <view class="result-actions">
        <u-button plain type="info" text="重新查询" @click="refresh" />
        <u-button type="success" text="回到首页" @click="goHome" />
      </view>
    </view>

    <!-- 待支付：轮询等待结果 -->
    <view v-else class="result-card">
      <view class="result-icon pending-icon">
        <u-icon name="hourglass" size="56" color="#ff9900" />
      </view>
      <text class="result-title">{{ firstLoaded ? '未支付' : '正在确认支付结果…' }}</text>
      <text class="result-amount">{{ firstLoaded ? formatPrice(amount) : '' }}</text>
      <text class="result-tip">
        {{ firstLoaded ? '尚未查询到支付成功记录，若您已完成支付，结果将自动更新，无需重复付款' : '如已完成支付，请稍候，结果将自动更新' }}
      </text>
      <view class="result-actions">
        <u-button plain type="info" text="刷新状态" @click="refresh" />
        <u-button type="success" text="回到首页" @click="goHome" />
      </view>
    </view>
  </view>
</template>

<script setup lang="ts">
/**
 * 支付结果页（免登录）
 * 支付宝完成支付后回跳的落地页：凭支付单号轮询公开状态接口展示结果。
 * 不要求登录态——回跳发生在系统浏览器，无本站登录态；
 * 已登录用户（微信内回到本页）额外提供「查看订单」入口
 */
import { ref, computed, onUnmounted } from 'vue'
import { onLoad, onShow, onHide } from '@dcloudio/uni-app'
import { getPaymentStatusPublic } from '@/api/payment'
import { useUserStore } from '@/stores/user'
import { formatPrice } from '@/utils/format'

/** 轮询间隔（毫秒）：回调到账通常在秒级，2.5s 足够跟手又不打扰 */
const POLL_INTERVAL = 2500
/** 轮询上限时长（毫秒）：超时后停止自动轮询，保留手动刷新 */
const POLL_MAX_DURATION = 10 * 60 * 1000

const userStore = useUserStore()
const paymentNo = ref('')
const orderId = ref(0)
const status = ref(0)
const amount = ref(0)
const firstLoaded = ref(false)
const errorMessage = ref('')
const viewState = computed<'loading' | 'pending' | 'paid' | 'failed' | 'closed' | 'refunded' | 'error'>(() => {
  if (!firstLoaded.value) return 'loading'
  // -1 为查询失败哨兵值（支付单不存在/网络异常），须先于状态分发判定
  if (status.value === -1) return 'error'
  switch (status.value) {
    case 2:
      return 'paid'
    case 3:
      // 渠道明确失败：终态必须渲染失败态，落入 default 的 pending 文案会误导用户继续等待
      return 'failed'
    case 4:
      return 'refunded'
    case 5:
      return 'closed'
    default:
      return 'pending'
  }
})

let pollTimer: ReturnType<typeof setInterval> | null = null
let pollStartAt = 0

onLoad((query) => {
  // 支付单号优先取显式参数；支付宝回跳会在 URL 上追加 out_trade_no，两者取其一
  paymentNo.value = query?.no ? decodeURIComponent(query.no) : (query?.out_trade_no || '')
  orderId.value = Number(query?.order_id) || 0
  checkOnce()
})

onShow(() => {
  startPolling()
})

onHide(() => {
  stopPolling()
})

onUnmounted(() => {
  stopPolling()
})

/**
 * 手动刷新：重置轮询窗口并立即查询一次
 */
function refresh() {
  pollStartAt = Date.now()
  checkOnce()
  startPolling()
}

/**
 * 启动自动轮询（页面可见时才轮询；终态与超时后停止）
 */
function startPolling() {
  if (!paymentNo.value || pollTimer) return
  if (isTerminal()) return
  if (!pollStartAt) pollStartAt = Date.now()
  pollTimer = setInterval(() => {
    if (isTerminal() || Date.now() - pollStartAt > POLL_MAX_DURATION) {
      stopPolling()
      return
    }
    checkOnce()
  }, POLL_INTERVAL)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/**
 * 是否已到终态（终态后不再轮询）
 */
function isTerminal(): boolean {
  return firstLoaded.value && status.value !== 1
}

/**
 * 查询一次支付状态；失败静默（下一轮继续），连续失败不弹全局提示
 */
async function checkOnce() {
  if (!paymentNo.value) {
    firstLoaded.value = true
    errorMessage.value = '缺少支付单号'
    status.value = -1
    stopPolling()
    return
  }
  try {
    const res = await getPaymentStatusPublic(paymentNo.value)
    const data = res.data
    if (!data) {
      // 理论不出现：接口 0 码必有 data
      return
    }
    status.value = data.status
    amount.value = data.amount || 0
    if (!orderId.value) orderId.value = data.order_id || 0
    errorMessage.value = ''
    firstLoaded.value = true
  } catch (err) {
    // 首次查询即失败（含支付单不存在）直接进错误态并停轮询；
    // 轮询中的瞬时网络异常保持当前态，下一轮自动重试
    if (!firstLoaded.value) {
      errorMessage.value = err instanceof Error ? err.message : ''
      status.value = -1
      firstLoaded.value = true
      stopPolling()
    }
  }
}

function goOrder() {
  if (!orderId.value) return
  uni.redirectTo({ url: `/pages/order/detail?id=${orderId.value}` })
}

function goHome() {
  // tabBar 页用 switchTab，redirectTo 不支持
  uni.switchTab({ url: '/pages/category/category' })
}
</script>

<style scoped>
.pay-result-page {
  min-height: 100vh;
  padding: 60rpx 30rpx;
  background-color: #f5f5f5;
  box-sizing: border-box;
}

.result-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 80rpx 40rpx 60rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.result-icon {
  margin-bottom: 30rpx;
}

.pending-icon :deep(.u-icon) {
  animation: pulse 1.2s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

.result-title {
  font-size: 36rpx;
  font-weight: bold;
  color: #333333;
}

.result-amount {
  margin-top: 16rpx;
  font-size: 44rpx;
  font-weight: bold;
  color: #ff4d4f;
  min-height: 60rpx;
}

.result-tip {
  margin-top: 16rpx;
  font-size: 26rpx;
  line-height: 1.6;
  color: #999999;
  text-align: center;
}

.result-actions {
  display: flex;
  gap: 24rpx;
  margin-top: 60rpx;
  width: 100%;
}

/* uniapp H5 的 button 默认块级撑满整行，双按钮需收缩横排 */
.result-actions :deep(.u-button) {
  flex: 1;
  width: auto;
}
</style>
