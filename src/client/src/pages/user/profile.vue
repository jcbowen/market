<template>
  <view class="profile-page">
    <!-- 顶部头像卡：渐变底 + 头像（点击更换）+ 昵称/手机号 -->
    <view class="hero">
      <!-- 微信小程序：使用原生头像选择能力（open-type=chooseAvatar） -->
      <!-- #ifdef MP-WEIXIN -->
      <button class="avatar-btn" open-type="chooseAvatar" @chooseavatar="onChooseAvatar">
        <image class="avatar" :src="form.avatar || '/static/default-avatar.png'" mode="aspectFill" />
        <view class="avatar-badge">
          <u-icon name="camera" color="#ffffff" size="13" />
        </view>
      </button>
      <!-- #endif -->
      <!-- #ifndef MP-WEIXIN -->
      <view class="avatar-btn" @click="onChooseAvatarTap">
        <image class="avatar" :src="form.avatar || '/static/default-avatar.png'" mode="aspectFill" />
        <view class="avatar-badge">
          <u-icon name="camera" color="#ffffff" size="13" />
        </view>
      </view>
      <!-- #endif -->
      <view class="hero-info">
        <text class="hero-nickname">{{ form.nickname || '未设置昵称' }}</text>
        <text class="hero-phone">{{ maskPhone(form.phone || '') }}</text>
      </view>
      <text class="hero-tip">点头像可更换</text>
    </view>

    <!-- 资料卡 -->
    <view class="card">
      <view class="cell">
        <text class="cell-label">昵称</text>
        <view class="cell-value">
          <!-- 微信小程序：type=nickname 唤起键盘上方的「微信昵称」快捷填入 -->
          <!-- #ifdef MP-WEIXIN -->
          <input
            v-model="form.nickname"
            class="cell-input"
            type="nickname"
            placeholder="请输入昵称"
            placeholder-class="input-placeholder"
            maxlength="30"
          />
          <!-- #endif -->
          <!-- #ifndef MP-WEIXIN -->
          <input
            v-model="form.nickname"
            class="cell-input"
            type="text"
            placeholder="请输入昵称"
            placeholder-class="input-placeholder"
            maxlength="30"
          />
          <!-- #endif -->
        </view>
      </view>
      <view class="cell" @click="genderPickerShow = true">
        <text class="cell-label">性别</text>
        <view class="cell-value">
          <text class="cell-text" :class="{ placeholder: !genderText }">{{ genderText || '请选择' }}</text>
          <u-icon name="arrow-right" color="#c0c4cc" size="14" />
        </view>
      </view>
      <view class="cell" @click="birthdayPickerShow = true">
        <text class="cell-label">生日</text>
        <view class="cell-value">
          <text class="cell-text" :class="{ placeholder: !form.birthday }">{{ form.birthday || '请选择' }}</text>
          <u-icon name="arrow-right" color="#c0c4cc" size="14" />
        </view>
      </view>
      <view class="cell cell-last">
        <text class="cell-label">手机号</text>
        <view class="cell-value">
          <text class="cell-text muted">{{ maskPhone(form.phone || '') }}</text>
        </view>
      </view>
    </view>
    <text class="cell-note">手机号为登录凭证，不可修改</text>

    <!-- 事件必须直接绑在 u-button 上：其 stop 属性默认 true 会拦截冒泡，外层 view 收不到点击 -->
    <view class="save-btn">
      <u-button type="primary" text="保存" shape="circle" :loading="saving" @click="onSave" />
    </view>

    <!-- 性别选择器（受控模式，确认后需手动收起；close-on-click-overlay 点遮罩可关闭） -->
    <u-picker
      :show="genderPickerShow"
      :columns="genderColumns"
      :default-index="genderDefaultIndex"
      title="选择性别"
      :close-on-click-overlay="true"
      @confirm="onGenderConfirm"
      @cancel="genderPickerShow = false"
      @close="genderPickerShow = false"
    ></u-picker>

    <!-- 生日选择器：mode=date 返回时间戳，maxDate 禁选未来日期 -->
    <u-datetime-picker
      :show="birthdayPickerShow"
      mode="date"
      :model-value="birthdayTs"
      :min-date="minBirthdayTs"
      :max-date="maxBirthdayTs"
      title="选择生日"
      :close-on-click-overlay="true"
      @confirm="onBirthdayConfirm"
      @cancel="birthdayPickerShow = false"
      @close="birthdayPickerShow = false"
    ></u-datetime-picker>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { getUserInfo, updateUserProfile, uploadUserAvatar } from '@/api/user'
