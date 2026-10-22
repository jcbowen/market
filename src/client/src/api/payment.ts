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
