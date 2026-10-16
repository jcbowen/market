<template>
  <n-card title="库存流水">
    <n-space vertical>
      <n-space>
        <n-select v-model:value="type" :options="typeOptions" placeholder="类型" clearable style="width: 140px" />
        <n-date-picker v-model:value="dateRange" type="daterange" clearable />
        <n-button type="primary" @click="loadData">查询</n-button>
        <n-button @click="showInModal = true">入库</n-button>
        <n-button @click="showOutModal = true">出库</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showInModal" title="商品入库" preset="card" style="width: 500px; max-width: 90vw">
    <n-space vertical>
      <n-input-number v-model:value="inForm.sku_id" placeholder="SKU ID" />
      <n-input-number v-model:value="inForm.quantity" placeholder="数量" />
      <n-input v-model:value="inForm.batch_no" placeholder="批次号" />
      <n-date-picker v-model:value="inForm.production_date" type="date" placeholder="生产日期" />
      <n-date-picker v-model:value="inForm.expire_date" type="date" placeholder="过期日期" />
      <n-input v-model:value="inForm.remark" placeholder="备注" />
      <n-button type="primary" @click="submitIn">提交入库</n-button>
    </n-space>
  </n-modal>

  <n-modal v-model:show="showOutModal" title="库存出库" preset="card" style="width: 500px; max-width: 90vw">
    <n-space vertical>
      <n-select v-model:value="outForm.type" :options="outTypeOptions" />
      <n-input-number v-model:value="outForm.sku_id" placeholder="SKU ID" />
      <n-input-number v-model:value="outForm.quantity" placeholder="数量" />
      <n-input v-model:value="outForm.remark" placeholder="备注" />
      <n-button type="primary" @click="submitOut">提交出库</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockRecords' })
import { ref, onMounted } from 'vue'
import type { DataTableColumns } from 'naive-ui'
import { getInventoryLogList, stockIn, stockOut } from '@/api/inventory'
import type { InventoryLog } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 库存流水页面
 * 展示入库/出库/盘点调整等库存变动记录
 */
const type = ref<number | null>(null)
const dateRange = ref<[number, number] | null>(null)
const list = ref<InventoryLog[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const showInModal = ref(false)
const showOutModal = ref(false)

const typeOptions = [
  { label: '采购入库', value: 1 },
  { label: '退货入库', value: 2 },
  { label: '销售出库', value: 3 },
  { label: '报损出库', value: 4 },
  { label: '盘点调整', value: 5 },
]
const outTypeOptions = [
  { label: '销售出库', value: 3 },
  { label: '报损出库', value: 4 },
]

const inForm = ref({ sku_id: undefined as number | undefined, quantity: undefined as number | undefined, batch_no: '', production_date: null as number | null, expire_date: null as number | null, remark: '' })
const outForm = ref({ type: 3, sku_id: undefined as number | undefined, quantity: undefined as number | undefined, remark: '' })

const columns: DataTableColumns<InventoryLog> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '类型', key: 'type_text' },
  { title: '变动数量', key: 'quantity' },
  { title: '变动前', key: 'before_stock' },
  { title: '变动后', key: 'after_stock' },
  { title: '备注', key: 'remark' },
  { title: '时间', key: 'created_at', render: (row) => formatDateTime(row.created_at) },
]

function formatDate(timestamp: number | null) {
  if (!timestamp) return ''
  const d = new Date(timestamp)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

async function loadData() {
  loading.value = true
  try {
    const params: Record<string, unknown> = { page: pagination.value.page, page_size: pagination.value.pageSize, type: type.value || undefined }
    if (dateRange.value) {
      params.start_date = formatDate(dateRange.value[0])
      params.end_date = formatDate(dateRange.value[1])
    }
    const res = await getInventoryLogList(params as any)
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
  }
}

async function submitIn() {
  const data = { ...inForm.value }
  data.production_date = formatDate(inForm.value.production_date) as any
  data.expire_date = formatDate(inForm.value.expire_date) as any
  const res = await stockIn(data as any)
  if (res.data.code === 0) {
    showInModal.value = false
    loadData()
  }
}

async function submitOut() {
  const res = await stockOut(outForm.value as any)
  if (res.data.code === 0) {
    showOutModal.value = false
    loadData()
  }
}

onMounted(loadData)
</script>
