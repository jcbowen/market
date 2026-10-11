import { createRouter, createWebHashHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import Cookies from 'js-cookie'
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
        path: 'orders/:id',
        name: 'OrderDetail',
        component: () => import('@/views/order/detail.vue'),
        meta: { title: '订单详情', permission: 'order:view', group: '订单管理', hidden: true },
      },
      {
        path: 'refunds',
        name: 'RefundList',
        component: () => import('@/views/order/refunds.vue'),
        meta: { title: '退款管理', permission: 'refund:view', group: '订单管理' },
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
        meta: { title: '管理员列表', permission: 'admin:view', group: '系统管理' },
      },
      {
        path: 'system/roles',
        name: 'RoleList',
        component: () => import('@/views/system/roles.vue'),
        meta: { title: '角色管理', permission: 'role:view', group: '系统管理' },
      },
      {
        path: 'system/payment',
        name: 'PaymentSetting',
        component: () => import('@/views/settings/payment.vue'),
        meta: { title: '支付配置', permission: 'settings:view', group: '系统管理' },
      },
      {
        path: 'system/attachment',
        name: 'AttachmentSetting',
        component: () => import('@/views/settings/attachment.vue'),
        meta: { title: '附件配置', permission: 'settings:view', group: '系统管理' },
      },
      // 个人设置：入口在右上角头像下拉，不在左侧菜单展示
      {
        path: 'settings/profile',
        name: 'Profile',
        component: () => import('@/views/settings/profile.vue'),
        meta: { title: '个人设置', permission: 'dashboard:view', hidden: true },
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
 * 路由守卫
 * - 未登录且访问非公开页面时跳转登录
 * - 已登录访问登录页时跳转工作台
 * - 已登录但管理员信息尚未加载（如刷新页面后 store 状态重置）时，先拉取信息恢复权限列表，
 *   避免权限为空导致需要权限的路由被守卫重定向到自身形成无限循环（表现为页面一直白屏加载）
 * - 拉取失败（token 失效等）或仍无权限时清空登录态兜底跳转登录页
 */
router.beforeEach(async (to, from, next) => {
  const authStore = useAuthStore()
  const token = Cookies.get('access_token')
  const isLoginPage = to.path === '/login'

  if (!token && !to.meta.public) {
    next('/login')
    return
  }

  if (token && isLoginPage) {
    next('/dashboard')
    return
  }

  const permission = to.meta.permission as string | undefined
  // 权限未就绪时先拉取管理员信息；失败说明 token 已失效，清空登录态回登录页
  if (token && permission && !authStore.hasPermission(permission) && !authStore.admin) {
    const loaded = await authStore.fetchAdminInfo().catch(() => false)
    if (!loaded) {
      authStore.logout()
      next('/login')
      return
    }
  }

  if (token && permission && !authStore.hasPermission(permission)) {
    // 注意：此处不能重定向到同样需要权限的 /dashboard，否则会再次进入守卫形成无限重定向；
    // 清空登录态跳转登录页是安全兜底
    authStore.logout()
    next('/login')
    return
  }

  next()
})

export default router
