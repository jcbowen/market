<template>
  <div class="statistics-page">
    <n-spin :show="loading">
    <!-- 概览：时间范围 + 核心指标 -->
    <n-card size="small" class="section-card">
      <div class="range-bar">
        <n-radio-group v-model:value="activePreset" size="small" @update:value="onPresetChange">
          <n-radio-button v-for="p in presetOptions" :key="p.value" :value="p.value">{{ p.label }}</n-radio-button>
        </n-radio-group>
        <n-date-picker v-model:value="dateRange" type="daterange" clearable size="small" class="range-picker" @update:value="onCustomRange" />
      </div>
      <n-grid cols="2 s:4" :x-gap="16" :y-gap="16" responsive="screen" class="summary-grid">
        <n-grid-item v-for="item in summaryCards" :key="item.label">
          <div class="summary-item">
            <div class="summary-label">{{ item.label }}</div>
            <div class="summary-value">
              <span v-if="item.prefix" class="summary-prefix">{{ item.prefix }}</span>{{ item.value }}
            </div>
            <div class="summary-extra">{{ item.extra }}</div>
          </div>
        </n-grid-item>
      </n-grid>
    </n-card>

    <div class="two-col">
      <!-- 营收趋势 -->
      <n-card size="small" title="营收趋势" class="section-card">
        <TrendBars :items="trendItems" />
      </n-card>

      <!-- 订单状态分布 -->
      <n-card size="small" title="订单状态分布" class="section-card">
        <template v-if="orderTotal > 0">
          <div class="status-bar">
            <div
              v-for="seg in statusSegments"
              :key="seg.label"
              class="status-seg"
              :style="{ width: seg.pct + '%', background: seg.color }"
              :title="`${seg.label}：${seg.count} 单（${seg.pct.toFixed(1)}%）`"
            />
          </div>
          <div class="status-legend">
            <div v-for="seg in statusSegments" :key="seg.label" class="legend-item">
              <span class="legend-dot" :style="{ background: seg.color }" />
              <span class="legend-label">{{ seg.label }}</span>
              <span class="legend-count">{{ seg.count }} 单</span>
              <span class="legend-pct">{{ seg.pct.toFixed(1) }}%</span>
            </div>
          </div>
        </template>
        <n-empty v-else description="所选时间范围内暂无订单" class="trend-empty" />
      </n-card>
    </div>

    <!-- 热销商品 -->
    <n-card size="small" title="热销商品 TOP10" class="section-card">
      <div v-if="hotProducts.length" class="rank-list rank-grid">
        <div v-for="(item, index) in hotProducts" :key="item.product_id" class="rank-item" @click="goProductList(item.product_name)">
          <span :class="['rank-badge', rankClass(index)]">{{ index + 1 }}</span>
          <div class="rank-info">
            <div class="rank-name">{{ item.product_name }}</div>
            <n-progress
              type="line"
              :show-indicator="false"
              :height="6"
              :border-radius="3"
              :percentage="rankPercent(item.sales_count)"
              color="#07c160"
              rail-color="rgba(7, 193, 96, 0.12)"
            />
          </div>
          <div class="rank-num">
            <div class="rank-sales">{{ item.sales_count }} 件</div>
            <div class="rank-amount">¥{{ formatMoney(item.sales_amount) }}</div>
          </div>
        </div>
      </div>
      <n-empty v-else description="所选时间范围内暂无销售数据" class="trend-empty" />
    </n-card>
    </n-spin>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'Statistics' })
import { computed, onMounted, ref } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { useRouter } from 'vue-router'
import { getOrderStatistics, getProductRankings, getRevenueReport, getSalesStatistics } from '@/api/statistics'
import type { OrderStatistics, ProductRankingItem, RevenueReportItem } from '@/api/statistics'
import TrendBars from '@/components/TrendBars.vue'
import { formatMoney } from '@/utils/format'

/**
 * 数据统计页（当前社区口径）
 * - 时间范围：今日/近7日/近30日/本月快捷切换 + 自定义区间
 * - 核心指标：销售额/订单数/客单价/日均销售额（金额统一经 formatMoney，避免浮点尾差直出）
 * - 营收趋势（按日聚合，缺数据日期补零）、订单状态分布、热销 TOP10
 * 统计口径：销售额与营收只计有效订单（排除待支付 1 / 已取消 6 / 已退款 8）；
 * 退款中 7 的订单退款尚未成功，仍计入，避免退款一发起就把当日营收整单抹掉
 */

type PresetValue = 'today' | '7d' | '30d' | 'month' | 'custom'

const router = useRouter()

/**
 * 跳转商品列表并按商品名预筛选（热销排行点击下钻）
 * @param productName 商品名
 */
function goProductList(productName: string) {
  router.push({ path: '/products', query: { keyword: productName } })
}

