<template>
  <n-card title="盘点管理">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/stock/check/create')">新建盘点</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockCheck' })
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getInventoryCheckList } from '@/api/inventory'
import type { InventoryCheck } from '@/api/types'

/**
 * 盘点管理页面
 * 展示盘点单列表并提供新建与详情入口
 */
const router = useRouter()
const list = ref<InventoryCheck[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<InventoryCheck> = [
  { title: '盘点单号', key: 'check_no' },
  { title: '品项数', key: 'total_items' },
  { title: '差异数', key: 'diff_items' },
  { title: '状态', key: 'status' },
  { title: '备注', key: 'remark' },
  { title: '创建时间', key: 'created_at' },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NButton, { size: 'small', onClick: () => router.push(`/stock/check/${row.id}`) }, { default: () => '详情' })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getInventoryCheckList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

onMounted(loadData)
</script>
