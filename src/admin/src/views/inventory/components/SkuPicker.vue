<template>
  <n-space align="center">
    <n-input
      v-model:value="keyword"
      :placeholder="placeholder || '输入商品名/编码搜索'"
      style="width: 200px"
      clearable
      @keyup.enter="searchProducts"
    />
    <n-button size="small" @click="searchProducts">查询商品</n-button>
    <n-select
      :value="productId"
      :options="productOptions"
      filterable
      clearable
      placeholder="选择商品"
      style="width: 220px"
      @update:value="onProductChange"
    />
    <n-select
      :value="modelValue"
      :options="skuOptions"
      :disabled="!skuOptions.length"
      filterable
      clearable
      placeholder="选择规格 SKU"
      style="width: 240px"
      @update:value="onSkuChange"
    />
  </n-space>
</template>

<script setup lang="ts">
/**
 * SKU 选择器：关键字搜索商品 → 选商品 → 选规格 SKU
 * 供采购单/入库单/出库单的明细行选择商品使用
 */
import { ref } from 'vue'
import { getProductList } from '@/api/product'
import { getSkuList } from '@/api/inventory'
import type { Sku } from '@/api/types'

const props = defineProps<{
  modelValue?: number
  placeholder?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: number | undefined): void
  (e: 'change', sku: Sku | null): void
}>()

const keyword = ref('')
const productId = ref<number | undefined>(undefined)
const productOptions = ref<{ label: string; value: number }[]>([])
const skuOptions = ref<{ label: string; value: number }[]>([])
const skuMap = ref<Record<number, Sku>>({})

async function searchProducts() {
  productId.value = undefined
  skuOptions.value = []
  emit('update:modelValue', undefined)
  emit('change', null)

  const res = await getProductList({ page: 1, page_size: 20, keyword: keyword.value || undefined })
  if (res.data.code === 0) {
    productOptions.value = res.data.data.list.map((p) => ({
      label: p.name,
      value: p.id,
    }))
  }
}

async function onProductChange(id: number | null) {
  productId.value = id ?? undefined
  skuOptions.value = []
  skuMap.value = {}
  emit('update:modelValue', undefined)
  emit('change', null)
  if (!id) return

  const res = await getSkuList(id)
  if (res.data.code === 0) {
    const list = res.data.data || []
    skuMap.value = Object.fromEntries(list.map((sku) => [sku.id, sku]))
    skuOptions.value = list.map((sku) => ({
      label: `${sku.sku_code || sku.id}${sku.spec_name ? ` · ${sku.spec_name}` : ''}（库存 ${sku.stock}）`,
      value: sku.id,
    }))
    // 单选规格商品：自动选中唯一 SKU
    if (list.length === 1) {
      onSkuChange(list[0].id)
    }
  }
}

function onSkuChange(id: number | null) {
  const value = id ?? undefined
  emit('update:modelValue', value)
  emit('change', value ? skuMap.value[value] ?? null : null)
}

// 暴露给父组件：外部直接设置已选 SKU（如补货建议带入）
defineExpose({
  reset() {
    keyword.value = ''
    productId.value = undefined
    productOptions.value = []
    skuOptions.value = []
    skuMap.value = {}
  },
})
void props
</script>
