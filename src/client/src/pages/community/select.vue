<template>
  <view class="community-select-page">
    <!-- 搜索栏：灰底胶囊 + 绿色搜索入口 -->
    <view class="search-bar">
      <view class="search-field">
        <u-icon name="search" size="16" color="#999999" />
        <u-input
          v-model="keyword"
          class="search-input"
          placeholder="搜索社区名称"
          border="none"
          clearable
          confirm-type="search"
          @confirm="onSearch"
        />
      </view>
      <text class="search-action" @click="onSearch">搜索</text>
    </view>

    <!-- 附近社区入口：带定位状态反馈 -->
    <view class="location-bar" @click="getNearby">
      <view class="location-icon">
        <u-icon name="map-fill" size="16" color="#07c160" />
      </view>
      <view class="location-text">
        <text class="location-title">附近社区</text>
        <text class="location-sub">{{ locating ? '正在定位…' : '按距离由近到远排序' }}</text>
      </view>
      <u-loading-icon v-if="locating" mode="semicircle" size="20" color="#07c160" />
    </view>

    <!-- 社区列表 -->
    <scroll-view class="community-list" scroll-y @scrolltolower="loadMore">
      <view
        v-for="item in communities"
        :key="item.id"
        class="community-card"
        :class="{ 'community-card--active': isCurrent(item.id) }"
        @click="onSelect(item)"
      >
        <view class="card-icon">
          <u-icon name="home" size="20" :color="isCurrent(item.id) ? '#07c160' : '#999999'" />
        </view>
        <view class="card-main">
          <view class="community-top">
            <text class="community-name">{{ item.name }}</text>
            <text v-if="isCurrent(item.id)" class="current-tag">当前</text>
          </view>
          <text class="community-address">{{ item.address }}</text>
        </view>
        <text v-if="distanceText(item)" class="community-distance">{{ distanceText(item) }}</text>
        <u-icon v-if="isCurrent(item.id)" name="checkmark-circle-fill" size="20" color="#07c160" />
      </view>
      <u-loadmore v-if="communities.length" :status="loadStatus" />
      <u-empty
        v-if="!communities.length && loadStatus === 'nomore'"
        mode="search"
        text="没有找到相关社区"
        margin-top="120"
      />
    </scroll-view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCommunityList, getIpLocation } from '@/api/user'
import { formatDistance } from '@/utils/format'
import type { Community } from '@/api/types'

