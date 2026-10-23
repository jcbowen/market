/**
 * 营销相关 API
 * 包含优惠券、秒杀活动
 */

import { get, post } from './request'
import type { ApiResponse, PageData } from './request'
import type { Coupon, UserCoupon, FlashSale } from './types'

/** 后端优惠券原始结构 */
interface BackendCoupon {
  id: number
  name: string
  type: number
  threshold: number
  discount_value: number
  total_count: number
  received_count: number
  start_time: string
  end_time: string
  status: number
}

/** 后端用户优惠券原始结构 */
interface BackendUserCoupon {
  id: number
  coupon_id: number
  name: string
  type: number
  threshold: number
  discount_value: number
  status: number
  expire_at: string
}

/** 后端秒杀活动商品项 */
interface BackendFlashSaleItem {
  id?: number
  sku_id: number
  product_id?: number
  product_name?: string
  spec_name?: string
  flash_price: number
  original_price: number
  stock: number
  limit_per_user: number
}

/** 后端秒杀活动原始结构（info + items） */
interface BackendFlashSale {
  id: number
  name: string
  start_time: string
  end_time: string
  status: number
  items?: BackendFlashSaleItem[]
}

/**
 * 将后端优惠券字段映射为前端 Coupon 类型
 * @param c 后端优惠券
 * @returns 前端 Coupon 对象
 */
function toCoupon(c: BackendCoupon): Coupon {
  return {
    id: c.id,
    name: c.name,
    type: c.type,
    threshold: c.threshold,
    value: c.discount_value,
    start_time: c.start_time,
    end_time: c.end_time,
    total: c.total_count,
    received: c.received_count,
  }
}

/**
 * 将后端用户优惠券字段映射为前端 UserCoupon 类型
 * @param c 后端用户优惠券
 * @returns 前端 UserCoupon 对象
 */
function toUserCoupon(c: BackendUserCoupon): UserCoupon {
  return {
    id: c.id,
    coupon_id: c.coupon_id,
    name: c.name,
    type: c.type,
    threshold: c.threshold,
    value: c.discount_value,
    start_time: '',
    end_time: c.expire_at,
    status: c.status,
  }
}

/**
 * 将后端秒杀活动扁平化为前端 FlashSale 列表
 * @param list 后端秒杀活动列表
 * @returns 前端 FlashSale 列表
 */
function flattenFlashSales(list: BackendFlashSale[]): FlashSale[] {
  const result: FlashSale[] = []
  for (const activity of list) {
    const items = Array.isArray(activity.items) ? activity.items : []
    if (items.length === 0) {
      continue
    }
    for (const item of items) {
      result.push({
        id: item.id ?? activity.id,
        product_id: item.product_id ?? 0,
        product_name: item.product_name || activity.name,
        product_image: '/static/logo.png',
        flash_price: item.flash_price,
        original_price: item.original_price,
        stock: item.stock,
        sold: 0,
        start_time: activity.start_time,
        end_time: activity.end_time,
        status: activity.status,
      })
    }
  }
  return result
}

/**
 * 统一将后端列表响应转换为前端分页结构
 * @param res 后端原始响应
 * @param mapper 映射函数
 * @param page 当前页
 * @param pageSize 每页大小
 * @returns 前端分页响应
 */
function toPageResponse<T, R>(
  res: ApiResponse<T[] | PageData<T>>,
  mapper: (item: T) => R,
  page = 1,
  pageSize = 10,
): ApiResponse<PageData<R>> {
  const rawList = Array.isArray(res.data) ? res.data : res.data?.list || []
  const list = rawList.map(mapper)
  return {
    ...res,
    data: {
      list,
      total: list.length,
      page,
      page_size: pageSize,
    },
  }
}

/**
 * 获取可领取优惠券列表
 * @param params 分页、社区等参数
 */
export function getCoupons(params?: { page?: number; page_size?: number; community_id?: number }) {
  return get<BackendCoupon[]>('/coupons', params as Record<string, unknown>).then((res) =>
    toPageResponse(res, toCoupon, params?.page, params?.page_size),
  )
}

/**
 * 领取优惠券
 * @param couponId 优惠券 ID
 */
export function receiveCoupon(couponId: number) {
  return post('/coupons/receive', { coupon_id: couponId })
}

/**
 * 获取当前用户优惠券列表
 * @param params 状态、分页参数
 */
export function getUserCoupons(params?: {
  status?: number
  page?: number
  page_size?: number
}) {
  return get<BackendUserCoupon[]>('/user/coupons', params as Record<string, unknown>).then((res) =>
    toPageResponse(res, toUserCoupon, params?.page, params?.page_size),
  )
}

/**
 * 获取秒杀活动列表
 * @param params 分页、社区等参数
 */
export function getFlashSales(params?: { page?: number; page_size?: number; community_id?: number }) {
  return get<BackendFlashSale[]>('/flash-sales', params as Record<string, unknown>).then((res) => {
    const rawList = Array.isArray(res.data) ? res.data : []
    const list = flattenFlashSales(rawList)
    return {
      ...res,
      data: {
        list,
        total: list.length,
        page: params?.page || 1,
        page_size: params?.page_size || list.length,
      },
    }
  })
}
