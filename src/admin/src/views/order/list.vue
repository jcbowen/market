<template>
  <n-card title="订单列表">
    <n-space vertical :size="16">
      <!-- 实时事件只提示不自动重载：本页可筛选/翻页/行内操作，自动重排会打断正在处理的行 -->
      <NewDataAlert :types="['order.paid', 'order.refund']" :loaded-at="lastLoadedAt" @refresh="loadData" />
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

      <!-- 筛选区：状态行 / 带标签条件行 / 折叠筛选区（浅色底）/ 操作行，分层对齐 -->
      <div class="filter-card">
        <div class="filter-row">
          <!-- 窄屏用下拉替代状态按钮组，避免 6 个按钮横向溢出 -->
          <n-select
            v-if="isNarrow"
            v-model:value="statusFilter"
            :options="statusFilterOptions"
            class="status-select"
            @update:value="onStatusChange"
          />
          <n-radio-group v-else v-model:value="statusFilter" size="small" @update:value="onStatusChange">
            <n-radio-button :value="0">全部订单</n-radio-button>
            <n-radio-button v-for="s in statusOptions" :key="s.value" :value="s.value">{{ s.label }}</n-radio-button>
          </n-radio-group>
          <!-- 班次预筛标签（交班记录「查看班内订单」直达），可关闭清除 -->
          <n-tag v-if="shiftFilter > 0" closable size="small" type="info" @close="clearShiftFilter">
            班次 #{{ shiftFilter }}
          </n-tag>
        </div>

        <div class="filter-row conditions">
          <div class="filter-item">
            <span class="f-label">订单号</span>
            <n-input
              v-model:value="filters.order_no"
              placeholder="订单号前缀匹配"
              clearable
              class="w-order"
              @keyup.enter="onSearch"
              @clear="onSearch"
            />
          </div>
          <div class="filter-item">
            <span class="f-label">账单号</span>
            <n-input
              v-model:value="filters.pay_no"
              placeholder="支付单号 / 渠道交易号"
              clearable
              class="w-pay"
              @keyup.enter="onSearch"
              @clear="onSearch"
            />
          </div>
          <div class="filter-item">
            <span class="f-label">下单时间</span>
            <div class="f-inline">
              <n-date-picker v-model:value="dateRange" type="daterange" clearable class="date-picker" @update:value="onDateRangeChange" />
              <div class="quick-chips">
                <n-button
                  v-for="q in dateQuickOptions"
                  :key="q.key"
                  size="tiny"
                  :secondary="dateQuick !== q.key"
                  :type="dateQuick === q.key ? 'primary' : 'default'"
                  @click="applyDateQuick(q.key)"
                >
                  {{ q.label }}
                </n-button>
              </div>
            </div>
          </div>
          <div class="filter-item">
            <span class="f-label">来源</span>
            <n-select
              v-model:value="sourceFilter"
              :options="orderSourceFilterOptions"
              class="source-select"
              @update:value="onSearch"
            />
          </div>
        </div>

        <!-- 折叠筛选区：会员/商品选择器与低频文本项，浅色底与主条件区分 -->
        <div v-if="showMoreFilters" class="filter-row more-area">
          <div class="filter-item">
            <span class="f-label">会员</span>
            <n-button v-if="!filters.user_id" dashed size="small" @click="userPickerVisible = true">+ 选择会员</n-button>
            <n-tag v-else closable size="small" type="primary" @close="clearUserFilter">{{ filters.user_name }}</n-tag>
          </div>
          <div class="filter-item">
            <span class="f-label">商品</span>
            <n-button v-if="!filters.product_id" dashed size="small" @click="productPickerVisible = true">+ 选择商品</n-button>
            <n-tag v-else closable size="small" type="primary" @close="clearProductFilter">{{ filters.product_name }}</n-tag>
          </div>
          <div class="filter-item">
            <span class="f-label">收货人</span>
            <n-input
              v-model:value="filters.receiver_name"
              placeholder="姓名模糊"
              clearable
              class="w-order"
              @keyup.enter="onSearch"
              @clear="onSearch"
            />
          </div>
          <div class="filter-item">
            <span class="f-label">电话</span>
            <n-input
              v-model:value="filters.receiver_phone"
              placeholder="手机号"
              clearable
              class="w-order"
              @keyup.enter="onSearch"
              @clear="onSearch"
            />
          </div>
        </div>

        <div class="filter-row actions">
          <n-button quaternary size="small" @click="toggleMoreFilters">
            {{ showMoreFilters ? '收起筛选' : '更多筛选' }}
            <template v-if="moreFilterCount > 0 && !showMoreFilters">（{{ moreFilterCount }}）</template>
          </n-button>
          <div class="flex-spacer" />
          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <n-button size="small" :loading="exporting" @click="createExport">导出 CSV</n-button>
            </template>
            后台生成，完成后在右上角「下载中心」取文件
          </n-tooltip>
          <n-button size="small" @click="resetFilters">重置</n-button>
          <n-button type="primary" size="small" @click="onSearch">查询</n-button>
        </div>
      </div>

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
        :scroll-x="isNarrow ? 670 : 1450"
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
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" @refresh="loadData" @view-user="openUserOrdersOf" />

  <!-- 会员订单弹窗：点击用户列查看该会员订单（订单号点击经 open-order 转发到详情弹窗） -->
  <user-orders-modal v-model:show="userOrdersVisible" :user="userOrdersUser" @open-order="openDetail" />

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

  <!-- 指派配送弹窗（B 履约工作台：选本社区履约人员送货） -->
  <n-modal
    v-model:show="assignVisible"
    preset="dialog"
    title="指派配送"
    positive-text="确认指派"
    negative-text="取消"
    :loading="assignLoading"
    @positive-click="submitAssign"
  >
    <n-space vertical>
      <n-text>订单号：{{ assignOrder?.order_no }}</n-text>
      <n-text depth="3">指派后履约人员在 C 端工作台送货（开始配送 → 送达拍照），不走物流发货</n-text>
      <n-spin :show="assignLoadingList">
        <n-radio-group v-model:value="assignUserId" style="display: block">
          <n-space vertical :size="8">
            <n-text v-if="fulfillers.length === 0" depth="3">
              本社区暂无履约人员，请先到「会员管理」将会员设为履约人员
            </n-text>
            <n-radio v-for="f in fulfillers" :key="f.user_id" :value="f.user_id">
              {{ f.nickname }}（{{ f.phone }}）
            </n-radio>
          </n-space>
        </n-radio-group>
      </n-spin>
    </n-space>
  </n-modal>

  <!-- 会员选择器（筛选用，单选；复用公共 UserPicker 组件） -->
  <user-picker v-model:show="userPickerVisible" title="按会员筛选订单" @select="onUserPicked" />

  <!-- 商品选择器（筛选用，单选；按商品过滤含全部规格） -->
  <n-modal
    v-model:show="productPickerVisible"
    preset="card"
    title="按商品筛选订单"
    style="width: 520px; max-width: 92vw"
  >
    <n-input
      v-model:value="productKeyword"
      placeholder="按商品名搜索"
      clearable
      style="margin-bottom: 12px"
    />
    <n-spin :show="productLoading">
      <n-empty v-if="!productOptions.length && !productLoading" description="暂无商品" style="padding: 40px 0" />
      <div v-else class="product-list">
        <div
          v-for="p in productOptions"
          :key="p.id"
          class="product-row"
          @click="onProductPicked(p)"
        >
          <span class="name">{{ p.name }}</span>
          <span class="meta">{{ p.min_price !== undefined ? `¥${p.min_price} 起` : '' }}</span>
        </div>
      </div>
    </n-spin>
    <n-pagination
      v-model:page="productPage"
      :item-count="productTotal"
      :page-size="10"
      style="margin-top: 12px; justify-content: flex-end"
      @update:page="loadProductOptions"
    />
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'OrderList' })
import { computed, h, onActivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRoute } from 'vue-router'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { createOrderExportTask, completeOrder, getOrderList, verifyPickup, assignDelivery, restoreOrder } from '@/api/order'
import { getFulfillers } from '@/api/user'
import { getProductList } from '@/api/product'
import { getOrderStatistics, type OrderStatistics } from '@/api/statistics'
import NewDataAlert from '@/components/NewDataAlert.vue'
import OrderDetailModal from './detail.vue'
import UserOrdersModal from '@/views/user/components/UserOrdersModal.vue'
import UserPicker from '@/components/UserPicker.vue'
import ShipModal from './components/ShipModal.vue'
import CancelOrderModal from './components/CancelOrderModal.vue'
import RefundModal from './components/RefundModal.vue'
import PayTypeLabel from '@/components/PayTypeLabel.vue'
import type { Order, Product, User } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { reopenModal } from '@/utils/modal'
import {
  DELIVERY_TYPE,
  ORDER_SOURCE,
  ORDER_STATUS,
  QUERY_STATUS,
  canAdminRefund,
  canCancelOrder,
  deliveryTypeText,
  orderSourceFilterOptions,
  orderSourceTagType,
  orderSourceText,
  orderStatusTagType,
} from '@/constants/order'

