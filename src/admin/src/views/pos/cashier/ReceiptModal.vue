<template>
  <n-modal
    :show="show"
    preset="card"
    title="收银成功"
    style="width: 420px; max-width: 92vw"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <div v-if="receipt" class="paid-wrap">
      <div class="paid-amount">￥{{ formatAmount(receipt.pay_amount) }}</div>
      <div class="paid-receipt" id="paid-receipt">
        <div class="r-title">收银小票</div>
        <div class="r-line">单号：{{ receipt.order_no }}</div>
        <div class="r-line">时间：{{ receipt.time }}</div>
        <div class="r-line">渠道：{{ receipt.channel_label }}</div>
        <div class="r-line">支付：{{ receipt.pay_label }}</div>
        <div class="r-line">客户：{{ receipt.member_label }}</div>
        <div v-if="receipt.pickup_code" class="r-line strong">取货码：{{ receipt.pickup_code }}</div>
        <template v-if="receipt.items.length">
          <div class="r-divider">- - - - - - - - - - - - - -</div>
          <div v-for="(it, idx) in receipt.items" :key="idx" class="r-row">
            <span>{{ it.name }}{{ it.spec ? `(${it.spec})` : '' }} x{{ it.qty }}</span>
            <span>￥{{ formatAmount(it.subtotal) }}</span>
          </div>
        </template>
        <div class="r-divider">- - - - - - - - - - - - - -</div>
        <div class="r-row"><span>商品合计</span><span>￥{{ formatAmount(receipt.total_amount) }}</span></div>
        <div v-if="receipt.coupon_discount > 0" class="r-row"><span>券优惠</span><span>-￥{{ formatAmount(receipt.coupon_discount) }}</span></div>
        <div v-if="receipt.manual_discount > 0" class="r-row"><span>手动优惠</span><span>-￥{{ formatAmount(receipt.manual_discount) }}</span></div>
        <div class="r-row total"><span>实收</span><span>￥{{ formatAmount(receipt.pay_amount) }}</span></div>
        <div class="r-line muted center">谢谢惠顾，欢迎再次光临</div>
      </div>
      <n-button type="primary" block size="large" class="print-btn" @click="printReceipt">打印小票</n-button>
      <n-text depth="3" style="font-size: 12px">订单已按「收银台」来源记录，可在「订单查询」Tab 处理</n-text>
    </div>
  </n-modal>
</template>

<script setup lang="ts">
import { formatAmount } from '@/utils/format'

/**
 * 收银成功弹窗（58mm 热敏小票预览与打印）
 * 商品下单 / 直接收款两路收款共用：快照由父组件组装后传入，
 * 无商品直接收款单 items 为空，小票自动省略商品明细段
 */

/** 小票快照（与 OrderTab 原 PaidReceipt 口径一致，商品下单/直接收款共用） */
export interface PaidReceipt {
  order_no: string
  pay_amount: number
  pickup_code?: string
  /** 渠道文案：到店消费 / 社区自提 / 送货上门 */
  channel_label: string
  pay_label: string
  member_label: string
  items: Array<{ name: string; spec: string; qty: number; subtotal: number }>
  total_amount: number
  coupon_discount: number
  manual_discount: number
  time: string
}

defineProps<{
  /** 弹窗展示状态 */
  show: boolean
  /** 小票快照（null 时不渲染内容） */
  receipt: PaidReceipt | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
}>()

/**
 * 打印收银小票：取弹窗内小票 DOM 快照写入打印窗口（58mm 热敏纸风格窄版，与交班小票同款）
 * 钱箱对接说明：钱箱经 RJ11 接小票打印机并在驱动开启「打印时弹钱箱」即可随打印自动弹开，无需代码对接
 */
function printReceipt() {
  const el = document.getElementById('paid-receipt')
  if (!el) return
  const win = window.open('', '_blank', 'width=320,height=600')
  if (!win) return
  win.document.write(`
    <html>
      <head><title>收银小票</title>
        <style>
          body { font-family: "Courier New", monospace; font-size: 12px; width: 220px; margin: 0 auto; padding: 8px; }
          .r-title { text-align: center; font-size: 15px; font-weight: bold; margin-bottom: 8px; }
          .r-line { margin: 3px 0; }
          .r-line.center { text-align: center; }
          .r-line.muted { color: #888; }
          .r-line.strong { font-weight: bold; font-size: 14px; }
          .r-divider { margin: 6px 0; color: #888; }
          .r-row { display: flex; justify-content: space-between; margin: 2px 0; }
          .r-row.total { font-weight: bold; font-size: 13px; margin-top: 6px; }
        </style>
      </head>
      <body>${el.innerHTML}</body>
    </html>
  `)
  win.document.close()
  win.focus()
  win.print()
}
</script>

<style lang="scss" scoped>
.paid-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.paid-amount {
  text-align: center;
  font-size: 34px;
  font-weight: 700;
  color: #d03050;
}
.paid-receipt {
  background: #fafafa;
  border-radius: 8px;
  padding: 14px 16px;
  font-family: 'Courier New', monospace;
  font-size: 12px;
}
.r-title {
  text-align: center;
  font-size: 15px;
  font-weight: bold;
  margin-bottom: 6px;
}
.r-line {
  margin: 3px 0;
  &.center {
    text-align: center;
  }
  &.muted {
    color: #888;
  }
  &.strong {
    font-weight: bold;
    font-size: 14px;
  }
}
.r-divider {
  margin: 6px 0;
  color: #888;
}
.r-row {
  display: flex;
  justify-content: space-between;
  margin: 2px 0;
  &.total {
    font-weight: bold;
    font-size: 13px;
    margin-top: 6px;
  }
}
</style>
