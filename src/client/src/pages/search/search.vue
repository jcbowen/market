<template>
  <view class="search-page">
    <!-- 顶部搜索栏：系统默认导航栏处理返回/状态栏，页面本身不显示社区 -->
    <view class="search-header">
      <view class="search-input-wrap">
        <u-icon name="search" size="16" color="#999999" />
        <input
          v-model="keyword"
          class="search-input native-input"
          placeholder="搜索商品或条形码"
          type="search"
          focus
          confirm-type="search"
          @confirm="onSearch"
        />
      </view>
      <view class="search-btn" @click="onSearch">搜索</view>
    </view>

    <!-- 列表区：初始态展示搜索历史 + 热销推荐，搜索态展示结果（两态互斥，卡片结构共用） -->
    <view class="search-result">
      <scroll-view class="search-result-list" scroll-y>
        <template v-if="!hasSearched">
          <!-- 搜索历史：无历史时整块不渲染 -->
          <view v-if="searchHistory.length" class="history-section">
            <view class="section-header">
              <text class="section-title">搜索历史</text>
              <u-icon name="trash" size="18" color="#999999" @click="onClearHistory" />
            </view>
            <view class="history-tags">
              <view
                v-for="word in searchHistory"
                :key="word"
                class="history-tag"
                @click="onHistoryClick(word)"
              >
                {{ word }}
              </view>
            </view>
          </view>
          <!-- 热销推荐：销量降序兜底内容，加载失败或无数据时不显示标题 -->
          <view v-if="hotProducts.length" class="section-header hot-header">
            <text class="section-title">热销推荐</text>
          </view>
        </template>
        <view v-if="hasSearched && searchLoading" class="empty-tip">搜索中...</view>
        <view v-else-if="hasSearched && !displayProducts.length" class="empty-tip">未找到相关商品</view>
        <view
          v-for="item in displayProducts"
          :key="item.id"
          class="product-card"
          @click="onProductClick(item)"
        >
          <image class="product-image" :src="item.main_image || '/static/logo.png'" mode="aspectFill" @click.stop="onImageClick(item)" />
          <view class="product-info">
            <text class="product-name">{{ item.name }}</text>
            <text v-if="item.show_sales" class="product-sales">已售 {{ item.sales || 0 }}</text>
            <view class="product-bottom">
              <text class="product-price">{{ formatPrice(item.price) }}</text>
              <view class="add-btn" @click.stop="onAddClick(item)">
                <u-icon name="plus-circle-fill" size="44" color="#07c160" />
              </view>
            </view>
          </view>
        </view>
        <view class="list-bottom-spacer" />
      </scroll-view>
    </view>

    <!-- 多规格商品弹窗（与首页共用公共规格选择组件） -->
    <sku-selector
      v-model:show="showSkuPopup"
      :product="currentProduct"
      :community-id="userStore.communityInfo?.id"
      @confirm="onSkuConfirm"
    />
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getProductDetail, getProducts, searchProducts } from '@/api/product'
import { addCartItem } from '@/api/cart'
import { addLocalCartItem } from '@/utils/localCart'
import { decodeQueryValue } from '@/utils/query'
import { formatPrice } from '@/utils/format'
import { goProductDetail } from '@/utils/navigate'
import SkuSelector from '@/components/sku-selector.vue'
import type { Product, Sku } from '@/api/types'

const userStore = useUserStore()

const keyword = ref('')
const searchResults = ref<Product[]>([])
const searchLoading = ref(false)
const SEARCH_PAGE_SIZE = 20

/** 是否已执行过搜索：初始态（历史+热销推荐）与结果态的切换开关 */
const hasSearched = ref(false)

/** 搜索历史本地存储 key */
const SEARCH_HISTORY_KEY = 'search_history'
/** 搜索历史上限条数 */
const SEARCH_HISTORY_MAX = 10
const searchHistory = ref<string[]>([])

/** 热销推荐商品（销量降序兜底内容） */
const hotProducts = ref<Product[]>([])

/** 列表展示数据：搜索态展示搜索结果，初始态展示热销推荐（卡片结构共用） */
const displayProducts = computed(() => (hasSearched.value ? searchResults.value : hotProducts.value))

const showSkuPopup = ref(false)
/** 当前加购弹窗商品（搜索结果项，SKU/规格数据由组件内兜底加载） */
const currentProduct = ref<Partial<Product> & { id: number } | null>(null)

