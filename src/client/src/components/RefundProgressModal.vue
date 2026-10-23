<template>
  <u-popup :show="show" mode="bottom" :round="16" @close="onClose">
    <view class="progress-modal">
      <view class="progress-header">
        <text class="progress-title">退款进度</text>
        <u-icon name="close" size="20" color="#999999" @click="onClose" />
      </view>

      <scroll-view scroll-y class="progress-body">
        <view v-if="loading" class="progress-empty">
          <text>加载中...</text>
        </view>
        <view v-else-if="!refunds.length" class="progress-empty">
          <text>暂无退款记录</text>
        </view>

        <view v-for="(refund, index) in refunds" :key="refund.id" class="refund-card">
          <!-- 金额与状态 -->
          <view class="refund-summary">
            <view class="summary-amount">
              <text class="amount-currency">￥</text>
              <text class="amount-value">{{ formatAmount(refund.amount) }}</text>
              <text class="amount-type">{{ typeText(refund.type) }}</text>
            </view>
            <text :class="['summary-status', `status-${refund.status}`]">{{ statusText(refund.status) }}</text>
          </view>

          <!-- 进度时间线 -->
          <view class="timeline">
            <view
              v-for="(step, i) in timelineSteps(refund)"
              :key="i"
              :class="['timeline-step', step.done ? 'done' : '', step.danger ? 'danger' : '']"
            >
              <view class="step-node" />
              <view class="step-content">
                <text class="step-title">{{ step.title }}</text>
                <text v-if="step.desc" class="step-desc">{{ step.desc }}</text>
                <text v-if="step.time" class="step-time">{{ step.time }}</text>
              </view>
            </view>
          </view>

          <!-- 退款原因 -->
          <view class="info-block">
            <view class="info-row">
              <text class="info-label">退款原因</text>
              <text class="info-value">{{ refund.reason || '-' }}</text>
            </view>
            <view v-if="refund.status === 3 && refund.reject_reason" class="info-row">
              <text class="info-label">拒绝原因</text>
              <text class="info-value reject">{{ refund.reject_reason }}</text>
            </view>
          </view>

          <!-- 退款商品明细（部分退/整单退均展示，便于核对） -->
          <view v-if="refund.items?.length" class="goods-list">
            <view v-for="(item, i) in refund.items" :key="i" class="goods-row">
              <text class="goods-name">{{ item.product_name }}<template v-if="item.spec_name">（{{ item.spec_name }}）</template></text>
              <text class="goods-qty">x{{ item.quantity }}</text>
            </view>
          </view>

          <!-- 多笔退款单分隔 -->
          <view v-if="index < refunds.length - 1" class="refund-divider" />
        </view>

        <view v-if="refunds.length" class="arrive-tip">
          <text>退款将原路返回至支付账户，到账时间以支付渠道为准（一般 1-3 个工作日）</text>
        </view>
      </scroll-view>
    </view>
  </u-popup>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { getOrderRefunds } from '@/api/order'
import type { RefundProgress } from '@/api/order'
import { formatPrice, formatDate } from '@/utils/format'

/**
 * 退款进度弹窗组件
 * 订单详情「退款进度」入口打开，按退款单展示状态时间线（待审核→退款中原路返回→已到账/已拒绝）
 */

