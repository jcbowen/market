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
      // 库存管理（进销存重构版：工作台/采购/入库/出库/盘点/报表）
      {
        path: 'stock',
        name: 'StockWorkbench',
        component: () => import('@/views/inventory/workbench.vue'),
        meta: { title: '库存工作台', permission: 'inventory:view', group: '库存管理' },
      },
      // 采购管理（采购单 + 供应商）
      {
        path: 'stock/purchase',
        name: 'StockPurchase',
        component: () => import('@/views/inventory/purchase.vue'),
        meta: { title: '采购管理', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/purchase/create',
        name: 'PurchaseCreate',
        component: () => import('@/views/inventory/purchase-form.vue'),
        meta: { title: '新建采购单', permission: 'inventory:create', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/purchase/edit/:id',
        name: 'PurchaseEdit',
        component: () => import('@/views/inventory/purchase-form.vue'),
        meta: { title: '编辑采购单', permission: 'inventory:update', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/purchase/:id',
        name: 'PurchaseDetail',
        component: () => import('@/views/inventory/purchase-detail.vue'),
        meta: { title: '采购单详情', permission: 'inventory:view', group: '库存管理', hidden: true },
      },
      // 入库管理
      {
        path: 'stock/in',
        name: 'StockIn',
        component: () => import('@/views/inventory/in.vue'),
        meta: { title: '入库管理', permission: 'inventory:view', group: '库存管理' },
      },
      {
        path: 'stock/in/create',
        name: 'StockInCreate',
        component: () => import('@/views/inventory/in-form.vue'),
        meta: { title: '新建入库单', permission: 'inventory:create', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/in/:id',
        name: 'StockInDetail',
        component: () => import('@/views/inventory/in-detail.vue'),
        meta: { title: '入库单详情', permission: 'inventory:view', group: '库存管理', hidden: true },
      },
      // 出库管理（报损/领用/调整；销售出库由订单系统自动完成）
      {
        path: 'stock/out',
        name: 'StockOut',
        component: () => import('@/views/inventory/out.vue'),
        meta: { title: '出库管理', permission: 'inventory:create', group: '库存管理' },
      },
      {
        path: 'stock/out/create',
        name: 'StockOutCreate',
        component: () => import('@/views/inventory/out-form.vue'),
        meta: { title: '新建出库单', permission: 'inventory:create', group: '库存管理', hidden: true },
      },
      {
        path: 'stock/out/:id',
        name: 'StockOutDetail',
        component: () => import('@/views/inventory/out-detail.vue'),
        meta: { title: '出库单详情', permission: 'inventory:view', group: '库存管理', hidden: true },
      },
      // 盘点管理
      {
        path: 'stock/check',
        name: 'StockCheck',
        component: () => import('@/views/inventory/check.vue'),
        meta: { title: '盘点管理', permission: 'inventory:view', group: '库存管理' },
      },
      // 新建盘点已改为列表页内弹窗（原 /stock/check/create 独立页已删除）
      {
        path: 'stock/check/:id',
        name: 'StockCheckDetail',
        component: () => import('@/views/inventory/check-detail.vue'),
        meta: { title: '盘点详情', permission: 'inventory:view', group: '库存管理', hidden: true },
      },
      // 报表中心
      {
        path: 'stock/reports',
        name: 'StockReports',
        component: () => import('@/views/inventory/reports.vue'),
        meta: { title: '报表中心', permission: 'inventory:view', group: '库存管理' },
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
      {
        path: 'payment-anomalies',
        name: 'PaymentAnomalies',
        component: () => import('@/views/order/payment-anomalies.vue'),
        meta: { title: '支付异常', permission: 'order:view', group: '订单管理' },
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
        path: 'marketing/coupons/:id/edit',
        name: 'CouponEdit',
        component: () => import('@/views/marketing/coupon-form.vue'),
        meta: { title: '编辑优惠券', permission: 'marketing:update', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/coupon-records',
        name: 'CouponRecords',
        component: () => import('@/views/marketing/coupon-records.vue'),
        meta: { title: '领取/发放记录', permission: 'marketing:view', group: '营销管理' },
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
        path: 'marketing/free-shipping',
        name: 'FreeShippingList',
        component: () => import('@/views/marketing/free-shipping.vue'),
        meta: { title: '免配送费活动', permission: 'marketing:view', group: '营销管理' },
      },
      {
        path: 'marketing/free-shipping/create',
        name: 'FreeShippingCreate',
        component: () => import('@/views/marketing/free-shipping-form.vue'),
        meta: { title: '创建免配送费活动', permission: 'marketing:create', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/free-shipping/:id/edit',
        name: 'FreeShippingEdit',
        component: () => import('@/views/marketing/free-shipping-form.vue'),
        meta: { title: '编辑免配送费活动', permission: 'marketing:update', group: '营销管理', hidden: true },
      },
      {
        path: 'marketing/banners',
        name: 'BannerList',
        component: () => import('@/views/marketing/components/BannerManager.vue'),
        meta: { title: 'Banner管理', permission: 'marketing:view', group: '营销管理' },
      },
      // 应用中心：宫格化应用入口（当前仅收银台），登录即可见；菜单紧跟营销管理之后
      {
        path: 'apps',
        name: 'AppCenter',
        component: () => import('@/views/apps/index.vue'),
        meta: { title: '应用管理', group: '应用管理' },
      },
      // 数据导入（应用中心独立应用页）：初期仅超管
      {
        path: 'apps/data-import',
        name: 'AppDataImport',
        component: () => import('@/views/apps/data-import.vue'),
        meta: { title: '数据导入', permission: 'product:create', superOnly: true, group: '应用管理' },
      },
      // 数据统计
      {
        path: 'statistics',
        name: 'Statistics',
        component: () => import('@/views/statistics/index.vue'),
        meta: { title: '数据统计', permission: 'statistics:view', group: '数据统计' },
      },
      // 系统管理（独立系统空间：入口在右上角用户下拉，meta.space 标识后左侧菜单整体替换为系统管理菜单）
      {
        path: 'system/communities',
        name: 'CommunityList',
        component: () => import('@/views/system/communities.vue'),
        meta: { title: '社区管理', permission: 'community:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/regions',
        name: 'RegionList',
        component: () => import('@/views/system/regions.vue'),
        meta: { title: '行政区划', permission: 'region:view', group: '系统管理', space: 'system' },
      },
      // 关于页：系统信息展示（版本号 / MySQL·Redis 版本 / 部署方式 / 技术支持），
      // 不设 permission（登录即可），系统空间整体仅超管可达（见路由守卫）；
      // 左侧菜单拍平为系统空间末尾的一级菜单（不归属任何分组），故不设 meta.group
      {
        path: 'system/about',
        name: 'SystemAbout',
        component: () => import('@/views/system/about.vue'),
        meta: { title: '关于系统', space: 'system' },
      },
      {
        path: 'system/banners',
        name: 'SystemBannerList',
        component: () => import('@/views/system/banners.vue'),
        meta: { title: '全局 Banner', permission: 'marketing:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/communities/edit/:id',
        name: 'CommunityEdit',
        component: () => import('@/views/system/community-form.vue'),
        meta: { title: '编辑社区', permission: 'community:update', group: '系统管理', space: 'system', hidden: true },
      },
      {
        path: 'system/communities/create',
        name: 'CommunityCreate',
        component: () => import('@/views/system/community-form.vue'),
        meta: { title: '新增社区', permission: 'community:create', group: '系统管理', space: 'system', hidden: true },
      },
      // 门店管理（社区级）
      {
        path: 'store/settings',
        name: 'StoreSettings',
        component: () => import('@/views/store/settings.vue'),
        meta: { title: '门店设置', permission: 'community:config', group: '门店管理' },
      },
      {
        path: 'system/admins',
        name: 'AdminList',
        component: () => import('@/views/system/admins.vue'),
        meta: { title: '员工管理', permission: 'admin:view', group: '门店管理' },
      },
      {
        path: 'system/roles',
        name: 'RoleList',
        component: () => import('@/views/system/roles.vue'),
        meta: { title: '角色管理', permission: 'role:view', group: '门店管理' },
      },
      {
        // 跨社区角色总览：只读展示各社区的角色与权限，写操作回到对应社区语境的「角色管理」
        path: 'system/community-roles',
        name: 'CommunityRoleList',
        component: () => import('@/views/system/roles.vue'),
        meta: { title: '社区角色', permission: 'role:view', group: '系统管理', space: 'system' },
      },
      // 账号管理：全局账号维度（超管专用，处理器与服务端双重限制）
      {
        path: 'system/accounts',
        name: 'AccountList',
        component: () => import('@/views/system/accounts.vue'),
        meta: { title: '账号管理', permission: 'admin:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/community-permissions',
        name: 'CommunityPermission',
        component: () => import('@/views/system/community-permissions.vue'),
        meta: { title: '社区权限', permission: 'community:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/statistics',
        name: 'SystemStatistics',
        component: () => import('@/views/system/statistics.vue'),
        meta: { title: '全平台统计', permission: 'statistics:view', group: '数据统计', space: 'system', superOnly: true },
      },
      {
        path: 'system/site',
        name: 'SiteSetting',
        component: () => import('@/views/settings/site.vue'),
        meta: { title: '站点设置', permission: 'settings:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/payment',
        name: 'PaymentSetting',
        component: () => import('@/views/settings/payment.vue'),
        meta: { title: '支付配置', permission: 'settings:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/channels',
        name: 'ChannelSetting',
        component: () => import('@/views/settings/channel.vue'),
        meta: { title: '渠道管理', permission: 'settings:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/refund',
        name: 'RefundSetting',
        component: () => import('@/views/settings/refund.vue'),
        meta: { title: '售后配置', permission: 'settings:view', group: '系统管理', space: 'system' },
      },
      {
        path: 'system/attachment',
        name: 'AttachmentSetting',
        component: () => import('@/views/settings/attachment.vue'),
        meta: { title: '附件配置', permission: 'settings:view', group: '系统管理', space: 'system' },
      },
      // 本地附件迁移工具：入口在附件配置页，不在左侧菜单展示
      {
        path: 'system/attachment-migrate',
        name: 'AttachmentMigrate',
        component: () => import('@/views/settings/attachment-migrate.vue'),
        meta: { title: '附件迁移', permission: 'settings:view', group: '系统管理', space: 'system', hidden: true },
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
  // 社区选择页：独立全屏页面（不套后台框架）。
  // 多社区账号登录后未选定社区时由守卫强制进入；顶栏"切换社区"携带 force=1 主动进入
  {
    path: '/select-community',
    name: 'SelectCommunity',
    component: () => import('@/views/select-community/index.vue'),
    meta: { title: '选择社区' },
  },
  // --- 收银台独立应用（/pos 前缀）：不套后台框架，独立登录页与布局壳 ---
  // 登录/退出/交班后统一落独立登录页，不回主系统
  {
    path: '/pos/login',
    name: 'PosLogin',
    component: () => import('@/views/pos/login.vue'),
    meta: { title: '收银台登录', public: true },
  },
  {
    path: '/pos',
    name: 'PosLayout',
    component: () => import('@/views/pos/layout.vue'),
    children: [
      {
        path: 'cashier',
        name: 'PosCashier',
        component: () => import('@/views/pos/cashier/index.vue'),
        meta: { title: '收银工作台', permission: 'order:create' },
      },
      {
        path: 'shifts',
        name: 'PosShifts',
        component: () => import('@/views/pos/shifts.vue'),
        meta: { title: '我的交班记录', permission: 'order:create' },
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

  // --- 收银台独立应用（/pos 前缀）：独立登录页与鉴权，不走主系统的社区选择/兜底首页逻辑 ---
  if (to.path === '/pos' || to.path.startsWith('/pos/')) {
    // 未登录：一律去收银台独立登录页（携带原始地址供登录后回跳）
    if (!authStore.isAuthenticated) {
      return to.path === '/pos/login' ? true : { path: '/pos/login', query: { redirect: to.fullPath } }
    }
    // 已登录访问收银台登录页：直接进收银台工作台
    if (to.path === '/pos/login') {
      return { path: '/pos/cashier' }
    }
    // 幂等恢复登录态，失败（token 失效）时 ensureReady 已自动登出，落独立登录页
    if (!(await authStore.ensureReady())) {
      return { path: '/pos/login', query: { redirect: to.fullPath } }
    }
    // 商品/订单均为社区级数据：多社区账号未选定社区时先去主系统选择社区，选完带原地址回来
    if (authStore.needsCommunitySelection) {
      return { path: '/select-community', query: { redirect: to.fullPath } }
    }
    // 权限判定：收银台页面要求 order:create（无权限跳主系统无权限提示页，保留登录态）
    const posPermission = to.meta.permission as string | undefined
    if (posPermission && !authStore.hasPermission(posPermission)) {
      return { path: '/403', query: { from: to.fullPath } }
    }
    return true
  }

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

  // ready：社区未选定（多社区账号无上次选择/超管首次进入）时强制进入社区选择页
  if (authStore.needsCommunitySelection && to.path !== '/select-community') {
    return { path: '/select-community', query: { redirect: to.fullPath } }
  }
  // 已选定社区后访问选择页：仅主动进入（顶栏"切换社区"带 force=1）时放行，其余回兜底首页
  if (to.path === '/select-community' && !authStore.needsCommunitySelection && to.query.force !== '1') {
    return { path: resolveHomePath(authStore) ?? '/dashboard' }
  }

  // ready：系统管理空间仅超管可入（与菜单入口、后端超管专用接口对应，防止直连 URL 越权进入）
  if (to.meta.space === 'system' && !authStore.isSuperAdmin) {
    const home = resolveHomePath(authStore)
    return home ? { path: home } : { path: '/403', query: { from: to.fullPath } }
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
