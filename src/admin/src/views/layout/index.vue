<template>
  <n-layout has-sider class="layout">
    <!-- 侧边栏 -->
    <n-layout-sider
      bordered
      collapse-mode="width"
      :collapsed-width="64"
      :width="220"
      :collapsed="collapsed"
      :native-scrollbar="false"
    >
      <div class="logo">
        <span v-if="!collapsed">社区商超</span>
        <span v-else>商超</span>
      </div>
      <n-menu
        :value="activeKey"
        :collapsed="collapsed"
        :collapsed-width="64"
        :collapsed-icon-size="22"
        :options="menuOptions"
        @update:value="onMenuChange"
      />
    </n-layout-sider>

    <!-- 主内容区 -->
    <n-layout>
      <n-layout-header bordered class="header">
        <div class="header-left">
          <n-button text @click="appStore.toggleCollapse">
            <n-icon size="20">
              <menu-outline />
            </n-icon>
          </n-button>
          <n-breadcrumb>
            <n-breadcrumb-item v-if="route.meta.group">{{ route.meta.group }}</n-breadcrumb-item>
            <n-breadcrumb-item>{{ route.meta.title }}</n-breadcrumb-item>
          </n-breadcrumb>
        </div>
        <div class="header-right">
          <!-- 当前社区展示/切换：超管可选全部社区与任意社区；多社区管理员可切换绑定社区；单社区仅展示 -->
          <div v-if="showCommunitySwitch" class="community-switch">
            <template v-if="canSwitchCommunity">
              <span class="community-label">社区</span>
              <n-select
                :value="authStore.currentCommunityId"
                :options="communityOptions"
                size="small"
                class="community-select"
                @update:value="onCommunityChange"
              />
            </template>
            <span v-else class="community-name" :title="currentCommunityName">{{ currentCommunityName }}</span>
          </div>
          <span class="admin-name">{{ authStore.admin?.nickname || authStore.admin?.username || '管理员' }}</span>
          <n-dropdown :options="userOptions" @select="onUserAction">
            <n-button text>
              <n-icon size="20">
                <person-circle-outline />
              </n-icon>
            </n-button>
          </n-dropdown>
        </div>
      </n-layout-header>
      <!-- 标签栏：访问过的页面形成标签，支持切换、关闭与批量关闭 -->
      <div class="tabs-bar">
        <n-tabs
          type="card"
          size="small"
          :value="tabsStore.activePath"
          @update:value="onTabSwitch"
          @close="onTabClose"
        >
          <n-tab v-for="tab in tabsStore.tabs" :key="tab.path" :name="tab.path" :closable="!tab.affix">
            {{ tab.title }}
          </n-tab>
        </n-tabs>
        <n-dropdown trigger="click" :options="tabMoreOptions" @select="onTabMoreAction">
          <n-button text class="tabs-more">
            <n-icon size="16">
              <ellipsis-horizontal />
            </n-icon>
          </n-button>
        </n-dropdown>
      </div>
      <n-layout-content class="content">
        <router-view v-slot="{ Component, route: viewRoute }">
          <!-- key 绑定 fullPath：路由参数变化时强制重建实例，避免详情/编辑页等参数页复用旧组件数据 -->
          <keep-alive :include="tabsStore.cachedNames">
            <component :is="Component" :key="viewRoute.fullPath" />
          </keep-alive>
        </router-view>
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import { MenuOutline, PersonCircleOutline, HomeOutline, CubeOutline, CartOutline, PeopleOutline, GiftOutline, BarChartOutline, FileTrayOutline, StorefrontOutline, EllipsisHorizontal } from '@vicons/ionicons5'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useTabsStore } from '@/stores/tabs'
import { resolveHomePath } from '@/router'
import { getCommunityList } from '@/api/user'

const router = useRouter()
const route = useRoute()
const message = useMessage()
const appStore = useAppStore()
const authStore = useAuthStore()
const tabsStore = useTabsStore()

const collapsed = computed(() => appStore.collapsed)
const activeKey = computed(() => route.path)

// --- 当前社区展示与切换 ---

/** 社区 ID → 名称映射（无 community:view 权限时拉取失败，降级显示"社区 #id"） */
const communityNameMap = ref<Map<number, string>>(new Map())
/** 超管的全部社区 ID 列表（来自社区管理接口，仅超管使用） */
const superCommunityIds = ref<number[]>([])

/** 是否展示当前社区区域：超管或绑定了社区的管理员 */
const showCommunitySwitch = computed(() => authStore.isSuperAdmin || authStore.communityIds.length > 0)
/** 是否可切换：超管（全部+任意）或绑定多个社区 */
const canSwitchCommunity = computed(() => authStore.isSuperAdmin || authStore.communityIds.length > 1)

/** 当前社区显示名 */
const currentCommunityName = computed(() => {
  const id = authStore.currentCommunityId
  if (authStore.isSuperAdmin && id === 0) return '全部社区'
  return communityNameMap.value.get(id) ?? `社区 #${id}`
})