/**
 * 订单列表页面
 * 状态概览卡片、平铺筛选（状态/订单号/支付单号/时间快捷）+ 折叠筛选（会员/商品选择器、收货人）、
 * 批量发货与核销、后台异步导出（下载中心取文件）
 */

const dialog = useDialog()
const message = useMessage()
const route = useRoute()

const statusFilter = ref(0)
const list = ref<Order[]>([])
const loading = ref(false)
const loadError = ref(false)
const exporting = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0, pageSizes: [10, 20, 50, 100], showSizePicker: true })

const filters = reactive({ order_no: '', receiver_name: '', receiver_phone: '', pay_no: '', user_id: 0, user_name: '', product_id: 0, product_name: '' })
const dateRange = ref<[number, number] | null>(null)
// 时间快捷档：today/yesterday/7d/30d；null 表示当前是手选日期
type DateQuickKey = 'today' | 'yesterday' | '7d' | '30d'
const dateQuick = ref<DateQuickKey | null>(null)
/** 时间快捷选项（与手选日期范围互斥联动） */
const dateQuickOptions: Array<{ key: DateQuickKey; label: string }> = [
  { key: 'today', label: '今天' },
  { key: 'yesterday', label: '昨天' },
  { key: '7d', label: '近7天' },
  { key: '30d', label: '近30天' },
]
// 来源筛选：0=全部 1=C端 2=收银台
const sourceFilter = ref(0)
// 班次筛选：>0 时仅看该收银班次订单（交班记录「查看班内订单」直达）
const shiftFilter = ref(0)
// 折叠筛选区展开态
const showMoreFilters = ref(false)

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

