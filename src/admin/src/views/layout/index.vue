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
      <!-- 折叠态（64px）只显示 logo 图放不下文字；未配置 logo 图时降级显示短名首字 -->
      <div class="logo">
        <img v-if="branding.logoURL" :src="branding.logoURL" alt="站点 Logo" :class="['logo-img', { 'is-collapsed': collapsed }]" />
        <span v-if="!collapsed">{{ isSystemSpace ? '管理后台' : branding.siteName }}</span>
        <span v-else-if="!branding.logoURL" class="logo-fallback">{{ (isSystemSpace ? '系统' : branding.siteNameShort).charAt(0) }}</span>
      </div>
      <!-- 系统空间：提供返回业务后台的出口，保证两套菜单完全隔离互不混排 -->
      <div v-if="isSystemSpace" class="sider-back">
        <n-button quaternary block size="small" @click="backToBusiness">
          <template #icon>
            <n-icon><arrow-back-outline /></n-icon>
          </template>
          <span v-if="!collapsed">返回业务后台</span>
        </n-button>
      </div>
      <n-menu
        :value="activeKey"
        :expanded-keys="expandedKeys"
        :collapsed="collapsed"
        :collapsed-width="64"
        :collapsed-icon-size="22"
        :options="currentMenuOptions"
        @update:value="onMenuChange"
        @update:expanded-keys="onExpandedKeysChange"
      />
      <!-- 页脚：版权与备案号富文本取自站点设置（服务端已消毒），未配置时整块不占位 -->
      <div v-if="!collapsed && branding.footerHTML" class="sider-foot" v-html="branding.footerHTML"></div>
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
          <!-- 下载中心：后台异步导出的任务列表入口，有未下载文件时图标带角标 -->
          <download-center />
          <!-- 当前社区：点击进入社区选择页（多社区账号切换入口） -->
          <n-button v-if="showCommunitySwitch" text class="community-trigger" @click="goSelectCommunity">
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
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import { MenuOutline, PersonCircleOutline, HomeOutline, GridOutline, CubeOutline, CartOutline, PeopleOutline, GiftOutline, BarChartOutline, FileTrayOutline, StorefrontOutline, SettingsOutline, InformationCircleOutline, EllipsisHorizontal, LocationOutline, ChevronDownOutline, ArrowBackOutline } from '@vicons/ionicons5'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useTabsStore } from '@/stores/tabs'
import { useBrandingStore } from '@/stores/branding'
import DownloadCenter from '@/components/DownloadCenter.vue'
import { resolveHomePath } from '@/router'
import { getCommunityList } from '@/api/user'

const router = useRouter()
const route = useRoute()
const message = useMessage()
const appStore = useAppStore()
const authStore = useAuthStore()
const tabsStore = useTabsStore()
const branding = useBrandingStore()

const collapsed = computed(() => appStore.collapsed)
const activeKey = computed(() => route.path)

// --- 当前社区展示与切换 ---

/** 社区 ID → 名称映射（无 community:view 权限时拉取失败，降级显示"社区 #id"） */
const communityNameMap = ref<Map<number, string>>(new Map())

/** 是否展示当前社区入口：超管必选社区（始终展示），绑定了社区的管理员展示并可点击切换 */
const showCommunitySwitch = computed(() => authStore.isSuperAdmin || authStore.communityIds.length > 0)

/** 当前社区显示名 */
const currentCommunityName = computed(() => {
  const id = authStore.currentCommunityId
  if (id <= 0) return '未选择社区'
  return communityNameMap.value.get(id) ?? `社区 #${id}`
})

/**
 * 进入社区选择页（顶栏"切换社区"入口）
 * 选择页选完社区后整页刷新，保证 keep-alive 缓存的页面不残留旧社区数据
 */
function goSelectCommunity() {
  // force=1：已选定社区时守卫默认会弹回首页，主动切换须携带标记放行
  router.push({ path: '/select-community', query: { force: '1' } })
}

