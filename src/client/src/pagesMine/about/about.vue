<template>
  <view class="about-page">
    <!-- 应用信息卡：logo + 名称 + 版本号 -->
    <view class="app-card">
      <image class="logo" src="/static/logo.png" mode="aspectFit" />
      <text class="app-name">社区商超</text>
      <text class="app-version">V{{ APP_VERSION }}</text>
    </view>

    <!-- 版本信息列表 -->
    <view class="info-card">
      <u-cell-group>
        <u-cell title="版本号" :value="APP_VERSION" />
        <u-cell title="版本码" :value="String(APP_VERSION_CODE)" />
        <u-cell title="运行环境" :value="APP_ENV_LABEL[APP_ENV]" />
        <u-cell title="版本环境" :value="APP_STAGE_LABEL[APP_STAGE]" />
        <u-cell v-if="APP_VERSION_RAW !== APP_VERSION" title="构建版本" :value="APP_VERSION_RAW" />
        <u-cell title="构建时间" :value="formatBuildTime(APP_BUILD_TIME)" />
      </u-cell-group>
    </view>

    <view class="copyright">
      <text>本应用基于开源项目构建 · 源码交付模式</text>
    </view>
  </view>
</template>

<script setup lang="ts">
/**
 * 关于页：展示应用版本号、版本码、运行环境、版本环境与构建信息
 * 数据全部来自构建期注入（vite.config.ts define）与运行时判定（utils/version.ts）
 */
import {
  APP_VERSION,
  APP_VERSION_CODE,
  APP_VERSION_RAW,
  APP_BUILD_TIME,
  APP_ENV,
  APP_ENV_LABEL,
  APP_STAGE,
  APP_STAGE_LABEL,
} from '@/utils/version'

/**
 * 格式化构建时间为本地可读格式（YYYY-MM-DD HH:mm）
 * @param iso ISO 8601 时间串
 * @returns 格式化结果；解析失败返回原串
 */
function formatBuildTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
</script>

<style scoped>
.about-page {
  min-height: 100vh;
  padding: 48rpx 32rpx;
  background-color: #f5f5f5;
  box-sizing: border-box;
}

.app-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 64rpx 0;
  margin-bottom: 32rpx;
  background-color: #ffffff;
  border-radius: 24rpx;
}

.logo {
  width: 144rpx;
  height: 144rpx;
  border-radius: 32rpx;
  margin-bottom: 24rpx;
}

.app-name {
  font-size: 36rpx;
  font-weight: 600;
  color: #303133;
}

.app-version {
  margin-top: 12rpx;
  font-size: 28rpx;
  color: #909193;
}

.info-card {
  background-color: #ffffff;
  border-radius: 24rpx;
  overflow: hidden;
}

.copyright {
  margin-top: 48rpx;
  text-align: center;
  font-size: 24rpx;
  color: #c0c4cc;
}
</style>
