<template>
  <n-card title="订单列表">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="orderNo" placeholder="订单号" clearable />
        <n-select v-model:value="status" :options="statusOptions" placeholder="订单状态" clearable style="width: 140px" />
        <n-button type="primary" @click="loadData">查询</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'OrderList' })
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList } from '@/api/order'
import type { Order } from '@/api/types'

/**
 * 订单列表页面
 * 展示订单分页数据并支持查看详情
 */
const router = useRouter()
const orderNo = ref('')
const status = ref<number | null>(null)
const list = ref<Order[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const statusOptions = [
  { label: '待支付', value: 1 },
  { label: '待发货', value: 2 },
  { label: '已发货', value: 4 },
  { label: '已完成', value: 5 },
  { label: '已取消', value: 6 },
]

const columns: DataTableColumns<Order> = [
  { title: '订单号', key: 'order_no' },
  { title: '用户', key: 'user_name' },
  { title: '订单金额', key: 'total_amount' },
  { title: '实付金额', key: 'pay_amount' },
  { title: '状态', key: 'status_text' },
  { title: '创建时间', key: 'created_at' },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NButton, { size: 'small', onClick: () => router.push(`/orders/${row.id}`) }, { default: () => '详情' })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getOrderList({
    page: pagination.value.page,
    page_size: pagination.value.pageSize,
    order_no: orderNo.value,
    status: status.value || undefined,
  })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

onMounted(loadData)
</script>