import { useUserStore } from '@/stores/user'
import { goLogin } from '@/utils/navigate'
import { maskPhone } from '@/utils/format'

const userStore = useUserStore()
const saving = ref(false)

const form = ref<{ phone: string; nickname: string; avatar: string; gender: number; birthday: string }>({
  phone: '',
  nickname: '',
  avatar: '',
  gender: 0,
  birthday: '',
})

// --- 头像选择与上传 ---
// 口径：选图后立即上传并保存（微信 chooseAvatar 只回临时路径必须立即上传，两端体验统一）

/**
 * H5/支付宝等端选头像：唤起系统相册/拍照，取第一张后走统一上传保存
 */
function onChooseAvatarTap() {
  uni.chooseImage({
    count: 1,
    sizeType: ['compressed'],
    success: (res) => {
      const filePath = res.tempFilePaths?.[0]
      if (filePath) uploadAvatarAndSave(filePath)
    },
  })
}

/**
 * 微信小程序头像选择回调（button open-type=chooseAvatar）
 * @param e 事件对象，e.detail.avatarUrl 为微信返回的头像临时路径
 */
function onChooseAvatar(e: { detail: { avatarUrl: string } }) {
  const filePath = e.detail?.avatarUrl
  if (filePath) uploadAvatarAndSave(filePath)
}

/**
 * 上传头像并立即保存到用户资料
 * 上传换附件 URL → PUT profile 携带 avatar（服务端非空才覆盖）→ 刷新本地登录态
 * @param filePath 本地临时文件路径
 */
async function uploadAvatarAndSave(filePath: string) {
  uni.showLoading({ title: '上传中' })
  try {
    const up = await uploadUserAvatar(filePath)
    const url = up.data?.url
    if (!url) return
    await updateUserProfile({ avatar: url })
    form.value.avatar = url
    uni.showToast({ title: '头像已更新', icon: 'success' })
    userStore.fetchUserInfo()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    uni.hideLoading()
  }
}

// --- 性别选择 ---
const genderPickerShow = ref(false)
const genderColumns = ref([['男', '女']])
const genderIndex = ref(-1)

/** 回显文案：未选择（-1）或未知（0）均显示「保密」 */
const genderText = computed(() => (genderIndex.value === 0 ? '男' : genderIndex.value === 1 ? '女' : '保密'))

/** 选择器回显默认索引：未选择时定位到「保密」概念外的第一项 */
const genderDefaultIndex = computed(() => [genderIndex.value < 0 ? 0 : genderIndex.value])

/**
 * 性别确认：记录索引并收起选择器（u-picker 为受控模式）
 * 提交口径：选男/女才传 gender（1/2），「保密」不传保持服务端现值
 * @param e 确认事件，e.indexs 为选中索引数组
 */
function onGenderConfirm(e: { indexs: number[] }) {
  genderIndex.value = e.indexs[0]
  genderPickerShow.value = false
}

// --- 生日选择 ---
const birthdayPickerShow = ref(false)
/** 生日下限：1920-01-01（组件默认仅前 10 年，无法覆盖真实生日场景） */
const minBirthdayTs = new Date('1920-01-01').getTime()
/** 生日上限：今天（禁选未来日期） */
const maxBirthdayTs = Date.now()

/** 生日回显时间戳（空串时回退到 2000-01-01 附近的选择位置） */
const birthdayTs = computed(() => {
  if (!form.value.birthday) return new Date('2000-01-01').getTime()
  return new Date(form.value.birthday.replace(/-/g, '/')).getTime()
})

/**
 * 生日确认：时间戳转 YYYY-MM-DD 并收起选择器（u-datetime-picker 为受控模式）
 * @param e 确认事件，e.value 为选中的时间戳（毫秒）
 */
