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
                  <SkuPicker v-model="item.sku_id" @change="(sku) => onSkuChange(index, sku)" />
                </td>
                <td>
                  <n-input-number v-model:value="item.quantity" :min="1" :precision="0" placeholder="数量" style="width: 120px" />
                </td>
                <td>
                  <n-input-number v-model:value="item.purchase_price" :min="0" :precision="2" placeholder="单价" style="width: 130px" />
                </td>
                <td>{{ (item.quantity * item.purchase_price).toFixed(2) }}</td>
                <td>
                  <n-button size="small" type="error" quaternary @click="removeItem(index)">删除</n-button>
                </td>
              </tr>
            </tbody>
          </n-table>
          <n-button size="small" dashed @click="addItem">+ 添加一行</n-button>
        </n-space>
      </n-form-item>
      <n-form-item label="合计金额">
        <n-text strong style="font-size: 16px">¥{{ totalAmount.toFixed(2) }}</n-text>
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
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst } from 'naive-ui'
import SkuPicker from './components/SkuPicker.vue'
import { getSupplierList, getPurchaseOrderDetail, createPurchaseOrder, updatePurchaseOrder, submitPurchaseOrder } from '@/api/inventory'
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
}

const formRef = ref<FormInst | null>(null)
const supplierOptions = ref<{ label: string; value: number }[]>([])
const supplierLoading = ref(false)
const submitting = ref(false)
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

/** 选中 SKU 时用当前成本价预填采购价，省一次输入 */
function onSkuChange(index: number, sku: Sku | null) {
  if (sku && !form.value.items[index].purchase_price && sku.cost_price) {
    form.value.items[index].purchase_price = Number(sku.cost_price)
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
  }
})
</script>
