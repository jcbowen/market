<template>
  <view v-if="footerHTML" class="site-footer">
    <!-- 富文本渲染（服务端白名单消毒）：版权与备案号合并，链接经 target/rel 处理 -->
    <rich-text class="site-footer-text" :nodes="footerHTML"></rich-text>
  </view>
</template>

<script lang="ts">
import { ref } from 'vue'
import type { SiteBrand } from '@/api/site'

/**
 * 模块级共享状态：所有页面的 SiteFooter 实例共用一份，
 * 整个会话只发一次请求（成功后）；失败状态全局共享，任一页面 onShow 都能触发重试
 */
type FooterStatus = 'idle' | 'loading' | 'success' | 'failed'

const status = ref<FooterStatus>('idle')
const brand = ref<SiteBrand | null>(null)

export default {}
</script>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { getSiteInfo } from '@/api/site'

/**
 * 站点页脚（版权与备案号富文本）
 * 文案取自「站点设置」，未配置时整块不渲染、不占位；
 * 状态机语义见文件头模块级状态：仅 success 回显，failed 在下次 onShow 重试
 * （tab 页常驻内存不会重新 onMounted，onShow 覆盖切 tab、返回与 App 回前台三种重试时机）
 */
const footerHTML = computed(() => brand.value?.footer_html || '')

/**
 * 加载站点品牌信息：success 直接复用，loading 合并并发，failed/idle 发起请求
 * 请求成功才写入 brand（回显），失败置 failed 保持不展示（getSiteInfo 内部已吞异常）
 */
function load() {
  if (status.value === 'success' || status.value === 'loading') return
  status.value = 'loading'
  getSiteInfo().then((data) => {
    if (data) {
      brand.value = data
      status.value = 'success'
    } else {
      status.value = 'failed'
    }
  })
}

onMounted(load)
onShow(load)
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
