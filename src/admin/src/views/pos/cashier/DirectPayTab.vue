<template>
  <div class="direct-pay-tab">
    <div class="pay-panel">
      <div class="panel-hint">直接收款：输入金额后收款，不关联商品（按到店消费记录）</div>

      <!-- 会员栏：默认散客，可把本笔消费挂到会员名下（口径与商品下单一致，布局更紧凑） -->
      <div class="member-bar">
        <span class="member-label">会员</span>
        <n-tag v-if="member" type="primary" size="small" closable @close="clearMember">{{ memberLabel }}</n-tag>
        <n-tag v-else type="default" size="small">散客</n-tag>
        <n-button size="small" quaternary class="member-btn" @click="showUserPicker = true">
          {{ member ? '更换会员' : '选会员' }}
        </n-button>
      </div>

      <!-- 金额展示与输入 -->
      <div class="amount-box" @click="focusAmount">
        <span class="amount-label">收款金额</span>
        <n-input
          ref="amountInputRef"
          :value="amountText"
          size="large"
          placeholder="0.00"
          class="amount-input"
          :input-props="{ inputmode: 'decimal' }"
          @keydown="onAmountKeydown"
          @update:value="sanitizeAmount"
        >
          <template #prefix>￥</template>
        </n-input>
      </div>

      <!-- 数字小键盘（触屏收银机点按） -->
      <number-pad dot @input="onPadInput" @backspace="onPadBackspace" @clear="clearAmount" />

      <!-- 收款动作（两按钮统一高度字号缩小视觉差异，付款码为主路径实心、现金次路径描边） -->
      <n-space vertical :size="10" class="pay-actions">
        <n-button
          v-if="codePayMethod"
          type="primary"
          size="large"
          block
          class="pay-btn"
          :loading="creating"
          :disabled="!amountValid"
          @click="onCodePay"
        >
          <template #icon><n-icon :component="ScanOutline" /></template>
          付款码收款 ￥{{ formatAmount(amount) }}（F2）
        </n-button>
        <n-button
          size="large"
          block
          type="warning"
          secondary
          class="pay-btn"
          :loading="creating"
          :disabled="!amountValid"
          @click="onCashPay"
        >
          <template #icon><n-icon :component="CashOutline" /></template>
          现金收款（F3）
        </n-button>
      </n-space>
    </div>

    <!-- 付款码扫码弹窗（与商品下单共用） -->
    <code-pay-dialog
      v-model:show="showCodePay"
      :order-id="codePayOrderId"
      :order-no="codePayOrderNo"
      :amount="codePayAmount"
      :payment-no="codePayPaymentNo"
      @paid="onCodePaid"
      @close-abandoned="onAbandoned"
    />

    <!-- 收银成功小票弹窗 -->
    <receipt-modal v-model:show="showPaid" :receipt="receipt" @bound="onReceiptBound" />

    <!-- 会员选择（单选，含现场注册；与商品下单共用） -->
    <user-picker v-model:show="showUserPicker" title="选择会员" registerable register-scene="pos" @select="onSelectMember" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useDialog, useMessage } from 'naive-ui'
import { CashOutline, ScanOutline } from '@vicons/ionicons5'
import { createPosOrder, ensurePosCodePaySession, listPosPayMethods } from '@/api/order'
import type { User } from '@/api/types'
import NumberPad from '@/components/NumberPad.vue'
import UserPicker from '@/components/UserPicker.vue'
import CodePayDialog from './CodePayDialog.vue'
import ReceiptModal, { type PaidReceipt } from './ReceiptModal.vue'
import { PAY_TYPE } from '@/constants/order'

/**
 * 直接收款 Tab（无商品快速收款）
 * 输入金额（物理键盘/触屏小键盘双通道）→ 付款码收款（建无商品到店单后扫码扣款）
 * 或现金收款（确认即完结）。金额即成交金额，不参与券/满减/库存，订单可在订单查询继续处理。
 * 可选会员（默认散客）：选中的会员随建单写入 user_id，消费计入其名下；
 * 收款成功后保留会员选择便于连续收款，可点标签关闭回散客
 */

const message = useMessage()
const dialog = useDialog()

/** 金额输入（字符串形式，含小数点；空串显示占位） */
const amountText = ref('')
const amountInputRef = ref<{ focus: () => void } | null>(null)
const creating = ref(false)

