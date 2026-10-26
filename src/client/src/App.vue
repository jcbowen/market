<template>
  <view class="app">
    <slot />
  </view>
</template>

<script setup lang="ts">
import { onLaunch } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { useCommunityRecommendStore, type CommunityRecommendPayload } from '@/stores/community-recommend'
import { getCommunityList, getCommunityById, getIpLocation } from '@/api/user'
import { formatDistance } from '@/utils/format'
import type { Community } from '@/api/types'

/** 自动选社区结果：社区 + 定位来源（用于弹窗副文案的展示口径） */
interface AutoPickResult {
  community: Community
  /** 定位原点，未定位时为 null */
  coords: { longitude: number; latitude: number } | null
  /** 定位来源：gps=系统定位（可展示距离）、ip=IP 兜底（城市级精度不展示距离）、none=默认列表第一个 */
  source: 'gps' | 'ip' | 'none'
}

onLaunch(() => {
  initCommunity()
})

/**
 * 初始化社区信息
 * - 已登录：本地无社区缓存时，尝试从用户信息恢复最近一次使用的社区；
 *   恢复失败（如绑定社区已停业，营业列表中匹配不到）时走自动选社区兜底
 * - 未登录：本地无社区缓存时自动定位选社区（系统定位 → IP 定位 → 默认第一个），
 *   仅写本地缓存不落库；自动选社区的结果通过推荐弹窗告知确认
 *   （弹窗由 tabbar 页挂载的 community-recommend-dialog 消费，未挂载场景降级 toast）
 * - 已有社区缓存时统一校验营业状态：社区已停业则自动切换到可用社区并弹窗确认
 */
async function initCommunity() {
  const userStore = useUserStore()
  if (userStore.isLogin) {
    // 兜底同步：服务端未绑定社区但本地已有社区选择（登录前自动/手动选的社区未落库），
    // 补写服务端绑定，否则下单/存地址/运费试算等服务端按未选社区处理都会报错
    if (!userStore.userInfo?.community_id && userStore.communityInfo?.id) {
      try {
        await userStore.switchCommunity(userStore.communityInfo)
      } catch {
        // 同步失败不阻断启动，下方按服务端绑定恢复时仍会兜底
      }
    }
    // 本地已有社区缓存：仍按服务端绑定刷新一次完整社区信息。
    // 绑定社区已删除/停业（营业列表匹配不到）时说明绑定已失效——
    // 直接信任本地缓存会导致下单报"社区不存在"，需走自愈或兜底重选
    if (userStore.communityInfo?.id && userStore.communityInfo?.name) {
      if (await userStore.syncBoundCommunity()) {
        await ensureCommunityOpen(userStore)
        return
      }
      // 服务端绑定已失效：本地缓存社区若仍在营业列表中则补绑定沿用，否则自动重选
      const local = userStore.communityInfo
      const localValid = local?.id ? await getCommunityById(local.id) : null
      if (localValid) {
        try {
          await userStore.switchCommunity(localValid)
        } catch {
          // 同步失败不阻断启动
        }
        await ensureCommunityOpen(userStore)
        return
      }
      const fallback = await autoPickCommunity()
      if (fallback) {
        await userStore.switchCommunity(fallback.community)
        notifyRecommend(fallback)
      }
      return
    }
    // 本地无社区缓存：按用户绑定社区恢复；恢复失败（绑定社区停业/不存在）时自动兜底选社区
    const restored = await userStore.syncBoundCommunity()
    if (!restored) {
      const fallback = await autoPickCommunity()
      if (fallback) {
        await userStore.switchCommunity(fallback.community)
        notifyRecommend(fallback)
      }
      return
    }
    await ensureCommunityOpen(userStore)
    return
  }

  // 未登录且本地已有社区缓存（store 初始化已从 storage 恢复）时不重复选择，仅校验停业状态
  if (userStore.communityInfo?.id) {
    await ensureCommunityOpen(userStore)
    return
  }

  const community = await autoPickCommunity()
  if (!community) return
  userStore.setCommunity(community.community)
  notifyRecommend(community)
}

/**
 * 校验当前社区营业状态：已停业时自动切换到可用社区并弹窗确认
 * 停业判断仅识别显式 status === 0（历史本地缓存无 status 字段时不误判）
 * 自动切换复用 switchCommunity：已登录同步服务端绑定（避免下次启动被切回停业社区），
 * 自动重选无可用社区时仅提示，保留原社区由用户手动处理
 * @param userStore 用户状态
 */
