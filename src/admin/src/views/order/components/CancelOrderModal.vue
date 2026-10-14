<template>
  <n-modal :show="show" preset="card" title="取消订单" style="width: min(460px, 94vw)" @update:show="onUpdateShow">
    <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
      确认后订单将取消并释放占用库存，该操作不可恢复。
    </n-alert>
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="left" label-width="80">
      <n-form-item label="取消原因" path="reason">
        <n-input
          v-model:value="form.reason"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-count
          placeholder="请填写取消原因"
        />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onUpdateShow(false)">返回</n-button>
        <n-button type="error" :loading="submitting" @click="submit">确认取消</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { cancelOrder } from '@/api/order'

/**
 * 后台取消订单弹窗（列表页与详情弹窗共用）
 * 取消原因必填，走专用取消接口（回补库存、写取消原因/时间）
 */
const props = defineProps<{
  show: boolean
  /** 待取消订单 ID */
  orderId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const submitting = ref(false)
const form = reactive({ reason: '' })

const rules: FormRules = {
  reason: { required: true, message: '请填写取消原因', trigger: ['blur', 'input'], whitespace: true },
}

function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      form.reason = ''
      formRef.value?.restoreValidation()
    }
  },
)

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
    await cancelOrder(props.orderId, form.reason.trim())
    message.success('订单已取消，占用库存已释放')
    emit('update:show', false)
    emit('success')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    submitting.value = false
  }
}
</script>
