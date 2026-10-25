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

      <!-- 散客单补绑会员：收款后顾客要求把消费挂到会员名下（积分等权益前置）；
           绑定后保留只读状态展示（不可换绑） -->
      <n-alert v-if="showBindBar" type="info" :bordered="false" class="bind-tip">
        <template v-if="(receipt.user_id ?? 0) === 0">
          当前为散客订单，如顾客要求计入会员（积分等），可补绑会员名下
          <n-button size="tiny" type="primary" :disabled="binding" @click="showUserPicker = true">
            绑定会员
          </n-button>
        </template>
        <template v-else>已绑定会员：{{ receipt.user_name || receipt.member_label }}（绑定后不可更换）</template>
      </n-alert>

      <n-button type="primary" block size="large" class="print-btn" @click="printReceipt">打印小票</n-button>
      <n-text depth="3" style="font-size: 12px">订单已按「收银台」来源记录，可在「订单查询」Tab 处理</n-text>
    </div>

    <!-- 会员选择（补绑，仅散客单） -->
    <user-picker
      v-if="canBind"
      v-model:show="showUserPicker"
      title="补绑会员"
      @select="onSelectMember"
    />
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { bindOrderUser } from '@/api/order'
import type { User } from '@/api/types'
import UserPicker from '@/components/UserPicker.vue'
import { useAuthStore } from '@/stores/auth'
import { formatAmount } from '@/utils/format'

/**
 * 收银成功弹窗（58mm 热敏小票预览与打印）
 * 商品下单 / 直接收款两路收款共用：快照由父组件组装后传入，
 * 无商品直接收款单 items 为空，小票自动省略商品明细段。
 * 散客单（user_id=0）在弹窗内提供「绑定会员」补录入口（2026-10-25）：
 * 顾客收款后要求挂账到会员名下时当场补绑，绑定后不可换绑（后端口径）
 */

/** 小票快照（与 OrderTab 原 PaidReceipt 口径一致，商品下单/直接收款共用） */
export interface PaidReceipt {
  order_no: string
  /** 订单 ID（>0 且 user_id=0 时开放「绑定会员」补录入口） */
  order_id?: number
  /** 订单归属会员 ID（0/未传=散客） */
  user_id?: number
  /** 绑定后的会员名（绑定成功后回显） */
  user_name?: string
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

const props = defineProps<{
  /** 弹窗展示状态 */
  show: boolean
  /** 小票快照（null 时不渲染内容） */
  receipt: PaidReceipt | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
}>()

const message = useMessage()
const authStore = useAuthStore()

/** 绑定请求进行中（防重复提交） */
const binding = ref(false)
/** 会员选择弹窗 */
const showUserPicker = ref(false)

/**
 * 绑定状态条展示：有订单 ID 且有订单处理权限即显示（不依赖绑定状态），
 * 绑定后转为只读「已绑定会员」展示，避免提示条消失让人误以为绑定丢失
 */
const showBindBar = computed(
  () =>
    !!props.receipt &&
    (props.receipt.order_id ?? 0) > 0 &&
    (authStore.isSuperAdmin || authStore.hasPermission('order:update')),
)

/** 补绑操作可用：状态条可见且仍是散客（user_id=0，绑后不可换绑） */
const canBind = computed(
  () => showBindBar.value && (props.receipt?.user_id ?? 0) === 0,
)

/**
 * 选择会员并补绑到本单：成功后更新小票客户行并提示；绑后不可换绑（后端拒绝）
 * @param u 选中的会员
 */
async function onSelectMember(u: User) {
  if (!props.receipt || binding.value) return
  binding.value = true
  try {
    const res = await bindOrderUser(props.receipt.order_id!, u.id)
    if (res.data.code !== 0) return
    showUserPicker.value = false
    props.receipt.user_id = u.id
    props.receipt.user_name = u.nickname || u.phone || `会员#${u.id}`
    props.receipt.member_label = props.receipt.user_name
    message.success(`已绑定到会员 ${props.receipt.user_name}，该单后续计入其名下`)
  } finally {
    binding.value = false
  }
}

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
.bind-tip {
  font-size: 12px;
  :deep(.n-button) {
    margin-left: 6px;
  }
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
