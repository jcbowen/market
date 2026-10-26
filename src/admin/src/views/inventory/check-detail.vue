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
            <SkuPicker v-model="addSkuId" @change="onAddSkuChange" />
            <n-input-number v-model:value="addActualStock" placeholder="实盘库存" :min="0" :precision="0" style="width: 140px" />
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
import { NButton, NInputNumber, createDiscreteApi } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getInventoryCheckDetail, submitInventoryCheck } from '@/api/inventory'
import SkuPicker from './components/SkuPicker.vue'
import { useTabsStore } from '@/stores/tabs'
import type { Sku, StockCheckDetail, StockCheckItem } from '@/api/types'

/**
 * 盘点详情页面
 * 展示盘点单基本信息及明细，支持待提交状态下录入/编辑盘点项后提交
 */
const route = useRoute()
const tabsStore = useTabsStore()
const { message, dialog } = createDiscreteApi(['message', 'dialog'])
const id = Number(route.params.id)
const detail = ref<StockCheckDetail | null>(null)
const submitting = ref(false)

const addSkuId = ref<number | undefined>(undefined)
const addActualStock = ref<number | undefined>(undefined)
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

async function onAddSkuChange(sku: Sku | null) {
  if (sku) {
    addSkuMap.value[sku.id] = sku
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
    product_name: sku?.product_name || '',
    spec_name: sku?.spec_name || '',
    system_stock: systemStock,
    actual_stock: addActualStock.value,
    diff_quantity: addActualStock.value - systemStock,
    created_at: '',
  })

  addSkuId.value = undefined
  addActualStock.value = undefined
}

function removeItem(index: number) {
  if (!detail.value) return
  detail.value.items.splice(index, 1)
}

/**
 * 提交盘点结果：先拦空明细，再二次确认
 * 盘点按差异直接改写库存（并影响移动加权成本），提交后不可撤销，
 * 误点一次就可能把整单账面库存清零，故必须确认；空明细服务端同样会拒，前置提示更省事
 */
function submit() {
  if (!detail.value) return
  if (detail.value.items.length === 0) {
    message.warning('请先添加盘点项')
    return
  }
  dialog.warning({
    title: '确认提交盘点结果',
    content: `本次盘点共 ${detail.value.items.length} 个 SKU，提交后系统库存将按差异立即调整且不可撤销，确认提交？`,
    positiveText: '确认提交',
    negativeText: '再看看',
    onPositiveClick: () => {
      void doSubmit()
    },
  })
}

async function doSubmit() {
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
