<template>
  <view class="detail-page">
    <!-- 轮播图 -->
    <swiper class="banner" indicator-dots autoplay circular>
      <swiper-item v-for="(img, idx) in product.images" :key="idx">
        <image class="banner-image" :src="img" mode="aspectFill" />
      </swiper-item>
      <swiper-item v-if="!product.images.length">
        <image class="banner-image" src="/static/logo.png" mode="aspectFill" />
      </swiper-item>
    </swiper>

    <!-- 价格与名称 -->
    <view class="info-card">
      <view class="price-row">
        <text class="price">{{ formatPrice(product.price) }}</text>
        <text v-if="product.original_price > 0" class="original">{{ formatPrice(product.original_price) }}</text>
      </view>
      <text class="name">{{ product.name }}</text>
      <text class="sales">已售 {{ product.sales || 0 }}</text>
    </view>

    <!-- 规格选择 -->
    <view class="sku-card" @click="showSkuPopup = true">
      <text class="label">规格</text>
      <text class="value">{{ selectedSku.name || '请选择规格' }}</text>
      <u-icon name="arrow-right" size="14" color="#999999" />
    </view>

    <!-- 商品详情 -->
    <view class="desc-card">
      <text class="desc-title">商品详情</text>
      <text class="desc-content">{{ product.description || '暂无详情' }}</text>
    </view>

    <!-- 底部操作栏 -->
    <view class="footer-bar">
      <view class="icon-btn" @click="goHome">
        <u-icon name="home" size="24" color="#666666" />
        <text>首页</text>
      </view>
      <view class="icon-btn" @click="goCart">
        <u-icon name="shopping-cart" size="24" color="#666666" />
        <text>购物车</text>
      </view>
      <view class="action-btn" @click="onAddCart">
        <u-button type="warning" text="加入购物车" />
      </view>
      <view class="action-btn" @click="onBuy">
        <u-button type="success" text="立即购买" />
      </view>
    </view>

    <!-- SKU 弹窗 -->
    <u-popup :show="showSkuPopup" mode="bottom" @close="showSkuPopup = false">
      <view class="sku-popup">
        <view class="sku-header">
          <image class="sku-image" :src="product.main_image || '/static/logo.png'" mode="aspectFill" />
          <view class="sku-info">
            <text class="sku-price">{{ formatPrice(selectedSku.price || product.price) }}</text>
            <text class="sku-stock">库存 {{ selectedSku.stock || product.stock }}</text>
          </view>
          <u-icon name="close" size="20" color="#999999" @click="showSkuPopup = false" />
        </view>
        <view class="sku-list">
          <text class="sku-title">规格</text>
          <view class="sku-options">
            <text
              v-for="sku in product.skus"
              :key="sku.id"
              class="sku-option"
              :class="{ active: selectedSku.id === sku.id }"
              @click="selectSku(sku)"
            >
              {{ sku.name }}
            </text>
          </view>
        </view>
        <view class="sku-quantity">
          <text>数量</text>
          <u-number-box v-model="quantity" :min="1" :max="selectedSku.stock || product.stock" integer />
        </view>
        <view class="sku-confirm-btn" @click="showSkuPopup = false">
          <u-button type="success" text="确定" />
        </view>
      </view>
    </u-popup>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getProductDetail } from '@/api/product'
import { addCartItem } from '@/api/cart'
import { formatPrice } from '@/utils/format'
import { goHome, goOrderConfirm, goLogin } from '@/utils/navigate'
import type { Product, Sku } from '@/api/types'

const userStore = useUserStore()
const productId = ref(0)
const product = ref<Product>({
  id: 0,
  name: '',
  main_image: '',
  images: [],
  price: 0,
  original_price: 0,
  stock: 0,
  category_id: 0,
  category_name: '',
  description: '',
  skus: [],
  sales: 0,
})
const selectedSku = ref<Sku>({
  id: 0,
  product_id: 0,
  name: '',
  price: 0,
  original_price: 0,
  stock: 0,
  image: '',
})
const quantity = ref(1)
const showSkuPopup = ref(false)

