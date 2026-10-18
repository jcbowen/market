<template>
  <n-card :title="purchaseId ? '按采购单入库' : '直接入库'">
    <n-space vertical style="width: 100%">
      <n-alert v-if="purchaseId && purchase" type="info" :bordered="false">
        采购单 {{ purchase.info.purchase_no }}（{{ purchase.info.supplier_name }}）· 状态 {{ statusText(purchase.info.status) }}。
        各行数量默认为待入库量，可按实际到货修改；批次号/日期留空时自动生成默认批次。
      </n-alert>

      <n-form label-placement="left" label-width="100">
        <n-form-item v-if="!purchaseId" label="供应商">
          <n-select
            v-model:value="form.supplier_id"
            :options="supplierOptions"
            :loading="supplierLoading"
            filterable
            clearable
            placeholder="请选择供应商（选填）"
            style="width: 360px"
          />
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="form.remark" type="textarea" placeholder="备注（选填）" :rows="2" maxlength="200" style="width: 480px" />
        </n-form-item>
        <n-form-item label="入库明细">
          <n-space vertical style="width: 100%">
            <n-table :bordered="false" :single-line="true" size="small" style="width: 980px">
              <thead>
                <tr>
                  <th v-if="!purchaseId" style="width: 360px">商品 / SKU</th>
                  <th v-else style="width: 280px">商品 / SKU</th>
                  <th style="width: 120px">入库数量</th>
                  <th style="width: 140px">单位成本（元）</th>
                  <th style="width: 160px">批次号</th>
                  <th style="width: 150px">生产日期</th>
                  <th style="width: 150px">过期日期</th>
                  <th v-if="!purchaseId" style="width: 80px">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(item, index) in rows" :key="index">
                  <td>
                    <template v-if="purchaseId">
                      <n-text>{{ item.sku_code }} · {{ item.product_name }}</n-text>
                      <n-text v-if="item.spec_name" depth="3"> / {{ item.spec_name }}</n-text>
                    </template>
                    <n-space v-else vertical size="small">
                      <SkuPicker v-model="item.sku_id" @change="(sku) => onSkuChange(index, sku)" />
                      <n-text v-if="skuInfoOf(item.sku_id)" depth="3" style="font-size: 12px">
                        当前库存 {{ skuInfoOf(item.sku_id)!.stock }} · 成本 ¥{{ Number(skuInfoOf(item.sku_id)!.cost_price || 0).toFixed(2) }}
                      </n-text>
                    </n-space>
                  </td>
                  <td>
                    <n-input-number v-model:value="item.quantity" :min="1" :precision="0" placeholder="数量" style="width: 110px" />
                  </td>
                  <td>
                    <n-input-number v-model:value="item.unit_cost" :min="0" :precision="2" placeholder="0=用当前成本" style="width: 120px" />
                  </td>
                  <td>
                    <n-input v-model:value="item.batch_no" placeholder="自动生成" maxlength="50" style="width: 140px" />
                  </td>
                  <td>
                    <n-date-picker v-model:value="item.production_date" type="date" clearable placeholder="选填" style="width: 140px" />
                  </td>
                  <td>
                    <n-date-picker v-model:value="item.expire_date" type="date" clearable placeholder="选填" style="width: 140px" />
                  </td>
                  <td v-if="!purchaseId">
                    <n-button size="small" type="error" quaternary @click="removeItem(index)">删除</n-button>
                  </td>
                </tr>
              </tbody>
            </n-table>
            <n-space>
              <n-button v-if="!purchaseId" size="small" dashed @click="addItem">+ 添加一行</n-button>
              <n-button v-if="!purchaseId" size="small" dashed @click="batchPickerVisible = true">批量选货</n-button>
              <BatchPickerModal v-if="!purchaseId" v-model:show="batchPickerVisible" @confirm="addBatchRows" />
            </n-space>
          </n-space>
        </n-form-item>
        <n-form-item>
          <n-space>
            <n-button type="primary" :loading="submitting" @click="submit">确认入库</n-button>
            <n-button @click="router.back()">返回</n-button>
          </n-space>
        </n-form-item>
      </n-form>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockInForm' })
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import SkuPicker from './components/SkuPicker.vue'
import BatchPickerModal from './components/BatchPickerModal.vue'
import { getSupplierList, getPurchaseOrderDetail, createStockInOrder } from '@/api/inventory'
import type { PurchaseOrderDetail, Sku, Supplier } from '@/api/types'

