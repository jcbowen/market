<template>
  <!-- 仅图标模式（2026-10-25 修订，列表用）：悬浮/触屏点击经 n-tooltip 显示文字，节省列宽 -->
  <n-tooltip v-if="iconOnly && iconDef" placement="top">
    <template #trigger>
      <span class="pay-type-label icon-only">
        <n-icon :color="iconDef.color" :size="18" class="pay-icon">
          <component :is="iconDef.icon" />
        </n-icon>
      </span>
    </template>
    {{ label }}
  </n-tooltip>
  <span v-else class="pay-type-label">
    <n-icon v-if="iconDef" :color="iconDef.color" :size="16" class="pay-icon">
      <component :is="iconDef.icon" />
    </n-icon>
    <span v-if="label" class="pay-text">{{ label }}</span>
    <span v-else class="pay-text muted">-</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NIcon, NTooltip } from 'naive-ui'
import { payTypeIcon, payTypeText } from '@/constants/order'

/**
 * 支付方式图标+文字标签（2026-10-25 订单列表图标化）
 * 后台订单列表/详情与收银台订单查询共用：
 * 有支付方式码时渲染渠道品牌色图标 + 文案，无码（待支付/未知）仅显示文字或 -
 * iconOnly 模式（列表）：仅渲染图标，文字放 tooltip（桌面悬浮 / 触屏点按均可触发）
 */

const props = defineProps<{
  /** 支付方式码（0/undefined 显示 - 或后端文案） */
  payType?: number
  /** 后端返回的支付方式文案（优先于前端映射，保持口径一致） */
  text?: string
  /** 仅图标模式：列表场景用，悬浮/点击显示文字 */
  iconOnly?: boolean
}>()

/** 图标定义（未知方式返回 null，不渲染图标） */
const iconDef = computed(() => (props.payType ? payTypeIcon(props.payType) : null))

/** 展示文案：优先后端文案，其次前端映射 */
const label = computed(() => props.text || (props.payType ? payTypeText(props.payType) : ''))
</script>

<style lang="scss" scoped>
.pay-type-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}
.pay-icon {
  flex-shrink: 0;
}
.pay-text.muted {
  color: #c2c2c2;
}
</style>
