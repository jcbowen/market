<template>
  <div class="dashboard-page">
    <!-- 欢迎横幅 -->
    <div class="welcome-banner">
      <div class="welcome-left">
        <div class="welcome-title">{{ greeting }}，{{ adminName }}</div>
        <div class="welcome-sub">{{ todayText }}，今天也要加油鸭～</div>
      </div>
      <n-space :size="12" class="welcome-actions">
        <n-button
          v-if="can('product:create')"
          color="#ffffff"
          text-color="#059a4c"
          @click="go('/products/create')"
        >
          <template #icon><n-icon :component="AddCircleOutline" /></template>
          新建商品
        </n-button>
        <n-button
          v-if="can('order:view')"
          color="#ffffff"
          text-color="#059a4c"
          @click="go('/orders')"
        >
          <template #icon><n-icon :component="ListOutline" /></template>
          订单管理
        </n-button>
      </n-space>
    </div>

    <!-- 核心指标：今日经营概览，点击卡片跳转对应页面 -->
    <n-grid cols="1 s:2 m:4 l:4 xl:4 xxl:4" :x-gap="16" :y-gap="16" responsive="screen" class="stat-grid">
      <n-grid-item v-for="card in statCards" :key="card.label">
        <n-card size="small" :class="['stat-card', { clickable: !!card.path }]" @click="card.path && go(card.path)">
          <div class="stat-card-body">
            <div class="stat-icon" :style="{ background: card.bg, color: card.color }">
              <n-icon :size="22" :component="card.icon" />
            </div>
            <div class="stat-info">
              <div class="stat-label">{{ card.label }}</div>
              <div class="stat-value">
                <span v-if="card.prefix" class="stat-prefix">{{ card.prefix }}</span>{{ card.value }}
              </div>
              <div class="stat-extra">{{ card.extra }}</div>
            </div>
          </div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 待办中心：数字来自各业务统计口径，点击直达处理页面 -->
    <n-card size="small" class="section-card">
      <template #header>
        <div class="section-header">
          <n-icon :size="18" :component="FlashOutline" color="#fa9d3b" />
          <span>待办中心</span>
          <n-button v-if="hasTodo" quaternary size="tiny" type="primary" @click="goFirstTodo">处理最紧急</n-button>
        </div>
      </template>
      <n-grid cols="2 s:3 m:3 l:6 xl:6" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item v-for="todo in todoItems" :key="todo.key">
          <div :class="['todo-item', { urgent: todo.count > 0 }]" @click="go(todo.path)">
            <n-badge :value="todo.count" :show="todo.count > 0" :max="99" :color="todo.color">
              <div class="todo-icon" :style="{ background: todo.bg, color: todo.color }">
                <n-icon :size="20" :component="todo.icon" />
              </div>
            </n-badge>
            <div class="todo-label">{{ todo.label }}</div>
            <div class="todo-count" :style="{ color: todo.count > 0 ? todo.color : 'var(--n-text-color-disabled, #bbb)' }">
              {{ todo.count > 0 ? `${todo.count} 项待处理` : '暂无待办' }}
            </div>
          </div>
        </n-grid-item>
      </n-grid>
    </n-card>

    <div class="two-col">
      <!-- 近 7 日营收趋势 -->
      <n-card size="small" title="近 7 日营收" class="section-card">
        <template #header-extra>
          <n-button text size="small" type="primary" @click="go('/statistics')">
            统计报表
            <n-icon :size="14" :component="ChevronForwardOutline" />
          </n-button>
        </template>
        <div v-if="revenueMax > 0" class="trend-chart">
          <div v-for="item in revenueTrend" :key="item.date" class="trend-col">
            <div class="trend-amount">¥{{ formatAmount(item.revenue) }}</div>
            <div class="trend-bar-wrap">
              <div
                class="trend-bar"
                :style="{ height: trendBarHeight(item.revenue) }"
                :title="`${item.date}：营收 ¥${item.revenue.toFixed(2)}，${item.order_count} 单`"
              />
            </div>
            <div class="trend-orders">{{ item.order_count }} 单</div>
            <div class="trend-date">{{ item.label }}</div>
          </div>
        </div>
        <n-empty v-else description="近 7 日暂无营收数据" class="trend-empty" />
      </n-card>

      <!-- 热销商品 TOP5 -->
      <n-card size="small" title="近 7 日热销 TOP5" class="section-card">
        <template #header-extra>
          <n-button text size="small" type="primary" @click="go('/products')">
            商品管理
            <n-icon :size="14" :component="ChevronForwardOutline" />
          </n-button>
        </template>
        <div v-if="hotProducts.length" class="rank-list">
          <div v-for="(item, index) in hotProducts" :key="item.product_id" class="rank-item">
            <span :class="['rank-badge', `rank-${index + 1}`]">{{ index + 1 }}</span>
            <div class="rank-info">
              <div class="rank-name">{{ item.product_name }}</div>
              <n-progress
                type="line"
                :show-indicator="false"
                :height="6"
                :border-radius="3"
                :percentage="rankPercent(item.sales_count)"
                color="#07c160"
                rail-color="rgba(7, 193, 96, 0.12)"
              />
            </div>
            <div class="rank-num">
              <div class="rank-sales">{{ item.sales_count }} 件</div>
              <div class="rank-amount">¥{{ formatAmount(item.sales_amount) }}</div>
            </div>
          </div>
        </div>
        <n-empty v-else description="暂无销售数据" class="trend-empty" />
      </n-card>
    </div>

    <!-- 快捷入口 -->
    <n-card size="small" title="快捷入口" class="section-card">
      <div class="quick-list">
        <div v-for="action in quickActions" :key="action.path" class="quick-item" @click="go(action.path)">
          <div class="quick-icon" :style="{ background: action.bg, color: action.color }">
            <n-icon :size="20" :component="action.icon" />
          </div>
          <span class="quick-label">{{ action.label }}</span>
        </div>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'Dashboard' })
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  AddCircleOutline,
  AlertCircleOutline,
  AirplaneOutline,
  ArrowUndoOutline,
  CartOutline,
  CashOutline,
  ChevronForwardOutline,
  CubeOutline,
  FlashOutline,
  GiftOutline,
  GridOutline,
  ListOutline,
  PeopleOutline,
  PricetagsOutline,
  ReceiptOutline,
  StatsChartOutline,
  StorefrontOutline,
  SyncOutline,
  TimeOutline,
} from '@vicons/ionicons5'
import { getDashboardStatistics, getOrderStatistics, getProductRankings, getRevenueReport } from '@/api/statistics'
import { getRefundList } from '@/api/order'
import { getStockWarnings } from '@/api/inventory'
import type { ProductRankingItem, RevenueReportItem } from '@/api/statistics'
import { useAuthStore } from '@/stores/auth'

