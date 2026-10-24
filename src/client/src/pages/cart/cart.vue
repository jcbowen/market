<template>
  <view class="cart-page">
    <view v-if="!userStore.isLogin" class="empty-wrap">
      <u-icon name="shopping-cart" size="80" color="#cccccc" />
      <text class="empty-text">登录后查看购物车</text>
      <view class="empty-btn">
        <u-button type="success" size="small" text="去登录" @click="goLogin" />
      </view>
    </view>

    <view v-else-if="!items.length" class="empty-wrap">
      <u-icon name="shopping-cart" size="80" color="#cccccc" />
      <text class="empty-text">购物车还是空的</text>
      <!-- 事件必须直接绑在 u-button 上：其 stop 属性默认 true 会拦截冒泡，外层 view 收不到点击 -->
      <view class="empty-btn">
        <u-button type="success" size="small" text="去逛逛" @click="goHome" />
      </view>
    </view>

    <view v-else class="cart-content">
      <!-- 失效商品汇总条 -->
      <view v-if="invalidItems.length" class="invalid-bar">
        <text class="invalid-bar-text">失效商品 {{ invalidItems.length }} 件</text>
        <text class="invalid-bar-clear" @click="onClearInvalid">清空失效商品</text>
      </view>

      <!-- 按社区分组展示 -->
      <view v-for="group in groupedItems" :key="group.community_id" class="community-group">
        <view class="community-header">
          <u-checkbox v-model="group.selected" shape="circle" @change="onGroupSelectChange(group)" />
          <u-icon name="map-fill" size="16" color="#07c160" />
          <text class="community-name">{{ group.community_name }}</text>
        </view>

        <u-swipe-action>
          <u-swipe-action-item
            v-for="item in group.items"
            :key="item.id"
            :options="swipeOptions"
            @click="onSwipeClick(item.id)"
          >
            <view class="cart-item" :class="{ 'cart-item-invalid': item.invalid }">
              <u-checkbox
                v-model="item.selected"
                shape="circle"
                :disabled="item.invalid"
                @change="onSelect(item)"
              />
              <image class="item-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
              <view class="item-info">
                <text class="item-name">{{ item.product_name }}</text>
                <text v-if="item.invalid" class="item-invalid-tag">{{ item.invalid_reason }}</text>
                <view v-else-if="item.sku_total > 1" class="item-sku-wrap" @click.stop="onSwitchSku(item)">
                  <text class="item-sku">{{ item.spec_name }}</text>
                  <u-icon name="arrow-down-fill" size="10" color="#999999" />
                </view>
                <view v-if="item.stock_hint" class="item-stock-hint">{{ item.stock_hint }}</view>
                <view class="item-bottom">
                  <text class="item-price">{{ formatPrice(item.price) }}</text>
                  <!-- 不用 v-model + @change：u-number-box 的 change 同步触发、v-model 回写在
                       nextTick 之后，change 回调里读 item.quantity 会拿到旧值，导致提交旧数量被弹回；
                       改用 @update:modelValue 显式接收新值（与 category.vue 弹层同模式） -->
                  <u-number-box
                    v-if="!item.invalid"
                    :modelValue="item.quantity"
                    :min="item.min_buy || 1"
                    :max="item.stock"
                    integer
                    @update:modelValue="(val: number) => onQuantityChange(item, val)"
                    @overlimit="onQuantityOverlimit(item, $event)"
                  />
                </view>
              </view>
            </view>
          </u-swipe-action-item>
        </u-swipe-action>
      </view>

      <!-- 底部结算栏 -->
      <view class="footer-bar">
        <view class="select-all" @click="toggleSelectAll()">
          <u-checkbox v-model="isAllSelected" shape="circle" />
          <text>全选</text>
        </view>
        <view class="total-wrap">
          <text class="total-label">合计:</text>
          <text class="total-price">{{ formatPrice(totalAmount) }}</text>
        </view>
        <view
          class="settlement-btn"
          :class="{ disabled: !selectedIds.length }"
        >
          <u-button
            type="success"
            text="去结算"
            :disabled="!selectedIds.length"
            @click="onSettlement"
          />
        </view>
      </view>
    </view>

    <!-- 切换规格弹窗（预选当前规格，确认后替换购物车项） -->
    <sku-selector
      v-model:show="showSkuSelector"
      :product="skuSelectorProduct"
      :initial-sku-id="switchItem?.sku_id || 0"
      :initial-quantity="switchItem?.quantity || 0"
      @confirm="onSkuConfirm"
    />

    <!-- 常买商品：登录后展示（含空购物车时的复购入口），点击一键加购 -->
    <view v-if="userStore.isLogin && frequentProducts.length" class="frequent-section">
      <text class="frequent-title">常买商品</text>
      <view class="frequent-list">
        <view
          v-for="p in frequentProducts"
          :key="p.product_id"
          class="frequent-card"
          @click="onAddFrequent(p)"
        >
          <image class="frequent-image" :src="p.product_image || '/static/logo.png'" mode="aspectFill" />
          <view class="frequent-info">
            <text class="frequent-name">{{ p.product_name }}</text>
            <text class="frequent-count">买过 {{ p.purchase_count }} 次</text>
          </view>
          <view class="frequent-add">
            <u-icon name="plus-circle" size="22" color="#07c160" />
          </view>
        </view>
      </view>
    </view>

    <!-- 跨社区结算：选择本次结算的社区（一次只结算一个社区） -->
    <u-popup :show="communityPickerVisible" mode="bottom" :round="16" @close="communityPickerVisible = false">
      <view class="community-picker">
        <view class="community-picker-header">
          <text class="community-picker-title">选择本次结算的社区</text>
          <u-icon name="close" size="20" color="#999999" @click="communityPickerVisible = false" />
        </view>
        <text class="community-picker-tip">一次只能结算一个社区的商品，其余商品保持选中</text>
        <scroll-view scroll-y class="community-picker-body">
          <view
            v-for="group in pendingGroups"
            :key="group.id"
            class="community-picker-option"
            @click="settleCommunity(group.id, pendingSelectedItems)"
          >
            <view class="community-picker-main">
              <text class="community-picker-name">{{ group.name }}</text>
              <text class="community-picker-desc">{{ group.count }} 件 · 小计 {{ formatPrice(group.amount) }}</text>
            </view>
            <u-icon name="arrow-right" size="16" color="#999999" />
          </view>
        </scroll-view>
      </view>
    </u-popup>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { useCartStore } from '@/stores/cart'
