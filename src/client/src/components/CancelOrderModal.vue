<template>
  <u-popup :show="show" mode="bottom" :round="16" :close-on-click-overlay="!submitting" @close="onClose">
    <view class="cancel-modal">
      <view class="cancel-header">
        <text class="cancel-title">取消订单</text>
        <u-icon name="close" size="20" color="#999999" @click="onClose" />
      </view>

      <view class="section-title">请选择取消原因</view>
      <view class="reason-list">
        <view
          v-for="reason in CANCEL_REASONS"
          :key="reason"
          class="reason-item"
          @click="selectedReason = reason"
        >
          <!-- uview-plus 无 radio-mark/circle 图标，改自绘单选圈（与 RefundModal 勾选样式统一） -->
          <view :class="['check-circle', selectedReason === reason ? 'checked' : '']">
            <u-icon v-if="selectedReason === reason" name="checkmark" size="12" color="#ffffff" />
          </view>
          <text class="reason-text">{{ reason }}</text>
        </view>
      </view>

      <view v-if="selectedReason === '其他'" class="reason-input">
        <u-input v-model="customReason" placeholder="请填写取消原因" border="surround" maxlength="100" />
      </view>

      <view class="cancel-footer">
        <u-button
          type="success"
          size="large"
          text="确认取消"
          :loading="submitting"
          @click="onSubmit"
        />
      </view>
    </view>
  </u-popup>
</template>

<script setup lang="ts">
/**
 * 取消订单弹窗
 * 预设原因单选 + 可选补充输入，提交时携带 reason（订单列表与详情页共用）
 */
import { ref, watch } from 'vue'
import { cancelOrder } from '@/api/order'
import { CANCEL_REASONS } from '@/utils/order'

const props = defineProps<{
  /** 弹窗显示状态（v-model） */
  show: boolean
  /** 待取消的订单 ID */
  orderId: number
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'success'): void
}>()

const selectedReason = ref(CANCEL_REASONS[0])
const customReason = ref('')
const submitting = ref(false)

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      selectedReason.value = CANCEL_REASONS[0]
      customReason.value = ''
    }
  },
)

/** 关闭弹窗 */
function onClose() {
  if (submitting.value) return
  emit('update:show', false)
}

/**
 * 提交取消请求
 * @throws 请求失败由 request 统一提示
 */
async function onSubmit() {
  const reason = selectedReason.value === '其他' ? customReason.value.trim() : selectedReason.value
  if (!reason) {
    uni.showToast({ title: '请填写取消原因', icon: 'none' })
    return
  }
  submitting.value = true
  try {
    await cancelOrder(props.orderId, reason)
    emit('update:show', false)
    emit('success')
  } catch {
    // request 已统一提示错误
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.cancel-modal {
  padding: 30rpx 30rpx calc(30rpx + env(safe-area-inset-bottom));
}

.cancel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.cancel-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.section-title {
  margin-bottom: 16rpx;
  font-size: 26rpx;
  color: #666666;
}

.reason-item {
  display: flex;
  align-items: center;
  gap: 14rpx;
  padding: 20rpx 10rpx;
}

/* 自绘单选圈：未选中灰边空心，选中绿色填充（与 RefundModal 勾选样式统一） */
.check-circle {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 36rpx;
  height: 36rpx;
  border: 2rpx solid #dddddd;
  border-radius: 50%;
  background-color: #ffffff;
}

.check-circle.checked {
  border-color: #07c160;
  background-color: #07c160;
}

.reason-text {
  font-size: 28rpx;
  color: #333333;
}

.reason-input {
  margin: 10rpx 0 20rpx;
}
</style>