// 会员订单弹窗（点击用户列打开）
const userOrdersVisible = ref(false)
const userOrdersUser = ref<{ id: number; nickname?: string } | null>(null)

/**
 * 打开会员订单弹窗
 * @param row 订单行（取归属会员）
 */
function openUserOrders(row: Order) {
  openUserOrdersOf({ id: row.user_id, nickname: row.user_name })
}

/**
 * 打开会员订单弹窗（轻量用户对象，含订单详情弹窗 view-user 事件转发）
 * @param user 会员 ID 与昵称
 */
function openUserOrdersOf(user: { id: number; nickname?: string }) {
  userOrdersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(userOrdersVisible)
}

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

/**
 * 应用路由 query 中的 status 预筛选（工作台待办直达入口）
 * 仅接受本页状态筛选项支持的值；无参数或非法值时回到全部订单
 * @returns 筛选是否发生变化（变化时调用方需重新加载列表）
 */
function applyQueryStatus(): boolean {
  const raw = Number(route.query.status)
  const allowed = new Set<number>(statusOptions.map((s) => s.value))
  const value = Number.isInteger(raw) && allowed.has(raw) ? raw : 0
  if (value === statusFilter.value) return false
  statusFilter.value = value
  pagination.value.page = 1
  return true
}

/**
 * 应用路由 query 中的 shift_id 预筛选（交班记录「查看班内订单」直达入口）
 * 仅接受正整数；无参数或非法值时清除班次筛选
 * @returns 筛选是否发生变化（变化时调用方需重新加载列表）
 */
function applyQueryShift(): boolean {
  const raw = Number(route.query.shift_id)
  const value = Number.isInteger(raw) && raw > 0 ? raw : 0
  if (value === shiftFilter.value) return false
  shiftFilter.value = value
  pagination.value.page = 1
  return true
}

