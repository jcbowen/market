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
        <u-button type="success" :text="fromWxH5 ? '返回微信' : '回到首页'" @click="goBack" />
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
        <u-button type="success" :text="fromWxH5 ? '返回微信' : '回到首页'" @click="goBack" />
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
        <u-button type="success" :text="fromWxH5 ? '返回微信' : '回到首页'" @click="goBack" />
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
        <u-button type="success" :text="fromWxH5 ? '返回微信' : '回到首页'" @click="goBack" />
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
        <!-- #ifdef H5 -->
        <!-- 继续支付：仅待支付确认后展示，凭当前页参数直接唤起支付宝，避免回微信绕行反复横跳 -->
        <u-button v-if="firstLoaded && paymentNo" type="primary" text="继续支付" @click="continuePay" />
        <!-- #endif -->
        <u-button plain type="info" text="刷新状态" @click="refresh" />
        <u-button type="success" :text="fromWxH5 ? '返回微信' : '回到首页'" @click="goBack" />
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
 *
 * 微信内发起的支付（支付宝回跳 URL 带 from=wxh5，由引导页换链时编入）：
 * 回跳必然落在无登录态的系统浏览器，「回到首页」会进未登录首页，
 * 故所有状态把该按钮替换为「返回微信」——weixin:// 拉起微信（按钮点击手势内触发，iOS 可用），
 * 用户切回微信后，微信内停留的引导页 onShow 自动转为完成页
 */
import { ref, computed, onUnmounted } from 'vue'
import { onLoad, onShow, onHide } from '@dcloudio/uni-app'
import { getAlipayPayUrlPublic, getPaymentStatusPublic, ALIPAY_FROM_WX_H5 } from '@/api/payment'
import { useUserStore } from '@/stores/user'
import { formatPrice } from '@/utils/format'
import { decodeQueryValue, queryNumber } from '@/utils/query'

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
// 微信内发起的支付：支付宝回跳 URL 携带 from=wxh5（服务端白名单编入，非用户可随意触发的分支）
const fromWxH5 = ref(false)
// 继续支付防抖：换链请求进行中禁止重复点击
const paying = ref(false)
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
  paymentNo.value = query?.no ? decodeQueryValue(query.no) : (query?.out_trade_no || '')
  orderId.value = queryNumber(query, 'order_id')
  fromWxH5.value = query?.from === ALIPAY_FROM_WX_H5
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
  uni.redirectTo({ url: `/pagesOrder/order/detail?id=${orderId.value}` })
}

/**
 * 继续支付：待支付状态下凭当前页支付单号重新换取收银台链接并跳转（仅 H5 编译）
 * 场景：支付宝收银台中途退出（quit_url）回跳本页后，若回微信再绕「在浏览器打开」
 * 会反复横跳；此刻用户已在系统浏览器，直接唤起支付宝即可闭环。
 * 参数复用当前页 no/from（带 wxh5 时再次回跳仍为「返回微信」按钮，闭环一致）；
 * 换取瞬间支付单可能已被回调落账（轮询间隙）：接口返回非待支付则仅同步本地状态，不再跳转；
 * 换链失败留在本页 toast 提示，轮询继续
 */
async function continuePay() {
  // #ifdef H5
  if (!paymentNo.value || paying.value) return
  paying.value = true
  uni.showLoading({ title: '正在唤起支付宝…' })
  try {
    const res = await getAlipayPayUrlPublic(paymentNo.value, fromWxH5.value ? ALIPAY_FROM_WX_H5 : '')
    // 换取瞬间已落账/已关闭：同步状态进终态展示，不再跳转收银台
    if (res.data?.status && res.data.status !== 1) {
      status.value = res.data.status
      firstLoaded.value = true
      return
    }
    if (res.data?.alipay_pay_url) {
      window.location.href = res.data.alipay_pay_url
      return
    }
    uni.showToast({ title: '支付链接获取失败，请稍后重试', icon: 'none' })
  } catch {
    uni.showToast({ title: '支付链接获取失败，请稍后重试', icon: 'none' })
  } finally {
    uni.hideLoading()
    paying.value = false
  }
  // #endif
}

/**
 * 底部主操作分发：微信内发起的支付（from=wxh5）回跳落在无登录态的系统浏览器，
 * 回未登录首页体验差，改为拉起微信；其余场景保持回首页
 */
function goBack() {
  if (fromWxH5.value) {
    goWeixin()
    return
  }
  goHome()
}

/**
 * 返回微信：以 weixin:// scheme 拉起微信（须在用户点击手势内触发，iOS 才放行），
 * 用户切回微信后，微信内停留的引导页 onShow 会自动查询并转为完成页。
 * scheme 拉起成败无回调可感知：点击后短窗内页面未转入后台即视为拉起失败，提示手动切换
 */
function goWeixin() {
  // #ifdef H5
  let switched = false
  const onHidden = () => {
    switched = true
  }
  document.addEventListener('visibilitychange', onHidden, { once: true })
  window.location.href = 'weixin://'
  setTimeout(() => {
    document.removeEventListener('visibilitychange', onHidden)
    if (!switched && document.visibilityState === 'visible') {
      uni.showToast({ title: '未打开微信？请手动切换到微信查看订单', icon: 'none' })
    }
  }, 2000)
  // #endif
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
