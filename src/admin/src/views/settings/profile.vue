<template>
  <n-card title="个人设置">
    <n-form :model="form" label-placement="left" label-width="100px" class="profile-form">
      <n-form-item label="用户名">
        <n-input :value="authStore.admin?.username || '-'" disabled />
      </n-form-item>
      <n-form-item label="角色">
        <n-input :value="authStore.admin?.role_name || '-'" disabled />
      </n-form-item>
      <n-form-item label="真实姓名">
        <n-input v-model:value="form.real_name" />
      </n-form-item>
      <n-form-item label="手机号">
        <n-input v-model:value="form.phone" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" @click="submit">保存</n-button>
          <n-button secondary @click="openPasswordModal">修改密码</n-button>
        </n-space>
      </n-form-item>
    </n-form>

    <!-- 修改密码弹窗 -->
    <n-modal v-model:show="showPasswordModal" title="修改密码" preset="card" style="width: 420px; max-width: 90vw" scrollable>
      <n-form :model="passwordForm" label-placement="top">
        <n-form-item label="旧密码">
          <n-input v-model:value="passwordForm.old_password" type="password" placeholder="请输入旧密码" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.new_password" type="password" :placeholder="`请输入新密码，${PASSWORD_LENGTH_TIP}`" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="passwordForm.confirm_password" type="password" placeholder="请再次输入新密码" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showPasswordModal = false">取消</n-button>
          <n-button type="primary" :loading="passwordLoading" @click="savePassword">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive } from 'vue'
import { useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { updateAdmin, changeAdminPassword } from '@/api/admin'
import { PASSWORD_LENGTH_TIP, isPasswordTooShort } from '@/constants/security'
import type { AdminInfo } from '@/api/types'

/**
 * 个人设置页面
 * 展示并修改当前登录管理员的真实姓名与手机号，支持修改登录密码
 */
const authStore = useAuthStore()
const message = useMessage()

const form = ref<Partial<AdminInfo>>({
  real_name: '',
  phone: '',
})

const showPasswordModal = ref(false)
const passwordLoading = ref(false)
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

/**
 * 打开修改密码弹窗并清空上次输入
 */
function openPasswordModal() {
  passwordForm.old_password = ''
  passwordForm.new_password = ''
  passwordForm.confirm_password = ''
  showPasswordModal.value = true
}

/**
 * 保存个人信息
 */
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

/**
 * 保存新密码
 * 校验非空与两次输入一致，成功后提示重新登录
 */
async function savePassword() {
  if (!passwordForm.old_password) {
    message.warning('请输入旧密码')
    return
  }
  if (!passwordForm.new_password || isPasswordTooShort(passwordForm.new_password)) {
    message.warning(`新${PASSWORD_LENGTH_TIP}`)
    return
  }
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    message.warning('两次输入的新密码不一致')
    return
  }

  const adminId = authStore.admin?.id
  if (!adminId) {
    message.error('未获取到管理员信息')
    return
  }

  passwordLoading.value = true
  try {
    await changeAdminPassword(adminId, {
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    message.success('密码修改成功，请重新登录')
    showPasswordModal.value = false
  } finally {
    passwordLoading.value = false
  }
}

onMounted(() => {
  if (authStore.admin) {
    form.value = { real_name: authStore.admin.real_name, phone: authStore.admin.phone }
  }
})
</script>

<style lang="scss" scoped>
.profile-form {
  max-width: 480px;
}
</style>
