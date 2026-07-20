import {
  BASE_URL,
  REQUEST_TIMEOUT,
  TOKEN_KEY,
  SUCCESS_CODE,
  UNAUTHORIZED_CODES,
} from './config'

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
 */
type Method = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH'

/**
 * 通用请求选项
 */
interface RequestOptions {
  url: string
  method?: Method
  data?: Record<string, unknown> | unknown
  header?: Record<string, string>
  hideLoading?: boolean
}

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
 * 处理 401 未登录，跳转登录页
 */
function handleUnauthorized(): void {
  uni.removeStorageSync(TOKEN_KEY)
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
 * 发送 HTTP 请求并返回 Promise
 * @param options 请求配置
 * @returns Promise<ApiResponse<T>>
 * @throws 业务错误时返回 rejected Promise
 */
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

export function request<T = unknown>(
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

  if (token) {
    header.Authorization = `Bearer ${token}`
  }

  // #ifndef H5
  if (!hideLoading) {
    uni.showLoading({ title: '加载中', mask: true })
  }
  // #endif

  return new Promise((resolve, reject) => {
    const task = uni.request({
      url: fullUrl,
      method,
      // H5 下 GET 请求已手动拼接 query string，不再传递 data，避免重复或 body 序列化问题
      // #ifdef H5
      data: method === 'GET' ? undefined : data,
      // #endif
      // #ifndef H5
      data,
      // #endif
      header,
      timeout: REQUEST_TIMEOUT,
      success: (res) => {
        const resp = res.data as ApiResponse<T>
        if (res.statusCode === 401) {
          handleUnauthorized()
          reject(new Error('登录已过期'))
          return
        }
        if (resp && UNAUTHORIZED_CODES.includes(resp.code)) {
          handleUnauthorized()
          reject(new Error(resp.message || '登录已过期'))
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
    })

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
