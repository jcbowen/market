<template>
  <n-card title="全局 Banner 配置">
    <!-- 复用社区业务端的 BannerManager，scope=system 切换为系统管理语境：仅管理 community_id=0 的全平台通用 banner -->
    <BannerManager scope="system" />
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'SystemBannerList' })
import BannerManager from '@/views/marketing/components/BannerManager.vue'
</script>
