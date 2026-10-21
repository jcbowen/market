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

/** 配送方式：1自提 2配送 3到店（到店仅收银台可用，即买即走） */
export const DELIVERY_TYPE = {
  SELF_PICKUP: 1,
  DELIVERY: 2,
  STORE: 3,
} as const

/** 订单来源：1=C端用户自下单 2=后台收银台代客（决策点⑨） */
export const ORDER_SOURCE = {
  USER: 1,
  POS: 2,
} as const

/** 支付方式：1微信 2支付宝 3货到付款 4现金 5/6 付款码预留（与后端 orderPayTypeText 对齐） */
export const PAY_TYPE = {
  WECHAT: 1,
  ALIPAY: 2,
  CASH_ON_DELIVERY: 3,
  CASH: 4,
  WECHAT_CODE: 5,
  ALIPAY_CODE: 6,
} as const

/**
 * 支付方式转文案（与后端 orderPayTypeText 对齐）
 * @param payType 支付方式码
 * @returns 中文描述
 */
export function payTypeText(payType: number): string {
  const map: Record<number, string> = {
    1: '微信',
    2: '支付宝',
    3: '货到付款',
    4: '现金',
    5: '微信付款码(预留)',
    6: '支付宝付款码(预留)',
  }
  return map[payType] || `支付方式${payType}`
}

/** 交班统计支付方式展示顺序（现金优先；5/6 为付款码预留位，未在清单中的支付方式排在末尾） */
export const SHIFT_PAY_TYPES: number[] = [4, 1, 2, 5, 6]

/**
 * 交班统计行：仅渲染后端实际返回的支付方式，按 SHIFT_PAY_TYPES 顺序排序
 * （无数据的渠道不显示占位行；未列入顺序清单的支付方式按原顺序排在末尾）
 * @param items 后端按支付方式分组的统计项（收款或退款）
 * @returns 按展示顺序排列的统计行
 */
export function payStatRows(
  items: Array<{ pay_type: number; amount: number; order_count: number }> | undefined,
): Array<{ pay_type: number; amount: number; order_count: number }> {
  const rank = (payType: number): number => {
    const idx = SHIFT_PAY_TYPES.indexOf(payType)
    return idx === -1 ? SHIFT_PAY_TYPES.length : idx
  }
  return [...(items ?? [])].sort((a, b) => rank(a.pay_type) - rank(b.pay_type))
}

/** 来源筛选下拉项（0=全部） */
export const orderSourceFilterOptions = [
  { label: '全部来源', value: 0 },
  { label: 'C端下单', value: ORDER_SOURCE.USER },
  { label: '收银台', value: ORDER_SOURCE.POS },
]

/**
 * 订单来源转文案
 * @param source 来源码
 * @returns 中文描述
 */
export function orderSourceText(source: number): string {
  return source === ORDER_SOURCE.POS ? '收银台' : 'C端'
}

/**
 * 订单来源标签类型
 * @param source 来源码
 * @returns n-tag 类型
 */
export function orderSourceTagType(source: number): TagType {
  return source === ORDER_SOURCE.POS ? 'warning' : 'default'
}

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
 * 判断订单是否允许后台取消（与后端 cancelableStatuses 口径一致）
 * - 待支付（status=1）恒可取消
 * - 货到付款单未实际收款，未发货（2已支付/3备货中）可直接取消，无需走退款流程
 * - 门店单（source=2）仅 C 端限制取消，后台取消与普通单同口径，不做额外限制
 * @param order 订单（需含状态与支付方式）
 * @returns 是否展示"取消订单"入口
 */
export function canCancelOrder(order: { status: number; pay_type?: number }): boolean {
  if (order.status === ORDER_STATUS.PENDING_PAY) return true
  const unshipped = order.status === ORDER_STATUS.PAID || order.status === ORDER_STATUS.STOCKING
  return order.pay_type === PAY_TYPE.CASH_ON_DELIVERY && unshipped
}

/**
 * 判断订单是否允许后台代客退款（与后端 refund.go 的 adminRefundable 口径一致）
 * - 已支付（2）/备货中（3）/配送中（4）恒可退
 * - 到店单（delivery_type=3）收款即在事务内自动完结为已完成（5），这个 5 是自动终态
 *   而非用户确认收货，故放行；否则门店售出的订单永远退不了款
 * - 自提/配送的已完成单不在此列（用户已确认收货，售后口径另行设计）
 * @param order 订单（需含状态与配送方式）
 * @returns 是否展示"退款"入口
 */
export function canAdminRefund(order: { status: number; delivery_type?: number }): boolean {
  const { status } = order
  if (status === ORDER_STATUS.PAID || status === ORDER_STATUS.STOCKING || status === ORDER_STATUS.SHIPPING) {
    return true
  }
  return status === ORDER_STATUS.COMPLETED && order.delivery_type === DELIVERY_TYPE.STORE
}

/**
 * 配送方式转文案
 * @param deliveryType 配送方式（1 自提 2 配送 3 到店）
 * @returns 中文描述
 */
export function deliveryTypeText(deliveryType: number): string {
  if (deliveryType === DELIVERY_TYPE.DELIVERY) return '配送'
  if (deliveryType === DELIVERY_TYPE.STORE) return '到店'
  return '自提'
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

/**
 * 退款单状态文案
 * @param status 退款单状态
 */
export function refundStatusText(status: number): string {
  return refundStatusMap[status]?.text || '未知'
}

/**
 * 退款单状态标签颜色类型
 * @param status 退款单状态
 */
export function refundStatusTagType(status: number): 'default' | 'warning' | 'info' | 'success' | 'error' {
  return refundStatusMap[status]?.type || 'default'
}

/**
 * 退款类型文案（1 整单退 2 部分退 3 仅金额退）
 * @param type 退款类型
 */
export function refundTypeText(type: number): string {
  if (type === 1) return '整单退'
  if (type === 3) return '金额退'
  return '部分退'
}

/**
 * 退款类型标签颜色类型
 * @param type 退款类型
 */
export function refundTypeTagType(type: number): 'default' | 'warning' | 'info' | 'success' | 'error' {
  if (type === 1) return 'info'
  if (type === 3) return 'success'
  return 'warning'
}
