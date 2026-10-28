<template>
  <view class="full-reduction-page">
    <scroll-view class="full-reduction-list" scroll-y @scrolltolower="loadMore">
      <view
        v-for="item in list"
        :key="item.id"
        class="full-reduction-card"
      >
        <view class="card-header">
          <text class="card-name">{{ item.name }}</text>
          <text class="card-status" :class="{ active: item.status === 1 }">
            {{ item.status === 1 ? '进行中' : '已结束' }}
          </text>
        </view>
        <view class="card-rule">
          <text class="rule-amount">满 {{ formatPrice(item.threshold) }}</text>
          <text class="rule-text">减</text>
          <text class="rule-amount">{{ formatPrice(item.reduce_amount) }}</text>
        </view>
        <text class="card-time">{{ formatDate(item.start_time) }} ~ {{ formatDate(item.end_time) }}</text>
      </view>
      <u-loadmore :status="loadStatus" />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getFullReductions } from '@/api/product'
import { formatPrice, formatDate } from '@/utils/format'
import type { FullReduction } from '@/api/types'

const userStore = useUserStore()
const list = ref<FullReduction[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

/**
 * 页面显示时回到第 1 页重新加载
 * 必须一并重置 loadStatus：上一轮已置 nomore 时，加载函数的守卫会让本次重载直接 return，
 * 列表被清空后不再填充
 */
onShow(() => {
  page.value = 1
  list.value = []
  loadStatus.value = 'loadmore'
  loadData()
})

/**
 * 加载满减活动列表
 */
async function loadData() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    const res = await getFullReductions({
      community_id: userStore.communityInfo?.id,
    })
    const data = res.data || []
    if (page.value === 1) {
      list.value = data
    } else {
      list.value.push(...data)
    }
    if (data.length < pageSize) {
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
  loadData()
}
</script>

<style scoped>
.full-reduction-page {
  /* 高度必须确定：否则 scroll-view 被内容撑开、scroll-y 失效、@scrolltolower 不触发，分页加载失效。
     100vh 含 H5 原生导航栏高度（小程序端 --window-top 为 0，行为不变），用 --window-top 扣除 */
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--window-top, 0px));
  background-color: #f5f5f5;
}

.full-reduction-list {
  /* scroll-view 默认 width:100%（content-box），padding 会横向溢出导致页面超宽 */
  flex: 1;
  min-height: 0;
  box-sizing: border-box;
  padding: 20rpx;
}

.full-reduction-card {
  margin-bottom: 20rpx;
  padding: 30rpx;
  border-radius: 16rpx;
  background: linear-gradient(135deg, #fff5f5 0%, #ffffff 100%);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.card-name {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.card-status {
  padding: 4rpx 16rpx;
  border-radius: 8rpx;
  font-size: 22rpx;
  color: #999999;
  background-color: #f0f0f0;
}

.card-status.active {
  color: #ff4d4f;
  background-color: rgba(255, 77, 79, 0.1);
}

.card-rule {
  display: flex;
  align-items: baseline;
  margin-bottom: 16rpx;
}

.rule-amount {
  font-size: 40rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.rule-amount::before {
  content: '¥';
  font-size: 24rpx;
}

.rule-text {
  margin: 0 16rpx;
  font-size: 28rpx;
  color: #666666;
}

.card-time {
  font-size: 24rpx;
  color: #999999;
}
</style>
