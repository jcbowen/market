<template>
  <n-card :title="isEdit ? '编辑采购单' : '新建采购单'">
    <n-form ref="formRef" :model="form" label-placement="left" label-width="100" require-mark-placement="right-hanging">
      <n-form-item label="供应商" path="supplier_id">
        <n-select
          v-model:value="form.supplier_id"
          :options="supplierOptions"
          :loading="supplierLoading"
          filterable
          placeholder="请选择供应商（可在采购管理页维护）"
          style="width: 360px"
        />
      </n-form-item>
      <n-form-item label="备注" path="remark">
        <n-input v-model:value="form.remark" type="textarea" placeholder="备注（选填）" :rows="2" maxlength="200" style="width: 480px" />
      </n-form-item>
      <n-form-item label="采购明细">
        <n-space vertical style="width: 100%">
          <n-table :bordered="false" :single-line="true" size="small" style="width: 900px">
            <thead>
              <tr>
                <th style="width: 400px">商品 / SKU</th>
                <th style="width: 140px">采购数量</th>
                <th style="width: 160px">采购单价（元）</th>
                <th style="width: 140px">小计（元）</th>
                <th style="width: 80px">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(item, index) in form.items" :key="index">
                <td>
                  <template v-if="item.from_replenish">
                    <n-text>{{ item.from_replenish.product_name }}</n-text>
                    <n-text v-if="item.from_replenish.spec_name" depth="3"> / {{ item.from_replenish.spec_name }}</n-text>
                    <n-text depth="3" style="font-size: 12px; display: block">
                      {{ item.from_replenish.sku_code }} · 当前库存 {{ item.from_replenish.current_stock }} / 安全库存 {{ item.from_replenish.safety_stock }}
                    </n-text>
                  </template>
                  <n-space v-else vertical size="small">
                    <SkuPicker v-model="item.sku_id" @change="(sku) => onSkuChange(index, sku)" />
                    <n-text v-if="skuInfoOf(item.sku_id)" depth="3" style="font-size: 12px">
                      当前库存 {{ skuInfoOf(item.sku_id)!.stock }} · 成本 ¥{{ formatAmount(skuInfoOf(item.sku_id)!.cost_price) }}
                    </n-text>
                  </n-space>
                </td>
                <td>
                  <n-input-number v-model:value="item.quantity" :min="1" :precision="0" placeholder="数量" style="width: 120px" />
                </td>
                <td>
                  <n-input-number v-model:value="item.purchase_price" :min="0" :precision="2" placeholder="单价" style="width: 130px" />
                </td>
                <td>{{ formatAmount(item.quantity * item.purchase_price) }}</td>
                <td>
                  <n-button size="small" type="error" quaternary @click="removeItem(index)">删除</n-button>
                </td>
              </tr>
            </tbody>
          </n-table>
          <n-button size="small" dashed @click="addItem">+ 添加一行</n-button>
          <n-button size="small" dashed @click="batchPickerVisible = true">批量选货</n-button>
          <BatchPickerModal v-model:show="batchPickerVisible" @confirm="addBatchRows" />
        </n-space>
      </n-form-item>
      <n-form-item label="合计金额">
        <n-text strong style="font-size: 16px">¥{{ formatAmount(totalAmount) }}</n-text>
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" :loading="submitting" @click="submit(false)">保存草稿</n-button>
          <n-button type="primary" :loading="submitting" @click="submit(true)">保存并提交</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'PurchaseForm' })
import { computed, onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst } from 'naive-ui'
import SkuPicker from './components/SkuPicker.vue'
import BatchPickerModal from './components/BatchPickerModal.vue'
import { generatePurchaseOrder, getSupplierList, getPurchaseOrderDetail, createPurchaseOrder, updatePurchaseOrder, submitPurchaseOrder } from '@/api/inventory'
import type { Sku, Supplier } from '@/api/types'

/**
 * 采购单表单页
 * 新建：选供应商 + 明细行（SKU/数量/采购价）
 * 编辑：仅草稿状态可编辑，提交后走 submit 接口
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()

const editId = route.params.id ? Number(route.params.id) : 0
const isEdit = computed(() => editId > 0)

interface ItemRow {
  sku_id: number | undefined
  quantity: number
  purchase_price: number
  /** 补货建议带入的行：只读展示，不走选择器 */
  from_replenish?: { product_name: string; spec_name: string; sku_code: string; current_stock: number; safety_stock: number }
}

const formRef = ref<FormInst | null>(null)
const supplierOptions = ref<{ label: string; value: number }[]>([])
const supplierLoading = ref(false)
const submitting = ref(false)
const batchPickerVisible = ref(false)
// 已选 SKU 信息（含库存/成本），用于行内展示
const skuInfoMap = ref<Record<number, Sku>>({})
const form = ref({
  supplier_id: null as number | null,
  remark: '',
  items: [] as ItemRow[],
})

const totalAmount = computed(() => form.value.items.reduce((sum, item) => sum + item.quantity * item.purchase_price, 0))

