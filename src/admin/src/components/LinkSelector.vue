<template>
  <div class="link-selector">
    <n-input :value="value" :placeholder="placeholder" clearable @update:value="onInput">
      <template #suffix>
        <n-button size="tiny" secondary @click="openModal">选择链接</n-button>
      </template>
    </n-input>
    <p class="hint">外部链接直接输入 http(s):// 地址；内部跳转点击「选择链接」</p>

    <n-modal
      v-model:show="showModal"
      preset="card"
      title="选择跳转链接"
      style="width: 680px; max-width: 90vw"
    >
      <n-tabs v-model:value="activeTab" type="line" animated>
        <n-tab-pane name="page" tab="页面">
          <n-data-table
            size="small"
            :columns="linkColumns"
            :data="PAGE_OPTIONS"
            :row-props="rowClickProps"
          />
        </n-tab-pane>
        <n-tab-pane name="product" tab="商品">
          <n-space vertical>
            <n-space wrap>
              <n-input
                v-model:value="productKeyword"
                placeholder="商品名称"
                clearable
                style="width: 180px"
                @keyup.enter="searchProducts"
              />
              <n-select
                v-model:value="productCategoryId"
                placeholder="全部分类"
                clearable
                filterable
                :options="categoryOptions"
                style="width: 160px"
              />
              <n-button type="primary" @click="searchProducts">查询</n-button>
            </n-space>
            <n-data-table
              size="small"
              remote
              :columns="productColumns"
              :data="products"
              :loading="productLoading"
              :pagination="productPagination"
              :row-props="rowClickProps"
            />
          </n-space>
        </n-tab-pane>
        <n-tab-pane name="activity" tab="活动">
          <n-data-table
            size="small"
            :columns="linkColumns"
            :data="ACTIVITY_OPTIONS"
            :row-props="rowClickProps"
          />
        </n-tab-pane>
      </n-tabs>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { NButton, NTag, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCategoryList, getProductList } from '@/api/product'
import type { Category, PageResult, Product } from '@/api/types'

/**
 * 跳转链接选择器通用组件（单链接模式）
 * input 支持手动输入外部链接，点击「选择链接」按钮弹出选择弹窗；
 * 弹窗内通过 Tab 切换三类内部跳转目标：页面（C 端可跳转页面静态列表）、
 * 商品（支持关键词/分类筛选的分页商品列表）、活动（活动页面列表）；
 * 点击任一目标即生成跳转地址回显到 input 并关闭弹窗
 */

/** 内部跳转目标选项（页面/活动共用结构） */
interface LinkOption {
  /** 页面名称 */
  name: string
  /** 页面路径（以 / 开头，含分包前缀，可直接用于 C 端跳转） */
  path: string
  /** 是否 tabBar 页面（C 端以 switchTab 方式跳转） */
  isTab?: boolean
}

/** C 端可跳转页面静态列表（与 src/client/src/pages.json 的主包+subPackages 保持同步） */
const PAGE_OPTIONS: LinkOption[] = [
  // 首页与分类已合并为同一页面，路径以 pages.json 为准
  { name: '首页/分类', path: '/pages/category/category', isTab: true },
  { name: '购物车', path: '/pages/cart/cart', isTab: true },
  { name: '我的', path: '/pages/user/user', isTab: true },
  { name: '订单列表', path: '/pagesOrder/order/list' },
  { name: '优惠券', path: '/pagesMine/coupon/coupon' },
  { name: '收货地址', path: '/pagesMine/address/list' },
  { name: '选择社区', path: '/pages/community/select' },
]

/** C 端活动页面静态列表（与 src/client/src/pages.json 的 subPackages 保持同步） */
const ACTIVITY_OPTIONS: LinkOption[] = [
  { name: '限时秒杀', path: '/pagesGoods/flash/flash' },
  { name: '满减活动', path: '/pagesGoods/full-reduction/full-reduction' },
  { name: '优惠券领取', path: '/pagesMine/coupon/coupon' },
]

const props = withDefaults(
  defineProps<{
    /** 跳转链接（v-model:value） */
    value?: string | null
    /** 占位提示文案 */
    placeholder?: string
  }>(),
  {
    value: '',
    placeholder: '请输入或选择跳转链接',
  }
)

const emit = defineEmits<{
  (e: 'update:value', url: string): void
}>()

const message = useMessage()
const showModal = ref(false)
const activeTab = ref<'page' | 'product' | 'activity'>('page')

