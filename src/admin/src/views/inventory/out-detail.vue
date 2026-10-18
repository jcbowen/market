<template>
  <n-card :title="`出库单详情 · ${detail?.info.out_no ?? ''}`">
    <template v-if="detail">
      <n-descriptions bordered :column="3">
        <n-descriptions-item label="出库单号">{{ detail.info.out_no }}</n-descriptions-item>
        <n-descriptions-item label="类型">
          <n-tag :type="typeMeta[detail.info.type]?.type ?? 'default'" size="small">
            {{ typeMeta[detail.info.type]?.label ?? detail.info.type }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="出库时间">{{ formatDateTime(detail.info.created_at) }}</n-descriptions-item>
        <n-descriptions-item label="备注" :span="3">{{ detail.info.remark || '-' }}</n-descriptions-item>
      </n-descriptions>
      <n-data-table :columns="columns" :data="detail.items" style="margin-top: 16px" />
      <n-space style="margin-top: 16px">
        <n-button @click="router.back()">返回</n-button>
      </n-space>
    </template>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockOutDetail' })
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getStockOutOrderDetail } from '@/api/inventory'
import type { StockOutOrderDetail, StockOutOrderItemRow } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { useTabsStore } from '@/stores/tabs'

/**
 * 出库单详情页
 * 展示出库单信息与明细（各 SKU 出库数量）
 */
const route = useRoute()
const router = useRouter()
const tabsStore = useTabsStore()
const id = Number(route.params.id)
const detail = ref<StockOutOrderDetail | null>(null)

const typeMeta: Record<number, { label: string; type: 'error' | 'info' | 'warning' | 'default' }> = {
  1: { label: '报损', type: 'error' },
  2: { label: '领用', type: 'info' },
  3: { label: '调整', type: 'warning' },
}

const columns: DataTableColumns<StockOutOrderItemRow> = [
  { title: 'SKU 编码', key: 'sku_code', width: 160 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 130, ellipsis: { tooltip: true } },
  { title: '出库数量', key: 'quantity', width: 110 },
]

async function loadDetail() {
  const res = await getStockOutOrderDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
    tabsStore.rename(route.fullPath, `出库单 · ${detail.value.info.out_no}`)
  }
}

onMounted(loadDetail)
</script>
