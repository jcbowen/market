<template>
  <n-card title="社区管理">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/system/communities/create')">新增社区</n-button>
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
import { NButton, NSpace } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCommunityList, deleteCommunity } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 社区管理页面
 * 展示社区列表并支持新增、编辑、删除
 */
const router = useRouter()
const list = ref<Community[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<Community> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '社区名称', key: 'name' },
  { title: '地址', key: 'address' },
  { title: '负责人', key: 'manager_name' },
  { title: '联系电话', key: 'manager_phone' },
  { title: '状态', key: 'status' },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => router.push(`/system/communities/edit/${row.id}`) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getCommunityList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

async function remove(row: Community) {
  const res = await deleteCommunity(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
