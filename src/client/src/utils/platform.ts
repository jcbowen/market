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
