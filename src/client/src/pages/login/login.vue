<template>
  <view class="login-page">
    <!-- #ifdef H5 -->
    <view class="nav-bar">
      <view class="nav-back" @click="onBack">
        <u-icon name="arrow-left" size="18" color="#333333" />
        <text>返回</text>
      </view>
      <text class="nav-title">登录</text>
      <view class="nav-placeholder" />
    </view>
    <!-- #endif -->

    <view class="logo-wrap">
      <image class="logo" src="/static/logo.png" mode="aspectFit" />
      <text class="title">社区商超</text>
    </view>

    <!-- #ifdef H5 || MP-WEIXIN -->
    <view class="form-wrap">
      <u-input
        v-model="phone"
        placeholder="请输入手机号"
        type="number"
        maxlength="11"
        border="surround"
        clearable
        @confirm="onPhoneLogin"
      />
      <u-button type="success" text="登录" :loading="loading" @click="onPhoneLogin" />
    </view>
    <!-- #endif -->

    <!-- #ifdef APP-PLUS -->
    <view class="app-wrap">
      <u-button type="success" text="App 登录" @click="onAppLogin" />
      <text class="tip">App 端请接入微信/手机号 SDK 后使用</text>
    </view>
    <!-- #endif -->
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { phoneLogin } from '@/api/auth'
import { addCartItem } from '@/api/cart'
import { getAllLocalCartItems, clearLocalCart } from '@/utils/localCart'

const userStore = useUserStore()
const phone = ref('')
const loading = ref(false)
const redirect = ref('')

onLoad((query) => {
  redirect.value = query?.redirect || ''
})

/**
 * 手机号登录
 * 当前阶段仅校验手机号格式即可登录/注册，不进行短信验证码校验
 */
async function onPhoneLogin() {
  if (phone.value.length !== 11) {
    uni.showToast({ title: '请输入正确手机号', icon: 'none' })
    return
  }
  loading.value = true
  try {
    const res = await phoneLogin(phone.value)
    await handleLoginSuccess(res.data)
  } finally {
    loading.value = false
  }
}

/**
 * App 端登录占位提示
 */
function onAppLogin() {
  uni.showToast({ title: 'App 登录待接入', icon: 'none' })
}

/**
 * 返回上一页，上一页不存在时回首页
 */
function onBack() {
  const pages = getCurrentPages()
  if (pages.length > 1) {
    uni.navigateBack({})
  } else {
    uni.reLaunch({ url: '/pages/index/index' })
  }
}

/**
 * 处理登录成功
 * @param data 登录响应数据
 */
async function handleLoginSuccess(data?: {
  access_token: string
  user: {
    id: number
    nickname: string
    avatar: string
    phone: string
    community_id: number
    community_name?: string
  }
}) {
  if (!data) return
  const { access_token, user } = data
  userStore.login(access_token, {
    id: user.id,
    nickname: user.nickname,
    avatar: user.avatar,
    phone: user.phone,
    community_id: user.community_id,
    community_name: user.community_name,
  })

  // 拉取用户绑定社区的完整信息（含 delivery_fee/min_order_amount），
  // 无条件执行：登录响应里的 community_name 只够展示，缺运费与起送门槛字段
  await userStore.syncBoundCommunity()

  // 登录成功后，将本地购物车数据同步到服务端
  await syncLocalCartToServer()
  uni.showToast({ title: '登录成功', icon: 'success' })
  const decodedRedirect = redirect.value ? decodeURIComponent(redirect.value) : ''
  if (decodedRedirect.startsWith('/pages')) {
    uni.redirectTo({ url: decodedRedirect })
  } else {
    uni.reLaunch({ url: '/pages/index/index' })
  }
}

/**
 * 将本地购物车同步到服务端
 * 按社区分组，依次调用加购接口，完成后清空本地购物车
 */
async function syncLocalCartToServer() {
  const localCart = getAllLocalCartItems()
  const communityIds = Object.keys(localCart).map(Number)
  if (!communityIds.length) return
  for (const communityId of communityIds) {
    const items = localCart[communityId] || []
    for (const item of items) {
      try {
        await addCartItem(item.product_id, item.sku_id, item.quantity, communityId)
      } catch {
        // 单条同步失败不影响其他商品
      }
    }
  }
  clearLocalCart()
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  padding: 60rpx 40rpx;
  background-color: #ffffff;
  box-sizing: border-box;
}

.nav-bar {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 88rpx;
  padding: 0 30rpx;
  background-color: #ffffff;
  box-sizing: border-box;
}

.nav-back {
  display: flex;
  align-items: center;
  min-width: 120rpx;
  font-size: 28rpx;
  color: #333333;
}

.nav-back text {
  margin-left: 8rpx;
}

.nav-title {
  flex: 1;
  text-align: center;
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.nav-placeholder {
  min-width: 120rpx;
}

.logo-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 80rpx;
  margin-bottom: 80rpx;
}

.logo {
  width: 160rpx;
  height: 160rpx;
  margin-bottom: 24rpx;
}

.title {
  font-size: 40rpx;
  font-weight: bold;
  color: #333333;
}

.form-wrap {
  display: flex;
  flex-direction: column;
  gap: 30rpx;
}

.login-btn-wrap {
  width: 100%;
}

.code-row {
  display: flex;
  align-items: center;
  gap: 20rpx;
}

.code-row .u-input {
  flex: 1;
}

.code-btn {
  width: 200rpx;
}

.app-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 30rpx;
}

.tip {
  font-size: 24rpx;
  color: #999999;
}
</style>
