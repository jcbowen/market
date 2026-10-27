<template>
  <n-card title="入库管理">
    <n-space style="margin-bottom: 12px">
      <n-date-picker v-model:value="dateRange" type="daterange" clearable />
      <n-button type="primary" :loading="loading" @click="loadData">查询</n-button>
      <n-button type="primary" @click="router.push('/stock/in/create')">直接入库</n-button>
    </n-space>
    <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote @update:page="onPageChange" />
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockIn' })
import { h, ref, onActivated } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRouter } from 'vue-router'
import { NButton, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getStockInOrderList } from '@/api/inventory'
import type { StockInOrder } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 入库管理页面
 * 入库单列表（直接入库/采购入库），详情入口与按采购单入库入口
 */
const router = useRouter()
const dateRange = ref<[number, number] | null>(null)
const list = ref<StockInOrder[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<StockInOrder> = [
  { title: '入库单号', key: 'in_no', width: 180 },
  {
    title: '类型',
    key: 'type',
    width: 110,
    render: (row) =>
      h(NTag, { type: row.type === 1 ? 'info' : 'default', size: 'small' }, {
        default: () => (row.type === 1 ? '采购入库' : '直接入库'),
      }),
  },
  { title: '供应商', key: 'supplier_name', width: 150, ellipsis: { tooltip: true } },
  {
    title: '关联采购单',
    key: 'purchase_no',
    width: 170,
    // 有采购单号时点击跳采购管理并按单号预筛（直接入库单无单号不可点）
    render: (row) =>
      row.purchase_no
        ? h(
            NButton,
            {
              size: 'small',
              text: true,
              type: 'primary',
              onClick: () => router.push({ path: '/stock/purchase', query: { keyword: row.purchase_no } }),
            },
            { default: () => row.purchase_no },
          )
        : '-',
  },
  {
    title: '入库金额',
    key: 'total_amount',
    width: 120,
    render: (row) => `¥${formatAmount(row.total_amount)}`,
  },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '入库时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 90,
    render(row) {
      return h(NButton, { size: 'small', type: 'primary', quaternary: true, onClick: () => router.push(`/stock/in/${row.id}`) }, { default: () => '详情' })
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
    }
    if (dateRange.value) {
      params.start_date = formatDate(dateRange.value[0])
      params.end_date = formatDate(dateRange.value[1])
    }
    const res = await getStockInOrderList(params as never)
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
// onActivated 重新拉取，保证入库单新增/操作后列表展示最新数据
onActivated(loadData)
</script>
