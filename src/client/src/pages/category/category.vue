<template>
  <view class="category-page" :style="{ top: statusBarHeight + 'px' }">
    <!-- 顶部社区与搜索栏 -->
    <view class="top-bar" :style="capsuleReserve ? { right: capsuleReserve + 'px' } : {}">
      <view v-show="!isSearching" class="community-bar" @click="goCommunitySelect">
        <u-icon name="map-fill" size="18" color="#07c160" />
        <text class="community-name">{{ communityName }}</text>
        <u-icon name="arrow-down" size="12" color="#999999" />
      </view>
      <view class="search-bar">
        <u-icon name="search" size="16" color="#999999" />
        <!-- #ifndef MP-WEIXIN -->
        <input
          v-show="isSearching"
          ref="searchInputRef"
          v-model="keyword"
          class="search-input native-input"
          placeholder="请输入商品名称或条形码"
          type="search"
          focus
          confirm-type="search"
          @confirm="onSearchConfirm"
          @blur="onSearchBlur"
        />
        <!-- #endif -->
        <!-- #ifdef MP-WEIXIN || APP-PLUS -->
        <view class="scan-btn" @click.stop="onScanCode">
          <u-icon name="scan" size="20" color="#333333" />
        </view>
        <!-- #endif -->
        <!-- #ifndef MP-WEIXIN -->
        <text v-show="isSearching" class="search-cancel" @click="cancelSearch">取消</text>
        <!-- #endif -->
        <view v-show="!isSearching" class="search-text" @click="startSearch">搜索商品</view>
      </view>
    </view>

    <!-- 分类内容 -->
    <view v-if="!isSearching" class="content">
      <!-- 左侧一级分类 -->
      <scroll-view class="left-menu" scroll-y :scroll-into-view="leftScrollIntoView" scroll-with-animation>
        <view
          v-for="item in categories"
          :id="'menu-' + item.id"
          :key="item.id"
          class="menu-item"
          :class="{ active: activeCategoryId === item.id }"
          @click="selectCategory(item.id)"
        >
          {{ item.name }}
        </view>
      </scroll-view>

      <!-- 右侧商品列表：按分类分区，支持跨分类无缝滚动 -->
      <scroll-view
        class="right-list"
        scroll-y
        :scroll-into-view="rightScrollIntoView"
        :scroll-top="rightScrollTop"
        scroll-with-animation
        @scroll="onRightScroll"
      >
        <!-- 分类页 Banner -->
        <view v-if="bannerImages.length" class="category-banner">
          <u-swiper
            :list="bannerImages"
            height="240rpx"
            radius="16"
            @click="onBannerClick"
          />
        </view>

        <view
          v-for="cat in categories"
          :id="'category-' + cat.id"
          :key="cat.id"
          class="category-section"
        >
          <view class="category-title">{{ cat.name }}</view>
          <view
            v-for="item in categoryProducts[cat.id] || []"
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
          <view v-if="!categoryProducts[cat.id]?.length" class="section-empty">
            该分类暂无商品
          </view>
        </view>

        <!-- 版权与备案号随列表滚动到底部展示，未配置时整块不渲染 -->
        <SiteFooter />

        <!-- 底部占位，避免内容被固定购物车栏遮挡 -->
        <view style="height: 140rpx" />
      </scroll-view>

      <!-- 分类标题吸顶层：scroll-view 为原生组件，position:sticky 在 mp-weixin 不生效，
           故在 scroll-view 之外用绝对定位层随滚动联动显示当前分类名，H5/小程序行为统一 -->
      <view v-show="showStickyTitle" class="sticky-title">{{ activeCategoryName }}</view>
    </view>

    <!-- 搜索结果：小程序端搜索独立成页，页内搜索仅 H5/App 使用 -->
    <!-- #ifndef MP-WEIXIN -->
    <view v-else class="search-result">
      <scroll-view class="search-result-list" scroll-y>
        <view v-if="searchLoading" class="empty-tip">搜索中...</view>
        <view v-else-if="!searchResults.length" class="empty-tip">未找到相关商品</view>
        <view
          v-for="item in searchResults"
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
        <view style="height: 140rpx" />
      </scroll-view>
    </view>
    <!-- #endif -->

    <!-- 底部购物车栏 -->
    <view class="cart-bar">
      <view class="cart-icon" @click="goCart">
        <u-icon name="shopping-cart-fill" size="48" color="#07c160" />
        <view v-if="cartTotalCount > 0" class="cart-badge">{{ cartTotalCount }}</view>
      </view>
      <view class="cart-total">
        <text class="total-label">预计:</text>
        <text class="total-price">{{ formatPrice(cartTotalAmount) }}</text>
      </view>
      <view class="cart-btn">
        <u-button type="success" text="去结算" :disabled="!cartTotalCount" @click="onSettlement" />
      </view>
    </view>

    <!-- 多规格商品弹窗（公共规格选择组件） -->
    <sku-selector
      v-model:show="showSkuPopup"
      :product="currentProduct"
      :community-id="userStore.communityInfo?.id"
      @confirm="onSkuConfirm"
    />

    <!-- 当前社区购物车弹窗 -->
    <u-popup :show="showCartPopup" mode="bottom" @close="closeCartPopup">
      <view class="cart-popup">
        <view class="cart-popup-header">
          <text class="cart-popup-title">已选商品</text>
          <u-icon name="close" size="20" color="#999999" @click="closeCartPopup" />
        </view>

        <scroll-view class="cart-popup-list" scroll-y>
          <view v-if="!cartItems.length" class="cart-popup-empty">购物车是空的</view>
          <view
            v-for="item in cartItems"
            :key="item.sku_id"
            class="cart-popup-item"
            :class="{ 'cart-popup-item-invalid': item.invalid }"
          >
            <image class="cart-popup-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
            <view class="cart-popup-info">
              <text class="cart-popup-name">{{ item.product_name }}</text>
              <text v-if="item.invalid" class="cart-popup-invalid-tag">{{ item.invalid_reason }}</text>
              <text v-else-if="item.sku_total > 1" class="cart-popup-sku" @click="onCartSkuClick(item)">{{ item.spec_name }}</text>
              <text v-if="item.stock_hint" class="cart-popup-stock-hint">{{ item.stock_hint }}</text>
              <text class="cart-popup-price">{{ formatPrice(item.price) }}</text>
            </view>
            <view class="cart-popup-action">
              <u-number-box
                v-if="!item.invalid"
                :modelValue="item.quantity"
                :min="item.min_buy || 1"
                :max="item.stock"
                integer
                @update:modelValue="(val: number) => onPopupQuantityChange(item, val)"
              />
              <u-icon name="trash" size="18" color="#999999" @click="onPopupRemove(item)" />
            </view>
          </view>
        </scroll-view>

        <view class="cart-popup-footer">
          <view class="cart-popup-total">
            <text class="total-label">合计:</text>
            <text class="total-price">{{ formatPrice(cartTotalAmount) }}</text>
          </view>
          <view class="cart-popup-btn">
            <u-button type="success" text="去结算" :disabled="!cartTotalCount" @click="onSettlement" />
          </view>
        </view>
      </view>
    </u-popup>

    <!-- 购物车切换规格弹窗（预选当前规格，确认后替换购物车项） -->
    <sku-selector
      v-model:show="showCartSkuPopup"
      :product="cartSkuProduct"
      :community-id="cartSwitchItem?.community_id || userStore.communityInfo?.id"
      :initial-sku-id="cartSwitchItem?.sku_id || 0"
      :initial-quantity="cartSwitchItem?.quantity || 0"
      @confirm="onCartSkuConfirm"
    />

    <!-- 社区推荐确认弹窗（启动自动选社区时展示） -->
    <community-recommend-dialog />
  </view>
