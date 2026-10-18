<template>
  <n-card title="商品列表">
    <n-space vertical>
      <!-- 状态 Tab：带统计计数，口径与列表筛选一致，随关键词/分类联动 -->
      <n-tabs v-model:value="statusFilter" type="line" :tabs="statusTabs" @update:value="onSearch" />
      <n-space align="center">
        <n-input v-model:value="keyword" placeholder="商品名称" clearable @keyup.enter="onSearch" @clear="onSearch" />
        <n-select v-model:value="categoryId" :options="categoryOptions" placeholder="商品分类" clearable style="width: 160px" @update:value="onSearch" />
        <n-button type="primary" @click="onSearch">查询</n-button>
        <n-button @click="router.push('/products/create')">新增商品</n-button>
        <!-- 批量上下架：选中后按选中项状态动态显示可用操作 -->
        <template v-if="checkedRowKeys.length > 0">
          <n-button v-if="showBatchOn" size="small" type="success" @click="batchToggle(1)">批量上架</n-button>
          <n-button v-if="showBatchOff" size="small" type="warning" @click="batchToggle(0)">批量下架</n-button>
        </template>
      </n-space>
      <n-data-table
        v-model:checked-row-keys="checkedRowKeys"
        :columns="columns"
        :data="list"
        :loading="loading"
        :pagination="pagination"
        :row-key="rowKey"
      />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'ProductList' })
import { ref, onMounted, onActivated, h, reactive, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NButton, NInput, NSpace, NTag, createDiscreteApi } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getProductList, getCategoryList, updateProductStatus, deleteProduct, updateProductName, batchUpdateProductStatus, getProductStatusStats } from '@/api/product'
import type { Product, Category } from '@/api/types'

/**
 * 商品列表页面
 * 展示商品分页数据，支持状态 Tab 筛选（带统计）、搜索、分类筛选、
 * 上下架（单条/批量）、删除、多规格标注、价格区间展示与名称行内改名
 */
const router = useRouter()
const route = useRoute()
const { message, dialog } = createDiscreteApi(['message', 'dialog'])
const keyword = ref('')
const categoryId = ref<number | null>(null)
const categoryOptions = ref<Array<{ label: string; value: number }>>([])
const list = ref<Product[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

// 状态 Tab 筛选：值与后端 status_filter 口径一致（0 全部 1 在售中 2 已售罄 3 仓库中）
const statusFilter = ref('0')
const stats = ref<{ total: number; on_sale: number; sold_out: number; warehouse: number } | null>(null)
const statusTabs = computed(() => [
  { name: '0', label: `全部 ${stats.value?.total ?? 0}` },
  { name: '1', label: `在售中 ${stats.value?.on_sale ?? 0}` },
  { name: '2', label: `已售罄 ${stats.value?.sold_out ?? 0}` },
  { name: '3', label: `仓库中 ${stats.value?.warehouse ?? 0}` },
])

// 多选与批量上下架：按钮按选中项状态动态显示（存在非该状态的行才显示对应操作）
const checkedRowKeys = ref<number[]>([])
const rowKey = (row: Product) => row.id
const selectedRows = computed(() => list.value.filter((row) => checkedRowKeys.value.includes(row.id)))
const showBatchOn = computed(() => selectedRows.value.some((row) => row.status !== 1))
const showBatchOff = computed(() => selectedRows.value.some((row) => row.status !== 0))

/**
 * 触发搜索/切换状态 Tab：重置页码后按当前条件加载（统计随 loadData 同步刷新）
 */
function onSearch() {
  pagination.value.page = 1
  loadData()
}

/**
 * 加载状态统计（口径与列表筛选一致，关键词/分类变化时随列表同步刷新）
 * 统计失败只由拦截器弹提示，不阻塞列表加载
 */
async function loadStats() {
  try {
    const res = await getProductStatusStats({
      keyword: keyword.value,
      category_id: categoryId.value || undefined,
    })
    if (res.data.code === 0) stats.value = res.data.data
  } catch {
    // 失败提示由 request 拦截器统一弹出；Tab 计数维持上次值
  }
}

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
  { type: 'selection' },
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
      const meta = displayStatusMeta(row)
      return h(NTag, { type: meta.type }, { default: () => meta.label })
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

/**
 * 商品展示状态（复合口径）：上架且有库存=在售中，上架无库存=已售罄，下架=仓库中
 * @param row 当前行商品
 * @returns 状态标签文本与 Tag 类型（success/warning/default）
 */
function displayStatusMeta(row: Product): { label: string; type: 'success' | 'warning' | 'default' } {
  if (row.status === 1) {
    return (row.stock ?? 0) > 0
      ? { label: '在售中', type: 'success' }
      : { label: '已售罄', type: 'warning' }
  }
  return { label: '仓库中', type: 'default' }
}

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
      status_filter: Number(statusFilter.value) || undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
    // 统计与列表同条件刷新（数据变更/筛选变化共用此入口）
    await loadStats()
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
    // 数据已变化，清空跨请求残留的多选，避免批量操作命中不可见行
    checkedRowKeys.value = []
  }
}

/**
 * 单条上下架切换，成功后刷新列表与统计
 * @param row 当前行商品
 */
async function toggleStatus(row: Product) {
  const res = await updateProductStatus(row.id, row.status === 1 ? 0 : 1)
  if (res.data.code === 0) loadData()
}

/**
 * 批量上下架（带确认弹窗），成功后刷新列表与统计并清空选中
 * @param status 目标状态：1 上架，0 下架
 */
function batchToggle(status: number) {
  const ids = [...checkedRowKeys.value]
  dialog.warning({
    title: '批量操作确认',
    content: `确定将选中的 ${ids.length} 件商品${status === 1 ? '上架' : '下架'}吗？`,
    positiveText: '确定',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        const res = await batchUpdateProductStatus(ids, status)
        if (res.data.code === 0) {
          message.success(`已${status === 1 ? '上架' : '下架'} ${ids.length} 件商品`)
          loadData()
        }
      } catch {
        // 失败提示由 request 拦截器统一弹出；弹窗保持打开供重试
      }
    },
  })
}

async function remove(row: Product) {
  const res = await deleteProduct(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(() => {
  // 支持「统计热销排行」等外部入口带 keyword 预筛选
  const q = route.query.keyword
  if (typeof q === 'string' && q) keyword.value = q
  loadCategories()
  loadData()
})

// keep-alive 缓存下再次进入（如统计页跳转）时，应用新的 keyword 参数重新筛选
onActivated(() => {
  const q = route.query.keyword
  if (typeof q === 'string' && q && q !== keyword.value) {
    keyword.value = q
    pagination.value.page = 1
    loadData()
  }
})
</script>
