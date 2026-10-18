<template>
  <n-card title="领取/发放记录">
    <n-space vertical>
      <n-space>
        <n-select
          v-model:value="filterCouponId"
          :options="couponOptions"
          placeholder="全部优惠券"
          clearable
          filterable
          style="width: 220px"
        />
        <n-select
          v-model:value="filterStatus"
          :options="statusOptions"
          placeholder="全部状态"
          clearable
          style="width: 140px"
        />
        <n-select
          v-model:value="filterSource"
          :options="sourceOptions"
          placeholder="全部来源"
          clearable
          style="width: 140px"
        />
        <n-button @click="router.push('/marketing/coupons')">返回优惠券列表</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote />
    </n-space>

    <!-- 会员订单弹窗：点击会员查看其订单与常买商品（订单号点击经 open-order 转发到详情弹窗） -->
    <user-orders-modal v-model:show="showUserOrders" :user="userOrdersUser" @open-order="openOrderDetail" />

    <!-- 订单详情弹窗：点击「查看订单」查看核销该券的订单（用户点击经 view-user 转发到会员订单弹窗） -->
    <order-detail-modal v-model:show="showOrderDetail" :order-id="orderDetailId" @view-user="openUserOrdersOf" />
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'CouponRecords' })
import { ref, onMounted, watch, h } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NButton, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCouponList, getCouponRecords } from '@/api/marketing'
import OrderDetailModal from '@/views/order/detail.vue'
import UserOrdersModal from '@/views/user/components/UserOrdersModal.vue'
import type { Coupon, CouponRecord } from '@/api/types'
import { reopenModal } from '@/utils/modal'

/**
 * 优惠券领取/发放记录页（M②期）
 * 展示 user_coupons 全量记录（自领 + 后台手动发放），支持按券/状态/来源筛选；
 * 记录范围由后端按当前社区语境限定，仅展示本社区券的领取情况。
 */
