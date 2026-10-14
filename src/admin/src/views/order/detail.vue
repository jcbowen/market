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
            <n-tag :type="orderStatusTagType(detail.status)" size="small">{{ detail.status_text }}</n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="配送方式">{{ deliveryTypeText(detail.delivery_type) }}</n-descriptions-item>
          <n-descriptions-item label="订单金额">￥{{ formatAmount(detail.total_amount) }}</n-descriptions-item>
          <n-descriptions-item label="优惠金额">-￥{{ formatAmount(detail.discount_amount) }}</n-descriptions-item>
          <n-descriptions-item label="运费">￥{{ formatAmount(detail.delivery_fee) }}</n-descriptions-item>
          <n-descriptions-item label="实付金额">
            <span class="amount-strong">￥{{ formatAmount(detail.pay_amount) }}</span>
          </n-descriptions-item>
          <n-descriptions-item label="期望送达">{{ formatDateTime(detail.delivery_time) }}</n-descriptions-item>
          <n-descriptions-item label="下单时间">{{ formatDateTime(detail.created_at) }}</n-descriptions-item>
          <n-descriptions-item v-if="detail.express_company || detail.express_no" label="物流信息" :span="2">
            {{ detail.express_company }}　{{ detail.express_no }}
          </n-descriptions-item>
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

        <!-- 操作区：按当前状态渲染与列表一致的操作按钮 -->
        <n-space v-if="actions.length" justify="end">
          <n-button
            v-for="action in actions"
            :key="action.key"
            size="small"
            :type="action.type"
            @click="action.run()"
          >
            {{ action.label }}
          </n-button>
        </n-space>
      </n-space>
    </n-spin>
  </n-modal>

  <!-- 发货 / 取消订单 / 代客退款弹窗（与列表页共用） -->
  <ship-modal v-model:show="shipVisible" :order-ids="shipOrderIds" @success="onActionSuccess" />
  <cancel-order-modal v-model:show="cancelVisible" :order-id="cancelOrderId" @success="onActionSuccess" />
  <refund-modal v-model:show="refundVisible" :order-id="refundOrderId" @success="onActionSuccess" />
</template>

<script setup lang="ts">
// 页面级组件：订单详情弹窗，供订单列表 / 售后管理复用
// 底部按当前状态渲染操作按钮（发货/核销/完成/取消），操作后刷新详情并通知父级刷新列表
defineOptions({ name: 'OrderDetailModal' })
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NButton, NTag, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { completeOrder, getOrderDetail } from '@/api/order'
import type { OrderDetail, OrderItem, OrderStatusLog } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { DELIVERY_TYPE, ORDER_STATUS, deliveryTypeText, orderStatusTagType, orderStatusText } from '@/constants/order'
import ShipModal from './components/ShipModal.vue'
import CancelOrderModal from './components/CancelOrderModal.vue'
import RefundModal from './components/RefundModal.vue'

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
  (e: 'refresh'): void
}>()

const dialog = useDialog()
const loading = ref(false)
const detail = ref<OrderDetail | null>(null)

const shipVisible = ref(false)
const shipOrderIds = ref<number[]>([])
const cancelVisible = ref(false)
const cancelOrderId = ref<number | null>(null)
const refundVisible = ref(false)
const refundOrderId = ref<number | null>(null)

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
    render: (row) => `${orderStatusText(row.from_status)} → ${orderStatusText(row.to_status)}`,
  },
  { title: '操作方', key: 'operator_type', width: 90 },
  { title: '备注', key: 'remark' },
  { title: '时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
]

/**
 * 按当前订单状态生成可用操作按钮
 */
const actions = computed(() => {
  const d = detail.value
  if (!d) return []
  const list: Array<{ key: string; label: string; type: 'default' | 'primary' | 'success' | 'error'; run: () => void }> = []
  if (d.status === ORDER_STATUS.PENDING_PAY) {
    list.push({ key: 'cancel', label: '取消订单', type: 'error', run: () => openCancel(d.id) })
  }
  if (d.status === ORDER_STATUS.PAID && d.delivery_type === DELIVERY_TYPE.DELIVERY) {
    list.push({ key: 'ship', label: '发货', type: 'primary', run: () => openShip(d.id) })
  }
  if (d.status === ORDER_STATUS.PAID && d.delivery_type === DELIVERY_TYPE.SELF_PICKUP) {
    list.push({ key: 'verify', label: '核销完成', type: 'success', run: () => confirmComplete(d, true) })
  }
  if (d.status === ORDER_STATUS.SHIPPING) {
    list.push({ key: 'complete', label: '完成', type: 'success', run: () => confirmComplete(d, false) })
  }
  // 已支付/备货中/配送中允许后台代客退款（与后端可退状态一致）
  if (d.status === ORDER_STATUS.PAID || d.status === ORDER_STATUS.STOCKING || d.status === ORDER_STATUS.SHIPPING) {
    list.push({ key: 'refund', label: '退款', type: 'error', run: () => openRefund(d.id) })
  }
  return list
})

function openShip(id: number) {
  shipOrderIds.value = [id]
  shipVisible.value = true
}

function openCancel(id: number) {
  cancelOrderId.value = id
  cancelVisible.value = true
}

function openRefund(id: number) {
  refundOrderId.value = id
  refundVisible.value = true
}

function confirmComplete(d: OrderDetail, isPickup: boolean) {
  dialog.warning({
    title: isPickup ? '核销订单' : '完成订单',
    content: `确认将订单 ${d.order_no} 标记为已完成？`,
    positiveText: isPickup ? '确认核销' : '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await completeOrder(d.id)
        loadDetail(d.id)
        emit('refresh')
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 操作成功后的统一刷新
 */
function onActionSuccess() {
  if (detail.value) {
    loadDetail(detail.value.id)
  }
  emit('refresh')
}

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
