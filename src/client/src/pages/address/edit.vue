<template>
  <view class="address-edit-page">
    <view class="form-card">
      <u-form :model="form" :rules="rules" ref="formRef" label-width="160rpx">
        <u-form-item label="联系人" prop="contact_name" border-bottom>
          <u-input v-model="form.contact_name" placeholder="请输入联系人姓名" border="none" />
        </u-form-item>
        <u-form-item label="手机号" prop="contact_phone" border-bottom>
          <u-input v-model="form.contact_phone" placeholder="请输入手机号" border="none" maxlength="11" />
        </u-form-item>
        <u-form-item label="省份" border-bottom>
          <u-input v-model="form.province" placeholder="请输入省份" border="none" />
        </u-form-item>
        <u-form-item label="城市" border-bottom>
          <u-input v-model="form.city" placeholder="请输入城市" border="none" />
        </u-form-item>
        <u-form-item label="区县" border-bottom>
          <u-input v-model="form.district" placeholder="请输入区县" border="none" />
        </u-form-item>
        <u-form-item label="详细地址" prop="detail" border-bottom>
          <u-input v-model="form.detail" placeholder="请输入详细地址" border="none" />
        </u-form-item>
        <u-form-item label="标签">
          <u-input v-model="form.tag" placeholder="如：家、公司" border="none" />
        </u-form-item>
      </u-form>
    </view>

    <view class="default-card">
      <text>设为默认地址</text>
      <u-switch v-model="form.is_default" />
    </view>

    <view class="save-btn" @click="onSave">
      <u-button type="success" text="保存" :loading="saving" />
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { createAddress, updateAddress } from '@/api/user'
import type { Address } from '@/api/types'

const addressId = ref(0)
const saving = ref(false)
const formRef = ref()

const form = ref<Address>({
  id: 0,
  contact_name: '',
  contact_phone: '',
  province: '',
  city: '',
  district: '',
  detail: '',
  tag: '',
  is_default: false,
})

const rules = ref({
  contact_name: [{ required: true, message: '请输入联系人', trigger: ['blur', 'change'] }],
  contact_phone: [
    { required: true, message: '请输入手机号', trigger: ['blur', 'change'] },
    { pattern: /^1\d{10}$/, message: '手机号格式错误', trigger: ['blur', 'change'] },
  ],
  detail: [{ required: true, message: '请输入详细地址', trigger: ['blur', 'change'] }],
})

onLoad((query) => {
  addressId.value = Number(query?.id) || 0
})

onMounted(() => {
  if (addressId.value) {
    const eventChannel = getOpenerEventChannel()
    eventChannel.on('addressData', (data: Address) => {
      form.value = { ...data }
    })
  }
})

/**
 * 保存地址
 */
async function onSave() {
  const valid = await formRef.value?.validate()
  if (!valid) return

  saving.value = true
  try {
    if (addressId.value) {
      await updateAddress(addressId.value, form.value)
    } else {
      await createAddress(form.value)
    }
    uni.showToast({ title: '保存成功', icon: 'success' })
    setTimeout(() => uni.navigateBack({}), 1000)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.address-edit-page {
  min-height: 100vh;
  padding: 20rpx 30rpx;
  background-color: #f5f5f5;
}

.form-card {
  padding: 0 20rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.default-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 20rpx;
  padding: 24rpx 30rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
  font-size: 28rpx;
  color: #333333;
}

.save-btn {
  margin-top: 40rpx;
}

.save-btn .u-button {
  width: 100%;
}
</style>
