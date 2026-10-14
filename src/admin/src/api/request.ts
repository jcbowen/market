import axios, { AxiosError, type AxiosInstance, type AxiosRequestConfig, type AxiosResponse } from 'axios'
import Cookies from 'js-cookie'
import { createDiscreteApi } from 'naive-ui'

// 在响应拦截器等非 setup 上下文也能安全弹出错误提示
const { message: discreteMessage } = createDiscreteApi(['message'])

/**
 * 创建 axios 实例
 * baseURL 为空，使用相对路径，配合 vite 代理转发到后端服务
 */
const service: AxiosInstance = axios.create({
  baseURL: '',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

/**
 * 当前社区 ID 的 localStorage 持久化键
 * 与 auth store 保持同一约定：store 写入，此处只读（避免拦截器与 Pinia 初始化时序耦合）
 */
const CURRENT_COMMUNITY_KEY = 'current_community_id'

/**
 * 请求拦截器
 * - 从 cookie 读取 access_token，加入 Authorization 请求头
 * - 从 localStorage 读取当前社区 ID，加入 X-Community-Id 请求头
 *   （后端数据域接口据此切换社区，与业务筛选参数 community_id 解耦；
 *   仅注入有效社区 ID，未登录或尚未选定社区（0）时不携带，由后端回退默认社区）
 */
service.interceptors.request.use(
  (config) => {
    const token = Cookies.get('access_token')
    if (token) {
      if (config.headers) {
        config.headers.Authorization = `Bearer ${token}`
      }
      const raw = localStorage.getItem(CURRENT_COMMUNITY_KEY)
      const id = Number.parseInt(raw ?? '', 10)
      if (Number.isFinite(id) && id > 0) {
        config.headers['X-Community-Id'] = String(id)
      }
    }
    return config
  },
  (error: AxiosError) => {
    return Promise.reject(error)
  }
)

/**
 * 认证失败业务码
 * 后端认证中间件以 HTTP 200 + 业务错误码返回认证失败（见 gateway/middleware/auth.go）：
 * 11001 未登录或 token 已过期，11002 token 无效
 */
const CODE_NOT_LOGIN_OR_EXPIRED = 11001
const CODE_INVALID_TOKEN = 11002

/**
 * 清除登录凭证并跳转登录页
 * 与 HTTP 401 分支保持同一处理：静默跳转，不弹错误提示
 */
function redirectToLogin() {
  Cookies.remove('access_token')
  window.location.href = '/b/#/login'
}

/**
 * 响应拦截器
 * 统一处理认证失效跳转登录、错误提示
 */
service.interceptors.response.use(
  (response: AxiosResponse) => {
    const data = response.data
    // 若后端返回业务错误码，统一抛出
    if (data && typeof data.code === 'number' && data.code !== 0 && data.code !== 200) {
      // 认证失效（HTTP 200 + 业务码）：清凭证并跳转登录
      if (data.code === CODE_NOT_LOGIN_OR_EXPIRED || data.code === CODE_INVALID_TOKEN) {
        redirectToLogin()
        return Promise.reject(new Error(data.message || '未登录或登录已过期'))
      }
      const message = data.message || '请求失败'
      tryShowError(message)
      return Promise.reject(new Error(message))
    }
    return response
  },
  (error: AxiosError) => {
    const status = error.response?.status
    const message = (error.response?.data as { message?: string })?.message || error.message || '网络异常'

    if (status === 401) {
      redirectToLogin()
      return Promise.reject(error)
    }

    tryShowError(message)
    return Promise.reject(error)
  }
)

/**
 * 使用 naive-ui 离散 API 显示错误提示
 * 响应拦截器不在 setup 上下文，createDiscreteApi 可安全创建 message 实例
 * @param msg 错误信息
 */
function tryShowError(msg: string) {
  discreteMessage.error(msg)
}

/**
 * 通用请求方法
 * @param config axios 请求配置
 * @returns 响应数据
 */
export function request<T = unknown>(config: AxiosRequestConfig): Promise<AxiosResponse<T>> {
  return service.request(config) as Promise<AxiosResponse<T>>
}

export default service
