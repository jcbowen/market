<template>
  <n-card title="库存工作台">
    <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
      <!-- 库存列表 -->
      <n-tab-pane name="stock" tab="库存列表">
        <n-space style="margin-bottom: 12px">
          <n-input v-model:value="stockKeyword" placeholder="商品名 / SKU 编码" style="width: 220px" clearable @keyup.enter="onStockSearch" />
          <n-button type="primary" @click="onStockSearch">查询</n-button>
          <n-button @click="goPurchase">去补货</n-button>
        </n-space>
        <n-data-table
          :columns="stockColumns"
          :data="stockList"
          :loading="stockLoading"
          :pagination="stockPagination"
          remote
          @update:page="onStockPageChange"
        />
      </n-tab-pane>

      <!-- 库存预警 -->
      <n-tab-pane name="warnings" tab="库存预警">
        <n-space style="margin-bottom: 12px" vertical>
          <n-text depth="3">
            低库存与临期批次预警；可勾选部分低库存项后「一键补货」，未勾选则按全部低库存项生成（临期项不参与补货）。
          </n-text>
          <n-space>
            <n-button type="primary" :loading="replenishing" @click="replenish">一键补货（生成采购单）</n-button>
            <n-button @click="loadWarnings">刷新</n-button>
          </n-space>
        </n-space>
        <n-data-table
          :columns="warningColumns"
          :data="warningList"
          :loading="warningLoading"
          :row-key="(row: Warning) => row.sku_id"
          v-model:checked-row-keys="checkedWarningKeys"
        />
      </n-tab-pane>

      <!-- 库存流水 -->
      <n-tab-pane name="records" tab="库存流水">
        <n-space style="margin-bottom: 12px">
          <n-select v-model:value="recordType" :options="recordTypeOptions" placeholder="变动类型" clearable style="width: 160px" />
          <n-date-picker v-model:value="recordDateRange" type="daterange" clearable />
          <n-button type="primary" @click="onRecordSearch">查询</n-button>
        </n-space>
        <n-data-table
          :columns="recordColumns"
          :data="recordList"
          :loading="recordLoading"
          :pagination="recordPagination"
          remote
          @update:page="onRecordPageChange"
        />
      </n-tab-pane>

      <!-- 批次库存 -->
      <n-tab-pane name="batches" tab="批次库存">
        <n-space style="margin-bottom: 12px">
          <n-input v-model:value="batchKeyword" placeholder="商品 / 批次号" style="width: 220px" clearable @keyup.enter="onBatchSearch" />
          <n-checkbox v-model:checked="batchInStockOnly">仅看在库</n-checkbox>
          <n-button type="primary" @click="onBatchSearch">查询</n-button>
        </n-space>
        <n-data-table
          :columns="batchColumns"
          :data="batchList"
          :loading="batchLoading"
          :pagination="batchPagination"
          remote
          @update:page="onBatchPageChange"
        />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockWorkbench' })
import { h, ref } from 'vue'
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NTag, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import {
  getInventoryLogList,
  getSkuStockList,
  getStockBatchList,
  getStockWarnings,
  generatePurchaseOrder,
} from '@/api/inventory'
import type { InventoryLog, InventorySummaryItem, Sku, StockBatch } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 库存工作台
 * 库存列表 / 库存预警 / 库存流水 / 批次库存 四个页签合一
 */
const router = useRouter()
const route = useRoute()
const message = useMessage()
const activeTab = ref('stock')

// ---------- 库存列表 ----------
const stockKeyword = ref('')
const stockList = ref<Sku[]>([])
const stockLoading = ref(false)
const stockPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const stockColumns: DataTableColumns<Sku> = [
  { title: 'SKU ID', key: 'id', width: 90 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: 'SKU 编码', key: 'sku_code', width: 150 },
  { title: '规格', key: 'spec_name', width: 140, ellipsis: { tooltip: true } },
  { title: '售价', key: 'price', width: 100 },
  { title: '成本价', key: 'cost_price', width: 100 },
  {
    title: '库存',
    key: 'stock',
    width: 110,
    render(row) {
      const low = row.stock <= row.safety_stock
      return h(NTag, { type: low ? 'warning' : 'success', size: 'small' }, { default: () => String(row.stock) })
    },
  },
  { title: '安全库存', key: 'safety_stock', width: 100 },
]

