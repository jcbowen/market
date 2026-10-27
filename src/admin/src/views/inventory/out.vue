<template>
  <n-card title="出库管理">
    <n-space style="margin-bottom: 12px">
      <n-select v-model:value="outType" :options="typeOptions" placeholder="出库类型" clearable style="width: 140px" />
      <n-date-picker v-model:value="dateRange" type="daterange" clearable />
      <n-button type="primary" :loading="loading" @click="loadData">查询</n-button>
      <n-button type="primary" @click="router.push('/stock/out/create')">新建出库单</n-button>
    </n-space>
    <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote @update:page="onPageChange" />
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockOut' })
import { h, ref, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getStockOutOrderList } from '@/api/inventory'
import type { StockOutOrder } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 出库管理页面
 * 出库单列表（报损/领用/调整），销售出库由订单系统自动完成不在此列
 */
const router = useRouter()
const outType = ref<number | null>(null)
const dateRange = ref<[number, number] | null>(null)
const list = ref<StockOutOrder[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const typeOptions = [
  { label: '报损', value: 1 },
  { label: '领用', value: 2 },
  { label: '调整', value: 3 },
]

const typeMeta: Record<number, { label: string; type: 'error' | 'info' | 'warning' }> = {
  1: { label: '报损', type: 'error' },
  2: { label: '领用', type: 'info' },
  3: { label: '调整', type: 'warning' },
}

const columns: DataTableColumns<StockOutOrder> = [
  { title: '出库单号', key: 'out_no', width: 180 },
  {
    title: '类型',
    key: 'type',
    width: 90,
    render: (row) => {
      const meta = typeMeta[row.type] ?? { label: String(row.type), type: 'default' as const }
      return h(NTag, { type: meta.type, size: 'small' }, { default: () => meta.label })
    },
  },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '出库时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 90,
    render(row) {
      return h(NButton, { size: 'small', type: 'primary', quaternary: true, onClick: () => router.push(`/stock/out/${row.id}`) }, { default: () => '详情' })
    },
  },
]

function formatDate(timestamp: number | null) {
  if (!timestamp) return ''
  const d = new Date(timestamp)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

async function loadData() {
  loading.value = true
  try {
    const params: Record<string, unknown> = {
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      type: outType.value ?? undefined,
    }
    if (dateRange.value) {
      params.start_date = formatDate(dateRange.value[0])
      params.end_date = formatDate(dateRange.value[1])
    }
    const res = await getStockOutOrderList(params as never)
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    loading.value = false
  }
}

function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

// keep-alive 缓存下从内页（新建/详情）返回时 onMounted 不会触发，
// onActivated 重新拉取，保证出库单新增/操作后列表展示最新数据
onActivated(loadData)
</script>
