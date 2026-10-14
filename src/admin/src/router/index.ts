import { createRouter, createWebHashHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

/**
 * 路由配置
 * 按需求文档拆分为子页面路由，支持菜单分组与权限控制
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Layout',
    component: () => import('@/views/layout/index.vue'),
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'Dashboard',
        component: () => import('@/views/dashboard/index.vue'),
        meta: { title: '工作台', permission: 'dashboard:view', group: '工作台' },
      },
      // 商品管理
      {
        path: 'products',
        name: 'ProductList',
        component: () => import('@/views/product/list.vue'),
        meta: { title: '商品列表', permission: 'product:view', group: '商品管理' },
      },
      {
        path: 'products/categories',
        name: 'ProductCategories',
        component: () => import('@/views/product/categories.vue'),
        meta: { title: '分类管理', permission: 'product:view', group: '商品管理' },
      },
      {
        path: 'products/create',
        name: 'ProductCreate',
        component: () => import('@/views/product/form.vue'),
        meta: { title: '新增商品', permission: 'product:create', group: '商品管理', hidden: true },
      },
      {
        path: 'products/detail/:id',
        name: 'ProductEdit',
        component: () => import('@/views/product/form.vue'),
        meta: { title: '编辑商品', permission: 'product:update', group: '商品管理', hidden: true },
      },
      // 库存管理
      {
        path: 'stock',
        name: 'StockList',
        component: () => import('@/views/inventory/list.vue'),
        meta: { title: '库存列表', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/records',
        name: 'StockRecords',
        component: () => import('@/views/inventory/records.vue'),
        meta: { title: '库存流水', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/check',
        name: 'StockCheck',
        component: () => import('@/views/inventory/check.vue'),
        meta: { title: '盘点管理', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/check/create',
        name: 'StockCheckCreate',
        component: () => import('@/views/inventory/check-form.vue'),
        meta: { title: '新建盘点', permission: 'inventory:create', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/check/:id',
        name: 'StockCheckDetail',
        component: () => import('@/views/inventory/check-detail.vue'),
        meta: { title: '盘点详情', permission: 'inventory:view', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/warnings',
        name: 'StockWarnings',
        component: () => import('@/views/inventory/warnings.vue'),
        meta: { title: '库存预警', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/out',
        name: 'StockOut',
        component: () => import('@/views/inventory/out.vue'),
        meta: { title: '出库管理', permission: 'inventory:create', group: '库存管理' },
      },
      {
        path: 'stock/purchase',
        name: 'StockPurchase',
        component: () => import('@/views/inventory/purchase.vue'),
        meta: { title: '补货建议', permission: 'inventory:view', group: '库存管理' },
      },
      // 订单管理
      {
        path: 'orders',
        name: 'OrderList',
        component: () => import('@/views/order/list.vue'),
        meta: { title: '订单列表', permission: 'order:view', group: '订单管理' },
      },
      {
        path: 'refunds',
        name: 'RefundList',
        component: () => import('@/views/order/refunds.vue'),
        meta: { title: '售后管理', permission: 'refund:view', group: '订单管理' },
      },
      // 用户管理
      {
        path: 'users',
        name: 'UserList',
        component: () => import('@/views/user/index.vue'),
        meta: { title: '会员列表', permission: 'user:view', group: '用户管理' },
      },
      // 营销管理
      {
        path: 'marketing/coupons',
        name: 'CouponList',
        component: () => import('@/views/marketing/coupons.vue'),
        meta: { title: '优惠券管理', permission: 'marketing:view', group: '营销管理' },
      },
      {
        path: 'marketing/coupons/create',
        name: 'CouponCreate',
        component: () => import('@/views/marketing/coupon-form.vue'),
        meta: { title: '创建优惠券', permission: 'marketing:create', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/flash-sales',
        name: 'FlashSaleList',
        component: () => import('@/views/marketing/flash-sales.vue'),
        meta: { title: '秒杀活动', permission: 'marketing:view', group: '营销管理' },
      },
      {
        path: 'marketing/flash-sales/create',
        name: 'FlashSaleCreate',
        component: () => import('@/views/marketing/flash-sale-form.vue'),
        meta: { title: '创建秒杀', permission: 'marketing:create', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/full-reduction',
        name: 'FullReductionList',
        component: () => import('@/views/marketing/full-reduction.vue'),
        meta: { title: '满减活动', permission: 'marketing:view', group: '营销管理' },
      },
      {
        path: 'marketing/full-reduction/create',
        name: 'FullReductionCreate',
        component: () => import('@/views/marketing/full-reduction-form.vue'),
        meta: { title: '创建满减', permission: 'marketing:create', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/banners',
        name: 'BannerList',
        component: () => import('@/views/marketing/components/BannerManager.vue'),
        meta: { title: 'Banner管理', permission: 'marketing:view', group: '营销管理' },
      },
      // 数据统计
      {
        path: 'statistics',
        name: 'Statistics',
        component: () => import('@/views/statistics/index.vue'),
        meta: { title: '数据统计', permission: 'statistics:view', group: '数据统计' },
      },
      // 系统管理
      {
        path: 'system/communities',
        name: 'CommunityList',
        component: () => import('@/views/system/communities.vue'),
        meta: { title: '社区管理', permission: 'community:view', group: '系统管理' },
      },
      {
        path: 'system/regions',
        name: 'RegionList',
        component: () => import('@/views/system/regions.vue'),
        meta: { title: '行政区划', permission: 'region:view', group: '系统管理' },
      },
      {
        path: 'system/communities/edit/:id',
        name: 'CommunityEdit',
        component: () => import('@/views/system/community-form.vue'),
        meta: { title: '编辑社区', permission: 'community:update', group: '系统管理', hidden: true },
      },
      {
        path: 'system/communities/create',
        name: 'CommunityCreate',
        component: () => import('@/views/system/community-form.vue'),
        meta: { title: '新增社区', permission: 'community:create', group: '系统管理', hidden: true },
      },
      {
        path: 'system/admins',
        name: 'AdminList',
        component: () => import('@/views/system/admins.vue'),
        meta: { title: '员工管理', permission: 'admin:view', group: '员工与角色' },
      },
      {
        path: 'system/roles',
        name: 'RoleList',
        component: () => import('@/views/system/roles.vue'),
        meta: { title: '角色管理', permission: 'role:view', group: '员工与角色' },
      },
      {
        path: 'system/roles-global',
        name: 'RoleListGlobal',
        component: () => import('@/views/system/roles.vue'),
        meta: { title: '全局角色', permission: 'role:view', group: '系统管理' },
      },
      {
        path: 'system/community-permissions',
        name: 'CommunityPermission',
        component: () => import('@/views/system/community-permissions.vue'),
        meta: { title: '社区权限', permission: 'community:view', group: '系统管理' },
      },
      {
        path: 'system/payment',
        name: 'PaymentSetting',
        component: () => import('@/views/settings/payment.vue'),
        meta: { title: '支付配置', permission: 'settings:view', group: '系统管理' },
      },
      {
        path: 'system/refund',
        name: 'RefundSetting',
        component: () => import('@/views/settings/refund.vue'),
        meta: { title: '售后配置', permission: 'settings:view', group: '系统管理' },
      },
      {
        path: 'system/attachment',
        name: 'AttachmentSetting',
        component: () => import('@/views/settings/attachment.vue'),
        meta: { title: '附件配置', permission: 'settings:view', group: '系统管理' },
      },
      // 本地附件迁移工具：入口在附件配置页，不在左侧菜单展示
      {
        path: 'system/attachment-migrate',
        name: 'AttachmentMigrate',
        component: () => import('@/views/settings/attachment-migrate.vue'),
        meta: { title: '附件迁移', permission: 'settings:view', group: '系统管理', hidden: true },
      },
      // 个人设置：入口在右上角头像下拉，不在左侧菜单展示
      {
        path: 'settings/profile',
        name: 'Profile',
        component: () => import('@/views/settings/profile.vue'),
        meta: { title: '个人设置', permission: 'dashboard:view', hidden: true },
      },
      // 无权限提示页：守卫重定向目标，不设 permission 字段避免被循环拦截
      {
        path: '403',
        name: 'Forbidden',
        component: () => import('@/views/error/403.vue'),
        meta: { title: '无权限', hidden: true },
      },
    ],
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/login/index.vue'),
    meta: { title: '登录', public: true },
  },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

/**
 * 解析当前管理员的兜底首页路径
 * 优先工作台，无 dashboard:view 权限时按路径长度与字母序取路由表中第一个有权限的可见页面
 * @param authStore 认证 store，用于权限判定
 * @returns 可访问的首页路径；当前管理员无任何可用页面时返回 null
 */
export function resolveHomePath(authStore: ReturnType<typeof useAuthStore>): string | null {
  if (authStore.hasPermission('dashboard:view')) return '/dashboard'
  const route = router
    .getRoutes()
    .filter((r) => r.meta.permission && !r.meta.hidden && authStore.hasPermission(r.meta.permission as string))
    .sort((a, b) => a.path.length - b.path.length || a.path.localeCompare(b.path))
  return route[0]?.path ?? null
}

/**
 * 路由守卫（认证状态机决策表）
 * 状态：unauthenticated（无登录态）→ restoring（刷新后恢复中）→ ready（token+权限就绪）
 * 转移规则：
 * - unauthenticated：公开页放行，受保护页去登录
 * - 已认证访问登录页：回兜底首页
 * - restoring：ensureReady() 幂等恢复；失败（token 失效）已自动登出，去登录
 * - ready：按权限判定；无权限时保留登录态跳转 /403 提示页，
 *   仅当当前管理员无任何可用页面（权限为空）时才登出兜底
 */
router.beforeEach(async (to) => {
  const authStore = useAuthStore()

  // unauthenticated：公开页放行，其余去登录（携带原始地址供登录成功后回跳）
  if (!authStore.isAuthenticated) {
    return to.meta.public ? true : { path: '/login', query: { redirect: to.fullPath } }
  }

  // 已认证访问登录页：回兜底首页
  // 兜底值不可用 '/login'：本分支即「已认证访问 /login」，跳 /login 会无限重定向。
  // resolveHomePath 返回 null（无任何可用页面）时落到 /dashboard，
  // 由下游权限判定接管送至 /403 提示页，全程不会产生 { path: null }
  if (to.path === '/login') {
    return { path: resolveHomePath(authStore) ?? '/dashboard' }
  }

  // restoring：幂等恢复登录态，失败时 ensureReady 已登出
  if (!(await authStore.ensureReady())) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  // ready：权限判定；无权限时保留登录态跳转无权限提示页
  const permission = to.meta.permission as string | undefined
  if (permission && !authStore.hasPermission(permission)) {
    const home = resolveHomePath(authStore)
    if (!home) {
      // 无任何可用页面，登录态已无意义，登出兜底
      authStore.logout()
      return { path: '/login' }
    }
    return { path: '/403', query: { from: to.fullPath } }
  }

  return true
})

export default router
