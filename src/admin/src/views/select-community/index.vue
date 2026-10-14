<template>
  <div class="select-community-page">
    <n-card class="select-card" :bordered="false">
      <div class="select-head">
        <h2 class="select-title">选择社区</h2>
        <p class="select-subtitle">当前账号可管理多个社区，请选择要进入的社区</p>
      </div>

      <n-spin :show="loading">
        <div class="community-grid">
          <div
            v-for="cm in communities"
            :key="cm.id"
            class="community-card"
            @click="onPick(cm)"
          >
            <div class="community-card-body">
              <n-icon size="22" color="#07c160">
                <storefront-outline />
              </n-icon>
              <span class="community-card-name">{{ cm.name }}</span>
            </div>
            <n-tag v-if="cm.status !== 1" type="warning" size="small">停业中</n-tag>
          </div>
        </div>
        <n-empty v-if="!loading && communities.length === 0" description="暂无可进入的社区，请联系管理员分配任职" />
      </n-spin>

      <div class="select-footer">
        <n-button quaternary @click="onLogout">退出登录</n-button>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { StorefrontOutline } from '@vicons/ionicons5'
import { getMyCommunities } from '@/api/user'
import { useAuthStore } from '@/stores/auth'

/** 可选社区项（服务端按账号任职/超管全量返回） */
interface CommunityOption {
  id: number
  name: string
  status: number
}

const route = useRoute()
const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()

const loading = ref(false)
const communities = ref<CommunityOption[]>([])

/**
 * 加载当前账号可进入的社区列表
 */
async function loadCommunities() {
  loading.value = true
  try {
    const res = await getMyCommunities()
    communities.value = res.data.data
  } finally {
    loading.value = false
  }
}

/**
 * 选择社区并进入工作台
 * 切换后整页刷新：keep-alive 缓存的标签页持有旧社区数据，reload 是最可靠的刷新方式
 * @param cm 目标社区
 */
function onPick(cm: CommunityOption) {
  if (!authStore.switchCommunity(cm.id)) {
    message.error('无权进入该社区')
    return
  }
  const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
  router
    .push(redirect && redirect !== '/select-community' ? redirect : '/dashboard')
    .then(() => window.location.reload())
}

/**
 * 退出登录
 */
function onLogout() {
  authStore.logout()
  router.push('/login')
}

onMounted(() => {
  loadCommunities()
})
</script>

<style lang="scss" scoped>
.select-community-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f5f5f5;
  padding: 24px;
}
.select-card {
  width: 560px;
  max-width: 100%;
}
.select-head {
  text-align: center;
  margin-bottom: 20px;
}
.select-title {
  margin: 0 0 6px;
  font-size: 20px;
  color: #333;
}
.select-subtitle {
  margin: 0;
  font-size: 13px;
  color: #999;
}
.community-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}
.community-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 14px 16px;
  border: 1px solid #eee;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s;
  &:hover {
    border-color: #07c160;
    background: #f2f9f4;
  }
  &-body {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  &-name {
    font-size: 14px;
    color: #333;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
.select-footer {
  margin-top: 24px;
  text-align: center;
}
</style>
