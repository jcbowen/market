import type { Component } from 'vue'
import { CashOutline, LogoAlipay, LogoWechat, ScanOutline, WalletOutline } from '@vicons/ionicons5'

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

/** 支付方式：1微信 2支付宝 3货到付款 4现金 5微信付款码 6支付宝付款码（与后端 orderPayTypeText 对齐） */
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
    5: '微信付款码',
    6: '支付宝付款码',
  }
  return map[payType] || `支付方式${payType}`
}

/** 交班统计支付方式展示顺序（现金优先；付款码次之，未在清单中的支付方式排在末尾） */
export const SHIFT_PAY_TYPES: number[] = [4, 1, 2, 5, 6]

/** 支付方式图标定义：图标组件 + 渠道品牌色 */
export interface PayTypeIcon {
  /** 图标组件（ionicons） */
  icon: Component
  /** 图标颜色（微信绿/支付宝蓝与渠道一致，图标形状区分在线支付与付款码） */
  color: string
}

/**
 * 支付方式图标映射（2026-10-25 订单列表图标化）：
 * 渠道维度用颜色区分（微信绿/支付宝蓝/现金橙/货到付款灰），
 * 形式维度用图标区分（logo=在线支付、扫码=付款码、钞票=现金、钱包=货到付款）
 * @param payType 支付方式码
 * @returns 图标定义；未知方式返回 null（调用方仅显示文字）
 */
export function payTypeIcon(payType: number): PayTypeIcon | null {
  const map: Record<number, PayTypeIcon> = {
    [PAY_TYPE.WECHAT]: { icon: LogoWechat, color: '#09bb07' },
    [PAY_TYPE.ALIPAY]: { icon: LogoAlipay, color: '#1677ff' },
    [PAY_TYPE.CASH_ON_DELIVERY]: { icon: WalletOutline, color: '#8a8a8a' },
    [PAY_TYPE.CASH]: { icon: CashOutline, color: '#f0a020' },
    [PAY_TYPE.WECHAT_CODE]: { icon: ScanOutline, color: '#09bb07' },
    [PAY_TYPE.ALIPAY_CODE]: { icon: ScanOutline, color: '#1677ff' },
  }
  return map[payType] ?? null
}

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
 * - 已支付（2）/备货中（3）/配送中（4）/已完成（5）均可退：
 *   到店单的 5 是收款即自动完结的终态；自提/配送单的 5 是确认收货后的终态，
 *   2026-10-25 已完成单售后窗口上线，后台代客退款不受窗口限制（窗口仅约束 C 端自助申请），
 *   由商家自行判断是否给超窗顾客退款
 * @param order 订单（需含状态与配送方式）
 * @returns 是否展示"退款"入口
 */
