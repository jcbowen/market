<template>
  <div class="pos-cashier-page">
    <!-- 无进行中班次：开工引导（收银前必须开工，后端双重校验） -->
    <div v-if="posStore.loaded && !posStore.currentShift" class="start-shift-guide">
      <n-icon :size="64" class="guide-icon"><StorefrontOutline /></n-icon>
      <div class="guide-title">欢迎上岗，{{ operatorName }}</div>
      <div class="guide-sub">当前没有进行中的班次，开工后即可开始收银</div>
      <n-input v-model:value="startNote" placeholder="开班备注（选填）" class="guide-note" />
      <n-button type="primary" size="large" :loading="starting" class="guide-btn" @click="onStartShift">
        开始营业
      </n-button>
    </div>

    <!-- 进行中班次：四 Tab 工作区 -->
    <template v-else>
      <n-tabs
        v-model:value="activeTab"
        type="line"
        size="large"
        class="cashier-tabs"
        :tabs-padding="16"
      >
        <n-tab name="order" tab="商品下单" />
        <n-tab name="direct" tab="直接收款" />
        <n-tab name="verify" tab="取货核销" />
        <n-tab name="orders" tab="订单查询" />
      </n-tabs>
      <div class="tab-body">
        <OrderTab v-if="activeTab === 'order'" />
        <DirectPayTab v-else-if="activeTab === 'direct'" />
        <VerifyTab v-else-if="activeTab === 'verify'" />
        <OrdersTab v-else @import-to-cashier="activeTab = 'order'" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { StorefrontOutline } from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { usePosStore } from '@/stores/pos'
import { startPosShift } from '@/api/order'
import OrderTab from './OrderTab.vue'
import DirectPayTab from './DirectPayTab.vue'
import VerifyTab from './VerifyTab.vue'
import OrdersTab from './OrdersTab.vue'

const message = useMessage()
const authStore = useAuthStore()
const posStore = usePosStore()

/** 当前激活 Tab */
const activeTab = ref<'order' | 'direct' | 'verify' | 'orders'>('order')

/** 开工引导：备注与提交状态 */
const startNote = ref('')
const starting = ref(false)

/** 操作员显示名 */
const operatorName = computed(() => authStore.admin?.nickname || authStore.admin?.username || '收银员')

/**
 * 开工：创建进行中班次后进入商品下单 Tab
 */
async function onStartShift() {
  starting.value = true
  try {
    await startPosShift(startNote.value.trim() || undefined)
    message.success('开工成功，开始营业')
    await posStore.loadCurrentShift(true)
  } catch (error: unknown) {
    message.error(error instanceof Error ? error.message : '开工失败，请重试')
  } finally {
    starting.value = false
  }
}
</script>

<style lang="scss" scoped>
.pos-cashier-page {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.start-shift-guide {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
}
.guide-icon {
  color: #07c160;
}
.guide-title {
  font-size: 22px;
  font-weight: 700;
}
.guide-sub {
  font-size: 14px;
  color: #999;
}
.guide-note {
  width: 320px;
  margin-top: 8px;
}
.guide-btn {
  min-width: 200px;
}
.cashier-tabs {
  flex-shrink: 0;
  background: #fff;
}
.tab-body {
  flex: 1;
  min-height: 0;
}
</style>