/**
 * 工作台首页
 * - 核心指标：今日订单/销售额/用户/商品（数据来自 dashboard 聚合接口）
 * - 待办中心：订单状态统计 + 售后单分页 total + 库存预警列表，点击带 status 参数直达列表页
 * - 近 7 日营收趋势 / 热销 TOP5：复用统计报表接口
 * - 快捷入口：按当前管理员权限过滤
 */

const router = useRouter()
const authStore = useAuthStore()

// --- 权限与身份 ---

const adminName = computed(() => authStore.admin?.nickname || authStore.admin?.username || '管理员')

/**
 * 当前管理员是否拥有指定权限（超管/通配在 store 内处理）
 */
function can(code: string): boolean {
  return authStore.hasPermission(code)
}

/**
 * 权限校验后跳转：无权限不跳（避免落 403 页）
 * @param path 目标路由路径
 */
function go(path: string) {
  router.push(path)
}

/** 按时段问候语 */
const greeting = computed(() => {
  const h = new Date().getHours()
  if (h < 6) return '夜深了'
  if (h < 12) return '上午好'
  if (h < 14) return '中午好'
  if (h < 18) return '下午好'
  return '晚上好'
})

const todayText = computed(() => {
  const now = new Date()
  const weekdays = ['日', '一', '二', '三', '四', '五', '六']
  return `${now.getFullYear()} 年 ${now.getMonth() + 1} 月 ${now.getDate()} 日 星期${weekdays[now.getDay()]}`
})

// --- 核心指标 ---

const statistics = reactive({
  order_count: 0,
  sale_amount: 0,
  user_count: 0,
  product_count: 0,
})

/** 今日客单价（无订单时显示 --） */
const avgPrice = computed(() => {
  if (!statistics.order_count) return '--'
  return `¥${formatAmount(statistics.sale_amount / statistics.order_count)}`
})

