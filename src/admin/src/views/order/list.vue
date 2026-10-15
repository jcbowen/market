<template>
  <n-card title="订单列表">
    <n-space vertical :size="16">
      <!-- 状态概览卡片：点击卡片即筛选对应状态；窄屏每行 3 张，桌面每行 6 张 -->
      <n-grid v-if="stats" :cols="6" :x-gap="12" item-responsive>
        <n-gi v-for="card in statCards" :key="card.label" span="2" l="1">
          <n-card size="small" :class="['stat-card', { active: statusFilter === card.value }]" @click="setStatusFilter(card.value)">
            <n-space vertical :size="2">
              <span class="stat-value">{{ card.count }}</span>
              <span class="stat-label">{{ card.label }}</span>
            </n-space>
          </n-card>
        </n-gi>
      </n-grid>

      <!-- 筛选区：窄屏允许换行，避免横向溢出 -->
      <n-space align="center" :size="12" class="filter-bar">
        <!-- 窄屏用下拉替代状态按钮组，避免 6 个按钮横向溢出 -->
        <n-select
          v-if="isNarrow"
          v-model:value="statusFilter"
          :options="statusFilterOptions"
          class="status-select"
          @update:value="onStatusChange"
        />
        <n-radio-group v-else v-model:value="statusFilter" @update:value="onStatusChange">
          <n-radio-button :value="0">全部订单</n-radio-button>
          <n-radio-button v-for="s in statusOptions" :key="s.value" :value="s.value">{{ s.label }}</n-radio-button>
        </n-radio-group>
        <n-input
          v-model:value="filters.order_no"
          placeholder="订单号搜索"
          clearable
          class="search-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
        <n-input
          v-model:value="filters.receiver_name"
          placeholder="收货人姓名"
          clearable
          class="search-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
        <n-input
          v-model:value="filters.receiver_phone"
          placeholder="收货人电话"
          clearable
          class="search-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
        <n-date-picker v-model:value="dateRange" type="daterange" clearable class="date-picker" />
        <n-select
          v-model:value="sourceFilter"
          :options="orderSourceFilterOptions"
          class="source-select"
          @update:value="onSearch"
        />
        <n-button type="primary" @click="onSearch">查询</n-button>
        <n-button :loading="exporting" @click="exportCsv">导出 CSV</n-button>
      </n-space>

      <!-- 批量操作条 -->
      <n-space v-if="selectedRows.length > 0" align="center">
        <span>已选 {{ selectedRows.length }} 单</span>
        <n-button size="small" type="primary" @click="openBatchShip">批量发货</n-button>
        <n-button size="small" type="success" @click="batchComplete">批量完成</n-button>
      </n-space>

      <n-data-table
        :columns="tableColumns"
        :data="list"
        :loading="loading"
        remote
        :row-key="(row: Order) => row.id"
        :scroll-x="isNarrow ? 670 : 1360"
        :pagination="pagination"
        v-model:checked-row-keys="checkedRowKeys"
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      >
        <template #empty>
          <n-empty :description="loadError ? '订单加载失败' : '暂无订单'">
            <template v-if="loadError" #extra>
              <n-button size="small" @click="loadData">重试</n-button>
            </template>
          </n-empty>
        </template>
      </n-data-table>
    </n-space>
  </n-card>

  <!-- 订单详情弹窗 -->
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" @refresh="loadData" />

  <!-- 发货弹窗（单笔/批量共用） -->
  <ship-modal v-model:show="shipVisible" :order-ids="shipOrderIds" @success="onActionSuccess" />

  <!-- 取消订单弹窗（取消原因必填） -->
  <cancel-order-modal v-model:show="cancelVisible" :order-id="cancelOrderId" @success="onActionSuccess" />

  <!-- 后台代客退款弹窗（发起即执行，免审核） -->
  <refund-modal v-model:show="refundVisible" :order-id="refundOrderId" @success="onActionSuccess" />

  <!-- 自提核销弹窗（凭取货码核销，防冒领） -->
  <n-modal
    v-model:show="showVerifyModal"
    preset="dialog"
    title="自提核销"
    positive-text="确认核销"
    negative-text="取消"
    :loading="verifyLoading"
    @positive-click="submitVerify"
  >
    <n-space vertical>
      <n-text>订单号：{{ verifyOrder?.order_no }}</n-text>
      <n-text depth="3">请输入顾客出示的取货码（订单详情页展示的 8 位数字码）</n-text>
      <n-input
        v-model:value="verifyCode"
        placeholder="8 位取货码"
        maxlength="8"
        :input-props="{ inputmode: 'numeric' }"
        @keyup.enter="submitVerify"
      />
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'OrderList' })
import { computed, h, onMounted, onUnmounted, reactive, ref } from 'vue'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { completeOrder, getOrderList, verifyPickup } from '@/api/order'
import { getOrderStatistics, type OrderStatistics } from '@/api/statistics'
import OrderDetailModal from './detail.vue'
import ShipModal from './components/ShipModal.vue'
import CancelOrderModal from './components/CancelOrderModal.vue'
import RefundModal from './components/RefundModal.vue'
import type { Order } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import {
  DELIVERY_TYPE,
  ORDER_SOURCE,
  ORDER_STATUS,
  QUERY_STATUS,
  orderSourceFilterOptions,
  orderSourceTagType,
  orderSourceText,
  orderStatusTagType,
} from '@/constants/order'

