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
          <text v-if="distanceText(item)" class="community-distance">{{ distanceText(item) }}</text>
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
  // 坐标系：H5 端用浏览器原生 wgs84（gcj02 转换需在 manifest 配置地图厂商 key，
  // H5 未配置），其他端用 gcj02；wgs84 偏差对距离展示与就近排序可接受
  // #ifdef H5
  const locationType: 'wgs84' | 'gcj02' = 'wgs84'
  // #endif
  // #ifndef H5
  const locationType: 'wgs84' | 'gcj02' = 'gcj02'
  // #endif
  // TODO 调试日志：定位链路排查，问题定位后移除
  console.log('[社区定位] 开始系统定位, type:', locationType)
  uni.getLocation({
    type: locationType,
    success: (res) => {
      console.log('[社区定位] 系统定位成功:', res.longitude, res.latitude, res)
      page.value = 1
      communities.value = []
      loadCommunitiesWithLocation(res.longitude, res.latitude, true)
    },
    fail: async (err) => {
      console.warn('[社区定位] 系统定位失败:', err)
      const ipLoc = await locateByIp()
      console.log('[社区定位] IP 定位兜底结果:', ipLoc)
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
      console.log('[社区定位] 全部定位失败，回退默认列表（不展示距离）')
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
    // TODO 调试日志：确认社区数据是否带经纬度，问题定位后移除
    console.log(
      '[社区定位] 展示距离:', withDistance,
      '定位原点:', origin.value,
      '社区坐标:', communities.value.map((c) => ({ id: c.id, name: c.name, longitude: c.longitude, latitude: c.latitude })),
    )
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

.community-distance {
  margin-left: 16rpx;
  font-size: 24rpx;
  color: #999999;
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
