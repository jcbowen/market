<template>
  <view class="coupon-page">
    <u-tabs :list="tabs" :current="currentTab" @change="onTabChange" />

    <scroll-view class="coupon-list" scroll-y @scrolltolower="loadMore">
      <!-- 可领取优惠券 -->
      <template v-if="currentTab === 0">
        <view
          v-for="item in coupons"
          :key="item.id"
          class="coupon-card"
        >
          <view class="coupon-left">
            <text class="coupon-value">{{ item.value }}</text>
            <text class="coupon-threshold">满 {{ formatPrice(item.threshold) }} 可用</text>
          </view>
          <view class="coupon-right">
            <text class="coupon-name">{{ item.name }}</text>
            <text class="coupon-time">{{ formatDate(item.start_time) }} ~ {{ formatDate(item.end_time) }}</text>
            <u-button
              size="mini"
              type="success"
              text="领取"
              :disabled="item.received >= item.total"
              @click="onReceive(item.id)"
            />
          </view>
        </view>
      </template>

      <!-- 我的优惠券 -->
      <template v-else>
        <view
          v-for="item in userCoupons"
          :key="item.id"
          class="coupon-card used"
        >
          <view class="coupon-left">
            <text class="coupon-value">{{ item.value }}</text>
            <text class="coupon-threshold">满 {{ formatPrice(item.threshold) }} 可用</text>
          </view>
          <view class="coupon-right">
            <text class="coupon-name">{{ item.name }}</text>
            <text class="coupon-time">{{ formatDate(item.start_time) }} ~ {{ formatDate(item.end_time) }}</text>
            <text class="coupon-status">{{ item.status === 1 ? '未使用' : '已使用' }}</text>
          </view>
        </view>
      </template>

      <u-loadmore :status="loadStatus" />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCoupons, receiveCoupon, getUserCoupons } from '@/api/marketing'
import { formatDate, formatPrice } from '@/utils/format'
import type { Coupon, UserCoupon } from '@/api/types'

const userStore = useUserStore()
const tabs = ref([
  { name: '可领取' },
  { name: '我的' },
])
const currentTab = ref(0)
const coupons = ref<Coupon[]>([])
const userCoupons = ref<UserCoupon[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

onShow(() => {
  page.value = 1
  coupons.value = []
  userCoupons.value = []
  loadData()
})

/**
 * 切换 tab
 * @param index tab 索引
 */
function onTabChange(index: number) {
  currentTab.value = index
  page.value = 1
  coupons.value = []
  userCoupons.value = []
  loadData()
}

/**
 * 加载优惠券数据
 */
async function loadData() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    if (currentTab.value === 0) {
      const res = await getCoupons({ page: page.value, page_size: pageSize, community_id: userStore.communityInfo?.id })
      const list = res.data?.list || []
      appendList(coupons, list)
    } else {
      const res = await getUserCoupons({ page: page.value, page_size: pageSize })
      const list = res.data?.list || []
      appendList(userCoupons, list)
    }
  } catch {
    loadStatus.value = 'loadmore'
  }
}

/**
 * 追加列表数据并更新分页状态
 * @param listRef 列表引用
 * @param list 新数据
 */
function appendList<T>(listRef: { value: T[] }, list: T[]) {
  if (page.value === 1) {
    listRef.value = list
  } else {
    listRef.value.push(...list)
  }
  if (list.length < pageSize) {
    loadStatus.value = 'nomore'
  } else {
    loadStatus.value = 'loadmore'
    page.value += 1
  }
}

/**
 * 加载更多
 */
function loadMore() {
  loadData()
}

/**
 * 领取优惠券
 * @param id 优惠券 ID
 */
async function onReceive(id: number) {
  try {
    await receiveCoupon(id)
    uni.showToast({ title: '领取成功', icon: 'success' })
    page.value = 1
    coupons.value = []
    loadData()
  } catch {
    // ignore
  }
}
</script>

<style scoped>
.coupon-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f5f5f5;
}

.coupon-list {
  flex: 1;
  overflow: hidden;
  padding: 20rpx;
}

.coupon-card {
  display: flex;
  margin-bottom: 20rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
  overflow: hidden;
}

.coupon-card.used {
  opacity: 0.7;
}

.coupon-left {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  width: 200rpx;
  padding: 24rpx;
  background-color: rgba(7, 193, 96, 0.1);
}

.coupon-value {
  font-size: 48rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.coupon-value::before {
  content: '¥';
  font-size: 24rpx;
}

.coupon-threshold {
  margin-top: 8rpx;
  font-size: 22rpx;
  color: #999999;
}

.coupon-right {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 24rpx;
}

.coupon-name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.coupon-time {
  margin: 12rpx 0;
  font-size: 22rpx;
  color: #999999;
}

.coupon-status {
  font-size: 24rpx;
  color: #07c160;
}
</style>