async function loadStocks() {
  stockLoading.value = true
  try {
    const res = await getSkuStockList({
      page: stockPagination.value.page,
      page_size: stockPagination.value.pageSize,
      keyword: stockKeyword.value || undefined,
    })
    if (res.data.code === 0) {
      stockList.value = res.data.data.list
      stockPagination.value.itemCount = res.data.data.total
    }
  } finally {
    stockLoading.value = false
  }
}

function onStockPageChange(page: number) {
  stockPagination.value.page = page
  loadStocks()
}

/**
 * 库存列表查询：条件变化后必须回到第 1 页
 * 停在第 3 页改关键词时，服务端按新条件返回的总数可能不足 3 页，
 * 表格会显示空白（或继续叠加旧页码），用户以为「查不到数据」
 */
function onStockSearch() {
  stockPagination.value.page = 1
  loadStocks()
}

// ---------- 库存预警 ----------
type Warning = {
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  stock: number
  safety_stock: number
  warning_type: number
  expire_date: string
}
const warningList = ref<Warning[]>([])
const warningLoading = ref(false)
const replenishing = ref(false)
// 勾选的预警 SKU（row-key=sku_id）；空 = 不勾选，按全部低库存项生成
const checkedWarningKeys = ref<Array<string | number>>([])

const warningColumns: DataTableColumns<Warning> = [
  { type: 'selection' },
  { title: 'SKU 编码', key: 'sku_code', width: 150 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 130, ellipsis: { tooltip: true } },
  {
    title: '预警类型',
    key: 'warning_type',
    width: 110,
    render: (row) =>
      h(NTag, { type: row.warning_type === 1 ? 'warning' : 'error', size: 'small' }, {
        default: () => (row.warning_type === 1 ? '低库存' : '临期'),
      }),
  },
  { title: '当前库存', key: 'stock', width: 100 },
  { title: '安全库存', key: 'safety_stock', width: 100 },
  { title: '过期日期', key: 'expire_date', width: 130 },
]

async function loadWarnings() {
  warningLoading.value = true
  try {
    const res = await getStockWarnings()
    if (res.data.code === 0) {
      warningList.value = res.data.data
    }
  } finally {
    warningLoading.value = false
  }
}

/** 一键补货：勾选时仅按选中项生成建议，否则全量；带入新建采购单页预填明细 */
async function replenish() {
  // 勾选项中仅低库存（warning_type=1）参与补货，临期项由促销/清仓流程处理
  const selectedLowStock = checkedWarningKeys.value
    .map((key) => warningList.value.find((w) => w.sku_id === key))
    .filter((w): w is Warning => !!w && w.warning_type === 1)
    .map((w) => w.sku_id)
  if (checkedWarningKeys.value.length > 0 && selectedLowStock.length === 0) {
    message.warning('勾选项均为临期预警，不参与补货；请勾选低库存项')
    return
  }
  const skuIds = selectedLowStock.length > 0 ? selectedLowStock : undefined

  replenishing.value = true
  try {
    const res = await generatePurchaseOrder(skuIds ? { sku_ids: skuIds } : undefined)
    if (res.data.code === 0) {
      router.push({
        path: '/stock/purchase/create',
        query: { replenish: '1', ...(skuIds ? { sku_ids: skuIds.join(',') } : {}) },
      })
    }
  } finally {
    replenishing.value = false
  }
}

// ---------- 库存流水 ----------
const recordType = ref<number | null>(null)
const recordDateRange = ref<[number, number] | null>(null)
const recordList = ref<InventoryLog[]>([])
const recordLoading = ref(false)
const recordPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const recordTypeOptions = [
  { label: '入库', value: 1 },
  { label: '出库', value: 2 },
  { label: '盘点调整', value: 3 },
  { label: '释放回补', value: 4 },
]

const recordColumns: DataTableColumns<InventoryLog> = [
  { title: 'ID', key: 'id', width: 80 },
  {
    title: '类型',
    key: 'type',
    width: 100,
    render: (row) => {
      const map: Record<number, { label: string; type: 'success' | 'error' | 'warning' | 'info' }> = {
        1: { label: '入库', type: 'success' },
        2: { label: '出库', type: 'error' },
        3: { label: '盘点', type: 'warning' },
        4: { label: '释放', type: 'info' },
      }
      const conf = map[row.type] ?? { label: String(row.type), type: 'info' as const }
      return h(NTag, { type: conf.type, size: 'small' }, { default: () => conf.label })
    },
  },
  {
    title: '变动数量',
    key: 'quantity',
    width: 100,
    render: (row) => (row.quantity > 0 ? `+${row.quantity}` : String(row.quantity)),
  },
  { title: '变动前', key: 'before_stock', width: 90 },
  { title: '变动后', key: 'after_stock', width: 90 },
  { title: '批次', key: 'batch_no', width: 120 },
  { title: '单据号', key: 'ref_no', width: 150 },
  { title: '关联订单', key: 'related_order_id', width: 100 },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
]

