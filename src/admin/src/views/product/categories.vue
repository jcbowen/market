<template>
  <n-card title="分类管理">
    <n-space vertical :size="12">
      <n-space align="center" justify="space-between">
        <n-text depth="3">按住行首图标拖拽可调整顺序，拖完自动保存；新增/编辑在弹窗中进行</n-text>
        <n-button type="primary" @click="openCreate">新增分类</n-button>
      </n-space>

      <table class="cat-table">
        <thead>
          <tr>
            <th class="cat-table__drag-col"></th>
            <th class="cat-table__id-col">ID</th>
            <th>分类名称</th>
            <th class="cat-table__sort-col">排序值</th>
            <th class="cat-table__status-col">状态</th>
            <th class="cat-table__action-col">操作</th>
          </tr>
        </thead>
        <draggable
          v-model="list"
          tag="tbody"
          item-key="id"
          handle=".drag-handle"
          animation="150"
          :disabled="saving"
          @end="onDragEnd"
        >
          <template #item="{ element }">
            <tr :class="{ 'cat-table__row--disabled': element.status !== 1 }">
              <td class="cat-table__drag-col">
                <n-icon class="drag-handle" :component="ReorderTwoOutline" :size="18" />
              </td>
              <td class="cat-table__id-col">{{ element.id }}</td>
              <td>
                <n-space align="center" :size="8">
                  <span>{{ element.name }}</span>
                  <n-tag v-if="element.status !== 1" size="small" type="warning">已禁用</n-tag>
                </n-space>
              </td>
              <td class="cat-table__sort-col">{{ element.sort }}</td>
              <td class="cat-table__status-col">
                <n-switch
                  size="small"
                  :value="element.status === 1"
                  :loading="element.__switching"
                  @update:value="(v: boolean) => toggleStatus(element, v)"
                />
              </td>
              <td class="cat-table__action-col">
                <n-space :size="6">
                  <n-button size="small" @click="openEdit(element)">编辑</n-button>
                  <n-button size="small" type="error" @click="remove(element)">删除</n-button>
                </n-space>
              </td>
            </tr>
          </template>
        </draggable>
      </table>
    </n-space>

    <!-- 新增/编辑弹窗：仅名称（排序交给拖拽，状态交给列表开关） -->
    <n-modal v-model:show="showForm" preset="card" :title="editing ? '编辑分类' : '新增分类'" style="max-width: 380px">
      <n-space vertical :size="12">
        <n-input v-model:value="form.name" placeholder="分类名称" maxlength="20" @keyup.enter="submit" />
        <n-space justify="end" :size="8">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="submitting" :disabled="!form.name.trim()" @click="submit">
            {{ editing ? '保存' : '创建' }}
          </n-button>
        </n-space>
      </n-space>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'ProductCategories' })
import { ref, onMounted } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { NButton, NIcon, NModal, NSpace, NSwitch, NTag, NText, createDiscreteApi } from 'naive-ui'
import { ReorderTwoOutline } from '@vicons/ionicons5'
import draggable from 'vuedraggable'
import { getCategoryList, createCategory, updateCategory, deleteCategory, batchSortCategories } from '@/api/product'
import type { Category } from '@/api/types'

/**
 * 商品分类管理页面
 * - 排序：列表内拖拽把手整表拖动，落点即提交批量排序接口（去掉了旧的排序输入框）
 * - 状态：行内开关直达启用/禁用（显式传 status，修复后端 0 值无法禁用的问题）
 * - 新增/编辑：弹窗内仅填名称
 */

// 行级开关 loading 标记（不进 Category 类型，仅渲染辅助）
type CategoryRow = Category & { __switching?: boolean }

const { message, dialog } = createDiscreteApi(['message', 'dialog'])

