import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import Cookies from 'js-cookie'
import { login as loginApi, getAdminInfo, refreshToken as refreshTokenApi } from '@/api/auth'
import { useTabsStore } from '@/stores/tabs'
import type { AdminInfo, LoginParams } from '@/api/types'

/**
 * 认证状态机状态
 * - unauthenticated: 无有效登录态
 * - restoring: 存在 token，正在恢复管理员信息（刷新页面后的瞬态）
 * - ready: 登录态完整（token + 管理员信息 + 权限列表）
 */
export type AuthStatus = 'unauthenticated' | 'restoring' | 'ready'

/**
 * 认证状态管理 Store
 * 存储 token、管理员信息、权限列表与认证状态，提供登录、登出、状态恢复等方法
 */
export const useAuthStore = defineStore('auth', () => {
  /** 访问 token */
  const accessToken = ref(Cookies.get('access_token') || '')
  /** 刷新 token */
  const refreshTokenValue = ref(Cookies.get('refresh_token') || '')
  /** 当前管理员信息 */
  const admin = ref<AdminInfo | null>(null)
  /** 权限列表 */
  const permissions = ref<string[]>([])
  /** 认证状态机当前状态：有 token 说明是刷新页面进入，需要先恢复信息 */
  const status = ref<AuthStatus>(accessToken.value ? 'restoring' : 'unauthenticated')

  /** 是否已登录 */
  const isAuthenticated = computed(() => !!accessToken.value)
  /** 登录态是否完整就绪（信息与权限已加载） */
  const isReady = computed(() => status.value === 'ready')
  /** 是否为超管 */
  const isSuperAdmin = computed(() => admin.value?.role_code === 'super_admin' || admin.value?.role_name === '超级管理员')

  /** 进行中的恢复请求单例，保证并发调用幂等（只发一次请求） */
  let restorePromise: Promise<boolean> | null = null

  /**
   * 设置 token 到 cookie 和响应式状态
   * @param token 访问 token
   * @param refresh 刷新 token
   * @param expiresIn 过期时间（秒）
   */
  function setTokens(token: string, refresh: string, expiresIn: number) {
    const expiresDay = expiresIn / 86400
    Cookies.set('access_token', token, { expires: expiresDay, path: '/' })
    Cookies.set('refresh_token', refresh, { expires: expiresDay * 2, path: '/' })
    accessToken.value = token
    refreshTokenValue.value = refresh
  }

  /**
   * 设置管理员信息和权限，状态机转移至 ready
   * 仅在仍持有 token 时转移，避免登出与恢复请求并发时状态回跳
   * @param info 管理员信息
   */
  function setAdminInfo(info: AdminInfo) {
    admin.value = info
    permissions.value = info.permissions || []
    if (isAuthenticated.value) {
      status.value = 'ready'
    }
  }

  /**
   * 登录
   * @param params 登录参数
   * @returns 是否登录成功
   */
  async function login(params: LoginParams) {
    const res = await loginApi(params)
    const data = res.data.data
    setTokens(data.access_token, data.refresh_token, data.expires_in)
    setAdminInfo(data.admin)
    return true
  }

  /**
   * 登出：清空 token 与管理员信息，状态机转移至 unauthenticated，并同步清空标签页
   */
  function logout() {
    Cookies.remove('access_token', { path: '/' })
    Cookies.remove('refresh_token', { path: '/' })
    accessToken.value = ''
    refreshTokenValue.value = ''
    admin.value = null
    permissions.value = []
    status.value = 'unauthenticated'
    // 清空标签页，避免残留上一个账号的标签与页面缓存
    useTabsStore().reset()
  }

  /**
   * 刷新访问 token
   * @returns 是否刷新成功
   */
  async function refreshToken() {
    const token = refreshTokenValue.value
    if (!token) return false
    const res = await refreshTokenApi(token)
    const data = res.data.data
    setTokens(data.access_token, data.refresh_token, data.expires_in)
    return true
  }

  /**
   * 拉取当前管理员信息，成功后状态机转移至 ready
   * @returns 是否获取成功
   */
  async function fetchAdminInfo() {
    if (!isAuthenticated.value) return false
    const res = await getAdminInfo()
    setAdminInfo(res.data.data)
    return true
  }

  /**
   * 确保登录态完整就绪（状态机转移至 ready）
   * - ready：直接成功
   * - restoring：发起（或复用进行中的）恢复请求，失败时自动登出转移至 unauthenticated
   * 并发调用共享同一请求，保证幂等
   * @returns 是否就绪
   */
  async function ensureReady(): Promise<boolean> {
    if (status.value === 'ready') return true
    restorePromise ??= fetchAdminInfo()
      .then(() => true)
      .catch(() => false)
      .finally(() => {
        restorePromise = null
      })
    const ok = await restorePromise
    if (!ok) {
      logout()
    }
    return ok
  }

  /**
   * 判断当前管理员是否拥有指定权限
   * @param code 权限码
   * @returns 是否拥有权限
   */
  function hasPermission(code: string) {
    if (isSuperAdmin.value) return true
    return permissions.value.includes(code)
  }

  return {
    accessToken,
    refreshTokenValue,
    admin,
    permissions,
    status,
    isAuthenticated,
    isReady,
    isSuperAdmin,
    login,
    logout,
    refreshToken,
    fetchAdminInfo,
    ensureReady,
    hasPermission,
  }
})
