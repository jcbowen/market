<template>
  <n-card title="个人设置">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="真实姓名">
        <n-input v-model:value="form.real_name" />
      </n-form-item>
      <n-form-item label="手机号">
        <n-input v-model:value="form.phone" />
      </n-form-item>
      <n-form-item>
        <n-button type="primary" @click="submit">保存</n-button>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import type { AdminInfo } from '@/api/types'

/**
 * 个人设置页面
 * 当前为前端本地展示，保存接口待对接
 */
const authStore = useAuthStore()
const form = ref<Partial<AdminInfo>>({
  real_name: '',
  phone: '',
})

onMounted(() => {
  if (authStore.admin) {
    form.value = { ...authStore.admin }
  }
})

function submit() {
  // 待对接修改个人信息接口
}
</script>
