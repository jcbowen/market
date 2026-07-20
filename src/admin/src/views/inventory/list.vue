<template>
  <n-card title="库存列表">
    <n-space vertical>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" @update:page="onPageChange" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { DataTableColumns } from 'naive-ui'
import { getSkuStockList } from '@/api/inventory'
import type { Sku } from '@/api/types'

/**
 * 库存列表页面
 * 展示当前社区所有 SKU 的实时库存
 */
const list = ref<Sku[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<Sku> = [
  { title: 'SKU ID', key: 'id', width: 100 },
  { title: 'SKU 编码', key: 'sku_code' },
  { title: '售价', key: 'price' },
  { title: '库存', key: 'stock' },
]

async function loadData() {
  loading.value = true
  const res = await getSkuStockList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

onMounted(loadData)
</script>
