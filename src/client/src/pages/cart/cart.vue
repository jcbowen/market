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
      <view class="empty-btn" @click="goHome">
        <u-button type="success" size="small" text="去逛逛" />
      </view>
    </view>

    <view v-else class="cart-content">
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
            <view class="cart-item">
              <u-checkbox
                v-model="item.selected"
                shape="circle"
                @change="onSelect(item)"
              />
              <image class="item-image" :src="item.image || '/static/logo.png'" mode="aspectFill" />
              <view class="item-info">
                <text class="item-name">{{ item.product_name }}</text>
                <text class="item-sku">{{ item.sku_name }}</text>
                <view class="item-bottom">
                  <text class="item-price">{{ formatPrice(item.price) }}</text>
                  <u-number-box
                    v-model="item.quantity"
                    :min="item.min_buy || 1"
                    :max="item.stock"
                    integer
                    @change="onQuantityChange(item)"
                  />
                </view>
              </view>
            </view>
          </u-swipe-action-item>
        </u-swipe-action>
      </view>

      <!-- 底部结算栏 -->
      <view class="footer-bar">
        <view class="select-all" @click="toggleSelectAll">
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
          @click="onSettlement"
        >
          <u-button
            type="success"
            text="去结算"
            :disabled="!selectedIds.length"
          />
        </view>
      </view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { useCartStore } from '@/stores/cart'
import { getCommunityList } from '@/api/user'
import { getCart, updateCartItem, removeCartItem, selectAllCartItems } from '@/api/cart'
import { formatPrice } from '@/utils/format'
import { goHome, goOrderConfirm, goLogin } from '@/utils/navigate'
import type { CartItem, Community } from '@/api/types'

const userStore = useUserStore()
const cartStore = useCartStore()

const items = ref<CartItem[]>([])
const communities = ref<Community[]>([])
const totalAmount = ref(0)

const swipeOptions = ref([
  {
    text: '删除',
    style: { backgroundColor: '#ff4d4f' },
  },
])

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
    const allSelected = list.length > 0 && list.every((item) => item.selected)
    return {
      community_id: Number(cid),
      community_name: communityMap.value[Number(cid)] || `社区 ${cid}`,
      selected: allSelected,
      items: list,
    }
  })
})

const selectedIds = computed(() =>
  items.value.filter((item) => item.selected).map((item) => item.id),
)

const isAllSelected = computed({
  get: () => items.value.length > 0 && items.value.every((item) => item.selected),
  set: (val: boolean) => toggleSelectAll(val),
})

onShow(() => {
  if (!userStore.isLogin) {
    // 未登录时展示登录提示，不强制跳转登录页
    return
  }
  loadCommunities()
  loadCart()
})

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
 * @param item 购物车项
 */
async function onQuantityChange(item: CartItem) {
  const minBuy = item.min_buy || 1
  if (item.quantity < minBuy) {
    uni.showToast({ title: `最低购买 ${minBuy} 份`, icon: 'none' })
    item.quantity = minBuy
    return
  }
  try {
    await updateCartItem(item.id, { quantity: item.quantity })
    loadCart()
  } catch {
    // ignore
  }
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
 * 只能结算当前选中社区的商品，其他社区商品会被过滤并提示
 */
function onSettlement() {
  if (!selectedIds.value.length) return
  if (!userStore.isLogin) {
    goLogin()
    return
  }

  const currentCommunityId = userStore.communityInfo?.id || 0
  const selectedItems = items.value.filter((item) => item.selected)
  const currentCommunityItems = selectedItems.filter(
    (item) => item.community_id === currentCommunityId,
  )

  if (!currentCommunityItems.length) {
    uni.showToast({ title: '当前社区没有选中商品，请切换社区', icon: 'none' })
    return
  }

  const otherCommunityItems = selectedItems.filter(
    (item) => item.community_id !== currentCommunityId,
  )
  if (otherCommunityItems.length) {
    uni.showToast({
      title: '已自动过滤非当前社区商品',
      icon: 'none',
      duration: 2000,
    })
  }

  goOrderConfirm({ cartItemIds: currentCommunityItems.map((item) => item.id) })
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
  bottom: 100rpx;
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
</style>
