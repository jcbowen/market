import { request } from './request'
import type { ApiResult, Order, OrderDetail, PageParams, PageResult } from './types'

/**
 * 订单管理相关 API
 */

/**
 * 获取订单列表
 * @param params 查询参数
 * @returns 分页订单列表
 */
export function getOrderList(params: PageParams & { order_no?: string; status?: number; start_time?: string; end_time?: string }) {
  return request<ApiResult<PageResult<Order>>>({
    url: '/api/v1/admin/orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取订单详情
 * @param id 订单 ID
 * @returns 订单详情
 */
export function getOrderDetail(id: number) {
  return request<ApiResult<OrderDetail>>({
    url: `/api/v1/admin/orders/${id}`,
    method: 'GET',
  })
}

/**
 * 更新订单状态
 * @param id 订单 ID
 * @param status 目标状态
 * @param remark 备注
 * @returns 更新结果
 */
export function updateOrderStatus(id: number, status: number, remark?: string) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/status`,
    method: 'PUT',
    data: { status, remark },
  })
}

/**
 * 订单发货
 * @param id 订单 ID
 * @param data 物流信息
 * @returns 发货结果
 */
export function shipOrder(id: number, data: { express_company?: string; express_no?: string; remark?: string }) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/ship`,
    method: 'POST',
    data,
  })
}

/**
 * 完成订单
 * @param id 订单 ID
 * @returns 完成结果
 */
export function completeOrder(id: number) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/complete`,
    method: 'POST',
  })
}

/**
 * 获取退款列表
 * @param params 查询参数
 * @returns 分页退款列表
 */
export function getRefundList(params: PageParams & { status?: number }) {
  return request<ApiResult<PageResult<Record<string, unknown>>>>({
    url: '/api/v1/admin/refunds',
    method: 'GET',
    params,
  })
}

/**
 * 同意退款
 * @param id 退款单 ID
 * @returns 操作结果
 */
export function approveRefund(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/approve`,
    method: 'POST',
  })
}

/**
 * 拒绝退款
 * @param id 退款单 ID
 * @param reject_reason 拒绝原因
 * @returns 操作结果
 */
export function rejectRefund(id: number, reject_reason: string) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/reject`,
    method: 'POST',
    data: { reject_reason },
  })
}
