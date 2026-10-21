<template>
  <n-modal :show="show" preset="card" title="标记已支付" style="width: min(520px, 94vw)" @update:show="onUpdateShow">
    <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
      仅用于渠道查询接口不可用、但已在渠道商户后台核实到账的订单。渠道交易号与核实依据必填，操作按管理员留痕，标记后订单流转为已支付。
    </n-alert>
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="left" label-width="100">
      <n-form-item label="支付单号" path="payment_no">
        <n-select
          v-model:value="form.payment_no"
          :options="paymentNoOptions"
          filterable
          tag
          placeholder="选择本单支付单，或手动填写渠道侧支付单号"
        />
      </n-form-item>
      <n-form-item label="渠道交易号" path="transaction_id">
        <n-input v-model:value="form.transaction_id" placeholder="渠道商户后台可见的交易号" />
      </n-form-item>
      <n-form-item label="实收金额" path="amount">
        <n-input-number v-model:value="form.amount" :min="0.01" :precision="2" style="width: 100%">
          <template #prefix>￥</template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="核实依据" path="remark">
        <n-input
          v-model:value="form.remark"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-count
          placeholder="例如：支付宝商户平台 2026-10-21 15:32 查询截图，交易号 2026xxxx 已支付"
        />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onUpdateShow(false)">返回</n-button>
        <n-button type="primary" :loading="submitting" @click="submit">确认标记</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 后台人工「标记已支付」弹窗
// 这条入口绕过渠道核实，因此四个字段全部必填：支付服务据此写 payments 成功态并流转订单，备注里带操作人
import { computed, reactive, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules, SelectOption } from 'naive-ui'
import { markOrderPaid } from '@/api/order'
import type { OrderPayment } from '@/api/types'
import { PAYMENT_STATUS, paymentStatusText } from '@/constants/order'

/**
 * 人工标记已支付弹窗组件
 * @prop show 弹窗显示状态（v-model:show）
 * @prop orderId 待标记订单 ID（后端只允许待支付订单）
 * @prop payments 本单已有支付尝试，用于给出支付单号与金额默认值
 */
const props = defineProps<{
  show: boolean
  orderId: number | null
  payments?: OrderPayment[]
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const submitting = ref(false)
const form = reactive<{ payment_no: string; transaction_id: string; amount: number | null; remark: string }>({
  payment_no: '',
  transaction_id: '',
  amount: null,
  remark: '',
})

const rules: FormRules = {
  payment_no: { required: true, message: '请填写支付单号', trigger: ['blur', 'input'], whitespace: true },
  transaction_id: { required: true, message: '请填写渠道交易号', trigger: ['blur', 'input'], whitespace: true },
  amount: {
    required: true,
    type: 'number',
    message: '请填写实收金额',
    trigger: ['blur', 'input'],
  },
  remark: { required: true, message: '请填写核实依据', trigger: ['blur', 'input'], whitespace: true },
}

/** 支付单号下拉项：本单每次支付尝试一行，标注状态供选择 */
const paymentNoOptions = computed<SelectOption[]>(() =>
  (props.payments ?? []).map((p) => ({
    label: `${p.payment_no}（${paymentStatusText(p.status)}　￥${(p.amount ?? 0).toFixed(2)}）`,
    value: p.payment_no,
  })),
)

/**
 * 打开时按「未成功的支付单」预填支付单号与金额，减少手抄错号
 */
watch(
  () => props.show,
  (visible) => {
    if (!visible) {
      return
    }
    const first = (props.payments ?? []).find((p) => p.status !== PAYMENT_STATUS.SUCCESS) ?? props.payments?.[0]
    form.payment_no = first?.payment_no ?? ''
    form.amount = first?.amount ?? null
    form.transaction_id = ''
    form.remark = ''
    formRef.value?.restoreValidation()
  },
)

function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

/**
 * 提交标记：校验通过后调用后台接口，成功后由父级刷新详情
 */
async function submit() {
  if (!props.orderId) {
    return
  }
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    await markOrderPaid(props.orderId, {
      payment_no: form.payment_no.trim(),
      transaction_id: form.transaction_id.trim(),
      amount: form.amount ?? 0,
      remark: form.remark.trim(),
    })
    message.success('已标记为支付成功')
    emit('update:show', false)
    emit('success')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    submitting.value = false
  }
}
</script>
