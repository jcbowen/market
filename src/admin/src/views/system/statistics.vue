<template>
  <div class="system-statistics-page">
    <!-- 全平台概览：时间范围 + 核心指标 -->
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

    <!-- 社区数据排行 -->
    <n-card size="small" class="section-card">
      <template #header>
        <div class="card-header">
          <span>社区数据排行</span>
          <n-text depth="3" style="font-size: 12px">按销售额排序，可点击表头切换</n-text>
        </div>
      </template>
      <n-data-table
        :columns="communityColumns"
        :data="communityRows"
        :loading="communityLoading"
        :row-key="(row: CommunityRow) => row.id"
        :max-height="420"
      >
        <template #empty>
          <n-empty description="暂无社区数据" class="trend-empty" />
        </template>
      </n-data-table>
    </n-card>

    <div class="two-col">
      <!-- 全平台营收趋势 -->
      <n-card size="small" title="全平台营收趋势" class="section-card">
        <TrendBars :items="trendItems" />
      </n-card>

      <!-- 全平台热销 -->
      <n-card size="small" title="全平台热销 TOP10" class="section-card">
        <div v-if="hotProducts.length" class="rank-list">
          <div v-for="(item, index) in hotProducts" :key="item.product_id" class="rank-item">
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
        <n-empty v-else description="暂无销售数据" class="trend-empty" />
      </n-card>
    </div>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'SystemStatistics' })
import { computed, h, onMounted, ref } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { NText } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getProductRankings, getRevenueReport, getSalesStatistics } from '@/api/statistics'
import type { ProductRankingItem, RevenueReportItem, SalesStatistics } from '@/api/statistics'
import { getCommunityList } from '@/api/user'
import TrendBars from '@/components/TrendBars.vue'
import { formatMoney, formatMoneyFixed } from '@/utils/format'

/**
 * 全平台统计页（系统管理空间，仅超管）
 * - 口径：scope=all 全平台聚合（网关 resolveStatsCommunityID 放行，普管传参被忽略）
 * - 社区排行：逐社区调用销售统计（community_id 参数）后前端汇总，支持按销售额/订单数排序
 * - 营收趋势与热销榜为全平台口径
 */

type PresetValue = 'today' | '7d' | '30d' | 'month' | 'custom'

const presetOptions: Array<{ label: string; value: PresetValue }> = [
  { label: '今日', value: 'today' },
  { label: '近 7 日', value: '7d' },
  { label: '近 30 日', value: '30d' },
  { label: '本月', value: 'month' },
]

const activePreset = ref<PresetValue>('30d')
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
    case '7d':
      return [offset(6), today]
    case 'month':
      return [`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`, today]
    case '30d':
    default:
      return [offset(29), today]
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

// --- 全平台核心指标 ---

const globalSales = ref<SalesStatistics | null>(null)
const communityCount = ref(0)

const summaryCards = computed(() => [
  {
    label: '全平台销售额',
    value: formatMoney(globalSales.value?.pay_amount ?? 0),
    prefix: '¥',
    extra: '全部社区，不含已退款订单',
  },
  {
    label: '全平台订单数',
    value: String(globalSales.value?.total_orders ?? 0),
    prefix: '',
    extra: '有效订单总数',
  },
  {
    label: '客单价',
    value: formatMoney(globalSales.value?.avg_order_amount ?? 0),
    prefix: '¥',
    extra: '销售额 / 订单数',
  },
  {
    label: '社区数',
    value: String(communityCount.value),
    prefix: '',
    extra: '已接入商超的社区',
  },
])

// --- 社区排行 ---

interface CommunityRow {
  id: number
  name: string
  pay_amount: number
  total_orders: number
  avg_order_amount: number
}

const communityRows = ref<CommunityRow[]>([])
const communityLoading = ref(false)