import { getCommunityList } from '@/api/user'
import { getCart, updateCartItem, removeCartItem, selectAllCartItems, addCartItem, clearInvalidItems } from '@/api/cart'
import { getFrequentProducts, type FrequentProduct } from '@/api/order'
import { formatPrice } from '@/utils/format'
import { goHome, goOrderConfirm, goLogin, goProductDetail } from '@/utils/navigate'
import SkuSelector from '@/components/sku-selector.vue'
import type { CartItem, Community, Product, Sku } from '@/api/types'

const userStore = useUserStore()
const cartStore = useCartStore()

const items = ref<CartItem[]>([])
const communities = ref<Community[]>([])
const totalAmount = ref(0)

/** 切换规格：待替换的购物车项与弹窗状态 */
const switchItem = ref<CartItem | null>(null)
const showSkuSelector = ref(false)
/** 切换规格弹窗商品（仅传 ID/名称，详情由组件内兜底加载） */
const skuSelectorProduct = computed(() =>
  switchItem.value
    ? { id: switchItem.value.product_id, name: switchItem.value.product_name }
    : null,
)

const swipeOptions = ref([
  {
    text: '删除',
    style: { backgroundColor: '#ff4d4f' },
  },
])

/** 失效商品列表（商品下架/删除、SKU 删除、库存为 0） */
const invalidItems = computed(() => items.value.filter((item) => item.invalid))

/** 有效商品列表（全选与合计口径） */
const validItems = computed(() => items.value.filter((item) => !item.invalid))

/** 社区 ID -> 社区名称映射 */
const communityMap = computed(() => {
  const map: Record<number, string> = {}
  communities.value.forEach((c) => {
    map[c.id] = c.name
  })
  return map
})

/** 按社区分组后的购物车数据 */
const groupedItems = computed(() => {
  const groups: Record<number, CartItem[]> = {}
  items.value.forEach((item) => {
    const cid = item.community_id || 0
    if (!groups[cid]) {
      groups[cid] = []
    }
    groups[cid].push(item)
  })
  return Object.keys(groups).map((cid) => {
    const list = groups[Number(cid)]
    const validList = list.filter((item) => !item.invalid)
    const allSelected = validList.length > 0 && validList.every((item) => item.selected)
    return {
      community_id: Number(cid),
      community_name: communityMap.value[Number(cid)] || `社区 ${cid}`,
      selected: allSelected,
      items: list,
    }
  })
})

const selectedIds = computed(() =>
  validItems.value.filter((item) => item.selected).map((item) => item.id),
)

const isAllSelected = computed({
  get: () => validItems.value.length > 0 && validItems.value.every((item) => item.selected),
  set: (val: boolean) => toggleSelectAll(val),
})

