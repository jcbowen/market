<template>
  <n-card title="社区管理">
    <n-space vertical>
      <n-space align="center">
        <n-button type="primary" @click="router.push('/system/communities/create')">新增社区</n-button>
        <n-select
          v-model:value="statusFilter"
          :options="statusFilterOptions"
          style="width: 140px"
          @update:value="handleFilterChange"
        />
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'CommunityList' })
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCommunityList, deleteCommunity } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 社区管理页面
 * 展示社区列表并支持新增、编辑、删除；支持按状态筛选（含停业社区）
 */
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const list = ref<Community[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

// 状态筛选：-1 全部，0 停业，1 营业
const statusFilter = ref(-1)
const statusFilterOptions = [
  { label: '全部状态', value: -1 },
  { label: '营业中', value: 1 },
  { label: '已停业', value: 0 },
]

const statusText: Record<number, string> = { 0: '停业', 1: '营业' }

const columns: DataTableColumns<Community> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '社区名称', key: 'name' },
  { title: '地址', key: 'address' },
  { title: '负责人', key: 'manager_name' },
  { title: '联系电话', key: 'manager_phone' },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(
        NTag,
        { type: row.status === 1 ? 'success' : 'default', size: 'small' },
        { default: () => statusText[row.status] ?? String(row.status) }
      )
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => router.push(`/system/communities/edit/${row.id}`) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'primary', secondary: true, onClick: () => router.push({ path: '/system/community-permissions', query: { community_id: row.id } }) }, { default: () => '权限分配' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

function handleFilterChange() {
  pagination.value.page = 1
  loadData()
}

async function loadData() {
  loading.value = true
  try {
    const res = await getCommunityList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      status: statusFilter.value,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    loading.value = false
  }
}

function remove(row: Community) {
  dialog.warning({
    title: '删除确认',
    content: `确定删除社区「${row.name}」吗？删除后不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        const res = await deleteCommunity(row.id)
        if (res.data.code === 0) {
          message.success('删除成功')
          loadData()
        }
      } catch {
        // 错误提示由响应拦截器统一弹出
      }
    },
  })
}

onMounted(loadData)
</script>
