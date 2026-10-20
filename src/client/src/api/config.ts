/**
 * API 全局配置
 * 集中管理请求基地址、超时时间、状态码等常量
 */

/**
 * 获取当前运行环境的请求基地址
 * H5 平台：优先读取环境变量 VITE_API_BASE_URL（见 .env.development / .env.production），
 * 未配置时回退为同源相对路径 /api/v1 —— 开发环境经 vite proxy 代理到后端（见 vite.config.ts），
 * 生产环境同样使用 /api/v1，避免与 gateway 的 /s 静态文件前缀冲突
 * 非 H5 平台（小程序/App）不受环境变量影响，使用线上域名
 */
export const BASE_URL = (() => {
  // #ifdef H5
  return import.meta.env.VITE_API_BASE_URL || '/api/v1'
  // #endif
  // 非 H5 平台（小程序/App）使用线上域名
  return 'https://www.jiuchet.com/s/api/v1'
})()

/** 请求超时时间（毫秒） */
export const REQUEST_TIMEOUT = 15000

/** 本地存储 token 的 key */
export const TOKEN_KEY = 'access_token'

/** 本地存储刷新令牌的 key，401 时用于静默续期（仅小程序/App；H5 改由 httpOnly cookie 承载） */
export const REFRESH_TOKEN_KEY = 'refresh_token'

/**
 * 声明「refresh_token 由 httpOnly cookie 承载」的请求头
 * 后端见该头则在登录/刷新时下发 cookie 且不再把 refresh_token 放进响应体；
 * H5 携带，小程序与 App 不携带（无 httpOnly cookie 语义），保持响应体下发
 */
export const REFRESH_COOKIE_HEADER = 'X-Refresh-Cookie'

/** 业务响应成功码 */
export const SUCCESS_CODE = 0

/** 需要重新登录的错误码 */
export const UNAUTHORIZED_CODES = [11001, 11002]