/**
 * 订单列表页面
 * 状态概览卡片、状态/订单号/收货人/时间筛选、批量发货与核销、CSV 导出
 */

const dialog = useDialog()
const message = useMessage()

const statusFilter = ref(0)
const list = ref<Order[]>([])
const loading = ref(false)
const loadError = ref(false)
const exporting = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0, pageSizes: [10, 20, 50, 100], showSizePicker: true })

const filters = reactive({ order_no: '', receiver_name: '', receiver_phone: '' })
const dateRange = ref<[number, number] | null>(null)
// 来源筛选：0=全部 1=C端 2=收银台
const sourceFilter = ref(0)

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

const shipVisible = ref(false)
const shipOrderIds = ref<number[]>([])
const cancelVisible = ref(false)
const cancelOrderId = ref<number | null>(null)
const refundVisible = ref(false)
const refundOrderId = ref<number | null>(null)

const checkedRowKeys = ref<number[]>([])
const selectedRows = computed(() => list.value.filter((row) => checkedRowKeys.value.includes(row.id)))

// --- 窄屏适配（<=768px，与全局布局断点一致）---
const isNarrow = ref(window.innerWidth <= 768)

function updateNarrow() {
  isNarrow.value = window.innerWidth <= 768
}

/** 状态筛选项：与后端聚合虚拟状态口径一致（90=待发货 9=售后） */
const statusOptions = [
  { label: '待支付', value: ORDER_STATUS.PENDING_PAY },
  { label: '待发货', value: QUERY_STATUS.SHIPPING_AGGREGATE },
  { label: '配送中', value: ORDER_STATUS.SHIPPING },
  { label: '已完成', value: ORDER_STATUS.COMPLETED },
  { label: '已取消', value: ORDER_STATUS.CANCELLED },
  { label: '售后', value: QUERY_STATUS.REFUND_AGGREGATE },
]

/** 状态筛选下拉选项（窄屏替代按钮组） */
const statusFilterOptions = [{ label: '全部订单', value: 0 }, ...statusOptions]

// --- 状态概览卡片 ---
const stats = ref<OrderStatistics | null>(null)

const statCards = computed(() => [
  { label: '全部订单', value: 0, count: stats.value?.total_orders ?? 0 },
  { label: '待支付', value: ORDER_STATUS.PENDING_PAY, count: stats.value?.pending_pay ?? 0 },
  { label: '待发货', value: QUERY_STATUS.SHIPPING_AGGREGATE, count: stats.value?.pending_ship ?? 0 },
  { label: '待收货', value: ORDER_STATUS.SHIPPING, count: stats.value?.pending_receive ?? 0 },
  { label: '已完成', value: ORDER_STATUS.COMPLETED, count: stats.value?.completed ?? 0 },
  { label: '售后中', value: QUERY_STATUS.REFUND_AGGREGATE, count: stats.value?.refunding ?? 0 },
])