/** 切换器选项列表 */
const communityOptions = computed(() => {
  const options: { label: string; value: number }[] = []
  if (authStore.isSuperAdmin) {
    options.push({ label: '全部社区', value: 0 })
    for (const id of superCommunityIds.value) {
      options.push({ label: communityNameMap.value.get(id) ?? `社区 #${id}`, value: id })
    }
    return options
  }
  for (const id of authStore.communityIds) {
    options.push({ label: communityNameMap.value.get(id) ?? `社区 #${id}`, value: id })
  }
  return options
})

/**
 * 加载社区名称映射
 * 仅在有 community:view 权限（或超管）时调用社区管理接口；
 * 无权限的普通管理员静默降级为"社区 #id"文案，避免每次进入都弹权限错误提示
 */
async function loadCommunityNames() {
  if (!authStore.hasPermission('community:view')) return
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    const map = new Map<number, string>()
    const ids: number[] = []
    for (const item of res.data.data.list) {
      map.set(item.id, item.name)
      ids.push(item.id)
    }
    communityNameMap.value = map
    if (authStore.isSuperAdmin) {
      superCommunityIds.value = ids
    }
  } catch {
    // 拉取失败（网络异常等）静默降级，不影响主流程
  }
}

/**
 * 切换当前社区
 * 校验通过后持久化选择并整页刷新：
 * keep-alive 缓存的标签页持有旧社区数据，reload 是最可靠的刷新方式
 * @param value 目标社区 ID（超管 0 = 全部社区）
 */
function onCommunityChange(value: number) {
  if (value === authStore.currentCommunityId) return
  if (!authStore.switchCommunity(value)) {
    message.error('无权切换到该社区')
    return
  }
  window.location.reload()
}

// Layout 挂载即按账号兜底首页初始化固定标签（登录切换账号时随 Layout 重建天然重置）
const homePath = resolveHomePath(authStore) ?? '/dashboard'
const homeResolved = router.resolve(homePath)
tabsStore.init(
  homePath,
  (homeResolved.meta.title as string) || '首页',
  typeof homeResolved.name === 'string' ? homeResolved.name : undefined,
)

/**
 * 路由变化同步标签页
 * 403 无权限提示页不进标签栏
 */
watch(
  () => route.fullPath,
  () => {
    if (route.path === '/403') return
    tabsStore.syncWithRoute(route.fullPath, route.meta.title as string, route.name, route.meta.hidden === true)
  },
  { immediate: true },
)

/**
 * 标签切换
 * @param key 目标标签路径
 */
function onTabSwitch(key: string | number) {
  router.push(String(key))
}

/**
 * 关闭标签：关闭的是激活标签时跳转到相邻标签
 * @param key 被关闭标签的路径
 */
function onTabClose(key: string | number) {
  const target = tabsStore.closeTab(String(key))
  if (target) router.push(target)
}

/** 标签栏更多操作选项 */
const tabMoreOptions = [
  { label: '关闭其他标签', key: 'closeOthers' },
  { label: '关闭全部标签', key: 'closeAll' },
]

/**
 * 标签栏更多操作处理
 * @param key 操作标识
 */
function onTabMoreAction(key: string) {
  if (key === 'closeOthers') {
    tabsStore.closeOthers()
    return
  }
  if (key === 'closeAll') {
    const target = tabsStore.closeAll()
    if (target) router.push(target)
  }
}

/**
 * 根据窗口宽度自动折叠侧边栏
 * 窄屏（< 768px）默认折叠，宽屏恢复展开
 */
function updateSiderByWidth() {
  const shouldCollapse = window.innerWidth < 768
  if (shouldCollapse !== appStore.collapsed) {
    appStore.setCollapse(shouldCollapse)
  }
}

onMounted(() => {
  updateSiderByWidth()
  window.addEventListener('resize', updateSiderByWidth)
  loadCommunityNames()
})

onUnmounted(() => {
  window.removeEventListener('resize', updateSiderByWidth)
})

/**
 * 渲染菜单图标
 * @param icon 图标组件
 */
function renderIcon(icon: unknown) {
  return () => h(icon as never)
}

/**
 * 菜单项定义
 * 按需求文档分组结构组织，含权限码与路由 key
 */
interface MenuItemConfig {
  label: string
  key: string
  permission: string
}

interface MenuGroupConfig {
  label: string
  key: string
  icon: unknown
  permission?: string
  children: MenuItemConfig[]
}

/**
 * 判断当前用户是否有指定权限
 * 超管默认拥有全部权限
 * @param code 权限码
 */
function checkPermission(code: string) {
  return authStore.isSuperAdmin || authStore.hasPermission(code)
}

/**
 * 构建分组子菜单配置
 * 根据权限过滤无权限的分组与菜单项
 */
