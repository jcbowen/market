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
  return post<CartItem>('/cart/items', {
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
  return put<CartItem>(`/cart/items/${id}`, data)
}

/**
 * 删除购物车商品
 * @param id 购物车项 ID
 */
export function removeCartItem(id: number) {
  return del(`/cart/items/${id}`)
}

/**
 * 全选/取消全选购物车商品
 * @param selected 是否选中
 */
export function selectAllCartItems(selected: boolean) {
  return post('/cart/select', { selected })
}
