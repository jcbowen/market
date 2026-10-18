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
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'ProductList' })
import { ref, onMounted, h, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NSpace, NTag, createDiscreteApi } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getProductList, getCategoryList, updateProductStatus, deleteProduct, updateProductName } from '@/api/product'
import type { Product, Category } from '@/api/types'

/**
 * 商品列表页面
 * 展示商品分页数据，支持搜索、分类筛选、上下架、删除、
 * 多规格标注、价格区间展示与名称行内改名
 */
const router = useRouter()
const { message } = createDiscreteApi(['message'])
const keyword = ref('')
const categoryId = ref<number | null>(null)
const categoryOptions = ref<Array<{ label: string; value: number }>>([])
const list = ref<Product[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

// 名称列行内编辑状态：编辑中商品 ID → 草稿名（不污染列表数据）
const editingNames = reactive<Record<number, string>>({})

/**
 * 进入名称行内编辑态
 * @param row 当前行商品
 */
function startRename(row: Product) {
  editingNames[row.id] = row.name
}

/**
 * 提交行内改名（空值或未变更时静默退出编辑态）
 * 业务失败（code !== 0）时拦截器已 reject 并统一弹出提示，
 * 此处捕获以避免 unhandled rejection；不删编辑态，保留草稿供用户修正
 * @param row 当前行商品
 */
async function confirmRename(row: Product) {
  const draft = (editingNames[row.id] ?? '').trim()
  if (!draft || draft === row.name) {
    delete editingNames[row.id]
    return
  }
  try {
    const res = await updateProductName(row.id, draft)
    if (res.data.code === 0) {
      message.success('名称已更新')
      delete editingNames[row.id]
      loadData()
    }
  } catch {
    // 失败提示由 request 拦截器统一弹出；保留编辑态，由用户修正后重试
  }
}

/**
 * 取消行内改名
 * @param row 当前行商品
 */
function cancelRename(row: Product) {
  delete editingNames[row.id]
}

/**
 * 售价展示文本：多规格显示价格区间，单规格显示单价
 * @param row 当前行商品
 * @returns 形如 "¥3.00" 或 "¥3.00 ~ ¥8.00"
 */
function priceText(row: Product): string {
  const fmt = (n: number) => `¥${n.toFixed(2)}`
  if (row.is_multi_spec && row.min_price != null && row.max_price != null) {
    return row.min_price === row.max_price ? fmt(row.min_price) : `${fmt(row.min_price)} ~ ${fmt(row.max_price)}`
  }
  return fmt(row.price ?? 0)
}

const columns: DataTableColumns<Product> = [
  { title: 'ID', key: 'id', width: 80 },
  {
    title: '商品名称',
    key: 'name',
    minWidth: 240,
    render(row) {
      // 编辑态：输入框 + 保存/取消（回车确认，Esc 取消）
      if (row.id in editingNames) {
        return h(NSpace, { wrap: false, size: 4, align: 'center' }, {
          default: () => [
            h(NInput, {
              value: editingNames[row.id],
              size: 'small',
              style: 'width: 200px',
              'onUpdate:value': (v: string) => {
                editingNames[row.id] = v
              },
              onKeydown: (e: KeyboardEvent) => {
                if (e.key === 'Enter') confirmRename(row)
                if (e.key === 'Escape') cancelRename(row)
              },
            }),
            h(NButton, { size: 'tiny', type: 'primary', onClick: () => confirmRename(row) }, { default: () => '保存' }),
            h(NButton, { size: 'tiny', quaternary: true, onClick: () => cancelRename(row) }, { default: () => '取消' }),
          ],
        })
      }
      // 展示态：名称 + 多规格标签 + 改名入口
      return h(NSpace, { wrap: false, size: 6, align: 'center' }, {
        default: () => [
          h('span', null, row.name),
          row.is_multi_spec ? h(NTag, { size: 'small', type: 'info', bordered: false }, { default: () => '多规格' }) : null,
          h(NButton, { size: 'tiny', quaternary: true, type: 'primary', onClick: () => startRename(row) }, { default: () => '改名' }),
        ],
      })
    },
  },
  { title: '分类', key: 'category_name', width: 130 },
  { title: '售价', key: 'price', width: 150, render: (row) => priceText(row) },
  { title: '销量', key: 'sales_count', width: 90, render: (row) => String(row.sales_count ?? 0) },
  { title: '库存', key: 'stock', width: 90 },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '上架' : '下架') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 220,
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
  try {
    const res = await getProductList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      keyword: keyword.value,
      category_id: categoryId.value || undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
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