async function loadStats() {
  try {
    const res = await getOrderStatistics({})
    if (res.data.code === 0) {
      stats.value = res.data.data
    }
  } catch {
    // 统计加载失败不阻塞列表，卡片隐藏
    stats.value = null
  }
}

function setStatusFilter(value: number) {
  statusFilter.value = value
  pagination.value.page = 1
  loadData()
}

const columns: DataTableColumns<Order> = [
  { type: 'selection' },
  { title: '订单号', key: 'order_no', width: 200 },
  { title: '用户', key: 'user_name', width: 110 },
  {
    title: '配送方式',
    key: 'delivery_type',
    width: 90,
    render: (row) => (row.delivery_type === DELIVERY_TYPE.DELIVERY ? '配送' : '自提'),
  },
  {
    title: '来源',
    key: 'source',
    width: 90,
    render: (row) =>
      h(
        NTag,
        { type: orderSourceTagType(row.source ?? ORDER_SOURCE.USER), size: 'small', bordered: false },
        { default: () => orderSourceText(row.source ?? ORDER_SOURCE.USER) },
      ),
  },
  { title: '经手人', key: 'operator_name', width: 90, render: (row) => row.operator_name || '-' },
  {
    title: '订单金额',
    key: 'total_amount',
    width: 110,
    render: (row) => `￥${(row.total_amount ?? 0).toFixed(2)}`,
  },
  {
    title: '实付金额',
    key: 'pay_amount',
    width: 110,
    render: (row) => `￥${(row.pay_amount ?? 0).toFixed(2)}`,
  },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render: (row) => h(NTag, { type: orderStatusTagType(row.status), size: 'small', bordered: false }, { default: () => row.status_text }),
  },
  { title: '创建时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 220,
    render(row) {
      const buttons = [
        h(
          NButton,
          { size: 'small', onClick: () => openDetail(row.id) },
          { default: () => '详情' },
        ),
      ]
      if (row.status === ORDER_STATUS.PAID || row.status === ORDER_STATUS.STOCKING) {
        if (row.delivery_type === DELIVERY_TYPE.DELIVERY) {
          buttons.push(
            h(
              NButton,
              { size: 'small', type: 'primary', onClick: () => openShip([row.id]) },
              { default: () => '发货' },
            ),
          )
        } else {
          // 自提单：凭顾客出示的取货码核销（输码校验，防冒领）
          buttons.push(
            h(
              NButton,
              { size: 'small', type: 'success', onClick: () => openVerify(row) },
              { default: () => '核销' },
            ),
          )
        }
      }
      if (row.status === ORDER_STATUS.SHIPPING) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'success', onClick: () => confirmComplete(row) },
            { default: () => '完成' },
          ),
        )
      }
      // 已支付/备货中/配送中允许后台代客退款（与后端可退状态一致）
      if (row.status === ORDER_STATUS.PAID || row.status === ORDER_STATUS.STOCKING || row.status === ORDER_STATUS.SHIPPING) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => openRefund(row.id) },
            { default: () => '退款' },
          ),
        )
      }
      if (row.status === ORDER_STATUS.PENDING_PAY) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => openCancel(row.id) },
            { default: () => '取消订单' },
          ),
        )
      }
      return h(NSpace, { size: 6, wrap: false }, { default: () => buttons })
    },
  },
]

/**
 * 表格列：窄屏隐藏次要列（用户/配送方式/订单金额/创建时间），保留关键列与操作列
 */
const tableColumns = computed<DataTableColumns<Order>>(() => {
  if (!isNarrow.value) return columns
  const hiddenKeys = new Set(['user_name', 'delivery_type', 'source', 'operator_name', 'total_amount', 'created_at'])
  return columns.filter((col) => !hiddenKeys.has((col as { key?: string }).key ?? ''))
})