const userStore = useUserStore()
const keyword = ref('')
const communities = ref<Community[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')
/** 系统定位成功的定位原点，仅系统定位时用于距离展示（IP 定位不展示距离） */
const origin = ref<{ longitude: number; latitude: number } | null>(null)
/** 定位进行中，用于「附近社区」入口的状态反馈 */
const locating = ref(false)

onShow(() => {
  page.value = 1
  communities.value = []
  locateAndLoad()
})

/**
 * 判断是否为当前社区
 * @param id 社区 ID
 */
function isCurrent(id: number): boolean {
  return userStore.communityInfo?.id === id
}

/**
 * 计算社区与定位原点的距离文案
 * @param community 社区对象
 * @returns 如 "850m"/"1.2km"，无系统定位原点或社区坐标缺失时返回空串
 */
function distanceText(community: Community): string {
  return formatDistance(origin.value, community)
}

/**
 * 加载社区列表（默认/搜索模式，无定位原点不展示距离）
 */
async function loadCommunities() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  origin.value = null
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
 * 进页自动定位排序：优先系统定位，失败按出口 IP 兜底（best-effort），
 * 两者均失败时回退默认列表（C 端社区列表按定位距离排序）
 * @param manual 是否用户手动触发「附近社区」（全部失败时给出 toast 反馈）
 */
function locateAndLoad(manual = false) {
  // 坐标系按端裁剪：H5 用 wgs84，其他端用 gcj02。
  // 写成 let 默认值 + 条件编译覆盖，避免双分支声明被 tsc 判为重复声明
  let locationType: 'wgs84' | 'gcj02' = 'gcj02'
  // #ifdef H5
  locationType = 'wgs84'
  // #endif
  locating.value = true
  uni.getLocation({
    type: locationType,
    success: (res) => {
      locating.value = false
      page.value = 1
      communities.value = []
      loadCommunitiesWithLocation(res.longitude, res.latitude, true)
    },
    fail: async (err) => {
      console.warn('[社区定位] 系统定位失败:', err)
      const ipLoc = await locateByIp()
      locating.value = false
      if (ipLoc) {
        page.value = 1
        communities.value = []
        loadCommunitiesWithLocation(ipLoc.longitude, ipLoc.latitude)
        return
      }
      if (manual) {
        uni.showToast({ title: '定位失败，请手动搜索社区', icon: 'none' })
        return
      }
      // 自动定位全失败：静默回退默认列表，不打断浏览
      loadCommunities()
    },
  })
}

/**
 * 手动触发附近社区（顶部「附近社区」入口）
 */
function getNearby() {
  locateAndLoad(true)
}

/**
 * IP 定位兜底：服务端按本地 GeoLite2-City 数据库解析出口 IP，返回大致经纬度（城市级精度）
 * 数据库未就绪、内网 IP 或未命中时 data 为 null
 * @returns 经纬度，获取失败时返回 null
 */
async function locateByIp(): Promise<{ longitude: number; latitude: number } | null> {
  try {
    const res = await getIpLocation()
    const data = res.data
    if (data?.longitude && data?.latitude) {
      return { longitude: data.longitude, latitude: data.latitude }
    }
    return null
  } catch {
    return null
  }
}

/**
 * 根据坐标加载社区
 * @param longitude 经度
 * @param latitude 纬度
 * @param withDistance 是否记录定位原点用于距离展示（仅系统定位传 true，IP 定位精度不足不展示）
 */
async function loadCommunitiesWithLocation(longitude: number, latitude: number, withDistance = false) {
  origin.value = withDistance ? { longitude, latitude } : null
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
  background-color: #f5f6f8;
}

/* 搜索栏：灰底胶囊，弱化边框感 */
.search-bar {
  display: flex;
  align-items: center;
  gap: 24rpx;
  padding: 20rpx 30rpx;
  background-color: #ffffff;
}

.search-field {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 12rpx;
  height: 64rpx;
  padding: 0 24rpx;
  border-radius: 999rpx;
  background-color: #f5f6f8;
}

.search-input {
  flex: 1;
}

.search-action {
  font-size: 28rpx;
  color: #07c160;
}

/* 附近社区入口：图标底色块 + 双行文案 + 定位中反馈 */
.location-bar {
  display: flex;
  align-items: center;
  padding: 20rpx 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #f0f0f0;
}

.location-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56rpx;
  height: 56rpx;
  border-radius: 16rpx;
  background-color: rgba(7, 193, 96, 0.1);
}

.location-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  margin-left: 16rpx;
}

.location-title {
  font-size: 28rpx;
  color: #333333;
}

.location-sub {
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #999999;
}

.community-list {
  flex: 1;
  overflow: hidden;
  /* scroll-view 默认 content-box，width:100% + padding 会横向溢出导致页面超宽 */
  box-sizing: border-box;
  padding: 24rpx;
}

/* 卡片：图标 + 主信息 + 右侧距离/选中态，透明占位边框避免选中时跳动 */
.community-card {
  display: flex;
  align-items: center;
  margin-bottom: 20rpx;
  padding: 24rpx;
  border: 2rpx solid transparent;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.community-card--active {
  border-color: #07c160;
  background-color: rgba(7, 193, 96, 0.04);
}

.card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 72rpx;
  height: 72rpx;
  border-radius: 16rpx;
  background-color: #f5f6f8;
}

.community-card--active .card-icon {
  background-color: rgba(7, 193, 96, 0.1);
}

.card-main {
  flex: 1;
  margin: 0 20rpx;
  overflow: hidden;
}

.community-top {
  display: flex;
  align-items: center;
  margin-bottom: 8rpx;
}

.community-name {
  font-size: 30rpx;
  font-weight: 600;
  color: #333333;
}

.current-tag {
  margin-left: 12rpx;
  padding: 2rpx 12rpx;
  border-radius: 999rpx;
  font-size: 20rpx;
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
}

.community-address {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 24rpx;
  color: #999999;
}

.community-distance {
  margin-right: 8rpx;
  font-size: 24rpx;
  color: #999999;
}
</style>
