import { request } from './request'
import type {
  ApiResult,
  Category,
  PageParams,
  PageResult,
  Product,
  ProductDetail,
  ProductSavePayload,
  Sku,
} from './types'

/**
 * 商品管理相关 API
 */

// 分类

/**
 * 获取分类列表
 * @returns 分类列表
 */
export function getCategoryList() {
  return request<ApiResult<Category[]>>({
    url: '/api/v1/admin/categories',
    method: 'GET',
  })
}

/**
 * 创建分类
 * @param data 分类数据
 * @returns 创建结果
 */
export function createCategory(data: Partial<Category>) {
  return request<ApiResult<Category>>({
    url: '/api/v1/admin/categories',
    method: 'POST',
    data,
  })
}

/**
 * 更新分类
 * @param id 分类 ID
 * @param data 分类数据
 * @returns 更新结果
 */
export function updateCategory(id: number, data: Partial<Category>) {
  return request<ApiResult<Category>>({
    url: `/api/v1/admin/categories/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除分类
 * @param id 分类 ID
 * @returns 删除结果
 */
export function deleteCategory(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/categories/${id}`,
    method: 'DELETE',
  })
}

/**
 * 分类批量排序（拖拽后按显示顺序整表提交，后端事务内逐项校验归属）
 * @param items 全量有序列表：id + 目标 sort（建议按显示顺序 ×10 留空隙）
 * @returns 更新结果
 */
export function batchSortCategories(items: Array<{ id: number; sort: number }>) {
  return request<ApiResult<null>>({
    url: '/api/v1/admin/categories/sort',
    method: 'POST',
    data: { items },
  })
}

// 商品

/**
 * 获取商品列表
 * @param params 查询参数（status=上架精确过滤，供选品/收银台用；status_filter=Tab 复合状态，供商品列表用）
 * @returns 分页商品列表
 */
export function getProductList(params: PageParams & { keyword?: string; category_id?: number; status?: number; status_filter?: number }) {
  return request<ApiResult<PageResult<Product>>>({
    url: '/api/v1/admin/products',
    method: 'GET',
    params,
  })
}

/**
 * 商品状态统计（口径与列表筛选条件一致，供 Tab 计数展示）
 * @param params 筛选参数（与列表共用 keyword/category_id）
 * @returns 各状态计数：total 全部 / on_sale 出售中 / sold_out 已售罄 / warehouse 仓库中 / low_stock 库存紧张（出售中的子集维度）
 */
export function getProductStatusStats(params: { keyword?: string; category_id?: number }) {
  return request<ApiResult<{ total: number; on_sale: number; sold_out: number; warehouse: number; low_stock: number }>>({
    url: '/api/v1/admin/products/status-stats',
    method: 'GET',
    params,
  })
}

/**
 * 获取商品详情（含 SKU 列表与规格树）
 * @param id 商品 ID
 * @returns 商品详情
 */
export function getProductDetail(id: number) {
  return request<ApiResult<ProductDetail>>({
    url: `/api/v1/admin/products/${id}`,
    method: 'GET',
  })
}

/**
 * 创建商品
 * @param data 商品数据（含规格树与 SKU 列表）
 * @returns 创建结果
 */
export function createProduct(data: ProductSavePayload) {
  return request<ApiResult<ProductDetail>>({
    url: '/api/v1/admin/products',
    method: 'POST',
    data,
  })
}

/**
 * 更新商品
 * @param id 商品 ID
 * @param data 商品数据（含规格树与 SKU 列表，全量提交）
 * @returns 更新结果
 */
export function updateProduct(id: number, data: ProductSavePayload) {
  return request<ApiResult<ProductDetail>>({
    url: `/api/v1/admin/products/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除商品
 * @param id 商品 ID
 * @returns 删除结果
 */
export function deleteProduct(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/products/${id}`,
    method: 'DELETE',
  })
}

/**
 * 更新商品上下架状态
 * @param id 商品 ID
 * @param status 状态：1 上架，0 下架
 * @returns 更新结果
 */
export function updateProductStatus(id: number, status: number) {
  return request<ApiResult<Product>>({
    url: `/api/v1/admin/products/${id}/status`,
    method: 'POST',
    data: { status },
  })
}

/**
 * 批量上下架商品（列表多选操作，后端不触碰已删除商品）
 * @param ids 商品 ID 列表
 * @param status 目标状态：1 上架，0 下架
 * @returns 更新结果
 */
export function batchUpdateProductStatus(ids: number[], status: number) {
  return request<ApiResult<null>>({
    url: '/api/v1/admin/products/batch-status',
    method: 'POST',
    data: { ids, status },
  })
}

/**
 * 行内更新商品名称（轻量接口，仅改 name，不影响规格/SKU）
 * @param id 商品 ID
 * @param name 新名称
 * @returns 更新结果
 */
export function updateProductName(id: number, name: string) {
  return request<ApiResult<{ id: number; name: string }>>({
    url: `/api/v1/admin/products/${id}/name`,
    method: 'PUT',
    data: { name },
  })
}

/**
 * 条形码精确查询（收银台扫码直达）
 * @param code 条形码
 * @returns 命中的商品与 SKU；未命中返回 404 业务错误
 */
export function getProductByBarcode(code: string) {
  return request<ApiResult<{ product: Product; sku: Sku }>>({
    url: `/api/v1/admin/products/barcode/${encodeURIComponent(code)}`,
    method: 'GET',
  })
}
