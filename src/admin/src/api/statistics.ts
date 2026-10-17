import { request } from './request'
import type { ApiResult } from './types'

/**
 * 订单状态统计响应
 */
export interface OrderStatistics {
  pending_pay: number
  pending_ship: number
  pending_receive: number
  completed: number
  cancelled: number
  refunding: number
  total_orders: number
}

/**
 * 销售统计响应
 */
export interface SalesStatistics {
  total_amount: number
  pay_amount: number
  total_orders: number
  avg_order_amount: number
}

/**
 * 商品排行项
 */
export interface ProductRankingItem {
  product_id: number
  product_name: string
  sales_count: number
  sales_amount: number
}

/**
 * 营收报表项
 */
export interface RevenueReportItem {
  date: string
  revenue: number
  order_count: number
}

/**
 * 仪表盘数据响应
 */
export interface DashboardStatistics {
  order_count: number
  sale_amount: number
  user_count: number
  product_count: number
  pending_ship: number
  refunding: number
}

/**
 * 获取仪表盘统计数据
 * 聚合今日订单数、今日销售额、注册用户数、在售商品数、待发货订单数、待处理退款数
 * @returns 仪表盘统计数据
 */
export function getDashboardStatistics() {
  return request<ApiResult<DashboardStatistics>>({
    url: '/api/v1/admin/dashboard',
    method: 'GET',
  })
}

/**
 * 获取订单状态统计
 * 超管可传 scope='all'（全平台聚合）或 community_id（指定社区）；
 * 普管传参被网关忽略，恒为当前社区
 * @param params 查询参数
 * @returns 订单状态统计
 */
export function getOrderStatistics(params: {
  start_date?: string
  end_date?: string
  scope?: 'all'
  community_id?: number
}) {
  return request<ApiResult<OrderStatistics>>({
    url: '/api/v1/admin/statistics/orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取销售统计
 * 超管可传 scope='all'（全平台聚合）或 community_id（指定社区）；
 * 普管传参被网关忽略，恒为当前社区
 * @param params 查询参数
 * @returns 销售统计
 */
export function getSalesStatistics(params: {
  start_date?: string
  end_date?: string
  scope?: 'all'
  community_id?: number
}) {
  return request<ApiResult<SalesStatistics>>({
    url: '/api/v1/admin/statistics/sales',
    method: 'GET',
    params,
  })
}

/**
 * 获取商品销售排行
 * 超管可传 scope='all'（全平台聚合）或 community_id（指定社区）；
 * 普管传参被网关忽略，恒为当前社区
 * 注意：网关 SuccessWithList 返回的 data 直接是数组（非 {list,total}）
 * @param params 查询参数
 * @returns 商品销售排行列表
 */
export function getProductRankings(params: {
  start_date?: string
  end_date?: string
  limit?: number
  scope?: 'all'
  community_id?: number
}) {
  return request<ApiResult<ProductRankingItem[]>>({
    url: '/api/v1/admin/statistics/rankings',
    method: 'GET',
    params,
  })
}

/**
 * 获取营收报表
 * 超管可传 scope='all'（全平台聚合）或 community_id（指定社区）；
 * 普管传参被网关忽略，恒为当前社区
 * 注意：网关 SuccessWithList 返回的 data 直接是数组（非 {list,total}）
 * @param params 查询参数
 * @returns 营收报表列表
 */
export function getRevenueReport(params: {
  start_date?: string
  end_date?: string
  group_by?: string
  scope?: 'all'
  community_id?: number
}) {
  return request<ApiResult<RevenueReportItem[]>>({
    url: '/api/v1/admin/statistics/revenue',
    method: 'GET',
    params,
  })
}