onLoad((query) => {
  productId.value = Number(query?.id) || 0
  if (productId.value) {
    loadProduct()
  }
})

/**
 * 加载商品详情
 */
async function loadProduct() {
  try {
    const res = await getProductDetail(productId.value, userStore.communityInfo?.id)
    if (res.data) {
      product.value = res.data
      if (res.data.skus.length) {
        selectedSku.value = res.data.skus[0]
      }
    }
  } catch {
    // ignore
  }
}

/**
 * 选择 SKU
 * @param sku SKU 对象
 */
function selectSku(sku: Sku) {
  selectedSku.value = sku
}

/**
 * 跳转购物车 tab
 */
function goCart() {
  uni.switchTab({ url: '/pages/cart/cart' })
}

/**
 * 加入购物车
 */
async function onAddCart() {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  if (!selectedSku.value.id) {
    showSkuPopup.value = true
    return
  }
  try {
    await addCartItem(product.value.id, selectedSku.value.id, quantity.value, userStore.communityInfo?.id)
    uni.showToast({ title: '已加入购物车', icon: 'success' })
  } catch {
    // ignore
  }
}

/**
 * 立即购买
 */
function onBuy() {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  if (!selectedSku.value.id) {
    showSkuPopup.value = true
    return
  }
  goOrderConfirm({
    productId: product.value.id,
    skuId: selectedSku.value.id,
    quantity: quantity.value,
  })
}
</script>

<style scoped>
.detail-page {
  min-height: 100vh;
  padding-bottom: 120rpx;
  background-color: #f5f5f5;
}

.banner {
  height: 600rpx;
}

.banner-image {
  width: 100%;
  height: 100%;
}

.info-card {
  padding: 30rpx;
  background-color: #ffffff;
}

.price-row {
  display: flex;
  align-items: baseline;
  margin-bottom: 16rpx;
}

.price {
  font-size: 44rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.original {
  margin-left: 16rpx;
  font-size: 26rpx;
  color: #999999;
  text-decoration: line-through;
}

.name {
  display: block;
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.sales {
  display: block;
  margin-top: 12rpx;
  font-size: 24rpx;
  color: #999999;
}

.sku-card {
  display: flex;
  align-items: center;
  margin-top: 20rpx;
  padding: 24rpx 30rpx;
  background-color: #ffffff;
}

.sku-card .label {
  font-size: 28rpx;
  color: #999999;
}

.sku-card .value {
  flex: 1;
  margin: 0 16rpx;
  font-size: 28rpx;
  color: #333333;
}

.desc-card {
  margin-top: 20rpx;
  padding: 30rpx;
  background-color: #ffffff;
}

.desc-title {
  display: block;
  margin-bottom: 16rpx;
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.desc-content {
  font-size: 28rpx;
  color: #666666;
  line-height: 1.6;
}

.footer-bar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  padding: 16rpx 20rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #eeeeee;
}

.icon-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 100rpx;
  font-size: 20rpx;
  color: #666666;
}

.action-btn {
  flex: 1;
  margin: 0 8rpx;
}

.action-btn .u-button,
.sku-confirm-btn .u-button {
  width: 100%;
}

.sku-confirm-btn {
  width: 100%;
}

.sku-popup {
  padding: 30rpx;
}

.sku-header {
  display: flex;
  align-items: center;
  margin-bottom: 30rpx;
}

.sku-image {
  width: 160rpx;
  height: 160rpx;
  border-radius: 12rpx;
}

.sku-info {
  flex: 1;
  margin-left: 20rpx;
}

.sku-price {
  display: block;
  font-size: 36rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.sku-stock {
  display: block;
  margin-top: 10rpx;
  font-size: 26rpx;
  color: #999999;
}

.sku-title {
  display: block;
  margin-bottom: 16rpx;
  font-size: 28rpx;
  color: #333333;
}

.sku-options {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  margin-bottom: 30rpx;
}

.sku-option {
  padding: 12rpx 24rpx;
  border-radius: 8rpx;
  font-size: 26rpx;
  color: #666666;
  background-color: #f5f5f5;
}

.sku-option.active {
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
}

.sku-quantity {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 30rpx;
}
</style>