/**
 * 切换状态按钮组后回到第 1 页并刷新
 * @param value 目标状态（0 表示全部）
 */
function onStatusChange(value: number) {
  statusFilter.value = value
  pagination.value.page = 1
  loadData()
}

/**
 * 点击查询回到第 1 页并刷新
 */
function onSearch() {
  pagination.value.page = 1
  loadData()
}

/**
 * 分页切换
 * @param page 目标页码
 */
function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

/**
 * 每页条数切换
 * @param pageSize 目标条数
 */
function onPageSizeChange(pageSize: number) {
  pagination.value.pageSize = pageSize
  pagination.value.page = 1
  loadData()
}

/**
 * 组装当前筛选条件（列表查询与导出共用）
 */
function buildQuery() {
  const [start, end] = dateRange.value ?? []
  const fmt = (ts: number) => {
    const d = new Date(ts)
    const m = `${d.getMonth() + 1}`.padStart(2, '0')
    const day = `${d.getDate()}`.padStart(2, '0')
    return `${d.getFullYear()}-${m}-${day}`
  }
  return {
    page: pagination.value.page,
    page_size: pagination.value.pageSize,
    order_no: filters.order_no || undefined,
    receiver_name: filters.receiver_name || undefined,
    receiver_phone: filters.receiver_phone || undefined,
    start_time: start ? fmt(start) : undefined,
    end_time: end ? fmt(end) : undefined,
    status: statusFilter.value > 0 ? statusFilter.value : undefined,
    source: sourceFilter.value > 0 ? sourceFilter.value : undefined,
  }
}

/**
 * 打开订单详情弹窗
 * @param id 订单 ID
 */
function openDetail(id: number) {
  detailId.value = id
  detailVisible.value = true
}

/**
 * 打开发货弹窗（单笔或批量）
 * @param ids 订单 ID 列表
 */
function openShip(ids: number[]) {
  shipOrderIds.value = ids
  shipVisible.value = true
}

/**
 * 打开批量发货弹窗：过滤出可发货的配送单
 */
function openBatchShip() {
  const shippable = selectedRows.value.filter(
    (row) => row.status === ORDER_STATUS.PAID && row.delivery_type === DELIVERY_TYPE.DELIVERY,
  )
  if (shippable.length === 0) {
    dialog.warning({
      title: '无法批量发货',
      content: '选中订单中没有「待发货的配送单」，自提单请使用批量核销。',
      positiveText: '知道了',
    })
    return
  }
  openShip(shippable.map((row) => row.id))
}

/**
 * 打开取消订单弹窗
 * @param id 订单 ID
 */
function openCancel(id: number) {
  cancelOrderId.value = id
  cancelVisible.value = true
}

/**
 * 打开后台代客退款弹窗
 * @param id 订单 ID
 */
function openRefund(id: number) {
  refundOrderId.value = id
  refundVisible.value = true
}

/**
 * 确认完成单笔订单（配送单，二次确认；自提单走输码核销 openVerify）
 * @param row 订单行
 */
