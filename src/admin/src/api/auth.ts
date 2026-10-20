import { request } from './request'
import type { ApiResult, LoginParams, LoginResult, AdminInfo } from './types'

/**
 * 认证相关 API
 */

/**
 * refresh_token 由后端 httpOnly cookie 承载的声明头（F2）
 * 后台是同源浏览器应用，cookie 自动随行，JS 不再持有这份 7 天凭证
 */
const REFRESH_COOKIE_HEADER = { 'X-Refresh-Cookie': '1' }

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
    headers: REFRESH_COOKIE_HEADER,
  })
}

/**
 * 登出：请后端清除 refresh_token 的 httpOnly cookie
 * httpOnly cookie 无法由 JS 删除，只能由后端下发过期值
 * @returns 后端响应
 */
export function logout() {
  return request<ApiResult<unknown>>({
    url: '/api/v1/admin/auth/logout',
    method: 'POST',
    data: {},
    headers: REFRESH_COOKIE_HEADER,
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
