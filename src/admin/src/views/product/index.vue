<template>
  <div class="product-page">
    <!-- 分类管理 -->
    <n-card title="分类管理">
      <n-space vertical>
        <n-space>
          <n-button type="primary" @click="openCategoryModal()">新增分类</n-button>
        </n-space>
        <n-data-table :columns="categoryColumns" :data="categories" :loading="categoryLoading" scroll-x="420" />
      </n-space>
    </n-card>

    <!-- 商品管理 -->
    <n-card title="商品管理" class="product-card">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="searchForm.keyword" placeholder="商品名称" clearable />
          <n-select v-model:value="searchForm.category_id" :options="categoryOptions" placeholder="全部分类" clearable style="width: 160px;" />
          <n-select v-model:value="searchForm.status" :options="statusOptions" placeholder="全部状态" clearable style="width: 160px;" />
          <n-button type="primary" @click="loadProducts">查询</n-button>
          <n-button type="primary" @click="openProductModal()">新增商品</n-button>
        </n-space>
        <n-data-table :columns="productColumns" :data="products" :loading="productLoading" scroll-x="600" />
        <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadProducts" />
      </n-space>
    </n-card>

    <!-- 分类编辑弹窗 -->
    <n-modal v-model:show="showCategoryModal" title="分类信息" preset="card" style="width: 420px; max-width: 90vw" scrollable>
      <n-form :model="categoryForm" label-placement="top">
        <n-form-item label="分类名称">
          <n-input v-model:value="categoryForm.name" placeholder="请输入分类名称" />
        </n-form-item>
        <n-form-item label="排序">
          <n-input-number v-model:value="categoryForm.sort" placeholder="排序" style="width: 100%;" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCategoryModal = false">取消</n-button>
          <n-button type="primary" :loading="categoryModalLoading" @click="saveCategory">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 商品编辑弹窗 -->
    <n-modal v-model:show="showProductModal" title="商品信息" preset="card" style="width: 560px; max-width: 90vw" scrollable>
      <n-form :model="productForm" label-placement="top">
        <n-form-item label="商品名称">
          <n-input v-model:value="productForm.name" placeholder="请输入商品名称" />
        </n-form-item>
        <n-form-item label="所属分类">
          <n-select v-model:value="productForm.category_id" :options="categoryOptions" placeholder="请选择分类" />
        </n-form-item>
        <n-form-item label="封面图 URL">
          <n-input v-model:value="productForm.cover_image" placeholder="请输入商品封面图地址" />
        </n-form-item>
        <n-space>
          <n-form-item label="售价" style="width: 240px;">
            <n-input-number v-model:value="productForm.price" placeholder="售价" style="width: 100%;" />
          </n-form-item>
          <n-form-item label="原价" style="width: 240px;">
            <n-input-number v-model:value="productForm.original_price" placeholder="原价" style="width: 100%;" />
          </n-form-item>
        </n-space>
        <n-space>
          <n-form-item label="库存" style="width: 240px;">
            <n-input-number v-model:value="productForm.stock" placeholder="库存" style="width: 100%;" />
          </n-form-item>
          <n-form-item label="单位" style="width: 240px;">
            <n-input v-model:value="productForm.unit" placeholder="如：份、斤、个" />
          </n-form-item>
        </n-space>
        <n-space>
          <n-form-item label="最低购买份数" style="width: 240px;">
            <n-input-number v-model:value="productForm.min_buy" placeholder="最低购买份数" :min="1" style="width: 100%;" />
          </n-form-item>
          <n-form-item label="排序" style="width: 240px;">
            <n-input-number v-model:value="productForm.sort" placeholder="排序" style="width: 100%;" />
          </n-form-item>
        </n-space>
        <n-form-item label="商品描述">
          <n-input v-model:value="productForm.description" type="textarea" placeholder="商品描述" />
        </n-form-item>
        <n-alert type="info" :show-icon="false">
          此处仅支持快速创建单规格商品；多规格商品请使用「新增商品」页面进行编辑。
        </n-alert>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showProductModal = false">取消</n-button>
          <n-button type="primary" :loading="productModalLoading" @click="saveProduct">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref, computed } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import {
  getCategoryList,
  createCategory,
  updateCategory,
  getProductList,
  createProduct,
  updateProduct,
  updateProductStatus,
} from '@/api/product'
import type { Category, Product, ProductSavePayload } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const authStore = useAuthStore()
const pageSize = 10

const categories = ref<Category[]>([])
const categoryLoading = ref(false)
const products = ref<Product[]>([])
const productLoading = ref(false)
const page = ref(1)
const pageCount = ref(1)

const searchForm = reactive({
  keyword: '',
  category_id: null as number | null,
  status: null as number | null,
})

const statusOptions = [
  { label: '下架', value: 0 },
  { label: '上架', value: 1 },
]

const categoryOptions = computed(() => categories.value.map((item) => ({ label: item.name, value: item.id })))

/** 分类表格列 */
const categoryColumns: DataTableColumns<Category> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '分类名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '排序', key: 'sort', width: 80 },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h('div', [
        h('a', { onClick: () => openCategoryModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑'),
      ])
    },
  },
]

