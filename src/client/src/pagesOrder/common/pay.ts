/**
 * 支付编排工具
 * 统一封装「创建支付单 + 拉起支付」流程，
 * 避免各页面重复实现，也避免出现未真正完成支付却提示支付成功的情况
 * 置于分包目录：仅 pagesOrder 分包使用，避免打进主包（微信体验评分主包体积项）
 */

import { createPayment } from '@/api/payment'
import type { PaymentChannel } from '@/api/types'
import { isWeixinBrowser } from '@/utils/platform'

/**
 * 支付编排结果
 * - success: 支付已完成
 * - redirected: 已跳转微信/支付宝支付页面，支付结果需回跳后以订单状态为准
 * - guided: 已跳转站内支付宝引导页（微信内置浏览器），引导页接管后续流程：
 *   微信内展示分步指引，系统浏览器打开时自动唤起支付宝；结果以订单状态为准
 * - cancelled: 用户主动取消支付
 * - unsupported: 当前渠道未开通，或当前运行端未接入对应支付 SDK
 * - failed: 调起参数缺失、解析失败或拉起失败
 */
export type PayOutcome = 'success' | 'redirected' | 'guided' | 'cancelled' | 'unsupported' | 'failed'

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

/** 支付宝手机网站支付调起参数 */
interface AlipayPayParams {
  alipay_pay_url?: string
}

/**
 * 创建支付单并按渠道拉起支付
 * 接口异常直接抛出，由调用方 catch（request 层已统一提示错误）
 * @param orderId 订单 ID
 * @param channel 支付渠道
 * @returns 支付编排结果
 */
export async function payOrder(orderId: number, channel: PaymentChannel['channel']): Promise<PayOutcome> {
  if (channel !== 'cash' && channel !== 'wechat' && channel !== 'alipay') {
    return 'unsupported'
  }

  const res = await createPayment(orderId, channel)
  if (channel === 'cash') {
    return 'success'
  }
  if (channel === 'alipay') {
    return launchAlipayPay(res.data?.alipay_pay_params || '', res.data?.payment_no || '', orderId)
  }
  return launchWechatPay(res.data?.wechat_pay_params || '')
}

/**
 * 拉起支付宝手机网站支付
 * H5 端跳转支付宝收银台 URL；微信内置浏览器无法唤起支付宝且收银台无
 * 「继续浏览器付款」兜底入口，改为跳转站内独立引导页（免登录）：
 * 引导页在微信内展示分步指引，用户「在浏览器打开」时打开的是引导页自身 URL，
 * 引导页检测到非微信环境后自动唤起支付宝，全程不需要登录态；
 * 微信小程序/公众号等微信生态内无法唤起支付宝，返回 unsupported
 * @param paramsJson 后端返回的支付宝调起参数 JSON 字符串
 * @param paymentNo 支付单号（引导页/结果页凭据，免登录场景的唯一上下文）
 * @param orderId 订单 ID（结果页已登录时跳订单详情用）
 * @returns 拉起结果
 */
export async function launchAlipayPay(paramsJson: string, paymentNo = '', orderId = 0): Promise<PayOutcome> {
  if (!paramsJson) {
    return 'failed'
  }

  let params: AlipayPayParams
  try {
    params = JSON.parse(paramsJson) as AlipayPayParams
  } catch {
    return 'failed'
  }

  // #ifdef H5
  if (params.alipay_pay_url) {
    // 微信内置浏览器：直接跳转会被微信拦截卡死，跳独立引导页由用户转系统浏览器
    if (isWeixinBrowser()) {
      // 引导页只携带支付单号（非微信打开后凭单号调公开接口实时换取支付链接），
      // 不传支付链接本身：链接含 # 等保留字符，经 uni-app H5 路由 query 传递
      // 会被框架编解码破坏（实测 ? 会被改写为 =，与编码层数无关），导致支付宝报 missing-method
      const query = [
        paymentNo ? `no=${encodeURIComponent(paymentNo)}` : '',
        orderId ? `order_id=${orderId}` : '',
      ].filter(Boolean).join('&')
      uni.redirectTo({ url: `/pagesOrder/pay/alipay?${query}` })
      return 'guided'
    }
    window.location.href = params.alipay_pay_url
    return 'redirected'
  }
  // #endif

  return 'unsupported'
}

// #ifdef H5
/**
 * 复制文本到剪贴板（H5）
 * 优先使用 Clipboard API（secure context 下可用），失败或非安全上下文
 * 回退 execCommand('copy')，兼容微信内置浏览器 http 场景
 * @param text 待复制文本
 * @returns 是否复制成功
 */
export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 回退 execCommand
    }
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  // 移出可视区域但仍需可选中，避免 iOS 键盘弹出
  textarea.style.position = 'fixed'
  textarea.style.top = '-9999px'
  document.body.appendChild(textarea)
  textarea.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(textarea)
  return ok
}
// #endif

/**
 * 注册「用户回到本站」一次性回调（H5 专用）
 * 整页跳转第三方收银台后当前文档会被卸载，此时执行站内路由不生效，
 * 必须等用户回跳或手动切回标签页再处理：
 * - 配了支付宝 return_url：浏览器直接落到回跳地址，本回调一般不触发；
 * - 未配 return_url：用户手动切回标签页时由 visibilitychange 接管，
 *   避免停留在下单确认页看到已结算商品或重复下单
 * 回调触发后自动解绑，非 H5 端为空实现（不会进入 redirected 分支）
 * @param callback 用户回到本站时执行一次
 */
export function onReturnToSite(callback: () => void): void {
  // #ifdef H5
  const off = () => {
    document.removeEventListener('visibilitychange', onVisible)
    window.removeEventListener('pageshow', onPageShow)
    callback()
  }
  const onVisible = () => {
    if (document.visibilityState === 'visible') off()
  }
  // bfcache 恢复（用户用返回键回到本站）同样视为回到本站
  const onPageShow = () => off()
  document.addEventListener('visibilitychange', onVisible)
  window.addEventListener('pageshow', onPageShow)
  // #endif
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
 * @param channel 支付渠道，用于区分跳转提示文案，可省略
 * @returns 订单是否已完成支付（redirected/guided 需以回跳或回访后的订单状态为准，此处为 false）
 */
export function notifyPayOutcome(outcome: PayOutcome, channel?: PaymentChannel['channel']): boolean {
  switch (outcome) {
    case 'success':
      // 货到付款：此时只是下单成功、尚未收款，提示「支付成功」会让用户误以为已付款
      if (channel === 'cash') {
        uni.showToast({ title: '下单成功，请当面付款', icon: 'none' })
        return true
      }
      uni.showToast({ title: '支付成功', icon: 'success' })
      return true
    case 'redirected':
      uni.showToast({
        title: channel === 'alipay' ? '请在支付宝中完成支付' : '请在微信中完成支付',
        icon: 'none',
      })
      return false
    case 'guided':
      // 引导页已接管：分步指引与结果查看均在独立免登录页内完成，不再叠加 toast
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
