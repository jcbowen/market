<template>
  <div class="pos-login-page">
    <div class="login-panel">
      <div class="brand">
        <n-icon :size="44" class="brand-icon">
          <CashOutline />
        </n-icon>
        <div class="brand-name">门店收银台</div>
        <div class="brand-sub">扫码枪 / 触屏收银专用工作台</div>
      </div>
      <n-form
        ref="formRef"
        :model="formData"
        :rules="rules"
        label-placement="left"
        label-width="auto"
        :show-label="false"
      >
        <n-form-item path="username">
          <n-input v-model:value="formData.username" placeholder="员工账号" size="large" />
        </n-form-item>
        <n-form-item path="password">
          <n-input
            v-model:value="formData.password"
            type="password"
            placeholder="密码"
            show-password-on="mousedown"
            size="large"
            @keydown.enter="onLogin"
          />
        </n-form-item>
        <n-button type="primary" block size="large" :loading="loading" class="login-btn" @click="onLogin">
          进入收银台
        </n-button>
      </n-form>
    </div>
    <div class="page-footer">收银数据与本店实时同步，退出或交班后需重新登录</div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { CashOutline } from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
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
  username: [{ required: true, message: '请输入员工账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

/**
 * 解析登录成功后的回跳地址
 * 仅允许回跳收银台域内路径（/pos 前缀），防止开放重定向与误回主系统
 * @returns 回跳目标路径
 */
function resolveRedirectPath(): string {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect.startsWith('/pos/') && redirect !== '/pos/login') {
    return redirect
  }
  return '/pos/cashier'
}

/**
 * 登录提交
 * 成功后进入收银台工作台（无班次时由工作台内开工引导接管）
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
    router.push(resolveRedirectPath())
  } catch (error: unknown) {
    const errMsg = error instanceof Error ? error.message : '登录失败，请重试'
    message.error(errMsg)
  } finally {
    loading.value = false
  }
}
</script>

<style lang="scss" scoped>
.pos-login-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 24px;
  // 收银台品牌绿渐变背景，与主系统灰底区分
  background: linear-gradient(160deg, #0a8f4d 0%, #07c160 60%, #35d68a 100%);
}
.login-panel {
  width: 400px;
  max-width: calc(100vw - 32px);
  background: #fff;
  border-radius: 14px;
  padding: 32px 36px 28px;
  box-shadow: 0 8px 32px rgba(0, 60, 30, 0.18);
}
.brand {
  text-align: center;
  margin-bottom: 24px;
}
.brand-icon {
  color: #07c160;
}
.brand-name {
  font-size: 20px;
  font-weight: 700;
  margin-top: 8px;
}
.brand-sub {
  font-size: 12px;
  color: #999;
  margin-top: 4px;
}
.login-btn {
  margin-top: 4px;
}
.page-footer {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.75);
}
</style>
