<template>
  <view class="pay-guide-page">
    <!-- 加载中 / 正在跳转 -->
    <view v-if="loading" class="guide-card">
      <view class="guide-loading">
        <u-icon name="hourglass" size="34" color="#1677ff" />
      </view>
      <text class="guide-title">正在跳转到支付宝…</text>
    </view>

    <!-- 换取链接失败：异常兜底，引导用户返回重试 -->
    <view v-else-if="!payUrl" class="guide-card">
      <view class="guide-error-icon">
        <u-icon name="close-circle" size="42" color="#ff9900" />
      </view>
      <text class="guide-error-text">{{ errorMsg || '支付链接获取失败，请返回重新发起支付' }}</text>
      <view class="result-entry-error" @click="goResult">
        <text class="result-entry-text">查看支付结果</text>
        <u-icon name="arrow-right" size="14" color="#999999" />
      </view>
    </view>

    <template v-else>
      <!-- 非微信环境：引导页只作跳转中转，正常不会停留在此 -->
      <view v-if="!inWeixin" class="guide-card">
        <text class="guide-title">正在跳转到支付宝…</text>
      </view>

      <!-- 微信内：分步指引 + 复制链接兜底 -->
      <template v-else>
        <view class="guide-card">
          <view class="guide-header">
            <view class="guide-logo">
              <text class="guide-logo-text">支</text>
            </view>
            <text class="guide-title">请在浏览器中打开支付宝完成支付</text>
          </view>
          <view class="guide-steps">
            <view class="guide-step">
              <text class="step-no">1</text>
              <text class="step-text">点击右上角「···」</text>
            </view>
            <view class="guide-step">
              <text class="step-no">2</text>
              <text class="step-text">选择「在浏览器中打开」，将自动唤起支付宝</text>
            </view>
            <view class="guide-step">
              <text class="step-no">3</text>
              <text class="step-text">或复制下方链接，粘贴到浏览器打开完成支付</text>
            </view>
          </view>
          <u-button type="primary" text="复制支付链接" @click="onCopy" />
          <view class="guide-foot">
            <text>完成支付后回到微信，点击下方入口查看结果</text>
          </view>
        </view>

        <view class="result-entry" @click="goResult">
          <text class="result-entry-text">已完成支付？查看支付结果</text>
          <u-icon name="arrow-right" size="14" color="#999999" />
        </view>
      </template>
    </template>
  </view>
</template>

<script setup lang="ts">
/**
 * 支付宝支付引导页（免登录）
 * 独立页面而非蒙层的原因：微信内「在浏览器打开」打开的是当前页 URL，
 * 引导页不依赖登录态，在系统浏览器打开时凭支付单号实时换取收银台链接后自动唤起支付宝，
 * 全程无需重新登录；支付结果由结果页（pages/pay/result）免登录轮询展示
 *
 * 链接获取口径：本页 URL 只携带支付单号，不携带支付链接本身——
 * 支付链接含 # 等保留字符，经 uni-app H5 路由 query 传递会被框架编解码破坏
 * （实测 ? 会被改写为 =，与编码层数无关），导致支付宝报 missing-method
 */
import { ref } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import { isWeixinBrowser } from '@/utils/platform'
import { getAlipayPayUrlPublic } from '@/api/payment'
// #ifdef H5
import { copyTextToClipboard } from '@/utils/pay'
// #endif

const payUrl = ref('')
const paymentNo = ref('')
const orderId = ref(0)
const inWeixin = ref(false)
const loading = ref(true)
const errorMsg = ref('')

onLoad((query) => {
  paymentNo.value = query?.no || ''
  orderId.value = Number(query?.order_id) || 0
})

onShow(async () => {
  inWeixin.value = isWeixinBrowser()
  await fetchPayUrlAndGo()
})

/**
 * 凭支付单号换取支付宝收银台链接并按环境/状态分流：
 * 拿到链接且非微信环境 → 直接跳转收银台；微信内 → 展示指引与复制入口；
 * 支付单非待支付（已支付/已关闭等）→ 自动转入结果页展示状态；换取失败 → 展示兜底文案
 */
