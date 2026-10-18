<template>
  <n-select
    :value="modelValue"
    :options="options"
    :loading="searching"
    :placeholder="placeholder || '输入商品名 / 编码 / 条形码搜索'"
    filterable
    remote
    clearable
    :clear-filter-after-select="false"
    style="width: 300px"
    @search="onSearch"
    @update:value="onSelect"
    @clear="onClear"
  />
</template>

<script setup lang="ts">
/**
 * SKU 选择器（单框远程搜索）
 * 输入即搜（防抖 300ms）：商品名 / SKU 编码 / 条形码，SKU 级选项直达选中，
 * 替代旧版「输入关键字 → 点查询 → 选商品 → 选 SKU」的多步交互。
 * 供采购单/入库单/出库单/盘点详情的明细行选择商品使用，change 事件回传完整 SKU（含商品名/库存）。
 */
import { onBeforeUnmount, ref } from 'vue'
import { getSkuStockList } from '@/api/inventory'
import type { Sku } from '@/api/types'

const props = defineProps<{
  modelValue?: number
  placeholder?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: number | undefined): void
  (e: 'change', sku: Sku | null): void
}>()

const options = ref<{ label: string; value: number; sku: Sku }[]>([])
const searching = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let requestSeq = 0 // 防止乱序响应覆盖新结果

function skuLabel(sku: Sku) {
  const name = sku.product_name || sku.sku_code
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  return `${name}${spec}（${sku.sku_code}｜库存 ${sku.stock}）`
}

function onSearch(keyword: string) {
  if (searchTimer) clearTimeout(searchTimer)
  if (!keyword.trim()) {
    options.value = []
    return
  }
  searchTimer = setTimeout(async () => {
    const seq = ++requestSeq
    searching.value = true
    try {
      const res = await getSkuStockList({ page: 1, page_size: 20, keyword: keyword.trim() })
      if (seq !== requestSeq) return
      if (res.data.code === 0) {
        options.value = (res.data.data.list || []).map((sku) => ({
          label: skuLabel(sku),
          value: sku.id,
          sku,
        }))
      }
    } finally {
      if (seq === requestSeq) searching.value = false
    }
  }, 300)
}

function onSelect(value: number | null) {
  const hit = value ? options.value.find((opt) => opt.value === value) : null
  emit('update:modelValue', value ?? undefined)
  emit('change', hit?.sku ?? null)
}

function onClear() {
  emit('update:modelValue', undefined)
  emit('change', null)
}

// 暴露给父组件：清空搜索状态（如盘点添加完成后重置）
defineExpose({
  reset() {
    if (searchTimer) clearTimeout(searchTimer)
    options.value = []
  },
})

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
})

void props
</script>
