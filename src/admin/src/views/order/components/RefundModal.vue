<template>
  <n-modal :show="show" preset="card" title="发起退款" style="width: min(640px, 94vw)" @update:show="onUpdateShow">
    <n-spin :show="loading">
      <n-empty v-if="!detail && !loading" description="订单不存在" />
      <n-form v-else-if="detail" :model="form" label-placement="left" label-width="80">
        <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
          发起后将立即按订单原支付流水退回，无需再审核；
          退款金额由系统按优惠分摊汇算。若渠道退款失败，售后单会保留在「退款中」，可在售后管理页重试。
        </n-alert>
        <n-form-item label="订单">
          <span>{{ detail.order_no }}（实付 ￥{{ formatAmount(detail.pay_amount) }}）</span>
        </n-form-item>
        <n-form-item label="退款方式">
          <n-radio-group v-model:value="form.mode">
            <n-radio-button value="full">整单退</n-radio-button>
            <n-radio-button value="partial">部分退</n-radio-button>
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
            </div>
          </div>
        </n-form-item>
        <n-form-item
          v-if="form.mode === 'full' && refundPolicy === 'user_decide' && (detail.delivery_fee ?? 0) > 0"
          label="退还运费"
        >
          <n-checkbox v-model:checked="form.refund_freight">
            退还运费 ￥{{ formatAmount(detail.delivery_fee) }}（当前运费策略：用户自行选择）
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
import { reactive, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { adminCreateRefund, getOrderDetail, getRefundSetting } from '@/api/order'
import type { OrderDetail } from '@/api/types'

/**
 * 后台代客退款弹窗（订单列表与详情弹窗共用）
 * 支持整单退/部分退（勾选商品行与数量）；金额由服务端汇算，发起即执行渠道退款（免审核）
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
  mode: 'full' as 'full' | 'partial',
  reason: '',
  refund_freight: false,
})

/** 部分退勾选状态：order_item_id -> { checked, qty } */
const selState = reactive<Record<number, { checked: boolean; qty: number }>>({})

function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

function formatAmount(value?: number): string {
  return (value ?? 0).toFixed(2)
}

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

  const desc =
    form.mode === 'full'
      ? `确认对订单 ${d.order_no} 发起整单退款？退款将立即按原支付流水退回。`
      : `确认对订单 ${d.order_no} 发起部分退款（${items.length} 项商品）？退款将立即按原支付流水退回。`
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
          items: form.mode === 'full' ? undefined : items,
          refund_freight: form.mode === 'full' ? form.refund_freight : undefined,
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
</style>
