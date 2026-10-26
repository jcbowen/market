<template>
  <n-space align="center" :wrap="false" :size="4" style="width: 100%">
    <n-button dashed block style="justify-content: flex-start" @click="openPicker">
      <span v-if="selectedSku" class="sku-picker-modal__echo" :title="fullLabel">{{ echoText }}</span>
      <n-text v-else depth="3">选择商品</n-text>
    </n-button>
    <n-button v-if="selectedSku" size="tiny" quaternary circle title="清除已选" @click="clear">×</n-button>
  </n-space>
  <n-modal v-model:show="visible" preset="card" title="选择商品" style="width: 860px">
    <n-space vertical>
      <n-input
        v-model:value="keyword"
        placeholder="输入商品名 / 编码 / 条形码搜索"
        clearable
        @input="onKeywordInput"
        @keyup.enter="searchNow"
      />
      <n-data-table
        :columns="columns"
        :data="list"
        :loading="loading"
        :pagination="pagination"
        :row-key="(row: Sku) => row.id"
        max-height="420px"
        size="small"
        remote
        @update:page="onPageChange"
      />
    </n-space>
    <template #footer>
      <n-text depth="3">点击行内「选择」带回该商品，输入即搜（防抖 300ms）</n-text>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
/**
 * SKU 选货弹窗（方案B）：触发按钮 + 搜索 + 多列表格弹窗选择。
 * 与 SkuPicker（方案A 单框下拉）对外接口一致（v-model + change 回传完整 SKU），
 * 便于在采购单/入库单/出库单/盘点详情的明细行间一行替换切换。
 * 弹窗内多列展示完整信息（名称/规格/编码/条形码/库存/成本），无截断遮挡。
 */
import { computed, h, onUnmounted, ref, watch } from 'vue'
import { formatAmount } from '@/utils/format'
import { NTag, NText } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getSkuStockList } from '@/api/inventory'
import type { Sku } from '@/api/types'

const props = defineProps<{
  modelValue?: number
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: number | undefined): void
  (e: 'change', sku: Sku | null): void
}>()

const visible = ref(false)
const keyword = ref('')
const list = ref<Sku[]>([])
const loading = ref(false)
const selectedSku = ref<Sku | null>(null)
const pagination = ref({ page: 1, pageSize: 10, itemCount: 0 })
let searchTimer: ReturnType<typeof setTimeout> | null = null
let requestSeq = 0 // 防止乱序响应覆盖新结果

// 防抖计时器需在卸载时清掉：组件常驻父页面时，弹窗关闭前残留的计时会在下次打开后误触发加载
onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = null
})

/** 回显文本：选中后触发按钮内显示「商品名 · 规格」，超长省略并悬浮全量提示 */
const echoText = computed(() => {
  const sku = selectedSku.value
  if (!sku) return ''
  const spec = sku.spec_name ? ` · ${sku.spec_name}` : ''
  return `${sku.product_name || sku.sku_code}${spec}`
})

/** 回显悬浮提示：商品名·规格（SKU编码｜库存 N）全量摘要 */
const fullLabel = computed(() => {
  const sku = selectedSku.value
  if (!sku) return ''
  return `${echoText.value}（${sku.sku_code}｜库存 ${sku.stock}）`
})

const columns: DataTableColumns<Sku> = [
  {
    title: '商品',
    key: 'product_name',
    minWidth: 220,
    render: (row) =>
      h('div', { style: 'min-width: 0' }, [
        h('div', { style: 'overflow: hidden; text-overflow: ellipsis; white-space: nowrap' }, row.product_name || row.sku_code),
        h(
          'div',
          { style: 'font-size: 12px; color: rgba(0, 0, 0, 0.45); margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap' },
          [row.spec_name, row.sku_code].filter(Boolean).join(' · '),
        ),
      ]),
  },
  { title: '条形码', key: 'barcode', width: 130, render: (row) => (row.barcode ? row.barcode : '—') },
  {
    title: '库存',
    key: 'stock',
    width: 110,
    render: (row) =>
      h('span', {}, [
        String(row.stock),
        row.stock <= row.safety_stock
          ? h(NTag, { size: 'small', type: 'error', style: 'margin-left: 6px' }, { default: () => '库存不足' })
          : null,
      ]),
  },
  { title: '成本价', key: 'cost_price', width: 90, render: (row) => `¥${formatAmount(row.cost_price)}` },
  {
    title: '操作',
    key: 'action',
    width: 80,
    render: (row) => h('a', { style: 'cursor: pointer', onClick: () => pick(row) }, '选择'),
  },
]

/**
 * 拉取 SKU 列表（含乱序响应保护）
 * @returns 无；结果写入 list 与 pagination.itemCount
 * @example searchNow()
 */
async function load() {
  const seq = ++requestSeq
  loading.value = true
  try {
    const res = await getSkuStockList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      keyword: keyword.value.trim() || undefined,
    })
    if (seq !== requestSeq) return
    if (res.data.code === 0) {
      list.value = res.data.data.list || []
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

/** 立即搜索：清空防抖计时并回到第一页重新加载 */
function searchNow() {
  if (searchTimer) clearTimeout(searchTimer)
  pagination.value.page = 1
  load()
}

/** 输入即搜（防抖 300ms），空关键词直接回第一页加载全量 */
function onKeywordInput(value: string) {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    pagination.value.page = 1
    load()
    void value
  }, 300)
}

function onPageChange(page: number) {
  pagination.value.page = page
  load()
}

/**
 * 选中 SKU：回传 v-model 与完整 SKU，关闭弹窗
 * @param sku 行内选中的 SKU
 */
function pick(sku: Sku) {
  selectedSku.value = sku
  emit('update:modelValue', sku.id)
  emit('change', sku)
  visible.value = false
}

/** 清除已选：回传空值，触发框恢复「选择商品」占位 */
function clear() {
  selectedSku.value = null
  emit('update:modelValue', undefined)
  emit('change', null)
}

function openPicker() {
  visible.value = true
}

// 暴露给父组件：清空搜索状态（与 SkuPicker.reset 对齐）
defineExpose({
  reset() {
    if (searchTimer) clearTimeout(searchTimer)
    keyword.value = ''
  },
})

// 打开时回到第一页重新加载，保证看到最新库存
watch(visible, (val) => {
  if (val) {
    pagination.value.page = 1
    load()
  }
})

void props
</script>

<style scoped>
.sku-picker-modal__echo {
  display: inline-block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
}
</style>
