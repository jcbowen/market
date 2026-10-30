<template>
  <n-card title="新建出库单">
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="left" label-width="100" require-mark-placement="right-hanging">
      <n-form-item label="出库类型" path="type">
        <n-select v-model:value="form.type" :options="typeOptions" style="width: 240px" />
      </n-form-item>
      <n-form-item label="备注" path="remark">
        <n-input v-model:value="form.remark" type="textarea" placeholder="备注（选填）" :rows="2" maxlength="200" style="width: 480px" />
      </n-form-item>
      <n-form-item label="出库明细">
        <n-space vertical style="width: 100%">
          <n-table :bordered="false" :single-line="true" size="small" style="width: 760px">
            <thead>
              <tr>
                <th style="width: 460px">商品 / SKU</th>
                <th style="width: 160px">出库数量</th>
                <th style="width: 80px">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(item, index) in form.items" :key="index">
                <td>
                  <n-space vertical size="small">
                    <SkuPicker v-model="item.sku_id" @change="(sku) => onSkuChange(index, sku)" />
                    <n-text v-if="skuInfoOf(item.sku_id)" depth="3" style="font-size: 12px">
                      当前库存 {{ skuInfoOf(item.sku_id)!.stock }}
                    </n-text>
                    <n-text v-if="overStock(item)" type="error" style="font-size: 12px">出库数量超过当前库存，可能提交失败</n-text>
                  </n-space>
                </td>
                <td>
                  <n-input-number v-model:value="item.quantity" :min="1" :precision="0" placeholder="数量" style="width: 120px" />
                </td>
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
      <n-form-item>
        <n-space>
          <n-button type="primary" :loading="submitting" @click="submit">确认出库</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockOutForm' })
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import SkuPicker from './components/SkuPicker.vue'
import BatchPickerModal from './components/BatchPickerModal.vue'
import { createStockOutOrder } from '@/api/inventory'
import type { Sku } from '@/api/types'

/**
 * 新建出库单页
 * 类型：1 报损 2 领用 3 调整（后端枚举校验）
 * 出库按 FEFO 从最早过期批次扣减
 */
const router = useRouter()
const message = useMessage()

const formRef = ref<FormInst | null>(null)
const submitting = ref(false)
const batchPickerVisible = ref(false)
// 已选 SKU 信息（含库存），用于行内展示与超库提示
const skuInfoMap = ref<Record<number, Sku>>({})
const form = ref({
  type: 1 as number,
  remark: '',
  items: [] as Array<{ sku_id: number | undefined; quantity: number }>,
})

const typeOptions = [
  { label: '报损', value: 1 },
  { label: '领用', value: 2 },
  { label: '调整', value: 3 },
]

const rules: FormRules = {
  type: { type: 'number', required: true, message: '请选择出库类型', trigger: 'change' },
}

function addItem() {
  form.value.items.push({ sku_id: undefined, quantity: 1 })
}

function removeItem(index: number) {
  form.value.items.splice(index, 1)
}

function skuInfoOf(skuId: number | undefined) {
  return skuId ? skuInfoMap.value[skuId] : undefined
}

/** 出库数量是否超过当前库存（软提示，最终以后端 FEFO 扣减校验为准） */
function overStock(item: { sku_id: number | undefined; quantity: number }) {
  const sku = skuInfoOf(item.sku_id)
  return !!sku && item.quantity > sku.stock
}

function onSkuChange(_index: number, sku: Sku | null) {
  if (sku) {
    skuInfoMap.value[sku.id] = sku
  }
}

/** 批量选货确认：逐个 SKU 生成明细行（跳过已存在的 SKU），数量默认 1 */
function addBatchRows(skus: Sku[]) {
  const existing = new Set(form.value.items.map((item) => item.sku_id))
  for (const sku of skus) {
    skuInfoMap.value[sku.id] = sku
    if (existing.has(sku.id)) continue
    form.value.items.push({ sku_id: sku.id, quantity: 1 })
    existing.add(sku.id)
  }
}

async function submit() {
  if (!formRef.value) return
  if (form.value.items.length === 0) {
    message.warning('请添加出库明细')
    return
  }
  if (form.value.items.some((item) => !item.sku_id)) {
    message.warning('存在未选择 SKU 的明细行')
    return
  }
  if (form.value.items.some((item) => item.quantity < 1)) {
    message.warning('出库数量必须大于 0')
    return
  }

  submitting.value = true
  try {
    const res = await createStockOutOrder({
      type: form.value.type,
      remark: form.value.remark,
      // 幂等业务号：防网络重试/双击重复出库（服务端同 biz_no 直接返回原单）
      biz_no: crypto.randomUUID(),
      items: form.value.items.map((item) => ({ sku_id: item.sku_id as number, quantity: item.quantity })),
    })
    if (res.data.code === 0) {
      message.success(`出库成功，单号 ${res.data.data.info.out_no}`)
      router.push('/stock/out')
    }
  } finally {
    submitting.value = false
  }
}

onMounted(addItem)
</script>
