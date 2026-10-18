<template>
  <div class="shifts-page">
    <div class="page-toolbar">
      <n-space justify="space-between" align="center">
        <n-radio-group v-model:value="rangeKey" size="small" @update:value="onRangeChange">
          <n-radio-button value="7d">近 7 天</n-radio-button>
          <n-radio-button value="30d">近 30 天</n-radio-button>
          <n-radio-button value="all">全部</n-radio-button>
        </n-radio-group>
        <!-- 返回收银台：交班记录页此前无回程入口，收银员看完记录无法回工作台 -->
        <n-button size="small" @click="backToCashier">
          <template #icon><n-icon><ArrowBackOutline /></n-icon></template>
          返回收银台
        </n-button>
      </n-space>
    </div>

    <div class="shifts-body">
      <!-- 左：班次列表 -->
      <div class="shift-list">
        <n-spin :show="loading">
          <n-empty v-if="!shifts.length && !loading" description="暂无交班记录" class="empty" />
          <div
            v-for="shift in shifts"
            :key="shift.id"
            class="shift-item"
            :class="{ active: shift.id === activeId }"
            @click="activeId = shift.id"
          >
            <div class="shift-item-top">
              <span class="shift-date">{{ formatDay(shift.start_time) }}</span>
              <n-tag :type="shift.status === SHIFT_ACTIVE ? 'success' : 'default'" size="tiny" :bordered="false">
                {{ shift.status === SHIFT_ACTIVE ? '进行中' : '已交班' }}
              </n-tag>
            </div>
            <div class="shift-item-meta">{{ formatTime(shift.start_time) }} - {{ shift.end_time ? formatTime(shift.end_time) : '至今' }}</div>
            <div class="shift-item-amount">
              <span>收款 ￥{{ (shift.statistics?.total_income ?? 0).toFixed(2) }}</span>
              <span v-if="shift.statistics?.total_refund" class="refund">退 ￥{{ shift.statistics.total_refund.toFixed(2) }}</span>
            </div>
          </div>
        </n-spin>
        <div v-if="total > pageSize" class="list-more">
          <n-button size="tiny" quaternary :loading="loading" @click="loadMore">加载更多</n-button>
        </div>
      </div>

      <!-- 右：班次详情（小票样式） -->
      <div class="shift-detail">
        <n-empty v-if="!activeShift" description="选择左侧班次查看详情" class="empty" />
        <template v-else>
          <div class="receipt" id="shift-receipt">
            <div class="receipt-title">交班小票</div>
            <div class="receipt-line">收银员：{{ activeShift.operator_name }}</div>
            <div class="receipt-line">班次编号：#{{ activeShift.id }}</div>
            <div class="receipt-line">上班：{{ formatDateTime(activeShift.start_time) }}</div>
            <div class="receipt-line">交班：{{ activeShift.end_time ? formatDateTime(activeShift.end_time) : '进行中' }}</div>
            <div class="receipt-divider">- - - - - - - - - - - - - - - -</div>
            <div class="receipt-section">收款</div>
            <!-- 仅展示实际有数据的支付方式，按 SHIFT_PAY_TYPES 顺序排列 -->
            <div v-for="item in incomeRows" :key="'in' + item.pay_type" class="receipt-row" :class="{ muted: item.amount === 0 }">
              <span>{{ payTypeText(item.pay_type) }}</span>
              <span>￥{{ item.amount.toFixed(2) }}（{{ item.order_count }} 单）</span>
            </div>
            <div class="receipt-section">退款</div>
            <div v-for="item in refundRows" :key="'rf' + item.pay_type" class="receipt-row" :class="{ muted: item.amount === 0 }">
              <span>{{ payTypeText(item.pay_type) }}</span>
              <span>-￥{{ item.amount.toFixed(2) }}（{{ item.order_count }} 单）</span>
            </div>
            <div class="receipt-divider">- - - - - - - - - - - - - - - -</div>
            <div class="receipt-row total">
              <span>收款合计</span><span>￥{{ (activeShift.statistics?.total_income ?? 0).toFixed(2) }}</span>
            </div>
            <div class="receipt-row total">
              <span>退款合计</span><span>￥{{ (activeShift.statistics?.total_refund ?? 0).toFixed(2) }}</span>
            </div>
            <div class="receipt-row total">
              <span>班内订单</span><span>{{ activeShift.statistics?.order_count ?? 0 }} 单</span>
            </div>
            <div class="receipt-row total strong">
              <span>现金合计</span><span>￥{{ (activeShift.statistics?.cash_total ?? 0).toFixed(2) }}</span>
            </div>
            <div v-if="activeShift.note" class="receipt-line note">备注：{{ activeShift.note }}</div>
            <div class="receipt-line muted center">打印时间：{{ printTimeText }}</div>
          </div>
          <n-space justify="center" class="detail-actions">
            <!-- 查看班内订单：跳订单列表按班次预筛（订单列表读取 shift_id query） -->
            <n-button size="small" type="primary" secondary @click="viewShiftOrders">查看班内订单</n-button>
            <n-button size="small" @click="printReceipt">打印小票</n-button>
          </n-space>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowBackOutline } from '@vicons/ionicons5'
