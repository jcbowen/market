/**
 * 订单通用工具
 * C 端订单列表/详情共用的状态判断与操作按钮逻辑
 */

/** 配送方式：社区自提 */
export const DELIVERY_TYPE_PICKUP = 1
/** 配送方式：送货上门 */
export const DELIVERY_TYPE_DELIVERY = 2

/** 可执行确认收货的订单结构（列表与详情响应均含 status/delivery_type） */
export interface OrderActionTarget {
  status: number
  delivery_type: number
}

/**
 * 判断订单是否可确认收货
 * 与后端 gateway/handler/order.go ConfirmReceive 的 canReceive 保持一致：
 * 配送单在配送中(4)可确认；自提单支付后(2)即可确认自提
 * @param o 订单（含 status 与 delivery_type）
 * @returns 是否可确认收货/自提
 */
export function canReceive(o: OrderActionTarget): boolean {
  return o.status === 4 || (o.status === 2 && o.delivery_type === DELIVERY_TYPE_PICKUP)
}

/**
 * 确认收货按钮文案：自提单为"确认自提"，配送单为"确认收货"
 * @param o 订单
 * @returns 按钮文案
 */
export function receiveButtonText(o: OrderActionTarget): string {
  return o.delivery_type === DELIVERY_TYPE_PICKUP ? '确认自提' : '确认收货'
}

/**
 * 订单取消原因预设列表（取消确认弹窗单选项）
 */
export const CANCEL_REASONS = ['不想要了', '买错了', '地址填错了', '商品缺货', '其他']
