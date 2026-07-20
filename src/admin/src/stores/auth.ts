import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import Cookies from 'js-cookie'
import { login as loginApi, getAdminInfo, refreshToken as refreshTokenApi } from '@/api/auth'
import type { AdminInfo, LoginParams } from '@/api/types'

/**
 * 认证状态管理 Store
 * 存储 token、管理员信息、权限列表，提供登录、登出、刷新 token 等方法
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

  /** 是否已登录 */
  const isAuthenticated = computed(() => !!accessToken.value)
  /** 是否为超管 */
  const isSuperAdmin = computed(() => admin.value?.role_code === 'super_admin' || admin.value?.role_name === '超级管理员')

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
   * 设置管理员信息和权限
   * @param info 管理员信息
   */
  function setAdminInfo(info: AdminInfo) {
    admin.value = info
    permissions.value = info.permissions || []
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
   * 登出
   * 清除 token 和管理员信息
   */
  function logout() {
    Cookies.remove('access_token', { path: '/' })
    Cookies.remove('refresh_token', { path: '/' })
    accessToken.value = ''
    refreshTokenValue.value = ''
    admin.value = null
    permissions.value = []
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
   * 拉取当前管理员信息
   * @returns 是否获取成功
   */
  async function fetchAdminInfo() {
    if (!isAuthenticated.value) return false
    const res = await getAdminInfo()
    setAdminInfo(res.data.data)
    return true
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
    isAuthenticated,
    isSuperAdmin,
    login,
    logout,
    refreshToken,
    fetchAdminInfo,
    hasPermission,
  }
})
