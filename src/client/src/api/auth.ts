/**
 * 认证相关 API
 * 包含微信小程序登录、H5 手机号验证码登录、刷新 token
 */

import { post } from './request'
import { REFRESH_COOKIE_HEADER } from './config'
import { isH5 } from '@/utils/platform'

/** 登录响应数据 */
export interface LoginResult {
  access_token: string
  /**
   * 刷新令牌：H5 由后端 httpOnly cookie 承载，响应体不再返回（F2）；
   * 小程序与 App 无 httpOnly cookie 语义，仍在此返回并需本地持久化
   */
  refresh_token?: string
  expires_in: number
  user: {
    id: number
    nickname: string
    avatar: string
    phone: string
    community_id: number
  }
}

/**
 * H5 下声明「refresh_token 走 httpOnly cookie」的请求头
 * @returns H5 返回带值的头对象，其他平台返回 undefined（后端保持响应体下发）
 */
function refreshCookieHeader(): Record<string, string> | undefined {
  return isH5() ? { [REFRESH_COOKIE_HEADER]: '1' } : undefined
}

/**
 * 微信小程序 code 登录
 * @param code 调用 uni.login 获取的 code
 * @returns 登录结果（含 token 与用户信息）
 */
export function wxLogin(code: string) {
  return post<LoginResult>('/auth/weixin-login', { code })
}

/**
 * H5 发送手机号验证码
 * @param phone 手机号
 * @returns 发送结果
 */
export function sendPhoneCode(phone: string) {
  return post('/auth/phone-code', { phone })
}

/**
 * H5 手机号登录
 * 当前开发阶段仅输入手机号即可登录/注册，无需验证码
 * @param phone 手机号
 * @returns 登录结果
 */
export function phoneLogin(phone: string) {
  return post<LoginResult>('/auth/login/phone', { phone }, refreshCookieHeader())
}

/**
 * 登出：请后端清除 refresh_token 的 httpOnly cookie
 * JS 删不掉 httpOnly cookie，只能由后端下发过期值；仅 H5 需要
 * bypassRefresh：调用时本地令牌往往已过期，若走静默续期链，这个 401 会再次触发
 * 登出收口（handleUnauthorized → logout → 再发本接口），形成请求放大
 * @returns 后端响应
 */
export function logout() {
  return post('/auth/logout', {}, refreshCookieHeader(), { bypassRefresh: true, silent: true })
}