function formatDate(timestamp: number | null) {
  if (!timestamp) return ''
  const d = new Date(timestamp)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

async function loadRecords() {
  recordLoading.value = true
  try {
    const params: Record<string, unknown> = {
      page: recordPagination.value.page,
      page_size: recordPagination.value.pageSize,
      type: recordType.value || undefined,
    }
    if (recordDateRange.value) {
      params.start_date = formatDate(recordDateRange.value[0])
      params.end_date = formatDate(recordDateRange.value[1])
    }
    const res = await getInventoryLogList(params as never)
    if (res.data.code === 0) {
      recordList.value = res.data.data.list
      recordPagination.value.itemCount = res.data.data.total
    }
  } finally {
    recordLoading.value = false
  }
}

function onRecordPageChange(page: number) {
  recordPagination.value.page = page
  loadRecords()
}

/**
 * 库存流水查询：与库存列表同口径，筛选/日期变化后回到第 1 页再加载
 */
function onRecordSearch() {
  recordPagination.value.page = 1
  loadRecords()
}

// ---------- 批次库存 ----------
const batchKeyword = ref('')
const batchInStockOnly = ref(true)
const batchList = ref<StockBatch[]>([])
const batchLoading = ref(false)
const batchPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const batchColumns: DataTableColumns<StockBatch> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: 'SKU 编码', key: 'sku_code', width: 150 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 120, ellipsis: { tooltip: true } },
  { title: '批次号', key: 'batch_no', width: 140 },
  { title: '生产日期', key: 'production_date', width: 120 },
  {
    title: '过期日期',
    key: 'expire_date',
    width: 120,
    render(row) {
      if (!row.expire_date) return '-'
      const days = Math.ceil((new Date(row.expire_date).getTime() - Date.now()) / 86400000)
      if (days < 0) return h(NTag, { type: 'error', size: 'small' }, { default: () => row.expire_date })
      if (days <= 30) return h(NTag, { type: 'warning', size: 'small' }, { default: () => row.expire_date })
      return row.expire_date
    },
  },
  { title: '剩余数量', key: 'quantity', width: 100 },
  { title: '单位成本', key: 'unit_cost', width: 100 },
  {
    title: '来源',
    key: 'source_type',
    width: 90,
    render: (row) => {
      const map: Record<number, string> = { 1: '入库', 2: '回补', 3: '盘盈' }
      return map[row.source_type] ?? '-'
    },
  },
  { title: '来源单号', key: 'ref_no', width: 150 },
  { title: '入库时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
]

async function loadBatches() {
  batchLoading.value = true
  try {
    const res = await getStockBatchList({
      page: batchPagination.value.page,
      page_size: batchPagination.value.pageSize,
      keyword: batchKeyword.value || undefined,
      in_stock_only: batchInStockOnly.value,
    })
    if (res.data.code === 0) {
      batchList.value = res.data.data.list
      batchPagination.value.itemCount = res.data.data.total
    }
  } finally {
    batchLoading.value = false
  }
}

function onBatchPageChange(page: number) {
  batchPagination.value.page = page
  loadBatches()
}

/**
 * 批次库存查询：关键词/在库开关变化后回到第 1 页再加载
 */
function onBatchSearch() {
  batchPagination.value.page = 1
  loadBatches()
}

// ---------- 页签切换懒加载 ----------
function onTabChange(value: string) {
  if (value === 'warnings') loadWarnings()
  if (value === 'records') loadRecords()
  if (value === 'batches') loadBatches()
}

function goPurchase() {
  router.push('/stock/purchase/create')
}

loadStocks()

// 支持 ?tab=warnings/records/batches 深链（如仪表盘库存预警入口）：定位页签并触发懒加载
onMounted(() => {
  const tab = route.query.tab
  if (typeof tab === 'string' && ['warnings', 'records', 'batches'].includes(tab)) {
    activeTab.value = tab
    onTabChange(tab)
  }
})

// 类型仅用于本文件内引用占位（InventorySummaryItem 在报表页使用）
export type { InventorySummaryItem }
</script>
