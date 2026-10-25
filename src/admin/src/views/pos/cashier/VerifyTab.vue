<template>
  <div class="verify-tab" tabindex="-1">
    <div class="verify-layout">
      <!-- 左栏：扫码/输码区（视觉锚点 + 数字键盘） -->
      <div class="scan-panel" :class="{ active: !!detail }">
        <div class="scan-head">
          <div class="scan-icon">
            <n-icon :size="34" :color="detail ? '#18a058' : '#36ad6a'">
              <BarcodeOutline />
            </n-icon>
          </div>
          <div class="scan-title">{{ detail ? '已识别订单' : '取货核销' }}</div>
          <div class="scan-hint">直接扫码枪扫入或敲键盘 8 位取货码，回车查询</div>
        </div>

        <!-- 8 位码格：扫码枪/键盘/触屏键盘统一入口（无需点击聚焦，页面级监听） -->
        <div class="code-cells" :class="{ shaking: shake }">
          <div
            v-for="i in 8"
            :key="i"
            class="code-cell"
            :class="{ filled: pickupCode.length >= i }"
          >
            <span v-if="pickupCode.length >= i">{{ pickupCode[i - 1] }}</span>
          </div>
        </div>

        <n-button v-if="pickupCode || detail" quaternary size="small" class="reset-btn" @click="resetVerify">
          清空重扫
        </n-button>

        <!-- 触屏数字小键盘（收银机点按输入） -->
        <number-pad class="verify-pad" @input="onPadInput" @backspace="onPadBackspace" @clear="resetVerify" />
      </div>

      <!-- 右栏：查询结果区 -->
      <div class="result-panel">
        <!-- 成功态：核销完成大反馈（自动回焦连续扫码） -->
        <div v-if="verifiedOrderNo" class="state-block success">
          <div class="state-icon">
            <n-icon :size="56" color="#18a058"><CheckmarkCircle /></n-icon>
          </div>
          <div class="state-title">核销成功</div>
          <div class="state-desc">订单 {{ verifiedOrderNo }} 已完成自提交付</div>
          <n-button type="primary" size="large" class="state-action" @click="resetVerify">
            继续核销下一单
          </n-button>
        </div>

        <!-- 未查询态：引导 -->
        <div v-else-if="!queried" class="state-block idle">
          <div class="state-icon">
            <n-icon :size="56" color="#c2c2c2"><QrCodeOutline /></n-icon>
          </div>
          <div class="state-title">等待扫码</div>
          <div class="state-desc">顾客出示取货码后，扫码或输入 8 位数字回车即可查询订单</div>
        </div>

        <!-- 未找到态 -->
        <div v-else-if="!detail" class="state-block miss">
          <div class="state-icon">
            <n-icon :size="56" color="#f0a020"><AlertCircleOutline /></n-icon>
          </div>
          <div class="state-title">未找到订单</div>
          <div class="state-desc">取货码 {{ lastCode }} 在本社区没有匹配的订单，请核对后重扫</div>
        </div>

        <!-- 结果态：订单摘要与核销 -->
        <div v-else class="order-card" :class="{ ready: canVerify }">
          <div class="card-head">
            <div>
              <div class="order-no">{{ detail.order_no }}</div>
              <div class="order-meta">
                {{ detail.user_name || '散客' }} · {{ deliveryTypeText(detail.delivery_type) }} · {{ formatDateTime(detail.created_at) }}
              </div>
            </div>
            <n-tag :type="orderStatusTagType(detail.status)" size="medium" :bordered="false">
              {{ detail.status_text }}
            </n-tag>
          </div>

          <div class="card-items">
            <div v-for="item in detail.items" :key="item.id" class="card-item">
              <span class="item-name">{{ item.product_name }}<template v-if="item.sku_name">（{{ item.sku_name }}）</template></span>
              <span class="item-qty">x{{ item.quantity }}</span>
              <span class="item-price">￥{{ formatAmount(item.price) }}</span>
            </div>
          </div>

          <div class="card-amount">
            <span>实付金额</span>
            <span class="amount">￥{{ formatAmount(detail.pay_amount) }}</span>
          </div>

          <!-- 状态差异化操作区：可核销 / 未支付 / 无权限 -->
          <div class="card-actions">
            <n-button
              v-if="canVerify"
              type="primary"
              size="large"
              block
              :loading="verifying"
              @click="onVerify"
            >
              确认核销（回车）
            </n-button>
            <n-alert v-else-if="detail.status === ORDER_STATUS.PENDING_PAY" type="warning" :bordered="false">
              订单尚未支付，收款后才能核销；可在「订单查询」继续收款
            </n-alert>
            <n-alert v-else-if="!hasUpdatePermission" type="default" :bordered="false">
              当前账号无核销权限（需订单处理权限 order:update）
            </n-alert>
            <n-alert v-else type="info" :bordered="false">
              当前状态（{{ detail.status_text }}）无需核销
            </n-alert>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useMessage } from 'naive-ui'