const statCards = computed(() => [
  {
    label: '今日订单数',
    value: statistics.order_count,
    prefix: '',
    icon: CartOutline,
    bg: 'rgba(7, 193, 96, 0.1)',
    color: '#07c160',
    extra: '今日全部来源订单',
    path: can('order:view') ? '/orders' : '',
  },
  {
    label: '今日销售额',
    value: formatAmount(statistics.sale_amount),
    prefix: '¥',
    icon: CashOutline,
    bg: 'rgba(250, 157, 59, 0.12)',
    color: '#fa9d3b',
    extra: `客单价 ${avgPrice.value}`,
    path: can('statistics:view') ? '/statistics' : '',
  },
  {
    label: '注册会员',
    value: statistics.user_count,
    prefix: '',
    icon: PeopleOutline,
    bg: 'rgba(16, 174, 255, 0.12)',
    color: '#10aeff',
    extra: '当前社区累计会员',
    path: can('user:view') ? '/users' : '',
  },
  {
    label: '在售商品',
    value: statistics.product_count,
    prefix: '',
    icon: CubeOutline,
    bg: 'rgba(124, 108, 240, 0.1)',
    color: '#7c6cf0',
    extra: '当前上架销售中',
    path: can('product:view') ? '/products' : '',
  },
])

// --- 待办中心 ---

interface TodoItem {
  key: string
  label: string
  count: number
  path: string
  icon: any
  color: string
  bg: string
}

/** 订单状态统计（全量口径，与订单列表页概览卡片一致） */
const orderStats = reactive({ pending_pay: 0, pending_ship: 0, pending_receive: 0 })
/** 售后待审核（退款单 status=1）与退款中（status=4） */
const refundPending = ref(0)
const refundProcessing = ref(0)
/** 库存预警 SKU 数 */
const stockWarningCount = ref(0)

const todoItems = computed<TodoItem[]>(() => [
  {
    key: 'pending_pay',
    label: '待支付订单',
    count: orderStats.pending_pay,
    path: '/orders?status=1',
    icon: TimeOutline,
    color: '#faad14',
    bg: 'rgba(250, 173, 20, 0.1)',
  },
  {
    key: 'pending_ship',
    label: '待发货订单',
    count: orderStats.pending_ship,
    path: '/orders?status=90',
    icon: AirplaneOutline,
    color: '#07c160',
    bg: 'rgba(7, 193, 96, 0.1)',
  },
  {
    key: 'pending_receive',
    label: '待收货订单',
    count: orderStats.pending_receive,
    path: '/orders?status=4',
    icon: StorefrontOutline,
    color: '#10aeff',
    bg: 'rgba(16, 174, 255, 0.12)',
  },
  {
    key: 'refund_pending',
    label: '售后待审核',
    count: refundPending.value,
    path: '/refunds?status=1',
    icon: ArrowUndoOutline,
    color: '#fa5151',
    bg: 'rgba(250, 81, 81, 0.08)',
  },
  {
    key: 'refund_processing',
    label: '退款中',
    count: refundProcessing.value,
    path: '/refunds?status=4',
    icon: SyncOutline,
    color: '#ff7a45',
    bg: 'rgba(255, 122, 69, 0.1)',
  },
  {
    key: 'stock_warning',
    label: '库存预警',
    count: stockWarningCount.value,
    path: '/stock/warnings',
    icon: AlertCircleOutline,
    color: '#7c6cf0',
    bg: 'rgba(124, 108, 240, 0.1)',
  },
])

/** 是否存在待办项 */
const hasTodo = computed(() => todoItems.value.some((t) => t.count > 0))

/** 跳转到第一个有待办的页面 */
function goFirstTodo() {
  const first = todoItems.value.find((t) => t.count > 0)
  if (first) go(first.path)
}

// --- 近 7 日营收趋势 ---

const revenueTrend = ref<TrendItem[]>([])

/** 趋势单项：在 RevenueReportItem 基础上补充展示用 label */
interface TrendItem extends RevenueReportItem {
  label: string
}

/** 营收最大值（柱高比例基准） */
const revenueMax = computed(() => Math.max(0, ...revenueTrend.value.map((i) => i.revenue)))

/**
 * 补齐 7 天日期序列：后端仅返回有数据的日期，缺的日子补零
 */
function buildRevenueTrend(list: RevenueReportItem[]) {
  const byDate = new Map(list.map((i) => [i.date, i]))
  const days: TrendItem[] = []
  for (let i = 6; i >= 0; i--) {
    const d = new Date()
    d.setDate(d.getDate() - i)
    const mm = String(d.getMonth() + 1).padStart(2, '0')
    const dd = String(d.getDate()).padStart(2, '0')
    const date = `${d.getFullYear()}-${mm}-${dd}`
    const hit = byDate.get(date)
    days.push({
      date,
      revenue: hit?.revenue ?? 0,
      order_count: hit?.order_count ?? 0,
      label: '',
    })
  }
  // label：今天显示"今天"，昨天显示"昨天"，其余显示 MM-DD
  days.forEach((day, idx) => {
    if (idx === 6) day.label = '今天'
    else if (idx === 5) day.label = '昨天'
    else day.label = day.date.slice(5)
  })
  revenueTrend.value = days
}

