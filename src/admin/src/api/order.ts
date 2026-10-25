import { request } from './request'
import type {
  ApiResult,
  AppPreferences,
  Order,
  OrderDetail,
  PageParams,
  PageResult,
  PaymentAnomalyPage,
  PaymentSyncResult,
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
 * @param params 查询参数（order_no/receiver_name/receiver_phone 模糊；start_time/end_time 下单日期 YYYY-MM-DD；
 *               pay_no 支付单号/渠道交易号精确（支付宝商家订单号/交易号单框双号）；product_id 按商品过滤含全部规格）
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
    /** 按会员过滤（会员选择器） */
    user_id?: number
    /** 按收银班次过滤（交班记录「查看班内订单」入口） */
    shift_id?: number
    /** 来源过滤：1=C端 2=收银台（0=全部） */
    source?: number
    /** 按取货码精确过滤（收银台核销/订单 Tab 扫码查询） */
    pickup_code?: string
    /** 支付单号/渠道交易号精确过滤（支付宝商家订单号或交易号均可，单框双号） */
    pay_no?: string
    /** 按商品过滤（含全部规格，命中含该商品任意规格的订单） */
    product_id?: number
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

// --- 导出任务（下载中心） ---

/** 导出任务状态：1待处理 2生成中 3已完成 4失败 */
export type ExportTaskStatus = 1 | 2 | 3 | 4

/** 导出任务（下载中心列表行） */
export interface ExportTask {
  id: number
  export_type: string
  /** 1待处理 2生成中 3已完成 4失败 */
  status: ExportTaskStatus
  /** 查询条件人读摘要 */
  query_desc: string
  file_name: string
  /** 文件大小（字节） */
  file_size: number
  /** 导出数据行数 */
  row_count: number
  admin_id: number
  admin_name: string
  /** 累计下载次数（>0 即已下载过） */
  download_count: number
  /** 下载留痕 JSON（[{admin_id,admin_name,at}]，仅超管可见） */
  downloaders?: string
  /** 失败原因；完成态下复用为截断提示（已达单次导出上限） */
  error_msg: string
  created_at: string
  /** 文件过期时间（生成后 3 天） */
  expires_at: string
  /** 查询条件 JSON 快照（失败任务「重新导出」时解析还原为创建参数） */
  query_cond?: string
}

/**
 * 创建订单导出任务（后台异步生成，创建即返回）
 * @param params 查询参数：复用列表筛选字段；query_desc 查询条件人读摘要（下载中心展示）
 * @returns 任务 ID
 */
export function createOrderExportTask(
  params: {
    order_no?: string
    status?: number
    start_time?: string
    end_time?: string
    receiver_name?: string
    receiver_phone?: string
    user_id?: number
    shift_id?: number
    source?: number
    pickup_code?: string
    pay_no?: string
    product_id?: number
    community_id?: number
    /** 查询条件摘要，如「今天 · 待发货」 */
    query_desc?: string
  },
) {
  return request<ApiResult<{ id: number }>>({
    url: '/api/v1/admin/orders/export-tasks',
    method: 'POST',
    params,
  })
}

/**
 * 下载中心任务列表（普通管理员仅见自己的，超管见全部）
 * @param params 分页参数
 * @returns 任务列表 + 未下载完成数（角标）
 */
export function listOrderExportTasks(params?: PageParams) {
  return request<ApiResult<{ list: ExportTask[]; total: number; undownloaded: number }>>({
    url: '/api/v1/admin/orders/export-tasks',
    method: 'GET',
    params,
  })
}

/**
 * 未下载导出任务数（右上角下载图标角标）
 * @returns count 未下载数
 */
export function countUndownloadedExports() {
  return request<ApiResult<{ count: number }>>({
    url: '/api/v1/admin/orders/export-tasks/undownloaded-count',
    method: 'GET',
  })
}

/**
 * 下载导出文件：blob 请求（携带 Authorization 头）后触发浏览器保存
 * 不能用 <a href> 原生导航——浏览器直链 GET 不带鉴权头，会被 authAdmin 中间件拒为 11001；
 * 下载留痕仍由后端 download 接口记录
 * @param id 任务 ID
 * @param fileName 保存到本地的文件名（取任务行 file_name）
 * @throws Error 后端返回业务错误（含认证过期 11001）时抛出 message
 */
export async function downloadOrderExportTask(id: number, fileName: string) {
  const res = await request<Blob>({
    url: `/api/v1/admin/orders/export-tasks/${id}/download`,
    method: 'GET',
    responseType: 'blob',
  })
  const blob = res.data
  // 后端出错时返回的是 HTTP 200 + JSON（blob 形态），解析后按业务错误抛出
  if (blob instanceof Blob && blob.type.includes('application/json')) {
    const result = JSON.parse(await blob.text()) as ApiResult<unknown>
    throw new Error(result.message || '下载失败')
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/**
 * 删除导出任务（仅超管，文件一并删除）
 * @param id 任务 ID
 */
export function deleteOrderExportTask(id: number) {
  return request<ApiResult<{ success: boolean }>>({
    url: `/api/v1/admin/orders/export-tasks/${id}`,
    method: 'DELETE',
  })
}

// --- 收银台（G） ---

/**
 * 收银台代客下单（按所选支付方式当场结清）
 * @param data 下单参数（user_id=0 为散客；配送单必须带联系人/电话/地址；pay_type 支付方式，缺省现金 4；
 *              items[].price 单品改价（>0 以该价成交）；manual_discount 整单优惠（改价/抹零折算，不超过应付）；
 *              manual_amount 直接收款金额（「直接收款」无商品单，>0 时 items 可空，仅到店）
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
  /** 直接收款金额（无商品单专用）：>0 时订单不含商品明细，金额即成交金额，仅到店 */
  manual_amount?: number
  items?: Array<{ sku_id: number; quantity: number; /** 单品成交价（>0 覆盖档案价，仅本单生效） */ price?: number }>
}) {
  return request<
    ApiResult<{
      id: number
      order_no: string
      pay_amount: number
      delivery_fee: number
      pickup_code?: string
      /** 现金=true 创建即收款完成；付款码=false 仅建待支付支付单，待扫码扣款 */
      paid: boolean
      /** 待支付支付单号（付款码收款两步化的第二步凭据，现金为空） */
      payment_no?: string
    }>
  >({
    url: '/api/v1/admin/pos/orders',
    method: 'POST',
    data,
  })
}

/**
 * 收银台可用支付方式（按支付配置动态渲染）
 * 现金恒可用；「付款码支付」（pay_type=5）仅当微信或支付宝任一渠道开启且就绪时返回
 * @returns 支付方式列表
 */
export function listPosPayMethods() {
  return request<ApiResult<{ list: Array<{ pay_type: number; label: string }> }>>({
    url: '/api/v1/admin/pos/pay-methods',
    method: 'GET',
  })
}

/**
 * 收银台付款码扣款（两步化第二步）：对待支付支付单提交顾客付款码向渠道收款
 * 渠道由付款码前缀自动识别（微信 10-15 / 支付宝 25-30，云闪付暂不支持）
 * @param orderId 订单 ID
 * @param data payment_no 待支付支付单号（下单响应返回）；auth_code 顾客付款码（扫码枪读取）
 * @returns 归一化结果：state=PAID 收款成功 / PAYING 用户支付中（继续轮询）/ FAILED 渠道拒绝（换码重扫）
 */
export function payPosOrderCode(orderId: number, data: { payment_no: string; auth_code: string }) {
  return request<
    ApiResult<{
      state: 'PAID' | 'PAYING' | 'FAILED'
      message: string
      payment_no: string
      order_id: number
      transaction_id: string
      pay_type: number
      amount: number
    }>
  >({
    url: `/api/v1/admin/pos/orders/${orderId}/pay-code`,
    method: 'POST',
    data,
  })
}

/**
 * 收银台付款码收款结果查询（PAYING 在途轮询用，幂等可反复调）
 * 向渠道核实该订单是否已收款，已收款则补同步订单与支付单
 * @param orderId 订单 ID
 * @param paymentNo 支付单号（可选精确定位）
 * @returns paid 是否已收款 / channel_state 渠道状态 / message 渠道说明
 */
export function getPosOrderPayStatus(orderId: number, paymentNo?: string) {
  return request<
    ApiResult<{
      paid: boolean
      changed: boolean
      channel_state?: string
      transaction_id?: string
      amount?: number
      payment_no?: string
      message?: string
      order_status?: number
    }>
  >({
    url: `/api/v1/admin/pos/orders/${orderId}/pay-code`,
    method: 'GET',
    params: paymentNo ? { payment_no: paymentNo } : undefined,
  })
}

/**
 * 收银台待支付订单改用现金收款（扣款失败换现金 / 订单查询继续收现金）
 * 后端先关闭遗留待支付支付单（旧付款码不可再扣款），再创建现金支付单，订单自动流转已支付
 * @param orderId 订单 ID
 * @returns 收款结果（paid=true 订单已完结，到店单自动完成）
 */
export function payPosOrderCash(orderId: number) {
  return request<
    ApiResult<{
      paid: boolean
      order_id: number
      order_no: string
      pay_amount: number
      pay_type: number
    }>
  >({
    url: `/api/v1/admin/pos/orders/${orderId}/pay-cash`,
    method: 'POST',
  })
}

/**
 * 收银台待支付订单重建付款码收款会话（订单查询「继续收款」入口）
 * 复用后端幂等：同渠道同金额待支付单直接复用（out_trade_no 不变）
 * @param orderId 订单 ID
 * @returns payment_no 付款码扫码窗凭据 / pay_type 占位值 / pay_amount 订单应付
 */
export function ensurePosCodePaySession(orderId: number) {
  return request<
    ApiResult<{
      payment_no: string
      pay_type: number
      order_id: number
      order_no: string
      pay_amount: number
    }>
  >({
    url: `/api/v1/admin/pos/orders/${orderId}/pay-code-session`,
    method: 'POST',
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
 * 恢复被用户软删除的订单（客服场景：用户误删，请求找回）
 * 软删只是把订单从用户自己的列表隐藏，后台始终全量可见；本接口把 user_deleted 置回 0
 * @param id 订单 ID
 * @returns 恢复后的订单
 */
export function restoreOrder(id: number) {
  return request<ApiResult<Order>>({
    url: `/api/v1/admin/orders/${id}/restore`,
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
 * 恢复退款单自动收敛（退款中卡单且自动重试已用满预算）
 * 归零支付流水与退款单两侧的重试预算，让调度器重新核实渠道终态，不再次向渠道发起出款
 * @param id 退款单 ID
 * @param remark 渠道核实依据（必填）
 * @returns 操作结果
 */
export function resetRefundRetry(id: number, remark: string) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/refunds/${id}/reset-retry`,
    method: 'POST',
    data: { remark },
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
 * 获取退款配置（运费退还策略 + 已完成单售后窗口）
 * @returns 退款配置
 */
export function getRefundSetting() {
  return request<ApiResult<{ refund_freight_policy: string; completed_refund_hours: number }>>({
    url: '/api/v1/admin/settings/refund',
    method: 'GET',
  })
}

/**
 * 更新退款配置（运费退还策略 + 已完成单售后窗口）
 * @param policy 运费退还策略：always 始终退运费 / never 不退运费 / user_decide 用户自行选择
 * @param completedRefundHours 已完成单售后窗口（小时），0=不允许已完成单申请售后
 * @returns 操作结果
 */
export function updateRefundSetting(policy: string, completedRefundHours: number) {
  return request<ApiResult<{ refund_freight_policy: string; completed_refund_hours: number }>>({
    url: '/api/v1/admin/settings/refund',
    method: 'PUT',
    data: { refund_freight_policy: policy, completed_refund_hours: completedRefundHours },
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

/**
 * 主动向渠道同步支付状态（回调丢失时的手动补偿入口）
 * @param id 订单 ID
 * @param payment_no 支付单号，可选；多笔支付尝试时用它精确指定要查哪一笔
 * @returns 渠道结论与是否已流转订单
 */
export function syncOrderPayment(id: number, payment_no?: string) {
  return request<ApiResult<PaymentSyncResult>>({
    url: `/api/v1/admin/orders/${id}/payment-sync`,
    method: 'POST',
    data: { payment_no },
  })
}

/**
 * 人工标记订单已支付（仅限渠道接口不可用但线下已核实到账的死角）
 * 渠道交易号与核实依据必填：这条入口绕过了渠道核实，全靠留痕保证事后可追责
 * @param id 订单 ID（仅待支付订单可用）
 * @param data 支付单号 + 渠道交易号 + 收款金额 + 核实依据
 * @returns 操作结果
 */
export function markOrderPaid(
  id: number,
  data: { payment_no: string; transaction_id: string; amount: number; remark: string },
) {
  return request<ApiResult<{ id: number; payment_no: string }>>({
    url: `/api/v1/admin/orders/${id}/payment-mark-paid`,
    method: 'POST',
    data,
  })
}

/**
 * 支付异常/对账流水队列
 * @param params 分页与筛选参数。handled 是筛选选择器（1 只看待处理 2 只看已处理 0/缺省 全部），
 *   与行数据的 handled 字段口径不同（后者是落库标记：0 待处理 1 已处理），勿混用；
 *   order_id 查单条订单的完整轨迹，include_ok 是否含确认成功的正常条目
 * @returns 异常流水分页（含社区范围内待处理条数 pending，pending 不随筛选变化）
 */
export function getPaymentAnomalies(
  params: PageParams & { handled?: number; order_id?: number; include_ok?: boolean },
) {
  return request<ApiResult<PaymentAnomalyPage>>({
    url: '/api/v1/admin/payments/anomalies',
    method: 'GET',
    params,
  })
}

/**
 * 支付异常销账（人工在渠道侧核对完成后关闭待办）
 * @param id 流水 ID
 * @param remark 核实依据（如渠道商户后台的交易状态截图说明）
 * @returns 操作结果
 */
export function handlePaymentAnomaly(id: number, remark: string) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/payments/anomalies/${id}/handle`,
    method: 'POST',
    data: { remark },
  })
}