/**
 * 清除班次筛选并刷新
 */
function clearShiftFilter() {
  shiftFilter.value = 0
  pagination.value.page = 1
  loadData()
}

// --- 时间快捷按钮 ---

/**
 * 应用时间快捷档：填充日期范围并刷新（再点同档取消）
 * @param key 快捷档 today/yesterday/7d/30d
 */
function applyDateQuick(key: DateQuickKey) {
  if (dateQuick.value === key) {
    // 再点同档：取消快捷并清空日期
    dateQuick.value = null
    dateRange.value = null
  } else {
    dateQuick.value = key
    const now = new Date()
    const start = new Date(now)
    const end = new Date(now)
    if (key === 'today') {
      // start=end=now
    } else if (key === 'yesterday') {
      start.setDate(start.getDate() - 1)
      end.setTime(start.getTime())
    } else if (key === '7d') {
      start.setDate(start.getDate() - 6)
    } else {
      start.setDate(start.getDate() - 29)
    }
    dateRange.value = [start.getTime(), end.getTime()]
  }
  pagination.value.page = 1
  loadData()
}

/**
 * 手选日期变化：清除快捷档标记（互斥联动），并触发刷新
 * @param value 新日期范围（null=已清空）
 */
function onDateRangeChange(value: [number, number] | null) {
  dateQuick.value = null
  pagination.value.page = 1
  // 手动选完范围才刷新；value=null 是清除操作，同样刷新回到全量
  void value
  loadData()
}

// --- 折叠筛选区 ---

/** 折叠区已设置的条件数（「更多筛选」按钮计数角标） */
const moreFilterCount = computed(
  () =>
    (filters.user_id > 0 ? 1 : 0) +
    (filters.product_id > 0 ? 1 : 0) +
    (filters.receiver_name ? 1 : 0) +
    (filters.receiver_phone ? 1 : 0),
)

/**
 * 展开/收起折叠筛选区
 */
function toggleMoreFilters() {
  showMoreFilters.value = !showMoreFilters.value
}

// --- 会员/商品选择器（筛选用） ---

const userPickerVisible = ref(false)

/**
 * 会员选择器回调：写入筛选条件并刷新（单选）
 * @param user 选中的会员
 */
function onUserPicked(user: User) {
  filters.user_id = user.id
  filters.user_name = user.nickname || user.phone || `用户${user.id}`
  showMoreFilters.value = true
  pagination.value.page = 1
  loadData()
}

/**
 * 清除会员筛选并刷新
 */
function clearUserFilter() {
  filters.user_id = 0
  filters.user_name = ''
  pagination.value.page = 1
  loadData()
}

const productPickerVisible = ref(false)
const productKeyword = ref('')
const productOptions = ref<Product[]>([])
const productTotal = ref(0)
const productPage = ref(1)
const productLoading = ref(false)

let productSearchTimer: ReturnType<typeof setTimeout> | null = null

// 商品关键字 300ms 防抖搜索
watch(productKeyword, () => {
  if (productSearchTimer) clearTimeout(productSearchTimer)
  productSearchTimer = setTimeout(() => {
    productSearchTimer = null
    productPage.value = 1
    void loadProductOptions()
  }, 300)
})

/**
 * 打开商品选择器时加载第一页
 */
watch(productPickerVisible, (v) => {
  if (v) {
    productKeyword.value = ''
    productPage.value = 1
    void loadProductOptions()
  }
})

/**
 * 加载商品分页列表（仅上架商品，供筛选选择）
 */
async function loadProductOptions() {
  productLoading.value = true
  try {
    const res = await getProductList({
      page: productPage.value,
      page_size: 10,
      status: 1,
      keyword: productKeyword.value.trim() || undefined,
    })
    if (res.data.code === 0) {
      productOptions.value = res.data.data.list || []
      productTotal.value = res.data.data.total || 0
    }
  } finally {
    productLoading.value = false
  }
}

/**
 * 商品选择器回调：写入筛选条件并刷新（按商品含全部规格）
 * @param p 选中的商品
 */
