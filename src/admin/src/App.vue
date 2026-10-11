<template>
  <n-config-provider :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="themeOverrides">
    <n-message-provider>
      <router-view />
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { zhCN, dateZhCN } from 'naive-ui'
import type { GlobalThemeOverrides } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()

/**
 * 全局主题配置
 * 绿色系主题，匹配社区商超品牌色
 */
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#07c160',
    primaryColorHover: '#06ad56',
    primaryColorPressed: '#059a4c',
    primaryColorSuppl: '#07c160',
  },
}

/**
 * 应用启动时，若本地存在 token，则拉取管理员信息并恢复权限列表。
 * 避免页面刷新后路由守卫因权限为空而将合法路由误判为无权限。
 */
onMounted(() => {
  if (authStore.isAuthenticated) {
    authStore.fetchAdminInfo()
  }
})
</script>

<style lang="scss">
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
html, body, #app {
  height: 100%;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
}

/**
 * 全局响应式适配
 * 窄屏（笔记本半屏 / 系统缩放 / 平板）下，左右布局的表单自动切换为标签置顶，
 * 避免固定 label 宽度挤压输入框；宽屏不受影响
 */
@media (max-width: 768px) {
  .n-form.n-form--label-left .n-form-item {
    display: block;
  }
  .n-form.n-form--label-left .n-form-item-label {
    width: auto !important;
    min-width: 0 !important;
    text-align: left;
    padding-bottom: 4px;
  }
  // 弹窗在窄屏下占满可视宽度，避免固定 px 宽度溢出
  .n-card.n-modal {
    width: 94vw !important;
  }
}
</style>