onShow(() => {
  if (!userStore.isLogin) {
    // 未登录时展示登录提示，不强制跳转登录页
    return
  }
  loadCommunities()
  loadCart()
  loadFrequentProducts()
})

/** 常买商品列表（登录后加载，按购买次数降序） */
const frequentProducts = ref<FrequentProduct[]>([])

/**
 * 加载常买商品
 */
async function loadFrequentProducts() {
  try {
    const res = await getFrequentProducts(10)
    frequentProducts.value = res.data?.list || []
  } catch {
    frequentProducts.value = []
  }
}

/**
 * 常买商品一键加购（按该用户最常买的 SKU 加 1 件）
 * 无 SKU 记录时兜底跳商品详情
 * @param p 常买商品
 */
async function onAddFrequent(p: FrequentProduct) {
  if (!p.sku_id) {
    goProductDetail(p.product_id)
    return
  }
  try {
    await addCartItem(p.product_id, p.sku_id, 1, userStore.communityInfo?.id)
    uni.showToast({ title: '已加入购物车', icon: 'success' })
    loadCart()
  } catch {
    // 请求拦截器已统一提示
  }
}

/**
 * 加载社区列表，用于显示购物车分组名称
 */
async function loadCommunities() {
  try {
    const res = await getCommunityList({ page: 1, page_size: 100 })
    communities.value = res.data?.list || []
  } catch {
    communities.value = []
  }
}

/**
 * 加载购物车数据（获取全部社区的商品）
 */
async function loadCart() {
  try {
    const res = await getCart()
    if (res.data) {
      items.value = res.data.items || []
      totalAmount.value = res.data.total_amount || 0
      cartStore.setCount(res.data.total_count, res.data.selected_count)
    }
  } catch {
    items.value = []
    totalAmount.value = 0
  }
}

/**
 * 切换单个商品选中状态
 * @param item 购物车项
 */
async function onSelect(item: CartItem) {
  try {
    await updateCartItem(item.id, { selected: item.selected })
    loadCart()
  } catch {
    item.selected = !item.selected
  }
}

/**
 * 切换整个社区分组的选中状态
 * @param group 社区分组
 */
async function onGroupSelectChange(group: { community_id: number; items: CartItem[] }) {
  const target = group.items.some((item) => !item.selected)
  try {
    for (const item of group.items) {
      if (item.selected !== target) {
        await updateCartItem(item.id, { selected: target })
      }
    }
    loadCart()
  } catch {
    // ignore
  }
}

/**
 * 数量变化，最低数量受 min_buy 限制
 * 数值由组件 @update:modelValue 显式传入：u-number-box 的 change 事件先于
 * v-model 回写触发，此处不能读 item.quantity（旧值），否则提交旧数量后会被 loadCart 弹回
 * @param item 购物车项
 * @param val 变更后的数量
 */
async function onQuantityChange(item: CartItem, val: number) {
  const minBuy = item.min_buy || 1
  if (val < minBuy) {
    uni.showToast({ title: `最低购买 ${minBuy} 份`, icon: 'none' })
    item.quantity = minBuy
    return
  }
  item.quantity = val
  try {
    await updateCartItem(item.id, { quantity: val })
    loadCart()
  } catch {
    // 请求失败时本地临时值会在下次 loadCart 时与服务端状态对齐
  }
}

/** 删除确认弹窗进行中标志：长按连减会连续触发 overlimit，防止重复弹窗 */
let deleteConfirmPending = false

/**
 * 数量已到下限仍点击减号（u-number-box 触发 overlimit）：
 * 数量为 1 时弹窗确认删除该商品；min_buy > 1 时提示最低购买份数
 * @param item 购物车项
 * @param type 触发方向：minus / plus
 */
function onQuantityOverlimit(item: CartItem, type: 'minus' | 'plus') {
  if (type !== 'minus' || deleteConfirmPending) return
  const minBuy = item.min_buy || 1
  if (item.quantity > minBuy) return
  if (minBuy > 1) {
    uni.showToast({ title: `最低购买 ${minBuy} 份`, icon: 'none' })
    return
  }
  deleteConfirmPending = true
  uni.showModal({
    title: '提示',
    content: '数量不能再少了，确定将该商品移出购物车吗？',
    confirmText: '删除',
    success: async (res) => {
      if (res.confirm) {
        try {
          await removeCartItem(item.id)
          uni.showToast({ title: '已删除', icon: 'success' })
        } catch {
          // 请求拦截器已统一提示
        } finally {
          loadCart()
        }
      }
      deleteConfirmPending = false
    },
    fail: () => {
      deleteConfirmPending = false
    },
  })
}