async function ensureCommunityOpen(userStore: ReturnType<typeof useUserStore>) {
  const current = userStore.communityInfo
  if (!current || current.status !== 0) return

  const next = await autoPickCommunity()
  if (next && next.community.id !== current.id) {
    await userStore.switchCommunity(next.community)
    notifyRecommend(next, '社区已停业，已为您切换')
  } else {
    uni.showToast({
      title: `社区「${current.name}」已停业，暂无其他可用社区`,
      icon: 'none',
      duration: 3000,
    })
  }
}

/**
 * 自动选社区结果弹窗确认：写入推荐弹窗 store，由 tabbar 页挂载的
 * community-recommend-dialog 展示（社区名 + 距离/来源说明 + 确认/更换）。
 * 兜底：落地页为深链详情页等未挂载弹窗组件的场景时，5 秒未被消费降级为 toast，
 * 避免自动选社区完全无感知
 * @param result 自动选社区结果
 * @param title 弹窗标题，默认推荐场景；停业切换场景传入切换说明
 */
function notifyRecommend(
  result: AutoPickResult,
  title = '为您推荐最近社区',
) {
  const recommendStore = useCommunityRecommendStore()
  const { community, coords, source } = result
  // 副文案按定位来源区分：系统定位可展示距离，IP 定位城市级精度不展示误导性距离
  let subtitle = '已为您选择默认社区'
  if (source === 'gps') {
    const distance = formatDistance(coords, community)
    subtitle = distance ? `距离您 ${distance}` : '根据当前位置为您推荐'
  } else if (source === 'ip') {
    subtitle = '根据当前位置为您推荐'
  }
  const payload: CommunityRecommendPayload = { title, communityName: community.name, subtitle }
  recommendStore.show(payload)
  setTimeout(() => {
    if (recommendStore.pending === payload) {
      recommendStore.clear()
      uni.showToast({ title: `已为您选择「${community.name}」，点击顶部可切换`, icon: 'none' })
    }
  }, 5000)
}

/**
 * 未登录自动选社区：定位坐标可用时取最近社区，定位失败取默认列表第一个
 * @returns 社区与定位来源，无可用社区时返回 null
 */
async function autoPickCommunity(): Promise<AutoPickResult | null> {
  const coords = await locateCoords()
  if (coords) {
    const nearby = await fetchFirstCommunity(coords)
    if (nearby) return { community: nearby, coords, source: coords.source }
  }
  const first = await fetchFirstCommunity()
  return first ? { community: first, coords: null, source: 'none' } : null
}

/**
 * 获取定位坐标：优先系统定位，失败按出口 IP 定位兜底
 * 坐标系：H5 端用浏览器原生 wgs84（gcj02 转换需在 manifest 配置地图厂商 key，
 * H5 未配置），其他端用 gcj02；wgs84 与 gcj02 存在约 100~500m 系统偏差，
 * 对就近排序（偏差对全体社区一致）与社区级距离展示可接受
 * @returns 经纬度与定位来源，两种定位均失败时返回 null
 */
function locateCoords(): Promise<{
  longitude: number
  latitude: number
  source: 'gps' | 'ip'
} | null> {
  // 坐标系按端裁剪：H5 用 wgs84，其他端用 gcj02。
  // 写成 let 默认值 + 条件编译覆盖，避免双分支声明被 tsc 判为重复声明
  let locationType: 'wgs84' | 'gcj02' = 'gcj02'
  // #ifdef H5
  locationType = 'wgs84'
  // #endif
  return new Promise((resolve) => {
    uni.getLocation({
      type: locationType,
      success: (res) => {
        resolve({ longitude: res.longitude, latitude: res.latitude, source: 'gps' })
      },
      fail: async (err) => {
        console.warn('[社区定位] 首页系统定位失败:', err)
        try {
          const res = await getIpLocation()
          const data = res.data
          if (data?.longitude && data?.latitude) {
            resolve({ longitude: data.longitude, latitude: data.latitude, source: 'ip' })
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
 * @param coords 定位坐标，缺省时按默认列表取第一个
 * @returns 社区，接口失败或列表为空时返回 null
 */
async function fetchFirstCommunity(coords?: {
  longitude: number
  latitude: number
}): Promise<Community | null> {
  try {
    const res = await getCommunityList({
      page: 1,
      page_size: 1,
      longitude: coords?.longitude,
      latitude: coords?.latitude,
    })
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