/**
 * 柱高：相对最大营收按比例，最小留 4px 可视高度
 */
function trendBarHeight(revenue: number): string {
  if (revenueMax.value <= 0) return '4px'
  return `${Math.max(4, Math.round((revenue / revenueMax.value) * 120))}px`
}

// --- 热销 TOP5 ---

const hotProducts = ref<ProductRankingItem[]>([])

/** 销量进度条百分比（以销量第一为 100%） */
function rankPercent(salesCount: number): number {
  const max = Math.max(1, ...hotProducts.value.map((i) => i.sales_count))
  return Math.max(4, Math.round((salesCount / max) * 100))
}

// --- 快捷入口 ---

const quickActions = computed(() => {
  const actions: Array<{ label: string; path: string; icon: any; color: string; bg: string; perm: string }> = [
    { label: '商品管理', path: '/products', icon: GridOutline, color: '#07c160', bg: 'rgba(7,193,96,0.1)', perm: 'product:view' },
    { label: '新建商品', path: '/products/create', icon: PricetagsOutline, color: '#fa9d3b', bg: 'rgba(250,157,59,0.12)', perm: 'product:create' },
    { label: '订单列表', path: '/orders', icon: ListOutline, color: '#10aeff', bg: 'rgba(16,174,255,0.12)', perm: 'order:view' },
    { label: '售后管理', path: '/refunds', icon: ReceiptOutline, color: '#fa5151', bg: 'rgba(250,81,81,0.08)', perm: 'refund:view' },
    { label: '会员管理', path: '/users', icon: PeopleOutline, color: '#7c6cf0', bg: 'rgba(124,108,240,0.1)', perm: 'user:view' },
    { label: '优惠券', path: '/marketing/coupons', icon: GiftOutline, color: '#fa9d3b', bg: 'rgba(250,157,59,0.12)', perm: 'marketing:view' },
    { label: '库存盘点', path: '/stock/check', icon: CubeOutline, color: '#10aeff', bg: 'rgba(16,174,255,0.12)', perm: 'inventory:view' },
    { label: '数据统计', path: '/statistics', icon: StatsChartOutline, color: '#07c160', bg: 'rgba(7,193,96,0.1)', perm: 'statistics:view' },
  ]
  return actions.filter((a) => can(a.perm))
})

// --- 数据加载 ---

/**
 * 金额格式化：千分位 + 最多两位小数（不补零，减少视觉噪音）
 */
function formatAmount(value: number): string {
  return value.toLocaleString('zh-CN', { minimumFractionDigits: 0, maximumFractionDigits: 2 })
}

/**
 * 并发加载工作台各区块数据
 * 使用 allSettled：单个接口失败只置空对应区块，不影响其他区块渲染
 */
async function loadAll() {
  const sevenDaysAgo = (() => {
    const d = new Date()
    d.setDate(d.getDate() - 6)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  })()
  const today = (() => {
    const d = new Date()
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  })()

  const [dashRes, orderRes, refundPendingRes, refundProcessingRes, stockRes, revenueRes, rankRes] = await Promise.allSettled([
    getDashboardStatistics(),
    getOrderStatistics({}),
    getRefundList({ page: 1, page_size: 1, status: 1 }),
    getRefundList({ page: 1, page_size: 1, status: 4 }),
    getStockWarnings(),
    getRevenueReport({ start_date: sevenDaysAgo, end_date: today, group_by: 'day' }),
    getProductRankings({ start_date: sevenDaysAgo, end_date: today, limit: 5 }),
  ])

  if (dashRes.status === 'fulfilled' && dashRes.value.data.code === 0) {
    Object.assign(statistics, dashRes.value.data.data)
  }
  if (orderRes.status === 'fulfilled' && orderRes.value.data.code === 0) {
    orderStats.pending_pay = orderRes.value.data.data.pending_pay
    orderStats.pending_ship = orderRes.value.data.data.pending_ship
    orderStats.pending_receive = orderRes.value.data.data.pending_receive
  }
  if (refundPendingRes.status === 'fulfilled' && refundPendingRes.value.data.code === 0) {
    refundPending.value = refundPendingRes.value.data.data.total
  }
  if (refundProcessingRes.status === 'fulfilled' && refundProcessingRes.value.data.code === 0) {
    refundProcessing.value = refundProcessingRes.value.data.data.total
  }
  if (stockRes.status === 'fulfilled' && stockRes.value.data.code === 0) {
    stockWarningCount.value = stockRes.value.data.data?.length ?? 0
  }
  if (revenueRes.status === 'fulfilled' && revenueRes.value.data.code === 0) {
    buildRevenueTrend(revenueRes.value.data.data || [])
  }
  if (rankRes.status === 'fulfilled' && rankRes.value.data.code === 0) {
    hotProducts.value = rankRes.value.data.data || []
  }
}

