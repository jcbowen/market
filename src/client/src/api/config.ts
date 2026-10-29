/**
 * API 全局配置
 * 集中管理请求基地址、超时时间、状态码等常量
 */

/**
 * 获取当前运行环境的请求基地址
 * H5 平台：优先读取环境变量 VITE_API_BASE_URL（见 .env.development / .env.production），
 * 未配置时回退为同源相对路径 /api/v1 —— 开发环境经 vite proxy 代理到后端（见 vite.config.ts），
 * 生产环境同样使用 /api/v1，避免与 gateway 的 /s 静态文件前缀冲突
 * 非 H5 平台（小程序/App）：优先级 = ext 注入（按客户托管发版注入域名）
 * > VITE_APP_API_BASE_URL（.env.production，客户自行构建时的兜底通道）
 * > 占位符（交付版回退值：忘配时明显连不通，不悄悄打到卖方服务器）
 * ext 注入分平台：
 * - 微信 = 代开发/托管场景的 ext.json，运行时 uni.getExtConfigSync 读取
 * - 支付宝 = 模板小程序实例化时经 alipay.open.mini.version.upload 传入的 ext，
 *   运行时 my.getExtConfigSync 读取（uni 未映射该 API，需直调 my 全局）
 */
export const BASE_URL = (() => {
  // #ifdef H5
  return import.meta.env.VITE_API_BASE_URL || '/api/v1'
  // #endif
  // 1. ext 注入（服务商/托管发版场景，运行时按客户区分域名）
  // #ifdef MP-WEIXIN
  try {
    const extConfig = uni.getExtConfigSync?.()
    if (extConfig && extConfig.base_url) return extConfig.base_url
  } catch (e) {
    // getExtConfigSync 在非代开发场景返回空对象，忽略
  }
  // #endif
  // #ifdef MP-ALIPAY
  try {
    const extConfig = my.getExtConfigSync?.() ?? {}
    const extBaseUrl = extConfig.base_url
    if (typeof extBaseUrl === 'string' && extBaseUrl) {
      return extBaseUrl
    }
  } catch (e) {
    // 非模板小程序实例（无 ext.json/extEnable=false）会抛错误码 20/21，忽略走 env 兜底
  }
  // #endif
  // 2. .env 配置（客户自行构建时在此填自己域名）
  // 3. 占位符回退（交付版默认值，故意不可达——提示域名未配置）
  return import.meta.env.VITE_APP_API_BASE_URL || 'https://your-domain.example.com/s/api/v1'
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
