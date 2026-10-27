<template>
  <n-config-provider :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="themeOverrides">
    <n-message-provider>
      <n-dialog-provider>
        <router-view />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { onUnmounted, watch } from 'vue'
import { zhCN, dateZhCN } from 'naive-ui'
import { storeToRefs } from 'pinia'
import type { GlobalThemeOverrides } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { connectOrderNotify, disconnectOrderNotify } from '@/utils/orderNotify'

/**
 * 后台 WebSocket 长连接管理（订单语音通知 + 权限变更提醒）
 * 登录就绪且已选定社区即建立连接，**不设权限码门槛**：权限被收窄的管理员恰恰需要
 * 收到权限变更提醒以刷新本地快照，加门槛会让提醒对它自己失效（服务端网关会按握手
 * 时刻的权限快照过滤数据类事件，订单内容不会因为放宽建连条件而多发给无权限连接）；
 * 登出或未选定社区时断开（社区切换由 currentCommunityId 变化触发断开重连）
 */
const authStore = useAuthStore()
const { isReady, currentCommunityId } = storeToRefs(authStore)
watch(
  [isReady, currentCommunityId],
  ([ready, communityId]) => {
    if (ready && communityId > 0) {
      connectOrderNotify()
    } else {
      disconnectOrderNotify()
    }
  },
  { immediate: true },
)
onUnmounted(() => disconnectOrderNotify())

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