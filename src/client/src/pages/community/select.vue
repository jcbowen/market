<template>
  <view class="community-select-page">
    <view class="search-bar">
      <u-input
        v-model="keyword"
        placeholder="搜索社区"
        prefix-icon="search"
        border="surround"
        clearable
      />
      <view class="search-btn" @click="onSearch">
        <u-button size="small" text="搜索" />
      </view>
    </view>

    <view class="location-bar" @click="getNearby">
      <u-icon name="map-fill" size="18" color="#07c160" />
      <text>附近社区</text>
    </view>

    <scroll-view class="community-list" scroll-y @scrolltolower="loadMore">
      <view
        v-for="item in communities"
        :key="item.id"
        class="community-card"
        @click="onSelect(item)"
      >
        <view class="community-top">
          <text class="community-name">{{ item.name }}</text>
          <text v-if="isCurrent(item.id)" class="current-tag">当前</text>
        </view>
        <text class="community-address">{{ item.address }}</text>
      </view>
      <u-loadmore :status="loadStatus" />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCommunityList } from '@/api/user'
import type { Community } from '@/api/types'

const userStore = useUserStore()
const keyword = ref('')
const communities = ref<Community[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

onShow(() => {
  page.value = 1
  communities.value = []
  loadCommunities()
})

/**
 * 判断是否为当前社区
 * @param id 社区 ID
 */
function isCurrent(id: number): boolean {
  return userStore.communityInfo?.id === id
}

/**
 * 加载社区列表
 */
async function loadCommunities() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    const res = await getCommunityList({
      page: page.value,
      page_size: pageSize,
      keyword: keyword.value || undefined,
    })
    const list = res.data?.list || []
    if (page.value === 1) {
      communities.value = list
    } else {
      communities.value.push(...list)
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
 * 搜索社区
 */
function onSearch() {
  page.value = 1
  communities.value = []
  loadCommunities()
}

/**
 * 加载更多
 */
function loadMore() {
  loadCommunities()
}

/**
 * 获取附近社区
 */
function getNearby() {
  uni.getLocation({
    type: 'gcj02',
    success: (res) => {
      page.value = 1
      communities.value = []
      loadCommunitiesWithLocation(res.longitude, res.latitude)
    },
    fail: () => {
      uni.showToast({ title: '定位失败', icon: 'none' })
    },
  })
}

/**
 * 根据坐标加载社区
 * @param longitude 经度
 * @param latitude 纬度
 */
async function loadCommunitiesWithLocation(longitude: number, latitude: number) {
  loadStatus.value = 'loading'
  try {
    const res = await getCommunityList({
      page: 1,
      page_size: pageSize,
      longitude,
      latitude,
    })
    communities.value = res.data?.list || []
    loadStatus.value = 'nomore'
  } catch {
    loadStatus.value = 'loadmore'
  }
}

/**
 * 选择社区
 * @param community 社区对象
 */
async function onSelect(community: Community) {
  try {
    await userStore.switchCommunity(community)
    uni.showToast({ title: '切换成功', icon: 'success' })
    setTimeout(() => uni.navigateBack({}), 1000)
  } catch {
    // ignore
  }
}
</script>

<style scoped>
.community-select-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f5f5f5;
}

.search-bar {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 20rpx 30rpx;
  background-color: #ffffff;
}

.search-bar .u-input {
  flex: 1;
}

.location-bar {
  display: flex;
  align-items: center;
  padding: 24rpx 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #f5f5f5;
  font-size: 28rpx;
  color: #333333;
}

.location-bar text {
  margin-left: 12rpx;
}

.community-list {
  flex: 1;
  overflow: hidden;
  /* scroll-view 默认 content-box，width:100% + padding 会横向溢出导致页面超宽 */
  box-sizing: border-box;
  padding: 20rpx;
}

.community-card {
  margin-bottom: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.community-top {
  display: flex;
  align-items: center;
  margin-bottom: 12rpx;
}

.community-name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.current-tag {
  margin-left: 16rpx;
  padding: 2rpx 10rpx;
  border-radius: 8rpx;
  font-size: 20rpx;
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
}

.community-address {
  font-size: 26rpx;
  color: #999999;
}
</style>