/**
 * 删除购物车项（侧滑触发）
 * @param id 购物车项 ID
 */
async function onSwipeClick(id: number) {
  try {
    await removeCartItem(id)
    loadCart()
  } catch {
    // ignore
  }
}

/**
 * 点击规格名，打开切换规格弹窗
 * 预选当前 SKU、数量保持购物车现值（同 SKU 确认等价于数量不变）
 * @param item 购物车项
 */
function onSwitchSku(item: CartItem) {
  switchItem.value = item
  showSkuSelector.value = true
}

/**
 * 切换规格确认回调：删旧购物车项 + 按新 SKU 重新加购
 * 服务端同 SKU 已存在时自动累加合并；
 * 无论成功与否最后都刷新购物车列表，保证与服务端状态一致
 * @param product 组件内兜底加载后的完整商品对象
 * @param sku 切换后的 SKU
 * @param quantity 数量
 */
async function onSkuConfirm({ product, sku, quantity }: { product: Product; sku: Sku; quantity: number }) {
  const item = switchItem.value
  if (!item) return
  try {
    await removeCartItem(item.id)
    await addCartItem(product.id, sku.id, quantity, userStore.communityInfo?.id)
    showSkuSelector.value = false
  } catch {
    // 请求拦截器已统一提示
  } finally {
    loadCart()
  }
}

/**
 * 清空失效商品
 * 成功后刷新购物车列表，失败由请求拦截器统一提示
 */
async function onClearInvalid() {
  try {
    await clearInvalidItems()
    uni.showToast({ title: '已清空失效商品', icon: 'success' })
  } finally {
    loadCart()
  }
}

/**
 * 全选/取消全选
 * @param selected 是否选中
 */
async function toggleSelectAll(selected?: boolean) {
  const target = selected !== undefined ? selected : !isAllSelected.value
  try {
    await selectAllCartItems(target)
    loadCart()
  } catch {
    // ignore
  }
}

/**
 * 去结算
 * 一次只结算一个社区的商品：单社区选中时直接结算；
 * 跨社区选中时弹窗让用户选择本次结算的社区，无需切换社区
 */
function onSettlement() {
  if (!selectedIds.value.length) return
  if (!userStore.isLogin) {
    goLogin()
    return
  }

  const selectedItems = validItems.value.filter((item) => item.selected)
  // 按社区聚合选中的商品（件数/小计供选择弹窗展示）
  const groups = new Map<number, CartItem[]>()
  for (const item of selectedItems) {
    const cid = item.community_id || 0
    const list = groups.get(cid) || []
    list.push(item)
    groups.set(cid, list)
  }
  if (groups.size === 1) {
    settleCommunity(groups.keys().next().value as number, selectedItems)
    return
  }

  // 弹窗列出有选中商品的社区（不受 ActionSheet 6 项上限约束）
  pendingSelectedItems.value = selectedItems
  pendingGroups.value = [...groups.entries()].map(([id, list]) => ({
    id,
    name: communityMap.value[id] || `社区 ${id}`,
    count: list.reduce((sum, item) => sum + item.quantity, 0),
    amount: list.reduce((sum, item) => sum + item.price * item.quantity, 0),
  }))
  communityPickerVisible.value = true
}

/** 跨社区结算的社区选择弹窗状态 */
const communityPickerVisible = ref(false)
const pendingGroups = ref<{ id: number; name: string; count: number; amount: number }[]>([])
const pendingSelectedItems = ref<CartItem[]>([])

/**
 * 按社区结算：携带该社区选中的购物车项与社区 ID 跳确认订单页
 * @param communityId 结算社区 ID
 * @param selectedItems 全部选中商品（按社区过滤）
 */
function settleCommunity(communityId: number, selectedItems: CartItem[]) {
  communityPickerVisible.value = false
  const items = selectedItems.filter((item) => (item.community_id || 0) === communityId)
  if (!items.length) return
  goOrderConfirm({
    cartItemIds: items.map((item) => item.id),
    communityId: communityId || undefined,
  })
}
</script>

<style scoped>
.cart-page {
  min-height: 100vh;
  padding-bottom: 120rpx;
  background-color: #f5f5f5;
}

.empty-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 200rpx;
}

.empty-text {
  margin: 24rpx 0;
  font-size: 28rpx;
  color: #999999;
}

.empty-btn {
  margin-top: 20rpx;
}

.community-group {
  margin: 20rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
  overflow: hidden;
}

.community-header {
  display: flex;
  align-items: center;
  padding: 20rpx;
  border-bottom: 1rpx solid #f5f5f5;
}

