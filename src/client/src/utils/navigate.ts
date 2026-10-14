/**
 * 页面跳转封装
 * 统一处理登录页、商品详情、订单确认等常用跳转
 */
import { TAB_BAR_PATHS } from './tab-bar-paths'

/**
 * 解析跳转地址，返回归一化后的纯路径与 query 参数
 * @param url 跳转地址，如 /pages/product/detail?id=1
 * @returns path：以 / 开头的纯路径；query：? 之后的原始参数串（可能为空）
 */
function parsePageUrl(url: string) {
  const normalized = url.startsWith('/') ? url : `/${url}`
  const queryIndex = normalized.indexOf('?')
  const path = (queryIndex === -1 ? normalized : normalized.slice(0, queryIndex)).replace(/\/+$/, '')
  const query = queryIndex === -1 ? '' : normalized.slice(queryIndex + 1)
  return { path, query }
}

/**
 * 通用页面跳转，自动判断使用 switchTab 还是 navigateTo
 * - tabBar 页面：switchTab 跳转（switchTab 不支持 query 参数，参数会被丢弃）
 * - 普通页面：navigateTo 跳转，失败（如页面栈超 10 层）时降级 redirectTo
 * - 外部链接（http/https 开头）：H5 端新窗口打开，其他端提示暂不支持
 * @param url 跳转地址，支持带 query，如 /pages/product/detail?id=1
 * @returns 无
 * @example
 * goPage('/pages/product/detail?id=1') // 普通页面
 * goPage('/pages/cart/cart') // tabBar 页面自动 switchTab
 * goPage('https://example.com') // 外部链接
 */
export function goPage(url: string) {
  if (!url) return
  // 外部链接：H5 新窗口打开，其他端提示
  if (/^https?:\/\//i.test(url)) {
    // #ifdef H5
    window.open(url, '_blank')
    // #endif
    // #ifndef H5
    uni.showToast({ title: '暂不支持外部链接', icon: 'none' })
    // #endif
    return
  }
  const { path, query } = parsePageUrl(url)
  // tabBar 页面：switchTab（不支持参数，带参数时警告提示，参数会被丢弃）
  if (TAB_BAR_PATHS.includes(path.replace(/^\//, '') as (typeof TAB_BAR_PATHS)[number])) {
    if (query) {
      console.warn(`[goPage] tabBar 页面不支持 query 参数，已忽略：${path} 的参数 "${query}"`)
    }
    uni.switchTab({ url: path })
    return
  }
  const target = query ? `${path}?${query}` : path
  uni.navigateTo({
    url: target,
    fail: () => {
      // 页面栈超限等场景降级为重定向
      uni.redirectTo({ url: target })
    },
  })
}

/**
 * 跳转登录页
 * @param redirect 登录成功后回跳地址（可选）
 */
export function goLogin(redirect?: string) {
  const url = redirect
    ? `/pages/login/login?redirect=${encodeURIComponent(redirect)}`
    : '/pages/login/login'
  goPage(url)
}

/**
 * 返回上一页，上一页不存在时跳转首页
 */
export function goBack() {
  const pages = getCurrentPages()
  if (pages.length > 1) {
    uni.navigateBack({})
  } else {
    goHome()
  }
}

/**
 * 跳转首页 tab（首页即分类页）
 */
export function goHome() {
  goPage('/pages/category/category')
}

/**
 * 跳转商品详情
 * @param id 商品 ID
 */
export function goProductDetail(id: number) {
  goPage(`/pages/product/detail?id=${id}`)
}

/**
 * 跳转确认订单页
 * @param query 订单参数，商品直接购买时传入 productId/skuId/quantity
 */
export function goOrderConfirm(query?: {
  productId?: number
  skuId?: number
  quantity?: number
  cartItemIds?: number[]
}) {
  const params = new URLSearchParams()
  if (query?.productId) params.append('productId', String(query.productId))
  if (query?.skuId) params.append('skuId', String(query.skuId))
  if (query?.quantity) params.append('quantity', String(query.quantity))
  if (query?.cartItemIds?.length) {
    params.append('cartItemIds', query.cartItemIds.join(','))
  }
  const qs = params.toString()
  goPage(`/pages/order/confirm${qs ? '?' + qs : ''}`)
}

/**
 * 跳转订单列表
 * @param status 订单状态筛选（可选）
 */
export function goOrderList(status?: number) {
  const url = status !== undefined
    ? `/pages/order/list?status=${status}`
    : '/pages/order/list'
  goPage(url)
}

/**
 * 跳转订单详情
 * @param id 订单 ID
 */
export function goOrderDetail(id: number) {
  goPage(`/pages/order/detail?id=${id}`)
}

/**
 * 跳转地址列表
 */
export function goAddressList() {
  goPage('/pages/address/list')
}

/**
 * 跳转地址编辑/新增
 * @param id 地址 ID，新增时不传
 */
export function goAddressEdit(id?: number) {
  const url = id ? `/pages/address/edit?id=${id}` : '/pages/address/edit'
  goPage(url)
}

/**
 * 跳转社区选择
 */
export function goCommunitySelect() {
  goPage('/pages/community/select')
}

/**
 * 跳转优惠券页
 */
export function goCoupon() {
  goPage('/pages/coupon/coupon')
}

/**
 * 跳转秒杀活动页
 */
export function goFlash() {
  goPage('/pages/flash/flash')
}

/**
 * 跳转满减活动页
 */
export function goFullReduction() {
  goPage('/pages/full-reduction/full-reduction')
}