import { AlertCircleOutline, BarcodeOutline, CheckmarkCircle, QrCodeOutline } from '@vicons/ionicons5'
import { getOrderDetail, getOrderList, verifyPickup } from '@/api/order'
import type { OrderDetail } from '@/api/types'
import NumberPad from '@/components/NumberPad.vue'
import { DELIVERY_TYPE, deliveryTypeText, ORDER_STATUS, orderStatusTagType } from '@/constants/order'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

/**
 * 取货核销 Tab（2026-10-25 UI 改版：左右分栏 + 状态差异化反馈）
 * 左栏扫码/输码区：8 位码格展示（扫码枪=键盘输入+回车、物理键盘、触屏小键盘三通道，
 * 页面级监听无需点击聚焦）；右栏结果区按「等待扫码/未找到/订单卡/核销成功」四态渲染，
 * 订单卡内再按可核销/未支付/无权限差异化提示。核销成功大反馈停留，支持连续扫码。
 * 核销口径不变：仅待自提（自提单已支付）可核销，权限沿用接口要求（order:update）
 */

const message = useMessage()
const authStore = useAuthStore()

/** 取货码输入 */
const pickupCode = ref('')
/** 查询中 */
const querying = ref(false)
/** 已执行过查询（区分空态与未查询态） */
const queried = ref(false)
/** 最近一次查询的取货码（未找到提示回显） */
const lastCode = ref('')
/** 命中的订单详情 */
const detail = ref<OrderDetail | null>(null)
/** 核销请求进行中 */
const verifying = ref(false)
/** 核销成功的订单号（成功态展示，resetVerify 后清空） */
const verifiedOrderNo = ref('')
/** 码位抖动动画（查询未找到时触发，吸引注意） */
const shake = ref(false)
let shakeTimer: number | undefined

/**
 * 是否可核销：自提单已支付（待自提）且有权限
 */
const canVerify = computed(
  () => !!detail.value && detail.value.status === ORDER_STATUS.PAID && detail.value.delivery_type === DELIVERY_TYPE.SELF_PICKUP && hasUpdatePermission.value,
)

/** 是否有核销权限（接口要求 order:update） */
const hasUpdatePermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('order:update'))

/**
 * 追加一位数字到取货码（页面级键盘与小键盘共用；满 8 位自动查询）
 * @param digit 数字字符
 */
function appendDigit(digit: string) {
  if (pickupCode.value.length >= 8) return
  pickupCode.value += digit
  // 满 8 位即查：扫码枪回车前、触屏点按最后一格后无需再按回车
  if (pickupCode.value.length === 8) void onQuery()
}

/**
 * 页面级键盘监听：数字键直接录入（无需聚焦输入框）、退格删一位、回车查询。
 * 扫码枪等价于高速键盘输入 + 回车，即扫即得
 * @param e 键盘事件
 */
function onGlobalKeydown(e: KeyboardEvent) {
  if (e.ctrlKey || e.metaKey || e.altKey) return
  if (/^\d$/.test(e.key)) {
    appendDigit(e.key)
    e.preventDefault()
    return
  }
  if (e.key === 'Backspace') {
    pickupCode.value = pickupCode.value.slice(0, -1)
    e.preventDefault()
    return
  }
  if (e.key === 'Enter') {
    void onQuery()
    e.preventDefault()
  }
}

/**
 * 按取货码查询订单并加载详情
 */
async function onQuery() {
  const code = pickupCode.value.trim()
  if (!code || querying.value) {
    return
  }
  if (code.length < 8) {
    message.warning('请输入完整的 8 位取货码')
    return
  }
  querying.value = true
  try {
    const res = await getOrderList({ page: 1, page_size: 10, pickup_code: code })
    const first = res.data.data.list?.[0]
    if (!first) {
      detail.value = null
      queried.value = true
      lastCode.value = code
      triggerShake()
      return
    }
    const detailRes = await getOrderDetail(first.id)
    detail.value = detailRes.data.data
    queried.value = true
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    querying.value = false
  }
}

/**
 * 未找到时码位抖动一次，提示输入有误
 */
