<template>
  <view v-if="payload" class="crd-mask" @touchmove.stop.prevent>
    <view class="crd-dialog">
      <text class="crd-title">{{ payload.title }}</text>
      <view class="crd-body">
        <text class="crd-name">{{ payload.communityName }}</text>
        <text class="crd-sub">{{ payload.subtitle }}</text>
      </view>
      <view class="crd-actions">
        <view class="crd-btn" hover-class="crd-btn--active" @click="onOther">其他社区</view>
        <view class="crd-btn crd-btn--primary" hover-class="crd-btn--active" @click="onConfirm">就选这里</view>
      </view>
    </view>
  </view>
</template>

<script setup lang="ts">
/**
 * 社区推荐确认弹窗（挂载于各 tabbar 页面）
 * 展示启动时自动选中的社区（名称 + 距离/来源说明），提供「就选这里」确认
 * 与「其他社区」跳转社区选择页两条出路。
 * 选择本身已在 App.vue 启动流程中应用（保证应用始终可用），
 * 弹窗仅做告知与确认；用户选择其他社区后由社区选择页完成覆盖切换。
 */
import { computed } from 'vue'
import { useCommunityRecommendStore } from '@/stores/community-recommend'
import { goCommunitySelect } from '@/utils/navigate'

const recommendStore = useCommunityRecommendStore()

/** 待展示载荷，非空时渲染弹窗 */
const payload = computed(() => recommendStore.pending)

/**
 * 确认沿用当前推荐社区（已在启动流程中应用，仅关闭弹窗）
 */
function onConfirm() {
  recommendStore.clear()
}

/**
 * 前往社区选择页手动更换（关闭弹窗后跳转，选择页完成切换）
 */
function onOther() {
  recommendStore.clear()
  goCommunitySelect()
}
</script>

<style scoped>
/* 遮罩：盖过 uview-plus 弹层（z-index 10075） */
.crd-mask {
  position: fixed;
  inset: 0;
  z-index: 10099;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: rgba(0, 0, 0, 0.45);
}

.crd-dialog {
  width: 600rpx;
  overflow: hidden;
  border-radius: 24rpx;
  background-color: #ffffff;
}

.crd-title {
  display: block;
  margin: 40rpx 0 30rpx;
  font-size: 34rpx;
  font-weight: 600;
  color: #333333;
  text-align: center;
}

.crd-body {
  margin: 0 40rpx 40rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #f5f6f8;
}

.crd-name {
  display: block;
  font-size: 30rpx;
  font-weight: 500;
  color: #333333;
}

.crd-sub {
  display: block;
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #999999;
}

.crd-actions {
  display: flex;
  border-top: 1rpx solid #f0f0f0;
}

.crd-btn {
  flex: 1;
  padding: 28rpx 0;
  font-size: 30rpx;
  color: #666666;
  text-align: center;
}

.crd-btn--primary {
  color: #07c160;
  font-weight: 500;
}

.crd-btn--active {
  background-color: #f5f6f8;
}
</style>