import { getMyPosShifts } from '@/api/order'
import type { PosShift } from '@/api/types'
import { payStatRows, payTypeText } from '@/constants/order'

/** 班次状态：1=进行中 */
const SHIFT_ACTIVE = 1

const router = useRouter()

/** 时间范围 key */
type RangeKey = '7d' | '30d' | 'all'
const rangeKey = ref<RangeKey>('7d')

/** 班次列表与分页 */
const shifts = ref<PosShift[]>([])
const total = ref(0)
const pageSize = 20
const loading = ref(false)

/** 当前选中的班次 ID */
const activeId = ref(0)

/** 当前选中的班次详情 */
const activeShift = computed(() => shifts.value.find((shift) => shift.id === activeId.value) ?? null)

/** 详情小票收款统计行（仅含有数据的支付方式，按 SHIFT_PAY_TYPES 排序） */
const incomeRows = computed(() => payStatRows(activeShift.value?.statistics?.income))
/** 详情小票退款统计行（仅含有数据的支付方式，按 SHIFT_PAY_TYPES 排序） */
const refundRows = computed(() => payStatRows(activeShift.value?.statistics?.refund))

/** 打印时间文本（打开详情时定格，避免小票内容随渲染变化） */
const printTimeText = ref('')

/**
 * 返回收银台工作台
 */
function backToCashier() {
  router.push('/pos/cashier')
}

/**
 * 查看班内订单：跳转订单列表并按班次预筛（订单列表读取 shift_id query）
 */
function viewShiftOrders() {
  if (!activeShift.value) return
  router.push({ path: '/orders', query: { shift_id: String(activeShift.value.id) } })
}

/**
 * 时间范围对应的起始时间（全部为空不过滤）
 * @returns YYYY-MM-DD 00:00:00 或空
 */
function rangeStartTime(): string {
  if (rangeKey.value === 'all') return ''
  const days = rangeKey.value === '7d' ? 7 : 30
  const date = new Date(Date.now() - days * 86400000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} 00:00:00`
}

/**
 * 加载我的交班记录（首页）
 */
async function loadShifts() {
  loading.value = true
  try {
    const res = await getMyPosShifts({ page: 1, page_size: pageSize, start_time: rangeStartTime() })
    shifts.value = res.data.data.list
    total.value = res.data.data.total
    activeId.value = shifts.value[0]?.id ?? 0
    markPrintTime()
  } finally {
    loading.value = false
  }
}

/**
 * 加载更多（追加下一页）
 */
async function loadMore() {
  if (loading.value || shifts.value.length >= total.value) return
  loading.value = true
  try {
    const res = await getMyPosShifts({ page: Math.ceil(shifts.value.length / pageSize) + 1, page_size: pageSize, start_time: rangeStartTime() })
    shifts.value = [...shifts.value, ...res.data.data.list]
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

/**
 * 切换时间范围后重新加载
 */
function onRangeChange() {
  loadShifts()
}

/**
 * 解析后端时间字符串为 Date（兼容 "YYYY-MM-DD HH:mm:ss" 与 RFC3339）
 * @param time 后端时间字符串
 */
function parseDate(time: string): Date {
  return new Date(time.includes('T') ? time : time.replace(/-/g, '/'))
}

/**
 * 日期文本（MM-DD）
 * @param time 后端时间字符串
 */
function formatDay(time: string): string {
  const date = parseDate(time)
  if (!Number.isFinite(date.getTime())) return time
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/**
 * 时间文本（HH:mm）
 * @param time 后端时间字符串
 */
function formatTime(time: string): string {
  const date = parseDate(time)
  if (!Number.isFinite(date.getTime())) return time
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/**
 * 日期时间文本（YYYY-MM-DD HH:mm）
 * @param time 后端时间字符串（兼容 "YYYY-MM-DD HH:mm:ss" 与 RFC3339）
 */
function formatDateTime(time: string): string {
  const date = parseDate(time)
  if (!Number.isFinite(date.getTime())) return time
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/**
 * 定格当前打印时间文本
 */
function markPrintTime() {
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  printTimeText.value = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`
}

