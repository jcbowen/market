<template>
  <n-card title="创建优惠券">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="类型">
        <n-select v-model:value="form.type" :options="typeOptions" />
      </n-form-item>
      <n-form-item label="使用门槛">
        <n-input-number v-model:value="form.threshold" />
      </n-form-item>
      <n-form-item label="优惠值">
        <n-input-number v-model:value="form.discount_value" />
      </n-form-item>
      <n-form-item label="发放总量">
        <n-input-number v-model:value="form.total_count" />
      </n-form-item>
      <n-form-item label="每人限领">
        <n-input-number v-model:value="form.per_user_limit" />
      </n-form-item>
      <n-form-item label="有效期">
        <n-date-picker v-model:value="dateRange" type="datetimerange" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { createCoupon } from '@/api/marketing'
import type { Coupon } from '@/api/types'

/**
 * 创建优惠券页面
 */
const router = useRouter()
const dateRange = ref<[number, number] | null>(null)
const form = ref<Partial<Coupon>>({
  name: '',
  type: 1,
  threshold: 0,
  discount_value: 0,
  total_count: 100,
  per_user_limit: 1,
})
const typeOptions = [
  { label: '满减券', value: 1 },
  { label: '折扣券', value: 2 },
]

function formatTime(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`
}

async function submit() {
  const data = { ...form.value } as Coupon
  if (dateRange.value) {
    (data as any).start_time = formatTime(dateRange.value[0])
    ;(data as any).end_time = formatTime(dateRange.value[1])
  }
  const res = await createCoupon(data)
  if (res.data.code === 0) {
    router.back()
  }
}
</script>