function onProductPicked(p: Product) {
  filters.product_id = p.id
  filters.product_name = p.name
  productPickerVisible.value = false
  pagination.value.page = 1
  loadData()
}

/**
 * 清除商品筛选并刷新
 */
function clearProductFilter() {
  filters.product_id = 0
  filters.product_name = ''
  pagination.value.page = 1
  loadData()
}

/**
 * 重置全部筛选条件并刷新（回到「全部订单 · 无条件」初始态）
 */
function resetFilters() {
  filters.order_no = ''
  filters.receiver_name = ''
  filters.receiver_phone = ''
  filters.pay_no = ''
  filters.user_id = 0
  filters.user_name = ''
  filters.product_id = 0
  filters.product_name = ''
  dateRange.value = null
  dateQuick.value = null
  sourceFilter.value = 0
  shiftFilter.value = 0
  statusFilter.value = 0
  pagination.value.page = 1
  loadData()
}

const columns: DataTableColumns<Order> = [
  { type: 'selection' },
  { title: '订单号', key: 'order_no', width: 200 },
  {
    title: '用户',
    key: 'user_name',
    width: 110,
    // 关联会员（user_id>0）可点击查看该会员订单；无归属时展示原文
    render: (row) =>
      row.user_id > 0
        ? h(
            NButton,
            { size: 'small', text: true, type: 'primary', onClick: () => openUserOrders(row) },
            { default: () => row.user_name || `用户${row.user_id}` },
          )
        : row.user_name || '-',
  },
  {
    title: '配送方式',
    key: 'delivery_type',
    width: 90,
    render: (row) => deliveryTypeText(row.delivery_type),
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
    title: '支付方式',
    key: 'pay_type',
    width: 90,
    // 仅图标：悬浮/触屏点按显示文字（2026-10-25 修订）
    render: (row) => h(PayTypeLabel, { payType: row.pay_type, text: row.pay_type_text, iconOnly: true }),
  },
  {
    title: '配送员',
    key: 'delivery_user_name',
    width: 90,
    render: (row) => (row.delivery_type === DELIVERY_TYPE.DELIVERY ? row.delivery_user_name || '-' : '-'),
  },
  {
    title: '订单金额',
    key: 'total_amount',
    width: 110,
    render: (row) => `￥${formatAmount(row.total_amount)}`,
  },
  {
    title: '实付金额',
    key: 'pay_amount',
    width: 110,
    render: (row) => `￥${formatAmount(row.pay_amount)}`,
  },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render: (row) =>
      h(NSpace, { size: 4, vertical: true, wrap: true }, {
        default: () => [
          h(NTag, { type: orderStatusTagType(row.status), size: 'small', bordered: false }, { default: () => row.status_text }),
          // 用户已从自己的订单列表删除（软删）：后台仍全量可见，这里标出来避免误判为脏数据
          row.user_deleted === 1
            ? h(NTag, { type: 'warning', size: 'small', bordered: false }, { default: () => '用户已删' })
            : null,
        ],
      }),
  },
  { title: '创建时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 280,
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
              { default: () => (row.delivery_user_id ? '改物流' : '发货') },
            ),
          )
          // 社区自配送：指派履约人员，C 端工作台送货（B）
          buttons.push(
            h(
              NButton,
              { size: 'small', type: 'info', secondary: true, onClick: () => openAssign(row) },
              { default: () => (row.delivery_user_id ? '改指派' : '指派') },
            ),
          )
        } else if (row.delivery_type === DELIVERY_TYPE.STORE) {
          // 到店单即买即走：收款即自动完成，正常不会停留在此；兜底提供"完成"入口（无取货码，不适用核销）
          buttons.push(
            h(
              NButton,
              { size: 'small', type: 'success', onClick: () => confirmComplete(row) },
              { default: () => '完成' },
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
      // 可退状态口径统一由 canAdminRefund 判定（已支付/备货中/配送中，以及到店单的已完成），与后端一致
      if (canAdminRefund(row)) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => openRefund(row.id) },
            { default: () => '退款' },
          ),
        )
      }
      // 取消订单：待支付恒可取消；货到付款单未发货（已支付/备货中）也可直接取消（与后端口径一致）
      if (canCancelOrder(row)) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => openCancel(row.id) },
            { default: () => '取消订单' },
          ),
        )
      }
      // 恢复被用户删除的订单：客服场景（用户误删后请求找回），只改可见性不动资金与状态
      if (row.user_deleted === 1) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'warning', onClick: () => confirmRestore(row) },
            { default: () => '恢复' },
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
 * 组装当前筛选条件（列表查询与导出共用，保证「所见即所导」）
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
    pay_no: filters.pay_no || undefined,
    user_id: filters.user_id > 0 ? filters.user_id : undefined,
    product_id: filters.product_id > 0 ? filters.product_id : undefined,
    start_time: start ? fmt(start) : undefined,
    end_time: end ? fmt(end) : undefined,
    status: statusFilter.value > 0 ? statusFilter.value : undefined,
    source: sourceFilter.value > 0 ? sourceFilter.value : undefined,
    shift_id: shiftFilter.value > 0 ? shiftFilter.value : undefined,
  }
}