const router = useRouter()
const route = useRoute()
const list = ref<CouponRecord[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const filterCouponId = ref<number | null>(null)
const filterStatus = ref<number | null>(null)
const filterSource = ref<number | null>(null)
const couponOptions = ref<{ label: string; value: number }[]>([])

const statusOptions = [
  { label: '未使用', value: 1 },
  { label: '已使用', value: 2 },
  { label: '已过期', value: 3 },
]

const sourceOptions = [
  { label: '用户自领', value: 1 },
  { label: '后台发放', value: 2 },
]

const typeMap: Record<number, string> = { 1: '满减券', 2: '折扣券' }

const statusMap: Record<number, { text: string; type: 'default' | 'success' | 'warning' }> = {
  1: { text: '未使用', type: 'success' },
  2: { text: '已使用', type: 'default' },
  3: { text: '已过期', type: 'warning' },
}

/** 面值列：满减券显示 ¥x，折扣券显示 x 折 */
function renderDiscount(row: CouponRecord) {
  if (row.type === 2) {
    const zhe = Math.round((1 - row.discount_value) * 10 * 10) / 10
    return `${zhe} 折`
  }
  return `¥${row.discount_value}`
}

// --- 会员订单弹窗（点击会员打开） ---
const showUserOrders = ref(false)
const userOrdersUser = ref<{ id: number; nickname?: string } | null>(null)

/**
 * 打开会员订单弹窗，查看该会员的订单与常买商品
 * @param row 记录行（取归属会员）
 */
function openUserOrders(row: CouponRecord) {
  openUserOrdersOf({ id: row.user_id, nickname: row.user_nickname })
}

/**
 * 打开会员订单弹窗（轻量用户对象，含订单详情弹窗 view-user 事件转发）
 * @param user 会员 ID 与昵称
 */
function openUserOrdersOf(user: { id: number; nickname?: string }) {
  userOrdersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(showUserOrders)
}

// --- 订单详情弹窗（点击「查看订单」打开） ---
const showOrderDetail = ref(false)
const orderDetailId = ref<number | null>(null)

/**
 * 打开订单详情弹窗，查看核销该券的订单
 * @param orderId 订单 ID
 */
function openOrderDetail(orderId: number) {
  orderDetailId.value = orderId
  reopenModal(showOrderDetail)
}

/** 门槛列：0 显示「无门槛」 */
function renderThreshold(row: CouponRecord) {
  return row.threshold > 0 ? `满${row.threshold}可用` : '无门槛'
}

const columns: DataTableColumns<CouponRecord> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '优惠券', key: 'coupon_name', width: 140, ellipsis: { tooltip: true } },
  { title: '类型', key: 'type', width: 80, render: (row) => typeMap[row.type] || '未知' },
  { title: '面值', key: 'discount_value', width: 80, render: renderDiscount },
  { title: '门槛', key: 'threshold', width: 100, render: renderThreshold },
  {
    title: '会员',
    key: 'user',
    width: 150,
    render(row) {
      const name = row.user_nickname || '未设置昵称'
      const phone = row.user_phone ? ` ${row.user_phone.slice(0, 3)}****${row.user_phone.slice(-4)}` : ''
      const text = `${name}${phone}`
      // 关联会员可点击查看其订单（复用会员订单弹窗）
      if (row.user_id <= 0) return text
      return h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => openUserOrders(row) },
        { default: () => text },
      )
    },
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      const s = statusMap[row.status] || { text: '未知', type: 'default' as const }
      return h(NTag, { type: s.type }, { default: () => s.text })
    },
  },
  {
    title: '关联订单',
    key: 'order_id',
    width: 100,
    // 已使用的券回填核销订单号，点击查看订单详情；未使用/过期无订单
    render: (row) =>
      row.order_id > 0
        ? h(
            NButton,
            { size: 'small', text: true, type: 'primary', onClick: () => openOrderDetail(row.order_id) },
            { default: () => '查看订单' },
          )
        : '-',
  },
  {
    title: '来源',
    key: 'source',
    width: 110,
    render(row) {
      return row.source === 2 ? h(NTag, { type: 'info' }, { default: () => `后台发放` }) : '用户自领'
    },
  },
  { title: '经手人', key: 'operator_name', width: 100, render: (row) => row.operator_name || '-' },
  { title: '过期时间', key: 'expire_at', width: 160, render: (row) => row.expire_at || '-' },
  { title: '领取/发放时间', key: 'created_at', width: 160 },
]

async function loadCoupons() {
  // 拉全量券做筛选下拉（单社区券量小，一次取 100 条足够）
  const res = await getCouponList({ page: 1, page_size: 100 })
  if (res.data.code === 0) {
    const coupons: Coupon[] = res.data.data.list || []
    couponOptions.value = coupons.map((c) => ({ label: c.name, value: c.id }))
  }
}

async function loadData() {
  loading.value = true
  try {
    const params: Parameters<typeof getCouponRecords>[0] = {
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
    }
    if (filterCouponId.value) params.coupon_id = filterCouponId.value
    if (filterStatus.value) params.status = filterStatus.value
    if (filterSource.value) params.source = filterSource.value
    const res = await getCouponRecords(params)
    if (res.data.code === 0) {
      list.value = res.data.data.list || []
      pagination.value.itemCount = res.data.data.total || 0
    }
  } finally {
    loading.value = false
  }
}

/** 筛选变化：重置到第一页重新加载 */
function onFilterChange() {
  pagination.value.page = 1
  void loadData()
}

watch([filterCouponId, filterStatus, filterSource], onFilterChange)
watch(
  () => pagination.value.page,
  () => void loadData()
)

onMounted(() => {
  // 支持从优惠券列表跳转带 coupon_id 预筛选
  const q = route.query.coupon_id
  if (q) {
    const id = Number(q)
    if (Number.isFinite(id) && id > 0) filterCouponId.value = id
  }
  void loadCoupons()
  void loadData()
})
</script>