const list = ref<CategoryRow[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const submitting = ref(false)
const editing = ref<CategoryRow | null>(null)
const form = ref<{ name: string }>({ name: '' })

/**
 * 加载分类列表（管理端接口含禁用分类，禁用行灰显可再启用）
 */
async function loadData() {
  loading.value = true
  try {
    const res = await getCategoryList()
    if (res.data.code === 0) {
      list.value = res.data.data ?? []
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
  }
}

/**
 * 打开新增弹窗
 */
function openCreate() {
  editing.value = null
  form.value = { name: '' }
  showForm.value = true
}

/**
 * 打开编辑弹窗
 *
 * @param row 目标分类
 */
function openEdit(row: CategoryRow) {
  editing.value = row
  form.value = { name: row.name }
  showForm.value = true
}

/**
 * 提交新增/编辑（全量提交当前行字段，避免编辑覆盖丢字段）
 */
async function submit() {
  const name = form.value.name.trim()
  if (!name) return
  submitting.value = true
  try {
    let res
    if (editing.value) {
      // 编辑：带上 icon/sort/status 原值，仅改名称
      res = await updateCategory(editing.value.id, {
        name,
        icon: editing.value.icon ?? '',
        sort: editing.value.sort,
        status: editing.value.status,
        parent_id: editing.value.parent_id,
      })
    } else {
      res = await createCategory({ name, parent_id: 0, sort: (list.value.length + 1) * 10, status: 1 })
    }
    if (res.data.code === 0) {
      message.success(editing.value ? '分类已更新' : '分类已创建')
      showForm.value = false
      loadData()
    }
  } finally {
    submitting.value = false
  }
}

/**
 * 行内切换启用/禁用（显式传 status；失败回滚开关由 loadData 恢复）
 *
 * @param row 目标分类
 * @param enabled 目标状态（true=启用）
 */
async function toggleStatus(row: CategoryRow, enabled: boolean) {
  row.__switching = true
  try {
    const res = await updateCategory(row.id, {
      name: row.name,
      icon: row.icon ?? '',
      sort: row.sort,
      status: enabled ? 1 : 0,
      parent_id: row.parent_id,
    })
    if (res.data.code === 0) {
      row.status = enabled ? 1 : 0
      message.success(enabled ? `分类「${row.name}」已启用` : `分类「${row.name}」已禁用`)
    }
  } finally {
    row.__switching = false
  }
}

/**
 * 拖拽结束：按新顺序整表提交 sort（显示顺序 ×10 留空隙），失败回滚为服务端顺序
 */
async function onDragEnd() {
  const ordered = list.value.map((row, index) => ({ id: row.id, sort: (index + 1) * 10 }))
  if (ordered.length === 0) return
  saving.value = true
  try {
    const res = await batchSortCategories(ordered)
    if (res.data.code === 0) {
      message.success('排序已保存')
    } else {
      // 提交失败回滚为服务端数据
      loadData()
    }
  } catch {
    loadData()
  } finally {
    saving.value = false
  }
}

/**
 * 删除分类（有确认）
 *
 * @param row 目标分类
 */
async function remove(row: CategoryRow) {
  dialog.warning({
    title: '删除确认',
    content: `确定删除分类「${row.name}」吗？删除后不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await deleteCategory(row.id)
      if (res.data.code === 0) {
        message.success('分类已删除')
        loadData()
      }
    },
  })
}

onMounted(loadData)
// keep-alive 缓存下切回本页不重新挂载，需在激活时刷新，避免展示离开前的旧数据
refreshOnActivated(loadData)
</script>

<style scoped>
.cat-table {
  width: 100%;
  border-collapse: collapse;
  background-color: #fff;
}

.cat-table th,
.cat-table td {
  padding: 10px 12px;
  border-bottom: 1px solid #efeff5;
  text-align: left;
  font-weight: normal;
}

.cat-table th {
  background-color: #fafafc;
  color: #666;
  font-size: 13px;
}

.cat-table tbody tr:hover {
  background-color: #f9f9fb;
}

.cat-table__row--disabled td {
  color: #b0b0b0;
}

.cat-table__drag-col {
  width: 44px;
  text-align: center;
}

.cat-table__id-col {
  width: 72px;
}

.cat-table__sort-col {
  width: 88px;
}

.cat-table__status-col {
  width: 88px;
}

.cat-table__action-col {
  width: 148px;
}

.drag-handle {
  cursor: grab;
  color: #999;
}

.drag-handle:active {
  cursor: grabbing;
}
</style>
