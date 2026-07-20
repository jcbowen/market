<template>
  <n-card title="新建盘点单">
    <n-space vertical>
      <n-input v-model:value="remark" placeholder="备注" />
      <n-space>
        <n-button type="primary" @click="submit">创建</n-button>
        <n-button @click="router.back()">返回</n-button>
      </n-space>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { createInventoryCheck } from '@/api/inventory'

/**
 * 新建盘点单页面
 * 创建盘点单后跳转到盘点详情录入实盘数据
 */
const router = useRouter()
const remark = ref('')

async function submit() {
  const res = await createInventoryCheck({ remark: remark.value } as any)
  if (res.data.code === 0) {
    router.push(`/stock/check/${res.data.data.id}`)
  }
}
</script>
