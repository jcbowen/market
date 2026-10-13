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
            <!-- 面额按券类型展示：满减券 ¥20.00，折扣券 8.5折 -->
            <text class="coupon-value">{{ couponValueText(item) }}</text>
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
            <!-- 面额按券类型展示：满减券 ¥20.00，折扣券 8.5折（type=2 折扣率） -->
            <text class="coupon-value">{{ couponValueText(item) }}</text>
            <text class="coupon-threshold">满 {{ formatPrice(item.threshold) }} 可用</text>
          </view>
          <view class="coupon-right">
            <text class="coupon-name">{{ item.name }}</text>
            <text class="coupon-time">有效期至 {{ formatDate(item.end_time) }}</text>
            <text class="coupon-status">{{ couponStatusText(item) }}</text>
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
import { couponValueText, couponStatusText } from '@/utils/coupon'
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
  resetAndReload()
})

/**
 * 重置分页状态并重新加载（切 tab / onShow 共用）
 */
function resetAndReload() {
  page.value = 1
  coupons.value = []
  userCoupons.value = []
  loadStatus.value = 'loadmore'
  loadData()
}

/**
 * 切换 tab
 * uview-plus u-tabs 的 change 参数为 { ...点击项, index }
 * @param item change 事件参数（含点击索引）
 */
function onTabChange(item: { index: number }) {
  if (item.index === currentTab.value) return
  currentTab.value = item.index
  resetAndReload()
}

/** 请求序号：防止快速切换 tab 时旧响应覆盖新数据 */
let requestSeq = 0

/**
 * 加载优惠券数据
 */
async function loadData() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  const seq = ++requestSeq
  try {
    if (currentTab.value === 0) {
      const res = await getCoupons({ page: page.value, page_size: pageSize, community_id: userStore.communityInfo?.id })
      const list = res.data?.list || []
      if (seq === requestSeq) appendList(coupons, list)
    } else {
      const res = await getUserCoupons({ page: page.value, page_size: pageSize })
      const list = res.data?.list || []
      if (seq !== requestSeq) return
      // /user/coupons 后端当前返回全量不分页，直接置 nomore，
      // 避免全量结果进入分页判断后滚动加载重复追加
      userCoupons.value = page.value === 1 ? list : [...userCoupons.value, ...list]
      loadStatus.value = 'nomore'
    }
  } catch {
    if (seq !== requestSeq) return
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
    resetAndReload()
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
  /* scroll-view 默认 content-box，width:100% + padding 会横向溢出导致页面超宽 */
  box-sizing: border-box;
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

/* uniapp H5 的 button 默认块级撑满整行，领取按钮收缩为小按钮 */
.coupon-right :deep(.u-button) {
  width: auto;
  align-self: flex-start;
  min-width: 120rpx;
  padding: 0 28rpx;
}
</style>
