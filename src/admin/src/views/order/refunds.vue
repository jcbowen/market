<template>
  <n-card title="退款管理">
    <n-space vertical>
      <n-space>
        <n-select v-model:value="status" :options="statusOptions" placeholder="退款状态" clearable style="width: 140px" />
        <n-button type="primary" @click="loadData">查询</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NInput } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRefundList, approveRefund, rejectRefund } from '@/api/order'

/**
 * 退款管理页面
 * 展示退款申请列表并支持同意/拒绝操作
 */
const status = ref<number | null>(null)
const list = ref<Array<Record<string, unknown>>>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const rejectReason = ref('')

const statusOptions = [
  { label: '待审核', value: 1 },
  { label: '已同意', value: 2 },
  { label: '已拒绝', value: 3 },
  { label: '退款中', value: 4 },
  { label: '已退款', value: 5 },
]

const columns: DataTableColumns<Record<string, unknown>> = [
  { title: '退款单号', key: 'refund_no' },
  { title: '订单 ID', key: 'order_id' },
  { title: '退款金额', key: 'amount' },
  { title: '原因', key: 'reason' },
  { title: '状态', key: 'status' },
  { title: '申请时间', key: 'created_at' },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      if (row.status !== 1) return null
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', type: 'primary', onClick: () => approve(row) }, { default: () => '同意' }),
          h(NInput, { size: 'small', value: rejectReason.value, onUpdateValue: (v: string) => { rejectReason.value = v }, placeholder: '拒绝原因', style: 'width: 140px' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => reject(row) }, { default: () => '拒绝' }),
        ],
      })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getRefundList({ page: pagination.value.page, page_size: pagination.value.pageSize, status: status.value || undefined })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

async function approve(row: Record<string, unknown>) {
  const res = await approveRefund(row.id as number)
  if (res.data.code === 0) loadData()
}

async function reject(row: Record<string, unknown>) {
  const res = await rejectRefund(row.id as number, rejectReason.value)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
