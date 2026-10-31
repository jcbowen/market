<template>
  <n-card title="创建满减活动">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="活动名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="满减门槛">
        <n-input-number v-model:value="form.threshold" />
      </n-form-item>
      <n-form-item label="减免金额">
        <n-input-number v-model:value="form.reduce_amount" />
      </n-form-item>
      <n-form-item label="活动时间">
        <n-date-picker v-model:value="dateRange" type="datetimerange" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" :loading="saving" :disabled="saving" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { createFullReduction } from '@/api/marketing'
import type { FullReduction } from '@/api/types'

/**
 * 创建满减活动页面
 */
const router = useRouter()
const dateRange = ref<[number, number] | null>(null)
const form = ref<Partial<FullReduction>>({
  name: '',
  threshold: 0,
  reduce_amount: 0,
})
/** 保存进行中标志：防连击重复创建活动 */
const saving = ref(false)

function formatTime(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`
}

async function submit() {
  if (saving.value) return
  saving.value = true
  try {
    const data: any = { ...form.value }
    if (dateRange.value) {
      data.start_time = formatTime(dateRange.value[0])
      data.end_time = formatTime(dateRange.value[1])
    }
    const res = await createFullReduction(data)
    if (res.data.code === 0) {
      router.back()
    }
  } finally {
    saving.value = false
  }
}
</script>
