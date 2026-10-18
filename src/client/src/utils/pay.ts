/**
 * 支付编排工具
 * 统一封装「创建支付单 + 拉起支付」流程，
 * 避免各页面重复实现，也避免出现未真正完成支付却提示支付成功的情况
 */

import { createPayment } from '@/api/payment'
import type { PaymentChannel } from '@/api/types'
import { isWeixinBrowser } from '@/utils/platform'

/**
 * 支付编排结果
 * - success: 支付已完成
 * - redirected: 已跳转微信/支付宝支付页面，支付结果需回跳后以订单状态为准
 * - guided: 已弹出支付宝引导蒙层（微信内置浏览器），需用户转系统浏览器完成支付，结果以订单状态为准
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
    return launchAlipayPay(res.data?.alipay_pay_params || '')
  }
  return launchWechatPay(res.data?.wechat_pay_params || '')
}

/**
 * 拉起支付宝手机网站支付
 * H5 端直接跳转支付宝收银台 URL；微信内置浏览器无法唤起支付宝且收银台无
 * 「继续浏览器付款」兜底入口，改为弹出引导蒙层提示用户转系统浏览器完成支付；
 * 微信小程序/公众号等微信生态内无法唤起支付宝，返回 unsupported
 * @param paramsJson 后端返回的支付宝调起参数 JSON 字符串
 * @returns 拉起结果
 */
export async function launchAlipayPay(paramsJson: string): Promise<PayOutcome> {
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
    // 微信内置浏览器：直接跳转会被微信拦截卡死，弹引导蒙层由用户转系统浏览器
    if (isWeixinBrowser()) {
      showAlipayGuide(params.alipay_pay_url)
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
async function copyTextToClipboard(text: string): Promise<boolean> {
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

/**
 * 在引导蒙层内展示轻量提示（H5）
 * uni.showToast 在 H5 的 z-index 低于蒙层会被遮挡，故在蒙层内自绘 toast
 * @param mask 蒙层根节点
 * @param msg 提示文案，约 2 秒后自动消失
 */
function showGuideToast(mask: HTMLElement, msg: string): void {
  const toast = document.createElement('div')
  toast.textContent = msg
  toast.setAttribute('style', [
    'position:absolute',
    'left:50%',
    'top:50%',
    'transform:translate(-50%,-50%)',
    'background:rgba(0,0,0,.8)',
    'color:#fff',
    'padding:10px 16px',
    'border-radius:8px',
    'font-size:14px',
    'white-space:nowrap',
    'z-index:2',
  ].join(';'))
  mask.appendChild(toast)
  setTimeout(() => toast.remove(), 2000)
}

/**
 * 弹出支付宝微信内引导蒙层（H5 专用）
 * 微信内置浏览器内无法唤起支付宝，展示「点击右上角 ··· → 在浏览器中打开」
 * 分步指引，并提供「复制支付链接」按钮供用户粘贴到系统浏览器完成支付；
 * 点击蒙层空白处关闭，重复调用不叠加
 * @param payUrl 支付宝收银台跳转 URL（alipay_pay_url）
 */
function showAlipayGuide(payUrl: string): void {
  const existed = document.getElementById('alipay-guide-mask')
  if (existed) {
    return
  }

  const mask = document.createElement('div')
  mask.id = 'alipay-guide-mask'
  // 不用 inset/gap：微信老 X5 内核（Chromium < 84）不支持，会整体失效
  mask.setAttribute('style', [
    'position:fixed',
    'top:0',
    'left:0',
    'right:0',
    'bottom:0',
    'background:rgba(0,0,0,.72)',
    'z-index:99999',
    'display:flex',
    'flex-direction:column',
    'padding:0 16px',
  ].join(';'))

  // 右上角指引：箭头指向微信「···」菜单位置
  const tip = document.createElement('div')
  tip.setAttribute('style', [
    'display:flex',
    'justify-content:flex-end',
    'align-items:flex-end',
    // env() 老内核不支持时回退前置的 10px
    'padding-top:10px',
    'padding-top:calc(env(safe-area-inset-top) + 10px)',
    'padding-right:36px',
  ].join(';'))
  tip.innerHTML = [
    '<span style="display:inline-block;width:0;height:0;',
    'border-left:10px solid transparent;',
    'border-bottom:14px solid #fff;',
    'transform:rotate(35deg)"></span>',
    '<span style="color:#fff;font-size:22px;letter-spacing:2px;line-height:1;',
    'margin-left:6px">···</span>',
  ].join('')

  // 步骤卡片
  const card = document.createElement('div')
  card.setAttribute('style', [
    'background:#fff',
    'border-radius:12px',
    'padding:20px 16px',
    'margin-top:10px',
  ].join(';'))
  card.innerHTML = [
    '<div style="font-size:15px;color:#333;line-height:2">',
    '1. 点击右上角 <b>「···」</b><br>2. 选择 <b>「在浏览器中打开」</b> 完成支付',
    '</div>',
  ].join('')

  const copyBtn = document.createElement('button')
  copyBtn.type = 'button'
  copyBtn.textContent = '复制支付链接，粘贴到浏览器打开'
  copyBtn.setAttribute('style', [
    'width:100%',
    'height:42px',
    'border:none',
    'border-radius:21px',
    'background:#1677ff',
    'color:#fff',
    'font-size:15px',
    'margin-top:12px',
  ].join(';'))
  copyBtn.onclick = async () => {
    const ok = await copyTextToClipboard(payUrl)
    showGuideToast(mask, ok ? '链接已复制，请粘贴到浏览器打开' : '复制失败，请按上方步骤操作')
  }

  const foot = document.createElement('div')
  foot.textContent = '完成支付后回到本页即可查看结果 · 点击空白处关闭'
  foot.setAttribute('style', [
    'font-size:12px',
    'color:#999',
    'text-align:center',
    'margin-top:12px',
  ].join(';'))

  card.appendChild(copyBtn)
  card.appendChild(foot)

  // 点击卡片内部不关闭，点击蒙层空白处关闭
  card.onclick = (e) => e.stopPropagation()
  mask.onclick = () => mask.remove()

  mask.appendChild(tip)
  mask.appendChild(card)
  document.body.appendChild(mask)
}
// #endif

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
      uni.showToast({ title: '支付成功', icon: 'success' })
      return true
    case 'redirected':
      uni.showToast({
        title: channel === 'alipay' ? '请在支付宝中完成支付' : '请在微信中完成支付',
        icon: 'none',
      })
      return false
    case 'guided':
      // 引导蒙层已展示完整操作步骤，不再叠加 toast 打断用户
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
