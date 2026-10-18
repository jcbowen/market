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
    :consistent-menu-width="false"
    :render-label="renderLabel"
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
import { h, onBeforeUnmount, ref, type VNodeChild } from 'vue'
import type { SelectOption } from 'naive-ui'
import { getSkuStockList } from '@/api/inventory'
import type { Sku } from '@/api/types'

/** 下拉选项结构：在标准 SelectOption 上挂载完整 SKU 信息，供自定义渲染与 change 回传使用 */
interface PickerOption extends SelectOption {
  label: string
  value: number
  sku: Sku
}

const props = defineProps<{
  modelValue?: number
  placeholder?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: number | undefined): void
  (e: 'change', sku: Sku | null): void
}>()

const options = ref<PickerOption[]>([])
const searching = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let requestSeq = 0 // 防止乱序响应覆盖新结果

/**
 * 生成选项的纯文本摘要（单行）
 * @param sku 完整 SKU 信息
 * @returns 形如「商品名 · 规格（SKU编码｜库存 N）」的字符串，用作选项 title 悬浮全量提示
 */
function skuLabel(sku: Sku) {
  const name = sku.product_name || sku.sku_code
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  return `${name}${spec}（${sku.sku_code}｜库存 ${sku.stock}）`
}

/**
 * 自定义下拉选项渲染：两行布局，避免「商品名·规格（编码｜库存）」单行超长被面板截断
 * 主行「商品名 · 规格」超长省略；副行小字灰色展示 SKU 编码 / 条形码 / 库存，
 * 库存 ≤ 安全库存时追加红色「库存不足」提醒（进销存选货关键信号）
 * @param option 当前选项（含自定义 sku 字段）
 * @returns VNode 子节点；根节点带 title 属性，悬浮可看全量文本，无抛错路径
 * @example <n-select :render-label="renderLabel" />
 */
function renderLabel(option: SelectOption): VNodeChild {
  const sku = (option as PickerOption).sku
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  const meta = [sku.sku_code, sku.barcode, `库存 ${sku.stock}`].filter(Boolean).join(' ｜ ')
  const slots = [
    h('span', { style: 'color: rgba(0, 0, 0, 0.45)' }, meta),
    ...(sku.stock <= sku.safety_stock ? [h('span', { style: 'color: #d03050' }, '库存不足')] : []),
  ]
  return h('div', { style: 'min-width: 0; max-width: 480px', title: skuLabel(sku) }, [
    h('div', { style: 'overflow: hidden; text-overflow: ellipsis; white-space: nowrap' }, `${sku.product_name || sku.sku_code}${spec}`),
    h('div', { style: 'display: flex; align-items: center; gap: 8px; margin-top: 2px; font-size: 12px; line-height: 17px' }, slots),
  ])
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