const menuOptions = computed<MenuOption[]>(() => {
  const groups: MenuGroupConfig[] = [
    {
      label: '工作台',
      key: '/dashboard',
      icon: renderIcon(HomeOutline),
      permission: 'dashboard:view',
      children: [{ label: '数据概览', key: '/dashboard', permission: 'dashboard:view' }],
    },
    {
      label: '商品管理',
      key: '/products',
      icon: renderIcon(CubeOutline),
      permission: 'product:view',
      children: [
        { label: '商品列表', key: '/products', permission: 'product:view' },
        { label: '分类管理', key: '/products/categories', permission: 'product:view' },
      ],
    },
    {
      label: '库存管理',
      key: '/stock',
      icon: renderIcon(FileTrayOutline),
      permission: 'inventory:view',
      children: [
        { label: '库存列表', key: '/stock', permission: 'inventory:view' },
        { label: '库存流水', key: '/stock/records', permission: 'inventory:view' },
        { label: '盘点管理', key: '/stock/check', permission: 'inventory:view' },
        { label: '库存预警', key: '/stock/warnings', permission: 'inventory:view' },
        { label: '出库管理', key: '/stock/out', permission: 'inventory:create' },
        { label: '补货建议', key: '/stock/purchase', permission: 'inventory:view' },
      ],
    },
    {
      label: '订单管理',
      key: '/orders',
      icon: renderIcon(CartOutline),
      permission: 'order:view',
      children: [
        { label: '订单列表', key: '/orders', permission: 'order:view' },
        { label: '退款管理', key: '/refunds', permission: 'refund:view' },
      ],
    },
    {
      label: '用户管理',
      key: '/users',
      icon: renderIcon(PeopleOutline),
      permission: 'user:view',
      children: [{ label: '会员列表', key: '/users', permission: 'user:view' }],
    },
    {
      label: '营销管理',
      key: '/marketing',
      icon: renderIcon(GiftOutline),
      permission: 'marketing:view',
      children: [
        { label: '优惠券', key: '/marketing/coupons', permission: 'marketing:view' },
        { label: '秒杀活动', key: '/marketing/flash-sales', permission: 'marketing:view' },
        { label: '满减活动', key: '/marketing/full-reduction', permission: 'marketing:view' },
        { label: 'Banner管理', key: '/marketing/banners', permission: 'marketing:view' },
      ],
    },
    {
      label: '数据统计',
      key: '/statistics',
      icon: renderIcon(BarChartOutline),
      permission: 'statistics:view',
      children: [{ label: '数据统计', key: '/statistics', permission: 'statistics:view' }],
    },
    {
      label: '系统管理',
      key: '/system',
      icon: renderIcon(StorefrontOutline),
      permission: 'community:view',
      children: [
        { label: '社区管理', key: '/system/communities', permission: 'community:view' },
        { label: '支付配置', key: '/system/payment', permission: 'settings:view' },
        { label: '附件配置', key: '/system/attachment', permission: 'settings:view' },
        { label: '管理员', key: '/system/admins', permission: 'admin:view' },
        { label: '角色管理', key: '/system/roles', permission: 'role:view' },
      ],
    },
  ]

  return groups
    .filter((group) => !group.permission || checkPermission(group.permission))
    .map((group) => {
      const children = group.children.filter((child) => checkPermission(child.permission))
      if (children.length === 0) return null
      return {
        label: group.label,
        key: group.key,
        icon: renderIcon(group.icon),
        children: children.map((child) => ({ label: child.label, key: child.key })),
      }
    })
    .filter(Boolean) as MenuOption[]
})

/**
 * 菜单切换
 * @param key 菜单路由
 */
function onMenuChange(key: string) {
  router.push(key)
}

/** 用户操作下拉选项 */
const userOptions = [
  { label: '个人设置', key: 'profile' },
  { label: '退出登录', key: 'logout' },
]

/**
 * 用户操作处理
 * @param key 操作标识
 */
function onUserAction(key: string) {
  if (key === 'profile') {
    router.push('/settings/profile')
    return
  }
  if (key === 'logout') {
    authStore.logout()
    message.success('已退出登录')
    router.push('/login')
  }
}
</script>

<style lang="scss" scoped>
.layout {
  height: 100vh;
}
.logo {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  font-weight: bold;
  color: #07c160;
  border-bottom: 1px solid #eee;
}
.header {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  &-left, &-right {
    display: flex;
    align-items: center;
    gap: 12px;
  }
}
.admin-name {
  font-size: 14px;
  color: #666;
}
.community-switch {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.community-label {
  font-size: 14px;
  color: #666;
  flex-shrink: 0;
}
.community-select {
  width: 160px;
}
.community-name {
  font-size: 14px;
  color: #666;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tabs-bar {
  height: 40px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  background: #fff;
  border-bottom: 1px solid #efeff5;
  :deep(.n-tabs) {
    flex: 1;
  }
  .tabs-more {
    flex-shrink: 0;
  }
}
.content {
  padding: 16px;
  background: #f5f5f5;
  min-height: calc(100vh - 96px);
}
</style>
