/**
 * 支付相关 API
 * 包含查询支付渠道、创建支付、查询支付
 */

import { get, post } from './request'
import type { Payment, PaymentChannel } from './types'

/**
 * 获取当前启用的支付渠道列表
 * @returns 渠道列表（包装在 channels 字段中）
 */
export function getPaymentChannels() {
  return get<{ channels: PaymentChannel[] }>('/payments/channels')
}

/**
 * 创建支付单
 * @param orderId 订单 ID
 * @param channel 支付渠道：wechat、alipay、cash（货到付款）
 */
export function createPayment(orderId: number, channel: 'wechat' | 'alipay' | 'cash') {
  return post<Payment>('/payments', { order_id: orderId, channel })
}

/**
 * 查询支付单详情
 * @param id 支付单 ID
 */
export function getPaymentDetail(id: number) {
  return get<Payment>(`/payments/${id}`)
}