async function fetchPayUrlAndGo() {
  if (!paymentNo.value) {
    loading.value = false
    errorMsg.value = '缺少支付单号，请返回重新发起支付'
    return
  }
  try {
    const res = await getAlipayPayUrlPublic(paymentNo.value)
    // 已支付/已关闭/已退款等非待支付状态：转入结果页展示真实状态
    if (res.data?.status && res.data.status !== 1) {
      goResult()
      return
    }
    payUrl.value = res.data?.alipay_pay_url || ''
    if (!payUrl.value) {
      errorMsg.value = '支付链接获取失败，请返回重新发起支付'
      return
    }
    // 系统浏览器打开本页（微信内「在浏览器打开」的落点）：自动唤起支付宝收银台
    if (!inWeixin.value) {
      redirectToAlipay()
      return
    }
    loading.value = false
  } catch {
    loading.value = false
    errorMsg.value = '支付链接获取失败，请返回重新发起支付'
  }
}

/**
 * 跳转支付宝收银台（仅 H5 有意义，非 H5 环境不会进入本页）
 */
function redirectToAlipay() {
  // #ifdef H5
  window.location.href = payUrl.value
  // #endif
}

/**
 * 复制支付链接：H5 走兼容实现（含微信 http 场景），其他端走 uni 封装
 */
function onCopy() {
  // #ifdef H5
  copyTextToClipboard(payUrl.value).then((ok) => {
    uni.showToast({ title: ok ? '链接已复制，请粘贴到浏览器打开' : '复制失败，请按上方步骤操作', icon: 'none' })
  })
  // #endif
  // #ifndef H5
  uni.setClipboardData({
    data: payUrl.value,
    success: () => {
      uni.showToast({ title: '链接已复制', icon: 'none' })
    },
  })
  // #endif
}

/**
 * 已完成支付？进入免登录结果页轮询支付状态
 */
function goResult() {
  const params = new URLSearchParams()
  if (paymentNo.value) params.append('no', paymentNo.value)
  if (orderId.value) params.append('order_id', String(orderId.value))
  const qs = params.toString()
  uni.redirectTo({ url: `/pages/pay/result${qs ? '?' + qs : ''}` })
}
</script>

<style scoped>
.pay-guide-page {
  min-height: 100vh;
  padding: 40rpx 30rpx;
  background-color: #f5f5f5;
  box-sizing: border-box;
}

.guide-card {
  padding: 50rpx 40rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.guide-header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20rpx;
  margin-bottom: 40rpx;
}

.guide-logo {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 88rpx;
  height: 88rpx;
  border-radius: 50%;
  background-color: #1677ff;
}

.guide-logo-text {
  font-size: 40rpx;
  font-weight: bold;
  color: #ffffff;
}

.guide-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
  text-align: center;
}

.guide-steps {
  display: flex;
  flex-direction: column;
  gap: 24rpx;
  margin-bottom: 50rpx;
}

.guide-step {
  display: flex;
  align-items: flex-start;
  gap: 16rpx;
}

.step-no {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36rpx;
  height: 36rpx;
  border-radius: 50%;
  background-color: #1677ff;
  font-size: 22rpx;
  color: #ffffff;
}

.step-text {
  font-size: 28rpx;
  line-height: 36rpx;
  color: #333333;
}

.guide-foot {
  margin-top: 24rpx;
  font-size: 24rpx;
  color: #999999;
  text-align: center;
}

.guide-loading {
  display: flex;
  justify-content: center;
  margin-bottom: 24rpx;
}

.guide-error-icon {
  display: flex;
  justify-content: center;
  margin-bottom: 20rpx;
}

.guide-error-text {
  display: block;
  font-size: 28rpx;
  color: #666666;
  text-align: center;
}

.result-entry-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8rpx;
  margin-top: 30rpx;
}

.result-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 24rpx;
  padding: 30rpx 40rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.result-entry-text {
  font-size: 28rpx;
  color: #07c160;
}
</style>
