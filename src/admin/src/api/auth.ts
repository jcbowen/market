import { request } from './request'
import { requestWithEncryptedPasswords } from '@/utils/crypto'
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
 * 密码经 RSA 公钥加密后传输（密钥轮换自动重试，见 utils/crypto.ts）；
 * 公钥不可得时降级明文，由后端明文兼容通道兜底
 * @param params 登录参数
 * @returns 登录结果，包含 token 和管理员信息
 */
export function login(params: LoginParams) {
  return requestWithEncryptedPasswords(
    async (enc) => ({
      username: params.username,
      ...(await enc.encryptFields({ password: params.password })),
    }),
    (data) =>
      request<ApiResult<LoginResult>>({
        url: '/api/v1/admin/auth/login',
        method: 'POST',
        data,
        headers: REFRESH_COOKIE_HEADER,
      }),
  )
}

/**
 * 静默续期访问令牌（原生 fetch，不经 axios 拦截器）
 * 刻意静默：WS 重连探测等场景下，refresh 失效不应触发全局错误提示或登录页跳转
 * （会话真失效时由业务请求拦截器统一收口跳转）；refresh_token 经 httpOnly cookie
 * 自动携带，请求头 X-Refresh-Cookie 声明 cookie 模式（后端同步轮换 refresh 与
 * access 两枚 cookie），成功响应体返回新 access_token
 * @returns 续期结果：成功时含新 token 与有效期（秒）
 */
export async function refreshToken(): Promise<{ ok: boolean; accessToken?: string; expiresIn?: number }> {
  try {
    const resp = await fetch('/api/v1/admin/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Refresh-Cookie': '1' },
      body: '{}',
    })
    const body = (await resp.json()) as ApiResult<{ access_token: string; expires_in: number }>
    if (!body || (body.code !== 0 && body.code !== 200)) return { ok: false }
    return { ok: true, accessToken: body.data.access_token, expiresIn: body.data.expires_in }
  } catch {
    // 网络不可达（如网关重启窗口）：视为本次续期失败，由调用方重试
    return { ok: false }
  }
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