const presetOptions: Array<{ label: string; value: PresetValue }> = [
  { label: '今日', value: 'today' },
  { label: '近 7 日', value: '7d' },
  { label: '近 30 日', value: '30d' },
  { label: '本月', value: 'month' },
]

const activePreset = ref<PresetValue>('7d')
const dateRange = ref<[number, number] | null>(null)

// --- 日期工具 ---

function fmtDate(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function toDateStr(ms: number): string {
  return fmtDate(new Date(ms))
}

function daysBetween(startStr: string, endStr: string): number {
  const start = new Date(startStr + 'T00:00:00').getTime()
  const end = new Date(endStr + 'T00:00:00').getTime()
  return Math.max(1, Math.round((end - start) / 86400000) + 1)
}

/** 根据当前预设计算 [start, end] 日期字符串 */
function currentRange(): [string, string] {
  if (activePreset.value === 'custom' && dateRange.value) {
    return [toDateStr(dateRange.value[0]), toDateStr(dateRange.value[1])]
  }
  const now = new Date()
  const today = fmtDate(now)
  const offset = (days: number): string => {
    const d = new Date()
    d.setDate(d.getDate() - days)
    return fmtDate(d)
  }
  switch (activePreset.value) {
    case 'today':
      return [today, today]
    case '30d':
      return [offset(29), today]
    case 'month':
      return [`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`, today]
    case '7d':
    default:
      return [offset(6), today]
  }
}

function onPresetChange() {
  loadData()
}

function onCustomRange(value: [number, number] | null) {
  if (!value) return
  activePreset.value = 'custom'
  loadData()
}

// --- 核心指标 ---

const sales = ref({ total_amount: 0, pay_amount: 0, total_orders: 0, avg_order_amount: 0 })

const summaryCards = computed(() => {
  const [start, end] = currentRange()
  const days = daysBetween(start, end)
  return [
    { label: '销售额', value: formatMoney(sales.value.pay_amount), prefix: '¥', extra: '不含已退款订单' },
    { label: '订单数', value: String(sales.value.total_orders), prefix: '', extra: '有效订单总数' },
    { label: '客单价', value: formatMoney(sales.value.avg_order_amount), prefix: '¥', extra: '销售额 / 订单数' },
    { label: '日均销售额', value: formatMoney(sales.value.pay_amount / days), prefix: '¥', extra: `共 ${days} 天` },
  ]
})

// --- 订单状态分布 ---

const orderStats = ref<OrderStatistics | null>(null)

const orderTotal = computed(() => orderStats.value?.total_orders ?? 0)

interface StatusSegment {
  label: string
  count: number
  color: string
  pct: number
}

const statusSegments = computed<StatusSegment[]>(() => {
  const s = orderStats.value
  if (!s || !s.total_orders) return []
  const raw = [
    { label: '已完成', count: s.completed, color: '#07c160' },
    { label: '待发货', count: s.pending_ship, color: '#10aeff' },
    { label: '待收货', count: s.pending_receive, color: '#13c2c2' },
    { label: '待支付', count: s.pending_pay, color: '#faad14' },
    { label: '售后中', count: s.refunding, color: '#fa5151' },
    { label: '已取消', count: s.cancelled, color: '#bfbfbf' },
  ]
  return raw
    .filter((r) => r.count > 0)
    .map((r) => ({ ...r, pct: (r.count / s.total_orders) * 100 }))
})

// --- 营收趋势 ---

const trendItems = ref<Array<{ label: string; revenue: number; order_count: number }>>([])

/**
 * 补齐日期序列：后端仅返回有数据的日期，缺的日子补零
 */
function buildTrend(list: RevenueReportItem[], startStr: string, endStr: string) {
  const byDate = new Map(list.map((i) => [i.date, i]))
  const start = new Date(startStr + 'T00:00:00')
  const end = new Date(endStr + 'T00:00:00')
  const days = Math.min(90, Math.max(1, Math.round((end.getTime() - start.getTime()) / 86400000) + 1))
  const today = fmtDate(new Date())
  const yesterday = (() => {
    const d = new Date()
    d.setDate(d.getDate() - 1)
    return fmtDate(d)
  })()
  const items: Array<{ label: string; revenue: number; order_count: number }> = []
  for (let i = 0; i < days; i++) {
    const d = new Date(start.getTime() + i * 86400000)
    const date = fmtDate(d)
    const hit = byDate.get(date)
    items.push({
      label: date === today ? '今天' : date === yesterday ? '昨天' : date.slice(5),
      revenue: hit?.revenue ?? 0,
      order_count: hit?.order_count ?? 0,
    })
  }
  trendItems.value = items
}

// --- 热销 TOP10 ---

const hotProducts = ref<ProductRankingItem[]>([])

function rankClass(index: number): string {
  if (index === 0) return 'rank-1'
  if (index === 1) return 'rank-2'
  if (index === 2) return 'rank-3'
  return ''
}

function rankPercent(salesCount: number): number {
  const max = Math.max(1, ...hotProducts.value.map((i) => i.sales_count))
  return Math.max(4, Math.round((salesCount / max) * 100))
}

// --- 数据加载 ---

// 整页加载态 + 请求序号守卫：四个数据各自独立 ref，快速切换日期时旧响应可能
// 晚于新响应返回并乱序覆盖，呈现混合区间数据；序号过期即丢弃
const loading = ref(false)
let loadSeq = 0

async function loadData() {
  const seq = ++loadSeq
  const [start, end] = currentRange()
  loading.value = true
  try {
    const [salesRes, orderRes, revenueRes, rankRes] = await Promise.allSettled([
      getSalesStatistics({ start_date: start, end_date: end }),
      getOrderStatistics({ start_date: start, end_date: end }),
      getRevenueReport({ start_date: start, end_date: end, group_by: 'day' }),
      getProductRankings({ start_date: start, end_date: end, limit: 10 }),
    ])
    if (seq !== loadSeq) return
    if (salesRes.status === 'fulfilled' && salesRes.value.data.code === 0) {
      sales.value = salesRes.value.data.data
    }
    if (orderRes.status === 'fulfilled' && orderRes.value.data.code === 0) {
      orderStats.value = orderRes.value.data.data
    }
    if (revenueRes.status === 'fulfilled' && revenueRes.value.data.code === 0) {
      buildTrend(revenueRes.value.data.data || [], start, end)
    }
    if (rankRes.status === 'fulfilled' && rankRes.value.data.code === 0) {
      hotProducts.value = rankRes.value.data.data || []
    }
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

onMounted(loadData)
// keep-alive 缓存下切回本页不重新挂载，需在激活时刷新，避免展示离开前的旧数据
refreshOnActivated(loadData)
</script>

<style lang="scss" scoped>
.statistics-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 双列区（趋势/状态）：naive-ui n-grid 单断点 cols 存在静默不渲染问题，改用 CSS grid */
.two-col {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;

  @media (min-width: 1024px) {
    grid-template-columns: 1fr 1fr;
  }
}

.range-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}

.range-picker {
  width: 260px;
}

/* 核心指标 */
.summary-item {
  padding: 4px 2px;
}

.summary-label {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.45);
}

.summary-value {
  margin-top: 4px;
  font-size: 24px;
  font-weight: 600;
  line-height: 1.2;
  color: rgba(0, 0, 0, 0.85);
  white-space: nowrap;
}

.summary-prefix {
  font-size: 14px;
  font-weight: 500;
  margin-right: 2px;
}

.summary-extra {
  margin-top: 4px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.4);
}

