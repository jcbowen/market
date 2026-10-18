import { request } from './request'
import type {
  ApiResult,
  AppPreferences,
  Order,
  OrderDetail,
  PageParams,
  PageResult,
  PosHeldOrder,
  PosShift,
  PosShiftPage,
  Refund,
} from './types'

/**
 * 订单管理相关 API
 */

/**
 * 获取订单列表
 * @param params 查询参数（order_no/receiver_name/receiver_phone 模糊；start_time/end_time 下单日期 YYYY-MM-DD）
 * @returns 分页订单列表
 */
export function getOrderList(
  params: PageParams & {
    order_no?: string
    status?: number
    start_time?: string
    end_time?: string
    receiver_name?: string
    receiver_phone?: string
    /** 按会员过滤（会员列表弹窗查订单） */
    user_id?: number
    /** 按收银班次过滤（交班记录「查看班内订单」入口） */
    shift_id?: number
    /** 来源过滤：1=C端 2=收银台（0=全部） */
    source?: number
    /** 按取货码精确过滤（收银台核销/订单 Tab 扫码查询） */
    pickup_code?: string
    /** 目标社区：仅超管可传，0=跨社区查全部；不传=当前社区语境 */
    community_id?: number
  },
) {
  return request<ApiResult<PageResult<Order>>>({
    url: '/api/v1/admin/orders',
    method: 'GET',
    params,
  })
}

// --- 收银台（G） ---

/**
 * 收银台代客下单（按所选支付方式当场结清）
 * @param data 下单参数（user_id=0 为散客；配送单必须带联系人/电话/地址；pay_type 支付方式，缺省现金 4；
 *              items[].price 单品改价（>0 以该价成交）；manual_discount 整单优惠（改价/抹零折算，不超过应付）
 * @returns 订单信息（含取货码，自提单）
 */
export function createPosOrder(data: {
  user_id: number
  delivery_type: number
  contact_name?: string
  contact_phone?: string
  contact_address?: string
  remark?: string
  coupon_id?: number
  pay_type?: number
  /** 整单手动优惠金额（整单改价/抹零折算），仅对本次订单生效 */
  manual_discount?: number
  items: Array<{ sku_id: number; quantity: number; /** 单品成交价（>0 覆盖档案价，仅本单生效） */ price?: number }>
}) {
  return request<
    ApiResult<{
      id: number
      order_no: string
      pay_amount: number
      delivery_fee: number
      pickup_code?: string
      paid: boolean
    }>
  >({
    url: '/api/v1/admin/pos/orders',
    method: 'POST',
    data,
  })
}

/**
 * 挂单：暂存当前收银台工作区（不生成订单、不占库存）
 * @param data 工作区快照（会员 + 商品明细）
 */
export function createPosHeldOrder(data: {
  user_id: number
  user_name: string
  remark?: string
  items: Array<{
    sku_id: number
    quantity: number
    product_name: string
    spec_name?: string
    product_image?: string
    price: number
  }>
}) {
  return request<ApiResult<PosHeldOrder>>({
    url: '/api/v1/admin/pos/held-orders',
    method: 'POST',
    data,
  })
}

/**
 * 挂单列表（当前社区语境）
 * @param params operator_id 传 1 仅看自己的挂单（当前管理员）
 */
export function getPosHeldOrders(params?: { operator_id?: number }) {
  return request<ApiResult<{ list: PosHeldOrder[] }>>({
    url: '/api/v1/admin/pos/held-orders',
    method: 'GET',
    params,
  })
}

/**
 * 删除挂单（恢复结账或顾客放弃后调用）
 * @param id 挂单 ID
 */
export function deletePosHeldOrder(id: number) {
  return request<ApiResult<{ id: number }>>({
    url: `/api/v1/admin/pos/held-orders/${id}`,
    method: 'DELETE',
  })
}

/**
 * 自提订单核销（兜底入口）：校验取货码后完成订单
 * @param id 订单 ID
 * @param pickup_code 顾客出示的取货码
 */
export function verifyPickup(id: number, pickup_code: string) {
  return request<ApiResult<{ order_id: number; status: number }>>({
    url: `/api/v1/admin/orders/${id}/verify-pickup`,
    method: 'POST',
    data: { pickup_code },
  })
}

/**
 * 指派/取消指派配送员（B 履约工作台）
 * 指派后履约人员在 C 端工作台送货（开始配送/送达拍照），不经传统物流发货
 * @param id 订单 ID
 * @param deliveryUserId 履约人员 user_id；0=取消指派
 * @returns 操作结果（含履约人员姓名快照）
 */
export function assignDelivery(id: number, deliveryUserId: number) {
  return request<ApiResult<{ success: boolean; delivery_user_name: string }>>({
    url: `/api/v1/admin/orders/${id}/assign-delivery`,
    method: 'PUT',
    data: { delivery_user_id: deliveryUserId },
  })
}

/**
 * 获取订单详情
 * @param id 订单 ID
 * @returns 订单详情
 */
export function getOrderDetail(id: number) {
  return request<ApiResult<OrderDetail>>({
    url: `/api/v1/admin/orders/${id}`,
    method: 'GET',
  })
}

/**
 * 更新订单状态
 * @param id 订单 ID
 * @param status 目标状态
 * @param remark 备注
 * @returns 更新结果
 */
export function updateOrderStatus(id: number, status: number, remark?: string) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/status`,
    method: 'PUT',
    data: { status, remark },
  })
}

/**
 * 后台取消订单（走专用取消接口，回补库存并写入取消原因）
 * @param id 订单 ID
 * @param reason 取消原因（必填）
 * @returns 操作结果
 */
export function cancelOrder(id: number, reason: string) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/cancel`,
    method: 'POST',
    data: { reason },
  })
}

