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

      <!-- 常买商品排行：按该会员有效订单（已支付及之后）统计，用户维度跨全部社区 -->
      <n-divider style="margin: 8px 0" />
      <n-space align="center" justify="space-between">
        <n-text strong>常买商品排行</n-text>
        <n-text depth="3" style="font-size: 12px">按购买次数排序 · 含全部社区有效订单</n-text>
      </n-space>
      <n-data-table :columns="productColumns" :data="products" :loading="productsLoading" size="small" />
      <n-pagination v-model:page="productPage" :page-count="productPageCount" @update:page="loadFrequentProducts" />
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { h, ref, watch } from 'vue'
import { NButton, NImage, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList } from '@/api/order'
import { getUserFrequentProducts } from '@/api/user'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import type { FrequentProduct, Order, User } from '@/api/types'

/** 弹窗入参：仅需会员 ID，昵称/手机号可选（订单列表场景仅有 id 与姓名快照） */
type ModalUser = Pick<User, 'id'> & Partial<Pick<User, 'nickname' | 'phone'>>

const props = defineProps<{
  show: boolean
  user: ModalUser | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 点击订单号：请求页面层打开订单详情弹窗（弹窗间不互相嵌套，避免组件递归） */
  (e: 'open-order', id: number): void
}>()

const authStore = useAuthStore()
const isSuperAdmin = authStore.isSuperAdmin

const pageSize = 10
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const orders = ref<Order[]>([])
const crossCommunity = ref(false)

// --- 常买商品排行（每页 5 条） ---
const PRODUCT_PAGE_SIZE = 5
const productsLoading = ref(false)
const productPage = ref(1)
const productPageCount = ref(1)
const products = ref<FrequentProduct[]>([])

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
  {
    title: '订单号',
    key: 'order_no',
    width: 200,
    ellipsis: { tooltip: true },
    render: (row) =>
      h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => emit('open-order', row.id) },
        { default: () => row.order_no },
      ),
  },
  { title: '社区', key: 'community_name', width: 110, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status_text', width: 90, render: (row) => h(NTag, { type: statusTagType(row.status), size: 'small' }, { default: () => row.status_text }) },
  { title: '实付金额', key: 'pay_amount', width: 100, render: (row) => `¥${Number(row.pay_amount ?? 0).toFixed(2)}` },
  { title: '配送方式', key: 'delivery_type', width: 90, render: (row) => (row.delivery_type === 1 ? '自提' : '配送') },
  { title: '下单时间', key: 'created_at', width: 160, render: (row) => formatDateTime(row.created_at) },
]

/** 常买商品排行列（排名按跨页累计序号） */
const productColumns: DataTableColumns<FrequentProduct> = [
  {
    title: '#',
    key: 'rank',
    width: 50,
    render: (_row, index) => `${(productPage.value - 1) * PRODUCT_PAGE_SIZE + index + 1}`,
  },
  {
    title: '商品',
    key: 'product_name',
    render: (row) =>
      h('div', { style: 'display:flex;align-items:center;gap:8px;min-width:0' }, [
        row.product_image
          ? h(NImage, {
              src: row.product_image,
              width: 32,
              height: 32,
              objectFit: 'cover',
              style: 'border-radius:4px;flex:none',
              imgProps: { referrerpolicy: 'no-referrer' },
            })
          : h('div', { style: 'width:32px;height:32px;border-radius:4px;background:var(--n-action-color);flex:none' }),
        h('span', { style: 'overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, row.product_name),
      ]),
  },
  { title: '购买次数', key: 'purchase_count', width: 90, render: (row) => `${row.purchase_count} 次` },
  { title: '累计件数', key: 'total_quantity', width: 90, render: (row) => `${row.total_quantity} 件` },
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

/**
 * 加载该会员的常买商品排行（分页）
 */
async function loadFrequentProducts() {
  if (!props.user) return
  productsLoading.value = true
  try {
    const res = await getUserFrequentProducts(props.user.id, {
      page: productPage.value,
      page_size: PRODUCT_PAGE_SIZE,
    })
    products.value = res.data.data.list || []
    productPageCount.value = Math.ceil(res.data.data.total / PRODUCT_PAGE_SIZE) || 1
  } finally {
    productsLoading.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show && props.user) {
      page.value = 1
      productPage.value = 1
      loadOrders()
      loadFrequentProducts()
    }
  },
)

watch(crossCommunity, () => {
  page.value = 1
  loadOrders()
})
</script>