.community-header :deep(.u-checkbox) {
  margin-right: 12rpx;
}

.community-name {
  margin-left: 8rpx;
  font-size: 28rpx;
  font-weight: bold;
  color: #333333;
}

.cart-item {
  display: flex;
  align-items: center;
  padding: 20rpx;
  background-color: #ffffff;
}

.item-image {
  width: 160rpx;
  height: 160rpx;
  margin: 0 20rpx;
  border-radius: 12rpx;
}

.item-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  height: 160rpx;
  overflow: hidden;
}

.item-name {
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-sku-wrap {
  display: flex;
  align-items: center;
  gap: 6rpx;
  align-self: flex-start;
  margin-top: 6rpx;
}

.item-sku {
  font-size: 24rpx;
  color: #999999;
}

.item-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.item-price {
  font-size: 30rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.footer-bar {
  position: fixed;
  left: 0;
  right: 0;
  /* iPhone 全面屏（viewport-fit=cover）下内置 tabBar 实际高 50px + 安全区，
     只让出 100rpx 会导致结算栏下沿被 tabBar 盖住，需叠加安全区高度 */
  bottom: calc(100rpx + env(safe-area-inset-bottom));
  display: flex;
  align-items: center;
  padding: 16rpx 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #eeeeee;
}

.select-all {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  width: 140rpx;
}

.total-wrap {
  flex: 1;
  min-width: 0;
  margin: 0 20rpx;
  text-align: right;
  overflow: hidden;
}

.total-label {
  font-size: 26rpx;
  color: #666666;
}

.total-price {
  font-size: 32rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.settlement-btn {
  flex-shrink: 0;
  width: 200rpx;
}

.settlement-btn.disabled {
  pointer-events: none;
  opacity: 0.6;
}

.settlement-btn .u-button {
  width: 100%;
}

/* 失效商品汇总条 */
.invalid-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 20rpx 20rpx 0;
  padding: 16rpx 24rpx;
  border-radius: 12rpx;
  background-color: #fff7e6;
}

.invalid-bar-text {
  font-size: 24rpx;
  color: #999999;
}

.invalid-bar-clear {
  font-size: 24rpx;
  color: #07c160;
}

/* 失效商品置灰展示 */
.cart-item-invalid .item-image,
.cart-item-invalid .item-name,
.cart-item-invalid .item-price {
  opacity: 0.4;
}

.cart-item-invalid .item-info {
  height: auto;
  min-height: 160rpx;
}

.item-invalid-tag {
  align-self: flex-start;
  margin-top: 6rpx;
  padding: 2rpx 12rpx;
  border: 1rpx solid #dddddd;
  border-radius: 6rpx;
  font-size: 20rpx;
  color: #999999;
  background-color: #f7f7f7;
}

.item-stock-hint {
  margin-top: 4rpx;
  font-size: 20rpx;
  color: #ff9800;
}

/* 常买商品区块 */
.frequent-section {
  margin: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.frequent-title {
  display: block;
  margin-bottom: 20rpx;
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.frequent-list {
  display: flex;
  flex-direction: column;
  gap: 20rpx;
}

.frequent-card {
  display: flex;
  align-items: center;
}

.frequent-image {
  width: 110rpx;
  height: 110rpx;
  border-radius: 12rpx;
  flex-shrink: 0;
}

.frequent-info {
  flex: 1;
  min-width: 0;
  margin: 0 20rpx;
}

.frequent-name {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 26rpx;
  color: #333333;
}

.frequent-count {
  display: block;
  margin-top: 8rpx;
  font-size: 22rpx;
  color: #999999;
}

.frequent-add {
  flex-shrink: 0;
  padding: 10rpx;
}

/* 跨社区结算社区选择弹窗 */
.community-picker {
  padding: 30rpx 30rpx calc(30rpx + env(safe-area-inset-bottom));
}

.community-picker-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12rpx;
}

.community-picker-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.community-picker-tip {
  display: block;
  margin-bottom: 16rpx;
  font-size: 22rpx;
  color: #999999;
}

.community-picker-body {
  max-height: 50vh;
}

.community-picker-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24rpx 8rpx;
  border-bottom: 1rpx solid #f5f5f5;
}

.community-picker-option:last-child {
  border-bottom: none;
}

.community-picker-main {
  flex: 1;
  min-width: 0;
}

.community-picker-name {
  display: block;
  font-size: 28rpx;
  color: #333333;
}

.community-picker-desc {
  display: block;
  margin-top: 6rpx;
  font-size: 22rpx;
  color: #999999;
}
</style>
