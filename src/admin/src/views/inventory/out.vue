<template>
  <n-card title="出库管理">
    <n-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-placement="left"
      label-width="100"
      require-mark-placement="right-hanging"
    >
      <n-form-item label="出库类型" path="type">
        <n-select v-model:value="form.type" :options="typeOptions" placeholder="请选择出库类型" />
      </n-form-item>
      <n-form-item label="选择 SKU" path="sku_id">
        <n-select
          v-model:value="form.sku_id"
          :options="skuOptions"
          :loading="loading"
          filterable
          placeholder="请选择要出库的 SKU"
        />
      </n-form-item>
      <n-form-item label="出库数量" path="quantity">
        <n-input-number v-model:value="form.quantity" :min="1" placeholder="请输入出库数量" />
      </n-form-item>
      <n-form-item label="备注" path="remark">
        <n-input v-model:value="form.remark" type="textarea" placeholder="请输入备注" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" :loading="submitting" @click="submit">提交出库</n-button>
          <n-button @click="reset">重置</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { stockOut, getSkuStockList } from '@/api/inventory'
import { getProductList } from '@/api/product'
import type { Sku, Product } from '@/api/types'

/**
 * 库存出库页面
 * 支持销售出库与报损出库，选择 SKU 后提交出库申请
 */
const message = useMessage()
const formRef = ref<FormInst | null>(null)
const loading = ref(false)
const submitting = ref(false)
const skuList = ref<Sku[]>([])
const productMap = ref<Record<number, string>>({})

const form = ref({
  type: 2,
  sku_id: null as number | null,
  quantity: 1,
  remark: '',
})

const typeOptions = [
  { label: '销售出库', value: 2 },
  { label: '报损出库', value: 3 },
]

const skuOptions = computed(() => {
  return skuList.value.map((sku) => {
    const productName = productMap.value[sku.product_id] || '未知商品'
    return {
      label: `${productName} / ${sku.spec_name || sku.sku_code} / 库存：${sku.stock}`,
      value: sku.id,
    }
  })
})

const rules: FormRules = {
  type: { type: 'number', required: true, message: '请选择出库类型', trigger: 'change' },
  sku_id: { type: 'number', required: true, message: '请选择 SKU', trigger: 'change' },
  quantity: { type: 'number', required: true, min: 1, message: '出库数量至少为 1', trigger: 'blur' },
}

/**
 * 加载 SKU 库存与商品信息
 * 通过商品 ID 映射补充 SKU 所属商品名称
 */
async function loadData() {
  loading.value = true
  try {
    const [skuRes, productRes] = await Promise.all([
      getSkuStockList({ page: 1, page_size: 1000 }),
      getProductList({ page: 1, page_size: 1000 }),
    ])
    if (skuRes.data.code === 0) {
      skuList.value = skuRes.data.data.list
    }
    if (productRes.data.code === 0) {
      const map: Record<number, string> = {}
      productRes.data.data.list.forEach((product: Product) => {
        map[product.id] = product.name
      })
      productMap.value = map
    }
  } finally {
    loading.value = false
  }
}

/**
 * 提交出库
 * 校验表单后调用 stockOut API，成功后重置表单
 */
async function submit() {
  if (!formRef.value) return
  await formRef.value.validate()
  if (!form.value.sku_id) return

  submitting.value = true
  try {
    const res = await stockOut({
      type: form.value.type,
      sku_id: form.value.sku_id,
      quantity: form.value.quantity,
      remark: form.value.remark,
    })
    if (res.data.code === 0) {
      message.success('出库成功')
      reset()
    }
  } finally {
    submitting.value = false
  }
}

/**
 * 重置表单
 */
function reset() {
  form.value = {
    type: 2,
    sku_id: null,
    quantity: 1,
    remark: '',
  }
  formRef.value?.restoreValidation()
}

onMounted(loadData)
</script>