/* 状态分布 */
.status-bar {
  display: flex;
  height: 14px;
  border-radius: 7px;
  overflow: hidden;
  margin: 8px 0 16px;
}

.status-seg {
  height: 100%;
  min-width: 4px;
}

.status-legend {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 10px 16px;
}

.legend-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.legend-dot {
  width: 10px;
  height: 10px;
  border-radius: 3px;
  flex-shrink: 0;
}

.legend-label {
  color: rgba(0, 0, 0, 0.65);
}

.legend-count {
  color: rgba(0, 0, 0, 0.85);
  font-weight: 500;
}

.legend-pct {
  margin-left: auto;
  color: rgba(0, 0, 0, 0.4);
  font-size: 12px;
}

/* 热销排行：两列自适应 */
.rank-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 14px 24px;
}

.rank-item {
  display: flex;
  align-items: center;
  gap: 12px;
  /* 点击跳商品列表（keyword 预筛） */
  cursor: pointer;
  border-radius: 6px;
  padding: 2px 4px;
  transition: background-color 0.2s;
}

.rank-item:hover {
  background-color: rgba(7, 193, 96, 0.06);
}

.rank-badge {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.55);
  background: rgba(0, 0, 0, 0.06);
  flex-shrink: 0;

  &.rank-1 {
    color: #fff;
    background: #faad14;
  }

  &.rank-2 {
    color: #fff;
    background: #8c8c8c;
  }

  &.rank-3 {
    color: #fff;
    background: #b46225;
  }
}

.rank-info {
  flex: 1;
  min-width: 0;
}

.rank-name {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.8);
  margin-bottom: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rank-num {
  text-align: right;
  flex-shrink: 0;
}

.rank-sales {
  font-size: 13px;
  font-weight: 500;
  color: #07c160;
}

.rank-amount {
  margin-top: 2px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
}

.trend-empty {
  padding: 40px 0;
}
</style>
