/**
 * 订单相关 API
 * 包含创建订单、列表、详情、取消、申请退款
 */

import { get, post, put } from './request'
import type { PageData } from './request'
import type { Order, Address } from './types'

/** 创建订单参数 */
export interface CreateOrderParams {
  /** 配送方式：1 社区自提 2 送货上门 */
  delivery_type: 1 | 2
  /** 收货地址 ID；自提单传 0 */
  address_id: number
  /** 期望配送时间（可选） */
  delivery_time?: string
  items: { product_id: number; sku_id: number; quantity: number }[]
  coupon_id?: number
  remark?: string
  /** 结算来源的购物车项 ID（下单成功后前端据此清理购物车） */
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
 * @param reason 取消原因（可选）
 */
export function cancelOrder(id: number, reason?: string) {
  return put<Order>(`/orders/${id}/cancel`, { reason } as Record<string, unknown>)
}

/** 申请退款参数 */
export interface ApplyRefundParams {
  /** 退款原因 */
  reason: string
  /** 退款商品明细；为空表示整单退 */
  items?: { order_item_id: number; quantity: number }[]
  /** 手动调低后的退款金额（仅允许低于服务端汇算值，不传按汇算值全额退） */
  amount?: number
  /** 运费退还策略为 user_decide 时用户是否选择退运费 */
  refund_freight?: boolean
}

/**
 * 申请退款
 * @param id 订单 ID
 * @param data 退款参数
 */
export function applyRefund(id: number, data: ApplyRefundParams) {
  return post(`/orders/${id}/refund`, data as unknown as Record<string, unknown>)
}

/**
 * 获取退款配置（运费退还策略）
 * @returns refund_freight_policy: always/never/user_decide
 */
export function getRefundConfig() {
  return get<{ refund_freight_policy: string }>('/refund/config')
}

/**
 * 确认收货
 * @param id 订单 ID
 */
export function confirmReceive(id: number) {
  return post<Order>(`/orders/${id}/receive`)
}
