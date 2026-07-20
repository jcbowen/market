import { request } from './request'
import type { ApiResult, Category, PageParams, PageResult, Product, Sku } from './types'

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

// 商品

/**
 * 获取商品列表
 * @param params 查询参数
 * @returns 分页商品列表
 */
export function getProductList(params: PageParams & { keyword?: string; category_id?: number; status?: number }) {
  return request<ApiResult<PageResult<Product>>>({
    url: '/api/v1/admin/products',
    method: 'GET',
    params,
  })
}

/**
 * 获取商品详情
 * @param id 商品 ID
 * @returns 商品详情
 */
export function getProductDetail(id: number) {
  return request<ApiResult<Product>>({
    url: `/api/v1/admin/products/${id}`,
    method: 'GET',
  })
}

/**
 * 创建商品
 * @param data 商品数据
 * @returns 创建结果
 */
export function createProduct(data: Partial<Product>) {
  return request<ApiResult<Product>>({
    url: '/api/v1/admin/products',
    method: 'POST',
    data,
  })
}

/**
 * 更新商品
 * @param id 商品 ID
 * @param data 商品数据
 * @returns 更新结果
 */
export function updateProduct(id: number, data: Partial<Product>) {
  return request<ApiResult<Product>>({
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
    method: 'PUT',
    data: { status },
  })
}

/**
 * 获取商品 SKU 列表
 * @param productId 商品 ID
 * @returns SKU 列表
 */
export function getSkuList(productId: number) {
  return request<ApiResult<Sku[]>>({
    url: `/api/v1/admin/products/${productId}/skus`,
    method: 'GET',
  })
}
