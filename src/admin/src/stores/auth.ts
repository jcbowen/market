import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import Cookies from 'js-cookie'
import { login as loginApi, getAdminInfo, logout as logoutApi, refreshToken as refreshTokenApi } from '@/api/auth'
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
 * 同时维护"当前社区"（管理员多社区绑定时可切换的数据域），持久化到 localStorage
 */
export const useAuthStore = defineStore('auth', () => {
  /** 当前社区 ID 的 localStorage 持久化键 */
  const CURRENT_COMMUNITY_KEY = 'current_community_id'

  /** 访问 token */
  const accessToken = ref(Cookies.get('access_token') || '')
  /** 当前管理员信息 */
  const admin = ref<AdminInfo | null>(null)
  /** 权限列表 */
  const permissions = ref<string[]>([])
  /**
   * 当前社区 ID
   * 超管与普通管理员一致：必须选定一个具体社区，表示当前在操作哪个社区的商超。
   * - 普通管理员：只能切换到自己绑定的社区，默认取绑定列表第一个
   * - 超管：可切换到任意社区；0 仅表示"尚未选定"的瞬态，由 Layout 加载社区列表后自动补选
   * 请求拦截器读取持久化值注入 X-Community-Id 请求头
   */
  const currentCommunityId = ref<number>(loadPersistedCommunityId())
  /** 认证状态机当前状态：有 token 说明是刷新页面进入，需要先恢复信息 */
  const status = ref<AuthStatus>(accessToken.value ? 'restoring' : 'unauthenticated')

  /** 是否已登录 */
  const isAuthenticated = computed(() => !!accessToken.value)
  /** 登录态是否完整就绪（信息与权限已加载） */
  const isReady = computed(() => status.value === 'ready')
  /** 是否为超管（新模型看 is_super 标记，role_code 兼容旧 token 场景） */
  const isSuperAdmin = computed(
    () => !!admin.value?.is_super || admin.value?.role_code === 'super_admin' || admin.value?.role_name === '超级管理员',
  )
  /** 当前管理员绑定的社区 ID 列表 */
  const communityIds = computed(() => admin.value?.community_ids ?? [])

  /** 进行中的恢复请求单例，保证并发调用幂等（只发一次请求） */
  let restorePromise: Promise<boolean> | null = null

  /**
   * 从 localStorage 读取持久化的当前社区 ID
   * @returns 有效的社区 ID，无记录或值非法时返回 0
   */
  function loadPersistedCommunityId(): number {
    const raw = localStorage.getItem(CURRENT_COMMUNITY_KEY)
    const id = Number.parseInt(raw ?? '', 10)
    return Number.isFinite(id) && id >= 0 ? id : 0
  }

  /**
   * 持久化当前社区 ID 到 localStorage
   */
  function persistCommunityId() {
    localStorage.setItem(CURRENT_COMMUNITY_KEY, String(currentCommunityId.value))
  }

  /**
   * 归一化当前社区 ID
   * 校验已选社区是否仍对当前管理员合法：
   * - 超管：已选具体社区保持；0（未选定）保留，由社区选择页强制选定
   * - 普通管理员：仅绑定一个社区（或旧字段有值）时自动选定；
   *   绑定多个社区且本地无上次选择记录时保持 0，由社区选择页手动选定
   */
  function normalizeCommunityId() {
    if (isSuperAdmin.value) {
      if (currentCommunityId.value < 0) {
        currentCommunityId.value = 0
      }
      persistCommunityId()
      return
    }
    const ids = communityIds.value
    const valid = currentCommunityId.value > 0 && ids.includes(currentCommunityId.value)
    if (valid) {
      persistCommunityId()
      return
    }
    // 仅绑定一个社区：自动选定；绑定多个社区且无上次选择：保持 0 交由社区选择页；
    // 无绑定（存量账号）：回退旧默认社区字段
    const fallback = ids.length === 1 ? ids[0] : (ids.length === 0 ? (admin.value?.community_id ?? 0) : 0)
    currentCommunityId.value = fallback
    persistCommunityId()
  }

  /**
   * 是否需要进入社区选择页
   * 登录态就绪且当前社区未选定（0）时为 true：
   * - 普通管理员：绑定多个社区且本地无上次选择记录
   * - 超管：尚未选定社区（含首次进入与主动进入选择页）
   */
  const needsCommunitySelection = computed(() => isReady.value && currentCommunityId.value <= 0)

  /**
   * 切换当前社区
   * 超管与普通管理员都必须选定具体社区（0 仅作为未选定瞬态，不接受切换）
   * @param id 目标社区 ID
   * @returns 是否切换成功（目标社区不合法时失败）
   */
  function switchCommunity(id: number): boolean {
    if (id <= 0) return false
    if (!isSuperAdmin.value && !communityIds.value.includes(id)) {
      return false
    }
    currentCommunityId.value = id
    persistCommunityId()
    return true
  }

  /**
   * 写入访问 token 到 cookie 与响应式状态
   * refresh_token 不在此列：由后端下发 httpOnly cookie（F2），JS 读不到也无需读
   * @param token 访问 token
   * @param expiresIn 过期时间（秒）
   */
  function setTokens(token: string, expiresIn: number) {
    const expiresDay = expiresIn / 86400
    // sameSite 必须显式声明为 lax：网关的 jcbaseGo Cors 会回显任意 Origin 并置
    // Access-Control-Allow-Credentials: true，靠 Lax 挡住跨站 XHR 携带 cookie，
    // 不能依赖浏览器默认行为
    Cookies.set('access_token', token, { expires: expiresDay, path: '/', sameSite: 'lax' })
    accessToken.value = token
  }

  /**
   * 设置管理员信息和权限，状态机转移至 ready
   * 仅在仍持有 token 时转移，避免登出与恢复请求并发时状态回跳
   * 同时归一化当前社区（管理员绑定关系可能较上次会话发生变化）
   * @param info 管理员信息
   */
  function setAdminInfo(info: AdminInfo) {
    admin.value = info
    permissions.value = info.permissions || []
    if (isAuthenticated.value) {
      status.value = 'ready'
    }
    normalizeCommunityId()
  }

  /**
   * 登录
   * @param params 登录参数
   * @returns 是否登录成功
   */
  async function login(params: LoginParams) {
    const res = await loginApi(params)
    const data = res.data.data
    setTokens(data.access_token, data.expires_in)
    setAdminInfo(data.admin)
    return true
  }

  /**
   * 登出：清空 token 与管理员信息，状态机转移至 unauthenticated，并同步清空标签页
   * 同时请后端清除 refresh_token 的 httpOnly cookie（JS 删不掉，只能后端下发过期值）；
   * 该请求失败不阻塞本地登出——cookie 最长 7 天后自然过期
   * 保留持久化的当前社区（"上次选中的社区"），同浏览器下次登录优先沿用；
   * 更换账号时由归一化逻辑校验：不合法则回退到新账号绑定列表的第一个社区
   */
  function logout() {
    Cookies.remove('access_token', { path: '/' })
    accessToken.value = ''
    admin.value = null
    permissions.value = []
    status.value = 'unauthenticated'
    void logoutApi().catch(() => {})
    // 清空标签页，避免残留上一个账号的标签与页面缓存
    useTabsStore().reset()
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
   * 静默续期访问令牌（refresh_token 经 httpOnly cookie 自动携带）
   * 供 WS 鉴权失败重连前探测等低打扰场景调用：成功则刷新本地 token 与 cookie；
   * 失败（refresh 已过期/账号被停用/网络不可达）不在此处跳登录，
   * 交由 HTTP 请求拦截器统一处理，调用方据返回值决定是否停止重试
   * @returns 是否续期成功
   */
  async function refreshAccessToken(): Promise<boolean> {
    const res = await refreshTokenApi()
    if (!res.ok || !res.accessToken) return false
    setTokens(res.accessToken, res.expiresIn && res.expiresIn > 0 ? res.expiresIn : 7200)
    return true
  }

  /**
   * 判断当前管理员是否拥有指定权限
   * 权限列表按当前社区语境解析，含 "*" 通配时视为全量
   * @param code 权限码
   * @returns 是否拥有权限
   */
  function hasPermission(code: string) {
    if (isSuperAdmin.value) return true
    return permissions.value.includes('*') || permissions.value.includes(code)
  }

  return {
    accessToken,
    admin,
    permissions,
    status,
    currentCommunityId,
    communityIds,
    isAuthenticated,
    isReady,
    isSuperAdmin,
    needsCommunitySelection,
    login,
    logout,
    fetchAdminInfo,
    ensureReady,
    refreshAccessToken,
    hasPermission,
    switchCommunity,
  }
})
