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
    @update:show="onShowChange"
    @update:value="onSelect"
    @clear="onClear"
  />
</template>

<script setup lang="ts">
/**
 * SKU 选择器（单框远程搜索）
 * 点击展开即加载默认前 20 条（免输入直接浏览选货）；输入即搜（防抖 300ms）：商品名 / SKU 编码 / 条形码，
 * SKU 级选项直达选中，替代旧版「输入关键字 → 点查询 → 选商品 → 选 SKU」的多步交互。
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
 * 生成触发框回显用的短文本
 * @param sku 完整 SKU 信息
 * @returns 「商品名 · 规格」，保持单行，避免回显撑高触发框导致页面错位
 */
function shortLabel(sku: Sku) {
  const name = sku.product_name || sku.sku_code
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  return `${name}${spec}`
}

/**
 * 自定义选项渲染：按渲染场景分支（Naive UI 的选中回显与菜单选项共用本函数）
 * - 触发框回显（selected=true）：单行「商品名 · 规格」，ellipsis 截断 + title 悬浮全量信息，
 *   触发框高度恒定不撑高表格行；库存/成本由明细行下方小字承载
 * - 菜单选项（selected=false）：两行详情布局，主行「商品名 · 规格」，副行小字灰色展示
 *   SKU 编码 / 条形码 / 库存，库存 ≤ 安全库存时追加红色「库存不足」提醒
 * @param option 当前选项（含自定义 sku 字段）
 * @param selected 是否为触发框内的选中回显
 * @returns VNode 子节点，无抛错路径
 * @example <n-select :render-label="renderLabel" />
 */
function renderLabel(option: SelectOption, selected: boolean): VNodeChild {
  const sku = (option as PickerOption).sku
  const full = skuLabel(sku)
  if (selected) {
    return h('span', {
      title: full,
      style: 'display: inline-block; vertical-align: middle; min-width: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap',
    }, shortLabel(sku))
  }
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  const meta = [sku.sku_code, sku.barcode, `库存 ${sku.stock}`].filter(Boolean).join(' ｜ ')
  const slots = [
    h('span', { style: 'color: rgba(0, 0, 0, 0.45)' }, meta),
    ...(sku.stock <= sku.safety_stock ? [h('span', { style: 'color: #d03050' }, '库存不足')] : []),
  ]
  return h('div', { style: 'min-width: 0; max-width: 520px', title: full }, [
    h('div', { style: 'overflow: hidden; text-overflow: ellipsis; white-space: nowrap' }, `${sku.product_name || sku.sku_code}${spec}`),
    h('div', { style: 'display: flex; align-items: center; gap: 8px; margin-top: 2px; font-size: 12px; line-height: 17px' }, slots),
  ])
}

function onSearch(keyword: string) {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => fetchOptions(keyword), 300)
}

/**
 * 拉取选项列表（实际请求，含乱序响应保护）
 * @param keyword 搜索关键词（商品名/SKU 编码/条形码），空串加载默认前 20 条
 * @returns 无；结果写入 options，加载态写入 searching
 * @example fetchOptions('鸡蛋')
 */
async function fetchOptions(keyword: string) {
  const seq = ++requestSeq
  searching.value = true
  try {
    const res = await getSkuStockList({ page: 1, page_size: 20, keyword: keyword.trim() || undefined })
    if (seq !== requestSeq) return
    if (res.data.code === 0) {
      options.value = (res.data.data.list || []).map((sku) => ({
        label: shortLabel(sku),
        value: sku.id,
        sku,
      }))
    }
  } finally {
    if (seq === requestSeq) searching.value = false
  }
}

/**
 * 下拉展开/收起回调：展开且尚无选项时（首次点击或 reset 后）直接加载默认列表，
 * 免输入即可浏览选货；已有选项（如刚搜索过）则保留不覆盖
 * @param show 下拉菜单是否展开
 */
function onShowChange(show: boolean) {
  if (show && options.value.length === 0) {
    if (searchTimer) clearTimeout(searchTimer)
    fetchOptions('')
  }
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
