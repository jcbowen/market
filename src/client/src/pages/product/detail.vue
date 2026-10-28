<template>
  <view class="detail-page">
    <!-- 轮播图 -->
    <swiper class="banner" indicator-dots autoplay circular>
      <swiper-item v-for="(img, idx) in product.images" :key="idx">
        <image class="banner-image" :src="img" mode="aspectFill" @click="previewGallery(idx)" />
      </swiper-item>
      <swiper-item v-if="!product.images.length">
        <image class="banner-image" src="/static/logo.png" mode="aspectFill" />
      </swiper-item>
    </swiper>

    <!-- 价格与名称 -->
    <view class="info-card">
      <view class="price-row">
        <text class="price">{{ formatPrice(activePrice) }}</text>
        <text v-if="product.original_price > 0" class="original">{{ formatPrice(product.original_price) }}</text>
      </view>
      <text class="name">{{ product.name }}</text>
      <view class="badge-row">
        <text v-if="product.show_sales" class="sales">已售 {{ product.sales || 0 }}</text>
        <text v-if="fsBadge" :class="['fs-badge', { excluded: fsBadgeExcluded }]">{{ fsBadge }}</text>
      </view>
    </view>

    <!-- 规格选择 -->
    <view class="sku-card" @click="showSkuPopup = true">
      <text class="label">规格</text>
      <text class="value">{{ skuCardText }}</text>
      <u-icon name="arrow-right" size="14" color="#999999" />
    </view>

    <!-- 商品详情：富文本优先（图文混排），无富文本回退纯文本描述 -->
    <view class="desc-card">
      <text class="desc-title">商品详情</text>
      <rich-text v-if="product.content" class="desc-rich" :nodes="product.content" />
      <text v-else class="desc-content">{{ product.description || '暂无详情' }}</text>
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
      <!-- 事件必须直接绑在 u-button 上：其 stop 属性默认 true 会拦截冒泡，外层 view 收不到点击 -->
      <view class="action-btn">
        <u-button type="warning" text="加入购物车" @click="onAddCart" />
      </view>
      <view class="action-btn">
        <u-button type="success" text="立即购买" @click="onBuy" />
      </view>
    </view>

    <!-- SKU 弹窗 -->
    <u-popup :show="showSkuPopup" mode="bottom" @close="showSkuPopup = false">
      <view class="sku-popup">
        <view class="sku-header">
          <image class="sku-image" :src="activeImage" mode="aspectFill" />
          <view class="sku-info">
            <text class="sku-price">{{ formatPrice(activePrice) }}</text>
            <text class="sku-stock">库存 {{ activeStock }}</text>
          </view>
          <u-icon name="close" size="20" color="#999999" @click="showSkuPopup = false" />
        </view>
        <!-- 多规格：按规格树渲染选择器 -->
        <view v-for="spec in product.specs" :key="spec.id" class="sku-list">
          <text class="sku-title">{{ spec.name }}</text>
          <view class="sku-options">
            <text
              v-for="value in spec.values"
              :key="value.id"
              class="sku-option"
              :class="{ active: selectedValueIds[spec.id] === value.id }"
              @click="selectValue(spec.id, value.id)"
            >
              {{ value.name }}
            </text>
          </view>
        </view>
        <view class="sku-quantity">
          <text>数量</text>
          <u-number-box v-model="quantity" :min="1" :max="activeStock || 1" integer />
        </view>
        <view class="sku-confirm-btn">
          <u-button type="success" :text="isMultiSpec && !matchedSku ? '请选择完整规格' : '确定'" @click="confirmSku" />
        </view>
      </view>
    </u-popup>
  </view>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getProductDetail, getActiveFreeShipping, type FreeShippingActivity } from '@/api/product'
import { addCartItem } from '@/api/cart'
import { formatPrice } from '@/utils/format'
import { ensureCommunity } from '@/utils/community'
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
  content: '',
  skus: [],
  specs: [],
  sales: 0,
  show_sales: true,
  is_multi_spec: false,
  min_buy: 1,
})
const selectedSku = ref<Sku>({
  id: 0,
  product_id: 0,
  name: '',
  price: 0,
  original_price: 0,
  stock: 0,
  image: '',
  spec_name: '',
  spec_json: '{}',
  spec_value_ids: '',
})
/** 多规格已选值：规格 ID -> 规格值 ID */
const selectedValueIds = ref<Record<number, number>>({})
const quantity = ref(1)
const showSkuPopup = ref(false)

onLoad(async (query) => {
  productId.value = Number(query?.id) || 0
  if (!productId.value) return
  // 详情接口按社区归属校验（P2 C2）：分享深链冷启动时社区可能尚未就绪，
  // 先兜底选定当前浏览社区再拉详情，否则详情会直接按参数错误失败
  try {
    await ensureCommunity(userStore)
  } catch {
    return
  }
  loadProduct()
  loadFreeShippingBadge()
})

/**
 * 相册大图预览：从点击的图片进入，左右滑动浏览全部相册
 * @param idx 点击的轮播图下标
 */
function previewGallery(idx: number) {
  const urls = product.value.images
  if (!urls.length) return
  uni.previewImage({ urls, current: idx })
}

// --- 满额免配送费角标 ---
const fsActivity = ref<FreeShippingActivity | null>(null)

/**
 * 加载当前社区正在生效的免配送费活动（无活动时角标不展示）
 */
async function loadFreeShippingBadge() {
  try {
    const res = await getActiveFreeShipping({ community_id: userStore.communityInfo?.id })
    fsActivity.value = res.data || null
  } catch {
    fsActivity.value = null
  }
}

