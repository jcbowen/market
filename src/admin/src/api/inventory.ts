import { request } from './request'
import type { ApiResult, InventoryCheck, InventoryLog, PageParams, PageResult, PurchaseOrderItem, Sku, StockBatch, StockCheckDetail, StockInOrder, StockInOrderDetail, StockOutOrder, StockOutOrderDetail, Supplier, PurchaseOrder, PurchaseOrderDetail, InventorySummaryItem, ProfitReportItem } from './types'

/**
 * 库存管理相关 API（进销存重构版）
 * 与后端 /api/v1/admin/inventory/* 路径保持一致
 */

// ==================== 库存工作台 ====================

/**
 * 获取库存流水列表
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
 */
export function getSkuStockList(params: PageParams & { keyword?: string }) {
  return request<ApiResult<PageResult<Sku>>>({
    url: '/api/v1/admin/inventory/skus',
    method: 'GET',
    params,
  })
}

/**
 * 获取库存预警列表
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
 * 获取批次库存列表
 */
export function getStockBatchList(params: PageParams & { sku_id?: number; keyword?: string; in_stock_only?: boolean }) {
  return request<ApiResult<PageResult<StockBatch>>>({
    url: '/api/v1/admin/inventory/batches',
    method: 'GET',
    params,
  })
}

// ==================== 供应商 ====================

/**
 * 获取供应商列表
 */
export function getSupplierList(params: PageParams & { keyword?: string; status?: number }) {
  return request<ApiResult<PageResult<Supplier>>>({
    url: '/api/v1/admin/inventory/suppliers',
    method: 'GET',
    params,
  })
}

/**
 * 创建供应商
 */
export function createSupplier(data: Partial<Supplier>) {
  return request<ApiResult<Supplier>>({
    url: '/api/v1/admin/inventory/suppliers',
    method: 'POST',
    data,
  })
}

/**
 * 更新供应商
 */
export function updateSupplier(id: number, data: Partial<Supplier>) {
  return request<ApiResult<Supplier>>({
    url: `/api/v1/admin/inventory/suppliers/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除供应商
 */
export function deleteSupplier(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/inventory/suppliers/${id}`,
    method: 'DELETE',
  })
}

// ==================== 采购单 ====================

/**
 * 获取补货建议（低库存 SKU + 建议采购量）
 * @param params.sku_ids 仅生成指定 SKU 的建议（库存工作台勾选补货）；缺省时生成全部低库存建议
 */
export function generatePurchaseOrder(params?: { sku_ids?: number[] }) {
  return request<ApiResult<PurchaseOrderItem[]>>({
    url: '/api/v1/admin/inventory/purchase-order',
    method: 'POST',
    data: params,
  })
}

/**
 * 获取采购单列表
 */
