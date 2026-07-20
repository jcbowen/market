<template>
  <n-card title="商品列表">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="keyword" placeholder="商品名称" clearable />
        <n-select v-model:value="categoryId" :options="categoryOptions" placeholder="商品分类" clearable style="width: 160px" />
        <n-button type="primary" @click="loadData">查询</n-button>
        <n-button @click="router.push('/products/create')">新增商品</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getProductList, getCategoryList, updateProductStatus, deleteProduct } from '@/api/product'
import type { Product, Category } from '@/api/types'

/**
 * 商品列表页面
 * 展示商品分页数据，支持搜索、分类筛选、上下架和编辑
 */
const router = useRouter()
const keyword = ref('')
const categoryId = ref<number | null>(null)
const categoryOptions = ref<Array<{ label: string; value: number }>>([])
const list = ref<Product[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<Product> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '商品名称', key: 'name' },
  { title: '分类', key: 'category_name' },
  { title: '售价', key: 'price' },
  { title: '库存', key: 'stock' },
  {
    title: '状态',
    key: 'status',
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '上架' : '下架') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => router.push(`/products/detail/${row.id}`) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', onClick: () => toggleStatus(row) }, { default: () => (row.status === 1 ? '下架' : '上架') }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadCategories() {
  const res = await getCategoryList()
  if (res.data.code === 0) {
    categoryOptions.value = (res.data.data || []).map((item: Category) => ({ label: item.name, value: item.id }))
  }
}

async function loadData() {
  loading.value = true
  const res = await getProductList({
    page: pagination.value.page,
    page_size: pagination.value.pageSize,
    keyword: keyword.value,
    category_id: categoryId.value || undefined,
  })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

async function toggleStatus(row: Product) {
  const res = await updateProductStatus(row.id, row.status === 1 ? 0 : 1)
  if (res.data.code === 0) loadData()
}

async function remove(row: Product) {
  const res = await deleteProduct(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(() => {
  loadCategories()
  loadData()
})
</script>
