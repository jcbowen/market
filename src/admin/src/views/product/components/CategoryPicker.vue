<template>
  <div class="category-picker" @click="open">
    <n-input
      :value="selectedName"
      readonly
      :placeholder="placeholder"
      class="category-picker__trigger"
    >
      <template #suffix>
        <n-icon :component="ChevronDownOutline" />
      </template>
    </n-input>

    <n-modal v-model:show="showModal" preset="card" title="选择商品分类" class="category-picker__modal">
      <n-spin :show="loading">
        <n-empty v-if="!loading && list.length === 0" description="暂无分类，先新建一个吧" />
        <div v-else class="category-picker__list">
          <div
            v-for="item in list"
            :key="item.id"
            class="category-picker__item"
            :class="{ 'category-picker__item--active': item.id === modelValue }"
            @click="select(item)"
          >
            <span>{{ item.name }}</span>
            <n-icon v-if="item.id === modelValue" :component="CheckmarkOutline" color="#18a058" />
          </div>
        </div>
      </n-spin>

      <n-divider style="margin: 12px 0 8px" />
      <div class="category-picker__create">
        <n-input
          v-model:value="newName"
          placeholder="新分类名称"
          maxlength="20"
          @keyup.enter="createNew"
        />
        <n-button type="primary" :loading="creating" :disabled="!newName.trim()" @click="createNew">
          新建分类
        </n-button>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
// 组件名与 keep-alive 无关（弹窗组件不被路由缓存），按语义命名便于调试
defineOptions({ name: 'CategoryPicker' })
import { ref, computed, watch } from 'vue'
import { NInput, NModal, NButton, NIcon, NSpin, NEmpty, NDivider, createDiscreteApi } from 'naive-ui'
import { ChevronDownOutline, CheckmarkOutline } from '@vicons/ionicons5'
import { getCategoryList, createCategory } from '@/api/product'
import type { Category } from '@/api/types'

/**
 * CategoryPicker 商品分类选择器
 *
 * 需求场景：新增/编辑商品时若无分类或分类不够用，可直接在选择器弹窗内
 * 选择已有分类或快速新建（选择与新增闭环在同一弹窗内完成）。
 * 仅展示启用分类（status=1），禁用分类仍需到分类管理页操作。
 *
 * v-model（modelValue）：已选分类 ID，null 表示未选择
 */
const { message } = createDiscreteApi(['message'])

const props = withDefaults(
  defineProps<{
    /** 已选分类 ID（v-model；null 表示未选择） */
    modelValue?: number | null
    /** 未选择时的占位文案 */
    placeholder?: string
  }>(),
  { modelValue: null, placeholder: '请选择商品分类' },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: number): void
}>()

const showModal = ref(false)
const loading = ref(false)
const list = ref<Category[]>([])
const newName = ref('')
const creating = ref(false)

/** 已选分类名称（触发框回显） */
const selectedName = computed(() => list.value.find((c) => c.id === props.modelValue)?.name ?? '')

/**
 * 打开弹窗并加载启用分类
 */
function open() {
  showModal.value = true
  loadList()
}

/**
 * 拉取分类列表（仅启用项；管理端接口含禁用分类，此处过滤）
 */
async function loadList() {
  loading.value = true
  try {
    const res = await getCategoryList()
    if (res.data.code === 0) {
      list.value = (res.data.data ?? []).filter((c) => c.status === 1)
    }
  } finally {
    loading.value = false
  }
}

/**
 * 选中分类：回填 ID 并关闭弹窗
 *
 * @param item 分类项
 */
function select(item: Category) {
  emit('update:modelValue', item.id)
  showModal.value = false
}

/**
 * 快速新建分类：成功后刷新列表并自动选中
 */
async function createNew() {
  const name = newName.value.trim()
  if (!name) return
  creating.value = true
  try {
    const res = await createCategory({ name, parent_id: 0, sort: 0, status: 1 })
    if (res.data.code === 0) {
      const created = res.data.data
      message.success(`分类「${name}」已创建`)
      newName.value = ''
      await loadList()
      // 后端返回完整分类对象时直接选中；否则按名称匹配兜底
      const target = created?.id ?? list.value.find((c) => c.name === name)?.id
      if (target) emit('update:modelValue', target)
    }
  } finally {
    creating.value = false
  }
}

// 弹窗每次打开清空上次的输入草稿
watch(showModal, (v) => {
  if (!v) newName.value = ''
})
</script>

<style scoped>
.category-picker {
  cursor: pointer;
}

.category-picker__trigger :deep(input) {
  cursor: pointer;
}

.category-picker__list {
  max-height: 320px;
  overflow-y: auto;
}

.category-picker__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 4px;
  cursor: pointer;
}

.category-picker__item:hover {
  background-color: var(--n-color-hover, #f3f3f5);
}

.category-picker__item--active {
  background-color: rgba(24, 160, 88, 0.1);
  color: #18a058;
  font-weight: 500;
}

.category-picker__create {
  display: flex;
  gap: 8px;
}
</style>
