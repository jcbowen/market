import {
  BASE_URL,
  REQUEST_TIMEOUT,
  TOKEN_KEY,
  REFRESH_TOKEN_KEY,
  REFRESH_COOKIE_HEADER,
  SUCCESS_CODE,
  UNAUTHORIZED_CODES,
} from './config'
import type { LoginResult } from './auth'
import { isH5, CLIENT_CHANNEL } from '@/utils/platform'

/**
 * 统一响应结构
 * 与后端 response.Response 对应
 */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data?: T
  total?: number
}

/**
 * 分页数据结构
 */
export interface PageData<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

/**
 * 请求方法类型
 * 不含 PATCH：uni.request 类型不接受，且当前无调用方
 */
type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

/**
 * 通用请求选项
 */
interface RequestOptions {
  url: string
  method?: Method
  data?: Record<string, unknown>
  header?: Record<string, string>
  hideLoading?: boolean
  /** 静默模式：业务失败时不弹全局 toast（轮询等自动重试场景由调用方自行提示） */
  silent?: boolean
  /**
   * 跳过静默续期：401 直接把 UnauthorizedError 抛给调用方，不触发刷新与登出收口。
   * 用于「登录态已确定失效时仍要发」的请求（如登出通知后端清 cookie）——
   * 这类请求若走续期链，会在 401 后再次触发 handleUnauthorized，形成登出套登出的请求放大
   */
  bypassRefresh?: boolean
}

/**
 * 401 未授权错误
 * 仅作为内部信号：标识当前请求因登录态失效被拒，
 * 由上层 request/upload 捕获后触发静默续期并重放，不外泄给业务调用方
 */
class UnauthorizedError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'UnauthorizedError'
  }
}

/**
 * 进行中的静默刷新 Promise
 * 模块级单例：并发触发的多个 401 共享同一次刷新请求（互斥），
 * 刷新结束后置回 null，允许下次过期再刷新
 */
let refreshingPromise: Promise<boolean> | null = null

/**
 * 从本地存储读取 token
 * @returns token 字符串，不存在返回空字符串
 */
function getToken(): string {
  try {
    return uni.getStorageSync(TOKEN_KEY) || ''
  } catch {
    return ''
  }
}

/**
 * 从本地存储读取刷新令牌
 * @returns refresh_token 字符串，不存在返回空字符串
 */
function getRefreshToken(): string {
  try {
    return uni.getStorageSync(REFRESH_TOKEN_KEY) || ''
  } catch {
    return ''
  }
}

/**
 * 401 登出回调（由 stores/user 模块注册）
 * 请求层不反向依赖 store 模块：request → stores/user → api/auth → request 存在循环依赖，
 * 且 store 同时被页面静态引用，动态 import 会触发 Rollup MIXED_IMPORTS 警告，
 * 故改为注册制：stores/user 加载时注入登出实现，请求层只留兜底
 */
let unauthorizedHandler: (() => void) | null = null

/**
 * 注册 401 登出回调（stores/user 模块加载时调用一次）
 * @param handler 登出实现（清 Pinia 状态 + 清缓存 + H5 通知后端清 cookie）
 */
export function registerUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler
}

/**
 * 处理未登录：清空登录态（含 Pinia）并重置到登录页
 * 用于静默续期不可用（无 refresh_token / refresh_token 已失效）时的兜底
 *
 * 关键点：
 * - 必须清 Pinia：仅 removeStorageSync 不会重置 store 里的 token ref，
 *   isLogin 仍为 true → 页面守卫继续放行发请求 → 401 洪泛。
 *   登出实现由 stores/user 模块注册（见 registerUnauthorizedHandler）。
 * - 未注册时（理论不可达：stores/user 被 App.vue 静态引用，必先于任何请求加载）
 *   兜底清本地令牌，至少保证下次冷启动不再带旧 token。
 * - reLaunch 而非 navigateTo：小程序页面栈满 10 层时 navigateTo 静默失败，
 *   会导致 token 已清却停在原页、反复跳登录全部失败。
 * - 并发去重：一屏多个请求同时 401 时只收口一次。reLaunch 是异步生效的，
 *   期间 getCurrentPages 仍是旧路由，靠「已在登录页」判断拦不住紧随的并发调用
 */
const UNAUTHORIZED_DEDUCE_WINDOW = 2000
let lastUnauthorizedAt = 0