let firstActivated = true
onMounted(loadAll)

// keep-alive 缓存下，从其他页面切回工作台时刷新待办与指标
onActivated(() => {
  if (firstActivated) {
    firstActivated = false
    return
  }
  loadAll()
})
</script>

<style lang="scss" scoped>
.dashboard-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 双列区（趋势/热销）：naive-ui n-grid 单断点 cols 存在静默不渲染问题，改用 CSS grid */
.two-col {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;

  @media (min-width: 1024px) {
    grid-template-columns: 1fr 1fr;
  }
}

/* 欢迎横幅 */
.welcome-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding: 22px 26px;
  border-radius: 8px;
  background: linear-gradient(120deg, #059a4c 0%, #07c160 55%, #2bd477 100%);
  color: #fff;
}

.welcome-title {
  font-size: 20px;
  font-weight: 600;
}

.welcome-sub {
  margin-top: 6px;
  font-size: 13px;
  opacity: 0.85;
}

/* 核心指标卡片 */
.stat-card {
  cursor: default;
  transition: box-shadow 0.2s, transform 0.2s;

  &.clickable:hover {
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
    transform: translateY(-2px);
    cursor: pointer;
  }
}

.stat-card-body {
  display: flex;
  align-items: center;
  gap: 14px;
}

.stat-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: 10px;
  flex-shrink: 0;
}

.stat-label {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.45);
}

.stat-value {
  margin-top: 2px;
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
  color: rgba(0, 0, 0, 0.85);
  white-space: nowrap;
}

.stat-prefix {
  font-size: 14px;
  font-weight: 500;
  margin-right: 2px;
}

.stat-extra {
  margin-top: 2px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.4);
}

/* 区块卡片 */
.section-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 待办中心 */
.todo-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 14px 8px;
  border-radius: 8px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  cursor: pointer;
  transition: box-shadow 0.2s, transform 0.2s, border-color 0.2s;

  &:hover {
    border-color: #07c160;
    box-shadow: 0 4px 12px rgba(7, 193, 96, 0.12);
    transform: translateY(-2px);
  }

  &.urgent .todo-label {
    font-weight: 600;
  }
}

.todo-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
}

.todo-label {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.7);
}

.todo-count {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
}

/* 营收趋势 */
.trend-chart {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding: 8px 4px 0;
}

.trend-col {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.trend-amount {
  font-size: 11px;
  color: rgba(0, 0, 0, 0.55);
  white-space: nowrap;
}

.trend-bar-wrap {
  height: 124px;
  display: flex;
  align-items: flex-end;
  width: 100%;
  justify-content: center;
}

.trend-bar {
  width: 55%;
  max-width: 32px;
  min-height: 4px;
  border-radius: 4px 4px 2px 2px;
  background: linear-gradient(180deg, #2bd477, #07c160);
  transition: height 0.3s;
}

.trend-orders {
  font-size: 11px;
  color: rgba(0, 0, 0, 0.45);
}

.trend-date {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.65);
}

.trend-empty {
  padding: 40px 0;
}

/* 热销排行 */
.rank-list {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.rank-item {
  display: flex;
  align-items: center;
  gap: 12px;
}

.rank-badge {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.55);
  background: rgba(0, 0, 0, 0.06);
  flex-shrink: 0;

  &.rank-1 {
    color: #fff;
    background: #faad14;
  }

  &.rank-2 {
    color: #fff;
    background: #8c8c8c;
  }

  &.rank-3 {
    color: #fff;
    background: #b46225;
  }
}

.rank-info {
  flex: 1;
  min-width: 0;
}

.rank-name {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.8);
  margin-bottom: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rank-num {
  text-align: right;
  flex-shrink: 0;
}

.rank-sales {
  font-size: 13px;
  font-weight: 500;
  color: #07c160;
}

.rank-amount {
  margin-top: 2px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
}

/* 快捷入口 */
.quick-list {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.quick-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-radius: 8px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  cursor: pointer;
  transition: border-color 0.2s, box-shadow 0.2s;

  &:hover {
    border-color: #07c160;
    box-shadow: 0 2px 10px rgba(7, 193, 96, 0.12);
  }
}

.quick-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
}

.quick-label {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.75);
}
</style>