function addItem() {
  form.value.items.push({ sku_id: undefined, quantity: 1, purchase_price: 0 })
}

function removeItem(index: number) {
  form.value.items.splice(index, 1)
}

function skuInfoOf(skuId: number | undefined) {
  return skuId ? skuInfoMap.value[skuId] : undefined
}

/** 选中 SKU 时记录信息用于行内展示，并用当前成本价预填采购价，省一次输入 */
function onSkuChange(index: number, sku: Sku | null) {
  if (sku) {
    skuInfoMap.value[sku.id] = sku
    if (!form.value.items[index].purchase_price && sku.cost_price) {
      form.value.items[index].purchase_price = Number(sku.cost_price)
    }
  }
}

/** 批量选货确认：逐个 SKU 生成明细行（跳过已存在的 SKU），数量默认 1 */
function addBatchRows(skus: Sku[]) {
  const existing = new Set(form.value.items.map((item) => item.sku_id))
  for (const sku of skus) {
    skuInfoMap.value[sku.id] = sku
    if (existing.has(sku.id)) continue
    form.value.items.push({ sku_id: sku.id, quantity: 1, purchase_price: Number(sku.cost_price || 0) })
    existing.add(sku.id)
  }
}

async function loadSuppliers() {
  supplierLoading.value = true
  try {
    const res = await getSupplierList({ page: 1, page_size: 100 })
    if (res.data.code === 0) {
      supplierOptions.value = res.data.data.list.filter((s) => s.status === 1).map((s) => ({ label: s.name, value: s.id }))
    }
  } finally {
    supplierLoading.value = false
  }
}

async function loadDetail() {
  const res = await getPurchaseOrderDetail(editId)
  if (res.data.code === 0) {
    const detail = res.data.data
    form.value.supplier_id = detail.info.supplier_id || null
    form.value.remark = detail.info.remark
    form.value.items = detail.items.map((item) => ({
      sku_id: item.sku_id,
      quantity: item.quantity,
      purchase_price: item.purchase_price,
    }))
  }
}

async function submit(submitNow: boolean) {
  if (!formRef.value) return
  if (!form.value.supplier_id) {
    message.warning('请选择供应商')
    return
  }
  if (form.value.items.length === 0) {
    message.warning('请至少添加一行采购明细')
    return
  }
  if (form.value.items.some((item) => !item.sku_id)) {
    message.warning('存在未选择 SKU 的明细行')
    return
  }
  if (form.value.items.some((item) => item.quantity < 1)) {
    message.warning('采购数量必须大于 0')
    return
  }

  submitting.value = true
  try {
    const payload = {
      supplier_id: form.value.supplier_id,
      remark: form.value.remark,
      items: form.value.items.map((item) => ({
        sku_id: item.sku_id as number,
        quantity: item.quantity,
        purchase_price: item.purchase_price,
      })),
    }
    let id = editId
    if (isEdit.value) {
      const res = await updatePurchaseOrder(editId, payload)
      if (res.data.code !== 0) return
      id = editId
    } else {
      const res = await createPurchaseOrder({ ...payload, submit_now: submitNow })
      if (res.data.code === 0) {
        id = res.data.data.info.id
      } else {
        return
      }
    }
    if (submitNow) {
      const res = await submitPurchaseOrder(id)
      if (res.data.code === 0) {
        message.success('采购单已提交')
        router.push(`/stock/purchase/${id}`)
        return
      }
    }
    message.success(submitNow ? '已提交' : '草稿已保存')
    router.push('/stock/purchase')
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  addItem()
  await loadSuppliers()
  if (isEdit.value) {
    await loadDetail()
  } else {
    await loadReplenishSuggestion()
  }
})

/** 读取库存工作台「一键补货」带来的 sku_ids（query.replenish=1&sku_ids=1,2,3），回查补货建议预填明细行 */
async function loadReplenishSuggestion() {
  if (typeof route.query.replenish !== 'string' || !route.query.replenish) return
  const raw = route.query.sku_ids
  let skuIds: number[] | undefined
  if (typeof raw === 'string' && raw) {
    skuIds = raw
      .split(',')
      .map((v) => Number(v))
      .filter((v) => Number.isInteger(v) && v > 0)
    if (skuIds.length === 0) return
  }
  const res = await generatePurchaseOrder(skuIds ? { sku_ids: skuIds } : undefined)
  if (res.data.code !== 0) return
  const items = res.data.data
  if (!Array.isArray(items) || items.length === 0) {
    message.warning('没有可补货的低库存项')
    return
  }
  form.value.items = items.map((item) => ({
    sku_id: item.sku_id,
    quantity: Math.max(1, item.suggest_quantity || 1),
    purchase_price: 0,
    from_replenish: {
      product_name: item.product_name,
      spec_name: item.spec_name,
      sku_code: item.sku_code,
      current_stock: item.current_stock,
      safety_stock: item.safety_stock,
    },
  }))
  message.info(`已带入 ${items.length} 条补货建议，请确认采购价后提交`)
}
</script>