/** 收款金额（非法输入按 0 处理） */
const amount = computed(() => {
  const v = Number.parseFloat(amountText.value)
  return Number.isFinite(v) ? Math.round(v * 100) / 100 : 0
})
/** 金额有效性：大于 0 才可收款 */
const amountValid = computed(() => amount.value > 0)

// --- 会员选择（默认散客） ---

const member = ref<User | null>(null)
const showUserPicker = ref(false)

/** 会员展示名：昵称优先，退回手机号，无有效 id 时按散客口径兜底（与 user_id ?? 0 一致） */
const memberLabel = computed(
  () =>
    member.value?.nickname ||
    member.value?.phone ||
    (member.value?.id ? `会员#${member.value.id}` : '散客')
)

/**
 * 会员选择弹窗回传：记录会员并回焦金额输入（连续输金额收款）
 * @param u 选中的会员
 */
function onSelectMember(u: User) {
  member.value = u
  focusAmount()
}

/**
 * 清除会员回散客（小票会员标签的关闭按钮）
 */
function clearMember() {
  member.value = null
}

// --- 支付方式：现金恒可用；付款码按渠道配置出现（与商品下单同口径） ---
const codePayMethod = ref<{ label: string; value: number } | null>(null)

/**
 * 拉取收银台可用支付方式，取第一个非现金方式作为付款码入口
 */
async function loadPayMethods() {
  codePayMethod.value = null
  try {
    const res = await listPosPayMethods()
    if (res.data.code !== 0) return
    const list = (res.data.data.list || []).map((m) => ({ label: m.label, value: m.pay_type }))
    codePayMethod.value = list.find((m) => m.value !== PAY_TYPE.CASH) ?? null
  } catch {
    // 拉取失败降级为仅现金，服务端渠道校验兜底
  }
}

// --- 金额输入 ---

/**
 * 小键盘输入：写入金额字符串（最多两位小数）
 * @param char 按键字符（数字/小数点）
 */
function onPadInput(char: string) {
  if (char === '.') {
    if (amountText.value.includes('.')) return
    amountText.value = (amountText.value || '0') + '.'
    return
  }
  // 限制两位小数
  const dotIdx = amountText.value.indexOf('.')
  if (dotIdx >= 0 && amountText.value.length - dotIdx > 2) return
  // 防止前导零堆积（"00" -> "0"）
  const next = amountText.value === '0' ? char : amountText.value + char
  amountText.value = next.length > 9 ? amountText.value : next
}

/**
 * 小键盘退格：删一位
 */
function onPadBackspace() {
  amountText.value = amountText.value.slice(0, -1)
}

/**
 * 清空金额
 */
function clearAmount() {
  amountText.value = ''
}

/**
 * 输入净化（v-model 与粘贴的统一白名单出口，2026-10-25 审查 P2）：
 * 只保留数字与小数点（单点、最多两位小数、总长 ≤9），
 * 防止 "12ab" 之类被 parseFloat 静默截断成 12 造成展示与实收不一致
 * @param v 输入框最新值
 */
function sanitizeAmount(v: string) {
  let s = v.replace(/[^0-9.]/g, '')
  const firstDot = s.indexOf('.')
  if (firstDot >= 0) {
    s = s.slice(0, firstDot + 1) + s.slice(firstDot + 1).replace(/\./g, '')
    const [int, dec] = s.split('.')
    s = int + '.' + dec.slice(0, 2)
  }
  if (s.length > 9) s = s.slice(0, 9)
  amountText.value = s
}

/**
 * 物理键盘：数字/小数点/退格直接写入，Enter 触发付款码收款（无付款码渠道时触发现金确认）
 * @param e 键盘事件
 */
function onAmountKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault()
    if (codePayMethod.value) onCodePay()
    else onCashPay()
    return
  }
  if (/^\d$/.test(e.key)) {
    onPadInput(e.key)
    e.preventDefault()
    return
  }
  if (e.key === '.') {
    onPadInput('.')
    e.preventDefault()
    return
  }
  if (e.key === 'Backspace') {
    onPadBackspace()
    e.preventDefault()
  }
}

/** 让金额输入框聚焦（点金额区域即聚焦） */
function focusAmount() {
  amountInputRef.value?.focus()
}

// --- F2/F3 全局收款快捷键 ---

/**
 * 全局 F2/F3：F2 付款码收款、F3 现金收款（弹窗打开时忽略，避免重复建单）
 * @param e 键盘事件
 */
