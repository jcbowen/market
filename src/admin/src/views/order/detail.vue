<template>
  <n-card title="订单详情">
    <n-alert v-if="!detail" type="info">加载中...</n-alert>
    <n-space v-else vertical>
      <n-descriptions bordered>
        <n-descriptions-item label="订单号">{{ detail.order_no }}</n-descriptions-item>
        <n-descriptions-item label="用户">{{ detail.user_name }}</n-descriptions-item>
        <n-descriptions-item label="状态">{{ detail.status_text }}</n-descriptions-item>
        <n-descriptions-item label="订单金额">{{ detail.total_amount }}</n-descriptions-item>
        <n-descriptions-item label="实付金额">{{ detail.pay_amount }}</n-descriptions-item>
        <n-descriptions-item label="创建时间">{{ detail.created_at }}</n-descriptions-item>
      </n-descriptions>
      <n-data-table :columns="itemColumns" :data="detail.items" />
      <n-space>
        <n-button v-if="detail.status === 2" @click="ship">发货</n-button>
        <n-button v-if="detail.status === 4" type="primary" @click="complete">完成</n-button>
      </n-space>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { DataTableColumns } from 'naive-ui'
import { getOrderDetail, shipOrder, completeOrder } from '@/api/order'
import type { OrderDetail, OrderItem } from '@/api/types'

/**
 * 订单详情页面
 * 展示订单基本信息、商品明细并支持发货/完成操作
 */
const route = useRoute()
const router = useRouter()
const detail = ref<OrderDetail | null>(null)

const itemColumns: DataTableColumns<OrderItem> = [
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'sku_name' },
  { title: '单价', key: 'price' },
  { title: '数量', key: 'quantity' },
  { title: '小计', key: 'total_amount' },
]

async function loadDetail() {
  const id = Number(route.params.id)
  const res = await getOrderDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
  }
}

async function ship() {
  if (!detail.value) return
  const res = await shipOrder(detail.value.id, { remark: '后台发货' })
  if (res.data.code === 0) loadDetail()
}

async function complete() {
  if (!detail.value) return
  const res = await completeOrder(detail.value.id)
  if (res.data.code === 0) loadDetail()
}

onMounted(loadDetail)
</script>
