<template>
  <view class="address-edit-page">
    <!-- 未选择社区：默认不允许社区外配送，阻断保存 -->
    <view v-if="communityChecked && !community" class="community-tip-card">
      <text class="community-tip-text">您还未选择社区，请先在「首页-切换社区」中选择所在社区后再添加收货地址</text>
    </view>

    <view class="form-card">
      <u-form :model="form" :rules="rules" ref="formRef" label-width="160rpx">
        <u-form-item label="联系人" prop="contact_name" border-bottom>
          <u-input v-model="form.contact_name" placeholder="请输入联系人姓名" border="none" />
        </u-form-item>
        <u-form-item label="手机号" prop="contact_phone" border-bottom>
          <u-input v-model="form.contact_phone" placeholder="请输入手机号" border="none" maxlength="11" />
        </u-form-item>

        <!-- 锁定模式：地址前缀取当前社区省市区，仅可填写详细地址 -->
        <template v-if="!allowOutside">
          <u-form-item label="所在社区" border-bottom>
            <view class="prefix-block">
              <text class="prefix-community">{{ community?.name || '未选择社区' }}</text>
              <text class="prefix-region">{{ prefixText || '社区省市区未配置，请联系管理员' }}</text>
            </view>
          </u-form-item>
          <u-form-item label="详细地址" prop="detail" border-bottom>
            <u-input v-model="form.detail" placeholder="请输入楼栋、单元、门牌号" border="none" />
          </u-form-item>
        </template>

        <!-- 自由模式：社区允许社区外配送，省市区可自由填写 -->
        <template v-else>
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
        </template>

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
import { ref, computed, onMounted } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { createAddress, updateAddress, getCommunity } from '@/api/user'
import { useUserStore } from '@/stores/user'
import type { Address, Community } from '@/api/types'

const userStore = useUserStore()
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
  community_id: 0,
  community_name: '',
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

/** 当前用户所属社区详情（含省市区与外配开关） */
const community = ref<Community | null>(null)
/** 社区详情是否已加载完成（避免未加载时误判为未选社区） */
const communityChecked = ref(false)

/** 是否允许社区外配送（默认不允许：未取到社区信息时同样锁定） */
const allowOutside = computed(() => !!community.value?.allow_outside_delivery)

/** 锁定模式展示的地址前缀（省市区） */
const prefixText = computed(() => {
  const c = community.value
  if (!c) return ''
  return `${c.province}${c.city}${c.district}`
})

onLoad((query) => {
  addressId.value = Number(query?.id) || 0
})

onMounted(() => {
  loadCommunity()
  if (addressId.value) {
    const eventChannel = getOpenerEventChannel()
    eventChannel.on('addressData', (data: Address) => {
      form.value = { ...form.value, ...data }
    })
  }
})

/**
 * 加载当前用户所属社区详情，用于判定地址前缀是否锁定
 */
async function loadCommunity() {
  communityChecked.value = false
  const communityId = userStore.userInfo?.community_id || userStore.communityInfo?.id || 0
  if (communityId) {
    try {
      const res = await getCommunity(communityId)
      community.value = res.data || null
    } catch {
      community.value = null
    }
  } else {
    community.value = null
  }
  communityChecked.value = true
}

/**
 * 保存地址
 */
async function onSave() {
  if (communityChecked.value && !community.value) {
    uni.showToast({ title: '请先选择社区', icon: 'none' })
    return
  }
  const valid = await formRef.value?.validate()
  if (!valid) return

  saving.value = true
  try {
    // 锁定模式下前缀由服务端按用户当前社区强制覆盖，这里同步填入社区省市区
    if (!allowOutside.value && community.value) {
      form.value.province = community.value.province
      form.value.city = community.value.city
      form.value.district = community.value.district
      form.value.community_id = community.value.id
    }
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

.community-tip-card {
  margin-bottom: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #fff7e6;
}

.community-tip-text {
  font-size: 26rpx;
  color: #fa8c16;
  line-height: 1.5;
}

.prefix-block {
  display: flex;
  flex-direction: column;
}

.prefix-community {
  font-size: 28rpx;
  color: #333333;
  font-weight: bold;
}

.prefix-region {
  margin-top: 6rpx;
  font-size: 26rpx;
  color: #666666;
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