onLoad((query) => {
  loadSearchHistory()
  // 首页扫码未命中条码时携带关键词跳入，直接执行搜索
  // 发送端 encodeURIComponent 过，小程序端 onLoad 不保证自动解码，走防御性解码
  const word = decodeQueryValue(query?.keyword).trim()
  if (word) {
    keyword.value = word
    doSearch()
  } else {
    loadHotProducts()
  }
})

/** 点击搜索按钮或键盘确认 */
function onSearch() {
  doSearch()
}

/**
 * 按关键词搜索商品
 * 空关键词时回到初始态（搜索历史 + 热销推荐）
 */
async function doSearch() {
  const word = keyword.value.trim()
  if (!word) {
    hasSearched.value = false
    searchResults.value = []
    if (!hotProducts.value.length) {
      loadHotProducts()
    }
    return
  }
  hasSearched.value = true
  searchLoading.value = true
  try {
    const res = await searchProducts({
      keyword: word,
      page: 1,
      page_size: SEARCH_PAGE_SIZE,
      community_id: userStore.communityInfo?.id,
    })
    searchResults.value = res.data?.list || []
    saveSearchHistory(word)
  } catch {
    searchResults.value = []
  } finally {
    searchLoading.value = false
  }
}

/**
 * 读取本地搜索历史（最新在前）
 * 存储损坏或类型异常时静默降级为空列表
 */
function loadSearchHistory() {
  try {
    const raw = uni.getStorageSync(SEARCH_HISTORY_KEY)
    const list = typeof raw === 'string' ? JSON.parse(raw) : raw
    searchHistory.value = Array.isArray(list) ? list.filter((w) => typeof w === 'string' && w) : []
  } catch {
    searchHistory.value = []
  }
}

/**
 * 写入搜索历史：去重后最新置顶，超出上限截断
 * @param word 搜索关键词
 */
function saveSearchHistory(word: string) {
  const next = [word, ...searchHistory.value.filter((w) => w !== word)].slice(0, SEARCH_HISTORY_MAX)
  searchHistory.value = next
  try {
    uni.setStorageSync(SEARCH_HISTORY_KEY, JSON.stringify(next))
  } catch {
    // 存储不可用时静默降级，仅本次会话内可见
  }
}

/**
 * 点击历史词：回填关键词并立即搜索
 * @param word 历史搜索词
 */
function onHistoryClick(word: string) {
  keyword.value = word
  doSearch()
}

/** 清空搜索历史（本地同步移除，误触成本低无需二次确认） */
function onClearHistory() {
  searchHistory.value = []
  try {
    uni.removeStorageSync(SEARCH_HISTORY_KEY)
  } catch {
    // ignore
  }
}

/**
 * 加载热销推荐商品（销量降序，真实+虚拟销量口径）
 * 失败静默：区块不渲染标题，不影响搜索主流程
 */
async function loadHotProducts() {
  try {
    const res = await getProducts({
      sort: 'sales',
      page: 1,
      page_size: SEARCH_PAGE_SIZE,
      community_id: userStore.communityInfo?.id,
    })
    hotProducts.value = res.data?.list || []
  } catch {
    hotProducts.value = []
  }
}

/**
 * 点击商品卡片
 * 单规格直接加购，多规格弹出选择窗
 * @param product 商品对象
 */
function onProductClick(product: Product) {
  if (product.is_multi_spec) {
    openSkuPopup(product)
  } else {
    addSingleSpecToCart(product)
  }
}

/**
 * 点击商品图片：跳转商品详情页（卡片其余区域维持加购语义）
 * @param product 商品对象
 */
function onImageClick(product: Product) {
  goProductDetail(product.id)
}

/**
 * 点击加号按钮，与点击卡片行为一致
 * @param product 商品对象
 */
function onAddClick(product: Product) {
  onProductClick(product)
}

/**
 * 单规格商品直接加入购物车
 * 数量取商品配置的最低购买份数
 * 未登录时存入本地购物车
 * @param product 商品对象
 */
async function addSingleSpecToCart(product: Product) {
  const sku = await getDefaultSku(product)
  if (!sku) {
    uni.showToast({ title: '商品无可用规格', icon: 'none' })
    return
  }
  await doAddCart(product, sku, product.min_buy || 1)
}

