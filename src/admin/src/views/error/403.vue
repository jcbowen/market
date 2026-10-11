<template>
  <div class="forbidden-page">
    <n-result status="403" title="无访问权限" :description="description">
      <template #footer>
        <n-button v-if="homePath" type="primary" @click="router.replace(homePath)">返回首页</n-button>
        <n-button @click="onRelogin">重新登录</n-button>
      </template>
    </n-result>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { resolveHomePath } from '@/router'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

/** 兜底首页路径，当前管理员无任何可用页面时为空 */
const homePath = computed(() => resolveHomePath(authStore))

/** 提示文案：带来源路径时展示被拦截的访问地址 */
const description = computed(() => {
  const from = route.query.from as string | undefined
  return from
    ? `您没有访问「${from}」的权限，请联系管理员开通，或返回首页继续操作。`
    : '您没有访问该页面的权限，请联系管理员开通，或返回首页继续操作。'
})

/**
 * 重新登录
 * 主动清空登录态并前往登录页
 */
function onRelogin() {
  authStore.logout()
  router.replace('/login')
}
</script>

<style lang="scss" scoped>
.forbidden-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 60vh;
}
</style>
