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
          <!-- 当前社区：点击弹出所属社区列表，在弹窗中切换 -->
          <n-button v-if="showCommunitySwitch" text class="community-trigger" @click="showCommunityModal = true">
            <n-icon size="16" class="community-trigger-icon">
              <location-outline />
            </n-icon>
            <span class="community-trigger-name">{{ currentCommunityName }}</span>
            <n-icon size="14">
              <chevron-down-outline />
            </n-icon>
          </n-button>
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

    <!-- 切换社区弹窗：列出自己所属的社区（超管为全部社区），点击即切换 -->
    <n-modal v-model:show="showCommunityModal" preset="card" title="切换社区" style="width: 380px; max-width: 90vw">
      <div class="community-list">
        <div
          v-for="opt in communityOptions"
          :key="opt.value"
          class="community-item"
          :class="{ active: opt.value === authStore.currentCommunityId }"
          @click="onCommunityPick(opt.value)"
        >
          <span class="community-item-name">{{ opt.label }}</span>
          <n-icon v-if="opt.value === authStore.currentCommunityId" size="18" color="#07c160">
            <checkmark-circle />
          </n-icon>
        </div>
        <n-empty v-if="communityOptions.length === 0" description="暂无可选社区" style="padding: 24px 0" />
      </div>
    </n-modal>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import { MenuOutline, PersonCircleOutline, HomeOutline, CubeOutline, CartOutline, PeopleOutline, GiftOutline, BarChartOutline, FileTrayOutline, StorefrontOutline, EllipsisHorizontal, LocationOutline, ChevronDownOutline, CheckmarkCircle } from '@vicons/ionicons5'
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

/** 是否展示当前社区入口：超管必选社区（始终展示），绑定了社区的管理员展示并可点击切换 */
const showCommunitySwitch = computed(() => authStore.isSuperAdmin || authStore.communityIds.length > 0)

/** 切换社区弹窗可见性 */
const showCommunityModal = ref(false)

/** 当前社区显示名 */
const currentCommunityName = computed(() => {
  const id = authStore.currentCommunityId
  if (id <= 0) return '未选择社区'
  return communityNameMap.value.get(id) ?? `社区 #${id}`
})

/** 切换器选项列表 */
const communityOptions = computed(() => {
  const ids = authStore.isSuperAdmin ? superCommunityIds.value : authStore.communityIds
  return ids.map((id) => ({
    label: communityNameMap.value.get(id) ?? `社区 #${id}`,
    value: id,
  }))
})

/**
 * 加载社区名称映射
 * 仅在有 community:view 权限（或超管）时调用社区管理接口；
 * 无权限的普通管理员静默降级为"社区 #id"文案，避免每次进入都弹权限错误提示。
 * 超管当前未选定社区（首次使用或异常残留的 0）时，自动选定列表第一个社区并刷新，
 * 保证"当前在操作哪个社区"始终明确。
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
      if (authStore.currentCommunityId <= 0 && ids.length > 0 && authStore.switchCommunity(ids[0])) {
        window.location.reload()
      }
    }
  } catch {
    // 拉取失败（网络异常等）静默降级，不影响主流程
  }
}

/**
 * 弹窗中点击社区项切换当前社区
 * 点击当前社区仅关闭弹窗；切换成功后整页刷新：
 * keep-alive 缓存的标签页持有旧社区数据，reload 是最可靠的刷新方式
 * @param value 目标社区 ID
 */
function onCommunityPick(value: number) {
  if (value === authStore.currentCommunityId) {
    showCommunityModal.value = false
    return
  }
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
      // 分组不设权限门槛：按子项权限过滤，无任何可见子项时整组隐藏
      children: [
        { label: '社区管理', key: '/system/communities', permission: 'community:view' },
        { label: '行政区划', key: '/system/regions', permission: 'region:view' },
        { label: '支付配置', key: '/system/payment', permission: 'settings:view' },
        { label: '附件配置', key: '/system/attachment', permission: 'settings:view' },
        { label: '员工管理', key: '/system/admins', permission: 'admin:view' },
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
.community-trigger {
  max-width: 220px;
  gap: 4px;
}
.community-trigger-icon {
  flex-shrink: 0;
  color: #07c160;
}
.community-trigger-name {
  font-size: 14px;
  color: #333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.community-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 50vh;
  overflow-y: auto;
}
.community-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background-color 0.2s;
  &:hover {
    background: #f2f9f4;
  }
  &.active {
    background: #e8f7ee;
    .community-item-name {
      color: #07c160;
      font-weight: 600;
    }
  }
}
.community-item-name {
  font-size: 14px;
  color: #333;
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
