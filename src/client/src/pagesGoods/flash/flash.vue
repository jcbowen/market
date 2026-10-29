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
            <text class="current">{{ formatPrice(item.original_price) }}</text>
          </view>
          <text class="flash-tip">限时活动价暂未生效，以下单实付为准</text>
          <view class="flash-stock">
            <text>已售 {{ item.sold }} · 剩余 {{ item.stock }}</text>
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

/**
 * 页面显示时回到第 1 页重新加载
 * 必须一并重置 loadStatus：上一轮已置 nomore 时，加载函数的守卫会让本次重载直接 return，
 * 列表被清空后不再填充，页面只剩「没有更多了」
 */
onShow(() => {
  page.value = 1
  flashSales.value = []
  loadStatus.value = 'loadmore'
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
 * 跳转商品详情（图片与「立即抢购」按钮同行为）
 * @param item 秒杀活动
 */
function onBuy(item: FlashSale) {
  // 与「立即抢购」按钮同一守卫：卡片图片也可点击，非进行中（未开始/已结束）不得跳详情
  if (item.status !== 1) return
  goProductDetail(item.product_id)
}
</script>

<style scoped>
.flash-page {
  /* 高度必须确定：否则 scroll-view 被内容撑开、scroll-y 失效、@scrolltolower 不触发，分页加载失效。
     100vh 含 H5 原生导航栏高度（小程序端 --window-top 为 0，行为不变），用 --window-top 扣除 */
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--window-top, 0px));
  background-color: #f5f5f5;
}

.flash-list {
  /* scroll-view 默认 width:100%（content-box），padding 会横向溢出导致页面超宽 */
  flex: 1;
  min-height: 0;
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

.flash-tip {
  font-size: 22rpx;
  color: #999999;
}

.flash-stock {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.flash-stock text {
  font-size: 22rpx;
  color: #999999;
}
</style>
