import { request } from './request'
import type { ApiResult, PageResult } from './types'

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
}

/**
 * 获取仪表盘统计数据
 * 由于后端 /dashboard 为占位接口，此处组合今日订单与销售统计返回
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
 * @param params 查询参数
 * @returns 订单状态统计
 */
export function getOrderStatistics(params: { start_date?: string; end_date?: string }) {
  return request<ApiResult<OrderStatistics>>({
    url: '/api/v1/admin/statistics/orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取销售统计
 * @param params 查询参数
 * @returns 销售统计
 */
export function getSalesStatistics(params: { start_date?: string; end_date?: string }) {
  return request<ApiResult<SalesStatistics>>({
    url: '/api/v1/admin/statistics/sales',
    method: 'GET',
    params,
  })
}

/**
 * 获取商品销售排行
 * @param params 查询参数
 * @returns 商品销售排行列表
 */
export function getProductRankings(params: { start_date?: string; end_date?: string; limit?: number }) {
  return request<ApiResult<PageResult<ProductRankingItem>>>({
    url: '/api/v1/admin/statistics/rankings',
    method: 'GET',
    params,
  })
}

/**
 * 获取营收报表
 * @param params 查询参数
 * @returns 营收报表列表
 */
export function getRevenueReport(params: { start_date?: string; end_date?: string; group_by?: string }) {
  return request<ApiResult<PageResult<RevenueReportItem>>>({
    url: '/api/v1/admin/statistics/revenue',
    method: 'GET',
    params,
  })
}
