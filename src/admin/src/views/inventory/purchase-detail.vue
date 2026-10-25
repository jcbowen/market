<template>
  <n-card :title="`采购单详情 · ${detail?.info.purchase_no ?? ''}`">
    <template v-if="detail">
      <n-descriptions bordered :column="3">
        <n-descriptions-item label="采购单号">{{ detail.info.purchase_no }}</n-descriptions-item>
        <n-descriptions-item label="供应商">{{ detail.info.supplier_name || '-' }}</n-descriptions-item>
        <n-descriptions-item label="状态">
          <n-tag :type="statusMeta[detail.info.status]?.type ?? 'default'" size="small">
            {{ statusMeta[detail.info.status]?.label ?? detail.info.status }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="采购金额">¥{{ formatAmount(detail.info.total_amount) }}</n-descriptions-item>
        <n-descriptions-item label="创建时间">{{ formatDateTime(detail.info.created_at) }}</n-descriptions-item>
        <n-descriptions-item label="备注">{{ detail.info.remark || '-' }}</n-descriptions-item>
      </n-descriptions>

      <n-data-table :columns="columns" :data="detail.items" style="margin-top: 16px" />

      <n-space style="margin-top: 16px">
        <template v-if="detail.info.status === 1">
          <n-button type="primary" @click="router.push(`/stock/purchase/edit/${detail.info.id}`)">编辑</n-button>
          <n-button type="info" @click="onSubmit">提交</n-button>
          <n-button type="error" @click="onCancel">取消</n-button>
        </template>
        <template v-else-if="detail.info.status === 2 || detail.info.status === 3">
          <n-button type="success" @click="router.push(`/stock/in/create?purchase_id=${detail.info.id}`)">去入库</n-button>
          <n-button type="error" @click="onCancel">取消</n-button>
        </template>
        <n-button @click="router.back()">返回</n-button>
      </n-space>
    </template>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'PurchaseDetail' })
import { onMounted, ref, h } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRoute, useRouter } from 'vue-router'
import { useDialog, useMessage } from 'naive-ui'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getPurchaseOrderDetail, submitPurchaseOrder, cancelPurchaseOrder } from '@/api/inventory'
import type { PurchaseOrderDetail, PurchaseOrderItemRow } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { useTabsStore } from '@/stores/tabs'

/**
 * 采购单详情页
 * 展示采购单信息与明细，按状态提供提交/取消/去入库操作
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const tabsStore = useTabsStore()
const id = Number(route.params.id)
const detail = ref<PurchaseOrderDetail | null>(null)

const statusMeta: Record<number, { label: string; type: 'default' | 'info' | 'warning' | 'success' | 'error' }> = {
  1: { label: '草稿', type: 'default' },
  2: { label: '已下单', type: 'info' },
  3: { label: '部分入库', type: 'warning' },
  4: { label: '已完结', type: 'success' },
  5: { label: '已取消', type: 'error' },
}

const columns: DataTableColumns<PurchaseOrderItemRow> = [
  { title: 'SKU 编码', key: 'sku_code', width: 160 },
  { title: '商品', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 140, ellipsis: { tooltip: true } },
  {
    title: '采购数量',
    key: 'quantity',
    width: 110,
    render: (row) =>
      h('span', {}, { default: () => `${row.quantity} / 已入 ${row.received_quantity}` }),
  },
  {
    title: '采购单价',
    key: 'purchase_price',
    width: 110,
    render: (row) => `¥${formatAmount(row.purchase_price)}`,
  },
  {
    title: '小计',
    key: 'subtotal',
    width: 110,
    render: (row) => `¥${formatAmount(row.quantity * row.purchase_price)}`,
  },
]

async function loadDetail() {
  const res = await getPurchaseOrderDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
    tabsStore.rename(route.fullPath, `采购单 · ${detail.value.info.purchase_no}`)
  }
}

function onSubmit() {
  dialog.warning({
    title: '确认提交',
    content: '提交后采购单将变为「已下单」状态，确认提交？',
    positiveText: '确认提交',
    negativeText: '再想想',
    onPositiveClick: async () => {
      const res = await submitPurchaseOrder(id)
      if (res.data.code === 0) {
        message.success('已提交')
        loadDetail()
      }
    },
  })
}

function onCancel() {
  dialog.warning({
    title: '确认取消',
    content: '取消后采购单不可恢复，确认取消？',
    positiveText: '确认取消',
    negativeText: '再想想',
    onPositiveClick: async () => {
      const res = await cancelPurchaseOrder(id)
      if (res.data.code === 0) {
        message.success('已取消')
        loadDetail()
      }
    },
  })
}

onMounted(loadDetail)
</script>