function handleUnauthorized(): void {
  const now = Date.now()
  if (now - lastUnauthorizedAt < UNAUTHORIZED_DEDUCE_WINDOW) return
  lastUnauthorizedAt = now

  if (unauthorizedHandler) {
    unauthorizedHandler()
  } else {
    uni.removeStorageSync(TOKEN_KEY)
    uni.removeStorageSync(REFRESH_TOKEN_KEY)
  }
  // 避免登录页重复跳转
  const pages = getCurrentPages()
  const current = pages[pages.length - 1]
  if (current && current.route?.includes('pages/login/login')) {
    return
  }
  uni.reLaunch({ url: '/pages/login/login' })
}

/**
 * 显示轻提示
 * @param message 提示文本
 */
function showError(message: string): void {
  uni.showToast({ title: message, icon: 'none' })
}

/**
 * 将对象序列化为 URL 查询字符串
 * @param params 查询参数对象
 * @returns 编码后的查询字符串，不含前导 ?
 */
function buildQueryString(params?: Record<string, unknown>): string {
  if (!params) return ''
  const parts: string[] = []
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
  }
  return parts.join('&')
}

/**
 * 调用 /auth/refresh 换取新令牌并持久化（滑动续期：响应含新 refresh_token）
 * 刷新请求为裸 uni.request 直调，不经过 requestOnce/静默续期逻辑，天然防递归死循环
 *
 * 令牌载体按平台分岔（F2）：
 * - H5：refresh_token 由后端下发 httpOnly cookie，JS 读不到也无需读，
 *   请求带 X-Refresh-Cookie: 1 声明该承载方式，响应体不再返回 refresh_token
 * - 小程序/App：无 httpOnly cookie 语义（小程序不自管浏览器 cookie、App 无同源），
 *   仍用本地存储的 refresh_token 走请求体
 *
 * 失败语义：
 * - refresh_token 缺失或被服务端判定无效：清空登录态并跳转登录页
 * - 网络异常：保留本地令牌，下次请求再触发重试（不误杀登录态）
 *
 * @returns 刷新成功返回 true；失败返回 false
 */
async function doRefresh(): Promise<boolean> {
  // H5 的 refresh_token 只存在于后端下发的 httpOnly cookie，JS 读不到，凭 cookie 续期
  if (isH5()) return doRefreshByCookie()
  const refreshToken = getRefreshToken()
  if (!refreshToken) {
    handleUnauthorized()
    return false
  }
  const resp = await new Promise<{
    statusCode: number
    data: ApiResponse<LoginResult>
  } | null>((resolve) => {
    uni.request({
      url: `${BASE_URL}/auth/refresh`,
      method: 'POST',
      data: { refresh_token: refreshToken },
      timeout: REQUEST_TIMEOUT,
      success: (res) => resolve(res as unknown as { statusCode: number; data: ApiResponse<LoginResult> }),
      fail: () => resolve(null),
    })
  })
  // 网络异常：保留令牌，静默失败，后续请求可再触发
  if (!resp) return false
  const body = resp.data
  if (
    resp.statusCode === 401 ||
    !body ||
    body.code !== SUCCESS_CODE ||
    !body.data
  ) {
    handleUnauthorized()
    return false
  }
  uni.setStorageSync(TOKEN_KEY, body.data.access_token)
  uni.setStorageSync(REFRESH_TOKEN_KEY, body.data.refresh_token)
  return true
}

/**
 * H5 专属：凭 httpOnly cookie 静默续期
 * 与小程序/App 分支同一套失败语义，仅令牌载体不同
 *
 * @returns 续期成功返回 true；cookie 缺失/失效返回 false（已跳转登录页）；
 *          网络异常返回 false（保留登录态，下次请求再触发）
 */
async function doRefreshByCookie(): Promise<boolean> {
  const resp = await new Promise<{
    statusCode: number
    data: ApiResponse<LoginResult>
  } | null>((resolve) => {
    uni.request({
      url: `${BASE_URL}/auth/refresh`,
      method: 'POST',
      header: { [REFRESH_COOKIE_HEADER]: '1' },
      // cookie 承载下请求体无需携带令牌，空对象保证后端按 JSON 解析
      data: {},
      timeout: REQUEST_TIMEOUT,
      success: (res) => resolve(res as unknown as { statusCode: number; data: ApiResponse<LoginResult> }),
      fail: () => resolve(null),
    })
  })
  if (!resp) return false
  const body = resp.data
  if (resp.statusCode === 401 || !body || body.code !== SUCCESS_CODE || !body.data) {
    handleUnauthorized()
    return false
  }
  uni.setStorageSync(TOKEN_KEY, body.data.access_token)
  return true
}

