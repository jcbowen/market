<template>
  <view class="app">
    <slot />
  </view>
</template>

<script setup lang="ts">
import { onLaunch } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCommunityList, getIpLocation } from '@/api/user'
import type { Community } from '@/api/types'

onLaunch(() => {
  initCommunity()
})

/**
 * 初始化社区信息
 * - 已登录：本地无社区缓存时，尝试从用户信息恢复最近一次使用的社区
 * - 未登录：本地无社区缓存时自动定位选社区（系统定位 → IP 定位 → 默认第一个），
 *   仅写本地缓存不落库，选中后 toast 轻提示，用户可随时在首页/分类页手动切换
 */
async function initCommunity() {
  const userStore = useUserStore()
  if (userStore.isLogin) {
    await restoreLoginCommunity(userStore)
    return
  }

  // 未登录且本地已有社区缓存（store 初始化已从 storage 恢复）时不重复选择
  if (userStore.communityInfo?.id) return

  const community = await autoPickCommunity()
  if (!community) return
  userStore.setCommunity(community)
  uni.showToast({ title: `已为您选择「${community.name}」，点击顶部可切换`, icon: 'none' })
}

/**
 * 已登录时从用户信息恢复最近使用的社区
 * @param userStore 用户状态
 */
async function restoreLoginCommunity(userStore: ReturnType<typeof useUserStore>) {
  if (userStore.communityInfo?.id && userStore.communityInfo?.name) return

  // 本地无社区缓存，先从用户信息中恢复 ID，再拉取社区名称补全信息
  const info = userStore.userInfo
  if (!info?.community_id) return

  let communityName = info.community_name || ''
  if (!communityName) {
    try {
      const res = await getCommunityList({ page: 1, page_size: 100 })
      const list = res.data?.list || []
      const matched = list.find((item) => item.id === info.community_id)
      if (matched) {
        userStore.setCommunity(matched)
        return
      }
      communityName = matched?.name || ''
    } catch {
      // 拉取失败时回退到空名称
    }
  }

  userStore.setCommunity({
    id: info.community_id,
    name: communityName,
    address: '',
    longitude: 0,
    latitude: 0,
  })
}

/**
 * 未登录自动选社区：定位坐标可用时取最近社区，定位失败取默认列表第一个
 * @returns 选中的社区，无可用社区时返回 null
 */
async function autoPickCommunity(): Promise<Community | null> {
  const coords = await locateCoords()
  if (coords) {
    const nearby = await fetchFirstCommunity(coords)
    if (nearby) return nearby
  }
  return fetchFirstCommunity()
}

/**
 * 获取定位坐标：优先系统定位，失败按出口 IP 定位兜底
 * 坐标系：H5 端用浏览器原生 wgs84（gcj02 转换需在 manifest 配置地图厂商 key，
 * H5 未配置），其他端用 gcj02；wgs84 与 gcj02 存在约 100~500m 系统偏差，
 * 对就近排序（偏差对全体社区一致）与社区级距离展示可接受
 * @returns 经纬度，两种定位均失败时返回 null
 */
function locateCoords(): Promise<{ longitude: number; latitude: number } | null> {
  // #ifdef H5
  const locationType: 'wgs84' | 'gcj02' = 'wgs84'
  // #endif
  // #ifndef H5
  const locationType: 'wgs84' | 'gcj02' = 'gcj02'
  // #endif
  return new Promise((resolve) => {
    // TODO 调试日志：定位链路排查，问题定位后移除
    console.log('[社区定位] 首页自动选社区，开始系统定位, type:', locationType)
    uni.getLocation({
      type: locationType,
      success: (res) => {
        console.log('[社区定位] 首页系统定位成功:', res.longitude, res.latitude, res)
        resolve({ longitude: res.longitude, latitude: res.latitude })
      },
      fail: async (err) => {
        console.warn('[社区定位] 首页系统定位失败:', err)
        try {
          const res = await getIpLocation()
          const data = res.data
          console.log('[社区定位] 首页 IP 定位兜底结果:', data)
          if (data?.longitude && data?.latitude) {
            resolve({ longitude: data.longitude, latitude: data.latitude })
            return
          }
          resolve(null)
        } catch (e) {
          console.warn('[社区定位] 首页 IP 定位兜底失败:', e)
          resolve(null)
        }
      },
    })
  })
}

/**
 * 按条件取第一个社区（附近模式按距离排序，即最近社区）
 * @param params 坐标参数，缺省时按默认列表取第一个
 * @returns 社区，接口失败或列表为空时返回 null
 */
async function fetchFirstCommunity(params?: {
  longitude: number
  latitude: number
}): Promise<Community | null> {
  try {
    const res = await getCommunityList({ page: 1, page_size: 1, ...params })
    return res.data?.list?.[0] || null
  } catch {
    return null
  }
}
</script>

<style lang="scss">
@import 'uview-plus/index.scss';

.app {
  min-height: 100vh;
  background-color: #f5f5f5;
}
</style>