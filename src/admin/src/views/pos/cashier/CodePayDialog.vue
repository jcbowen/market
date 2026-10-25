<template>
  <n-modal
    :show="show"
    preset="card"
    title="付款码收款"
    style="width: 460px; max-width: 92vw"
    :mask-closable="false"
    :close-on-esc="state === 'idle'"
    @update:show="(v: boolean) => (v ? null : onRequestClose())"
  >
    <div class="codepay-wrap">
      <div class="codepay-amount">￥{{ formatAmount(amount) }}</div>
      <div class="codepay-order muted">{{ orderNo }}</div>
      <n-input
        ref="inputRef"
        v-model:value="authCode"
        size="large"
        placeholder="请扫顾客付款码（微信/支付宝）"
        :disabled="state !== 'idle'"
        @keydown.enter="submitAuthCode"
      />
      <div class="codepay-tip" :class="{ error: state === 'failed' }">
        <template v-if="state === 'idle'">光标在输入框内直接扫码即可，渠道按付款码自动识别</template>
        <template v-else-if="state === 'submitting'">扣款中，请勿重复扫码…</template>
        <template v-else-if="state === 'polling'">顾客支付中（等待输入密码），自动查询结果…</template>
        <template v-else-if="state === 'failed'">{{ failMessage || '扣款失败，请让顾客重新出示付款码' }}</template>
      </div>
      <n-space vertical :size="8">
        <n-button block quaternary :disabled="state !== 'idle'" @click="onRequestClose">
          暂不收款（订单保留为待支付）
        </n-button>
        <!-- 扣款失败改现金：关闭遗留支付单后收现金，订单立即完结；扣款在途/轮询中禁用，避免与现金收款并发双通道 -->
        <n-button
          block
          type="warning"
          secondary
          :loading="cashing"
          :disabled="state === 'submitting' || state === 'polling' || cashing"
          @click="onPayCash"
        >
          扣款失败？改用现金收款
        </n-button>
      </n-space>
    </div>
  </n-modal>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import { getPosOrderPayStatus, payPosOrderCode, payPosOrderCash } from '@/api/order'
import { formatAmount } from '@/utils/format'

/**
 * 付款码扫码收款弹窗（收银台通用）
 * 商品下单 / 直接收款 / 订单查询「继续收款」共用：传入待支付订单凭据即扫即收；
 * PAID 出小票（emit paid）、PAYING 轮询（2s 间隔、3 分钟超时）、FAILED 清码重扫；
 * 「改用现金收款」按钮：调 pay-cash 关旧单收现金（扣款失败换支付方式的核心出口）
 */

const props = defineProps<{
  /** 弹窗展示状态 */
  show: boolean
  /** 订单 ID */
  orderId: number
  /** 订单号（弹窗展示） */
  orderNo: string
  /** 应收金额 */
  amount: number
  /** 待支付支付单号（下单响应或 pay-code-session 返回） */
  paymentNo: string
  /** 取货码（自提单收款成功后父组件回填小票用，透传给 paid 事件） */
  pickupCode?: string
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 收款成功：payLabel 实际扣款渠道文案；via 现金=改用现金路径，code=扫码扣款 */
  (e: 'paid', payload: { payLabel: string; via: 'code' | 'cash'; pickupCode?: string }): void
  /** 收银员放弃收款（订单保留待支付，父组件据此提示后续处理入口） */
  (e: 'close-abandoned'): void
}>()

const message = useMessage()

/** 扫码状态机：idle 可扫码 / submitting 扣款中 / polling 用户支付中轮询 / failed 待重扫 */
const state = ref<'idle' | 'submitting' | 'polling' | 'failed'>('idle')
const authCode = ref('')
const failMessage = ref('')
const inputRef = ref<{ focus: () => void } | null>(null)
/** 改现金请求进行中 */
const cashing = ref(false)
/** 轮询定时器句柄 */
let pollTimer: ReturnType<typeof setTimeout> | null = null
/** 轮询起始时间（超过 3 分钟仍 PAYING 提示走订单列表处理） */
let pollStart = 0

// 弹窗每次打开：重置状态并聚焦输入框（扫码枪即扫即得）
watch(
  () => props.show,
  (open) => {
    if (!open) return
    state.value = 'idle'
    authCode.value = ''
    failMessage.value = ''
    setTimeout(() => inputRef.value?.focus(), 100)
  },
)

onBeforeUnmount(() => {
  clearPoll()
})

/** 清理轮询定时器 */
function clearPoll() {
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
}

/**
 * 提交付款码扣款（输入框回车触发）：PAID 收工出小票；
 * PAYING 转轮询；FAILED 停留弹窗清码重扫；网络异常回 idle 可重试
 */
