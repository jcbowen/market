<template>
  <n-card :title="`入库单详情 · ${detail?.info.in_no ?? ''}`">
    <template v-if="detail">
      <n-descriptions bordered :column="3">
        <n-descriptions-item label="入库单号">{{ detail.info.in_no }}</n-descriptions-item>
        <n-descriptions-item label="类型">{{ detail.info.type === 1 ? '采购入库' : '直接入库' }}</n-descriptions-item>
        <n-descriptions-item label="供应商">{{ detail.info.supplier_name || '-' }}</n-descriptions-item>
        <n-descriptions-item label="关联采购单">
          <!-- 有采购单号时点击跳采购管理并按单号预筛 -->
          <n-button
            v-if="detail.info.purchase_no"
            size="tiny"
            text
            type="primary"
            @click="router.push({ path: '/stock/purchase', query: { keyword: detail.info.purchase_no } })"
          >
            {{ detail.info.purchase_no }}
          </n-button>
          <template v-else>-</template>
        </n-descriptions-item>
        <n-descriptions-item label="入库金额">¥{{ formatAmount(detail.info.total_amount) }}</n-descriptions-item>
        <n-descriptions-item label="入库时间">{{ formatDateTime(detail.info.created_at) }}</n-descriptions-item>
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
defineOptions({ name: 'StockInDetail' })
import { onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRoute, useRouter } from 'vue-router'
import type { DataTableColumns } from 'naive-ui'
import { getStockInOrderDetail } from '@/api/inventory'
import type { StockInOrderDetail, StockInOrderItemRow } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { useTabsStore } from '@/stores/tabs'

/**
 * 入库单详情页
 * 展示入库单信息、批次与成本明细
 */
const route = useRoute()
const router = useRouter()
const tabsStore = useTabsStore()
const id = Number(route.params.id)
const detail = ref<StockInOrderDetail | null>(null)

const columns: DataTableColumns<StockInOrderItemRow> = [
  { title: 'SKU 编码', key: 'sku_code', width: 160 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 130, ellipsis: { tooltip: true } },
  { title: '入库数量', key: 'quantity', width: 100 },
  {
    title: '单位成本',
    key: 'unit_cost',
    width: 110,
    render: (row) => `¥${formatAmount(row.unit_cost)}`,
  },
  { title: '批次号', key: 'batch_no', width: 150 },
  { title: '生产日期', key: 'production_date', width: 120, render: (row) => row.production_date || '-' },
  { title: '过期日期', key: 'expire_date', width: 120, render: (row) => row.expire_date || '-' },
]

async function loadDetail() {
  const res = await getStockInOrderDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
    tabsStore.rename(route.fullPath, `入库单 · ${detail.value.info.in_no}`)
  }
}

onMounted(loadDetail)
</script>
