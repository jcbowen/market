<template>
  <n-card title="创建秒杀活动">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="活动名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="活动时间">
        <n-date-picker v-model:value="dateRange" type="datetimerange" />
      </n-form-item>
      <n-form-item label="SKU ID">
        <n-input-number v-model:value="item.sku_id" />
      </n-form-item>
      <n-form-item label="秒杀价">
        <n-input-number v-model:value="item.flash_price" />
      </n-form-item>
      <n-form-item label="秒杀库存">
        <n-input-number v-model:value="item.stock" />
      </n-form-item>
      <n-form-item label="每人限购">
        <n-input-number v-model:value="item.limit_per_user" />
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
import { createSeckill } from '@/api/marketing'
import type { Seckill } from '@/api/types'

/**
 * 创建秒杀活动页面
 * 仅支持单个 SKU 的简单秒杀创建
 */
const router = useRouter()
const dateRange = ref<[number, number] | null>(null)
const form = ref<Partial<Seckill & { name: string }>>({ name: '' })
const item = ref({ sku_id: undefined as number | undefined, flash_price: undefined as number | undefined, stock: undefined as number | undefined, limit_per_user: 1 })
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
    data.items = [{ ...item.value }]
    const res = await createSeckill(data)
    if (res.data.code === 0) {
      router.back()
    }
  } finally {
    saving.value = false
  }
}
</script>
