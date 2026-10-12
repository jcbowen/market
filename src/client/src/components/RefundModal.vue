<template>
  <u-popup :show="show" mode="bottom" :round="16" :close-on-click-overlay="!submitting" @close="onClose">
    <view class="refund-modal">
      <view class="refund-header">
        <text class="refund-title">申请退款</text>
        <u-icon name="close" size="20" color="#999999" @click="onClose" />
      </view>

      <scroll-view scroll-y class="refund-body">
        <!-- 商品明细：勾选 + 数量（默认全部=整单退） -->
        <view class="section-title">退款商品（默认整单退，可取消勾选或调低数量改为部分退）</view>
        <view v-for="(line, index) in lines" :key="line.order_item_id" class="refund-item">
          <view class="item-check" @click="toggleLine(index)">
            <view :class="['check-circle', line.checked ? 'checked' : '']">
              <u-icon v-if="line.checked" name="checkmark" size="12" color="#ffffff" />
            </view>
          </view>
          <view class="item-info">
            <text class="item-name">{{ line.product_name }}</text>
            <text class="item-spec">{{ line.spec_name }}</text>
            <text class="item-price">￥{{ formatAmount(line.line_full_refund) }} / {{ line.max_qty }}件</text>
          </view>
          <view class="item-qty">
            <u-number-box
              :modelValue="line.qty"
              :min="1"
              :max="line.max_qty"
              :disabled="!line.checked"
              integer
              @update:modelValue="(val: number) => changeQty(index, val)"
            />
          </view>
        </view>

        <!-- 运费（仅整单退时展示，按后台策略） -->
        <view v-if="isFullRefund && freight > 0" class="freight-row">
          <text>运费：￥{{ formatAmount(freight) }}</text>
          <text class="freight-policy">{{ freightPolicyText }}</text>
          <view v-if="policy === 'user_decide'" class="freight-check" @click="wantFreight = !wantFreight">
            <view :class="['check-circle', wantFreight ? 'checked' : '']">
              <u-icon v-if="wantFreight" name="checkmark" size="12" color="#ffffff" />
            </view>
            <text class="freight-check-text">同时退运费</text>
          </view>
        </view>

        <!-- 退款原因 -->
        <view class="section-title">退款原因</view>
        <view class="reason-input">
          <u-input v-model="reason" placeholder="请填写退款原因" border="surround" maxlength="100" />
        </view>

        <!-- 金额确认 -->
        <view class="amount-box">
          <view class="amount-line">
            <text class="amount-label">商品应退</text>
            <text>￥{{ formatAmount(goodsRefund) }}</text>
          </view>
          <view v-if="freightRefund > 0" class="amount-line">
            <text class="amount-label">运费应退</text>
            <text>￥{{ formatAmount(freightRefund) }}</text>
          </view>
          <view class="amount-line total">
            <text class="amount-label">合计应退</text>
            <text class="amount-value">￥{{ formatAmount(refundable) }}</text>
          </view>
          <view class="amount-edit">
            <text class="amount-label">退款金额（可调低）</text>
            <!-- uview-plus u-input 值变化 emit change（仅用户输入触发，外部赋值不触发），用 change 标记手动修改 -->
            <u-input
              v-model="amountInput"
              type="digit"
              border="surround"
              :placeholder="`不超过 ${formatAmount(refundable)}`"
              style="flex: 1"
              @change="onAmountInput"
            />
          </view>
          <text class="amount-tip">金额以商家审核确认为准，仅支持调低退款金额</text>
        </view>
      </scroll-view>

      <view class="refund-footer">
        <u-button type="success" text="提交申请" :loading="submitting" @click="submit" />
      </view>
    </view>
  </u-popup>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { getOrderDetail, getRefundConfig, applyRefund } from '@/api/order'
import type { Order, OrderItem } from '@/api/types'

/**
 * 申请退款弹窗组件
 * 展示退款商品明细（可选行/数量）、运费策略、汇算金额，支持手动调低退款金额
 */

/** 退款行状态 */
interface RefundLine {
  order_item_id: number
  product_name: string
  spec_name: string
  /** 行可退数量上限 */
  max_qty: number
  /** 当前申请退货数量 */
  qty: number
  checked: boolean
  /** 行全额应退（分摊优惠后） */
  line_full_refund: number
}

