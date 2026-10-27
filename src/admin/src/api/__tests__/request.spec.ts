import { AxiosError, type AxiosResponse, type InternalAxiosRequestConfig } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * 401 会话收口单测（2026-10-27 复位回归守卫）
 *
 * 盯的缺陷：收口用模块级单次标志 `sessionClosed` 防「一屏并发 401 重复跳转」，
 * 而跳登录只改 hash（同一文档不重载），该标志会跨越整个文档生命周期——
 * 不在写入新令牌处复位的话，「重新登录后的下一次会话过期」不再清凭证、不再跳登录，
 * 用户静默卡在坏会话里。复位挂在 `stores/auth` 的 `setTokens`（见 stores 侧同名用例）。
 *
 * 运行方式：
 *   npm test（src/admin 目录）
 *
 * 覆盖：
 *   - 认证失效两条入口（HTTP 200 + 业务码 11001 / HTTP 401）都触发收口回调；
 *   - 并发重复失效只收口一次（去重语义不因加入复位而退化）；
 *   - 复位后再次失效仍能收口（本文件要防的回归本体）；
 *   - 收银台路径失效回跳 /pos/login 并带 redirect，主后台回跳 /login。
 *
 * 说明：`sessionClosed` 是模块私有状态，故每个用例用 `vi.resetModules()` 重新加载模块，
 * 用例之间互不污染
 */

/** api/request 的模块形状（用于 resetModules 后的动态导入类型） */
type RequestModule = typeof import('@/api/request')

/**
 * 以全新实例加载 api/request
 * 返回值：
 *   - RequestModule: 新模块（sessionClosed 回到 false）
 */
async function loadRequestModule(): Promise<RequestModule> {
  vi.resetModules()
  return await import('@/api/request')
}

/**
 * 伪造「后端返回认证失效」的传输层
 * 参数：
 *   - mod: 目标模块
 *   - kind: biz=HTTP 200 + 业务码 11001（网关 auth 中间件口径）；http=HTTP 401
 */
function mockUnauthorized(mod: RequestModule, kind: 'biz' | 'http'): void {
  const service = mod.default
  const handler = (config: InternalAxiosRequestConfig): Promise<AxiosResponse> => {
    if (kind === 'biz') {
      return Promise.resolve({
        data: { code: 11001, message: '未登录或登录已过期' },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      } as AxiosResponse)
    }
    return Promise.reject(
      new AxiosError('Request failed with status code 401', AxiosError.ERR_BAD_REQUEST, config, undefined, {
        data: {},
        status: 401,
        statusText: 'Unauthorized',
        headers: {},
        config,
      })
    )
  }
  service.defaults.adapter = handler as unknown as typeof service.defaults.adapter
}

/**
 * 发起一次请求并吞掉预期内的 rejection（只关心拦截器的副作用）
 * 参数：
 *   - mod: 目标模块
 */
async function fireOnce(mod: RequestModule): Promise<void> {
  await mod.request({ url: '/api/v1/admin/ping' }).catch(() => undefined)
}

// 收口跳转写的是 window.location：happy-dom 的 window 在同一测试文件内共享，
// vi.resetModules() 只重置模块状态（sessionClosed）而不重置地址，
// 故逐用例把地址归位到中性路由，避免上一个用例的 /pos 跳转污染本用例的判定
beforeEach(() => {
  window.location.hash = '#/dashboard'
  document.cookie = 'access_token=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT'
})

// 单跑每例约 1.8s（成本在 loadRequestModule 的 vi.resetModules + 重新 import axios），
// 全量并发时会被 CPU 争用顶到默认 5s 超时而偶发红，故按套件放宽预算
describe('401 会话收口', { timeout: 20000 }, () => {
  it('业务码 11001 触发收口回调并跳转登录页', async () => {
    const mod = await loadRequestModule()
    const onUnauthorized = vi.fn()
    mod.registerUnauthorizedHandler(onUnauthorized)
    mockUnauthorized(mod, 'biz')

    await fireOnce(mod)

    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(window.location.href).toContain('#/login')
  })

  it('HTTP 401 同样触发收口（两条入口口径一致）', async () => {
    const mod = await loadRequestModule()
    const onUnauthorized = vi.fn()
    mod.registerUnauthorizedHandler(onUnauthorized)
    mockUnauthorized(mod, 'http')

    await fireOnce(mod)

    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('重复失效只收口一次，复位后再次失效仍能收口', async () => {
    const mod = await loadRequestModule()
    const onUnauthorized = vi.fn()
    mod.registerUnauthorizedHandler(onUnauthorized)
    mockUnauthorized(mod, 'biz')

    await fireOnce(mod)
    await fireOnce(mod)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)

    // 重新登录写入新令牌即视为新会话，必须重开收口；漏复位即为本次回归
    mod.resetSessionGuard()
    await fireOnce(mod)
    expect(onUnauthorized).toHaveBeenCalledTimes(2)

    // 复位后的一次性语义仍然成立（不会退化成每次都跳）
    await fireOnce(mod)
    expect(onUnauthorized).toHaveBeenCalledTimes(2)
  })

  it('收银台会话过期回跳 /pos/login 且带 redirect 回跳参数', async () => {
    const mod = await loadRequestModule()
    mod.registerUnauthorizedHandler(vi.fn())
    mockUnauthorized(mod, 'biz')
    window.location.hash = '#/pos/cashier'

    await fireOnce(mod)

    expect(window.location.href).toContain('#/pos/login')
    expect(window.location.href).toContain('redirect=%2Fpos%2Fcashier')
  })

  it('未注册收口回调时兜底删除 access_token cookie（path 与写入侧一致）', async () => {
    const mod = await loadRequestModule()
    mockUnauthorized(mod, 'biz')
    // 与 stores/auth 的写入同口径：path=/，删除也必须带 path，否则删不掉
    document.cookie = 'access_token=abc; path=/'
    expect(document.cookie).toContain('access_token')

    await fireOnce(mod)

    expect(document.cookie).not.toContain('access_token')
    expect(window.location.href).toContain('#/login')
  })
})
