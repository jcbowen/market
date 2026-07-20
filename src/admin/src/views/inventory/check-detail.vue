<template>
  <n-card title="盘点详情">
    <n-space v-if="detail" vertical>
      <n-descriptions bordered>
        <n-descriptions-item label="盘点单号">{{ detail.check_no }}</n-descriptions-item>
        <n-descriptions-item label="状态">{{ detail.status === 1 ? '待提交' : '已提交' }}</n-descriptions-item>
        <n-descriptions-item label="备注">{{ detail.remark }}</n-descriptions-item>
      </n-descriptions>
      <n-data-table :columns="columns" :data="items" />
      <n-button v-if="detail.status === 1" type="primary" @click="submit">提交盘点结果</n-button>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import type { DataTableColumns } from 'naive-ui'
import { getInventoryCheckDetail, submitInventoryCheck } from '@/api/inventory'
import type { InventoryCheck } from '@/api/types'

/**
 * 盘点详情页面
 * 展示盘点单基本信息，并支持录入盘点项后提交
 */
const route = useRoute()
const id = Number(route.params.id)
const detail = ref<InventoryCheck | null>(null)
const items = ref<Array<{ sku_id: number; actual_stock: number }>>([])

const columns: DataTableColumns<{ sku_id: number; actual_stock: number }> = [
  { title: 'SKU ID', key: 'sku_id' },
  { title: '实盘库存', key: 'actual_stock' },
]

async function loadDetail() {
  const res = await getInventoryCheckDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
  }
}

async function submit() {
  const res = await submitInventoryCheck(id, items.value)
  if (res.data.code === 0) {
    window.history.back()
  }
}

onMounted(loadDetail)
</script>