const props = defineProps<{
  /** 弹窗显示状态（v-model:show） */
  show: boolean
  /** 订单 ID */
  orderId: number
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const order = ref<Order | null>(null)
const lines = ref<RefundLine[]>([])
const policy = ref('always')
const wantFreight = ref(false)
const reason = ref('')
const amountInput = ref('')
const submitting = ref(false)
/** 用户是否手动修改过退款金额；为 true 后不再自动联动覆盖 */
const amountTouched = ref(false)

/** 运费金额（订单实收运费） */
const freight = computed(() => order.value?.delivery_fee ?? 0)

/** 是否整单退：全部行勾选且数量与行数量一致 */
const isFullRefund = computed(() => {
  if (!lines.value.length) return false
  return lines.value.every((line) => line.checked && line.qty === line.max_qty)
})

/** 运费策略展示文案 */
const freightPolicyText = computed(() => {
  if (policy.value === 'always') return '整单退款可退运费'
  if (policy.value === 'never') return '整单退款不退运费'
  return '由您选择是否退运费'
})

/** 本单应退运费 */
const freightRefund = computed(() => {
  if (!isFullRefund.value || freight.value <= 0) return 0
  if (policy.value === 'always') return freight.value
  if (policy.value === 'user_decide' && wantFreight.value) return freight.value
  return 0
})

/** 商品部分应退合计（服务端汇算为准，此处为预览） */
const goodsRefund = computed(() => {
  const o = order.value
  if (!o) return 0
  if (isFullRefund.value) {
    return round2(o.total_amount - o.discount_amount)
  }
  let sum = 0
  for (const line of lines.value) {
    if (line.checked) {
      sum += round2(line.line_full_refund * line.qty / line.max_qty)
    }
  }
  return round2(sum)
})

/** 合计应退金额（商品 + 运费） */
const refundable = computed(() => round2(goodsRefund.value + freightRefund.value))

/**
 * 金额保留两位小数
 * @param value 原始金额
 * @returns 两位小数金额
 */
function round2(value: number): number {
  return Math.round(value * 100) / 100
}

/**
 * 金额展示格式化
 * @param value 原始金额
 * @returns 两位小数字符串
 */
function formatAmount(value: number): string {
  return (value ?? 0).toFixed(2)
}

/**
 * 关闭弹窗
 */
function onClose() {
  emit('update:show', false)
}

/**
 * 切换商品行勾选
 * @param index 行索引
 */
function toggleLine(index: number) {
  lines.value[index].checked = !lines.value[index].checked
}

/**
 * 修改商品行退货数量
 * @param index 行索引
 * @param qty 目标数量
 */
function changeQty(index: number, qty: number) {
  lines.value[index].qty = qty
}

/**
 * 计算订单商品行全额应退金额（行小计减去按比例分摊的订单优惠）
 * @param item 订单商品行
 * @param o 所属订单
 * @returns 行全额应退金额
 */
function calcLineFullRefund(item: OrderItem, o: Order): number {
  if (!o.total_amount || o.total_amount <= 0) {
    return round2(item.total_price)
  }
  const share = round2(o.discount_amount * item.total_price / o.total_amount)
  return round2(item.total_price - share)
}

/**
 * 用户手动输入退款金额，标记不再自动联动覆盖
 */
function onAmountInput() {
  amountTouched.value = true
}

/**
 * 默认填入汇算金额，并重置为未手动修改状态
 */
function fillDefaultAmount() {
  amountTouched.value = false
  amountInput.value = formatAmount(refundable.value)
}

/**
 * 加载订单详情与退款配置，初始化退款行
 */
async function load() {
  submitting.value = false
  reason.value = ''
  amountInput.value = ''
  amountTouched.value = false
  wantFreight.value = false
  try {
    const [orderRes, configRes] = await Promise.all([
      getOrderDetail(props.orderId),
      getRefundConfig().catch(() => null),
    ])
    if (orderRes.data) {
      order.value = orderRes.data
      lines.value = (orderRes.data.items || []).map((item) => ({
        order_item_id: item.id,
        product_name: item.product_name,
        spec_name: item.sku_name,
        max_qty: item.quantity,
        qty: item.quantity,
        checked: true,
        line_full_refund: calcLineFullRefund(item, orderRes.data as Order),
      }))
    }
    if (configRes?.data?.refund_freight_policy) {
      policy.value = configRes.data.refund_freight_policy
    }
    fillDefaultAmount()
  } catch {
    // request 已统一提示错误
  }
}

/**
 * 提交退款申请
 * 整单退（全部行全数量）不传 items；手动调低金额时传 amount
 */
async function submit() {
  if (!reason.value.trim()) {
    uni.showToast({ title: '请填写退款原因', icon: 'none' })
    return
  }
  const o = order.value
  if (!o) return

  // 金额校验：仅允许调低（0 < amount <= 汇算值），为空按汇算值全额退
  let amount: number | undefined
  const input = parseFloat(amountInput.value)
  if (!Number.isNaN(input) && input > 0) {
    if (input > refundable.value + 0.001) {
      uni.showToast({ title: `退款金额不能高于 ￥${formatAmount(refundable.value)}`, icon: 'none' })
      return
    }
    if (input < refundable.value - 0.001) {
      amount = round2(input)
    }
  }

  // 部分退：传勾选行明细；整单退：items 置空由服务端汇算
  const full = isFullRefund.value
  const items = full
    ? undefined
    : lines.value
        .filter((line) => line.checked)
        .map((line) => ({ order_item_id: line.order_item_id, quantity: line.qty }))
  if (!full && (!items || items.length === 0)) {
    uni.showToast({ title: '请至少选择一件退款商品', icon: 'none' })
    return
  }

  submitting.value = true
  try {
    await applyRefund(o.id, {
      reason: reason.value.trim(),
      items,
      amount,
      refund_freight: full && policy.value === 'user_decide' ? wantFreight.value : false,
    })
    uni.showToast({ title: '已提交退款申请', icon: 'success' })
    onClose()
    emit('success')
  } catch {
    // request 已统一提示错误
  } finally {
    submitting.value = false
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

// 勾选行/数量变化导致汇算金额变化时，用户未手动修改过则跟随更新
watch(refundable, (value) => {
  if (!amountTouched.value) {
    amountInput.value = formatAmount(value)
  }
})
</script>

<style scoped>
.refund-modal {
  display: flex;
  flex-direction: column;
  max-height: 80vh;
  padding: 30rpx;
}

.refund-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.refund-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.refund-body {
  flex: 1;
  max-height: 56vh;
}

.section-title {
  margin: 20rpx 0 12rpx;
  font-size: 26rpx;
  color: #999999;
}

.refund-item {
  display: flex;
  align-items: center;
  padding: 16rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
}

.item-check {
  padding-right: 16rpx;
}

.check-circle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36rpx;
  height: 36rpx;
  border: 2rpx solid #dddddd;
  border-radius: 50%;
  background-color: #ffffff;
}

.check-circle.checked {
  border-color: #07c160;
  background-color: #07c160;
}

.item-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4rpx;
  min-width: 0;
}

.item-name {
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-spec {
  font-size: 24rpx;
  color: #999999;
}

.item-price {
  font-size: 24rpx;
  color: #ff4d4f;
}

.item-qty {
  padding-left: 12rpx;
}

.freight-row {
  display: flex;
  align-items: center;
  gap: 12rpx;
  margin-top: 16rpx;
  padding: 16rpx;
  font-size: 26rpx;
  color: #666666;
  border-radius: 12rpx;
  background-color: #f7f7f7;
}

.freight-policy {
  flex: 1;
  font-size: 24rpx;
  color: #999999;
}

.freight-check {
  display: flex;
  align-items: center;
  gap: 8rpx;
}

.freight-check-text {
  font-size: 26rpx;
  color: #333333;
}

.reason-input {
  margin-bottom: 8rpx;
}

.amount-box {
  margin-top: 20rpx;
  padding: 20rpx;
  border-radius: 12rpx;
  background-color: #f7f7f7;
}

.amount-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8rpx 0;
  font-size: 26rpx;
  color: #666666;
}

.amount-line.total {
  border-top: 1rpx solid #eeeeee;
  margin-top: 8rpx;
  padding-top: 16rpx;
}

.amount-label {
  color: #333333;
}

.amount-value {
  font-size: 32rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.amount-edit {
  display: flex;
  align-items: center;
  gap: 16rpx;
  margin-top: 12rpx;
  font-size: 26rpx;
}

.amount-tip {
  display: block;
  margin-top: 12rpx;
  font-size: 22rpx;
  color: #999999;
}

.refund-footer {
  padding-top: 20rpx;
}
</style>