export function canAdminRefund(order: { status: number; delivery_type?: number }): boolean {
  const { status } = order
  return status === ORDER_STATUS.PAID || status === ORDER_STATUS.STOCKING || status === ORDER_STATUS.SHIPPING || status === ORDER_STATUS.COMPLETED
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

// ---------- 支付单与对账流水（订单详情「支付记录」/时间线 与「支付异常」页共用） ----------

/** 支付单状态：1待支付 2支付成功 3支付失败 4已退款 5已关闭（与 payment 服务 payments.status 对齐） */
export const PAYMENT_STATUS = {
  PENDING: 1,
  SUCCESS: 2,
  FAILED: 3,
  REFUNDED: 4,
  CLOSED: 5,
} as const

/** 支付确认来源：区分「渠道回调正常确认」与「补偿/人工捞回」，非 1 的成功单就是回调丢过的证据 */
export const PAYMENT_CONFIRM_SOURCE = {
  NONE: 0,
  CALLBACK: 1,
  RECONCILE: 2,
  MANUAL: 3,
  OFFLINE: 4,
} as const

/** 流水类别：1渠道回调 2对账补同步 3挂账检出(系统) 4渠道关单 5后台手动同步 6放弃对账 7人工恢复退款收敛 8挂账检出(人工核实) */
export const NOTIFY_KIND = {
  CALLBACK: 1,
  RECONCILE: 2,
  STUCK: 3,
  CHANNEL_CLOSE: 4,
  MANUAL_SYNC: 5,
  GIVE_UP: 6,
  REFUND_RESET: 7,
  MANUAL_STUCK_CHECK: 8,
} as const

/** 流水结果：1确认成功 2幂等忽略 3验签失败 4金额不符 5处理异常 6渠道未支付 7渠道无此交易 8关单结果 9资金挂账 10订单同步失败 11人工恢复退款收敛 */
export const NOTIFY_RESULT = {
  SUCCESS: 1,
  IGNORED: 2,
  VERIFY_FAILED: 3,
  AMOUNT_MISMATCH: 4,
  ERROR: 5,
  NOT_PAID: 6,
  NOT_EXIST: 7,
  CLOSED: 8,
  STUCK: 9,
  ORDER_SYNC_FAILED: 10,
  REFUND_RESET: 11,
} as const

/** 渠道：1微信 2支付宝（支付流水只涉及在线渠道） */
export const PAY_CHANNEL = {
  WECHAT: 1,
  ALIPAY: 2,
} as const

/** 流水处理标记：0 待人工处理（异常页队列） 1 已处理/无需处理 */
export const NOTIFY_HANDLED = {
  PENDING: 0,
  HANDLED: 1,
} as const

/**
 * 支付单状态文案
 * @param status 支付单状态码
 */
export function paymentStatusText(status: number): string {
  const map: Record<number, string> = { 1: '待支付', 2: '支付成功', 3: '支付失败', 4: '已退款', 5: '已关闭' }
  return map[status] || `状态${status}`
}

/**
 * 支付单状态标签颜色
 * @param status 支付单状态码
 */
export function paymentStatusTagType(status: number): TagType {
  const map: Record<number, TagType> = { 1: 'warning', 2: 'success', 3: 'error', 4: 'info', 5: 'default' }
  return map[status] || 'default'
}

/**
 * 支付确认来源文案：异常页与订单详情据此说明「这笔钱是谁确认的」
 * @param source 确认来源码
 */
export function paymentConfirmSourceText(source: number): string {
  const map: Record<number, string> = {
    0: '未确认',
    1: '渠道回调',
    2: '对账补同步',
    3: '后台同步/人工',
    4: '线下收款',
  }
  return map[source] || `来源${source}`
}

/**
 * 支付确认来源标签颜色：非「渠道回调」的成功一律标黄——那代表回调链路出过问题
 * @param source 确认来源码
 */
export function paymentConfirmSourceTagType(source: number): TagType {
  if (source === PAYMENT_CONFIRM_SOURCE.CALLBACK) return 'success'
  if (source === PAYMENT_CONFIRM_SOURCE.OFFLINE) return 'info'
  if (source === PAYMENT_CONFIRM_SOURCE.NONE) return 'default'
  return 'warning'
}

/**
 * 确认依据（payments.confirm_ref）文案：说明这条成功是被谁、在什么场景捞回来的
 * @param ref 后端写入的标记串，空串表示无特殊来源
 */
export function paymentConfirmRefText(ref: string): string {
  if (!ref) return ''
  const map: Record<string, string> = {
    'cancel-guard': '取消前置渠道确认',
    'manual-sync': '后台手动同步',
    'manual-mark': '后台人工标记',
  }
  if (map[ref]) return map[ref]
  // 对账凭据形如 reconcile:ZFxxx，翻译前缀并保留单号便于回溯具体流水
  if (ref.startsWith('reconcile:')) return `对账补同步（${ref.slice('reconcile:'.length)}）`
  return ref
}

/**
 * 流水类别文案
 * @param kind 流水类别码
 */
export function notifyKindText(kind: number): string {
  const map: Record<number, string> = {
    1: '渠道回调',
    2: '对账补同步',
    // 同一挂账事实的两个检出来源（B3）：系统扫出说明回调/关单链路有洞，
    // 人工核实出说明该单早已超出调度窗口——追查漏径的方向不同，页面上必须分开显示
    3: '资金挂账(系统)',
    4: '渠道关单',
    5: '后台同步',
    6: '放弃对账',
    7: '人工恢复退款收敛',
    8: '资金挂账(人工核实)',
  }
  return map[kind] || `类别${kind}`
}

/**
 * 流水结果文案
 * @param result 流水结果码
 */
export function notifyResultText(result: number): string {
  const map: Record<number, string> = {
    1: '确认成功',
    2: '幂等忽略',
    3: '验签/解密失败',
    4: '金额不符',
    5: '处理异常',
    6: '渠道未支付',
    7: '渠道无此交易',
    8: '已关单',
    9: '资金挂账',
    10: '订单同步失败',
    11: '人工恢复退款收敛',
  }
  return map[result] || `结果${result}`
}

/**
 * 流水结果标签颜色：验签失败、金额不符、处理异常、资金挂账、订单同步失败是待办，其余是正常轨迹
 * @param result 流水结果码
 */
export function notifyResultTagType(result: number): TagType {
  const map: Record<number, TagType> = {
    1: 'success',
    2: 'default',
    3: 'error',
    4: 'error',
    5: 'error',
    6: 'warning',
    7: 'info',
    8: 'info',
    9: 'error',
    10: 'error',
    11: 'default',
  }
  return map[result] || 'default'
}

/**
 * 渠道文案（支付流水只涉及微信/支付宝）
 * @param channel 渠道码
 */
export function payChannelText(channel: number): string {
  if (channel === PAY_CHANNEL.WECHAT) return '微信'
  if (channel === PAY_CHANNEL.ALIPAY) return '支付宝'
  return '-'
}

/**
 * 是否重复支付：同一订单成功支付笔数大于 1，说明用户被扣了两次，必须走退款
 * @param summary 订单详情的支付汇总
 */
export function isDuplicatePayment(summary?: { attempts: number; succeeded: number }): boolean {
  return (summary?.succeeded ?? 0) > 1
}