/**
 * 组装查询条件的人读摘要（导出任务展示用），如「今天 · 待发货 · 会员张三」
 */
function buildQueryDesc() {
  const parts: string[] = []
  if (dateQuick.value) {
    parts.push(dateQuickOptions.find((q) => q.key === dateQuick.value)?.label ?? '')
  } else if (dateRange.value) {
    parts.push('自定义时间段')
  }
  const statusLabel = statusOptions.find((s) => s.value === statusFilter.value)?.label
  if (statusLabel) parts.push(statusLabel)
  if (filters.user_id > 0) parts.push(`会员${filters.user_name}`)
  if (filters.product_id > 0) parts.push(`商品${filters.product_name}`)
  if (filters.order_no) parts.push(`单号${filters.order_no}`)
  if (filters.pay_no) parts.push('账单号')
  if (sourceFilter.value > 0) parts.push(orderSourceFilterOptions.find((o) => o.value === sourceFilter.value)?.label ?? '')
  return parts.filter(Boolean).join(' · ') || '全部订单'
}

/**
 * 打开订单详情弹窗（含会员订单弹窗 open-order 事件转发；已开时重挂载置顶并重载）
 * @param id 订单 ID
 */
function openDetail(id: number) {
  detailId.value = id
  reopenModal(detailVisible)
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

// --- 指派配送（B 履约工作台） ---
const assignVisible = ref(false)
const assignOrder = ref<Order | null>(null)
const assignUserId = ref<number | null>(null)
const assignLoading = ref(false)
const assignLoadingList = ref(false)
const fulfillers = ref<{ user_id: number; nickname: string; phone: string }[]>([])

/**
 * 打开指派配送弹窗（加载本社区履约人员列表）
 * @param row 订单行
 */
async function openAssign(row: Order) {
  assignOrder.value = row
  assignUserId.value = row.delivery_user_id && row.delivery_user_id > 0 ? row.delivery_user_id : null
  assignVisible.value = true
  assignLoadingList.value = true
  try {
    const res = await getFulfillers()
    fulfillers.value = res.data.data ?? []
  } finally {
    assignLoadingList.value = false
  }
}

/**
 * 提交指派：写入履约人员（服务端校验履约身份与社区归属）
 */
async function submitAssign() {
  const row = assignOrder.value
  if (!row) return
  if (!assignUserId.value) {
    message.warning('请选择履约人员')
    return false
  }
  assignLoading.value = true
  try {
    const res = await assignDelivery(row.id, assignUserId.value)
    message.success(`已指派给 ${res.data.data.delivery_user_name || '履约人员'}`)
    assignVisible.value = false
    loadData()
    return true
  } catch {
    return false
  } finally {
    assignLoading.value = false
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
 * 恢复被用户删除的订单
 * 软删只是把订单从用户自己的列表隐藏（资金、状态、售后一律不变），
 * 恢复后订单重新出现在用户的「我的订单」里；错误提示由请求拦截器统一弹出
 * @param row 订单行
 */
function confirmRestore(row: Order) {
  dialog.warning({
    title: '恢复订单',
    content: `确认将订单 ${row.order_no} 恢复到用户的订单列表？该操作只改可见性，不影响订单状态与资金。`,
    positiveText: '确认恢复',
    negativeText: '取消',
    onPositiveClick: async () => {
      await restoreOrder(row.id)
      message.success('订单已恢复')
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
 * 创建后台导出任务（异步生成，创建即返回）
 * 文件生成完成后在右上角「下载中心」取文件，不再前端逐页拉取拼 CSV
 */
async function createExport() {
  exporting.value = true
  try {
    const query = buildQuery()
    await createOrderExportTask({
      order_no: query.order_no,
      status: query.status,
      start_time: query.start_time,
      end_time: query.end_time,
      receiver_name: query.receiver_name,
      receiver_phone: query.receiver_phone,
      user_id: query.user_id,
      product_id: query.product_id,
      pay_no: query.pay_no,
      shift_id: query.shift_id,
      source: query.source,
      query_desc: buildQueryDesc(),
    })
    message.success('导出任务已创建，生成完成后可在右上角「下载中心」下载')
  } catch {
    // 错误提示由请求拦截器统一弹出（含在途任务超限）
  } finally {
    exporting.value = false
  }
}

/** 列表最近一次加载完成的时间戳，供 NewDataAlert 归零陈旧提示 */
const lastLoadedAt = ref(0)

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
    // 通知「有新数据」提示条：屏幕上的列表已重新拉取，陈旧计数作废
    lastLoadedAt.value = Date.now()
  }
}

onMounted(() => {
  applyQueryStatus()
  applyQueryShift()
  loadData()
  loadStats()
  window.addEventListener('resize', updateNarrow)
})

// keep-alive 缓存下再次进入（如工作台待办/交班记录跳转）时，应用新的 query 参数重新筛选
onActivated(() => {
  const statusChanged = applyQueryStatus()
  const shiftChanged = applyQueryShift()
  if (statusChanged || shiftChanged) {
    pagination.value.page = 1
    loadData()
  }
})

onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow)
  // 清理商品搜索防抖 timer，否则卸载后 300ms 仍会触发一次列表请求
  if (productSearchTimer) {
    clearTimeout(productSearchTimer)
    productSearchTimer = null
  }
})
</script>

<style scoped>
/* --- 筛选区（filter-card 分层布局） --- */

/* 筛选卡片：整体包一层浅描边，与统计卡片、表格视觉分区 */
.filter-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid #efeff5;
  border-radius: 8px;
  background: #fff;
}

/* 通用行：允许换行，行内控件纵向居中 */
.filter-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px 20px;
}

