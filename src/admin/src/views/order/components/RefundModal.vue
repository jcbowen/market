<template>
  <n-modal :show="show" preset="card" title="发起退款" style="width: min(680px, 94vw)" @update:show="onUpdateShow">
    <n-spin :show="loading">
      <n-empty v-if="!detail && !loading" description="订单不存在" />
      <n-form v-else-if="detail" :model="form" label-placement="left" label-width="80">
        <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
          发起后将立即按订单原支付流水退回，无需再审核；
          退款金额以服务端按实付汇算为准（支持多笔部分退，可调低金额）。若渠道退款失败，售后单会保留在「退款中」，可在售后管理页重试。
        </n-alert>
        <n-form-item label="订单">
          <span>
            {{ detail.order_no }}（实付 ￥{{ formatAmount(detail.pay_amount) }}
            <template v-if="refundedTotal > 0">，已退 ￥{{ formatAmount(refundedTotal) }}</template>
            ，剩余可退 <span class="remaining">￥{{ formatAmount(remaining) }}</span>）
          </span>
        </n-form-item>
        <n-form-item label="退款方式">
          <n-radio-group v-model:value="form.mode">
            <n-radio-button value="full">整单退</n-radio-button>
            <n-radio-button value="partial">部分商品退</n-radio-button>
            <n-radio-button value="amount">仅金额退</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-form-item v-if="form.mode === 'partial'" label="退款商品">
          <div class="item-list">
            <div v-for="item in detail.items" :key="item.id" class="item-row">
              <n-checkbox v-model:checked="selState[item.id].checked" />
              <span class="item-name">
                {{ item.product_name }}
                <span v-if="item.sku_name" class="item-spec">{{ item.sku_name }}</span>
              </span>
              <span class="item-qty">共 {{ item.quantity }} 件</span>
              <n-input-number
                v-model:value="selState[item.id].qty"
                size="small"
                :min="1"
                :max="item.quantity"
                :disabled="!selState[item.id].checked"
                style="width: 110px"
              />
              <span class="item-amount">￥{{ formatAmount(itemEstimate(item, selState[item.id].qty)) }}</span>
            </div>
          </div>
        </n-form-item>
        <n-form-item v-if="form.mode !== 'amount'" label="退款金额">
          <n-input-number
            v-model:value="form.amount"
            :min="0"
            :max="remaining"
            :precision="2"
            style="width: 200px"
          >
            <template #prefix>￥</template>
          </n-input-number>
          <span class="amount-hint">默认按实付汇算（{{ form.mode === 'partial' ? '约' : '' }}￥{{ formatAmount(estimate) }}），可调低</span>
        </n-form-item>
        <n-form-item v-if="form.mode === 'amount'" label="退款金额" required>
          <n-input-number v-model:value="form.amount" :min="0.01" :max="remaining" :precision="2" style="width: 200px">
            <template #prefix>￥</template>
          </n-input-number>
          <span class="amount-hint">不退商品、不回补库存，金额不超过剩余可退 ￥{{ formatAmount(remaining) }}</span>
        </n-form-item>
        <n-form-item
          v-if="form.mode === 'full' && refundPolicy === 'user_decide' && freightRemaining > 0"
          label="退还运费"
        >
          <n-checkbox v-model:checked="form.refund_freight">
            退还运费 ￥{{ formatAmount(freightRemaining) }}（当前运费策略：用户自行选择）
          </n-checkbox>
        </n-form-item>
        <n-form-item label="退款原因" required>
          <n-input
            v-model:value="form.reason"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-count
            placeholder="请填写退款原因"
          />
        </n-form-item>
      </n-form>
    </n-spin>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onUpdateShow(false)">取消</n-button>
        <n-button type="error" :loading="submitting" :disabled="!detail" @click="submit">发起退款</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { adminCreateRefund, getOrderDetail, getRefundSetting } from '@/api/order'
import type { OrderDetail, OrderItem } from '@/api/types'

/**
 * 后台代客退款弹窗（订单列表与详情弹窗共用）
 * 三种退款方式：整单退 / 部分商品退（勾选商品行与数量）/ 仅金额退（不退商品不回补库存）；
 * 金额默认由服务端口径汇算（支持收银台改价/抹零订单），可调低；
 * 发起即执行渠道退款（免审核）；多笔部分退累计不超过实付
 */
