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

    <n-modal v-model:show="showModal" preset="card" title="选择商品分类" style="width: 360px; max-width: 90vw">
      <n-spin :show="loading">
        <n-empty v-if="!loading && selectableList.length === 0" description="暂无分类，先新建一个吧" />
        <div v-else class="category-picker__list">
          <div
            v-for="item in selectableList"
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
import { ref, computed, watch, onMounted } from 'vue'
import { NInput, NModal, NButton, NIcon, NSpin, NEmpty, NDivider, createDiscreteApi } from 'naive-ui'
import { ChevronDownOutline, CheckmarkOutline } from '@vicons/ionicons5'
import { getCategoryList, createCategory } from '@/api/product'
import type { Category } from '@/api/types'

/**
 * CategoryPicker 商品分类选择器
 *
 * 需求场景：新增/编辑商品时若无分类或分类不够用，可直接在选择器弹窗内
 * 选择已有分类或快速新建（选择与新增闭环在同一弹窗内完成）。
 * 弹窗仅展示启用分类（status=1），禁用分类仍需到分类管理页操作；
 * 触发框回显不限启用状态（含禁用分类），并有 fallbackName / 裸 ID 兜底。
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
    /** 列表未就绪或分类已删除时的回显兜底名（编辑态传商品接口的 category_name） */
    fallbackName?: string
  }>(),
  { modelValue: null, placeholder: '请选择商品分类', fallbackName: '' },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: number): void
}>()

const showModal = ref(false)
const loading = ref(false)
/** 全量分类（含禁用：已选分类即使被禁用也要参与回显匹配） */
const list = ref<Category[]>([])
const newName = ref('')
const creating = ref(false)

/** 弹窗可选分类（仅启用项；禁用分类仍需到分类管理页操作） */
const selectableList = computed(() => list.value.filter((c) => c.status === 1))

/**
 * 已选分类名称（触发框回显）
 * 三级兜底：列表匹配 → 外部传入的 category_name → 裸 ID 提示，避免编辑态显示空占位
 */
const selectedName = computed(() => {
  if (props.modelValue == null) return ''
  return (
    list.value.find((c) => c.id === props.modelValue)?.name ??
    props.fallbackName ??
    `分类 #${props.modelValue}`
  )
})

// 编辑态回填 modelValue 后列表尚未就绪会导致回显为空（点开弹窗才纠正），
// 挂载即静默预取一次，保证触发框直接显示分类名
onMounted(() => {
  if (props.modelValue != null) void loadList(true)
})

/**
 * 打开弹窗并加载启用分类
 */
function open() {
  showModal.value = true
  loadList()
}

/**
 * 拉取分类列表
 *
 * @param silent 静默模式：不切换 loading（用于挂载预取，避免弹窗外闪烁）
 */
async function loadList(silent = false) {
  if (!silent) loading.value = true
  try {
    const res = await getCategoryList()
    if (res.data.code === 0) {
      list.value = res.data.data ?? []
    }
  } finally {
    if (!silent) loading.value = false
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
