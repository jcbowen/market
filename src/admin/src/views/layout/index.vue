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
          <span class="page-title">{{ route.meta.title }}</span>
        </div>
        <div class="header-right">
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
      <n-layout-content class="content">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import { MenuOutline, PersonCircleOutline, HomeOutline, CubeOutline, CartOutline, AlbumsOutline, PeopleOutline, GiftOutline, BarChartOutline, SettingsOutline, FileTrayOutline, StorefrontOutline, OptionsOutline } from '@vicons/ionicons5'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const route = useRoute()
const message = useMessage()
const appStore = useAppStore()
const authStore = useAuthStore()

const collapsed = computed(() => appStore.collapsed)
const activeKey = computed(() => route.path)

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
        { label: '管理员', key: '/system/admins', permission: 'admin:view' },
        { label: '角色管理', key: '/system/roles', permission: 'role:view' },
      ],
    },
    {
      label: '设置',
      key: '/settings',
      icon: renderIcon(OptionsOutline),
      children: [
        { label: '个人设置', key: '/settings/profile', permission: 'dashboard:view' },
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
  { label: '退出登录', key: 'logout' },
]

/**
 * 用户操作处理
 * @param key 操作标识
 */
function onUserAction(key: string) {
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
.page-title {
  font-size: 16px;
  font-weight: 500;
}
.admin-name {
  font-size: 14px;
  color: #666;
}
.content {
  padding: 16px;
  background: #f5f5f5;
  min-height: calc(100vh - 56px);
}
</style>