const props = defineProps<{
  show: boolean
  /** 待退款订单 ID */
  orderId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const dialog = useDialog()
const message = useMessage()

const loading = ref(false)
const submitting = ref(false)
const detail = ref<OrderDetail | null>(null)

/** 运费退还策略（always/never/user_decide），加载失败兜底 always 与后端口径一致 */
const refundPolicy = ref('always')

const form = reactive({
  mode: 'full' as 'full' | 'partial' | 'amount',
  amount: 0 as number | null,
  reason: '',
  refund_freight: false,
})

/** 部分退勾选状态：order_item_id -> { checked, qty } */
const selState = reactive<Record<number, { checked: boolean; qty: number }>>({})

function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

function formatAmount(value?: number | null): string {
  return (value ?? 0).toFixed(2)
}

/** 已成功退款合计（含运费），来自订单详情的售后记录 */
const refundedTotal = computed(() =>
  Math.round((detail.value?.refunds ?? []).filter((r) => r.status === 5).reduce((s, r) => s + (r.amount ?? 0), 0) * 100) / 100,
)

/** 已成功退还的运费合计 */
const refundedFreight = computed(() =>
  Math.round((detail.value?.refunds ?? []).filter((r) => r.status === 5).reduce((s, r) => s + (r.freight_amount ?? 0), 0) * 100) / 100,
)

/** 剩余可退总额（实付 - 已成功退款） */
const remaining = computed(() => Math.round(((detail.value?.pay_amount ?? 0) - refundedTotal.value) * 100) / 100)

/** 剩余可退运费 */
const freightRemaining = computed(() => Math.round(((detail.value?.delivery_fee ?? 0) - refundedFreight.value) * 100) / 100)

/**
 * 单行商品指定数量的应退金额估算（前端展示用，以服务端汇算为准）
 * 行全额应退 = 行小计 - 行小计/订单总额 × 订单优惠总额（系统优惠 + 手动优惠），按数量比例折算
 * @param item 订单商品行
 * @param qty 退货数量
 */
function itemEstimate(item: OrderItem, qty: number): number {
  const d = detail.value
  if (!d || d.total_amount <= 0) return 0
  const full = item.total_amount - ((d.discount_amount + (d.manual_discount ?? 0)) * item.total_amount) / d.total_amount
  return Math.round(Math.max(0, full) * (qty / item.quantity) * 100) / 100
}

/** 当前方式的应退金额估算（整单=剩余可退-未退运费按勾选；部分=勾选行合计+未退运费按勾选；仅金额=当前输入） */
const estimate = computed(() => {
  const d = detail.value
  if (!d) return 0
  if (form.mode === 'amount') return form.amount ?? 0
  let goods = 0
  if (form.mode === 'full') {
    goods = d.items.reduce((s, item) => s + itemEstimate(item, item.quantity), 0)
    // 整单退以剩余可退兜底（多笔退后行合计可能有余差）
    const withFreight = remaining.value - (form.refund_freight ? freightRemaining.value : 0)
    goods = Math.min(goods, withFreight)
  } else {
    for (const item of d.items) {
      const sel = selState[item.id]
      if (sel?.checked) goods += itemEstimate(item, sel.qty)
    }
  }
  const freight = form.mode === 'full' && form.refund_freight ? freightRemaining.value : 0
  return Math.round((Math.min(goods, remaining.value) + freight) * 100) / 100
})

/** 同步金额默认值：跟随估算变化，仅当用户未手动改过或已超上限时覆盖 */
watch(estimate, (val) => {
  if (form.amount == null || form.amount > remaining.value || form.amount === 0) {
    form.amount = val
  }
})

/**
 * 加载订单详情与退款配置
 * @param id 订单 ID
 */
async function loadDetail(id: number) {
  loading.value = true
  try {
    const [detailRes, settingRes] = await Promise.all([getOrderDetail(id), getRefundSetting()])
    if (detailRes.data.code === 0) {
      detail.value = detailRes.data.data
      // 重建部分退勾选状态（默认全不勾选、数量取整行数量）
      for (const key of Object.keys(selState)) {
        delete selState[Number(key)]
      }
      for (const item of detailRes.data.data.items) {
        selState[item.id] = { checked: false, qty: item.quantity }
      }
      form.amount = remaining.value
    }
    if (settingRes.data.code === 0 && settingRes.data.data?.refund_freight_policy) {
      refundPolicy.value = settingRes.data.data.refund_freight_policy
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

function submit() {
  const d = detail.value
  if (!d) return
  if (!form.reason.trim()) {
    message.error('请填写退款原因')
    return
  }
  const items = d.items
    .filter((item) => selState[item.id]?.checked)
    .map((item) => ({ order_item_id: item.id, quantity: selState[item.id].qty }))
  if (form.mode === 'partial' && items.length === 0) {
    message.error('部分退款请至少勾选一项商品')
    return
  }
  const amount = form.amount ?? 0
  if (amount <= 0) {
    message.error('请填写退款金额')
    return
  }
  if (amount > remaining.value + 1e-6) {
    message.error(`退款金额不能超过剩余可退 ￥${formatAmount(remaining.value)}`)
    return
  }

  const modeText =
    form.mode === 'full' ? '整单退款' : form.mode === 'partial' ? `部分退款（${items.length} 项商品）` : `仅金额退款（不退商品）`
  const desc = `确认对订单 ${d.order_no} 发起${modeText}，退款 ￥${formatAmount(amount)}？退款将立即按原支付流水退回。`
  dialog.warning({
    title: '发起退款',
    content: desc,
    positiveText: '确认退款',
    negativeText: '取消',
    onPositiveClick: async () => {
      submitting.value = true
      try {
        await adminCreateRefund(d.id, {
          reason: form.reason.trim(),
          amount,
          items: form.mode === 'partial' ? items : undefined,
          refund_freight: form.mode === 'full' ? form.refund_freight : undefined,
          mode: form.mode === 'amount' ? 2 : undefined,
        })
        message.success('退款发起成功，可在售后管理页查看进度')
        emit('update:show', false)
        emit('success')
      } catch {
        // 错误提示由请求拦截器统一弹出
      } finally {
        submitting.value = false
      }
    },
  })
}

watch(
  () => props.show,
  (visible) => {
    if (visible && props.orderId) {
      detail.value = null
      form.mode = 'full'
      form.reason = ''
      form.refund_freight = false
      form.amount = null
      loadDetail(props.orderId)
    }
  },
)
</script>

<style scoped>
.item-list {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.item-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.item-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-spec {
  color: #999;
  font-size: 12px;
  margin-left: 4px;
}

.item-qty {
  color: #999;
  font-size: 12px;
  flex-shrink: 0;
}

.item-amount {
  color: #666;
  font-size: 12px;
  width: 70px;
  text-align: right;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.remaining {
  color: #d03050;
  font-weight: 600;
}

.amount-hint {
  color: #999;
  font-size: 12px;
  margin-left: 8px;
}
</style>
