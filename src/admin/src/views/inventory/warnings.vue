<template>
  <n-card title="库存预警">
    <n-space vertical>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { DataTableColumns } from 'naive-ui'
import { getStockWarnings } from '@/api/inventory'

/**
 * 库存预警页面
 * 展示低库存与临期商品预警信息
 */
const list = ref<Array<Record<string, unknown>>>([])
const loading = ref(false)

const columns: DataTableColumns<Record<string, unknown>> = [
  { title: 'SKU ID', key: 'sku_id' },
  { title: 'SKU 编码', key: 'sku_code' },
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'spec_name' },
  { title: '当前库存', key: 'stock' },
  { title: '安全库存', key: 'safety_stock' },
  { title: '预警类型', key: 'warning_type' },
  { title: '过期日期', key: 'expire_date' },
]

async function loadData() {
  loading.value = true
  const res = await getStockWarnings()
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data
  }
}

onMounted(loadData)
</script>
