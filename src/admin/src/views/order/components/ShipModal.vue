<template>
  <n-modal :show="show" preset="card" :title="title" style="width: min(460px, 94vw)" @update:show="onUpdateShow">
    <n-alert v-if="isBatch" type="info" :show-icon="true" style="margin-bottom: 12px">
      将对选中的 {{ orderIds.length }} 个配送单使用同一物流信息发货
    </n-alert>
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="left" label-width="80">
      <n-form-item label="物流公司" path="express_company">
        <n-select
          v-model:value="form.express_company"
          tag
          filterable
          clearable
          :options="companyOptions"
          placeholder="选择或输入物流公司"
        />
      </n-form-item>
      <n-form-item label="物流单号" path="express_no">
        <n-input v-model:value="form.express_no" placeholder="请输入物流单号" :input-props="{ autocomplete: 'off' }" />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onUpdateShow(false)">取消</n-button>
        <n-button type="primary" :loading="submitting" @click="submit">确认发货</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { shipOrder } from '@/api/order'

/**
 * 订单发货弹窗（列表页与详情弹窗共用）
 * 支持单笔与批量（同物流信息）发货；物流公司/单号必填
 */
const props = defineProps<{
  show: boolean
  /** 待发货订单 ID（单笔传单项数组，批量传多项） */
  orderIds: number[]
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const submitting = ref(false)
const form = reactive({ express_company: '', express_no: '' })

const rules: FormRules = {
  express_company: { required: true, message: '请选择或输入物流公司', trigger: ['blur', 'change'], type: 'string' },
  express_no: [
    { required: true, message: '请输入物流单号', trigger: ['blur', 'input'] },
    {
      validator: (_rule, value: string) => value.trim().length >= 5,
      message: '物流单号至少 5 位',
      trigger: ['blur', 'input'],
    },
  ],
}

const isBatch = computed(() => props.orderIds.length > 1)
const title = computed(() => (isBatch.value ? '批量发货' : '订单发货'))

/** 常用物流公司字典（可输入自定义值） */
const companyOptions = ['顺丰速运', '京东物流', '中通快递', '圆通速递', '韵达快递', '申通快递', '极兔速递', '邮政 EMS'].map((label) => ({
  label,
  value: label,
}))

function onUpdateShow(value: boolean) {
  emit('update:show', value)
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      form.express_company = ''
      form.express_no = ''
      formRef.value?.restoreValidation()
    }
  },
)

async function submit() {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    // 并行发货：单笔接口彼此独立，后端按 WHERE status=2 条件更新，天然幂等安全
    const results = await Promise.allSettled(
      props.orderIds.map((id) =>
        shipOrder(id, {
          express_company: form.express_company.trim(),
          express_no: form.express_no.trim(),
          remark: '后台发货',
        }),
      ),
    )
    const failCount = results.filter((r) => r.status === 'rejected').length
    const successCount = results.length - failCount
    if (failCount === 0) {
      message.success(`发货成功（${successCount} 单）`)
    } else {
      message.warning(`发货完成：成功 ${successCount} 单，失败 ${failCount} 单（失败订单可能状态已变更）`)
    }
    emit('update:show', false)
    emit('success')
  } finally {
    submitting.value = false
  }
}
</script>