/**
 * 静默刷新令牌（进程内互斥）
 * 并发 401 复用同一次刷新：首个请求发起刷新，其余等待同一 Promise，
 * 避免并发重复刷新导致 refresh_token 被轮换后旧值失效的竞态
 *
 * 使用示例：
 *   const ok = await silentRefresh()
 *   if (ok) return requestOnce(options) // 用新令牌重放
 *
 * @returns 刷新成功返回 true；失败返回 false（无效令牌场景已跳转登录页）
 */
function silentRefresh(): Promise<boolean> {
  if (!refreshingPromise) {
    refreshingPromise = doRefresh().finally(() => {
      refreshingPromise = null
    })
  }
  return refreshingPromise
}

/**
 * 单次请求：发送 HTTP 请求并返回 Promise，不做静默续期与重放
 * 401（HTTP 状态码或业务码）时 reject UnauthorizedError，由 request 层处理
 * @param options 请求配置
 * @returns Promise<ApiResponse<T>>
 * @throws UnauthorizedError 登录态失效；Error 网络异常或业务失败
 */
function requestOnce<T = unknown>(
  options: RequestOptions,
): Promise<ApiResponse<T>> {
  const { url, method = 'GET', data, header = {}, hideLoading = false, silent = false } = options
  const token = getToken()
  let fullUrl = url.startsWith('http') ? url : `${BASE_URL}${url}`

  // H5 下 uni.request 对 GET 的 data 拼接到 URL 的行为不稳定，这里手动拼接
  // #ifdef H5
  if (method === 'GET' && data && typeof data === 'object') {
    const query = buildQueryString(data as Record<string, unknown>)
    if (query) {
      fullUrl += `${fullUrl.includes('?') ? '&' : '?'}${query}`
    }
  }
  // #endif

  // 请求体统一预计算（避免条件编译下对象字面量出现同名 data 属性触发 TS1117）：
  // H5 的 GET 已手动拼接 query，不再传 data；其他端 GET 由 uni.request 转换为 query
  const requestData = method === 'GET' && isH5() ? undefined : data

  if (token) {
    header.Authorization = `Bearer ${token}`
  }
  // 客户端渠道标识：服务端按渠道渲染支付方式与解析登录/支付凭证
  header['X-Channel-Code'] = CLIENT_CHANNEL

  // #ifndef H5
  if (!hideLoading) {
    uni.showLoading({ title: '加载中', mask: true })
  }
  // #endif

  return new Promise((resolve, reject) => {
    // 超时交由 uni.request 的 timeout 处理：不再额外挂 setTimeout 兜底，
    // 避免每个请求遗留一个 REQUEST_TIMEOUT+2s、成功/失败都不清的定时器闭包
    uni.request({
      url: fullUrl,
      method,
      data: requestData,
      header,
      timeout: REQUEST_TIMEOUT,
      success: (res) => {
        const resp = res.data as ApiResponse<T>
        if (res.statusCode === 401) {
          reject(new UnauthorizedError('登录已过期'))
          return
        }
        if (resp && UNAUTHORIZED_CODES.includes(resp.code)) {
          reject(new UnauthorizedError(resp.message || '登录已过期'))
          return
        }
        if (resp && resp.code !== SUCCESS_CODE) {
          if (!silent) showError(resp.message || '请求失败')
          reject(new Error(resp.message || '请求失败'))
          return
        }
        resolve(resp)
      },
      fail: (err) => {
        if (!silent) showError(err.errMsg || '网络异常')
        reject(new Error(err.errMsg || '网络异常'))
      },
      complete: () => {
        // #ifndef H5
        if (!hideLoading) {
          uni.hideLoading()
        }
        // #endif
      },
    })
  })
}

/**
 * 统一请求入口：401 时静默续期并重放原请求（仅重放 1 次）
 * 续期失败（refresh_token 缺失/已过 7 天有效期）时清空登录态跳转登录页
 *
 * 使用示例：
 *   const res = await get<UserInfo>('/user/info') // 过期时自动续期，业务无感
 *
 * @param options 请求配置
 * @returns Promise<ApiResponse<T>>
 * @throws Error 静默续期失败、网络异常或业务失败
 */