const communityColumns = computed<DataTableColumns<CommunityRow>>(() => [
  {
    title: '#',
    key: 'rank',
    width: 56,
    render: (_row, index) =>
      h('span', { class: ['rank-badge', index < 3 ? `rank-${index + 1}` : ''] }, { default: () => index + 1 }),
  },
  { title: '社区名称', key: 'name', minWidth: 160, ellipsis: { tooltip: true } },
  {
    title: '销售额',
    key: 'pay_amount',
    width: 140,
    sorter: (a, b) => a.pay_amount - b.pay_amount,
    defaultSortOrder: 'descend',
    render: (row) => h(NText, { strong: true }, { default: () => `¥${formatMoneyFixed(row.pay_amount)}` }),
  },
  {
    title: '订单数',
    key: 'total_orders',
    width: 110,
    sorter: (a, b) => a.total_orders - b.total_orders,
    render: (row) => `${row.total_orders}`,
  },
  {
    title: '客单价',
    key: 'avg_order_amount',
    width: 120,
    sorter: (a, b) => a.avg_order_amount - b.avg_order_amount,
    render: (row) => `¥${formatMoneyFixed(row.avg_order_amount)}`,
  },
])

// --- 全平台趋势与热销 ---

const trendItems = ref<Array<{ label: string; revenue: number; order_count: number }>>([])
const hotProducts = ref<ProductRankingItem[]>([])

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

async function loadData() {
  const [start, end] = currentRange()

  const [globalRes, communityListRes, trendRes, rankRes] = await Promise.allSettled([
    getSalesStatistics({ start_date: start, end_date: end, scope: 'all' }),
    getCommunityList({ page: 1, page_size: 200 }),
    getRevenueReport({ start_date: start, end_date: end, group_by: 'day', scope: 'all' }),
    getProductRankings({ start_date: start, end_date: end, limit: 10, scope: 'all' }),
  ])

  if (globalRes.status === 'fulfilled' && globalRes.value.data.code === 0) {
    globalSales.value = globalRes.value.data.data
  }

  let communities: Array<{ id: number; name: string }> = []
  if (communityListRes.status === 'fulfilled' && communityListRes.value.data.code === 0) {
    communities = (communityListRes.value.data.data.list || []).map((c) => ({ id: c.id, name: c.name }))
    communityCount.value = communityListRes.value.data.data.total
  }

  if (trendRes.status === 'fulfilled' && trendRes.value.data.code === 0) {
    buildTrend(trendRes.value.data.data || [], start, end)
  }
  if (rankRes.status === 'fulfilled' && rankRes.value.data.code === 0) {
    hotProducts.value = rankRes.value.data.data || []
  }

  // 社区排行：逐社区查询销售统计（口径与单社区统计页完全一致）
  communityLoading.value = true
  try {
    const results = await Promise.allSettled(
      communities.map((c) => getSalesStatistics({ start_date: start, end_date: end, community_id: c.id })),
    )
    const rows: CommunityRow[] = []
    results.forEach((r, idx) => {
      if (r.status === 'fulfilled' && r.value.data.code === 0) {
        const s = r.value.data.data
        rows.push({
          id: communities[idx].id,
          name: communities[idx].name,
          pay_amount: s.pay_amount,
          total_orders: s.total_orders,
          avg_order_amount: s.avg_order_amount,
        })
      }
    })
    rows.sort((a, b) => b.pay_amount - a.pay_amount)
    communityRows.value = rows
  } finally {
    communityLoading.value = false
  }
}

onMounted(loadData)
// keep-alive 缓存下切回本页不重新挂载，需在激活时刷新，避免展示离开前的旧数据
refreshOnActivated(loadData)
</script>

<style lang="scss" scoped>
.system-statistics-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 双列区（趋势/热销）：naive-ui n-grid 单断点 cols 存在静默不渲染问题，改用 CSS grid */
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

.card-header {
  display: flex;
  align-items: baseline;
  gap: 10px;
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

/* 表格内排名徽标 */
:deep(.rank-badge) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.55);
  background: rgba(0, 0, 0, 0.06);

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

/* 热销排行 */
.rank-list {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.rank-item {
  display: flex;
  align-items: center;
  gap: 12px;
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