function onGlobalKeydown(e: KeyboardEvent) {
  if (e.key !== 'F2' && e.key !== 'F3') return
  if (showCodePay.value || showPaid.value || cashConfirmOpen.value) return
  e.preventDefault()
  if (!amountValid.value) {
    message.warning('请先输入收款金额')
    return
  }
  if (e.key === 'F2') onCodePay()
  else onCashPay()
}

// --- 付款码收款 ---

const showCodePay = ref(false)
const codePayOrderId = ref(0)
const codePayOrderNo = ref('')
const codePayAmount = ref(0)
const codePayPaymentNo = ref('')

/** 现金确认弹窗是否打开（全局快捷键与付款码入口的互斥守卫） */
const cashConfirmOpen = ref(false)
/** 确认弹窗聚焦定时器（组件卸载时清理，避免已卸载实例操作 DOM） */
let dialogFocusTimer: number | undefined
/**
 * 放弃扫码遗留的待支付单快照：同金额再次发起付款码收款时复用，
 * 避免直接收款页重复建单（key 为本次收款请求的序列化，判定同一笔业务）
 */
const abandonedPending = ref<{
  orderId: number
  orderNo: string
  paymentNo: string
  amount: number
  key: string
} | null>(null)

/**
 * 弹窗渲染后聚焦确认按钮，回车即确认（不依赖鼠标）
 * @param selector 确认按钮选择器
 */
function focusDialogConfirm(selector = '.n-dialog__action button:last-of-type') {
  if (dialogFocusTimer !== undefined) window.clearTimeout(dialogFocusTimer)
  dialogFocusTimer = window.setTimeout(() => {
    dialogFocusTimer = undefined
    const btn = document.querySelector(selector) as HTMLButtonElement | null
    btn?.focus()
  }, 150)
}

/**
 * 直接收款单：付款码收款：建无商品到店待支付单 → 打开扫码窗（无需确认弹窗，金额已明示）；
 * 放弃扫码后同金额再按 F2 复用遗留单（pay-code-session），不重复建待支付单
 */
async function onCodePay() {
  if (!codePayMethod.value || creating.value) return
  if (showCodePay.value || cashConfirmOpen.value) return
  if (!amountValid.value) {
    message.warning('请先输入收款金额')
    return
  }
  creating.value = true
  try {
    // 会员参与载荷判定：换会员后同金额也不得复用他人名下的遗留单
    const key = JSON.stringify({
      amount: amount.value,
      pay_type: codePayMethod.value.value,
      user_id: member.value?.id ?? 0,
    })
    if (abandonedPending.value && abandonedPending.value.key === key) {
      const reused = await ensurePosCodePaySession(abandonedPending.value.orderId)
      if (reused.data.code !== 0) return
      const p = abandonedPending.value
      codePayOrderId.value = p.orderId
      codePayOrderNo.value = p.orderNo
      codePayAmount.value = p.amount
      codePayPaymentNo.value = reused.data.data.payment_no || p.paymentNo
      showCodePay.value = true
      return
    }
    const res = await createPosOrder({
      user_id: member.value?.id ?? 0,
      delivery_type: 3,
      pay_type: codePayMethod.value.value,
      manual_amount: amount.value,
      items: [],
    })
    if (res.data.code !== 0) return
    codePayOrderId.value = res.data.data.id
    codePayOrderNo.value = res.data.data.order_no
    codePayAmount.value = res.data.data.pay_amount
    codePayPaymentNo.value = res.data.data.payment_no || ''
    abandonedPending.value = {
      orderId: codePayOrderId.value,
      orderNo: codePayOrderNo.value,
      paymentNo: codePayPaymentNo.value,
      amount: codePayAmount.value,
      key,
    }
    showCodePay.value = true
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    creating.value = false
  }
}

// --- 现金收款 ---

const showPaid = ref(false)
const receipt = ref<PaidReceipt | null>(null)

/**
 * 小票弹窗内补绑会员成功：回写快照客户行（ReceiptModal 经 bound 事件通知，不直接改 props）
 * @param p 绑定结果（会员 ID 与展示名）
 */
function onReceiptBound(p: { userId: number; userName: string }) {
  if (!receipt.value) return
  receipt.value.user_id = p.userId
  receipt.value.user_name = p.userName
  receipt.value.member_label = p.userName
}

