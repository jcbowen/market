<template>
  <n-card title="应用" class="apps-card">
    <template #header-extra>
      <n-input
        v-model:value="keyword"
        placeholder="搜索应用"
        clearable
        size="small"
        class="search"
      >
        <template #prefix>
          <n-icon><SearchOutline /></n-icon>
        </template>
      </n-input>
    </template>

    <!-- 常用应用 -->
    <template v-if="favoriteApps.length">
      <div class="section-title">常用应用</div>
      <div class="app-grid">
        <div v-for="app in favoriteApps" :key="app.key" class="app-card" @click="openApp(app)">
          <n-icon :size="34" class="app-icon">
            <component :is="app.icon" />
          </n-icon>
          <div class="app-info">
            <div class="app-name">{{ app.name }}</div>
            <div class="app-desc">{{ app.description }}</div>
          </div>
          <n-button text size="small" class="star-btn starred" @click.stop="toggleFavorite(app)">
            <n-icon :size="18"><Star /></n-icon>
          </n-button>
        </div>
      </div>
    </template>

    <!-- 全部应用 -->
    <div class="section-title">{{ favoriteApps.length ? '全部应用' : '' }}</div>
    <n-empty
      v-if="!otherApps.length && !favoriteApps.length"
      :description="keyword ? '没有匹配的应用' : '暂无可用应用'"
      class="empty"
    />
    <div v-else-if="otherApps.length" class="app-grid">
      <div v-for="app in otherApps" :key="app.key" class="app-card" @click="openApp(app)">
        <n-icon :size="34" class="app-icon">
          <component :is="app.icon" />
        </n-icon>
        <div class="app-info">
          <div class="app-name">{{ app.name }}</div>
          <div class="app-desc">{{ app.description }}</div>
        </div>
        <n-button text size="small" class="star-btn" @click.stop="toggleFavorite(app)">
          <n-icon :size="18"><StarOutline /></n-icon>
        </n-button>
      </div>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { SearchOutline, Star, StarOutline } from '@vicons/ionicons5'
import { APP_REGISTRY } from '@/constants/apps'
import type { AppMeta } from '@/constants/apps'
import { getAppPreferences, saveAppPreferences } from '@/api/order'
import { useAuthStore } from '@/stores/auth'
import { useMessage } from 'naive-ui'

const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()

/** 搜索关键字（按名称/描述模糊过滤） */
const keyword = ref('')
/** 常用应用 key 列表（有序，后端社区级存储） */
const favoriteKeys = ref<string[]>([])

/**
 * 当前账号可见的应用：权限码缺省视为登录即可
 */
const visibleApps = computed(() =>
  APP_REGISTRY.filter((app) => !app.permission || authStore.isSuperAdmin || authStore.hasPermission(app.permission)),
)

/**
 * 常用应用：按偏好 key 顺序展示，并应用搜索过滤
 */
const favoriteApps = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return favoriteKeys.value
    .map((key) => visibleApps.value.find((app) => app.key === key))
    .filter((app): app is AppMeta =>
      !!app && (!kw || app.name.toLowerCase().includes(kw) || app.description.toLowerCase().includes(kw)),
    )
})

/**
 * 非常用应用：注册顺序展示，并应用搜索过滤
 */
const otherApps = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return visibleApps.value.filter(
    (app) =>
      !favoriteKeys.value.includes(app.key) &&
      (!kw || app.name.toLowerCase().includes(kw) || app.description.toLowerCase().includes(kw)),
  )
})

/**
 * 进入应用
 * @param app 应用元信息
 */
function openApp(app: AppMeta) {
  router.push(app.path)
}

/**
 * 切换常用应用星标：乐观更新本地，失败静默回滚
 * @param app 应用元信息
 */
async function toggleFavorite(app: AppMeta) {
  const prev = [...favoriteKeys.value]
  favoriteKeys.value = prev.includes(app.key)
    ? prev.filter((key) => key !== app.key)
    : [...prev, app.key]
  try {
    await saveAppPreferences(favoriteKeys.value)
  } catch {
    favoriteKeys.value = prev
    message.error('保存常用应用失败')
  }
}

/**
 * 加载常用应用偏好：仅保留当前可见且已注册的 key，避免脏数据渲染
 */
async function loadPreferences() {
  try {
    const res = await getAppPreferences()
    const keys = res.data.data.app_keys ?? []
    favoriteKeys.value = keys.filter((key) => visibleApps.value.some((app) => app.key === key))
  } catch {
    // 拉取失败按无常用应用处理，不影响页面展示
  }
}

onMounted(loadPreferences)
</script>

<style lang="scss" scoped>
.apps-card {
  .search {
    width: 220px;
  }
}
.section-title {
  font-size: 14px;
  font-weight: 600;
  color: #666;
  margin: 4px 0 12px;
}
.app-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 14px;
  margin-bottom: 20px;
}
.app-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border: 1px solid #efeff5;
  border-radius: 10px;
  cursor: pointer;
  transition: box-shadow 0.2s, border-color 0.2s;
  &:hover {
    border-color: #07c160;
    box-shadow: 0 2px 12px rgba(7, 193, 96, 0.12);
    .star-btn {
      opacity: 1;
    }
  }
}
.app-icon {
  color: #07c160;
  flex-shrink: 0;
}
.app-info {
  flex: 1;
  min-width: 0;
}
.app-name {
  font-size: 15px;
  font-weight: 600;
}
.app-desc {
  font-size: 12px;
  color: #999;
  margin-top: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.star-btn {
  opacity: 0.35;
  flex-shrink: 0;
  &.starred {
    opacity: 1;
    color: #f0a020;
  }
}
.empty {
  padding: 40px 0;
}
</style>
