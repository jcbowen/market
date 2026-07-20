/**
 * 商品相关 API
 * 包含分类、商品列表、商品详情、搜索
 */

import { get } from './request'
import type { PageData } from './request'
import type { Banner, Category, FullReduction, Product, Sku } from './types'

/** 后端 SKU 原始字段 */
interface RawSku {
  id?: number
  product_id?: number
  spec_name?: string
  price?: number
  original_price?: number
  stock?: number
  image?: string
}

/** 后端商品原始字段 */
interface RawProduct {
  id?: number
  name?: string
  cover_image?: string
  images?: string[]
  price?: number
  original_price?: number
  stock?: number
  category_id?: number
  category_name?: string
  description?: string
  sales?: number
  is_multi_spec?: boolean
  min_buy?: number
  skus?: RawSku[]
}

/**
 * 将后端 SKU 字段映射为前端 Sku 类型
 * @param sku 后端 SKU 对象
 * @returns 前端 SKU 对象
 */
function normalizeSku(sku: RawSku): Sku {
  return {
    id: sku.id || 0,
    product_id: sku.product_id || 0,
    name: sku.spec_name || '',
    price: sku.price || 0,
    original_price: sku.original_price || 0,
    stock: sku.stock || 0,
    image: sku.image || '',
  }
}

/**
 * 将后端商品字段映射为前端 Product 类型
 * @param p 后端商品对象
 * @returns 前端商品对象
 */
function normalizeProduct(p: RawProduct): Product {
  return {
    id: p.id || 0,
    name: p.name || '',
    main_image: p.cover_image || '',
    images: p.images || [],
    price: p.price || 0,
    original_price: p.original_price || 0,
    stock: p.stock || 0,
    category_id: p.category_id || 0,
    category_name: p.category_name || '',
    description: p.description || '',
    sales: p.sales || 0,
    is_multi_spec: p.is_multi_spec || false,
    min_buy: p.min_buy || 1,
    skus: (p.skus || []).map(normalizeSku),
  }
}

/**
 * 获取商品分类列表
 * @param params 查询参数，如 parent_id
 */
export function getCategories(params?: { parent_id?: number; community_id?: number }) {
  return get<Category[]>('/categories', params as Record<string, unknown>)
}

/**
 * 获取商品列表
 * @param params 分页、分类、关键词等参数
 */
export function getProducts(params?: {
  page?: number
  page_size?: number
  category_id?: number
  keyword?: string
  community_id?: number
}) {
  return get<PageData<RawProduct>>('/products', params as Record<string, unknown>).then((res) => {
    if (res.data && res.data.list) {
      res.data.list = res.data.list.map(normalizeProduct)
    }
    return res as unknown as { code: number; message: string; data: PageData<Product> }
  })
}

/**
 * 获取商品详情
 * @param id 商品 ID
 * @param communityId 社区 ID（可选）
 */
export function getProductDetail(id: number, communityId?: number) {
  return get<{ product: RawProduct; skus: RawSku[] }>(`/products/${id}`, { community_id: communityId }).then((res) => {
    const raw = res.data?.product || {}
    raw.skus = res.data?.skus || []
    return {
      code: res.code,
      message: res.message,
      data: normalizeProduct(raw),
    }
  })
}

/**
 * 根据 SKU ID 查询 SKU 信息
 * @param id SKU ID
 */
export function getSkuById(id: number) {
  return get<RawSku>(`/skus/${id}`).then((res) => {
    return {
      code: res.code,
      message: res.message,
      data: normalizeSku(res.data || {}),
    }
  })
}

/**
 * 搜索商品（基于商品列表的关键词查询）
 * @param params 搜索参数，包含关键词、分页、社区 ID 等
 */
export function searchProducts(params: {
  keyword: string
  page?: number
  page_size?: number
  community_id?: number
}) {
  return getProducts(params)
}

/**
 * 获取 Banner 列表
 * @param params 社区 ID、展示位置等参数
 */
export function getBanners(params?: { community_id?: number; position?: number }) {
  return get<Banner[]>('/banners', params as Record<string, unknown>)
}

/**
 * 获取满减活动列表
 * @param params 社区 ID 等参数
 */
export function getFullReductions(params?: { community_id?: number }) {
  return get<FullReduction[]>('/full-reductions', params as Record<string, unknown>)
}