</template>

<script setup lang="ts">
import { ref, computed, getCurrentInstance, nextTick } from 'vue'
import type { Ref } from 'vue'
import { onLoad, onShow, onUnload } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { useCartStore } from '@/stores/cart'
import { getBanners, getCategories, getProducts, getProductByBarcode, getProductDetail, searchProducts } from '@/api/product'
import { addCartItem, getCart, updateCartItem, removeCartItem } from '@/api/cart'
import { ensureCommunity } from '@/utils/community'
import { formatPrice } from '@/utils/format'
import { goOrderConfirm, goLogin, goCommunitySelect, goPage, goProductDetail } from '@/utils/navigate'
import SkuSelector from '@/components/sku-selector.vue'
import SiteFooter from '@/components/SiteFooter.vue'
import CommunityRecommendDialog from '@/components/community-recommend-dialog.vue'
import {
  addLocalCartItem,
  updateLocalCartItem,
  removeLocalCartItem,
  getLocalCartSummary,
  localCartToCartItems,
} from '@/utils/localCart'
import type { Banner, Category, Product, Sku, CartItem } from '@/api/types'

const userStore = useUserStore()
const cartStore = useCartStore()
const instance = getCurrentInstance()

/** 状态栏高度（px）。小程序端 uniapp 注入的 --status-bar-height 固定为 25px（编译期常量），
 *  灵动岛/刘海屏机型真实状态栏更高（约 47~60px），顶栏会被遮挡，需运行时取真实值；
 *  H5 端为 0，维持原 var(--status-bar-height, 0px) 行为 */
const statusBarHeight = ref(0)
/** 小程序端顶栏右侧让出的胶囊按钮预留宽度（px），避免扫码按钮被胶囊遮挡；H5 为 0 不让位 */
const capsuleReserve = ref(0)
// #ifdef MP-WEIXIN
try {
  const windowInfo = uni.getWindowInfo()
  statusBarHeight.value = windowInfo.statusBarHeight || 0
  const menuRect = uni.getMenuButtonBoundingClientRect()
  if (menuRect && menuRect.left > 0) {
    capsuleReserve.value = windowInfo.windowWidth - menuRect.left + 8
  }
} catch {
  // 取不到时保持 0，走 CSS 变量兜底
}
// #endif