const props = defineProps<{
  /** 弹窗显示状态（v-model:show） */
  show: boolean
  /** 订单 ID */
  orderId: number
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

const refunds = ref<RefundProgress[]>([])
const loading = ref(false)

/**
 * 退款状态文案
 * @param status 退款单状态
 * @returns 状态文案
 */
function statusText(status: number): string {
  const map: Record<number, string> = {
    1: '待审核',
    2: '已同意',
    3: '已拒绝',
    4: '退款中',
    5: '已到账',
  }
  return map[status] || '处理中'
}

/**
 * 退款类型文案
 * @param type 退款类型
 * @returns 类型文案
 */
function typeText(type: number): string {
  const map: Record<number, string> = {
    1: '整单退',
    2: '部分退',
    3: '仅金额退',
  }
  return map[type] || ''
}

/**
 * 构造退款单时间线步骤
 * 已拒绝单：申请 → 审核（拒绝，展示原因）终止；
 * 其余：申请 → 商家审核 → 退款中（已同意后）→ 已到账（有到账时间时）
 * @param refund 退款单
 * @returns 时间线步骤数组
 */
function timelineSteps(refund: RefundProgress) {
  const steps = [
    { title: '提交申请', desc: '', time: formatDate(refund.created_at), done: true, danger: false },
    {
      title: '商家审核',
      desc: refund.status === 3 && refund.reject_reason ? refund.reject_reason : '',
      time: '',
      done: refund.status >= 2,
      danger: refund.status === 3,
    },
  ]
  if (refund.status === 3) {
    return steps
  }
  steps.push({
    title: '退款处理',
    desc: '退款原路返回中',
    time: '',
    done: refund.status >= 4,
    danger: false,
  })
  steps.push({
    title: '已到账',
    desc: '',
    time: refund.refund_time ? formatDate(refund.refund_time) : '',
    done: refund.status >= 5,
    danger: false,
  })
  return steps
}

/**
 * 金额展示格式化（复用价格格式化并去掉前缀，模板自行渲染 ￥ 符号）
 * @param value 原始金额
 * @returns 两位小数字符串
 */
function formatAmount(value: number): string {
  return formatPrice(value, '')
}

/**
 * 关闭弹窗
 */
function onClose() {
  emit('update:show', false)
}

/**
 * 加载订单退款进度
 */
async function load() {
  loading.value = true
  try {
    const res = await getOrderRefunds(props.orderId)
    refunds.value = res.data?.refunds || []
  } catch {
    // request 已统一提示错误
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (visible) => {
    if (visible && props.orderId) {
      load()
    }
  },
)
</script>

<style scoped>
.progress-modal {
  display: flex;
  flex-direction: column;
  max-height: 75vh;
  padding: 30rpx 30rpx calc(30rpx + env(safe-area-inset-bottom));
}

.progress-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.progress-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.progress-body {
  flex: 1;
  max-height: 60vh;
}

.progress-empty {
  padding: 80rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: #999999;
}

.refund-card {
  padding: 24rpx;
  margin-bottom: 20rpx;
  border: 2rpx solid #eef2ee;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.refund-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.summary-amount {
  display: flex;
  align-items: baseline;
  color: #ff4d4f;
}

.amount-currency {
  font-size: 26rpx;
  font-weight: bold;
}

.amount-value {
  font-size: 40rpx;
  font-weight: bold;
}

.amount-type {
  margin-left: 12rpx;
  font-size: 22rpx;
  color: #999999;
}

.summary-status {
  font-size: 26rpx;
  font-weight: bold;
  color: #fa8c16;
}

/* 已到账/已同意绿色，已拒绝红色，待审核/退款中橙色 */
.summary-status.status-2,
.summary-status.status-5 {
  color: #07c160;
}

.summary-status.status-3 {
  color: #ff4d4f;
}

/* 时间线 */
.timeline {
  margin-bottom: 8rpx;
}

.timeline-step {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 16rpx;
  padding-bottom: 28rpx;
}

/* 连接线：节点圆点纵向贯穿 */
.timeline-step:not(:last-child)::before {
  content: '';
  position: absolute;
  left: 9rpx;
  top: 24rpx;
  bottom: 0;
  width: 2rpx;
  background-color: #e5e5e5;
}

.timeline-step.done:not(:last-child)::before {
  background-color: #07c160;
}

.step-node {
  flex-shrink: 0;
  width: 20rpx;
  height: 20rpx;
  margin-top: 4rpx;
  border: 4rpx solid #dddddd;
  border-radius: 50%;
  background-color: #ffffff;
  box-sizing: content-box;
}

.timeline-step.done .step-node {
  border-color: #07c160;
  background-color: #07c160;
}

.timeline-step.danger .step-node {
  border-color: #ff4d4f;
  background-color: #ff4d4f;
}

.step-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4rpx;
  min-width: 0;
}

.step-title {
  font-size: 26rpx;
  color: #999999;
}

.timeline-step.done .step-title {
  color: #333333;
}

.timeline-step.danger .step-title {
  color: #ff4d4f;
}

.step-desc {
  font-size: 24rpx;
  color: #666666;
}

.timeline-step.danger .step-desc {
  color: #ff4d4f;
}

.step-time {
  font-size: 22rpx;
  color: #bbbbbb;
}

/* 原因信息 */
.info-block {
  padding: 16rpx;
  border-radius: 12rpx;
  background-color: #f7f7f7;
}

.info-row {
  display: flex;
  gap: 20rpx;
  padding: 4rpx 0;
  font-size: 24rpx;
}

.info-label {
  flex-shrink: 0;
  color: #999999;
}

.info-value {
  flex: 1;
  color: #333333;
  word-break: break-all;
}

.info-value.reject {
  color: #ff4d4f;
}

/* 退款商品明细 */
.goods-list {
  margin-top: 16rpx;
}

.goods-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16rpx;
  padding: 6rpx 0;
  font-size: 24rpx;
  color: #666666;
}

.goods-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.goods-qty {
  flex-shrink: 0;
  color: #999999;
}

.refund-divider {
  height: 2rpx;
  margin-top: 24rpx;
  background-color: #f0f0f0;
}

.arrive-tip {
  padding: 8rpx 0 16rpx;
  font-size: 22rpx;
  line-height: 1.5;
  color: #999999;
  text-align: center;
}
</style>
