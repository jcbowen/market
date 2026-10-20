<template>
  <n-modal
    :show="show"
    :title="title"
    preset="card"
    style="width: 760px; max-width: 92vw"
    @update:show="emit('update:show', $event)"
  >
    <!-- 已选范围标签（分类与商品分区回显，可移除） -->
    <div class="picked-area">
      <div class="picked-row">
        <span class="label">已选分类（{{ pickedCategories.length }}）：</span>
        <template v-if="pickedCategories.length">
          <n-tag
            v-for="c in pickedCategories"
            :key="`c-${c.id}`"
            size="small"
            closable
            type="info"
            @close="removePicked(c)"
          >
            {{ c.name }}（全部商品）
          </n-tag>
        </template>
        <span v-else class="none">无</span>
      </div>
      <div class="picked-row">
        <span class="label">已选商品（{{ pickedProducts.length }}）：</span>
        <template v-if="pickedProducts.length">
          <n-tag
            v-for="p in pickedProducts"
            :key="`p-${p.id}`"
            size="small"
            closable
            type="primary"
            @close="removePicked(p)"
          >
            {{ p.name }}
          </n-tag>
        </template>
        <span v-else class="none">无</span>
      </div>
      <div class="hint">选分类 = 该分类下全部商品自动纳入；也可精确到具体商品。两者可叠加。</div>
    </div>

    <n-spin :show="loading">
      <div class="category-bar">
        <span class="label">按分类选择：</span>
        <div class="category-tags">
          <n-tag
            v-for="c in categories"
            :key="c.id"
            size="medium"
            checkable
            :checked="isCategoryPicked(c.id)"
            @update:checked="() => onToggleCategory(c)"
          >
            {{ c.name }}
          </n-tag>
        </div>
      </div>

      <div class="toolbar">
        <n-select
          v-model:value="filterCategoryId"
          :options="categoryOptions"
          placeholder="全部分类"
          clearable
          class="category-filter"
        />
        <n-input
          v-model:value="keyword"
          placeholder="按商品名称搜索"
          clearable
          class="keyword"
        >
          <template #prefix>
            <n-icon><SearchOutline /></n-icon>
          </template>
        </n-input>
      </div>

      <n-empty v-if="!list.length && !loading" description="暂无商品" class="empty" />
      <div v-else class="product-list">
        <div
          v-for="p in list"
          :key="p.id"
          class="product-row"
          :class="{ selected: isProductPicked(p.id) }"
          @click="onToggleProduct(p)"
        >
          <n-checkbox :checked="isProductPicked(p.id)" @click.stop @update:checked="() => onToggleProduct(p)" />
          <img v-if="p.cover_image" :src="p.cover_image" class="cover" loading="lazy" />
          <div v-else class="cover placeholder">无图</div>
          <div class="info">
            <span class="name">{{ p.name }}</span>
            <span class="meta">{{ p.category_name }} · ¥{{ formatPrice(p.price) }}</span>
          </div>
          <n-tag v-if="p.status !== 1" size="small" type="warning">已下架</n-tag>
        </div>
      </div>
    </n-spin>

    <div class="footer">
      <span class="picked-count">共选 {{ pickedCategories.length + pickedProducts.length }} 项</span>
      <n-pagination
        v-model:page="page"
        :item-count="total"
        :page-size="pageSize"
        @update:page="loadList"
      />
      <n-button type="primary" @click="onConfirm">确认选择</n-button>
    </div>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { SearchOutline } from '@vicons/ionicons5'
import { getProductList, getCategoryList } from '@/api/product'
import type { Product, Category } from '@/api/types'

/**
 * 商品/分类范围选择弹窗组件（公共组件，免配送费活动 / 优惠券商品范围共用）
 * 两类选择粒度：
 *  - 分类：一级平铺标签（数据源 getCategoryList），勾选即代表该分类下全部商品纳入范围；
 *  - 商品：分页 + 模糊搜索（数据源 getProductList，支持分类下拉过滤），逐个勾选。
 * 选择结果以 ScopeItem[] 回传（type 区分 category/product），包含/排除语义由父级表单决定
 * （同一组件可在「适用范围」「排除范围」两个入口分别打开）。
 */

/** 单个范围项：分类（含全部商品）或具体商品 */
export interface ScopeItem {
  type: 'category' | 'product'
  id: number
  name: string
}

const props = withDefaults(
  defineProps<{
    /** 弹窗显示（v-model:show） */
    show: boolean
    /** 弹窗标题（父级按「适用/排除」语义传入） */
    title?: string
    /** 打开时的已选范围项 */
    selected?: ScopeItem[]
  }>(),
  {
    title: '选择商品范围',
    selected: () => [],
  }
)

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 确认回传全部已选范围项 */
  (e: 'confirm', items: ScopeItem[]): void
}>()

const PAGE_SIZE = 8

