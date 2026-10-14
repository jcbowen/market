/**
 * 订单/售后枚举与展示逻辑集中定义
 * 页面一律引用此处常量与函数，禁止再散落魔法数字
 */

/** 订单状态码：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款 */
export const ORDER_STATUS = {
  PENDING_PAY: 1,
  PAID: 2,
  STOCKING: 3,
  SHIPPING: 4,
  COMPLETED: 5,
  CANCELLED: 6,
  REFUNDING: 7,
  REFUNDED: 8,
} as const

/** 查询聚合虚拟状态（仅列表筛选用，后端 ListOrders 特判）：9=售后(7∪8) 90=待发货(2∪3) */
export const QUERY_STATUS = {
  REFUND_AGGREGATE: 9,
  SHIPPING_AGGREGATE: 90,
} as const

/** 配送方式：1自提 2配送 */
export const DELIVERY_TYPE = {
  SELF_PICKUP: 1,
  DELIVERY: 2,
} as const

/** 退款单状态：1待审核 2已同意 3已拒绝 4退款中 5已退款 */
export const REFUND_STATUS = {
  PENDING: 1,
  APPROVED: 2,
  REJECTED: 3,
  PROCESSING: 4,
  REFUNDED: 5,
} as const

export type TagType = 'default' | 'warning' | 'info' | 'primary' | 'success' | 'error'

/**
 * 订单状态彩色标签类型
 * @param status 订单状态码
 * @returns n-tag 类型
 */
export function orderStatusTagType(status: number): TagType {
  switch (status) {
    case ORDER_STATUS.PENDING_PAY:
      return 'warning'
    case ORDER_STATUS.PAID:
    case ORDER_STATUS.STOCKING:
      return 'info'
    case ORDER_STATUS.SHIPPING:
      return 'primary'
    case ORDER_STATUS.COMPLETED:
      return 'success'
    case ORDER_STATUS.REFUNDING:
    case ORDER_STATUS.REFUNDED:
      return 'error'
    default:
      return 'default'
  }
}

/**
 * 状态码转文案（状态流转日志展示用）
 * @param status 订单状态码
 * @returns 状态中文描述
 */
export function orderStatusText(status: number): string {
  const map: Record<number, string> = {
    0: '创建',
    1: '待支付',
    2: '已支付',
    3: '备货中',
    4: '配送中',
    5: '已完成',
    6: '已取消',
    7: '退款中',
    8: '已退款',
  }
  return map[status] || String(status)
}

/**
 * 配送方式转文案
 * @param deliveryType 配送方式（1 自提 2 配送）
 * @returns 中文描述
 */
export function deliveryTypeText(deliveryType: number): string {
  return deliveryType === DELIVERY_TYPE.DELIVERY ? '配送' : '自提'
}

/**
 * 退款单状态标签（文案 + 颜色）
 */
export const refundStatusMap: Record<number, { text: string; type: 'default' | 'warning' | 'info' | 'success' | 'error' }> = {
  [REFUND_STATUS.PENDING]: { text: '待审核', type: 'warning' },
  [REFUND_STATUS.APPROVED]: { text: '已同意', type: 'info' },
  [REFUND_STATUS.REJECTED]: { text: '已拒绝', type: 'default' },
  [REFUND_STATUS.PROCESSING]: { text: '退款中', type: 'error' },
  [REFUND_STATUS.REFUNDED]: { text: '已退款', type: 'success' },
}
