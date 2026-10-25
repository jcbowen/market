/**
 * 订单相关 API
 * 包含创建订单、列表、详情、取消、申请退款
 */

import { get, post, put, del } from './request'
import type { PageData } from './request'
import type { Order, Address } from './types'

/** 创建订单参数 */
export interface CreateOrderParams {
  /** 配送方式：1 社区自提 2 送货上门 */
  delivery_type: 1 | 2
  /** 收货地址 ID；自提单传 0 */
  address_id: number
  /** 期望配送时间（可选） */
  delivery_time?: string
  /** 下单社区：购物车跨社区结算时传结算社区，不传按用户当前社区 */
  community_id?: number
  items: { product_id: number; sku_id: number; quantity: number }[]
  coupon_id?: number
  remark?: string
  /** 结算来源的购物车项 ID（下单成功后前端据此清理购物车） */
  cart_item_ids?: number[]
}

/**
 * 创建订单
 * @param data 订单参数
 */
export function createOrder(data: CreateOrderParams) {
  return post<Order>('/orders', data as unknown as Record<string, unknown>)
}

/**
 * 获取订单列表
 * @param params 状态、分页、社区等参数
 */
export function getOrders(params?: {
  status?: number
  page?: number
  page_size?: number
  community_id?: number
}) {
  return get<PageData<Order>>('/orders', params as Record<string, unknown>)
}

/**
 * 获取订单详情
 * @param id 订单 ID
 */
export function getOrderDetail(id: number) {
  return get<Order>(`/orders/${id}`)
}

/** 用户订单状态角标统计（口径与订单列表 Tab 一致） */
export interface OrderStatusCounts {
  /** 待支付（status=1） */
  pending_pay: number
  /** 待发货（status=2/3 聚合） */
  pending_ship: number
  /** 待收货（status=4） */
  pending_receive: number
  /** 退款/售后（status=7/8 聚合） */
  after_sale: number
}

/**
 * 获取当前用户订单状态角标统计（「我的-我的订单」红标）
 */
export function getOrderStatusCounts() {
  return get<OrderStatusCounts>('/orders/status-counts')
}

/**
 * 取消订单
 * @param id 订单 ID
 * @param reason 取消原因（可选）
 */
export function cancelOrder(id: number, reason?: string) {
  return put<Order>(`/orders/${id}/cancel`, { reason } as Record<string, unknown>)
}

/**
 * 删除订单（软删除，仅终态订单可删）
 * 删除后 C 端列表与角标统计不可见；详情链路（售后进度/支付详情）保留可达
 * @param id 订单 ID
 */
export function deleteOrder(id: number) {
  return del<{ id: number }>(`/orders/${id}`)
}

/** 申请退款参数 */
export interface ApplyRefundParams {
  /** 退款原因 */
  reason: string
  /** 退款商品明细；为空表示整单退 */
  items?: { order_item_id: number; quantity: number }[]
  /** 手动调低后的退款金额（仅允许低于服务端汇算值，不传按汇算值全额退） */
  amount?: number
  /** 运费退还策略为 user_decide 时用户是否选择退运费 */
  refund_freight?: boolean
}

/**
 * 申请退款
 * @param id 订单 ID
 * @param data 退款参数
 */
export function applyRefund(id: number, data: ApplyRefundParams) {
  return post(`/orders/${id}/refund`, data as unknown as Record<string, unknown>)
}

/** 退款进度商品明细项 */
export interface RefundProgressItem {
  product_name: string
  spec_name: string
  quantity: number
  refund_amount: number
}

/** 退款进度记录（订单详情「退款进度」弹窗展示用） */
export interface RefundProgress {
  id: number
  refund_no: string
  /** 退款金额（商品 + 运费，服务端汇算） */
  amount: number
  /** 本单退还的运费金额 */
  freight_amount: number
  /** 退款类型：1整单退 2部分退 3仅金额退 */
  type: number
  /** 退款原因 */
  reason: string
  /** 退款状态：1待审核 2已同意 3已拒绝 4退款中 5已退款 */
  status: number
  /** 拒绝原因（仅已拒绝时有值） */
  reject_reason: string
  /** 退款成功时间（到账时间） */
  refund_time: string
  created_at: string
  items: RefundProgressItem[]
}

/**
 * 查询订单退款进度
 * @param id 订单 ID
 */
export function getOrderRefunds(id: number) {
  return get<{ refunds: RefundProgress[] }>(`/orders/${id}/refunds`)
}

/**
 * 获取退款配置（运费退还策略 + 已完成单售后窗口）
 * @returns refund_freight_policy: always/never/user_decide；completed_refund_hours: 完成后 N 小时内可自助申请售后，0=关闭
 */
export function getRefundConfig() {
  return get<{ refund_freight_policy: string; completed_refund_hours?: number }>('/refund/config')
}

/**
 * 确认收货
 * @param id 订单 ID
 */
export function confirmReceive(id: number) {
  return post<Order>(`/orders/${id}/receive`)
}

/** 常买商品项（购物车"常买商品"列表） */
export interface FrequentProduct {
  product_id: number
  product_name: string
  product_image: string
  /** 该用户最常买的 SKU（一键加购用） */
  sku_id: number
  /** 购买次数（有效订单数） */
  purchase_count: number
  /** 累计购买件数 */
  total_quantity: number
}

/**
 * 获取当前用户常买商品
 * 统计口径：仅计有效订单（已支付及之后），按购买次数降序
 * @param limit 返回条数，默认 10
 */
export function getFrequentProducts(limit = 10) {
  return get<{ list: FrequentProduct[] }>('/orders/frequent-products', { limit })
}

/** 免配送费试算结果 */
export interface FreeShippingPreview {
  /** 是否已满足免配送费 */
  free_shipping: boolean
  /** 命中的活动名 */
  activity_name: string
  /** 免配送费门槛 */
  threshold: number
  /** 参与免配送费的商品金额合计 */
  eligible_amount: number
  /** 距门槛差额（已满足时为 0） */
  remain_amount: number
  /** 被排除商品/分类的展示名清单 */
  excluded_names: string[]
  /** 基础配送费（社区配置值） */
  delivery_fee: number
}

/**
 * 免配送费试算（订单确认页展示）
 * 按结算商品清单计算免配送费活动命中情况
 * @param items 结算商品（sku_id + quantity）
 * @param communityId 试算社区（购物车跨社区结算时传结算社区，不传按用户当前社区）
 */
export function previewFreeShipping(
  items: { sku_id: number; quantity: number }[],
  communityId?: number,
) {
  return post<FreeShippingPreview>('/orders/free-shipping/preview', { items, community_id: communityId })
}

/** 再来一单时未能加入购物车的商品项 */
export interface ReorderSkippedItem {
  product_name: string
  spec_name: string
  /** 跳过原因（如"商品已下架"、"规格已失效"） */
  reason: string
}

/** 再来一单结果 */
export interface ReorderResult {
  /** 成功加入购物车的商品项数 */
  added: number
  /** 未能加入购物车的商品及原因 */
  skipped: ReorderSkippedItem[]
}

/**
 * 再来一单：把订单内商品按原数量重新加入订单所属社区的购物车
 * 仅用户本人的终态订单（已完成5/已取消6/已退款8）可用，门店单不可用
 * @param id 订单 ID
 */
export function reorderOrder(id: number) {
  return post<ReorderResult>(`/orders/${id}/reorder`)
}
