import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'

/**
 * 认证 store 与 401 收口的耦合用例（2026-10-27）
 *
 * 为什么单独锁这一条：`api/request` 的会话收口用模块级单次标志防并发重复跳转，
 * 而跳登录只改 hash（同一文档不重载），标志必须在新令牌写入时复位，
 * 否则「重新登录后的下一次会话过期」不再清凭证、不再跳登录。
 * 复位调用点只有一个——`setTokens`；本文件锁「两条写入新令牌的路径都会复位、
 * 失败路径不复位」，将来有人把复位从 setTokens 挪走时这里先红。
 *
 * 运行方式：
 *   npm test（src/admin 目录）
 */

const { login, getAdminInfo, logout, refreshToken, resetSessionGuard } = vi.hoisted(() => ({
  login: vi.fn(),
  getAdminInfo: vi.fn(),
  logout: vi.fn(),
  refreshToken: vi.fn(),
  resetSessionGuard: vi.fn(),
}))

vi.mock('@/api/auth', () => ({ login, getAdminInfo, logout, refreshToken }))

// 只替换 resetSessionGuard（断言调用点），其余导出保持真实实现（store 还用到 registerUnauthorizedHandler）
vi.mock('@/api/request', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/request')>()),
  resetSessionGuard,
}))

/** 后端登录/信息接口返回的管理员形状（只给到归一化社区所需的字段） */
const adminInfo = {
  id: 1,
  username: 'tester',
  is_super: false,
  permissions: ['order:view'],
  community_ids: [7],
  community_id: 7,
}

/** 登录接口响应（拦截器约定的 res.data.data 形状） */
const loginPayload = {
  access_token: 'token-new',
  expires_in: 7200,
  admin: adminInfo,
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  // 后端登出是「清 refresh cookie」的 fire-and-forget 调用，给个可 await 的壳
  logout.mockResolvedValue({})
  document.cookie = 'access_token=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT'
})

describe('useAuthStore 写入新令牌', () => {
  it('登录成功写 token 时复位 401 收口', async () => {
    login.mockResolvedValue({ data: { data: loginPayload } })
    const store = useAuthStore()

    await expect(store.login({ username: 'tester', password: 'pwd' })).resolves.toBe(true)

    expect(store.accessToken).toBe('token-new')
    expect(resetSessionGuard).toHaveBeenCalledTimes(1)
  })

  it('静默续期成功同样复位（续期也是新会话令牌）', async () => {
    refreshToken.mockResolvedValue({ ok: true, accessToken: 'token-refresh', expiresIn: 7200 })
    const store = useAuthStore()

    await expect(store.refreshAccessToken()).resolves.toBe(true)

    expect(store.accessToken).toBe('token-refresh')
    expect(resetSessionGuard).toHaveBeenCalledTimes(1)
  })

  it('续期失败不写令牌、不复位（会话仍由拦截器统一收口）', async () => {
    refreshToken.mockResolvedValue({ ok: false })
    const store = useAuthStore()

    await expect(store.refreshAccessToken()).resolves.toBe(false)

    expect(resetSessionGuard).not.toHaveBeenCalled()
  })

  it('登出清空 token 与权限并回到未认证态', async () => {
    login.mockResolvedValue({ data: { data: loginPayload } })
    const store = useAuthStore()
    await store.login({ username: 'tester', password: 'pwd' })

    store.logout()

    expect(store.accessToken).toBe('')
    expect(store.isAuthenticated).toBe(false)
    expect(store.status).toBe('unauthenticated')
  })
})
