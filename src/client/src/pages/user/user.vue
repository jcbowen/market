<template>
  <view class="user-page">
    <!-- 用户信息卡片 -->
    <view class="user-card">
      <view v-if="userStore.isLogin" class="user-info">
        <image class="avatar" :src="userStore.userInfo?.avatar || '/static/default-avatar.png'" mode="aspectFill" />
        <view class="info">
          <text class="nickname">{{ userStore.userInfo?.nickname || '用户' }}</text>
          <text class="phone">{{ maskPhone(userStore.userInfo?.phone || '') }}</text>
        </view>
      </view>
      <view v-else class="user-info" @click="goLogin">
        <image class="avatar" src="/static/default-avatar.png" mode="aspectFill" />
        <view class="info">
          <text class="nickname">点击登录</text>
          <text class="phone">登录后享受更多服务</text>
        </view>
      </view>
    </view>

    <!-- 我的订单 -->
    <view class="menu-card">
      <view class="menu-title">
        <text>我的订单</text>
        <text class="more" @click="goOrderList()">全部订单 &gt;</text>
      </view>
      <view class="order-grid">
        <view class="order-item" @click="goOrderList(1)">
          <u-icon name="rmb-circle" size="40" color="#07c160" />
          <text>待支付</text>
        </view>
        <view class="order-item" @click="goOrderList(2)">
          <u-icon name="bag" size="40" color="#07c160" />
          <text>待发货</text>
        </view>
        <view class="order-item" @click="goOrderList(3)">
          <u-icon name="car" size="40" color="#07c160" />
          <text>待收货</text>
        </view>
        <view class="order-item" @click="goOrderList(9)">
          <u-icon name="server-man" size="40" color="#07c160" />
          <text>退款/售后</text>
        </view>
      </view>
    </view>

    <!-- 功能列表 -->
    <view class="menu-card">
      <u-cell-group>
        <u-cell icon="map" title="收货地址" @click="goAddressList" />
        <u-cell icon="coupon" title="优惠券包" @click="goCoupon" />
        <u-cell icon="clock" title="限时秒杀" @click="goFlash" />
        <u-cell icon="tags" title="满减活动" @click="goFullReduction" />
        <u-cell icon="home" title="切换社区" @click="goCommunitySelect" />
      </u-cell-group>
    </view>

    <!-- 热门商品 -->
    <view class="product-section">
      <text class="section-title">热门商品</text>
      <view class="product-grid">
        <view
          v-for="item in products"
          :key="item.id"
          class="product-card"
          @click="goProductDetail(item.id)"
        >
          <image class="product-image" :src="item.main_image || '/static/logo.png'" mode="aspectFill" />
          <view class="product-info">
            <text class="product-name">{{ item.name }}</text>
            <text class="product-price">{{ formatPrice(item.price) }}</text>
            <text v-if="item.original_price > 0" class="product-original">{{ formatPrice(item.original_price) }}</text>
          </view>
        </view>
      </view>
      <u-loadmore :status="loadStatus" />
    </view>

    <view v-if="userStore.isLogin" class="logout-btn">
      <!-- u-button 默认阻止事件冒泡（stop: true），click 必须绑在组件上而非外层 view -->
      <!-- 显式指定样式（plain 透明底 + 固定红色描边），不依赖 uview 默认卡片背景变量，规避深色模式漂移 -->
      <u-button text="退出登录" plain type="error" @click="onLogout" />
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow, onReachBottom } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getProducts } from '@/api/product'
import { formatPrice, maskPhone } from '@/utils/format'
import {
  goLogin,
  goOrderList,
  goAddressList,
  goCoupon,
  goCommunitySelect,
  goFlash,
  goFullReduction,
  goProductDetail,
  goHome,
} from '@/utils/navigate'
import type { Product } from '@/api/types'

const userStore = useUserStore()

const products = ref<Product[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

onShow(() => {
  userStore.fetchUserInfo()
  page.value = 1
  products.value = []
  // 重置加载状态，避免上次 nomore/loading 门禁导致 onShow 重新加载时直接 return
  loadStatus.value = 'loadmore'
  loadProducts()
})

onReachBottom(() => {
  loadProducts()
})

/**
 * 加载热门商品列表
 */
async function loadProducts() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    const res = await getProducts({
      page: page.value,
      page_size: pageSize,
      community_id: userStore.communityInfo?.id,
    })
    const list = res.data?.list || []
    if (page.value === 1) {
      products.value = list
    } else {
      products.value.push(...list)
    }
    if (list.length < pageSize) {
      loadStatus.value = 'nomore'
    } else {
      loadStatus.value = 'loadmore'
      page.value += 1
    }
  } catch {
    loadStatus.value = 'loadmore'
  }
}

/**
 * 退出登录
 */
function onLogout() {
  uni.showModal({
    title: '提示',
    content: '确定退出登录吗？',
    success: (res) => {
      if (res.confirm) {
        userStore.logout()
        goHome()
      }
    },
  })
}
</script>

<style scoped>
.user-page {
  min-height: 100vh;
  padding-bottom: 140rpx;
  background-color: #f5f5f5;
}

.user-card {
  padding: 40rpx 30rpx;
  background: linear-gradient(135deg, #07c160, #10b981);
}

.user-info {
  display: flex;
  align-items: center;
}

.avatar {
  width: 120rpx;
  height: 120rpx;
  border-radius: 50%;
  background-color: #ffffff;
}

.info {
  display: flex;
  flex-direction: column;
  margin-left: 24rpx;
}

.nickname {
  font-size: 36rpx;
  font-weight: bold;
  color: #ffffff;
}

.phone {
  margin-top: 8rpx;
  font-size: 26rpx;
  color: rgba(255, 255, 255, 0.8);
}

.menu-card {
  margin: 20rpx 30rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
  overflow: hidden;
}

.menu-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24rpx 30rpx;
  font-size: 30rpx;
  color: #333333;
  border-bottom: 1rpx solid #f5f5f5;
}

.more {
  font-size: 26rpx;
  color: #999999;
}

.order-grid {
  display: flex;
  padding: 30rpx 0;
}

.order-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10rpx;
  font-size: 24rpx;
  color: #666666;
}

.logout-btn {
  margin: 40rpx 30rpx;
}

.logout-btn .u-button {
  width: 100%;
}

.product-section {
  margin: 0 30rpx;
}

.section-title {
  display: block;
  margin: 20rpx 0;
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.product-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 20rpx;
}

.product-card {
  width: calc(50% - 10rpx);
  border-radius: 16rpx;
  background-color: #ffffff;
  overflow: hidden;
}

.product-image {
  width: 100%;
  height: 320rpx;
}

.product-info {
  padding: 16rpx;
}

.product-name {
  display: block;
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.product-price {
  display: inline-block;
  margin-top: 12rpx;
  font-size: 30rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.product-original {
  display: inline-block;
  margin-left: 12rpx;
  font-size: 24rpx;
  color: #999999;
  text-decoration: line-through;
}
</style>
