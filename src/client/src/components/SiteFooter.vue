<template>
  <view v-if="footerText" class="site-footer">
    <text class="site-footer-text">{{ footerText }}</text>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getSiteInfo, type SiteBrand } from '@/api/site'

/**
 * 站点页脚（版权与备案号）
 * 文案取自「站点设置」，未配置时整块不渲染、不占位；
 * 模块级缓存，多个页面共用只发起一次请求
 */
let cached: SiteBrand | null = null
let pending: Promise<SiteBrand | null> | null = null

const brand = ref<SiteBrand | null>(cached)

const footerText = computed(() => {
  const b = brand.value
  if (!b) return ''
  return [b.copyright_text, b.icp_number].filter(Boolean).join(' · ')
})

/**
 * 加载站点品牌信息：命中缓存直接用，未命中则发起请求
 * 并发到达的页面共享同一次请求；失败按不展示降级（getSiteInfo 内部已吞异常）
 */
async function load() {
  if (cached) {
    brand.value = cached
    return
  }
  pending = pending ?? getSiteInfo().then((data) => (cached = data))
  brand.value = await pending
}

onMounted(load)
</script>

<style lang="scss" scoped>
.site-footer {
  padding: 32rpx 24rpx 48rpx;
  text-align: center;
}

.site-footer-text {
  font-size: 22rpx;
  color: #999999;
  line-height: 1.6;
  word-break: break-all;
}
</style>