function onBirthdayConfirm(e: { value: number }) {
  const d = new Date(e?.value)
  if (Number.isNaN(d.getTime())) return
  form.value.birthday = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  birthdayPickerShow.value = false
}

onShow(async () => {
  // 未登录直接回登录页（本页无独立浏览价值）
  if (!userStore.isLogin) {
    goLogin('/pages/user/profile')
    return
  }
  try {
    const res = await getUserInfo()
    const info = res.data
    form.value = {
      phone: info?.phone || '',
      nickname: info?.nickname || '',
      avatar: info?.avatar || '',
      gender: info?.gender || 0,
      birthday: info?.birthday || '',
    }
    genderIndex.value = info?.gender === 1 ? 0 : info?.gender === 2 ? 1 : -1
  } catch {
    // 拉取失败保持空表单，保存时昵称为空不会被提交覆盖
  }
})

/**
 * 保存基础资料：昵称/生日非空才携带（与后端「非空才覆盖」口径一致），
 * 性别仅在明确选择男/女时携带；头像已随选择即时保存，此处不再提交；
 * 成功后刷新本地 userInfo 并返回
 */
async function onSave() {
  saving.value = true
  try {
    await updateUserProfile({
      nickname: form.value.nickname.trim() || undefined,
      gender: genderIndex.value >= 0 ? genderIndex.value + 1 : undefined,
      birthday: form.value.birthday || undefined,
    })
    uni.showToast({ title: '保存成功', icon: 'success' })
    await userStore.fetchUserInfo()
    setTimeout(() => uni.navigateBack(), 600)
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.profile-page {
  min-height: 100vh;
  padding-bottom: 60rpx;
  background-color: #f5f6f8;
}

/* --- 顶部头像卡 --- */
.hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 56rpx 40rpx 44rpx;
  background: linear-gradient(160deg, #07c160 0%, #05a04f 100%);
}

.avatar-btn {
  position: relative;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  line-height: 1;
}

/* 重置微信 button 自带样式（边框/内边距） */
.avatar-btn::after {
  border: none;
}

.avatar {
  width: 144rpx;
  height: 144rpx;
  border-radius: 50%;
  border: 6rpx solid rgba(255, 255, 255, 0.75);
  background-color: #ffffff;
  display: block;
}

.avatar-badge {
  position: absolute;
  right: 2rpx;
  bottom: 2rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44rpx;
  height: 44rpx;
  border-radius: 50%;
  background-color: rgba(0, 0, 0, 0.45);
}

.hero-info {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-top: 20rpx;
}

.hero-nickname {
  max-width: 480rpx;
  font-size: 34rpx;
  font-weight: 600;
  color: #ffffff;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hero-phone {
  margin-top: 8rpx;
  font-size: 26rpx;
  color: rgba(255, 255, 255, 0.85);
}

.hero-tip {
  margin-top: 14rpx;
  font-size: 22rpx;
  color: rgba(255, 255, 255, 0.65);
}

/* --- 资料卡 --- */
.card {
  margin: 24rpx 24rpx 0;
  border-radius: 20rpx;
  background-color: #ffffff;
  overflow: hidden;
}

.cell {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 104rpx;
  padding: 0 30rpx;
  border-bottom: 1rpx solid #f2f3f5;
}

.cell-last {
  border-bottom: none;
}

.cell-label {
  flex-shrink: 0;
  width: 150rpx;
  font-size: 30rpx;
  color: #333333;
}

.cell-value {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: flex-end;
  gap: 8rpx;
}

.cell-input {
  width: 100%;
  font-size: 30rpx;
  color: #333333;
  text-align: right;
}

.input-placeholder {
  color: #c0c4cc;
}

.cell-text {
  font-size: 30rpx;
  color: #333333;
}

.cell-text.placeholder {
  color: #c0c4cc;
}

.cell-text.muted {
  color: #999999;
}

.cell-note {
  display: block;
  margin: 16rpx 40rpx 0;
  font-size: 22rpx;
  color: #999999;
}

/* --- 保存按钮 --- */
.save-btn {
  margin: 56rpx 40rpx 0;
}
</style>
