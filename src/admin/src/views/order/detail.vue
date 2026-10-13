<template>
  <n-modal :show="show" preset="card" title="订单详情" class="order-detail-modal" @update:show="onUpdateShow">
    <n-spin :show="loading">
      <n-empty v-if="!detail && !loading" description="订单不存在" />
      <n-space v-else-if="detail" vertical :size="16">
        <!-- 描述列表：窄屏自动降为单列，避免两列内容挤压换行难读 -->
        <n-descriptions label-placement="left" bordered :column="descColumn" size="small">
          <n-descriptions-item label="订单号">{{ detail.order_no }}</n-descriptions-item>
          <n-descriptions-item label="用户">{{ detail.user_name }}</n-descriptions-item>
          <n-descriptions-item label="状态">
            <n-tag :type="statusTagType(detail.status)" size="small">{{ detail.status_text }}</n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="配送方式">{{ detail.delivery_type === 2 ? '配送' : '自提' }}</n-descriptions-item>
          <n-descriptions-item label="订单金额">￥{{ formatAmount(detail.total_amount) }}</n-descriptions-item>
          <n-descriptions-item label="优惠金额">-￥{{ formatAmount(detail.discount_amount) }}</n-descriptions-item>
          <n-descriptions-item label="运费">￥{{ formatAmount(detail.delivery_fee) }}</n-descriptions-item>
          <n-descriptions-item label="实付金额">
            <span class="amount-strong">￥{{ formatAmount(detail.pay_amount) }}</span>
          </n-descriptions-item>
          <n-descriptions-item label="期望送达">{{ detail.delivery_time || '-' }}</n-descriptions-item>
          <n-descriptions-item label="下单时间">{{ detail.created_at }}</n-descriptions-item>
          <n-descriptions-item label="收货信息" :span="2">
            <template v-if="detail.address && detail.address.name">
              {{ detail.address.name }} {{ detail.address.phone }} {{ detail.address.address }}
            </template>
            <template v-else>-</template>
          </n-descriptions-item>
          <n-descriptions-item v-if="detail.remark" label="订单备注" :span="2">{{ detail.remark }}</n-descriptions-item>
          <n-descriptions-item v-if="detail.cancel_reason" label="取消原因" :span="2">
            {{ detail.cancel_reason }}
          </n-descriptions-item>
        </n-descriptions>

        <!-- 商品明细：固定列宽,窄屏横向滚动 -->
        <n-data-table :columns="itemColumns" :data="detail.items" size="small" :bordered="true" :scroll-x="620" />

        <div v-if="detail.status_logs && detail.status_logs.length">
          <div class="section-title">状态流转</div>
          <n-data-table :columns="logColumns" :data="detail.status_logs" size="small" :scroll-x="620" />
        </div>
      </n-space>
    </n-spin>
  </n-modal>
</template>

<script setup lang="ts">
// 页面级组件：订单详情弹窗，供订单列表 / 售后管理复用
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderDetail } from '@/api/order'
import type { OrderDetail, OrderItem, OrderStatusLog } from '@/api/types'

/**
 * 订单详情弹窗组件
 * 通过弹窗加载订单基本信息、商品明细与状态流转日志
 */
const props = defineProps<{
  /** 弹窗显示状态（v-model:show） */
  show: boolean
  /** 订单 ID，弹窗打开时加载详情 */
  orderId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

const loading = ref(false)
const detail = ref<OrderDetail | null>(null)

// --- 描述列表响应式列数 ---
// 窄屏（<=768px，与全局布局断点一致）降为单列，宽屏保持两列
const isNarrow = ref(window.innerWidth <= 768)

/** 描述列表列数：宽屏 2 列，窄屏 1 列 */
const descColumn = computed(() => (isNarrow.value ? 1 : 2))

/** 窗口尺寸变化时同步窄屏标记 */
function updateNarrow() {
  isNarrow.value = window.innerWidth <= 768
}

const itemColumns: DataTableColumns<OrderItem> = [
  { title: '商品名称', key: 'product_name' },
  { title: '规格', key: 'sku_name', width: 120 },
  { title: '单价', key: 'price', width: 100, render: (row) => `￥${formatAmount(row.price)}` },
  { title: '数量', key: 'quantity', width: 70 },
  { title: '小计', key: 'total_amount', width: 110, render: (row) => `￥${formatAmount(row.total_amount)}` },
]

const logColumns: DataTableColumns<OrderStatusLog> = [
  {
    title: '流转',
    key: 'transition',
    width: 160,
    render: (row) => `${statusTextOf(row.from_status)} → ${statusTextOf(row.to_status)}`,
  },
  { title: '操作方', key: 'operator_type', width: 90 },
  { title: '备注', key: 'remark' },
  { title: '时间', key: 'created_at', width: 180 },
]

/**
 * 关闭弹窗时同步 v-model:show
 * @param value 是否显示
 */
function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

/**
 * 加载订单详情
 * @param id 订单 ID
 */
async function loadDetail(id: number) {
  loading.value = true
  try {
    const res = await getOrderDetail(id)
    if (res.data.code === 0) {
      detail.value = res.data.data
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

/**
 * 订单状态彩色标签类型
 * @param status 订单状态码
 * @returns n-tag 类型
 */
function statusTagType(status: number): 'default' | 'warning' | 'info' | 'primary' | 'success' | 'error' {
  switch (status) {
    case 1:
      return 'warning'
    case 2:
      return 'info'
    case 3:
      return 'info'
    case 4:
      return 'primary'
    case 5:
      return 'success'
    case 7:
    case 8:
      return 'error'
    default:
      return 'default'
  }
}

/**
 * 状态码转文案（状态流转日志展示用）
 * @param status 状态码
 * @returns 状态中文描述
 */
function statusTextOf(status: number): string {
  const map: Record<number, string> = {
    0: '创建',
    1: '待支付',
    2: '待发货',
    3: '备货中',
    4: '配送中',
    5: '已完成',
    6: '已取消',
    7: '退款中',
    8: '已退款',
  }
  return map[status] || String(status)
}

/**
 * 金额格式化（保留两位小数）
 * @param value 金额
 * @returns 格式化结果
 */
function formatAmount(value: number): string {
  return (value ?? 0).toFixed(2)
}

watch(
  () => props.show,
  (visible) => {
    if (visible && props.orderId) {
      detail.value = null
      loadDetail(props.orderId)
    }
  },
)

onMounted(() => {
  window.addEventListener('resize', updateNarrow)
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
})
</script>

<style scoped>
.order-detail-modal {
  width: min(880px, calc(100vw - 48px));
}

.amount-strong {
  color: #d03050;
  font-weight: 600;
}

.section-title {
  font-weight: 600;
  margin-bottom: 8px;
}
</style>