function triggerShake() {
  shake.value = true
  if (shakeTimer !== undefined) window.clearTimeout(shakeTimer)
  shakeTimer = window.setTimeout(() => {
    shake.value = false
    shakeTimer = undefined
  }, 500)
}

/**
 * 确认核销：校验取货码后完成订单，成功进成功态（停留反馈，支持连续扫码）
 */
async function onVerify() {
  if (!detail.value) return
  verifying.value = true
  try {
    await verifyPickup(detail.value.id, pickupCode.value.trim())
    verifiedOrderNo.value = detail.value.order_no
    detail.value = null
    queried.value = false
    pickupCode.value = ''
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifying.value = false
  }
}

/**
 * 清空重扫：回到输入态（成功态点击即继续下一单）
 */
function resetVerify() {
  detail.value = null
  queried.value = false
  verifiedOrderNo.value = ''
  pickupCode.value = ''
}

// --- 小键盘事件 ---

/** 小键盘数字输入 */
function onPadInput(char: string) {
  appendDigit(char)
}

/** 小键盘退格 */
function onPadBackspace() {
  pickupCode.value = pickupCode.value.slice(0, -1)
}

onMounted(() => {
  window.addEventListener('keydown', onGlobalKeydown)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
  if (shakeTimer !== undefined) {
    window.clearTimeout(shakeTimer)
    shakeTimer = undefined
  }
})
</script>

<style lang="scss" scoped>
.verify-tab {
  height: 100%;
  overflow-y: auto;
  box-sizing: border-box;
  padding: 24px 20px;
  background: linear-gradient(180deg, #f6f9f7 0%, #f3f5f4 100%);
}
.verify-layout {
  display: flex;
  gap: 20px;
  align-items: flex-start;
  justify-content: center;
  max-width: 980px;
  margin: 0 auto;
}

// --- 左栏：扫码区 ---
.scan-panel {
  width: 330px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  background: #fff;
  border-radius: 14px;
  padding: 22px 20px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  border-top: 3px solid #36ad6a;
  transition: border-color 0.25s;
  &.active {
    border-top-color: #18a058;
  }
}
.scan-head {
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  .scan-icon {
    width: 64px;
    height: 64px;
    border-radius: 50%;
    background: rgba(54, 173, 106, 0.1);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .scan-title {
    font-size: 17px;
    font-weight: 700;
  }
  .scan-hint {
    font-size: 12px;
    color: #999;
  }
}
.code-cells {
  display: flex;
  gap: 6px;
  &.shaking {
    animation: shake 0.4s;
  }
}
.code-cell {
  width: 30px;
  height: 42px;
  border: 1.5px solid #e0e0e6;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: #18a058;
  background: #fafafa;
  transition: all 0.15s;
  &.filled {
    border-color: #36ad6a;
    background: #f0fbf5;
  }
}
@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-6px); }
  75% { transform: translateX(6px); }
}
.reset-btn {
  margin-top: -6px;
}
.verify-pad {
  width: 100%;
}

// --- 右栏：结果区 ---
.result-panel {
  flex: 1;
  min-width: 0;
}
.state-block {
  background: #fff;
  border-radius: 14px;
  padding: 56px 28px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  .state-title {
    font-size: 18px;
    font-weight: 700;
  }
  .state-desc {
    font-size: 13px;
    color: #999;
    max-width: 360px;
    line-height: 1.6;
  }
  .state-action {
    margin-top: 10px;
    min-width: 200px;
  }
}

// 订单卡：可核销时绿色描边强化"就绪"暗示
.order-card {
  background: #fff;
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  border: 1.5px solid transparent;
  &.ready {
    border-color: #36ad6a;
  }
}
.card-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  .order-no {
    font-size: 16px;
    font-weight: 700;
  }
  .order-meta {
    font-size: 12px;
    color: #999;
    margin-top: 4px;
  }
}
.card-items {
  margin-top: 14px;
  border-top: 1px dashed #eee;
  padding-top: 10px;
}
.card-item {
  display: flex;
  gap: 10px;
  font-size: 13px;
  padding: 4px 0;
  .item-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .item-qty {
    color: #999;
  }
  .item-price {
    font-variant-numeric: tabular-nums;
  }
}
.card-amount {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 10px;
  padding-top: 12px;
  border-top: 1px dashed #eee;
  font-size: 14px;
  .amount {
    font-size: 22px;
    font-weight: 700;
    color: #d03050;
  }
}
.card-actions {
  margin-top: 16px;
}
</style>
