import { request } from './request'
import type { ApiResult, LoginParams, LoginResult, AdminInfo } from './types'

/**
 * 认证相关 API
 */

/**
 * 管理员登录
 * @param params 登录参数
 * @returns 登录结果，包含 token 和管理员信息
 */
export function login(params: LoginParams) {
  return request<ApiResult<LoginResult>>({
    url: '/api/v1/admin/auth/login',
    method: 'POST',
    data: params,
  })
}

/**
 * 刷新访问 token
 * @param refreshToken 刷新令牌
 * @returns 新的登录结果
 */
export function refreshToken(refreshToken: string) {
  return request<ApiResult<LoginResult>>({
    url: '/api/v1/admin/auth/refresh',
    method: 'POST',
    data: { refresh_token: refreshToken },
  })
}

/**
 * 获取当前管理员信息
 * @returns 管理员信息
 */
export function getAdminInfo() {
  return request<ApiResult<AdminInfo>>({
    url: '/api/v1/admin/auth/info',
    method: 'GET',
  })
}