/** 本商品是否被活动排除（命中排除分类或排除商品） */
const fsBadgeExcluded = computed(() => {
  const act = fsActivity.value
  if (!act) return false
  return (act.scopes || []).some(
    (s) =>
      (s.scope_type === 4 && s.category_id === product.value.category_id) ||
      (s.scope_type === 5 && s.product_id === product.value.id)
  )
})

/** 角标文案：参与显示门槛，被排除显示不参与，无活动不展示 */
const fsBadge = computed(() => {
  const act = fsActivity.value
  if (!act) return ''
  if (fsBadgeExcluded.value) return '不参与免配送费'
  return `满${formatPrice(act.threshold)}免配送费`
})

/** 是否多规格商品 */
const isMultiSpec = computed(() => product.value.is_multi_spec && product.value.specs.length > 0)

/** 当前选中的组合键（规格值 ID 升序逗号串，与后端 spec_value_ids 一致） */
const selectedKey = computed(() =>
  Object.values(selectedValueIds.value)
    .sort((a, b) => a - b)
    .join(',')
)

/** 规格是否已全部选择 */
const allSelected = computed(() =>
  isMultiSpec.value && product.value.specs.every((s) => selectedValueIds.value[s.id])
)

/**
 * 当前匹配的 SKU
 * 单规格为唯一 SKU；多规格需全部选完才能按组合键匹配
 */
const matchedSku = computed<Sku | null>(() => {
  const skus = product.value.skus
  if (!skus.length) return null
  if (!isMultiSpec.value) return skus[0]
  if (!allSelected.value) return null
  return skus.find((s) => s.spec_value_ids === selectedKey.value) || null
})

// 注意用三目而非 ||：SKU 价格/库存为 0 时是有效值，不应回退到商品汇总字段
const activePrice = computed(() => (matchedSku.value ? matchedSku.value.price : product.value.price))
const activeStock = computed(() => (matchedSku.value ? matchedSku.value.stock : product.value.stock))
const activeImage = computed(
  () => matchedSku.value?.image || product.value.main_image || '/static/logo.png'
)

/** 规格卡片展示文案 */
const skuCardText = computed(() => {
  if (!isMultiSpec.value) return matchedSku.value?.spec_name || '默认规格'
  if (matchedSku.value) return matchedSku.value.spec_name
  const names = product.value.specs
    .map((s) => s.values.find((v) => v.id === selectedValueIds.value[s.id])?.name)
    .filter(Boolean)
  return names.length ? names.join(' / ') : '请选择规格'
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
      // 多规格默认选中每个规格的第一个值并定位 SKU
      if (res.data.is_multi_spec && res.data.specs.length) {
        const init: Record<number, number> = {}
        for (const spec of res.data.specs) {
          if (spec.values.length) {
            init[spec.id] = spec.values[0].id
          }
        }
        selectedValueIds.value = init
        if (matchedSku.value) {
          selectedSku.value = matchedSku.value
        }
      }
    }
  } catch {
    // ignore
  }
}

/**
 * 选择规格值，组合完整时同步定位 SKU
 * @param specId 规格 ID
 * @param valueId 规格值 ID
 */
function selectValue(specId: number, valueId: number) {
  selectedValueIds.value = { ...selectedValueIds.value, [specId]: valueId }
  if (matchedSku.value) {
    selectedSku.value = matchedSku.value
  }
}

/**
 * 弹窗确定：组合完整才允许关闭
 */
function confirmSku() {
  if (isMultiSpec.value && !matchedSku.value) {
    uni.showToast({ title: '请选择完整规格', icon: 'none' })
    return
  }
  if (matchedSku.value) {
    selectedSku.value = matchedSku.value
  }
  showSkuPopup.value = false
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
  if (!matchedSku.value) {
    showSkuPopup.value = true
    return
  }
  selectedSku.value = matchedSku.value
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
  if (!matchedSku.value) {
    showSkuPopup.value = true
    return
  }
  selectedSku.value = matchedSku.value
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

/* 全站商品图统一 1:1 方形口径（首页/购物车/订单等均方容器），
   swiper 宽度默认 100% = 750rpx，高度同步为 750rpx，
   方图不再被 aspectFill 裁切（旧 600rpx 高的 5:4 容器会纵向裁掉 25%） */
.banner {
  height: 750rpx;
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

.badge-row {
  display: flex;
  align-items: center;
  gap: 16rpx;
  margin-top: 12rpx;
}

.badge-row .sales {
  margin-top: 0;
}

/* 免配送费角标：绿色小标签；被排除时灰色 */
.fs-badge {
  padding: 2rpx 12rpx;
  border-radius: 6rpx;
  font-size: 20rpx;
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.08);
}

.fs-badge.excluded {
  color: #999999;
  background-color: #f5f5f5;
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

/* 富文本详情：rich-text 子节点由服务端注入 max-width 内联样式，容器侧仅兜底换行 */
.desc-rich {
  display: block;
  width: 100%;
  font-size: 28rpx;
  color: #666666;
  line-height: 1.6;
  word-break: break-all;
}

.footer-bar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  /* 非 tabBar 页：fixed bottom:0 在带 Home Indicator 的设备上会被底部安全区遮挡，
     加 env(safe-area-inset-bottom) 让操作按钮避开底部安全区（H5/Android 该值为 0 无影响） */
  padding: 16rpx 20rpx calc(16rpx + env(safe-area-inset-bottom));
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
