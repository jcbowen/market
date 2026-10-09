/**
 * 支付编排工具
 * 统一封装「创建支付单 + 拉起支付」流程，
 * 避免各页面重复实现，也避免出现未真正完成支付却提示支付成功的情况
 */

import { createPayment } from '@/api/payment'
import type { PaymentChannel } from '@/api/types'

/**
 * 支付编排结果
 * - success: 支付已完成
 * - redirected: 已跳转微信 H5 支付页面，支付结果需回跳后以订单状态为准
 * - cancelled: 用户主动取消支付
 * - unsupported: 当前渠道未开通，或当前运行端未接入对应支付 SDK
 * - failed: 调起参数缺失、解析失败或拉起失败
 */
export type PayOutcome = 'success' | 'redirected' | 'cancelled' | 'unsupported' | 'failed'

/** 微信 JSAPI 调起参数（小程序端） */
interface WechatJsapiParams {
  appId?: string
  timeStamp: string
  nonceStr: string
  package: string
  signType: string
  paySign: string
}

/** 微信 H5 调起参数 */
interface WechatH5Params {
  h5_url?: string
}

/** 微信调起参数（两种形态的并集） */
type WechatPayParams = WechatJsapiParams & WechatH5Params

/**
 * 创建支付单并按渠道拉起支付
 * 接口异常直接抛出，由调用方 catch（request 层已统一提示错误）
 * @param orderId 订单 ID
 * @param channel 支付渠道
 * @returns 支付编排结果
 */
export async function payOrder(orderId: number, channel: PaymentChannel['channel']): Promise<PayOutcome> {
  if (channel !== 'cash' && channel !== 'wechat') {
    return 'unsupported'
  }

  const res = await createPayment(orderId, channel)
  if (channel === 'cash') {
    return 'success'
  }
  return launchWechatPay(res.data?.wechat_pay_params || '')
}

/**
 * 拉起微信支付
 * 小程序端使用 uni.requestPayment 调起 JSAPI 支付；
 * H5 端 uni-app 未封装浏览器支付，直接跳转后端返回的 h5_url；
 * 其余端未接入微信 SDK，返回 unsupported
 * @param paramsJson 后端返回的微信调起参数 JSON 字符串
 * @returns 拉起结果
 */
export async function launchWechatPay(paramsJson: string): Promise<PayOutcome> {
  if (!paramsJson) {
    return 'failed'
  }

  let params: WechatPayParams
  try {
    params = JSON.parse(paramsJson) as WechatPayParams
  } catch {
    return 'failed'
  }

  // #ifdef H5
  if (params.h5_url) {
    window.location.href = params.h5_url
    return 'redirected'
  }
  // #endif

  // #ifdef MP-WEIXIN
  if (params.paySign) {
    return requestWechatPayment(params)
  }
  // #endif

  return 'unsupported'
}

// #ifdef MP-WEIXIN
/**
 * 调用 uni.requestPayment 完成微信小程序支付
 * @param params 后端下发的 JSAPI 签名参数
 * @returns 支付成功、用户取消或失败
 */
async function requestWechatPayment(params: WechatJsapiParams): Promise<PayOutcome> {
  try {
    await uni.requestPayment({
      provider: 'wxpay',
      timeStamp: params.timeStamp,
      nonceStr: params.nonceStr,
      package: params.package,
      signType: params.signType,
      paySign: params.paySign,
    })
    return 'success'
  } catch (err) {
    const message = (err as { errMsg?: string })?.errMsg || ''
    return message.includes('cancel') ? 'cancelled' : 'failed'
  }
}
// #endif

/**
 * 按支付编排结果统一提示
 * @param outcome 支付编排结果
 * @returns 订单是否已完成支付（redirected 需回跳后查订单状态，此处为 false）
 */
export function notifyPayOutcome(outcome: PayOutcome): boolean {
  switch (outcome) {
    case 'success':
      uni.showToast({ title: '支付成功', icon: 'success' })
      return true
    case 'redirected':
      uni.showToast({ title: '请在微信中完成支付', icon: 'none' })
      return false
    case 'cancelled':
      uni.showToast({ title: '已取消支付', icon: 'none' })
      return false
    case 'unsupported':
      uni.showToast({ title: '该支付方式暂未开通', icon: 'none' })
      return false
    default:
      uni.showToast({ title: '支付拉起失败', icon: 'none' })
      return false
  }
}