const categories = ref<Category[]>([])
const categoryProducts = ref<Record<number, Product[]>>({})
const activeCategoryId = ref(0)
const rightScrollIntoView = ref('')
const rightScrollTop = ref(0)
const leftScrollIntoView = ref('')
const lastCommunityId = ref<number | undefined>(undefined)
const banners = ref<Banner[]>([])
/** 各分类分区的固定内容内偏移（scroll-view 原生滚动不改变该值，配合 scrollTop 可实时计算当前分类） */
const sectionOffsets = ref<{ id: number; top: number; bottom: number }[]>([])
/** 右侧列表（scroll-view）相对页面视口的固定顶部坐标，用于吸顶/高亮联动的阈值判断 */
const listViewportTop = ref(0)
/** measureSections 空结果重试计数，避免数据/渲染未就绪时一次性度量失败导致吸顶/高亮不跟随 */
let measureRetry = 0

/** 分类页 Banner 图片列表 */
const bannerImages = computed(() =>
  banners.value.map((item) => ({
    url: item.image_url,
    link: item.link_url,
  })),
)

/** 顶部社区入口展示名，未选社区时引导选择 */
const communityName = computed(() => userStore.communityInfo?.name || '选择社区')

/** 当前高亮分类名，用于吸顶标题层展示 */
const activeCategoryName = computed(() => {
  const cat = categories.value.find((c) => c.id === activeCategoryId.value)
  return cat?.name || ''
})

const isSearching = ref(false)
/** 右侧列表顶部是否显示吸顶分类名层 */
const showStickyTitle = ref(false)
const keyword = ref('')
const searchResults = ref<Product[]>([])
const searchLoading = ref(false)
const SEARCH_PAGE_SIZE = 20
const searchInputRef: Ref<HTMLInputElement | null> = ref(null)

/** 是否正在响应左侧点击导致的自动滚动，滚动期间禁用右侧滚动联动 */
let isClickScrolling = false
let clickScrollTimer: number | null = null

const cartItems = ref<CartItem[]>([])
const cartTotalCount = ref(0)
const cartTotalAmount = ref(0)
const cartSelectedCount = ref(0)

const showSkuPopup = ref(false)
const showCartPopup = ref(false)
/** 当前加购弹窗商品（分类列表项，SKU/规格数据由组件内兜底加载） */
const currentProduct = ref<Partial<Product> & { id: number } | null>(null)

/** 购物车切换规格：待替换的购物车项与弹窗状态 */
const cartSwitchItem = ref<CartItem | null>(null)
const showCartSkuPopup = ref(false)
/** 切换规格弹窗商品（仅传 ID/名称，详情由组件内兜底加载） */
const cartSkuProduct = computed(() =>
  cartSwitchItem.value
    ? { id: cartSwitchItem.value.product_id, name: cartSwitchItem.value.product_name }
    : null,
)

onLoad((query) => {
  const id = Number(query?.categoryId) || 0
  activeCategoryId.value = id
})

onShow(() => {
  initPage()
})

onUnload(() => {
  if (sectionObserver) {
    try {
      sectionObserver.disconnect()
    } catch {
      // ignore
    }
    sectionObserver = null
  }
})

/**
 * 初始化页面
 * 分类未加载或社区发生变化时重新加载分类与商品；否则只刷新购物车摘要，避免返回时列表闪烁/空白
 */
async function initPage() {
  const communityId = userStore.communityInfo?.id
  const shouldLoad = !categories.value.length || lastCommunityId.value !== communityId
  if (shouldLoad) {
    lastCommunityId.value = communityId
    await loadCategories()
  }
  loadBanners()
  loadCartSummary()
}

/**
 * 加载商品分类
 * 若本地未选中社区，先获取社区列表并默认选中第一个
 */
async function loadCategories() {
  try {
    await ensureCommunity()
    const res = await getCategories({ parent_id: 0, community_id: userStore.communityInfo?.id })
    categories.value = res.data || []
    if (!activeCategoryId.value && categories.value.length) {
      activeCategoryId.value = categories.value[0].id
    }
    await loadAllCategoryProducts()
  } catch {
    categories.value = []
  }
}

/**
 * 加载分类页 Banner
 */
