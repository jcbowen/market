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