/**
 * 订单发货
 * @param id 订单 ID
 * @param data 物流信息（物流公司/单号必填）
 * @returns 发货结果
 */
export function shipOrder(id: number, data: { express_company: string; express_no: string; remark?: string }) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/ship`,
    method: 'POST',
    data,
  })
}

/**
 * 完成订单
 * @param id 订单 ID
 * @returns 完成结果
 */
export function completeOrder(id: number) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/complete`,
    method: 'POST',
  })
}

/**
 * 后台代客退款（发起即执行，免审核）
 * @param id 订单 ID
 * @param data 退款参数（items 为空表示整单退；金额由服务端汇算，amount 仅允许调低）
 * @returns 创建的售后单
 */
export function adminCreateRefund(
  id: number,
  data: {
    reason: string
    items?: Array<{ order_item_id: number; quantity: number }>
    amount?: number
    refund_freight?: boolean
    /** 退款模式：不传/1=按商品退（items 空=整单退） 2=仅金额退（不退商品不回补库存） */
    mode?: number
  },
) {
  return request<ApiResult<Refund>>({
    url: `/api/v1/admin/orders/${id}/refund`,
    method: 'POST',
    data,
  })
}

/**
 * 获取退款（售后）列表
 * @param params 查询参数
 * @returns 分页售后单列表
 */
export function getRefundList(params: PageParams & { status?: number }) {
  return request<ApiResult<PageResult<Refund>>>({
    url: '/api/v1/admin/refunds',
    method: 'GET',
    params,
  })
}

/**
 * 同意退款（支付单号由服务端以退款单记录为准，前端不再传 payment_no）
 * @param id 退款单 ID
 * @returns 操作结果
 */
export function approveRefund(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/approve`,
    method: 'POST',
  })
}

/**
 * 重试退款（卡在退款中的退款单重新发起渠道退款）
 * @param id 退款单 ID
 * @returns 操作结果
 */
export function retryRefund(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/retry`,
    method: 'POST',
  })
}

/**
 * 拒绝退款
 * @param id 退款单 ID
 * @param reject_reason 拒绝原因
 * @returns 操作结果
 */
export function rejectRefund(id: number, reject_reason: string) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/reject`,
    method: 'POST',
    data: { reject_reason },
  })
}

/**
 * 获取退款配置（运费退还策略）
 * @returns 退款配置
 */
export function getRefundSetting() {
  return request<ApiResult<{ refund_freight_policy: string }>>({
    url: '/api/v1/admin/settings/refund',
    method: 'GET',
  })
}

/**
 * 更新退款配置（运费退还策略）
 * @param policy 运费退还策略：always 始终退运费 / never 不退运费 / user_decide 用户自行选择
 * @returns 操作结果
 */
export function updateRefundSetting(policy: string) {
  return request<ApiResult<{ refund_freight_policy: string }>>({
    url: '/api/v1/admin/settings/refund',
    method: 'PUT',
    data: { refund_freight_policy: policy },
  })
}

// --- 收银台交班与应用偏好 ---

/**
 * 开工（开新班次）：同一操作员已有进行中班次时报错
 * @param note 开班备注
 * @returns 新班次信息
 */
export function startPosShift(note?: string) {
  return request<ApiResult<PosShift>>({
    url: '/api/v1/admin/pos/shifts/start',
    method: 'POST',
    data: { note },
  })
}

/**
 * 获取当前进行中的班次（本人语境）
 * @returns 班次信息；id=0 表示当前无进行中班次
 */
export function getCurrentPosShift() {
  return request<ApiResult<PosShift>>({
    url: '/api/v1/admin/pos/shifts/current',
    method: 'GET',
  })
}

/**
 * 交班（结束当前班次）：统计快照冻结后置为已结束
 * @param note 交班备注
 * @returns 已结束的班次信息（含统计快照）
 */
export function endPosShift(note?: string) {
  return request<ApiResult<PosShift>>({
    url: '/api/v1/admin/pos/shifts/end',
    method: 'POST',
    data: { note },
  })
}

/**
 * 我的交班记录（固定当前操作员）
 * @param params 分页参数（start_time 开班时间下限，近7天/近30天筛选）
 */
export function getMyPosShifts(params: PageParams & { start_time?: string }) {
  return request<ApiResult<PosShiftPage>>({
    url: '/api/v1/admin/pos/shifts/mine',
    method: 'GET',
    params,
  })
}

/**
 * 本店交班记录（order:view 权限，可按操作员过滤）
 * @param params 分页与操作员过滤参数
 */
export function getPosShifts(params: PageParams & { operator_id?: number }) {
  return request<ApiResult<PosShiftPage>>({
    url: '/api/v1/admin/pos/shifts',
    method: 'GET',
    params,
  })
}

/**
 * 获取应用偏好（常用应用，社区级共享存储）
 * @returns 偏好信息；app_keys 为空表示尚未设置
 */
export function getAppPreferences() {
  return request<ApiResult<AppPreferences>>({
    url: '/api/v1/admin/app-preferences',
    method: 'GET',
  })
}

/**
 * 保存应用偏好（常用应用，社区级共享存储）
 * @param app_keys 应用 key 列表（有序）
 */
export function saveAppPreferences(app_keys: string[]) {
  return request<ApiResult<AppPreferences>>({
    url: '/api/v1/admin/app-preferences',
    method: 'PUT',
    data: { app_keys },
  })
}