export async function request<T = unknown>(
  options: RequestOptions,
): Promise<ApiResponse<T>> {
  try {
    return await requestOnce<T>(options)
  } catch (err) {
    if (!(err instanceof UnauthorizedError)) throw err
    // 显式声明跳过续期的请求（登出通知等）：401 原样上抛，由调用方自行忽略，
    // 不再走 silentRefresh → handleUnauthorized 链路
    if (options.bypassRefresh) throw err
    const ok = await silentRefresh()
    if (!ok) throw new Error('登录已过期')
    // requestOnce 每次调用重新读取本地 token，重放自然携带新令牌
    return requestOnce<T>(options)
  }
}

/**
 * GET 请求快捷方法
 * @param url 接口地址
 * @param params URL 参数
 * @param header 自定义请求头
 * @param extra 额外选项（silent 静默失败等）
 */
export function get<T = unknown>(
  url: string,
  params?: Record<string, unknown>,
  header?: Record<string, string>,
  extra?: Pick<RequestOptions, 'silent'>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'GET', data: params, header, ...extra })
}

/**
 * POST 请求快捷方法
 * @param url 接口地址
 * @param data 请求体
 * @param header 自定义请求头
 * @param extra 额外选项（silent 静默失败、bypassRefresh 跳过静默续期）
 */
export function post<T = unknown>(
  url: string,
  data?: Record<string, unknown>,
  header?: Record<string, string>,
  extra?: Pick<RequestOptions, 'silent' | 'bypassRefresh'>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'POST', data, header, ...extra })
}

/**
 * PUT 请求快捷方法
 * @param url 接口地址
 * @param data 请求体
 * @param header 自定义请求头
 */
export function put<T = unknown>(
  url: string,
  data?: Record<string, unknown>,
  header?: Record<string, string>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'PUT', data, header })
}

/**
 * DELETE 请求快捷方法
 * @param url 接口地址
 * @param data 请求体
 * @param header 自定义请求头
 */
export function del<T = unknown>(
  url: string,
  data?: Record<string, unknown>,
  header?: Record<string, string>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'DELETE', data, header })
}

/**
 * 单次上传（multipart/form-data，uni.uploadFile），不做静默续期与重放
 * token 注入与 401 处理与 requestOnce 一致；响应体为 JSON 字符串需手动解析
 * @param url 接口地址（如 /upload/image）
 * @param filePath 本地文件路径（uni.chooseImage 返回）
 * @param name 文件字段名，默认 file
 * @returns Promise<ApiResponse<T>>
 * @throws UnauthorizedError 登录态失效；Error 网络异常或业务失败
 */
function uploadOnce<T = unknown>(
  url: string,
  filePath: string,
  name = 'file',
): Promise<ApiResponse<T>> {
  const token = getToken()
  const fullUrl = url.startsWith('http') ? url : `${BASE_URL}${url}`
  return new Promise((resolve, reject) => {
    uni.uploadFile({
      url: fullUrl,
      filePath,
      name,
      header: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        'X-Channel-Code': CLIENT_CHANNEL,
      },
      timeout: REQUEST_TIMEOUT,
      success: (res) => {
        if (res.statusCode === 401) {
          reject(new UnauthorizedError('登录已过期'))
          return
        }
        let resp: ApiResponse<T> | null = null
        try {
          resp = JSON.parse(res.data) as ApiResponse<T>
        } catch {
          reject(new Error('响应解析失败'))
          return
        }
        if (resp && UNAUTHORIZED_CODES.includes(resp.code)) {
          reject(new UnauthorizedError(resp.message || '登录已过期'))
          return
        }
        if (resp && resp.code !== SUCCESS_CODE) {
          showError(resp.message || '上传失败')
          reject(new Error(resp.message || '上传失败'))
          return
        }
        resolve(resp as ApiResponse<T>)
      },
      fail: (err) => {
        showError(err.errMsg || '上传失败')
        reject(new Error(err.errMsg || '上传失败'))
      },
    })
  })
}

/**
 * 统一上传入口：401 时静默续期并重放上传（仅重放 1 次）
 * @param url 接口地址
 * @param filePath 本地文件路径
 * @param name 文件字段名，默认 file
 * @returns Promise<ApiResponse<T>>
 * @throws Error 静默续期失败、网络异常或业务失败
 */
export async function upload<T = unknown>(
  url: string,
  filePath: string,
  name = 'file',
): Promise<ApiResponse<T>> {
  try {
    return await uploadOnce<T>(url, filePath, name)
  } catch (err) {
    if (!(err instanceof UnauthorizedError)) throw err
    const ok = await silentRefresh()
    if (!ok) throw new Error('登录已过期')
    return uploadOnce<T>(url, filePath, name)
  }
}
