<template>
  <view class="index-page">
    <!-- 顶部社区与搜索 -->
    <view class="header">
      <view class="community-bar" @click="goCommunitySelect">
        <u-icon name="map-fill" size="18" color="#07c160" />
        <text class="community-name">{{ communityName }}</text>
        <u-icon name="arrow-down" size="12" color="#999999" />
      </view>
      <view class="search-bar" @click="onSearch">
        <u-icon name="search" size="16" color="#999999" />
        <text class="search-text">搜索商品</text>
      </view>
    </view>

    <!-- 首页 Banner -->
    <view class="banner-section">
      <u-swiper
        :list="bannerImages"
        showTitle
        height="320rpx"
        radius="16"
        @click="onBannerClick"
      />
    </view>

    <!-- 分类宫格 -->
    <view class="category-section">
      <u-grid :col="5" :border="false">
        <u-grid-item
          v-for="item in categories"
          :key="item.id"
          @click="navigateCategory(item.id)"
        >
          <u-icon name="grid-fill" size="36" color="#07c160" />
          <text class="category-name">{{ item.name }}</text>
        </u-grid-item>
      </u-grid>
    </view>

    <!-- 营销入口 -->
    <view class="marketing-section">
      <view class="marketing-item flash" @click="goFlash">
        <text class="title">限时秒杀</text>
        <text class="sub">每日好价</text>
      </view>
      <view class="marketing-item coupon" @click="goCoupon">
        <text class="title">领券中心</text>
        <text class="sub">大额优惠券</text>
      </view>
      <view class="marketing-item full" @click="goFullReduction">
        <text class="title">满减活动</text>
        <text class="sub">多买多省</text>
      </view>
    </view>

    <!-- 商品列表 -->
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
            <text class="product-original">{{ formatPrice(item.original_price) }}</text>
          </view>
        </view>
      </view>
      <u-loadmore :status="loadStatus" />
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getBanners, getCategories, getProducts } from '@/api/product'
import { formatPrice } from '@/utils/format'
import {
  goCommunitySelect,
  goCategory as navigateCategory,
  goProductDetail,
  goCoupon,
  goFlash,
  goFullReduction,
} from '@/utils/navigate'
import type { Banner, Category, Product } from '@/api/types'

const userStore = useUserStore()
const communityName = computed(() => userStore.communityInfo?.name || '选择社区')

const categories = ref<Category[]>([])
const products = ref<Product[]>([])
const banners = ref<Banner[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

/** u-swiper 需要的图片列表 */
const bannerImages = computed(() =>
  banners.value.map((item) => ({
    url: item.image_url,
    title: item.title,
    link: item.link_url,
  })),
)

onShow(() => {
  loadCategories()
  loadBanners()
  page.value = 1
  products.value = []
  // 重置加载状态，避免上次 nomore/loading 门禁导致 onShow 重新加载时直接 return
  loadStatus.value = 'loadmore'
  loadProducts()
})

/**
 * 加载商品分类
 */
async function loadCategories() {
  try {
    const res = await getCategories({ parent_id: 0 })
    categories.value = res.data || []
  } catch {
    categories.value = []
  }
}

/**
 * 加载首页 Banner
 */
async function loadBanners() {
  try {
    const res = await getBanners({
      community_id: userStore.communityInfo?.id,
      position: 1,
    })
    banners.value = res.data || []
  } catch {
    banners.value = []
  }
}

/**
 * 点击 Banner 跳转
 * @param index 当前点击的 Banner 索引
 */
function onBannerClick(index: number) {
  const link = bannerImages.value[index]?.link
  if (!link) return
  if (link.startsWith('/pages')) {
    uni.navigateTo({ url: link })
  } else if (link.startsWith('http')) {
    // #ifdef H5
    window.open(link, '_blank')
    // #endif
    // #ifndef H5
    uni.showToast({ title: '暂不支持外部链接', icon: 'none' })
    // #endif
  }
}

/** 请求序号：防止快速切换页面时旧响应覆盖新数据 */
let productReqSeq = 0

/**
 * 加载商品列表
 */
async function loadProducts() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  const seq = ++productReqSeq
  try {
    const res = await getProducts({
      page: page.value,
      page_size: pageSize,
      community_id: userStore.communityInfo?.id,
    })
    if (seq !== productReqSeq) return
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
    if (seq !== productReqSeq) return
    loadStatus.value = 'loadmore'
  }
}

/**
 * 点击搜索栏占位
 * 跳转分类页，由分类页提供搜索能力
 */
function onSearch() {
  navigateCategory()
}

</script>

<style scoped>
.index-page {
  min-height: 100vh;
  padding-bottom: 40rpx;
  background-color: #f5f5f5;
}

.header {
  padding: 20rpx 30rpx;
  background-color: #ffffff;
}

.community-bar {
  display: flex;
  align-items: center;
  margin-bottom: 16rpx;
}

.community-name {
  margin: 0 8rpx;
  font-size: 28rpx;
  color: #333333;
}

.search-bar {
  display: flex;
  align-items: center;
  padding: 16rpx 24rpx;
  border-radius: 32rpx;
  background-color: #f5f5f5;
}

.search-text {
  margin-left: 12rpx;
  font-size: 26rpx;
  color: #999999;
}

.banner-section {
  margin: 20rpx 30rpx 0;
}

.category-section {
  margin-top: 20rpx;
  padding: 20rpx 0;
  background-color: #ffffff;
}

.category-name {
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #666666;
}

.marketing-section {
  display: flex;
  gap: 16rpx;
  margin: 20rpx 30rpx;
}

.marketing-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.marketing-item .title {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.marketing-item .sub {
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #999999;
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