const categories = ref<Category[]>([])
const list = ref<Product[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = PAGE_SIZE
const loading = ref(false)
const keyword = ref('')
const filterCategoryId = ref<number | null>(null)

/** 已选分类（含全部商品语义） */
const pickedCategories = ref<ScopeItem[]>([])
/** 已选具体商品 */
const pickedProducts = ref<ScopeItem[]>([])

let keywordTimer: ReturnType<typeof setTimeout> | null = null

/**
 * 请求序号：防抖搜索与分类筛选交错时，只采用最后一次请求的响应，
 * 否则慢响应会后到并覆盖新结果，列表显示与筛选条件不一致
 */
let loadSeq = 0

onUnmounted(() => {
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = null
})

const categoryOptions = computed(() =>
  categories.value.map((c) => ({ label: c.name, value: c.id }))
)

watch(
  () => props.show,
  (v) => {
    if (v) init()
  }
)

watch(keyword, () => {
  // 关键字输入 300ms 防抖后自动搜索
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => {
    keywordTimer = null
    reload()
  }, 300)
})

watch(filterCategoryId, () => {
  reload()
})

/**
 * 打开弹窗时的初始化：带入已选项、加载分类与商品列表
 */
function init() {
  const selected = props.selected || []
  pickedCategories.value = selected.filter((s) => s.type === 'category')
  pickedProducts.value = selected.filter((s) => s.type === 'product')
  page.value = 1
  keyword.value = ''
  filterCategoryId.value = null
  void loadCategories()
  void loadList()
}

/**
 * 重置到第一页并加载商品列表
 */
function reload() {
  page.value = 1
  void loadList()
}

/**
 * 加载一级分类列表
 */
async function loadCategories() {
  try {
    const res = await getCategoryList()
    categories.value = res.data.data || []
  } catch {
    categories.value = []
  }
}

/**
 * 加载商品分页列表（关键字 + 分类过滤）
 */
async function loadList() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const params: Parameters<typeof getProductList>[0] = { page: page.value, page_size: pageSize }
    if (keyword.value.trim()) {
      params.keyword = keyword.value.trim()
    }
    if (filterCategoryId.value != null) {
      params.category_id = filterCategoryId.value
    }
    const res = await getProductList(params)
    if (seq !== loadSeq) return
    // proto3 omitempty：空列表时 total/list 字段可能缺省，需兜底
    list.value = res.data.data.list || []
    total.value = res.data.data.total || 0
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

/**
 * 分类是否已选
 */
function isCategoryPicked(id: number): boolean {
  return pickedCategories.value.some((c) => c.id === id)
}

/**
 * 商品是否已选
 */
function isProductPicked(id: number): boolean {
  return pickedProducts.value.some((p) => p.id === id)
}

/**
 * 切换分类选中态（跨弹窗留存）
 */
function onToggleCategory(c: Category) {
  if (isCategoryPicked(c.id)) {
    pickedCategories.value = pickedCategories.value.filter((x) => x.id !== c.id)
  } else {
    pickedCategories.value = [...pickedCategories.value, { type: 'category', id: c.id, name: c.name }]
  }
}

/**
 * 切换商品选中态（跨页留存）
 */
function onToggleProduct(p: Product) {
  if (isProductPicked(p.id)) {
    pickedProducts.value = pickedProducts.value.filter((x) => x.id !== p.id)
  } else {
    pickedProducts.value = [...pickedProducts.value, { type: 'product', id: p.id, name: p.name }]
  }
}

/**
 * 移除已选标签（分类/商品通用）
 */
function removePicked(item: ScopeItem) {
  if (item.type === 'category') {
    pickedCategories.value = pickedCategories.value.filter((x) => x.id !== item.id)
  } else {
    pickedProducts.value = pickedProducts.value.filter((x) => x.id !== item.id)
  }
}

/**
 * 确认：分类 + 商品合并回传（分类在前）
 */
function onConfirm() {
  emit('confirm', [...pickedCategories.value, ...pickedProducts.value])
  emit('update:show', false)
}

/**
 * 金额格式化（后端金额单位为元，decimal(10,2)）
 */
function formatPrice(price: number): string {
  return Number(price ?? 0).toFixed(2)
}
</script>

<style lang="scss" scoped>
.picked-area {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  margin-bottom: 12px;
  background: #f7f9fa;
  border-radius: 6px;

  .picked-row {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;

    .label {
      font-size: 13px;
      color: #666;
      flex-shrink: 0;
    }

    .none {
      font-size: 13px;
      color: #bbb;
    }
  }

  .hint {
    font-size: 12px;
    color: #999;
  }
}

.category-bar {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  margin-bottom: 12px;

  .label {
    font-size: 13px;
    color: #666;
    line-height: 28px;
    flex-shrink: 0;
  }

  .category-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
}

.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 12px;

  .category-filter {
    width: 160px;
  }

  .keyword {
    flex: 1;
  }
}

.empty {
  padding: 40px 0;
}

.product-list {
  max-height: 360px;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
}

.product-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  border-bottom: 1px solid #f3f3f7;
  cursor: pointer;
  transition: background 0.15s;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: #f7f9fa;
  }

  &.selected {
    background: rgba(24, 160, 88, 0.06);
  }

  .cover {
    width: 40px;
    height: 40px;
    border-radius: 4px;
    object-fit: cover;
    flex-shrink: 0;

    &.placeholder {
      display: flex;
      align-items: center;
      justify-content: center;
      background: #f0f2f5;
      font-size: 11px;
      color: #bbb;
    }
  }

  .info {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;

    .name {
      font-size: 14px;
      color: #333;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .meta {
      font-size: 12px;
      color: #999;
    }
  }
}

.footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 14px;

  .picked-count {
    margin-right: auto;
    font-size: 13px;
    color: #18a058;
  }
}
</style>
