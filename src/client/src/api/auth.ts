/**
 * 认证相关 API
 * 包含微信小程序登录、H5 手机号验证码登录、刷新 token
 */

import { post } from './request'

/** 登录响应数据 */
export interface LoginResult {
  access_token: string
  refresh_token: string
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
  return post<LoginResult>('/auth/login/phone', { phone })
}

/**
 * 使用 refresh_token 换取新的 access_token
 * @param refreshToken 刷新令牌
 * @returns 新的 token 信息
 */
export function refreshToken(refreshToken: string) {
  return post<LoginResult>('/auth/refresh', { refresh_token: refreshToken })
}
