/**
 * 页面跳转封装
 * 统一处理登录页、商品详情、订单确认等常用跳转
 */

/**
 * 跳转登录页
 * @param redirect 登录成功后回跳地址（可选）
 */
export function goLogin(redirect?: string) {
  const url = redirect
    ? `/pages/login/login?redirect=${encodeURIComponent(redirect)}`
    : '/pages/login/login'
  uni.navigateTo({ url })
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
 * 跳转首页 tab
 */
export function goHome() {
  uni.switchTab({ url: '/pages/index/index' })
}

/**
 * 跳转分类页
 * @param categoryId 默认选中的分类 ID（可选）
 */
export function goCategory(categoryId?: number) {
  const url = categoryId
    ? `/pages/category/category?categoryId=${categoryId}`
    : '/pages/category/category'
  uni.switchTab({ url })
}

/**
 * 跳转商品详情
 * @param id 商品 ID
 */
export function goProductDetail(id: number) {
  uni.navigateTo({ url: `/pages/product/detail?id=${id}` })
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
  uni.navigateTo({
    url: `/pages/order/confirm${qs ? '?' + qs : ''}`,
  })
}

/**
 * 跳转订单列表
 * @param status 订单状态筛选（可选）
 */
export function goOrderList(status?: number) {
  const url = status !== undefined
    ? `/pages/order/list?status=${status}`
    : '/pages/order/list'
  uni.navigateTo({ url })
}

/**
 * 跳转订单详情
 * @param id 订单 ID
 */
export function goOrderDetail(id: number) {
  uni.navigateTo({ url: `/pages/order/detail?id=${id}` })
}

/**
 * 跳转地址列表
 */
export function goAddressList() {
  uni.navigateTo({ url: '/pages/address/list' })
}

/**
 * 跳转地址编辑/新增
 * @param id 地址 ID，新增时不传
 */
export function goAddressEdit(id?: number) {
  const url = id ? `/pages/address/edit?id=${id}` : '/pages/address/edit'
  uni.navigateTo({ url })
}

/**
 * 跳转社区选择
 */
export function goCommunitySelect() {
  uni.navigateTo({ url: '/pages/community/select' })
}

/**
 * 跳转优惠券页
 */
export function goCoupon() {
  uni.navigateTo({ url: '/pages/coupon/coupon' })
}

/**
 * 跳转秒杀活动页
 */
export function goFlash() {
  uni.navigateTo({ url: '/pages/flash/flash' })
}

/**
 * 跳转满减活动页
 */
export function goFullReduction() {
  uni.navigateTo({ url: '/pages/full-reduction/full-reduction' })
}
