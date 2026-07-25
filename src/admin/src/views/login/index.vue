<template>
  <div class="login-page">
    <n-card class="login-card" title="社区商超后台管理">
      <n-form
        ref="formRef"
        :model="formData"
        :rules="rules"
        label-placement="left"
        label-width="auto"
      >
        <n-form-item label="账号" path="username">
          <n-input v-model:value="formData.username" placeholder="请输入管理员账号" />
        </n-form-item>
        <n-form-item label="密码" path="password">
          <n-input
            v-model:value="formData.password"
            type="password"
            placeholder="请输入密码"
            show-password-on="mousedown"
            @keydown.enter="onLogin"
          />
        </n-form-item>
        <n-button type="primary" block :loading="loading" @click="onLogin">登录</n-button>
      </n-form>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()

/** 表单引用 */
const formRef = ref<FormInst | null>(null)
/** 登录加载状态 */
const loading = ref(false)

/** 登录表单数据 */
const formData = ref({
  username: '',
  password: '',
})

/** 表单校验规则 */
const rules: FormRules = {
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

/**
 * 登录提交
 * 校验表单后调用登录接口，成功后跳转工作台
 */
async function onLogin() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  loading.value = true
  try {
    await authStore.login(formData.value)
    message.success('登录成功')
    router.push('/dashboard')
  } catch (error: unknown) {
    const errMsg = error instanceof Error ? error.message : '登录失败，请重试'
    message.error(errMsg)
  } finally {
    loading.value = false
  }
}
</script>

<style lang="scss" scoped>
.login-page {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f5f5f5;
}
.login-card {
  width: 400px;
  max-width: calc(100vw - 32px);
}
</style>
