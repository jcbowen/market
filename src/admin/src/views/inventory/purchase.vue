<template>
  <n-card title="补货建议">
    <n-space vertical>
      <n-space>
        <n-button type="primary" :loading="loading" @click="generate">生成补货建议</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
      <n-empty v-if="!loading && list.length === 0" description="暂无补货建议，点击上方按钮生成" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockPurchase' })
import { ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { generatePurchaseOrder } from '@/api/inventory'
import type { PurchaseOrderItem } from '@/api/types'

/**
 * 补货建议页面
 * 调用后端接口生成并展示需要补货的 SKU 建议清单
 */
const message = useMessage()
const loading = ref(false)
const list = ref<PurchaseOrderItem[]>([])

const columns: DataTableColumns<PurchaseOrderItem> = [
  { title: 'SKU ID', key: 'sku_id', width: 100 },
  { title: 'SKU 编码', key: 'sku_code' },
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'spec_name' },
  { title: '当前库存', key: 'current_stock' },
  { title: '安全库存', key: 'safety_stock' },
  { title: '建议补货量', key: 'suggest_quantity' },
]

/**
 * 生成补货建议
 * 调用 generatePurchaseOrder 接口并刷新表格数据
 */
async function generate() {
  loading.value = true
  try {
    const res = await generatePurchaseOrder()
    if (res.data.code === 0) {
      list.value = res.data.data
      if (list.value.length === 0) {
        message.info('当前无需补货')
      }
    }
  } finally {
    loading.value = false
  }
}
</script>
