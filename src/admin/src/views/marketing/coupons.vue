<template>
  <n-card title="优惠券管理">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/coupons/create')">创建优惠券</n-button>
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
import { getCouponList, deleteCoupon } from '@/api/marketing'
import type { Coupon } from '@/api/types'

/**
 * 优惠券管理页面
 * 展示优惠券列表并支持创建、删除
 */
const router = useRouter()
const list = ref<Coupon[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const typeMap: Record<number, string> = {
  1: '满减券',
  2: '折扣券',
}

const columns: DataTableColumns<Coupon> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '名称', key: 'name' },
  { title: '类型', key: 'type', render(row) { return typeMap[row.type] || '未知' } },
  { title: '门槛', key: 'threshold' },
  { title: '面值/折扣', key: 'discount_value' },
  { title: '总量', key: 'total_count' },
  { title: '已领取', key: 'received_count' },
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
  const res = await getCouponList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

async function remove(row: Coupon) {
  const res = await deleteCoupon(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
