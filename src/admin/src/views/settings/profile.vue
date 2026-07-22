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
import { useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { updateAdmin } from '@/api/admin'
import type { AdminInfo } from '@/api/types'

/**
 * 个人设置页面
 * 展示并修改当前登录管理员的真实姓名与手机号
 */
const authStore = useAuthStore()
const message = useMessage()
const form = ref<Partial<AdminInfo>>({
  real_name: '',
  phone: '',
})

onMounted(() => {
  if (authStore.admin) {
    form.value = { real_name: authStore.admin.real_name, phone: authStore.admin.phone }
  }
})

async function submit() {
  if (!authStore.admin) return
  const res = await updateAdmin(authStore.admin.id, {
    real_name: form.value.real_name,
    phone: form.value.phone,
  })
  if (res.data.code === 0) {
    message.success('保存成功')
    authStore.fetchAdminInfo()
  } else {
    message.error(res.data.message || '保存失败')
  }
}
</script>
