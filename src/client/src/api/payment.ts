/**
 * 支付相关 API
 * 包含查询支付渠道、创建支付、查询支付
 */

import { get, post } from './request'
import type { CreatePaymentResult, Payment, PaymentChannel, PaymentStatusResult } from './types'

/**
 * 获取当前启用的支付渠道列表
 * @returns 渠道列表（包装在 channels 字段中）
 */
export function getPaymentChannels() {
  return get<{ channels: PaymentChannel[] }>('/payments/channels')
}

/**
 * 创建支付单
 * 微信 openid 由服务端从用户资料取得，无需前端传入
 * @param orderId 订单 ID
 * @param channel 支付渠道：wechat、alipay、cash（货到付款）
 * @returns 支付单号与对应渠道的调起参数
 */
export function createPayment(orderId: number, channel: PaymentChannel['channel']) {
  return post<CreatePaymentResult>('/payments', { order_id: orderId, channel })
}

/**
 * 查询订单的支付单详情
 * 后端按订单 ID 查询已支付记录
 * @param orderId 订单 ID
 */
export function getPaymentDetail(orderId: number) {
  return get<Payment>(`/payments/${orderId}`)
}

/**
 * 公开查询支付状态（免登录）
 * 支付宝回跳结果页轮询用：凭不可枚举的支付单号取最小字段集，
 * 无需登录态，系统浏览器打开时同样可用
 * @param paymentNo 支付单号
 */
export function getPaymentStatusPublic(paymentNo: string) {
  return get<PaymentStatusResult>('/payments/status', { no: paymentNo }, undefined, { silent: true })
}

/** 支付发起场景标记：微信内置浏览器发起（引导页携带，服务端编入支付宝回跳地址供结果页识别） */
export const ALIPAY_FROM_WX_H5 = 'wxh5'

/**
 * 凭支付单号换取支付宝收银台链接（免登录）
 * 支付引导页用：引导页 URL 只携带支付单号，打开后实时换取新签名的收银台链接，
 * 规避支付链接（含 # 等保留字符）经 uni-app H5 路由 query 传递时被框架编解码破坏的问题；
 * 仅待支付的支付宝单返回链接，其它状态只回支付单状态
 * @param paymentNo 支付单号
 * @param from 支付发起场景标记：引导页固定传 wxh5（微信内发起），
 *             服务端白名单校验后编入支付宝回跳地址，结果页据此切换「返回微信」按钮
 */
export function getAlipayPayUrlPublic(paymentNo: string, from = '') {
  return get<{ payment_no: string; status: number; order_id: number; alipay_pay_url: string }>(
    '/payments/alipay/url',
    { no: paymentNo, from },
    undefined,
    { silent: true },
  )
}