/**
 * 组装直接收款小票快照（无商品明细；order_id/user_id 供散客单补绑会员入口判定）：
 * 收款前已选会员时直接写入会员信息，小票弹窗不再出现补绑入口
 * @param payLabel 支付方式文案
 */
function buildReceipt(payLabel: string) {
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  receipt.value = {
    order_no: '',
    order_id: 0,
    user_id: member.value?.id ?? 0,
    user_name: member.value ? memberLabel.value : '',
    pay_amount: amount.value,
    channel_label: '到店消费',
    pay_label: payLabel,
    member_label: member.value ? memberLabel.value : '散客',
    items: [],
    total_amount: amount.value,
    coupon_discount: 0,
    manual_discount: 0,
    time: `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`,
  }
}

/**
 * 现金收款：确认后建无商品到店单（现金创建即收款完结，服务端自动流转已完成）
 * 确认弹窗打开后自动聚焦确认按钮，回车即确认（不依赖鼠标）
 */
function onCashPay() {
  if (creating.value) return
  if (showCodePay.value || cashConfirmOpen.value) return
  if (!amountValid.value) {
    message.warning('请先输入收款金额')
    return
  }
  cashConfirmOpen.value = true
  dialog.warning({
    title: '现金收款',
    content: `确认已收现金 ￥${formatAmount(amount.value)}，为${member.value ? `会员 ${memberLabel.value}` : '散客'}收款？（回车确认，Esc 取消）`,
    positiveText: '确认收款',
    negativeText: '取消',
    onPositiveClick: async () => {
      // 收款进行中保持弹窗打开，避免重复提交建单
      if (creating.value) return false
      creating.value = true
      try {
        const res = await createPosOrder({
          user_id: member.value?.id ?? 0,
          delivery_type: 3,
          pay_type: PAY_TYPE.CASH,
          manual_amount: amount.value,
          items: [],
        })
        if (res.data.code === 0) {
          buildReceipt('现金')
          receipt.value!.order_id = res.data.data.id
          receipt.value!.order_no = res.data.data.order_no
          showPaid.value = true
          message.success('收款成功')
          clearAmount()
        }
      } catch {
        // 错误提示由请求拦截器统一弹出
      } finally {
        creating.value = false
      }
    },
    // 覆盖所有关闭路径（确认/取消/Esc），关闭动画结束即释放快捷键守卫
    onAfterLeave: () => {
      cashConfirmOpen.value = false
    },
  })
  // 回车确认：弹窗渲染后聚焦确认按钮，原生回车触发其 click
  focusDialogConfirm()
}

/**
 * 付款码收款成功收尾：出小票、清金额
 * @param payload 收款结果（payLabel 渠道文案）
 */
function onCodePaid(payload: { payLabel: string; via: 'code' | 'cash' }) {
  showCodePay.value = false
  abandonedPending.value = null
  buildReceipt(payload.payLabel)
  receipt.value!.order_id = codePayOrderId.value
  receipt.value!.order_no = codePayOrderNo.value
  showPaid.value = true
  message.success('收款成功')
  clearAmount()
}

/**
 * 收银员放弃扫码：订单保留待支付，提示去订单查询处理
 */
function onAbandoned() {
  message.info(`订单 ${codePayOrderNo.value} 已创建未收款，可在「订单查询」继续收款`)
}

onMounted(() => {
  void loadPayMethods()
  window.addEventListener('keydown', onGlobalKeydown)
  void focusAmount()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
  if (dialogFocusTimer !== undefined) {
    window.clearTimeout(dialogFocusTimer)
    dialogFocusTimer = undefined
  }
})
</script>

<style lang="scss" scoped>
.direct-pay-tab {
  height: 100%;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 24px 16px;
  overflow-y: auto;
  box-sizing: border-box;
}
.pay-panel {
  width: 400px;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.panel-hint {
  text-align: center;
  font-size: 13px;
  color: #999;
}
.member-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border: 1px solid #efeff5;
  border-radius: 8px;
  background: #fafafc;

  .member-label {
    font-size: 14px;
    color: #666;
  }
  .member-btn {
    margin-left: auto;
  }
}
.amount-box {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.amount-label {
  font-size: 14px;
  color: #666;
}
.amount-input {
  :deep(input) {
    font-size: 30px;
    font-weight: 700;
    text-align: center;
    font-variant-numeric: tabular-nums;
  }
}
.pay-actions {
  margin-top: 4px;
}
.pay-btn {
  height: 48px;
  font-size: 16px;
}
</style>
