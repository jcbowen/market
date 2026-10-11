<template>
  <n-card title="分类管理">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="form.name" placeholder="分类名称" />
        <n-input-number v-model:value="form.sort" placeholder="排序" />
        <n-button type="primary" @click="submit">{{ isEdit ? '更新' : '新增' }}</n-button>
        <n-button v-if="isEdit" @click="reset">取消</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'ProductCategories' })
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCategoryList, createCategory, updateCategory, deleteCategory } from '@/api/product'
import type { Category } from '@/api/types'

/**
 * 商品分类管理页面
 * 支持分类的增删改查，当前按一级分类管理
 */
const list = ref<Category[]>([])
const loading = ref(false)
const isEdit = ref(false)
const form = ref<Partial<Category>>({ name: '', sort: 0, parent_id: 0 })

const columns: DataTableColumns<Category> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '分类名称', key: 'name' },
  { title: '排序', key: 'sort', width: 100 },
  { title: '状态', key: 'status' },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getCategoryList()
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data
  }
}

function edit(row: Category) {
  isEdit.value = true
  form.value = { ...row }
}

function reset() {
  isEdit.value = false
  form.value = { name: '', sort: 0, parent_id: 0 }
}

async function submit() {
  const data = { ...form.value } as Category
  const res = isEdit.value ? await updateCategory(data.id, data) : await createCategory(data)
  if (res.data.code === 0) {
    reset()
    loadData()
  }
}

async function remove(row: Category) {
  const res = await deleteCategory(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