function confirmComplete(row: Order) {
  dialog.warning({
    title: '完成订单',
    content: `确认将订单 ${row.order_no} 标记为已完成？`,
    positiveText: '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await completeOrder(row.id)
        loadData()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

// --- 自提核销（输码校验） ---
const showVerifyModal = ref(false)
const verifyOrder = ref<Order | null>(null)
const verifyCode = ref('')
const verifyLoading = ref(false)

/**
 * 打开自提核销弹窗
 * @param row 订单行
 */
function openVerify(row: Order) {
  verifyOrder.value = row
  verifyCode.value = ''
  showVerifyModal.value = true
}

/**
 * 提交核销：校验取货码后完成订单
 */
async function submitVerify() {
  const row = verifyOrder.value
  if (!row) return
  if (!verifyCode.value.trim()) {
    message.warning('请输入顾客出示的取货码')
    return
  }
  verifyLoading.value = true
  try {
    await verifyPickup(row.id, verifyCode.value.trim())
    message.success('核销成功')
    showVerifyModal.value = false
    loadData()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifyLoading.value = false
  }
}

/**
 * 批量核销：对选中的「已发货配送单」逐单完结
 * 自提单必须逐单凭取货码核销（防冒领），不参与批量
 */
function batchComplete() {
  const completable = selectedRows.value.filter((row) => row.status === ORDER_STATUS.SHIPPING)
  if (completable.length === 0) {
    dialog.warning({
      title: '无法批量核销',
      content: '选中订单中没有「已发货的配送单」。自提单请使用「核销」按钮凭取货码逐单核销。',
      positiveText: '知道了',
    })
    return
  }
  dialog.warning({
    title: '批量完成',
    content: `确认将选中的 ${completable.length} 个已发货配送单标记为已完成？`,
    positiveText: '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      let successCount = 0
      for (const row of completable) {
        try {
          await completeOrder(row.id)
          successCount++
        } catch {
          // 错误提示由请求拦截器统一弹出，继续处理下一单
        }
      }
      message.success(`批量完成：成功 ${successCount} 单`)
      loadData()
    },
  })
}

/**
 * 操作成功后的统一刷新
 */
function onActionSuccess() {
  checkedRowKeys.value = []
  loadData()
}

/**
 * 导出当前筛选条件下的订单 CSV（逐页拉取，上限 2000 条）
 */
async function exportCsv() {
  exporting.value = true
  try {
    const baseQuery = { ...buildQuery(), page: 1, page_size: 100 }
    const all: Order[] = []
    for (let page = 1; page <= 20; page++) {
      const res = await getOrderList({ ...baseQuery, page })
      if (res.data.code !== 0) break
      all.push(...res.data.data.list)
      if (all.length >= res.data.data.total) break
    }
    if (all.length === 0) {
      message.info('当前筛选条件下没有可导出的订单')
      return
    }
    const header = ['订单号', '用户', '配送方式', '来源', '经手人', '订单金额', '实付金额', '状态', '创建时间']
    const rows = all.map((row) => [
      row.order_no,
      row.user_name,
      row.delivery_type === DELIVERY_TYPE.DELIVERY ? '配送' : '自提',
      orderSourceText(row.source ?? ORDER_SOURCE.USER),
      row.operator_name || '',
      (row.total_amount ?? 0).toFixed(2),
      (row.pay_amount ?? 0).toFixed(2),
      row.status_text,
      formatDateTime(row.created_at),
    ])
    // BOM 保证 Excel 正确识别 UTF-8
    const csv =
      '\ufeff' +
      [header, ...rows]
        .map((cells) => cells.map((cell) => `"${String(cell).replace(/"/g, '""')}"`).join(','))
        .join('\n')
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `orders-${new Date().toISOString().slice(0, 10)}.csv`
    link.click()
    URL.revokeObjectURL(url)
  } finally {
    exporting.value = false
  }
}

/**
 * 加载订单分页数据；失败时清空列表并展示错误态
 */
async function loadData() {
  loading.value = true
  loadError.value = false
  try {
    const res = await getOrderList(buildQuery())
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } catch {
    loadError.value = true
    list.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadData()
  loadStats()
  window.addEventListener('resize', updateNarrow)
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
})
</script>

<style scoped>
/* 筛选区允许换行后,窄屏下搜索框自适应宽度,避免固定宽度挤压 */
.search-input {
  width: 160px;
}

.date-picker {
  width: 260px;
}

.status-select {
  width: 100%;
}

.source-select {
  width: 120px;
}

@media (max-width: 768px) {
  .search-input,
  .date-picker {
    width: 100%;
  }
}

.stat-card {
  cursor: pointer;
  transition: border-color 0.2s;
}

.stat-card.active {
  border-color: #18a058;
}

.stat-value {
  font-size: 20px;
  font-weight: 600;
}

.stat-label {
  font-size: 12px;
  color: #999;
}
</style>
