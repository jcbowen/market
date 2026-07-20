/**
 * 订单相关 API
 * 包含创建订单、列表、详情、取消、申请退款
 */

import { get, post, put } from './request'
import type { PageData } from './request'
import type { Order, Address } from './types'

/** 创建订单参数 */
export interface CreateOrderParams {
  address_id: number
  items: { product_id: number; sku_id: number; quantity: number }[]
  coupon_id?: number
  remark?: string
  cart_item_ids?: number[]
}

/**
 * 创建订单
 * @param data 订单参数
 */
export function createOrder(data: CreateOrderParams) {
  return post<Order>('/orders', data as Record<string, unknown>)
}

/**
 * 获取订单列表
 * @param params 状态、分页、社区等参数
 */
export function getOrders(params?: {
  status?: number
  page?: number
  page_size?: number
  community_id?: number
}) {
  return get<PageData<Order>>('/orders', params as Record<string, unknown>)
}

/**
 * 获取订单详情
 * @param id 订单 ID
 */
export function getOrderDetail(id: number) {
  return get<Order>(`/orders/${id}`)
}

/**
 * 取消订单
 * @param id 订单 ID
 */
export function cancelOrder(id: number) {
  return put<Order>(`/orders/${id}/cancel`)
}

/**
 * 申请退款
 * @param id 订单 ID
 * @param reason 退款原因
 * @param amount 退款金额（可选）
 */
export function applyRefund(id: number, reason: string, amount?: number) {
  return post(`/orders/${id}/refund`, { reason, amount })
}

/**
 * 确认收货
 * @param id 订单 ID
 */
export function confirmReceive(id: number) {
  return post<Order>(`/orders/${id}/receive`)
}
