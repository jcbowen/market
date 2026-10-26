<template>
  <n-modal v-model:show="visible" preset="card" title="批量选货" style="width: 860px">
    <n-space vertical>
      <n-space>
        <n-input
          v-model:value="keyword"
          placeholder="商品名 / 编码 / 条形码"
          style="width: 240px"
          clearable
          @keyup.enter="onSearch"
        />
        <n-button type="primary" :loading="loading" @click="onSearch">查询</n-button>
        <n-text depth="3">勾选商品后点「添加所选」，一次带入多行明细</n-text>
      </n-space>
      <n-data-table
        :columns="columns"
        :data="list"
        :loading="loading"
        :pagination="pagination"
        remote
        :row-key="(row: Sku) => row.id"
        :checked-row-keys="checkedKeys"
        max-height="420px"
        @update:page="onPageChange"
        @update:checked-row-keys="onCheckedChange"
      />
    </n-space>
    <template #footer>
      <n-space justify="space-between" align="center">
        <n-text depth="3">已选 {{ checkedKeys.length }} 项</n-text>
        <n-space>
          <n-button @click="visible = false">取消</n-button>
          <n-button type="primary" :disabled="!checkedKeys.length" @click="confirm">添加所选</n-button>
        </n-space>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
/**
 * 批量选货弹窗：搜索 + 分页 + 勾选多个 SKU，确认后由父组件批量生成明细行。
 * 供采购单/入库单/出库单表单复用，避免"加 10 个商品点 10 次添加一行"的重复操作。
 */
import { h, ref, watch } from 'vue'
import { NText } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getSkuStockList } from '@/api/inventory'
import type { Sku } from '@/api/types'

const emit = defineEmits<{
  (e: 'confirm', skus: Sku[]): void
}>()

const visible = defineModel<boolean>('show', { default: false })

const keyword = ref('')
const list = ref<Sku[]>([])
const loading = ref(false)
const checkedKeys = ref<number[]>([])
const checkedMap = ref<Record<number, Sku>>({})
const pagination = ref({ page: 1, pageSize: 10, itemCount: 0 })

const columns: DataTableColumns<Sku> = [
  { type: 'selection' },
  {
    title: '商品',
    key: 'product_name',
    ellipsis: { tooltip: true },
    render: (row) =>
      h('span', {}, [
        row.product_name || row.sku_code,
        row.spec_name ? h(NText, { depth: 3 }, { default: () => ` · ${row.spec_name}` }) : null,
      ]),
  },
  { title: 'SKU 编码', key: 'sku_code', width: 140 },
  { title: '售价', key: 'price', width: 90 },
  { title: '成本价', key: 'cost_price', width: 90 },
  { title: '库存', key: 'stock', width: 80 },
]

async function load() {
  loading.value = true
  try {
    const res = await getSkuStockList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      keyword: keyword.value.trim() || undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
      // 翻页不丢勾选：把当前页已勾选的 SKU 记入 map
      for (const sku of list.value) {
        if (checkedKeys.value.includes(sku.id)) checkedMap.value[sku.id] = sku
      }
    }
  } finally {
    loading.value = false
  }
}

function onPageChange(page: number) {
  pagination.value.page = page
  load()
}

/**
 * 查询：关键词变化后回到第 1 页
 * 停在第 N 页换关键词时，新结果不足 N 页会显示为空，看起来像「搜不到货」
 */
function onSearch() {
  pagination.value.page = 1
  load()
}

function onCheckedChange(keys: Array<number | string>) {
  checkedKeys.value = keys as number[]
  for (const sku of list.value) {
    if (checkedKeys.value.includes(sku.id)) checkedMap.value[sku.id] = sku
  }
}

function confirm() {
  const skus = checkedKeys.value.map((id) => checkedMap.value[id]).filter(Boolean)
  emit('confirm', skus)
  visible.value = false
  // 下次打开重置勾选
  checkedKeys.value = []
  checkedMap.value = {}
}

defineExpose({ open: () => (visible.value = true) })

// 打开时重置到第一页并加载
watch(visible, (val) => {
  if (val) {
    pagination.value.page = 1
    load()
  }
})
</script>
