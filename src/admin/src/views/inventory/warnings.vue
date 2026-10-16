<template>
  <n-card title="库存预警">
    <n-space vertical>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockWarnings' })
import { ref, onMounted, h } from 'vue'
import type { DataTableColumns } from 'naive-ui'
import { NTag } from 'naive-ui'
import { getStockWarnings } from '@/api/inventory'

/**
 * 库存预警页面
 * 展示低库存与临期商品预警信息
 */
const list = ref<Array<Record<string, unknown>>>([])
const loading = ref(false)

/**
 * 将预警类型编码转换为可读文本
 * @param type 预警类型：1 低库存，2 临期预警
 * @returns 类型标签文本
 */
function warningTypeText(type: number) {
  return type === 2 ? '临期预警' : '低库存'
}

/**
 * 根据预警类型返回对应的标签类型
 * @param type 预警类型
 * @returns Naive UI 标签类型
 */
function warningTagType(type: number) {
  return type === 2 ? 'warning' : 'error'
}

const columns: DataTableColumns<Record<string, unknown>> = [
  { title: 'SKU ID', key: 'sku_id' },
  { title: 'SKU 编码', key: 'sku_code' },
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'spec_name' },
  { title: '当前库存', key: 'stock' },
  { title: '安全库存', key: 'safety_stock' },
  {
    title: '预警类型',
    key: 'warning_type',
    render(row) {
      const type = Number(row.warning_type)
      return h(NTag, { type: warningTagType(type) }, { default: () => warningTypeText(type) })
    },
  },
  { title: '过期日期', key: 'expire_date' },
]

async function loadData() {
  loading.value = true
  try {
    const res = await getStockWarnings()
    if (res.data.code === 0) {
      list.value = res.data.data
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
  }
}

onMounted(loadData)
</script>
