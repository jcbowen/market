import {
  BASE_URL,
  REQUEST_TIMEOUT,
  TOKEN_KEY,
  REFRESH_TOKEN_KEY,
  SUCCESS_CODE,
  UNAUTHORIZED_CODES,
} from './config'
import type { LoginResult } from './auth'
import { isH5 } from '@/utils/platform'

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
 * 处理未登录：清空本地令牌并跳转登录页
 * 用于静默续期不可用（无 refresh_token / refresh_token 已失效）时的兜底
 */
function handleUnauthorized(): void {
  uni.removeStorageSync(TOKEN_KEY)
  uni.removeStorageSync(REFRESH_TOKEN_KEY)
  // 避免登录页重复跳转
  const pages = getCurrentPages()
  const current = pages[pages.length - 1]
  if (current && current.route?.includes('pages/login/login')) {
    return
  }
  uni.navigateTo({ url: '/pages/login/login' })
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
 * 失败语义：
 * - refresh_token 缺失或被服务端判定无效：清空登录态并跳转登录页
 * - 网络异常：保留本地令牌，下次请求再触发重试（不误杀登录态）
 *
 * @returns 刷新成功返回 true；失败返回 false
 */
async function doRefresh(): Promise<boolean> {
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
  const { url, method = 'GET', data, header = {}, hideLoading = false } = options
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

  // #ifndef H5
  if (!hideLoading) {
    uni.showLoading({ title: '加载中', mask: true })
  }
  // #endif

  return new Promise((resolve, reject) => {
    // uni.promisify.adaptor 下类型推断可能落到 Promise 分支，运行时回调形式返回 RequestTask
    const task = uni.request({
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
          showError(resp.message || '请求失败')
          reject(new Error(resp.message || '请求失败'))
          return
        }
        resolve(resp)
      },
      fail: (err) => {
        showError(err.errMsg || '网络异常')
        reject(new Error(err.errMsg || '网络异常'))
      },
      complete: () => {
        // #ifndef H5
        if (!hideLoading) {
          uni.hideLoading()
        }
        // #endif
      },
    }) as unknown as { abort?: () => void }

    // 超时兜底
    setTimeout(() => {
      try {
        task.abort && task.abort()
      } catch {
        // ignore
      }
    }, REQUEST_TIMEOUT + 2000)
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
 */
export function get<T = unknown>(
  url: string,
  params?: Record<string, unknown>,
  header?: Record<string, string>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'GET', data: params, header })
}

/**
 * POST 请求快捷方法
 * @param url 接口地址
 * @param data 请求体
 * @param header 自定义请求头
 */
export function post<T = unknown>(
  url: string,
  data?: Record<string, unknown>,
  header?: Record<string, string>,
): Promise<ApiResponse<T>> {
  return request<T>({ url, method: 'POST', data, header })
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
      header: token ? { Authorization: `Bearer ${token}` } : {},
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