/** 商品表格列 */
const productColumns: DataTableColumns<Product> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '商品名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '分类', key: 'category_name', width: 120, ellipsis: { tooltip: true } },
  { title: '售价', key: 'price', width: 100, render(row) { return `¥${row.price.toFixed(2)}` } },
  { title: '库存', key: 'stock', width: 80 },
  { title: '最低购买', key: 'min_buy', width: 90 },
  {
    title: '多规格',
    key: 'is_multi_spec',
    width: 80,
    render(row) {
      return h('span', row.is_multi_spec ? '是' : '否')
    },
  },
  {
    title: '状态',
    key: 'status',
    width: 80,
    render(row) {
      return h('span', row.status === 1 ? '上架' : '下架')
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render(row) {
      return h('div', { style: 'display: flex; gap: 12px; flex-wrap: wrap;' }, [
        h('a', { onClick: () => openProductModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑'),
        authStore.hasPermission('product:update')
          ? h('a', { onClick: () => toggleStatus(row), style: 'color: #07c160; cursor: pointer;' }, row.status === 1 ? '下架' : '上架')
          : null,
      ])
    },
  },
]

const showCategoryModal = ref(false)
const categoryModalLoading = ref(false)
const categoryForm = reactive<Partial<Category>>({
  id: undefined,
  name: '',
  sort: 0,
})

const showProductModal = ref(false)
const productModalLoading = ref(false)
const productForm = reactive<Partial<Product>>({
  id: undefined,
  name: '',
  category_id: undefined,
  price: undefined,
  original_price: undefined,
  stock: undefined,
  cover_image: '',
  unit: '',
  description: '',
  is_multi_spec: false,
  min_buy: 1,
  sort: 0,
})

/**
 * 加载分类列表
 */
async function loadCategories() {
  categoryLoading.value = true
  try {
    const res = await getCategoryList()
    categories.value = res.data.data
  } finally {
    categoryLoading.value = false
  }
}

/**
 * 加载商品列表
 */
async function loadProducts() {
  productLoading.value = true
  try {
    const res = await getProductList({
      page: page.value,
      page_size: pageSize,
      keyword: searchForm.keyword || undefined,
      category_id: searchForm.category_id ?? undefined,
      status: searchForm.status ?? undefined,
    })
    products.value = res.data.data.list
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    productLoading.value = false
  }
}

/**
 * 打开分类编辑弹窗
 * @param row 分类数据
 */
function openCategoryModal(row?: Category) {
  categoryForm.id = row?.id
  categoryForm.name = row?.name || ''
  categoryForm.sort = row?.sort || 0
  showCategoryModal.value = true
}

/**
 * 保存分类
 */
async function saveCategory() {
  if (!categoryForm.name) {
    message.warning('请输入分类名称')
    return
  }
  categoryModalLoading.value = true
  try {
    if (categoryForm.id) {
      await updateCategory(categoryForm.id, categoryForm)
    } else {
      await createCategory(categoryForm)
    }
    message.success('保存成功')
    showCategoryModal.value = false
    loadCategories()
  } finally {
    categoryModalLoading.value = false
  }
}

/**
 * 打开商品编辑弹窗
 * @param row 商品数据
 */
function openProductModal(row?: Product) {
  productForm.id = row?.id
  productForm.name = row?.name || ''
  productForm.category_id = row?.category_id
  productForm.cover_image = row?.cover_image || ''
  productForm.price = row?.price
  productForm.original_price = row?.original_price
  productForm.stock = row?.stock
  productForm.unit = row?.unit || ''
  productForm.description = row?.description || ''
  productForm.is_multi_spec = row?.is_multi_spec ?? false
  productForm.min_buy = row?.min_buy ?? 1
  productForm.sort = row?.sort ?? 0
  showProductModal.value = true
}

/**
 * 保存商品（快速创建/编辑，固定单规格）
 * 多规格商品的规格编辑请使用商品编辑页
 */
async function saveProduct() {
  if (!productForm.name || !productForm.category_id || productForm.price === undefined) {
    message.warning('请填写完整商品信息')
    return
  }
  if (productForm.stock === undefined || productForm.stock < 0) {
    message.warning('请填写库存')
    return
  }
  productModalLoading.value = true
  try {
    const payload: ProductSavePayload = {
      name: productForm.name,
      category_id: productForm.category_id,
      cover_image: productForm.cover_image ?? '',
      images: [],
      unit: productForm.unit ?? '',
      description: productForm.description ?? '',
      is_multi_spec: false,
      min_buy: productForm.min_buy ?? 1,
      sort: productForm.sort ?? 0,
      specs: [],
      sku_items: [
        {
          id: 0,
          spec_value_ids: [],
          sku_code: '',
          price: productForm.price,
          cost_price: 0,
          stock: productForm.stock,
          safety_stock: 0,
          image: '',
        },
      ],
    }
    if (productForm.id) {
      await updateProduct(productForm.id, payload)
    } else {
      await createProduct(payload)
    }
    message.success('保存成功')
    showProductModal.value = false
    loadProducts()
  } finally {
    productModalLoading.value = false
  }
}

/**
 * 切换商品上下架状态
 * @param row 商品数据
 */
async function toggleStatus(row: Product) {
  const status = row.status === 1 ? 0 : 1
  await updateProductStatus(row.id, status)
  message.success(status === 1 ? '已上架' : '已下架')
  loadProducts()
}

onMounted(() => {
  loadCategories()
  loadProducts()
})
</script>

<style lang="scss" scoped>
.product-page {
  .product-card {
    margin-top: 16px;
  }
}
</style>