async function submitAuthCode() {
  const code = authCode.value.trim()
  if (!code || state.value === 'submitting' || state.value === 'polling') return
  if (props.amount <= 0) {
    state.value = 'failed'
    failMessage.value = '付款码支付不支持 0 元订单，请改用现金收款'
    return
  }
  state.value = 'submitting'
  failMessage.value = ''
  let finished = false
  try {
    const res = await payPosOrderCode(props.orderId, { payment_no: props.paymentNo, auth_code: code })
    if (res.data.code !== 0) {
      state.value = 'failed'
      return
    }
    const data = res.data.data
    if (data.state === 'PAID') {
      finished = true
      emit('paid', { payLabel: payLabelOf(data.pay_type), via: 'code', pickupCode: props.pickupCode })
      return
    }
    if (data.state === 'PAYING') {
      state.value = 'polling'
      pollStart = Date.now()
      schedulePoll()
      return
    }
    // 渠道确定性拒绝：清码重扫（同一支付单重试，out_trade_no 不变）
    state.value = 'failed'
    failMessage.value = data.message || '扣款失败，请重新扫码'
  } catch {
    // 网络/服务异常：资金状态未知，回到可扫码状态由收银员重试（渠道幂等 + 对账兜底）
    state.value = 'idle'
  } finally {
    authCode.value = ''
    // 未收工时保持输入框聚焦，连续作业不点鼠标
    if (!finished && props.show) {
      setTimeout(() => inputRef.value?.focus(), 50)
    }
  }
}

/**
 * PAYING 在途轮询：向渠道核实是否已收款，已收款补收工；持续在途则 2 秒后再查；
 * 超过 3 分钟仍未定则停止轮询提示改走订单列表，避免收银台无限等
 */
function schedulePoll() {
  clearPoll()
  pollTimer = setTimeout(async () => {
    if (!props.show || state.value !== 'polling') return
    try {
      const res = await getPosOrderPayStatus(props.orderId, props.paymentNo)
      if (res.data.code === 0 && res.data.data.paid) {
        // 轮询路径拿不到实际扣款渠道，小票按「付款码支付」展示
        emit('paid', { payLabel: '付款码支付', via: 'code', pickupCode: props.pickupCode })
        return
      }
    } catch {
      // 单次轮询失败忽略，下一轮继续
    }
    if (Date.now() - pollStart > 3 * 60 * 1000) {
      state.value = 'failed'
      failMessage.value = '顾客长时间未完成支付，可改用现金收款或到「订单查询」处理该笔订单'
      return
    }
    schedulePoll()
  }, 2000)
}

/**
 * 改用现金收款：先关遗留待支付支付单再收现金（后端 pay-cash 编排）
 * 成功即收工出小票；失败停留弹窗可重试或继续扫码
 */
async function onPayCash() {
  if (cashing.value) return
  if (state.value === 'submitting' || state.value === 'polling') {
    message.warning('正在收款中，请等待结果后再改用现金收款')
    return
  }
  cashing.value = true
  try {
    const res = await payPosOrderCash(props.orderId)
    if (res.data.code === 0) {
      emit('paid', { payLabel: '现金', via: 'cash', pickupCode: props.pickupCode })
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    cashing.value = false
  }
}

/**
 * 请求关闭弹窗：submitting/polling 中不允许关（避免漏单）；其余状态通知父组件关闭
 */
function onRequestClose() {
  if (state.value === 'submitting' || state.value === 'polling') {
    message.warning('正在收款中，请等待结果或稍后再试')
    return
  }
  clearPoll()
  emit('update:show', false)
  emit('close-abandoned')
}

/**
 * 扣款渠道文案（扣款响应按实际渠道回传 pay_type）
 * @param payType 支付方式码
 */
function payLabelOf(payType: number): string {
  const map: Record<number, string> = { 1: '微信', 2: '支付宝', 5: '微信付款码', 6: '支付宝付款码' }
  return map[payType] || '付款码支付'
}

/** 暴露关闭前拦截给父组件（ ESC 关闭走 n-modal update:show 同样会经过 onRequestClose） */
defineExpose({ onRequestClose })
</script>

<style lang="scss" scoped>
.codepay-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.codepay-amount {
  text-align: center;
  font-size: 34px;
  font-weight: 700;
  color: #d03050;
}
.codepay-order {
  text-align: center;
  font-size: 12px;
}
.muted {
  color: #999;
}
.codepay-tip {
  text-align: center;
  font-size: 13px;
  color: #666;
  min-height: 18px;
  &.error {
    color: #d03050;
    font-weight: 600;
  }
}
</style>
