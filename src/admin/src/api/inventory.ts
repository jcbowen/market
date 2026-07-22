import { request } from './request'
import type { ApiResult, InventoryCheck, InventoryLog, PageParams, PageResult, PurchaseOrderItem, Sku, StockCheckDetail } from './types'

/**
 * 库存管理相关 API
 * 与后端 /api/v1/admin/inventory/* 路径保持一致
 */

/**
 * 商品入库
 * @param data 入库数据
 * @returns 入库结果
 */
export function stockIn(data: {
  sku_id: number
  quantity: number
  remark?: string
  batch_no?: string
  production_date?: string
  expire_date?: string
}) {
  return request<ApiResult<null>>({
    url: '/api/v1/admin/inventory/entries',
    method: 'POST',
    data: {
      type: 1,
      items: [data],
      remark: data.remark,
    },
  })
}

/**
 * 库存出库
 * @param data 出库数据
 * @returns 出库结果
 */
export function stockOut(data: {
  type: number
  sku_id: number
  quantity: number
  remark?: string
}) {
  return request<ApiResult<null>>({
    url: '/api/v1/admin/inventory/out',
    method: 'POST',
    data: {
      type: data.type,
      items: [
        {
          sku_id: data.sku_id,
          quantity: data.quantity,
          remark: data.remark,
        },
      ],
      remark: data.remark,
    },
  })
}

/**
 * 生成补货建议单
 * @returns 补货建议列表
 */
export function generatePurchaseOrder() {
  return request<ApiResult<PurchaseOrderItem[]>>({
    url: '/api/v1/admin/inventory/purchase-order',
    method: 'POST',
  })
}

/**
 * 获取库存流水列表
 * @param params 查询参数
 * @returns 分页库存流水
 */
export function getInventoryLogList(
  params: PageParams & { sku_id?: number; type?: number; start_date?: string; end_date?: string },
) {
  return request<ApiResult<PageResult<InventoryLog>>>({
    url: '/api/v1/admin/inventory/entries',
    method: 'GET',
    params,
  })
}

/**
 * 获取 SKU 库存列表
 * @param params 分页参数
 * @returns 分页 SKU 库存
 */
export function getSkuStockList(params: PageParams) {
  return request<ApiResult<PageResult<Sku>>>({
    url: '/api/v1/admin/inventory/skus',
    method: 'GET',
    params,
  })
}

/**
 * 获取库存预警列表
 * @returns 库存预警列表
 */
export function getStockWarnings() {
  return request<
    ApiResult<
      Array<{
        sku_id: number
        sku_code: string
        spec_name: string
        product_name: string
        stock: number
        safety_stock: number
        warning_type: number
        expire_date: string
      }>
    >
  >({
    url: '/api/v1/admin/inventory/warnings',
    method: 'GET',
  })
}

/**
 * 获取 SKU 列表
 * @param productId 商品 ID
 * @returns SKU 列表
 */
export function getSkuList(productId: number) {
  return request<ApiResult<Sku[]>>({
    url: '/api/v1/skus',
    method: 'GET',
    params: { product_id: productId },
  })
}

/**
 * 获取盘点列表
 * @param params 查询参数
 * @returns 分页盘点记录
 */
export function getInventoryCheckList(params: PageParams & { product_id?: number; status?: number }) {
  return request<ApiResult<PageResult<InventoryCheck>>>({
    url: '/api/v1/admin/inventory/checks',
    method: 'GET',
    params,
  })
}

/**
 * 获取盘点单详情
 * @param id 盘点单 ID
 * @returns 盘点单详情
 */
export function getInventoryCheckDetail(id: number) {
  return request<ApiResult<StockCheckDetail>>({
    url: `/api/v1/admin/inventory/checks/${id}`,
    method: 'GET',
  })
}

/**
 * 创建盘点单
 * @param data 盘点数据
 * @returns 创建结果
 */
export function createInventoryCheck(data: { remark?: string }) {
  return request<ApiResult<InventoryCheck>>({
    url: '/api/v1/admin/inventory/checks',
    method: 'POST',
    data,
  })
}

/**
 * 提交盘点单
 * @param id 盘点单 ID
 * @param items 盘点项
 * @returns 提交结果
 */
export function submitInventoryCheck(id: number, items: Array<{ sku_id: number; actual_stock: number }>) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/inventory/checks/${id}/submit`,
    method: 'POST',
    data: { items },
  })
}
