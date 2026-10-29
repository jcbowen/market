<template>
  <view class="coupon-page">
    <u-tabs :list="tabs" :current="currentTab" @change="onTabChange" />

    <scroll-view class="coupon-list" scroll-y @scrolltolower="loadMore">
      <!-- 我的优惠券（第一页，默认打开）：按状态筛选展示 -->
      <template v-if="currentTab === 0">
        <view class="status-filter">
          <text
            v-for="opt in statusOptions"
            :key="opt.value"
            class="status-chip"
            :class="{ active: myFilter === opt.value }"
            @click="myFilter = opt.value"
          >{{ opt.label }}</text>
        </view>
        <view
          v-for="item in filteredUserCoupons"
          :key="item.id"
          class="coupon-card"
          :class="{ used: item.status !== 1 || isCouponExpired(item) }"
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
        <u-empty
          v-if="!filteredUserCoupons.length && loadStatus === 'nomore'"
          mode="coupon"
          text="暂无相关优惠券"
          margin-top="80"
        />
      </template>

      <!-- 领券中心（可领取） -->
      <template v-else>
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

      <u-loadmore :status="loadStatus" />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCoupons, receiveCoupon, getUserCoupons } from '@/api/marketing'
import { formatDate, formatPrice } from '@/utils/format'
import { couponValueText, couponStatusText, isCouponExpired } from '@/utils/coupon'
import type { UserCoupon, Coupon } from '@/api/types'

const userStore = useUserStore()
/** 优惠券包：第一页=我的券（默认），第二页=领券中心 */
const tabs = ref([
  { name: '我的优惠券' },
  { name: '领券中心' },
])
const currentTab = ref(0)
const coupons = ref<Coupon[]>([])
const userCoupons = ref<UserCoupon[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

/** 我的券状态筛选：0 全部 1 未使用 2 已使用 3 已过期（按状态分组展示的轻量实现） */
const myFilter = ref<0 | 1 | 2 | 3>(0)
const statusOptions = [
  { label: '全部', value: 0 },
  { label: '未使用', value: 1 },
  { label: '已使用', value: 2 },
  { label: '已过期', value: 3 },
] as const

/**
 * 按状态过滤后的我的券列表
 * 后端 /user/coupons 返回全量不分页，过期态由 end_time 动态判定，客户端过滤即可
 */
const filteredUserCoupons = computed(() => {
  if (!myFilter.value) return userCoupons.value
  return userCoupons.value.filter((c) => {
    if (myFilter.value === 3) return c.status === 1 && isCouponExpired(c)
    return c.status === myFilter.value && !(myFilter.value === 1 && isCouponExpired(c))
  })
})

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
      const res = await getUserCoupons({ page: page.value, page_size: pageSize })
      const list = res.data?.list || []
      if (seq !== requestSeq) return
      // /user/coupons 后端当前返回全量不分页，直接置 nomore，
      // 避免全量结果进入分页判断后滚动加载重复追加
      userCoupons.value = page.value === 1 ? list : [...userCoupons.value, ...list]
      loadStatus.value = 'nomore'
    } else {
      const res = await getCoupons({ page: page.value, page_size: pageSize, community_id: userStore.communityInfo?.id })
      const list = res.data?.list || []
      if (seq === requestSeq) appendList(coupons, list)
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
  /* 100vh 含 H5 原生导航栏高度，直接用会把底部裁掉一截（需滚动整个页面才能看全），
     用 --window-top 扣除导航栏：H5 为 44px，小程序端为 0 */
  height: calc(100vh - var(--window-top, 0px));
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

/* 我的券状态筛选 chips */
.status-filter {
  display: flex;
  gap: 16rpx;
  margin-bottom: 20rpx;
}

.status-chip {
  padding: 8rpx 28rpx;
  border-radius: 999rpx;
  font-size: 24rpx;
  color: #666666;
  background-color: #ffffff;
}

.status-chip.active {
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
  font-weight: bold;
}
</style>