/**
 * 打印交班小票：取详情 DOM 快照写入打印窗口（58mm 热敏纸风格窄版）
 */
function printReceipt() {
  const el = document.getElementById('shift-receipt')
  if (!el || !activeShift.value) return
  markPrintTime()
  const win = window.open('', '_blank', 'width=320,height=600')
  if (!win) {
    return
  }
  win.document.write(`
    <html>
      <head><title>交班小票 #${activeShift.value.id}</title>
        <style>
          body { font-family: "Courier New", monospace; font-size: 12px; width: 220px; margin: 0 auto; padding: 8px; }
          .receipt-title { text-align: center; font-size: 15px; font-weight: bold; margin-bottom: 8px; }
          .receipt-line { margin: 3px 0; }
          .receipt-line.center { text-align: center; }
          .receipt-line.muted, .receipt-row.muted { color: #888; }
          .receipt-divider { margin: 6px 0; color: #888; }
          .receipt-section { font-weight: bold; margin: 6px 0 2px; }
          .receipt-row { display: flex; justify-content: space-between; margin: 2px 0; }
          .receipt-row.total { margin-top: 2px; }
          .receipt-row.total.strong { font-weight: bold; font-size: 13px; margin-top: 6px; }
          .receipt-line.note { margin-top: 6px; }
        </style>
      </head>
      <body>${el.innerHTML}</body>
    </html>
  `)
  win.document.close()
  win.focus()
  win.print()
}

onMounted(loadShifts)
</script>

<style lang="scss" scoped>
.shifts-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 14px;
  gap: 12px;
  box-sizing: border-box;
}
.page-toolbar {
  flex-shrink: 0;
}
.shifts-body {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 14px;
}
.shift-list {
  width: 300px;
  flex-shrink: 0;
  overflow: auto;
  background: #fff;
  border-radius: 8px;
  padding: 8px;
}
.shift-item {
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
  &:hover {
    background: #f5f6f8;
  }
  &.active {
    background: #e8f8ef;
  }
}
.shift-item-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.shift-date {
  font-size: 14px;
  font-weight: 600;
}
.shift-item-meta {
  font-size: 12px;
  color: #999;
  margin: 4px 0;
}
.shift-item-amount {
  display: flex;
  gap: 10px;
  font-size: 13px;
  .refund {
    color: #d03050;
  }
}
.list-more {
  text-align: center;
  padding: 6px 0;
}
.shift-detail {
  flex: 1;
  min-width: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 16px 0;
}
.receipt {
  width: 300px;
  background: #fff;
  border: 1px dashed #ddd;
  border-radius: 8px;
  padding: 18px 20px;
  font-size: 13px;
}
.receipt-title {
  text-align: center;
  font-size: 16px;
  font-weight: 700;
  margin-bottom: 10px;
}
.receipt-line {
  margin: 4px 0;
  &.center {
    text-align: center;
  }
  &.note {
    margin-top: 8px;
  }
}
.receipt-divider {
  margin: 8px 0;
  color: #bbb;
  text-align: center;
}
.receipt-section {
  font-weight: 600;
  margin: 6px 0 2px;
}
.receipt-row {
  display: flex;
  justify-content: space-between;
  margin: 3px 0;
  &.total {
    margin-top: 4px;
  }
  &.strong {
    font-weight: 700;
    color: #07c160;
    font-size: 14px;
  }
  &.muted {
    color: #ccc;
  }
}
.muted {
  color: #999;
}
.detail-actions {
  margin-top: 14px;
}
.empty {
  margin-top: 80px;
}
</style>
