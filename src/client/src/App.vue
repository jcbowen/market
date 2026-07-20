<template>
  <view class="app">
    <slot />
  </view>
</template>

<script setup lang="ts">
import { onLaunch } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getCommunityList } from '@/api/user'

onLaunch(() => {
  initCommunity()
})

/**
 * 初始化社区信息
 * - 未登录：不自动选择，等待用户首次手动选择并写入本地缓存
 * - 已登录：本地无社区缓存时，尝试从用户信息恢复最近一次使用的社区
 */
async function initCommunity() {
  const userStore = useUserStore()
  if (!userStore.isLogin) {
    // 未登录时不自动选择社区，由用户在首页/分类页手动选择后缓存
    return
  }

  if (userStore.communityInfo?.id && userStore.communityInfo?.name) return

  // 已登录且本地无社区缓存，先从用户信息中恢复 ID，再拉取社区名称补全信息
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
</script>

<style lang="scss">
@import 'uview-plus/index.scss';

.app {
  min-height: 100vh;
  background-color: #f5f5f5;
}
</style>