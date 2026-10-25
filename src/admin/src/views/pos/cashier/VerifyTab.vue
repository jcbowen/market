<template>
  <div class="verify-tab" tabindex="-1">
    <div class="verify-panel">
      <div class="verify-hint">直接敲键盘或扫码枪扫入 8 位取货码，回车查询（无需点击输入框）</div>
      <n-input
        ref="codeInputRef"
        v-model:value="pickupCode"
        size="large"
        placeholder="取货码"
        maxlength="8"
        class="code-input"
        :input-props="{ inputmode: 'numeric' }"
        clearable
      >
        <template #prefix>
          <n-icon><BarcodeOutline /></n-icon>
        </template>
      </n-input>
    </div>

    <!-- 触屏数字小键盘（收银机点按输入） -->
    <number-pad class="verify-pad" @input="onPadInput" @backspace="onPadBackspace" @clear="resetVerify" />

    <!-- 订单摘要与核销 -->
    <div v-if="detail" class="order-summary">
      <div class="summary-head">
        <div>
          <div class="order-no">{{ detail.order_no }}</div>
          <div class="order-meta">{{ detail.user_name || '散客' }} · {{ formatDateTime(detail.created_at) }}</div>
        </div>
        <n-tag :type="orderStatusTagType(detail.status)" size="medium">{{ detail.status_text }}</n-tag>
      </div>
      <div class="summary-items">
        <div v-for="item in detail.items" :key="item.id" class="summary-item">
          <span class="item-name">{{ item.product_name }}<template v-if="item.sku_name">（{{ item.sku_name }}）</template></span>
          <span class="item-qty">x{{ item.quantity }}</span>
          <span class="item-price">￥{{ formatAmount(item.price) }}</span>
        </div>
      </div>
      <div class="summary-amount">
        <span>应付</span>
        <span class="amount">￥{{ formatAmount(detail.pay_amount) }}</span>
      </div>
      <n-space justify="center" class="summary-actions">
        <n-button :disabled="verifying" @click="resetVerify">清空重扫</n-button>
        <n-button
          v-if="canVerify"
          type="primary"
          size="large"
          :loading="verifying"
          @click="onVerify"
        >
          确认核销
        </n-button>
        <n-tag v-else-if="detail.status === ORDER_STATUS.PENDING_PAY" type="warning" size="medium">
          订单未支付，暂不能核销
        </n-tag>
        <n-tag v-else-if="!hasUpdatePermission" type="default" size="medium">无核销权限（需订单处理权限）</n-tag>
      </n-space>
    </div>
    <n-empty v-else-if="queried" description="未找到该取货码对应的订单" class="empty" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useMessage } from 'naive-ui'
import { BarcodeOutline } from '@vicons/ionicons5'
import { getOrderDetail, getOrderList, verifyPickup } from '@/api/order'
import type { OrderDetail } from '@/api/types'
import NumberPad from '@/components/NumberPad.vue'
import { ORDER_STATUS, orderStatusTagType } from '@/constants/order'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

/**
 * 取货核销 Tab
 * 进入即聚焦：页面级键盘监听（无需点击输入框），物理键盘数字与扫码枪直接录入、回车查询；
 * 配触屏数字小键盘供触屏收银机点按。输入/扫码枪扫入取货码 → 查本社区订单 → 展示摘要
 * → 仅待自提（自提单已支付）可核销。核销权限沿用接口要求（order:update）
 */

const message = useMessage()
const authStore = useAuthStore()

/** 取货码输入 */
const pickupCode = ref('')
/** 查询中 */
const querying = ref(false)
/** 已执行过查询（区分空态与未查询态） */
const queried = ref(false)
/** 命中的订单详情 */
const detail = ref<OrderDetail | null>(null)
/** 核销请求进行中 */
const verifying = ref(false)

/** 输入框引用（核销完成后回焦，便于连续扫码） */
const codeInputRef = ref<{ focus: () => void } | null>(null)

/** 是否有核销权限（接口要求 order:update） */
const hasUpdatePermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('order:update'))

/**
 * 是否可核销：自提单已支付（待自提）且有权限
 */
const canVerify = computed(
  () => !!detail.value && detail.value.status === ORDER_STATUS.PAID && detail.value.delivery_type === 1 && hasUpdatePermission.value,
)

/**
 * 追加一位数字到取货码（页面级键盘与小键盘共用）
 * @param digit 数字字符
 */
function appendDigit(digit: string) {
  if (pickupCode.value.length >= 8) return
  pickupCode.value += digit
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
 * 确认核销：校验取货码后完成订单
 */
async function onVerify() {
  if (!detail.value) return
  verifying.value = true
  try {
    await verifyPickup(detail.value.id, pickupCode.value.trim())
    message.success(`订单 ${detail.value.order_no} 核销成功`)
    resetVerify()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifying.value = false
  }
}

/**
 * 清空重扫：回到输入态并回焦输入框
 */
function resetVerify() {
  detail.value = null
  queried.value = false
  pickupCode.value = ''
  void nextTick(() => codeInputRef.value?.focus())
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
  void nextTick(() => codeInputRef.value?.focus())
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<style lang="scss" scoped>
.verify-tab {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 24px 16px;
  gap: 20px;
  overflow-y: auto;
  box-sizing: border-box;
}
.verify-panel {
  width: 420px;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
  .verify-hint {
    text-align: center;
    font-size: 13px;
    color: #999;
  }
  .code-input {
    :deep(input) {
      font-size: 24px;
      letter-spacing: 6px;
      text-align: center;
      font-variant-numeric: tabular-nums;
    }
  }
}
.verify-pad {
  width: 320px;
  max-width: 100%;
}
.order-summary {
  width: 520px;
  max-width: 100%;
  background: #fff;
  border-radius: 10px;
  padding: 18px 20px;
}
.summary-head {
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
.summary-items {
  margin-top: 14px;
  border-top: 1px dashed #eee;
  padding-top: 10px;
}
.summary-item {
  display: flex;
  gap: 10px;
  font-size: 13px;
  padding: 3px 0;
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
.summary-amount {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed #eee;
  font-size: 14px;
  .amount {
    font-size: 20px;
    font-weight: 700;
    color: #d03050;
  }
}
.summary-actions {
  margin-top: 16px;
}
.empty {
  margin-top: 40px;
}
</style>
