<template>
  <view class="flash-page">
    <scroll-view class="flash-list" scroll-y @scrolltolower="loadMore">
      <view
        v-for="item in flashSales"
        :key="item.id"
        class="flash-card"
      >
        <image class="flash-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" @click="onBuy(item)" />
        <view class="flash-info">
          <text class="flash-name">{{ item.product_name }}</text>
          <view class="flash-price">
            <text class="current">{{ formatPrice(item.flash_price) }}</text>
            <text class="original">{{ formatPrice(item.original_price) }}</text>
          </view>
          <view class="flash-stock">
            <progress :percent="soldPercent(item)" stroke-width="12" activeColor="#ff4d4f" />
            <text>已抢 {{ item.sold }}/{{ item.stock }}</text>
          </view>
          <u-button
            size="small"
            type="error"
            text="立即抢购"
            :disabled="item.status !== 1"
            @click="onBuy(item)"
          />
        </view>
      </view>
      <u-loadmore :status="loadStatus" />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getFlashSales } from '@/api/marketing'
import { formatPrice } from '@/utils/format'
import { goProductDetail } from '@/utils/navigate'
import type { FlashSale } from '@/api/types'

const userStore = useUserStore()
const flashSales = ref<FlashSale[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

onShow(() => {
  page.value = 1
  flashSales.value = []
  loadFlashSales()
})

/**
 * 加载秒杀列表
 */
async function loadFlashSales() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    const res = await getFlashSales({ page: page.value, page_size: pageSize, community_id: userStore.communityInfo?.id })
    const list = res.data?.list || []
    if (page.value === 1) {
      flashSales.value = list
    } else {
      flashSales.value.push(...list)
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
 * 加载更多
 */
function loadMore() {
  loadFlashSales()
}

/**
 * 计算已售百分比
 * @param item 秒杀活动
 */
function soldPercent(item: FlashSale): number {
  if (!item.stock) return 0
  return Math.min(100, Math.round((item.sold / item.stock) * 100))
}

/**
 * 跳转商品详情（图片与「立即抢购」按钮同行为）
 * @param item 秒杀活动
 */
function onBuy(item: FlashSale) {
  goProductDetail(item.product_id)
}
</script>

<style scoped>
.flash-page {
  min-height: 100vh;
  background-color: #f5f5f5;
}

.flash-list {
  /* scroll-view 默认 width:100%（content-box），padding 会横向溢出导致页面超宽 */
  box-sizing: border-box;
  padding: 20rpx;
}

.flash-card {
  display: flex;
  margin-bottom: 20rpx;
  padding: 20rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.flash-image {
  width: 220rpx;
  height: 220rpx;
  border-radius: 12rpx;
}

.flash-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: 20rpx;
}

.flash-name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.flash-price {
  display: flex;
  align-items: baseline;
}

.flash-price .current {
  font-size: 36rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.flash-price .original {
  margin-left: 12rpx;
  font-size: 24rpx;
  color: #999999;
  text-decoration: line-through;
}

.flash-stock {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.flash-stock progress {
  flex: 1;
}

.flash-stock text {
  font-size: 22rpx;
  color: #999999;
}
</style>
