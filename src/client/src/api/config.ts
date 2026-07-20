/**
 * API 全局配置
 * 集中管理请求基地址、超时时间、状态码等常量
 */

/**
 * 获取当前运行环境的请求基地址
 * H5 开发环境通过 vite proxy 代理到 localhost:18080
 * H5 生产环境同样使用 /api/v1，避免与 gateway 的 /s 静态文件前缀冲突
 */
export const BASE_URL = (() => {
  // #ifdef H5
  return '/api/v1'
  // #endif
  // 非 H5 平台（小程序/App）使用线上域名
  return 'https://www.jiuchet.com/s/api/v1'
})()

/** 请求超时时间（毫秒） */
export const REQUEST_TIMEOUT = 15000

/** 本地存储 token 的 key */
export const TOKEN_KEY = 'access_token'

/** 业务响应成功码 */
export const SUCCESS_CODE = 0

/** 需要重新登录的错误码 */
export const UNAUTHORIZED_CODES = [11001, 11002]