/**
 * 获取商品默认 SKU
 * 单规格商品直接取第一个可用 SKU
 * @param product 商品对象
 * @returns SKU 对象或 null
 */
async function getDefaultSku(product: Product): Promise<Sku | null> {
  if (product.skus.length) {
    return product.skus.find((s) => s.stock > 0) || product.skus[0]
  }
  try {
    const res = await getProductDetail(product.id, userStore.communityInfo?.id)
    if (res.data?.skus?.length) {
      return res.data.skus.find((s) => s.stock > 0) || res.data.skus[0]
    }
  } catch {
    // ignore
  }
  return null
}

/**
 * 打开多规格商品弹窗
 * SKU/规格数据与预选逻辑均由公共 sku-selector 组件内部处理
 * @param product 商品对象（搜索结果项）
 */
function openSkuPopup(product: Product) {
  currentProduct.value = product
  showSkuPopup.value = true
}

/**
 * 规格选择弹窗确认回调：将选中 SKU 加入购物车
 * @param product 组件内兜底加载后的完整商品对象
 * @param sku 选中的 SKU
 * @param quantity 数量
 */
function onSkuConfirm({ product, sku, quantity }: { product: Product; sku: Sku; quantity: number }) {
  showSkuPopup.value = false
  doAddCart(product, sku, quantity)
}

/**
 * 加入购物车
 * 已登录调用服务端接口，未登录存入本地缓存
 * 购物车角标由首页 onShow 刷新，本页无购物车栏
 * @param product 商品对象
 * @param sku SKU 对象
 * @param quantity 数量
 */
async function doAddCart(product: Product, sku: Sku, quantity: number) {
  const minBuy = product.min_buy || 1
  if (quantity < minBuy) {
    uni.showToast({ title: `该商品最低购买 ${minBuy} 份`, icon: 'none' })
    return
  }
  const communityId = userStore.communityInfo?.id
  if (!communityId) {
    uni.showToast({ title: '请先选择社区', icon: 'none' })
    return
  }
  try {
    if (userStore.isLogin) {
      await addCartItem(product.id, sku.id, quantity, communityId)
    } else {
      addLocalCartItem(product, sku, quantity, communityId)
    }
    uni.showToast({ title: '已加入购物车', icon: 'success' })
  } catch {
    // 请求拦截器已统一提示
  }
}
</script>

<style scoped>
.search-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f5f5f5;
  /* #ifdef H5 */
  height: calc(100vh - var(--window-top, 44px));
  /* #endif */
}

.search-header {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 20rpx 30rpx;
}

.search-input-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  height: 72rpx;
  padding: 0 24rpx;
  box-sizing: border-box;
  border-radius: 36rpx;
  background-color: #ffffff;
}

.search-input {
  flex: 1;
  margin-left: 12rpx;
  font-size: 26rpx;
}

.search-btn {
  flex-shrink: 0;
  width: 120rpx;
  height: 72rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 36rpx;
  background-color: #07c160;
  font-size: 26rpx;
  color: #ffffff;
}

.search-result {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.search-result-list {
  flex: 1;
  height: 100%;
  min-height: 0;
  padding: 0 20rpx;
  box-sizing: border-box;
}

.list-bottom-spacer {
  height: calc(40rpx + env(safe-area-inset-bottom));
}

.empty-tip {
  padding: 60rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: #999999;
}

/* 初始态区块：搜索历史 + 热销推荐标题 */
.history-section {
  padding: 20rpx 10rpx 0;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.section-title {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

/* 热销推荐标题与历史区块的间距 */
.hot-header {
  padding: 30rpx 10rpx 20rpx;
}

.history-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  margin-top: 20rpx;
}

.history-tag {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 12rpx 28rpx;
  border-radius: 32rpx;
  background-color: #ffffff;
  font-size: 24rpx;
  color: #666666;
}

/* 商品卡片样式与首页搜索结果一致 */
.product-card {
  display: flex;
  margin-bottom: 20rpx;
  padding: 16rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.product-image {
  width: 180rpx;
  height: 180rpx;
  border-radius: 12rpx;
}

.product-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: 20rpx;
  overflow: hidden;
}

.product-name {
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.product-sales {
  font-size: 22rpx;
  color: #999999;
}

.product-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.product-price {
  font-size: 32rpx;
  font-weight: bold;
  color: #e24b4a;
}

.add-btn {
  display: flex;
  align-items: center;
}
</style>
