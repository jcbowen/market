/**
 * 商品相关 API
 * 包含分类、商品列表、商品详情、搜索
 */

import { get } from './request'
import type { PageData } from './request'
import type { Banner, Category, FullReduction, Product, Sku, Spec } from './types'

/** 后端规格值原始字段 */
interface RawSpecValue {
  id?: number
  name?: string
  sort?: number
}

/** 后端规格原始字段 */
interface RawSpec {
  id?: number
  name?: string
  sort?: number
  values?: RawSpecValue[]
}

/** 后端 SKU 原始字段 */
interface RawSku {
  id?: number
  product_id?: number
  spec_name?: string
  spec_json?: string
  spec_value_ids?: string
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
  /** 商品富文本详情（服务端消毒后 HTML） */
  content?: string
  /** 后端返回的是 sales_count（累计真实销量） */
  sales_count?: number
  sales?: number
  /** C 端是否显示销量 */
  show_sales?: boolean
  /** 虚拟销量（展示口径 = 真实销量 + 虚拟销量） */
  virtual_sales?: number
  is_multi_spec?: boolean
  min_buy?: number
  skus?: RawSku[]
  specs?: RawSpec[]
}

/**
 * 将后端规格字段映射为前端 Spec 类型
 * @param spec 后端规格对象
 * @returns 前端规格对象
 */
function normalizeSpec(spec: RawSpec): Spec {
  return {
    id: spec.id || 0,
    name: spec.name || '',
    sort: spec.sort || 0,
    values: (spec.values || []).map((v) => ({
      id: v.id || 0,
      name: v.name || '',
      sort: v.sort || 0,
    })),
  }
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
    spec_name: sku.spec_name || '',
    spec_json: sku.spec_json || '{}',
    spec_value_ids: sku.spec_value_ids || '',
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
    content: p.content || '',
    // 展示口径：真实销量（sales_count）+ 虚拟销量；后端历史字段名为 sales，做兼容
    sales: (p.sales_count ?? p.sales ?? 0) + (p.virtual_sales ?? 0),
    show_sales: p.show_sales !== false,
    is_multi_spec: p.is_multi_spec || false,
    min_buy: p.min_buy || 1,
    skus: (p.skus || []).map(normalizeSku),
    specs: (p.specs || []).map(normalizeSpec),
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
 * @param communityId 当前浏览社区 ID（必填且 >0：公开路由不解析令牌语境，
 *   缺参时网关按参数错误拒绝，避免跨社区读取价目与正文；无社区时先用 ensureCommunity 兜底）
 */
export function getProductDetail(id: number, communityId?: number) {
  return get<{ product: RawProduct; skus: RawSku[]; specs?: RawSpec[] }>(`/products/${id}`, { community_id: communityId }).then((res) => {
    const raw = res.data?.product || {}
    raw.skus = res.data?.skus || []
    raw.specs = res.data?.specs || []
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
 * 条形码精确查询（扫码直达）
 * @param code 条形码
 * @param communityId 社区 ID
 * @returns 命中的商品与 SKU（未命中时请求层抛出业务错误）
 */
export function getProductByBarcode(code: string, communityId?: number) {
  return get<{ product: RawProduct; sku?: RawSku }>(`/products/barcode/${encodeURIComponent(code)}`, {
    community_id: communityId,
  }).then((res) => {
    const product = res.data?.product || {}
    const skuRaw = res.data?.sku || {}
    product.skus = skuRaw.id ? [skuRaw] : []
    return {
      code: res.code,
      message: res.message,
      data: {
        product: normalizeProduct(product),
        sku: normalizeSku(skuRaw),
      },
    }
  })
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

/** 免配送费排除范围项 */
export interface FreeShippingScopeInfo {
  scope_type: number
  category_id: number
  category_name: string
  product_id: number
  product_name: string
}

/** 正在生效的免配送费活动 */
export interface FreeShippingActivity {
  id: number
  community_id: number
  name: string
  /** 免配送费门槛（参与商品额） */
  threshold: number
  start_time: string
  end_time: string
  status: number
  scopes: FreeShippingScopeInfo[]
}

/**
 * 获取当前社区正在生效的免配送费活动（无生效活动返回 null）
 * 商品详情页据此展示「参与满额免配送费 / 不参与」角标
 * @param params 社区 ID
 */
export function getActiveFreeShipping(params?: { community_id?: number }) {
  return get<FreeShippingActivity | null>('/free-shippings/active', params as Record<string, unknown>)
}