/**
 * 加载社区名称映射
 * 仅在有 community:view 权限（或超管）时调用社区管理接口；
 * 无权限的普通管理员静默降级为"社区 #id"文案，避免每次进入都弹权限错误提示。
 * 社区未选定（0）时由路由守卫强制进入社区选择页，此处不做自动补选
 */
async function loadCommunityNames() {
  if (!authStore.hasPermission('community:view')) return
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    const map = new Map<number, string>()
    for (const item of res.data.data.list) {
      map.set(item.id, item.name)
    }
    communityNameMap.value = map
  } catch {
    // 拉取失败（网络异常等）静默降级，不影响主流程
  }
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
  branding.load()
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
  /** 权限码；缺省表示登录即可见（如应用中心） */
  permission?: string
  /** 仅超管可见（系统级入口，社区管理员即便持有对应权限码也不展示） */
  superOnly?: boolean
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
        { label: '库存工作台', key: '/stock', permission: 'inventory:view' },
        { label: '采购管理', key: '/stock/purchase', permission: 'inventory:view' },
        { label: '入库管理', key: '/stock/in', permission: 'inventory:view' },
        { label: '出库管理', key: '/stock/out', permission: 'inventory:create' },
        { label: '盘点管理', key: '/stock/check', permission: 'inventory:view' },
        { label: '报表中心', key: '/stock/reports', permission: 'inventory:view' },
      ],
    },
    {
      label: '订单管理',
      key: '/orders',
      icon: renderIcon(CartOutline),
      permission: 'order:view',
      children: [
        { label: '订单列表', key: '/orders', permission: 'order:view' },
        { label: '售后管理', key: '/refunds', permission: 'refund:view' },
        { label: '支付异常', key: '/payment-anomalies', permission: 'order:view' },
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
        { label: '免配送费活动', key: '/marketing/free-shipping', permission: 'marketing:view' },
        { label: 'Banner管理', key: '/marketing/banners', permission: 'marketing:view' },
      ],
    },
    {
      // 应用中心：宫格化应用入口（收银台等独立应用从这里进入，登录即可见）
      // 纯启动器性质，紧跟营销管理之后
      label: '应用管理',
      key: '/apps',
      icon: renderIcon(GridOutline),
      children: [{ label: '应用中心', key: '/apps' }],
    },
    {
      label: '数据统计',
      key: '/statistics',
      icon: renderIcon(BarChartOutline),
      permission: 'statistics:view',
      children: [{ label: '数据统计', key: '/statistics', permission: 'statistics:view' }],
    },
    {
      label: '门店管理',
      key: '/staff',
      icon: renderIcon(StorefrontOutline),
      // 社区门店级：门店设置、员工管理与本社区私有角色管理
      children: [
        { label: '门店设置', key: '/store/settings', permission: 'community:config' },
        { label: '员工管理', key: '/system/admins', permission: 'admin:view' },
        { label: '角色管理', key: '/system/roles', permission: 'role:view' },
      ],
    },
  ]

  return groups
    .filter((group) => !group.permission || checkPermission(group.permission))
    .map((group) => {
      const children = group.children.filter(
        (child) =>
          (!child.permission || checkPermission(child.permission)) &&
          (!child.superOnly || authStore.isSuperAdmin),
      )
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
 * 系统空间菜单（仅系统管理页面展示，替代业务菜单）
 * 按职能拆分三个一级菜单：数据统计 / 系统管理 / 平台配置
 * superOnly 项仅超管可见（社区权限/账号/社区角色/全平台统计）
 */
const systemMenuOptions = computed<MenuOption[]>(() => {
  const groups: MenuGroupConfig[] = [
    {
      label: '数据统计',
      key: 'sys-group-stats',
      icon: renderIcon(BarChartOutline),
      children: [{ label: '全平台统计', key: '/system/statistics', permission: 'statistics:view', superOnly: true }],
    },
    {
      label: '系统管理',
      key: 'sys-group-system',
      icon: renderIcon(StorefrontOutline),
      children: [
        { label: '社区管理', key: '/system/communities', permission: 'community:view' },
        { label: '社区权限', key: '/system/community-permissions', permission: 'community:view', superOnly: true },
        { label: '账号管理', key: '/system/accounts', permission: 'admin:view', superOnly: true },
        { label: '社区角色', key: '/system/community-roles', permission: 'role:view', superOnly: true },
        { label: '行政区划', key: '/system/regions', permission: 'region:view' },
      ],
    },
    {
      label: '平台配置',
      key: 'sys-group-config',
      icon: renderIcon(SettingsOutline),
      children: [
        { label: '站点设置', key: '/system/site', permission: 'settings:view' },
        { label: '支付配置', key: '/system/payment', permission: 'settings:view' },
        { label: '渠道管理', key: '/system/channels', permission: 'settings:view' },
        { label: '售后配置', key: '/system/refund', permission: 'settings:view' },
        { label: '附件配置', key: '/system/attachment', permission: 'settings:view' },
        { label: '全局 Banner', key: '/system/banners', permission: 'marketing:view' },
      ],
    },
  ]

  return [
    ...(groups
      .map((group) => {
        const children = group.children.filter(
          (child) =>
            (!child.permission || checkPermission(child.permission)) &&
            (!child.superOnly || authStore.isSuperAdmin),
        )
        if (children.length === 0) return null
        return {
          label: group.label,
          key: group.key,
          icon: renderIcon(group.icon),
          children: children.map((child) => ({ label: child.label, key: child.key })),
        }
      })
      .filter(Boolean) as MenuOption[]),
    // 关于系统：单页拍平为一级菜单，置于系统空间末尾（不归属任何分组，后续若扩展可独立成组）
    { label: '关于系统', key: '/system/about', icon: renderIcon(InformationCircleOutline) },
  ]
})

/** 是否处于系统管理空间（路由 meta.space = 'system'），此时左侧菜单整体替换为系统管理菜单 */
const isSystemSpace = computed(() => route.meta.space === 'system')

/** 当前侧边栏菜单：系统空间用系统菜单，业务空间用业务菜单 */
const currentMenuOptions = computed(() => (isSystemSpace.value ? systemMenuOptions.value : menuOptions.value))

// --- 菜单展开态：受控 + 持久化 ---
// n-menu 的展开态默认由组件内部控制，刷新即全折叠；此处改为受控并持久化，
// 使刷新后恢复用户上次的展开选择，且当前页所属分组始终自动展开

/** 菜单展开态的 localStorage 持久化键 */
const MENU_EXPANDED_KEY = 'admin_menu_expanded_keys'

/**
 * 首次进入（无持久化记录）时的默认展开组
 * 业务空间取日常最高频三组（订单/库存/商品），系统空间取超管落地页所在组（系统管理）
 */
const DEFAULT_EXPANDED_KEYS = ['/orders', '/stock', '/products', 'sys-group-system']

/**
 * 读取持久化的菜单展开态
 * 无记录（首次进入）或记录损坏时回落默认展开组
 * @returns 展开的菜单组 key 列表
 */
function readExpandedKeys(): string[] {
  try {
    const raw = localStorage.getItem(MENU_EXPANDED_KEY)
    if (raw === null) return [...DEFAULT_EXPANDED_KEYS]
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return [...DEFAULT_EXPANDED_KEYS]
    return parsed.filter((k): k is string => typeof k === 'string')
  } catch {
    // 隐私模式/存储禁用等场景静默回落默认值
    return [...DEFAULT_EXPANDED_KEYS]
  }
}

/**
 * 持久化菜单展开态
 * 写入失败（隐私模式/容量不足等）静默降级为仅本次会话生效
 * @param keys 展开的菜单组 key 列表
 */
function persistExpandedKeys(keys: string[]) {
  try {
    localStorage.setItem(MENU_EXPANDED_KEY, JSON.stringify(keys))
  } catch {
    // 静默降级：不持久化，刷新后回到默认展开组
  }
}

/** 菜单展开态（受控）：刷新后恢复用户上次选择，无记录时用默认展开组 */
const expandedKeys = ref<string[]>(readExpandedKeys())

/**
 * 路由路径 → 所属菜单组 key
 * 遍历当前空间已按权限过滤的菜单，支持 hidden 详情页按前缀归属
 * （如 /orders/123 → 订单管理、/system/admins → 门店管理）；
 * 不在菜单内的路由（如 /settings/profile）返回 null
 * @param path 当前路由 path
 * @returns 所属菜单组 key；无匹配返回 null
 */
function resolveGroupKey(path: string): string | null {
  for (const group of currentMenuOptions.value) {
    const hit = group.children?.some(
      (child) => path === child.key || path.startsWith(`${child.key}/`),
    )
    if (hit) return String(group.key)
  }
  return null
}

// 路由变化：当前页所属分组自动展开（强制展开，即使用户此前手动收起过，保证刷新后看得见当前页位置）
watch(
  () => route.path,
  (path) => {
    const key = resolveGroupKey(path)
    if (key && !expandedKeys.value.includes(key)) {
      expandedKeys.value = [...expandedKeys.value, key]
      persistExpandedKeys(expandedKeys.value)
    }
  },
  { immediate: true },
)

/**
 * 菜单展开态变更（用户手动展开/收起）：同步受控状态并持久化
 * @param keys 新的展开组 key 列表
 */
function onExpandedKeysChange(keys: Array<string | number>) {
  const next = keys.map(String)
  expandedKeys.value = next
  persistExpandedKeys(next)
}

/** 是否可进入系统管理空间：仅超管（系统级权限点不对社区分配，社区管理员只见业务菜单） */
const canEnterSystem = computed(() => authStore.isSuperAdmin)

/** 系统空间落地页：按权限取第一个可见的系统页面（超管落社区管理） */
function systemHomePath(): string {
  const candidates: Array<{ path: string; permission: string }> = [
    { path: '/system/communities', permission: 'community:view' },
    { path: '/system/regions', permission: 'region:view' },
    { path: '/system/payment', permission: 'settings:view' },
    { path: '/system/refund', permission: 'settings:view' },
    { path: '/system/attachment', permission: 'settings:view' },
  ]
  const hit = candidates.find((c) => authStore.hasPermission(c.permission))
  return hit?.path ?? '/system/communities'
}

/**
 * 返回业务后台
 */
function backToBusiness() {
  router.push(resolveHomePath(authStore) ?? '/dashboard')
}

/**
 * 菜单切换
 * @param key 菜单路由
 */
function onMenuChange(key: string) {
  router.push(key)
}

/** 用户操作下拉选项：管理后台入口在"退出登录"之前，仅有系统级权限时展示 */
const userOptions = computed(() => {
  const options: Array<{ label: string; key: string }> = [{ label: '个人设置', key: 'profile' }]
  if (canEnterSystem.value) {
    options.push({ label: '管理后台', key: 'system' })
  }
  options.push({ label: '退出登录', key: 'logout' })
  return options
})

/**
 * 用户操作处理
 * @param key 操作标识
 */
function onUserAction(key: string) {
  if (key === 'profile') {
    router.push('/settings/profile')
    return
  }
  if (key === 'system') {
    router.push(systemHomePath())
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
.sider-back {
  padding: 8px 12px 0;
}
.logo-img {
  height: 24px;
  margin-right: 8px;
}
/* 折叠态无文字跟随，去掉右边距让 logo 精确居中 */
.logo-img.is-collapsed {
  margin-right: 0;
}
/* 折叠态未配置 logo 图时的降级块：短名首字居中，避免空白 */
.logo-fallback {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: rgba(7, 193, 96, 0.12);
  color: #07c160;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  font-weight: bold;
}
.sider-foot {
  padding: 12px 16px;
  font-size: 12px;
  line-height: 1.6;
  color: #999;
  text-align: center;
  word-break: break-all;
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
