/**
 * 购物车相关 API
 * 包含购物车获取、添加、更新、删除、全选
 */

import { get, post, put, del } from './request'
import type { CartItem } from './types'

/** 购物车数据 */
export interface CartData {
  items: CartItem[]
  total_amount: number
  total_count: number
  selected_count: number
}

/**
 * 写操作响应数据
 * 服务端所有购物车写接口（加购/更新/删除/全选/清失效）均返回处理后的全量购物车项，
 * 前端直接用该列表回写本地，无需再发 GET /cart（合计金额/件数由前端本地计算）
 */
export interface CartWriteData {
  items: CartItem[]
}

/**
 * 获取当前用户购物车
 * @param communityId 社区 ID（可选）
 * @returns 购物车数据
 */
export function getCart(communityId?: number) {
  return get<CartData>('/cart', { community_id: communityId })
}

/**
 * 添加商品到购物车
 * @param productId 商品 ID
 * @param skuId SKU ID
 * @param quantity 数量
 * @param communityId 社区 ID（可选）
 */
export function addCartItem(productId: number, skuId: number, quantity = 1, communityId?: number) {
  return post<CartWriteData>('/cart/items', {
    product_id: productId,
    sku_id: skuId,
    quantity,
    community_id: communityId,
  })
}

/**
 * 更新购物车商品数量或选中状态
 * @param id 购物车项 ID
 * @param data 更新字段
 */
export function updateCartItem(
  id: number,
  data: { quantity?: number; selected?: boolean },
) {
  return put<CartWriteData>(`/cart/items/${id}`, data)
}

/**
 * 删除购物车商品
 * @param id 购物车项 ID
 */
export function removeCartItem(id: number) {
  return del<CartWriteData>(`/cart/items/${id}`)
}

/**
 * 全选/取消全选购物车商品
 * @param selected 是否选中
 * @param communityId 社区 ID（可选，>0 时仅对该社区批量选中，用于购物车页分组全选）
 */
export function selectAllCartItems(selected: boolean, communityId?: number) {
  return post<CartWriteData>('/cart/select', {
    selected,
    community_id: communityId,
  })
}

/**
 * 清空当前用户购物车中的失效商品
 * @returns 清空后的剩余购物车项列表
 */
export function clearInvalidItems() {
  return del<CartWriteData>('/cart/invalid')
}