/* 单个条件项：label + 控件横排，保证同一行标签与输入框对齐 */
.filter-item {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

/* 条件标签 */
.f-label {
  font-size: 13px;
  color: #666;
  white-space: nowrap;
}

/* 下单时间项内部：日期选择器 + 快捷按钮组合 */
.f-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

/* 时间快捷按钮组 */
.quick-chips {
  display: flex;
  gap: 4px;
}

/* 订单号/收货人/电话输入框宽度 */
.w-order {
  width: 150px;
}

/* 账单号（支付单号/渠道交易号）稍宽，容纳支付宝交易号长度 */
.w-pay {
  width: 200px;
}

.date-picker {
  width: 280px;
}

.status-select {
  width: 100%;
}

.source-select {
  width: 110px;
}

/* 折叠筛选区：浅色底与主条件行区分 */
.more-area {
  background: #fafafc;
  border-radius: 6px;
  padding: 10px 12px;
}

/* 操作行右端对齐：查询/重置/导出靠右，更多筛选靠左 */
.actions .flex-spacer {
  flex: 1;
}

@media (max-width: 768px) {
  .w-order,
  .w-pay,
  .date-picker,
  .source-select {
    width: 100%;
  }

  .filter-item {
    flex: 1 1 100%;
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

/* 商品选择器列表（弹窗内）：行式布局，点击行即选中 */
.product-list {
  max-height: 380px;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
}

.product-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid #f3f3f7;
  cursor: pointer;
  transition: background 0.15s;
}

.product-row:last-child {
  border-bottom: none;
}

.product-row:hover {
  background: #f7f9fa;
}

.product-row .name {
  font-size: 14px;
  color: #333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.product-row .meta {
  font-size: 12px;
  color: #888;
  white-space: nowrap;
}
</style>
