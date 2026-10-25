/**
 * 订单通用工具
 * C 端订单列表/详情共用的状态判断与操作按钮逻辑
 */

/** 配送方式：社区自提 */
export const DELIVERY_TYPE_PICKUP = 1
/** 配送方式：送货上门 */
export const DELIVERY_TYPE_DELIVERY = 2

/** 支付类型：货到付款 */
export const PAY_TYPE_CASH_ON_DELIVERY = 3

/** 订单来源：收银台代客（门店单，C 端展示但只读） */
export const ORDER_SOURCE_POS = 2

/** 可执行取消/退款操作的订单结构（列表与详情响应均含 status/delivery_type/pay_type/source） */
export interface OrderActionTarget {
  status: number
  delivery_type: number
  pay_type?: number
  /** 订单来源：1=C端用户 2=收银台代客（门店单）；缺省按 C 端单处理 */
  source?: number
}

/**
 * 判断订单是否为门店单（收银台代客下单）
 * 门店单在 C 端展示但只读：取消/退款/支付等操作由门店处理，C 端不提供任何操作按钮
 * @param o 订单
 * @returns 是否门店单
 */
export function isStoreOrder(o: Pick<OrderActionTarget, 'source'>): boolean {
  return o.source === ORDER_SOURCE_POS
}

/**
 * 判断订单是否可取消
 * 待支付(1)恒可取消；货到付款单未实际收款，未发货（2已支付/3备货中）也可直接取消
 * （与后端 order-service CancelOrder 的 cancelableStatuses 口径一致）；
 * 门店单（收银台代客）C 端只读，一律不可取消（后端接口同样拒绝）；
 * 发货后（4 配送中）不可直接取消，走退款/拒收流程
 * @param o 订单
 * @returns 是否可取消
 */
export function canCancel(o: OrderActionTarget): boolean {
  if (isStoreOrder(o)) return false
  if (o.status === 1) return true
  return o.pay_type === PAY_TYPE_CASH_ON_DELIVERY && (o.status === 2 || o.status === 3)
}

/**
 * 判断订单是否可申请退款
 * 已支付及之后（2/3/4）可申请；已完成（5）在售后窗口内可申请（2026-10-25 已完成单售后窗口，
 * 窗口由服务端按订单完成时间校验，前端不预判——超窗提交时由服务端返回明确文案引导联系门店）；
 * 货到付款未发货单（2/3）应直接取消而非退款，不显示退款入口；
 * 门店单（收银台代客）C 端只读，退款由门店处理，不提供退款入口
 * @param o 订单
 * @returns 是否可申请退款
 */
export function canRefund(o: OrderActionTarget): boolean {
  if (isStoreOrder(o)) return false
  if (![2, 3, 4, 5].includes(o.status)) return false
  // 货到付款 + 未发货：走直接取消，不提供退款入口（已完成单已实际收款，正常走退款）
  if (o.pay_type === PAY_TYPE_CASH_ON_DELIVERY && o.status !== 4 && o.status !== 5) return false
  return true
}

/**
 * 判断订单是否可确认收货
 * 与后端 gateway/handler/order.go ConfirmReceive 保持一致：
 * 仅配送中(4)的配送单可确认；自提单已收紧为必须凭取货码到店核销（决策点⑤），
 * C 端不可自行"确认自提"；门店单（收银台代客）C 端只读，由门店/履约人员完成
 * @param o 订单（含 status 与 delivery_type）
 * @returns 是否可确认收货
 */
export function canReceive(o: OrderActionTarget): boolean {
  if (isStoreOrder(o)) return false
  return o.status === 4
}

/**
 * 判断订单是否可删除（C 端软删除）
 * 与后端 order-service DeleteOrder 口径一致：仅终态订单（5已完成 6已取消 8已退款）可删，
 * 且门店单（收银台代客下单，source=2）不可删——门店单据归门店口径管理，
 * 用户侧删掉只会让单据在「我的订单」凭空消失，后续售后也无从发起
 * @param o 订单
 * @returns 是否可删除
 */
export function canDelete(o: OrderActionTarget): boolean {
  if (isStoreOrder(o)) return false
  return [5, 6, 8].includes(o.status)
}

/**
 * 判断订单是否可「再来一单」
 * 与后端 gateway OrderHandler.Reorder 口径一致：仅用户本人的终态订单
 * （已完成 5 / 已取消 6 / 已退款 8）可复购；门店单（收银台代客）C 端只读，不提供入口
 * @param o 订单
 * @returns 是否可再来一单
 */
export function canReorder(o: OrderActionTarget): boolean {
  if (isStoreOrder(o)) return false
  return [5, 6, 8].includes(o.status)
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

/**
 * 退款原因预设列表（申请退款弹窗标签快选项，选中「其他」时补充输入）
 */
export const REFUND_REASONS = [
  '不想要了',
  '拍错了',
  '商品与描述不符',
  '商品质量问题',
  '商品破损或漏发',
  '少发货',
  '发货太慢',
  '其他',
]