async function loadBanners() {
  try {
    const res = await getBanners({
      community_id: userStore.communityInfo?.id,
      position: 2,
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
  goPage(link)
}

/**
 * 加载所有分类的商品（首屏），用于右侧无缝滚动展示
 */
async function loadAllCategoryProducts() {
  const communityId = userStore.communityInfo?.id
  const list = categories.value
  if (!list.length || !communityId) return
  const map: Record<number, Product[]> = {}
  await Promise.all(
    list.map(async (cat) => {
      try {
        const res = await getProducts({
          category_id: cat.id,
          page: 1,
          page_size: SEARCH_PAGE_SIZE,
          community_id: communityId,
        })
        map[cat.id] = res.data?.list || []
      } catch {
        map[cat.id] = []
      }
    }),
  )
  categoryProducts.value = map
  await nextTick()
  setTimeout(() => {
    observeSections()
    measureSections()
  }, 200)
}

/**
 * 选中左侧分类
 * 右侧列表平滑滚动到对应分类分区，并高亮左侧菜单
 * 点击当前已高亮分类同样触发滚动（用于滚回该分区顶部）
 * @param id 分类 ID
 */
function selectCategory(id: number) {
  if (!id) return
  activeCategoryId.value = id
  leftScrollIntoView.value = `menu-${id}`
  scrollRightToCategory(id)
}

/**
 * 将右侧列表滚动到指定分类分区
 * 非 H5 平台使用 scroll-into-view；H5 平台仅用 scrollTop 驱动：
 * scroll-into-view 与 scroll-top 双机制同时生效会在滚动动画期间互相覆盖，
 * 导致滚动停在途中（实测点击分类后 scrollTop 停在 112/536）
 * @param id 分类 ID
 */
function scrollRightToCategory(id: number) {
  isClickScrolling = true
  // #ifndef H5
  rightScrollIntoView.value = `category-${id}`
  // #endif
  if (clickScrollTimer) {
    clearTimeout(clickScrollTimer)
  }
  clickScrollTimer = setTimeout(() => {
    isClickScrolling = false
    clickScrollTimer = null
    rightScrollIntoView.value = ''
  }, 800) as unknown as number
  // 点击分类必然触发滚动，吸顶标题层应随之显示当前分类名
  showStickyTitle.value = true

  // #ifdef H5
  nextTick(() => {
    if (!instance?.proxy) return
    const query = uni.createSelectorQuery().in(instance.proxy)
    query.select(`#category-${id}`).boundingClientRect()
    query.select('.right-list').boundingClientRect()
    // 结果统一从 exec 回调取（res[2]），此处空回调仅为满足类型签名（callback 必填）
    query.select('.right-list').scrollOffset(() => {})
    query.exec((res) => {
      const sectionRect = res[0] as { top: number } | null
      const listRect = res[1] as { top: number } | null
      const scrollInfo = res[2] as { scrollTop: number } | null
      if (!sectionRect || !listRect || !scrollInfo) return
      const offset = sectionRect.top - listRect.top + scrollInfo.scrollTop
      if (offset >= 0) {
        rightScrollTop.value = offset
      }
    })
  })
  // #endif
}

/**
 * 测量各分类分区的固定内容内偏移。
 * mp-weixin 的 scroll-view 为原生组件，其内部元素 boundingClientRect 相对内容顶部（不随原生滚动变化），
 * 正好作为内容内偏移；H5 端在 scrollTop=0 时获取同样得到初始内容偏移。配合滚动事件的 scrollTop 即可实时判定当前分类，
 * 规避 mp-weixin 下「用 boundingClientRect 计算当前分区」因坐标不随滚动变化而失效的坑。
 */
function measureSections() {
  const query = uni.createSelectorQuery()
  query.select('.right-list').boundingClientRect()
  query.selectAll('.category-section').boundingClientRect()
  query.exec((res) => {
    const listRect = res[0] as { top: number } | null
    if (listRect) listViewportTop.value = listRect.top
    const list = (res[1] || []) as { id?: string; top: number; height: number }[]
    const offsets = list
      .map((s) => ({
        id: Number(s.id?.replace('category-', '')) || 0,
        top: s.top,
        bottom: s.top + s.height,
      }))
      .filter((s) => s.id)
    if (offsets.length) {
      sectionOffsets.value = offsets
      measureRetry = 0
    } else if (measureRetry < 3) {
      // 数据/渲染尚未就绪，延迟重试（如首屏商品接口较慢）
      measureRetry += 1
      setTimeout(() => measureSections(), 400)
    }
  })
}

/**
 * 根据右侧列表滚动位置计算当前所处分类 ID
 * @param scrollTop 当前滚动偏移
 */
function computeStickyCategory(scrollTop: number): number {
  if (!sectionOffsets.value.length) return 0
  const probe = scrollTop + 10
  for (const s of sectionOffsets.value) {
    if (probe >= s.top && probe < s.bottom) return s.id
  }
  // 兜底：取最后一个起始位置不超过探针的分类
  let found = 0
  for (const s of sectionOffsets.value) {
    if (s.top <= probe) found = s.id
  }
  return found
}

/** 右侧列表滚动监听对象 */
let sectionObserver: any = null

/**
 * 使用 IntersectionObserver 监听各分类分区是否到达右侧列表顶部
 * 当某个分区进入顶部可视区域时，联动高亮左侧对应分类
 */
function observeSections() {
  if (sectionObserver) {
    try {
      sectionObserver.disconnect()
    } catch {
      // ignore
    }
  }
  sectionObserver = uni.createIntersectionObserver(null as any, {
    thresholds: [0],
    initialRatio: 0,
  })
  // 注意：mp-weixin 的 scroll-view 是原生组件，relativeTo 一个原生 scroll-view 节点不被支持，
  // 必须用 relativeToViewport 才能正确感知内部节点滚动；回调里用 boundingClientRect.top 与列表视口顶部比较。
  sectionObserver.relativeToViewport().observe('.category-section', (res: any) => {
    if (isClickScrolling) return
    const id = Number(res?.id?.replace('category-', ''))
    if (!id) return
    const sectionTop = res?.boundingClientRect?.top
    if (typeof sectionTop !== 'number') return
    // 当前分区顶部到达右侧列表顶部附近时，视为当前所处分类（阈值与 updateActiveCategoryByScroll 一致）
    if (sectionTop - listViewportTop.value > 20) return
    if (id !== activeCategoryId.value) {
      activeCategoryId.value = id
      leftScrollIntoView.value = `menu-${id}`
    }
  })
}

let scrollTimer: number | null = null
/**
 * 右侧列表滚动时，根据分类分区位置联动更新左侧高亮（兜底方案）
 * @param event 滚动事件
 */
function onRightScroll(e: any) {
  if (isClickScrolling) return
  const scrollTop = e?.detail?.scrollTop ?? 0
  // 仅 H5 回写 scroll-top 驱动程序化滚动（保证重复点击同一分类可滚回分区顶部）；
  // 小程序端禁止回写：:scroll-top 在滚动中被 setData 回写会与手势抢滚动位置，
  // 导致下滑回弹、无法继续滑动。小程序端程序化滚动由 scroll-into-view 承担。
  // #ifdef H5
  rightScrollTop.value = scrollTop
  // #endif
  // 列表滚动时联动吸顶标题层：滚离顶部即显示当前分类名，回到顶部隐藏
  showStickyTitle.value = scrollTop > 0
  // 同步高亮左侧分类（与吸顶标题一致）：用内容内 section 偏移 + 实时 scrollTop 计算，跨端稳定，
  // 规避 mp-weixin 下坐标不随滚动变化导致高亮/吸顶标题不跟随的问题
  const id = computeStickyCategory(scrollTop)
  if (id && id !== activeCategoryId.value) {
    activeCategoryId.value = id
    leftScrollIntoView.value = `menu-${id}`
  }
  if (scrollTimer) return
  scrollTimer = setTimeout(() => {
    scrollTimer = null
    updateActiveCategoryByScroll(scrollTop)
  }, 100) as unknown as number
}

/**
 * 根据右侧滚动位置，计算当前顶部对应的分类 ID
 * 当 IntersectionObserver 不可用时作为兜底
 * @param scrollTop 当前滚动偏移
 */
function updateActiveCategoryByScroll(scrollTop: number) {
  if (!scrollTop || isClickScrolling) return
  if (!instance?.proxy) return
  const query = uni.createSelectorQuery().in(instance.proxy)
  query.selectAll('.category-section').boundingClientRect()
  query.select('.right-list').boundingClientRect()
  query.exec((res) => {
    const sections = (res[0] || []) as { id?: string; top: number }[]
    const listRect = res[1] as { top: number } | null
    if (!listRect) return
    const listTop = listRect.top
    let newId = activeCategoryId.value
    for (const section of sections) {
      if (section.top - listTop <= 20) {
        const id = Number(section.id?.replace('category-', ''))
        if (id) newId = id
      }
    }
    if (newId && newId !== activeCategoryId.value) {
      activeCategoryId.value = newId
      leftScrollIntoView.value = `menu-${newId}`
    }
  })
}

/**
 * 加载购物车摘要，用于底部显示总数量与预计金额
 * 未登录时读取本地购物车，已登录时读取服务端购物车
 */
async function loadCartSummary() {
  const communityId = userStore.communityInfo?.id
  if (!userStore.isLogin) {
    const summary = getLocalCartSummary(communityId)
    cartItems.value = localCartToCartItems(communityId)
    cartTotalCount.value = summary.totalCount
    cartTotalAmount.value = summary.totalAmount
    cartSelectedCount.value = summary.selectedCount
    cartStore.setCount(summary.totalCount, summary.selectedCount)
    return
  }
  try {
    const res = await getCart(communityId)
    if (res.data) {
      cartItems.value = res.data.items || []
      cartTotalCount.value = res.data.total_count || 0
      cartTotalAmount.value = res.data.total_amount || 0
      cartSelectedCount.value = res.data.selected_count || 0
      cartStore.setCount(res.data.total_count, res.data.selected_count)
    }
  } catch {
    cartItems.value = []
    cartTotalCount.value = 0
    cartTotalAmount.value = 0
    cartSelectedCount.value = 0
  }
}

/**
 * 进入搜索
 * 小程序端：搜索框为纯入口，直接跳转独立搜索页（无页内搜索态/动效）；
 * H5/App 端：维持原页内搜索
 * H5 下通过 nextTick + DOM 查询聚焦原生 input，部分浏览器需用户手动点击
 */
function startSearch() {
  // #ifdef MP-WEIXIN
  uni.navigateTo({ url: '/pages/search/search' })
  // #endif
  // #ifndef MP-WEIXIN
  isSearching.value = true
  keyword.value = ''
  searchResults.value = []
  nextTick(() => {
    // #ifdef H5
    setTimeout(() => {
      const inputEl = document.querySelector('.search-input input') as HTMLInputElement | null
      inputEl?.focus()
    }, 50)
    // #endif
  })
  // #endif
}

/**
 * 取消搜索并返回分类视图
 */
function cancelSearch() {
  isSearching.value = false
  keyword.value = ''
  searchResults.value = []
}

/**
 * 搜索框失焦时触发搜索
 */
function onSearchBlur() {
  // 失焦时若关键词为空则清空结果，否则延迟触发搜索
  nextTick(() => {
    if (!keyword.value.trim()) {
      searchResults.value = []
      return
    }
    doSearch()
  })
}

/**
 * 搜索框确认时触发搜索
 */
function onSearchConfirm() {
  doSearch()
}

/**
 * 按关键词搜索商品
 */
async function doSearch() {
  const word = keyword.value.trim()
  if (!word) {
    searchResults.value = []
    return
  }
  searchLoading.value = true
  try {
    const res = await searchProducts({
      keyword: word,
      page: 1,
      page_size: SEARCH_PAGE_SIZE,
      community_id: userStore.communityInfo?.id,
    })
    searchResults.value = res.data?.list || []
  } catch {
    searchResults.value = []
  } finally {
    searchLoading.value = false
  }
}

/**
 * 扫码识别（仅小程序/App 展示入口，H5 无统一扫码 API 故隐藏）
 * 识别结果交给统一处理：条码直达，其余内容当关键词搜索
 */
function onScanCode() {
  uni.scanCode({
    scanType: ['barCode', 'qrCode'],
    success: (res) => {
      const code = (res.result || '').trim()
      if (code) void handleScanResult(code)
    },
  })
}

/**
 * 处理扫码结果：
 * 纯数字条码（6-20 位）走条码精确查询——命中单规格直接加购，多规格弹规格选择窗；
 * 未命中回落为关键词搜索（请求层已提示"未找到该条码对应的商品"）；
 * 非条码内容（如二维码文本）直接当关键词搜索。
 * 小程序端关键词搜索统一跳独立搜索页；H5/App 维持页内搜索
 * @param code 扫码内容
 */
async function handleScanResult(code: string) {
  if (!/^\d{6,20}$/.test(code)) {
    goSearchWithKeyword(code)
    return
  }
  try {
    const res = await getProductByBarcode(code, userStore.communityInfo?.id)
    const { product, sku } = res.data
    if (!product.id) return
    if (product.is_multi_spec) {
      isSearching.value = false
      openSkuPopup(product)
    } else if (sku.stock > 0) {
      await addSingleSpecToCart(product)
    } else {
      uni.showToast({ title: '该商品暂无库存', icon: 'none' })
    }
  } catch {
    // 未命中：请求层已统一提示，携带关键词进搜索页/回填页内搜索保留扫码上下文
    goSearchWithKeyword(code)
  }
}

/**
 * 按关键词进入搜索：小程序端跳独立搜索页，H5/App 回填页内搜索
 * @param word 关键词
 */
function goSearchWithKeyword(word: string) {
  // #ifdef MP-WEIXIN
  uni.navigateTo({ url: `/pages/search/search?keyword=${encodeURIComponent(word)}` })
  // #endif
  // #ifndef MP-WEIXIN
  isSearching.value = true
  keyword.value = word
  doSearch()
  // #endif
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
 * @param product 商品对象（分类列表项）
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
    loadCartSummary()
  } catch {
    // 请求拦截器已统一提示
  }
}

/**
 * 打开当前社区购物车弹窗
 */
function goCart() {
  if (!cartTotalCount.value) {
    uni.showToast({ title: '购物车是空的', icon: 'none' })
    return
  }
  showCartPopup.value = true
}

/**
 * 关闭购物车弹窗
 */
function closeCartPopup() {
  showCartPopup.value = false
}

/**
 * 购物车弹窗中修改商品数量
 * @param item 购物车项
 * @param quantity 新数量
 */
function onPopupQuantityChange(item: CartItem, quantity: number) {
  const communityId = userStore.communityInfo?.id
  if (userStore.isLogin) {
    // 服务端购物车：数量为 0 时删除，否则更新
    if (quantity <= 0) {
      removeCartItem(item.id)
    } else {
      updateCartItem(item.id, { quantity })
    }
  } else {
    // 本地购物车
    updateLocalCartItem(item.sku_id, quantity, communityId)
  }
  loadCartSummary()
}

/**
 * 删除购物车弹窗中的商品
 * @param item 购物车项
 */
function onPopupRemove(item: CartItem) {
  const communityId = userStore.communityInfo?.id
  if (userStore.isLogin) {
    removeCartItem(item.id)
  } else {
    removeLocalCartItem(item.sku_id, communityId)
  }
  loadCartSummary()
  if (!cartTotalCount.value) {
    showCartPopup.value = false
  }
}

/**
 * 点击购物车项规格名，打开切换规格弹窗
 * 预选当前 SKU、数量保持购物车现值（同 SKU 确认等价于数量不变）
 * @param item 购物车项
 */
function onCartSkuClick(item: CartItem) {
  cartSwitchItem.value = item
  showCartSkuPopup.value = true
}

/**
 * 切换规格确认回调：删旧购物车项 + 按新 SKU 重新加购
 * 服务端同 SKU 已存在时自动累加合并；未登录时操作本地购物车。
 * 无论成功与否最后都刷新购物车摘要，保证与服务端状态一致
 * @param product 组件内兜底加载后的完整商品对象
 * @param sku 切换后的 SKU
 * @param quantity 数量
 */
async function onCartSkuConfirm({ product, sku, quantity }: { product: Product; sku: Sku; quantity: number }) {
  const item = cartSwitchItem.value
  if (!item) return
  const communityId = userStore.communityInfo?.id
  try {
    if (userStore.isLogin) {
      await removeCartItem(item.id)
      await addCartItem(product.id, sku.id, quantity, communityId)
    } else {
      removeLocalCartItem(item.sku_id, communityId)
      addLocalCartItem(product, sku, quantity, communityId)
    }
    showCartSkuPopup.value = false
  } catch {
    // 请求拦截器已统一提示
  } finally {
    loadCartSummary()
  }
}

/**
 * 去结算
 * 未登录时先跳转登录页并携带订单确认页回跳地址，登录后由订单确认页加载已同步的购物车选中项；
 * 已登录时直接携带当前选中的购物车项 ID 跳转订单确认页。
 */
function onSettlement() {
  if (!cartTotalCount.value) return
  const selectedIds = cartItems.value
    .filter((item) => item.selected)
    .map((item) => item.id)
  if (!selectedIds.length) {
    uni.showToast({ title: '请先选择商品', icon: 'none' })
    return
  }
  if (!userStore.isLogin) {
    // 本地购物车 ID 为临时负数，登录同步后才能拿到服务端真实 ID，
    // 因此未登录时只回跳确认订单页，由页面加载同步后的已选中购物车项。
    goLogin('/pages/order/confirm')
    return
  }
  showCartPopup.value = false
  goOrderConfirm({ cartItemIds: selectedIds })
}
</script>

<style scoped>
.category-page {
  position: fixed;
  /* 自定义导航下小程序 webview 从屏幕顶端（含状态栏）开始渲染。
     真实状态栏高度由脚本运行时取值后内联覆盖（小程序端 --status-bar-height 固定 25px，
     灵动岛机型会被遮挡）；此处 CSS 变量仅作脚本异常时的兜底，H5 端为 0 不受影响 */
  top: var(--status-bar-height, 0px);
  left: 0;
  right: 0;
  /* 小程序端：原生 tabBar 由框架从 webview 中排除，fixed bottom:0 即紧贴 tabBar 顶边；
     若额外让出 100rpx 反而在购物车条与 tabBar 之间留出灰白条（H5 无此问题）。
     H5 端：tabBar 是页面内 DOM 元素，需让出 100rpx + 安全区避免购物车条被遮。 */
  /* #ifdef H5 */
  bottom: calc(100rpx + env(safe-area-inset-bottom));
  /* #endif */
  /* #ifdef MP-WEIXIN */
  bottom: 0;
  /* #endif */
  /* 高于根层级 uni-tabbar（z-index 998），避免 SKU 弹窗被困在本层叠上下文内被 tabBar 遮挡 */
  z-index: 999;
  background-color: #f5f5f5;
  overflow: hidden;
}

.top-bar {
  position: absolute;
  top: 16rpx;
  left: 30rpx;
  right: 30rpx;
  display: flex;
  align-items: center;
  z-index: 10;
}

/* 与首页 community-bar 样式一致 */
.community-bar {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  margin-right: 20rpx;
}

.community-name {
  max-width: 260rpx;
  margin: 0 8rpx;
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.search-bar {
  flex: 1;
  min-width: 0;
  height: 80rpx;
  display: flex;
  align-items: center;
  padding: 0 24rpx;
  box-sizing: border-box;
  border-radius: 32rpx;
  background-color: #ffffff;
}

.search-text {
  flex: 1;
  display: flex;
  align-items: center;
  margin-left: 12rpx;
  font-size: 26rpx;
  color: #999999;
}

.search-input {
  flex: 1;
  margin: 0 16rpx;
}

.search-input.native-input {
  height: 44rpx;
  padding: 0;
  font-size: 26rpx;
  color: #333333;
  background: transparent;
  border: none;
  outline: none;
}

.search-cancel {
  flex-shrink: 0;
  font-size: 26rpx;
  color: #666666;
}

.scan-btn {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  padding: 0 8rpx 0 16rpx;
}

.content,
.search-result {
  position: absolute;
  top: 112rpx;
  bottom: 100rpx;
  left: 0;
  right: 0;
  display: flex;
  overflow: hidden;
}

.left-menu {
  width: 200rpx;
  height: 100%;
  min-height: 0;
  background-color: #ffffff;
}

/* #ifdef H5 */
.left-menu ::-webkit-scrollbar {
  display: none;
}
/* #endif */

.right-list,
.search-result-list {
  flex: 1;
  height: 100%;
  min-height: 0;
  padding: 0 20rpx 20rpx 20rpx;
  box-sizing: border-box;
}

.menu-item {
  padding: 30rpx 20rpx;
  font-size: 26rpx;
  color: #666666;
  text-align: center;
  border-left: 6rpx solid transparent;
  cursor: pointer;
}

.menu-item:active {
  opacity: 0.7;
}

.menu-item.active {
  color: #07c160;
  background-color: #f5f5f5;
  border-left-color: #07c160;
}

.category-section {
  margin-bottom: 20rpx;
}

.category-banner {
  margin-bottom: 20rpx;
}

.category-title {
  padding: 16rpx 0;
  font-size: 28rpx;
  font-weight: bold;
  color: #333333;
  background-color: #f5f5f5;
}

/* 吸顶分类名层：绝对定位覆盖在右侧列表顶部，随滚动联动当前分类名。
   置于 scroll-view 之外，规避小程序下 sticky 在原生 scroll-view 内失效的问题 */
.sticky-title {
  position: absolute;
  top: 0;
  left: 200rpx;
  right: 0;
  z-index: 5;
  padding: 16rpx 20rpx;
  font-size: 28rpx;
  font-weight: bold;
  color: #333333;
  background-color: #f5f5f5;
  box-sizing: border-box;
}

.section-empty {
  padding: 40rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: #999999;
}

.empty-tip {
  padding: 60rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: #999999;
}

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
  font-size: 24rpx;
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
  color: #ff4d4f;
}

.add-btn {
  padding: 6rpx;
}

.cart-bar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 100rpx;
  display: flex;
  align-items: center;
  padding: 0 30rpx;
  box-sizing: border-box;
  background-color: #ffffff;
  border-top: 1rpx solid #eeeeee;
}

.cart-icon {
  position: relative;
  width: 80rpx;
  height: 80rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

/* #ifdef H5 */
.cart-icon:active {
  opacity: 0.7;
}
/* #endif */

.cart-badge {
  position: absolute;
  top: 0;
  right: 0;
  min-width: 32rpx;
  height: 32rpx;
  padding: 0 8rpx;
  border-radius: 16rpx;
  background-color: #ff4d4f;
  color: #ffffff;
  font-size: 20rpx;
  text-align: center;
  line-height: 32rpx;
}

.cart-total {
  flex: 1;
  margin-left: 20rpx;
}

.total-label {
  font-size: 24rpx;
  color: #666666;
}

.total-price {
  font-size: 36rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.cart-btn {
  width: 200rpx;
}

.cart-btn .u-button {
  width: 100%;
}

.cart-popup {
  display: flex;
  flex-direction: column;
  max-height: 70vh;
  padding: 30rpx;
  padding-bottom: 50rpx;
}

/* #ifdef H5 */
.cart-popup {
  padding-bottom: 130rpx;
}
/* #endif */

.cart-popup-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.cart-popup-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.cart-popup-list {
  flex: 1;
  max-height: 50vh;
}

.cart-popup-empty {
  padding: 60rpx 0;
  text-align: center;
  font-size: 28rpx;
  color: #999999;
}

.cart-popup-item {
  display: flex;
  align-items: center;
  padding: 20rpx 0;
  border-bottom: 1rpx solid #eeeeee;
}

.cart-popup-image {
  width: 120rpx;
  height: 120rpx;
  border-radius: 12rpx;
}

.cart-popup-info {
  flex: 1;
  margin: 0 20rpx;
  overflow: hidden;
}

.cart-popup-name {
  display: block;
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cart-popup-sku {
  display: block;
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #999999;
}

.cart-popup-price {
  display: block;
  margin-top: 8rpx;
  font-size: 30rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.cart-popup-action {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 16rpx;
}

/* 失效商品置灰展示（不可改数量，仅保留删除） */
.cart-popup-item-invalid .cart-popup-image,
.cart-popup-item-invalid .cart-popup-name,
.cart-popup-item-invalid .cart-popup-price {
  opacity: 0.4;
}

.cart-popup-invalid-tag {
  align-self: flex-start;
  margin-top: 6rpx;
  padding: 2rpx 12rpx;
  border: 1rpx solid #dddddd;
  border-radius: 6rpx;
  font-size: 20rpx;
  color: #999999;
  background-color: #f7f7f7;
}

.cart-popup-stock-hint {
  display: block;
  margin-top: 4rpx;
  font-size: 20rpx;
  color: #ff9800;
}

.cart-popup-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 20rpx;
  padding-top: 20rpx;
  border-top: 1rpx solid #eeeeee;
}

.cart-popup-total {
  flex: 1;
}

.cart-popup-total .total-label {
  font-size: 24rpx;
  color: #666666;
}

.cart-popup-total .total-price {
  font-size: 36rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.cart-popup-btn {
  width: 200rpx;
}

.cart-popup-btn .u-button {
  width: 100%;
}
</style>
