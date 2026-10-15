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
    5: '余额',
  }
  return map[payType] || `支付方式${payType}`
}

/** 交班统计固定展示的支付方式顺序（现金优先；余额 pay_type=5 预留，渠道未接入前恒为 0） */
export const SHIFT_PAY_TYPES: number[] = [4, 1, 2, 5]

/**
 * 交班统计固定行：按 现金/微信/支付宝/余额 顺序补全分组数据
 * （后端仅返回有数据的支付方式，此处补齐固定四项，缺省数额为 0）
 * @param items 后端按支付方式分组的统计项（收款或退款）
 * @returns 固定四项的统计行
 */
export function payStatRows(
  items: Array<{ pay_type: number; amount: number; order_count: number }> | undefined,
): Array<{ pay_type: number; amount: number; order_count: number }> {
  const map = new Map((items ?? []).map((item) => [item.pay_type, item]))
  return SHIFT_PAY_TYPES.map((payType) => {
    const hit = map.get(payType)
    return { pay_type: payType, amount: hit?.amount ?? 0, order_count: hit?.order_count ?? 0 }
  })
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
