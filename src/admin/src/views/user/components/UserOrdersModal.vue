<template>
  <n-modal
    :show="show"
    title="会员订单"
    preset="card"
    style="width: 860px; max-width: 92vw"
    scrollable
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-space vertical>
      <n-space wrap align="center">
        <n-text>会员：{{ user?.nickname || user?.phone || '-' }}</n-text>
        <!-- 跨社区查询：仅超管可见，传 community_id=0 查该会员全部社区订单 -->
        <n-checkbox v-if="isSuperAdmin" v-model:checked="crossCommunity">跨社区查询（全部社区）</n-checkbox>
        <n-button size="small" type="primary" secondary @click="loadOrders">刷新</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="orders" :loading="loading" scroll-x="760" size="small" />
      <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadOrders" />
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { h, ref, watch } from 'vue'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList } from '@/api/order'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import type { Order, User } from '@/api/types'

const props = defineProps<{
  show: boolean
  user: User | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
}>()

const authStore = useAuthStore()
const isSuperAdmin = authStore.isSuperAdmin

const pageSize = 10
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const orders = ref<Order[]>([])
const crossCommunity = ref(false)

/** 状态标签配色（与订单列表页口径一致） */
function statusTagType(status: number): 'default' | 'success' | 'warning' | 'error' | 'info' {
  switch (status) {
    case 1:
      return 'warning' // 待支付
    case 2:
    case 3:
      return 'info' // 已支付/备货中
    case 4:
      return 'info' // 配送中
    case 5:
      return 'success' // 已完成
    case 6:
      return 'default' // 已取消
    default:
      return 'error' // 退款中/已退款
  }
}

const columns: DataTableColumns<Order> = [
  { title: '订单号', key: 'order_no', width: 200, ellipsis: { tooltip: true } },
  { title: '社区', key: 'community_name', width: 110, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status_text', width: 90, render: (row) => h(NTag, { type: statusTagType(row.status), size: 'small' }, { default: () => row.status_text }) },
  { title: '实付金额', key: 'pay_amount', width: 100, render: (row) => `¥${Number(row.pay_amount ?? 0).toFixed(2)}` },
  { title: '配送方式', key: 'delivery_type', width: 90, render: (row) => (row.delivery_type === 1 ? '自提' : '配送') },
  { title: '下单时间', key: 'created_at', width: 160, render: (row) => formatDateTime(row.created_at) },
]

/**
 * 加载该会员的订单列表
 */
async function loadOrders() {
  if (!props.user) return
  loading.value = true
  try {
    const res = await getOrderList({
      page: page.value,
      page_size: pageSize,
      user_id: props.user.id,
      // 仅跨社区查询时显式传 0；默认不传走当前社区语境
      ...(crossCommunity.value && isSuperAdmin ? { community_id: 0 } : {}),
    })
    orders.value = res.data.data.list || []
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show && props.user) {
      page.value = 1
      loadOrders()
    }
  },
)

watch(crossCommunity, () => {
  page.value = 1
  loadOrders()
})
</script>
