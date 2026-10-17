<template>
  <div v-if="items.length" class="trend-chart" :style="{ minWidth: items.length * 40 + 'px' }">
    <div v-for="item in items" :key="item.label" class="trend-col">
      <div class="trend-amount">¥{{ formatMoney(item.revenue) }}</div>
      <div class="trend-bar-wrap">
        <div
          class="trend-bar"
          :style="{ height: barHeight(item.revenue) }"
          :title="`${item.label}：营收 ¥${formatMoney(item.revenue)}，${item.order_count} 单`"
        />
      </div>
      <div class="trend-orders">{{ item.order_count }} 单</div>
      <div class="trend-date">{{ item.label }}</div>
    </div>
  </div>
  <n-empty v-else description="所选时间范围内暂无营收数据" class="trend-empty" />
</template>

<script setup lang="ts">
// 组件名供 keep-alive include 匹配（父页面缓存场景）
defineOptions({ name: 'TrendBars' })
import { computed } from 'vue'
import { formatMoney } from '@/utils/format'

/**
 * 营收趋势柱状图（纯 CSS 实现，不依赖图表库）
 * 宽容器横向滚动，适配 7~30+ 天的展示
 */

export interface TrendBarItem {
  /** 展示标签（今天/昨天/MM-DD 等） */
  label: string
  revenue: number
  order_count: number
}

const props = defineProps<{ items: TrendBarItem[] }>()

/** 最大营收（柱高比例基准） */
const maxValue = computed(() => Math.max(0, ...props.items.map((i) => i.revenue)))

/**
 * 柱高：相对最大值按比例，无数据时留 4px 可视高度
 */
function barHeight(revenue: number): string {
  if (maxValue.value <= 0) return '4px'
  return `${Math.max(4, Math.round((revenue / maxValue.value) * 120))}px`
}
</script>

<style lang="scss" scoped>
.trend-chart {
  display: flex;
  align-items: flex-end;
  gap: 6px;
  padding: 8px 4px 0;
  overflow-x: auto;
}

.trend-col {
  flex: 1;
  min-width: 34px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.trend-amount {
  font-size: 11px;
  color: rgba(0, 0, 0, 0.55);
  white-space: nowrap;
}

.trend-bar-wrap {
  height: 124px;
  display: flex;
  align-items: flex-end;
  width: 100%;
  justify-content: center;
}

.trend-bar {
  width: 55%;
  max-width: 32px;
  min-height: 4px;
  border-radius: 4px 4px 2px 2px;
  background: linear-gradient(180deg, #2bd477, #07c160);
  transition: height 0.3s;
}

.trend-orders {
  font-size: 11px;
  color: rgba(0, 0, 0, 0.45);
}

.trend-date {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.65);
  white-space: nowrap;
}

.trend-empty {
  padding: 40px 0;
}
</style>
