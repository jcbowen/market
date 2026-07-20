<template>
  <n-card title="秒杀活动">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/flash-sales/create')">创建秒杀</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getSeckillList, deleteSeckill } from '@/api/marketing'
import type { Seckill } from '@/api/types'

/**
 * 秒杀活动管理页面
 */
const router = useRouter()
const list = ref<Seckill[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<Seckill> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '活动名称', key: 'product_name' },
  { title: '秒杀价', key: 'seckill_price' },
  { title: '库存', key: 'stock' },
  { title: '开始时间', key: 'start_time' },
  { title: '结束时间', key: 'end_time' },
  {
    title: '状态',
    key: 'status',
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '启用' : '禁用') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getSeckillList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

async function remove(row: Seckill) {
  const res = await deleteSeckill(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