export function getPurchaseOrderList(params: PageParams & { status?: number; supplier_id?: number; keyword?: string }) {
  return request<ApiResult<PageResult<PurchaseOrder>>>({
    url: '/api/v1/admin/inventory/purchase-orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取采购单详情
 */
export function getPurchaseOrderDetail(id: number) {
  return request<ApiResult<PurchaseOrderDetail>>({
    url: `/api/v1/admin/inventory/purchase-orders/${id}`,
    method: 'GET',
  })
}

/**
 * 创建采购单
 */
export function createPurchaseOrder(data: {
  supplier_id: number
  remark?: string
  submit_now?: boolean
  items: Array<{ sku_id: number; quantity: number; purchase_price: number }>
}) {
  return request<ApiResult<PurchaseOrderDetail>>({
    url: '/api/v1/admin/inventory/purchase-orders',
    method: 'POST',
    data,
  })
}

/**
 * 更新采购单（仅草稿）
 */
export function updatePurchaseOrder(
  id: number,
  data: {
    supplier_id: number
    remark?: string
    /** 乐观锁版本号：载入详情时的 updated_at，冲突返回 10007 */
    updated_at?: string
    items: Array<{ sku_id: number; quantity: number; purchase_price: number }>
  },
) {
  return request<ApiResult<PurchaseOrderDetail>>({
    url: `/api/v1/admin/inventory/purchase-orders/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 提交采购单（草稿 → 已下单）
 */
export function submitPurchaseOrder(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/inventory/purchase-orders/${id}/submit`,
    method: 'POST',
  })
}

/**
 * 取消采购单
 */
export function cancelPurchaseOrder(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/inventory/purchase-orders/${id}/cancel`,
    method: 'POST',
  })
}

// ==================== 入库单 ====================

/**
 * 创建入库单（支持直接入库或按采购单入库，创建即生效）
 */
export function createStockInOrder(data: {
  supplier_id?: number
  purchase_order_id?: number
  remark?: string
  /** 幂等业务号：客户端 UUID，同号重复提交不重复入库 */
  biz_no?: string
  items: Array<{
    sku_id: number
    quantity: number
    unit_cost?: number
    batch_no?: string
    production_date?: string
    expire_date?: string
  }>
}) {
  return request<ApiResult<StockInOrderDetail>>({
    url: '/api/v1/admin/inventory/in-orders',
    method: 'POST',
    data,
  })
}

/**
 * 获取入库单列表
 */
export function getStockInOrderList(params: PageParams & { purchase_order_id?: number; start_date?: string; end_date?: string }) {
  return request<ApiResult<PageResult<StockInOrder>>>({
    url: '/api/v1/admin/inventory/in-orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取入库单详情
 */
export function getStockInOrderDetail(id: number) {
  return request<ApiResult<StockInOrderDetail>>({
    url: `/api/v1/admin/inventory/in-orders/${id}`,
    method: 'GET',
  })
}

// ==================== 出库单 ====================

/**
 * 创建出库单（1报损 2领用 3调整）
 */
export function createStockOutOrder(data: {
  type: number
  remark?: string
  /** 幂等业务号：客户端 UUID，同号重复提交不重复出库 */
  biz_no?: string
  items: Array<{ sku_id: number; quantity: number }>
}) {
  return request<ApiResult<StockOutOrderDetail>>({
    url: '/api/v1/admin/inventory/out-orders',
    method: 'POST',
    data,
  })
}

/**
 * 获取出库单列表
 */
export function getStockOutOrderList(params: PageParams & { type?: number; start_date?: string; end_date?: string }) {
  return request<ApiResult<PageResult<StockOutOrder>>>({
    url: '/api/v1/admin/inventory/out-orders',
    method: 'GET',
    params,
  })
}

/**
 * 获取出库单详情
 */
export function getStockOutOrderDetail(id: number) {
  return request<ApiResult<StockOutOrderDetail>>({
    url: `/api/v1/admin/inventory/out-orders/${id}`,
    method: 'GET',
  })
}

// ==================== 报表 ====================

/**
 * 进销存汇总报表（期初 + 入 - 出 = 期末）
 */
export function getInventorySummaryReport(params: PageParams & { start_date?: string; end_date?: string; keyword?: string }) {
  return request<ApiResult<PageResult<InventorySummaryItem>>>({
    url: '/api/v1/admin/inventory/reports/summary',
    method: 'GET',
    params,
  })
}

/**
 * 毛利分析报表
 */
export function getProfitReport(params: PageParams & { start_date?: string; end_date?: string; keyword?: string }) {
  return request<ApiResult<PageResult<ProfitReportItem>>>({
    url: '/api/v1/admin/inventory/reports/profit',
    method: 'GET',
    params,
  })
}

// ==================== 盘点 ====================

/**
 * 获取盘点列表
 */
export function getInventoryCheckList(params: PageParams & { status?: number }) {
  return request<ApiResult<PageResult<InventoryCheck>>>({
    url: '/api/v1/admin/inventory/checks',
    method: 'GET',
    params,
  })
}

/**
 * 获取盘点单详情
 */
export function getInventoryCheckDetail(id: number) {
  return request<ApiResult<StockCheckDetail>>({
    url: `/api/v1/admin/inventory/checks/${id}`,
    method: 'GET',
  })
}

/**
 * 创建盘点单
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
 */
export function submitInventoryCheck(id: number, items: Array<{ sku_id: number; actual_stock: number }>) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/inventory/checks/${id}/submit`,
    method: 'POST',
    data: { items },
  })
}
