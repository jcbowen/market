<template>
  <n-card title="盘点详情">
    <n-space v-if="detail" vertical>
      <n-descriptions bordered>
        <n-descriptions-item label="盘点单号">{{ detail.info.check_no }}</n-descriptions-item>
        <n-descriptions-item label="状态">{{ detail.info.status === 1 ? '待提交' : '已提交' }}</n-descriptions-item>
        <n-descriptions-item label="SKU 总数">{{ detail.info.total_items }}</n-descriptions-item>
        <n-descriptions-item label="差异数">{{ detail.info.diff_items }}</n-descriptions-item>
        <n-descriptions-item label="备注">{{ detail.info.remark }}</n-descriptions-item>
      </n-descriptions>
      <n-data-table :columns="columns" :data="detail.items" />
      <n-space v-if="detail.info.status === 1" vertical>
        <n-card title="添加盘点项" size="small">
          <n-space>
            <n-input-number v-model:value="addProductId" placeholder="商品 ID" style="width: 140px" @update:value="onAddProductChange" />
            <n-select v-model:value="addSkuId" :options="addSkuOptions" placeholder="选择 SKU" style="width: 220px" :disabled="!addSkuOptions.length" />
            <n-input-number v-model:value="addActualStock" placeholder="实盘库存" style="width: 140px" />
            <n-button type="primary" @click="addItem">添加</n-button>
          </n-space>
        </n-card>
        <n-button type="primary" :loading="submitting" @click="submit">提交盘点结果</n-button>
      </n-space>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { useRoute } from 'vue-router'
import { NButton, NInputNumber } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getInventoryCheckDetail, submitInventoryCheck, getSkuList } from '@/api/inventory'
import { useTabsStore } from '@/stores/tabs'
import type { Sku, StockCheckDetail, StockCheckItem } from '@/api/types'

/**
 * 盘点详情页面
 * 展示盘点单基本信息及明细，支持待提交状态下录入/编辑盘点项后提交
 */
const route = useRoute()
const tabsStore = useTabsStore()
const id = Number(route.params.id)
const detail = ref<StockCheckDetail | null>(null)
const submitting = ref(false)

const addProductId = ref<number | undefined>(undefined)
const addSkuId = ref<number | undefined>(undefined)
const addActualStock = ref<number | undefined>(undefined)
const addSkuOptions = ref<{ label: string; value: number }[]>([])
const addSkuMap = ref<Record<number, Sku>>({})

function createColumns(): DataTableColumns<StockCheckItem> {
  const baseColumns: DataTableColumns<StockCheckItem> = [
    { title: 'SKU ID', key: 'sku_id', width: 80 },
    { title: 'SKU 编码', key: 'sku_code', width: 140 },
    { title: '商品名称', key: 'product_name', ellipsis: { tooltip: true } },
    { title: '规格', key: 'spec_name', width: 140, ellipsis: { tooltip: true } },
    { title: '账面库存', key: 'system_stock', width: 100 },
    { title: '差异', key: 'diff_quantity', width: 100 },
  ]

  if (detail.value?.info.status === 1) {
    baseColumns.push({
      title: '实盘库存',
      key: 'actual_stock',
      width: 140,
      render(row, index) {
        return h(NInputNumber, {
          value: row.actual_stock,
          min: 0,
          style: 'width: 120px',
          onUpdateValue: (v: number | null) => {
            if (detail.value && v !== null && v !== undefined) {
              detail.value.items[index].actual_stock = v
              detail.value.items[index].diff_quantity = v - row.system_stock
            }
          },
        })
      },
    })
    baseColumns.push({
      title: '操作',
      key: 'actions',
      width: 80,
      render(_, index) {
        return h(NButton, { size: 'small', type: 'error', onClick: () => removeItem(index) }, { default: () => '删除' })
      },
    })
  } else {
    baseColumns.push({ title: '实盘库存', key: 'actual_stock', width: 100 })
  }

  return baseColumns
}

const columns = ref<DataTableColumns<StockCheckItem>>(createColumns())

async function loadDetail() {
  const res = await getInventoryCheckDetail(id)
  if (res.data.code === 0) {
    detail.value = res.data.data
    columns.value = createColumns()
    // 加载完成后用盘点单号区分标签页
    tabsStore.rename(route.fullPath, `盘点详情 · ${detail.value.info.check_no}`)
  }
}

async function onAddProductChange(productId: number | null) {
  addSkuId.value = undefined
  addSkuOptions.value = []
  addSkuMap.value = {}
  if (!productId) return

  try {
    const res = await getSkuList(productId)
    const list: Sku[] = res.data.data || []
    addSkuMap.value = Object.fromEntries(list.map((sku) => [sku.id, sku]))
    addSkuOptions.value = list.map((sku) => ({
      label: `${sku.sku_code || sku.id} ${sku.spec_name || ''}`.trim(),
      value: sku.id,
    }))
  } catch {
    // 加载失败时选项为空，由调用方处理
  }
}

function addItem() {
  if (!addSkuId.value || addActualStock.value === undefined) return
  if (!detail.value) return

  const sku = addSkuMap.value[addSkuId.value]
  const systemStock = sku?.stock || 0
  detail.value.items.push({
    id: 0,
    sku_id: addSkuId.value,
    sku_code: sku?.sku_code || '',
    product_name: '',
    spec_name: sku?.spec_name || '',
    system_stock: systemStock,
    actual_stock: addActualStock.value,
    diff_quantity: addActualStock.value - systemStock,
    created_at: '',
  })

  addProductId.value = undefined
  addSkuId.value = undefined
  addActualStock.value = undefined
  addSkuOptions.value = []
  addSkuMap.value = {}
}

function removeItem(index: number) {
  if (!detail.value) return
  detail.value.items.splice(index, 1)
}

async function submit() {
  if (!detail.value) return
  const items = detail.value.items.map((item) => ({
    sku_id: item.sku_id,
    actual_stock: item.actual_stock,
  }))
  submitting.value = true
  try {
    const res = await submitInventoryCheck(id, items)
    if (res.data.code === 0) {
      window.history.back()
    }
  } finally {
    submitting.value = false
  }
}

onMounted(loadDetail)
</script>
