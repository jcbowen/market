import { request } from './request'
import type { ApiResult, Banner, Coupon, CouponRecord, FreeShipping, FullReduction, PageParams, PageResult, Seckill } from './types'

/**
 * 营销管理相关 API
 */

// 优惠券

/**
 * 获取优惠券列表
 * @param params 查询参数
 * @returns 分页优惠券列表
 */
export function getCouponList(params: PageParams & { name?: string; status?: number }) {
  return request<ApiResult<PageResult<Coupon>>>({
    url: '/api/v1/admin/coupons',
    method: 'GET',
    params,
  })
}

/**
 * 创建优惠券
 * @param data 优惠券数据
 * @returns 创建结果
 */
export function createCoupon(data: Partial<Coupon>) {
  return request<ApiResult<Coupon>>({
    url: '/api/v1/admin/coupons',
    method: 'POST',
    data,
  })
}

/**
 * 更新优惠券
 * @param id 优惠券 ID
 * @param data 优惠券数据
 * @returns 更新结果
 */
export function updateCoupon(id: number, data: Partial<Coupon>) {
  return request<ApiResult<Coupon>>({
    url: `/api/v1/admin/coupons/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除优惠券
 * @param id 优惠券 ID
 * @returns 删除结果
 */
export function deleteCoupon(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/coupons/${id}`,
    method: 'DELETE',
  })
}

/**
 * 手动发放优惠券（批量选会员，逐人独立校验，单人失败不影响其他人）
 * @param id 优惠券 ID
 * @param userIds 目标会员 ID 列表
 * @returns 成功发放人数 + 失败列表（user_id + reason）
 */
export function grantCoupon(id: number, userIds: number[]) {
  return request<ApiResult<{ granted_count: number; failures: { user_id: number; reason: string }[] }>>({
    url: `/api/v1/admin/coupons/${id}/grant`,
    method: 'POST',
    data: { user_ids: userIds },
  })
}

/**
 * 领取/发放记录（user_coupons，按当前社区语境过滤）
 * @param params 筛选参数（券/会员/状态/来源）
 */
export function getCouponRecords(params: PageParams & { coupon_id?: number; user_id?: number; status?: number; source?: number }) {
  return request<ApiResult<PageResult<CouponRecord>>>({
    url: '/api/v1/admin/coupon-records',
    method: 'GET',
    params,
  })
}

// 秒杀

/**
 * 获取秒杀活动列表
 * @param params 查询参数
 * @returns 分页秒杀列表
 */
export function getSeckillList(params: PageParams & { product_id?: number; status?: number }) {
  return request<ApiResult<PageResult<Seckill>>>({
    url: '/api/v1/admin/flash-sales',
    method: 'GET',
    params,
  })
}

/**
 * 创建秒杀活动
 * @param data 秒杀数据
 * @returns 创建结果
 */
export function createSeckill(data: Partial<Seckill>) {
  return request<ApiResult<Seckill>>({
    url: '/api/v1/admin/flash-sales',
    method: 'POST',
    data,
  })
}

/**
 * 更新秒杀活动
 * @param id 秒杀 ID
 * @param data 秒杀数据
 * @returns 更新结果
 */
export function updateSeckill(id: number, data: Partial<Seckill>) {
  return request<ApiResult<Seckill>>({
    url: `/api/v1/admin/flash-sales/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除秒杀活动
 * @param id 秒杀 ID
 * @returns 删除结果
 */
export function deleteSeckill(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/flash-sales/${id}`,
    method: 'DELETE',
  })
}

// 满减

/**
 * 获取满减活动列表
 * @param params 查询参数
 * @returns 分页满减列表
 */
export function getFullReductionList(params: PageParams & { name?: string; status?: number }) {
  return request<ApiResult<PageResult<FullReduction>>>({
    url: '/api/v1/admin/full-reductions',
    method: 'GET',
    params,
  })
}

/**
 * 创建满减活动
 * @param data 满减数据
 * @returns 创建结果
 */
export function createFullReduction(data: Partial<FullReduction>) {
  return request<ApiResult<FullReduction>>({
    url: '/api/v1/admin/full-reductions',
    method: 'POST',
    data,
  })
}

/**
 * 更新满减活动
 * @param id 满减 ID
 * @param data 满减数据
 * @returns 更新结果
 */
export function updateFullReduction(id: number, data: Partial<FullReduction>) {
  return request<ApiResult<FullReduction>>({
    url: `/api/v1/admin/full-reductions/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除满减活动
 * @param id 满减 ID
 * @returns 删除结果
 */
export function deleteFullReduction(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/full-reductions/${id}`,
    method: 'DELETE',
  })
}

// 免配送费

/**
 * 获取免配送费活动列表（含排除范围）
 * @param params 查询参数
 * @returns 分页免配送费活动列表
 */
export function getFreeShippingList(params: PageParams & { name?: string; status?: number }) {
  return request<ApiResult<PageResult<FreeShipping>>>({
    url: '/api/v1/admin/free-shippings',
    method: 'GET',
    params,
  })
}

/**
 * 创建免配送费活动
 * @param data 活动数据（含排除范围 scopes）
 * @returns 创建结果
 */
export function createFreeShipping(data: Partial<FreeShipping>) {
  return request<ApiResult<FreeShipping>>({
    url: '/api/v1/admin/free-shippings',
    method: 'POST',
    data,
  })
}

/**
 * 更新免配送费活动（启停/编辑；scopes 传 null 不修改范围，空数组清空）
 * @param id 活动 ID
 * @param data 活动数据
 * @returns 更新结果
 */
export function updateFreeShipping(id: number, data: Partial<FreeShipping>) {
  return request<ApiResult<FreeShipping>>({
    url: `/api/v1/admin/free-shippings/${id}`,
    method: 'PUT',
    data,
  })
}

// Banner

/**
 * 获取 Banner 列表
 * @param params 查询参数
 * @returns Banner 列表
 */
export function getBannerList(params: { title?: string; position?: number; status?: number }) {
  return request<ApiResult<Banner[]>>({
    url: '/api/v1/admin/banners',
    method: 'GET',
    params,
  })
}

/**
 * 创建 Banner
 * @param data Banner 数据
 * @returns 创建结果
 */
export function createBanner(data: Partial<Banner>) {
  return request<ApiResult<Banner>>({
    url: '/api/v1/admin/banners',
    method: 'POST',
    data,
  })
}

/**
 * 更新 Banner
 * @param id Banner ID
 * @param data Banner 数据
 * @returns 更新结果
 */
export function updateBanner(id: number, data: Partial<Banner>) {
  return request<ApiResult<Banner>>({
    url: `/api/v1/admin/banners/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除 Banner
 * @param id Banner ID
 * @returns 删除结果
 */
export function deleteBanner(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/banners/${id}`,
    method: 'DELETE',
  })
}
