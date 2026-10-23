/**
 * 平台判断工具
 * 用于区分 H5、微信小程序、App 等运行环境
 */

/** 当前是否为 H5 环境 */
export const isH5 = (): boolean => {
  // #ifdef H5
  return true
  // #endif
  return false
}

/** 当前是否为微信小程序环境 */
export const isMpWeixin = (): boolean => {
  // #ifdef MP-WEIXIN
  return true
  // #endif
  return false
}

/** 当前是否为 App 环境 */
export const isApp = (): boolean => {
  // #ifdef APP-PLUS
  return true
  // #endif
  return false
}

/**
 * 当前是否为微信内置浏览器（仅 H5 环境有意义，按 UA 判断）
 * 微信内置浏览器无法唤起支付宝 App，且新签约手机网站支付产品已无
 * 「继续浏览器付款」兜底入口，选择支付宝时需商户侧引导转系统浏览器
 * @returns 是否为微信内置浏览器（非 H5 环境恒为 false）
 */
export const isWeixinBrowser = (): boolean => {
  // #ifdef H5
  return /MicroMessenger/i.test(navigator.userAgent)
  // #endif
  return false
}

/**
 * 当前客户端渠道标识（与后台「渠道管理」的渠道 code 对应）
 * 构建期由条件编译决定：MP-WEIXIN → mp；APP-PLUS → app；其余（H5）→ h5。
 * 公众号（gzh）需 OAuth 授权后才可识别，授权链路开放前 H5 统一上报 h5。
 * 通过 X-Channel-Code 请求头随所有请求上报，服务端据此渲染支付方式与解析登录/支付凭证
 */
export const CLIENT_CHANNEL = (() => {
  // #ifdef MP-WEIXIN
  return 'mp'
  // #endif
  // #ifdef APP-PLUS
  return 'app'
  // #endif
  return 'h5'
})()
