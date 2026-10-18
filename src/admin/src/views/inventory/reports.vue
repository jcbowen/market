<template>
  <n-card title="报表中心">
    <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
      <!-- 进销存汇总 -->
      <n-tab-pane name="summary" tab="进销存汇总">
        <n-space style="margin-bottom: 12px" vertical>
          <n-text depth="3">公式：期末库存 = 期初库存 + 入库 - 出库（含销售/报损/领用/盘点调整）</n-text>
          <n-space>
            <n-date-picker v-model:value="summaryDateRange" type="daterange" clearable />
            <n-input
              v-model:value="summaryKeyword"
              placeholder="商品名 / SKU 编码"
              style="width: 200px"
              clearable
              @keyup.enter="loadSummary"
            />
            <n-button type="primary" :loading="summaryLoading" @click="loadSummary">查询</n-button>
          </n-space>
        </n-space>
        <n-data-table
          :columns="summaryColumns"
          :data="summaryList"
          :loading="summaryLoading"
          :pagination="summaryPagination"
          @update:page="onSummaryPageChange"
        />
      </n-tab-pane>

      <!-- 毛利分析 -->
      <n-tab-pane name="profit" tab="毛利分析">
        <n-space style="margin-bottom: 12px" vertical>
          <n-text depth="3">按已支付订单统计：销售额 - 成本 = 毛利；成本取订单发生时 SKU 成本价快照</n-text>
          <n-space>
            <n-date-picker v-model:value="profitDateRange" type="daterange" clearable />
            <n-input
              v-model:value="profitKeyword"
              placeholder="商品名 / SKU 编码"
              style="width: 200px"
              clearable
              @keyup.enter="loadProfit"
            />
            <n-button type="primary" :loading="profitLoading" @click="loadProfit">查询</n-button>
          </n-space>
        </n-space>
        <n-data-table
          :columns="profitColumns"
          :data="profitList"
          :loading="profitLoading"
          :pagination="profitPagination"
          @update:page="onProfitPageChange"
        />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockReports' })
import { h, ref } from 'vue'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getInventorySummaryReport, getProfitReport } from '@/api/inventory'
import type { InventorySummaryItem, ProfitReportItem } from '@/api/types'

/**
 * 报表中心
 * 进销存汇总（期初+入-出=期末）与毛利分析两个页签
 */
const activeTab = ref('summary')

function formatDate(timestamp: number | null) {
  if (!timestamp) return ''
  const d = new Date(timestamp)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

// ---------- 进销存汇总 ----------
const summaryDateRange = ref<[number, number] | null>(null)
const summaryKeyword = ref('')
const summaryList = ref<InventorySummaryItem[]>([])
const summaryLoading = ref(false)
const summaryPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const summaryColumns: DataTableColumns<InventorySummaryItem> = [
  { title: 'SKU 编码', key: 'sku_code', width: 150 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 120, ellipsis: { tooltip: true } },
  { title: '期初库存', key: 'opening_stock', width: 100 },
  {
    title: '入库',
    key: 'in_quantity',
    width: 90,
    render: (row) => h('span', { style: 'color: #d03050' }, { default: () => `+${row.in_quantity}` }),
  },
  {
    title: '出库',
    key: 'out_quantity',
    width: 90,
    render: (row) => h('span', { style: 'color: #2080f0' }, { default: () => `-${row.out_quantity}` }),
  },
  {
    title: '期末库存',
    key: 'closing_stock',
    width: 100,
    render: (row) => h(NTag, { size: 'small', type: row.closing_stock <= 0 ? 'warning' : 'success' }, { default: () => String(row.closing_stock) }),
  },
  {
    title: '成本价',
    key: 'cost_price',
    width: 100,
    render: (row) => `¥${Number(row.cost_price || 0).toFixed(2)}`,
  },
  {
    title: '库存金额',
    key: 'stock_value',
    width: 120,
    render: (row) => `¥${(row.closing_stock * row.cost_price).toFixed(2)}`,
  },
]

async function loadSummary() {
  summaryLoading.value = true
  try {
    const params: Record<string, unknown> = {
      page: summaryPagination.value.page,
      page_size: summaryPagination.value.pageSize,
      keyword: summaryKeyword.value || undefined,
    }
    if (summaryDateRange.value) {
      params.start_date = formatDate(summaryDateRange.value[0])
      params.end_date = formatDate(summaryDateRange.value[1])
    }
    const res = await getInventorySummaryReport(params as never)
    if (res.data.code === 0) {
      summaryList.value = res.data.data.list
      summaryPagination.value.itemCount = res.data.data.total
    }
  } finally {
    summaryLoading.value = false
  }
}

function onSummaryPageChange(page: number) {
  summaryPagination.value.page = page
  loadSummary()
}

// ---------- 毛利分析 ----------
const profitDateRange = ref<[number, number] | null>(null)
const profitKeyword = ref('')
const profitList = ref<ProfitReportItem[]>([])
const profitLoading = ref(false)
const profitPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const profitColumns: DataTableColumns<ProfitReportItem> = [
  { title: 'SKU 编码', key: 'sku_code', width: 150 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 120, ellipsis: { tooltip: true } },
  { title: '销量', key: 'quantity', width: 90 },
  {
    title: '销售额',
    key: 'sales_amount',
    width: 120,
    render: (row) => `¥${Number(row.sales_amount || 0).toFixed(2)}`,
  },
  {
    title: '成本',
    key: 'cost_amount',
    width: 120,
    render: (row) => `¥${Number(row.cost_amount || 0).toFixed(2)}`,
  },
  {
    title: '毛利',
    key: 'profit',
    width: 120,
    render: (row) =>
      h('span', { style: `color: ${row.profit >= 0 ? '#d03050' : '#2080f0'}` }, {
        default: () => `¥${Number(row.profit || 0).toFixed(2)}`,
      }),
  },
  {
    title: '毛利率',
    key: 'profit_rate',
    width: 100,
    render: (row) => {
      const rate = row.sales_amount > 0 ? (row.profit / row.sales_amount) * 100 : 0
      return `${rate.toFixed(1)}%`
    },
  },
]

async function loadProfit() {
  profitLoading.value = true
  try {
    const params: Record<string, unknown> = {
      page: profitPagination.value.page,
      page_size: profitPagination.value.pageSize,
      keyword: profitKeyword.value || undefined,
    }
    if (profitDateRange.value) {
      params.start_date = formatDate(profitDateRange.value[0])
      params.end_date = formatDate(profitDateRange.value[1])
    }
    const res = await getProfitReport(params as never)
    if (res.data.code === 0) {
      profitList.value = res.data.data.list
      profitPagination.value.itemCount = res.data.data.total
    }
  } finally {
    profitLoading.value = false
  }
}

function onProfitPageChange(page: number) {
  profitPagination.value.page = page
  loadProfit()
}

// ---------- 页签懒加载 ----------
function onTabChange(value: string) {
  if (value === 'profit') loadProfit()
}

loadSummary()
</script>
