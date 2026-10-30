<template>
  <view class="payment-card">
    <view class="payment-title">
      <text class="payment-title-text">支付方式</text>
    </view>
    <view
      v-for="channel in channels"
      :key="channel.channel"
      class="payment-option"
      @click="select(channel)"
    >
      <u-icon
        :name="channelIcon(channel.channel)"
        :color="channelIconColor(channel.channel)"
        size="24"
      />
      <text class="payment-option-name">{{ channel.name }}</text>
      <view
        class="payment-radio"
        :class="{ checked: modelValue?.channel === channel.channel }"
      />
    </view>
    <view v-if="!channels.length" class="payment-empty">
      <text>暂无可用支付方式</text>
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getPaymentChannels } from '@/api/payment'
import type { PaymentChannel } from '@/api/types'

/**
 * 支付方式选择卡片
 * 从确认订单页抽取的公共组件：渠道列表（品牌图标 + 名称 + 右侧单选圆圈）直接
 * 渲染在页面流中（抖音收银台风格），组件挂载时自行加载可用渠道并默认选中第一个；
 * 供订单确认页与订单详情页共用，避免两份重复实现
 * 使用示例：
 *   <PaymentChannelCard v-model="selectedChannel" />
 *   const selectedChannel = ref<PaymentChannel | null>(null)
 */

const props = defineProps<{
  /** 当前选中的渠道（v-model） */
  modelValue: PaymentChannel | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: PaymentChannel | null): void
}>()

/** 可用支付渠道列表 */
const channels = ref<PaymentChannel[]>([])

/** 渠道品牌图标映射（uview-plus 图标名），未识别渠道回退人民币图标 */
const channelIconMap: Record<string, string> = {
  wechat: 'weixin-fill',
  alipay: 'zhifubao-circle-fill',
  cash: 'rmb-circle-fill',
}

/**
 * 渠道图标名
 * @param channel 渠道标识（wechat/alipay/cash）
 * @returns uview-plus 图标名，未识别渠道回退 rmb-circle-fill
 */
function channelIcon(channel: string): string {
  return channelIconMap[channel] || 'rmb-circle-fill'
}

/**
 * 渠道图标品牌色
 * @param channel 渠道标识（wechat/alipay/cash）
 * @returns 颜色值，未识别渠道用深灰
 */
function channelIconColor(channel: string): string {
  switch (channel) {
    case 'wechat': return '#07c160'
    case 'alipay': return '#1677ff'
    case 'cash': return '#ff9800'
    default: return '#666666'
  }
}

/**
 * 选中渠道并同步给父组件（v-model）
 * @param channel 被点击的渠道
 */
function select(channel: PaymentChannel): void {
  emit('update:modelValue', channel)
}

/**
 * 加载可用支付渠道，默认选中第一个；失败时保持空列表占位提示
 */
async function loadChannels(): Promise<void> {
  try {
    const res = await getPaymentChannels()
    const list = res.data?.channels || []
    channels.value = list
    if (list.length && !props.modelValue) {
      emit('update:modelValue', list[0])
    }
  } catch {
    channels.value = []
  }
}

onMounted(loadChannels)
</script>

<style scoped>
/* 支付方式卡片：独立置底（抖音收银台风格），每渠道一行（品牌图标+名称+右侧圆圈）
   外边距：纵向间距组件内置，左右边距由使用页按自身版式覆盖（如详情页 30rpx 边距） */
.payment-card {
  margin-top: 20rpx;
  padding: 0 30rpx 10rpx;
  background-color: #ffffff;
}

.payment-title {
  padding: 24rpx 0 8rpx;
}

.payment-title-text {
  font-size: 28rpx;
  color: #333333;
}

.payment-option {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 24rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
}

.payment-option:last-child {
  border-bottom: none;
}

.payment-option-name {
  flex: 1;
  font-size: 28rpx;
  color: #333333;
}

/* 单选圆点：未选中为灰色空心圆，选中为绿色实心 */
.payment-radio {
  width: 36rpx;
  height: 36rpx;
  box-sizing: border-box;
  border: 2rpx solid #cccccc;
  border-radius: 50%;
  background-color: transparent;
  transition: all 0.2s;
}

.payment-radio.checked {
  border: 10rpx solid #07c160;
}

/* 无可用渠道时的占位提示 */
.payment-empty {
  padding: 20rpx 0 24rpx;
  font-size: 26rpx;
  color: #999999;
}
</style>