// 商品 Tab 状态
const products = ref<Product[]>([])
const productLoading = ref(false)
const productKeyword = ref('')
const productCategoryId = ref<number | null>(null)
const categoryOptions = ref<{ label: string; value: number }[]>([])
const productPage = ref(1)
const productPageSize = 10
const productTotal = ref(0)

/** 商品分页配置（n-data-table remote 模式） */
const productPagination = reactive({
  page: 1,
  pageSize: productPageSize,
  itemCount: 0,
  onChange: (page: number) => {
    productPage.value = page
    loadProducts()
  },
})

/** 页面/活动 Tab 的表格列：名称、路径、跳转方式 */
const linkColumns: DataTableColumns<LinkOption> = [
  { title: '名称', key: 'name', width: 140 },
  { title: '路径', key: 'path', ellipsis: { tooltip: true } },
  {
    title: '跳转方式',
    key: 'isTab',
    width: 100,
    render(row) {
      return h(NTag, { size: 'small', type: row.isTab ? 'info' : 'default' }, { default: () => (row.isTab ? 'tab 页' : '普通页') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h(
        NButton,
        { size: 'small', type: 'primary', secondary: true, onClick: () => choose(row.path) },
        { default: () => '选择' }
      )
    },
  },
]

/** 商品 Tab 的表格列：名称、分类、价格、操作 */
const productColumns: DataTableColumns<Product> = [
  { title: '商品名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '分类', key: 'category_name', width: 120 },
  {
    title: '价格',
    key: 'price',
    width: 100,
    render(row) {
      return `¥${formatAmount(row.min_price ?? row.price)}`
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h(
        NButton,
        { size: 'small', type: 'primary', secondary: true, onClick: () => chooseProduct(row) },
        { default: () => '选择' }
      )
    },
  },
]

/**
 * 手动输入链接时同步到 v-model
 * @param url 输入的链接
 */
function onInput(url: string) {
  emit('update:value', url)
}

/**
 * 生成表格行点击属性：点击行即选中对应链接
 * @param row 行数据
 * @returns 传给 n-data-table row-props 的属性对象
 */
function rowClickProps(row: LinkOption | Product) {
  return {
    style: 'cursor: pointer',
    onClick: () => {
      if ('path' in row) {
        choose(row.path)
      } else {
        chooseProduct(row)
      }
    },
  }
}

/**
 * 打开选择弹窗，并预取商品 Tab 所需数据（分类、商品列表）
 */
function openModal() {
  showModal.value = true
  loadCategories()
  loadProducts()
}

/**
 * 选中页面/活动链接：回显路径并关闭弹窗
 * @param path 页面路径
 */
function choose(path: string) {
  emit('update:value', path)
  showModal.value = false
}

/**
 * 选中商品：回显商品详情链接并关闭弹窗
 * @param product 选中的商品
 */
function chooseProduct(product: Product) {
  choose(`/pagesGoods/product/detail?id=${product.id}`)
}

/**
 * 按当前筛选条件重置到第一页查询商品
 */
function searchProducts() {
  productPage.value = 1
  loadProducts()
}

/**
 * 加载商品列表（仅上架商品，支持关键词与分类筛选）
 * 加载失败时由 axios 拦截器统一提示，此处清空列表兜底
 */
async function loadProducts() {
  productLoading.value = true
  try {
    const res = await getProductList({
      page: productPage.value,
      page_size: productPageSize,
      keyword: productKeyword.value.trim() || undefined,
      category_id: productCategoryId.value ?? undefined,
      status: 1,
    })
    const result: PageResult<Product> | undefined = res.data.data
    products.value = result?.list || []
    productTotal.value = result?.total || 0
    productPagination.page = productPage.value
    productPagination.itemCount = productTotal.value
  } catch {
    products.value = []
  } finally {
    productLoading.value = false
  }
}

/**
 * 加载商品分类下拉选项
 * 加载失败时由 axios 拦截器统一提示，此处清空选项兜底
 */
async function loadCategories() {
  if (categoryOptions.value.length) return
  try {
    const res = await getCategoryList()
    const list: Category[] = res.data.data || []
    categoryOptions.value = list.map((item) => ({ label: item.name, value: item.id }))
  } catch {
    categoryOptions.value = []
  }
}

onMounted(() => {
  // 首次挂载即预取分类，减少打开弹窗时的等待
  loadCategories()
})
</script>

<style lang="scss" scoped>
/* 组件整体宽度行为与普通 n-input 一致（默认 100%，可由外部 style/class 设宽） */
.link-selector {
  width: 100%;
}

.hint {
  margin-top: 4px;
  font-size: 12px;
  color: #999;
}
</style>