/**
 * 新建入库单页
 * 模式一：按采购单入库（query purchase_id）—— 预置采购单明细行
 * 模式二：直接入库 —— 自行选择 SKU / 成本 / 批次
 * 入库即生效：生成批次、按移动加权平均回写成本、联动采购单收货状态
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()

const purchaseId = computed(() => Number(route.query.purchase_id) || 0)
const purchase = ref<PurchaseOrderDetail | null>(null)

interface InRow {
  sku_id?: number
  quantity: number
  unit_cost: number
  batch_no: string
  production_date: number | null
  expire_date: number | null
  // 按采购单入库时展示用
  sku_code?: string
  product_name?: string
  spec_name?: string
}

const supplierOptions = ref<{ label: string; value: number }[]>([])
const supplierLoading = ref(false)
const submitting = ref(false)
const batchPickerVisible = ref(false)
// 已选 SKU 信息（含库存/成本），用于行内展示
const skuInfoMap = ref<Record<number, Sku>>({})
const form = ref({ supplier_id: null as number | null, remark: '' })
const rows = ref<InRow[]>([])

function statusText(status: number) {
  const map: Record<number, string> = { 1: '草稿', 2: '已下单', 3: '部分入库', 4: '已完结', 5: '已取消' }
  return map[status] ?? String(status)
}

function addItem() {
  rows.value.push({ sku_id: undefined, quantity: 1, unit_cost: 0, batch_no: '', production_date: null, expire_date: null })
}

function removeItem(index: number) {
  rows.value.splice(index, 1)
}

function skuInfoOf(skuId: number | undefined) {
  return skuId ? skuInfoMap.value[skuId] : undefined
}

function onSkuChange(index: number, sku: Sku | null) {
  if (sku) {
    skuInfoMap.value[sku.id] = sku
    if (!rows.value[index].unit_cost && sku.cost_price) {
      rows.value[index].unit_cost = Number(sku.cost_price)
    }
  }
}

/** 批量选货确认：逐个 SKU 生成明细行（跳过已存在的 SKU），数量默认 1 */
function addBatchRows(skus: Sku[]) {
  const existing = new Set(rows.value.map((row) => row.sku_id))
  for (const sku of skus) {
    skuInfoMap.value[sku.id] = sku
    if (existing.has(sku.id)) continue
    rows.value.push({
      sku_id: sku.id,
      quantity: 1,
      unit_cost: Number(sku.cost_price || 0),
      batch_no: '',
      production_date: null,
      expire_date: null,
    })
    existing.add(sku.id)
  }
}

async function loadSuppliers() {
  supplierLoading.value = true
  try {
    const res = await getSupplierList({ page: 1, page_size: 100 })
    if (res.data.code === 0) {
      const list: Supplier[] = res.data.data.list
      supplierOptions.value = list.filter((s) => s.status === 1).map((s) => ({ label: s.name, value: s.id }))
      // 按采购单入库：自动带出采购单供应商
      if (purchase.value?.info.supplier_id && !form.value.supplier_id) {
        form.value.supplier_id = purchase.value.info.supplier_id
      }
    }
  } finally {
    supplierLoading.value = false
  }
}

async function loadPurchase() {
  const res = await getPurchaseOrderDetail(purchaseId.value)
  if (res.data.code === 0) {
    purchase.value = res.data.data
    rows.value = res.data.data.items
      .filter((item) => item.quantity - item.received_quantity > 0)
      .map((item) => ({
        sku_id: item.sku_id,
        sku_code: item.sku_code,
        product_name: item.product_name,
        spec_name: item.spec_name,
        quantity: item.quantity - item.received_quantity,
        unit_cost: item.purchase_price,
        batch_no: '',
        production_date: null,
        expire_date: null,
      }))
  }
}

function formatDate(value: number | null) {
  if (!value) return ''
  const d = new Date(value)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

async function submit() {
  if (rows.value.length === 0) {
    message.warning('请添加入库明细')
    return
  }
  if (rows.value.some((row) => row.quantity < 1)) {
    message.warning('入库数量必须大于 0')
    return
  }
  if (!purchaseId.value && rows.value.some((row) => !row.sku_id)) {
    message.warning('存在未选择 SKU 的明细行')
    return
  }

  submitting.value = true
  try {
    const res = await createStockInOrder({
      supplier_id: form.value.supplier_id ?? undefined,
      purchase_order_id: purchaseId.value || undefined,
      remark: form.value.remark,
      items: rows.value.map((row) => ({
        sku_id: row.sku_id as number,
        quantity: row.quantity,
        unit_cost: row.unit_cost || undefined,
        batch_no: row.batch_no || undefined,
        production_date: formatDate(row.production_date) || undefined,
        expire_date: formatDate(row.expire_date) || undefined,
      })),
    })
    if (res.data.code === 0) {
      message.success(`入库成功，单号 ${res.data.data.info.in_no}`)
      router.push('/stock/in')
    }
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  if (purchaseId.value) {
    await loadPurchase()
  } else {
    addItem()
  }
  await loadSuppliers()
})
</script>
