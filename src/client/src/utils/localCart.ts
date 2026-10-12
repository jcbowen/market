/**
 * 本地购物车工具
 * 未登录状态下使用 localStorage 缓存当前社区购物车数据，
 * 登录后可将本地数据同步到服务端。
 */

import type { CartItem, Product, Sku } from '@/api/types'

const LOCAL_CART_KEY = 'local_cart'

/** 本地购物车原始项 */
export interface LocalCartItem {
  product_id: number
  product_name: string
  product_image: string
  sku_id: number
  sku_name: string
  price: number
  quantity: number
  selected: boolean
  stock: number
  min_buy: number
  /** 商品 SKU 总数，>1 表示多规格（可切换规格） */
  sku_total: number
}

/** 本地购物车结构：按社区 ID 分组 */
type LocalCartMap = Record<number, LocalCartItem[]>

/**
 * 读取本地购物车全部数据
 * @returns 按社区 ID 分组的购物车映射
 */
function readCart(): LocalCartMap {
  try {
    const raw = uni.getStorageSync(LOCAL_CART_KEY)
    if (raw) {
      return JSON.parse(raw as string) as LocalCartMap
    }
  } catch {
    // ignore
  }
  return {}
}

/**
 * 写入本地购物车全部数据
 * @param cart 按社区 ID 分组的购物车映射
 */
function writeCart(cart: LocalCartMap) {
  try {
    uni.setStorageSync(LOCAL_CART_KEY, JSON.stringify(cart))
  } catch {
    // ignore
  }
}

/**
 * 获取指定社区的本地购物车列表
 * @param communityId 社区 ID
 * @returns 购物车项数组
 */
export function getLocalCartItems(communityId?: number): LocalCartItem[] {
  if (!communityId) return []
  return readCart()[communityId] || []
}

/**
 * 获取指定社区的本地购物车统计摘要
 * @param communityId 社区 ID
 * @returns totalCount, totalAmount, selectedCount
 */
export function getLocalCartSummary(communityId?: number) {
  const items = getLocalCartItems(communityId)
  let totalCount = 0
  let totalAmount = 0
  let selectedCount = 0
  for (const item of items) {
    totalCount += item.quantity
    totalAmount += item.price * item.quantity
    if (item.selected) {
      selectedCount += item.quantity
    }
  }
  return { items, totalCount, totalAmount, selectedCount }
}

/**
 * 添加商品到本地购物车
 * @param product 商品对象
 * @param sku SKU 对象
 * @param quantity 数量
 * @param communityId 社区 ID
 */
export function addLocalCartItem(
  product: Product,
  sku: Sku,
  quantity: number,
  communityId?: number,
) {
  if (!communityId) return
  const cart = readCart()
  const list = cart[communityId] || []
  const exist = list.find((item) => item.sku_id === sku.id)
  if (exist) {
    exist.quantity += quantity
  } else {
    list.push({
      product_id: product.id,
      product_name: product.name,
      // 图片优先使用 SKU 规格图，与登录后服务端购物车展示口径一致
      product_image: sku.image || product.main_image || '/static/logo.png',
      sku_id: sku.id,
      sku_name: sku.name,
      price: sku.price || product.price,
      quantity,
      selected: true,
      stock: sku.stock,
      min_buy: product.min_buy || 1,
      // 多规格商品（走 SKU 弹窗）携带完整 skus；列表直加商品无 skus 数据按单规格处理
      sku_total: product.skus?.length || 1,
    })
  }
  cart[communityId] = list
  writeCart(cart)
}

/**
 * 更新本地购物车中某个 SKU 的数量
 * @param skuId SKU ID
 * @param quantity 新数量，小于等于 0 时删除该商品
 * @param communityId 社区 ID
 */
export function updateLocalCartItem(skuId: number, quantity: number, communityId?: number) {
  if (!communityId) return
  const cart = readCart()
  let list = cart[communityId] || []
  if (quantity <= 0) {
    list = list.filter((item) => item.sku_id !== skuId)
  } else {
    const item = list.find((i) => i.sku_id === skuId)
    if (item) {
      item.quantity = quantity
    }
  }
  if (list.length) {
    cart[communityId] = list
  } else {
    delete cart[communityId]
  }
  writeCart(cart)
}

/**
 * 删除本地购物车中的某个 SKU
 * @param skuId SKU ID
 * @param communityId 社区 ID
 */
export function removeLocalCartItem(skuId: number, communityId?: number) {
  if (!communityId) return
  const cart = readCart()
  const list = (cart[communityId] || []).filter((item) => item.sku_id !== skuId)
  if (list.length) {
    cart[communityId] = list
  } else {
    delete cart[communityId]
  }
  writeCart(cart)
}

/**
 * 清空本地购物车
 * @param communityId 仅清空指定社区，不传则清空全部
 */
export function clearLocalCart(communityId?: number) {
  if (communityId) {
    const cart = readCart()
    delete cart[communityId]
    writeCart(cart)
  } else {
    try {
      uni.removeStorageSync(LOCAL_CART_KEY)
    } catch {
      // ignore
    }
  }
}

/**
 * 将本地购物车项转换为服务端 CartItem 结构（用于界面展示）
 * @param communityId 社区 ID
 * @returns CartItem 数组
 */
export function localCartToCartItems(communityId?: number): CartItem[] {
  return getLocalCartItems(communityId).map((item, index) => ({
    id: -(index + 1),
    product_id: item.product_id,
    product_name: item.product_name,
    sku_id: item.sku_id,
    spec_name: item.sku_name,
    price: item.price,
    quantity: item.quantity,
    selected: item.selected,
    product_image: item.product_image,
    stock: item.stock,
    community_id: communityId || 0,
    min_buy: item.min_buy,
    sku_total: item.sku_total || 1,
    // 本地购物车数据实时，不做失效判定（登录后以服务端为准）
    invalid: false,
    invalid_reason: '',
    stock_hint: '',
  }))
}

/**
 * 获取所有本地购物车商品（用于登录后批量同步到服务端）
 * @returns 按社区分组的所有本地购物车项
 */
export function getAllLocalCartItems(): LocalCartMap {
  return readCart()
}
