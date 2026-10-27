import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'

/**
 * 多标签页播报选主（utils/notifyLeader.ts）用例
 *
 * 锁的是三级降级链的选主语义：locks 模式下非 leader 静默、持锁即 leader；
 * channel 认领模式先到先得、本 tab 重复事件去重；都不可用时维持直通。
 * Web Locks / BroadcastChannel 均以替身模拟，不依赖真实浏览器实现。
 *
 * 运行方式：npm test（src/admin 目录）
 */
import { notifyLeaderMode, runAsAnnouncer, setupNotifyLeader, __resetNotifyLeaderForTest }
  from '@/utils/notifyLeader'

/** BroadcastChannel 替身：广播给其他实例（不回声自身，与真实语义一致） */
class FakeBroadcastChannel {
  static instances: FakeBroadcastChannel[] = []
  onmessage: ((e: MessageEvent) => void) | null = null
  constructor(public name: string) {
    FakeBroadcastChannel.instances.push(this)
  }
  postMessage(data: unknown) {
    for (const ch of FakeBroadcastChannel.instances) {
      if (ch !== this) ch.onmessage?.({ data } as MessageEvent)
    }
  }
  close() {
    const i = FakeBroadcastChannel.instances.indexOf(this)
    if (i >= 0) FakeBroadcastChannel.instances.splice(i, 1)
  }
}

/** locks 替身：记录回调，由用例手动决定何时持到锁 */
let locksRequest: ((name: string, cb: (lock: unknown) => Promise<void>) => Promise<unknown>) | null

beforeEach(() => {
  __resetNotifyLeaderForTest()
  vi.useFakeTimers()
  FakeBroadcastChannel.instances = []
  locksRequest = null
  // happy-dom navigator 默认无 locks；BroadcastChannel 用替身注入
  vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel as unknown as typeof BroadcastChannel)
  Object.defineProperty(navigator, 'locks', {
    configurable: true,
    get: () => locksRequest && {
      request: (name: string, cb: (lock: unknown) => Promise<void>) => locksRequest!(name, cb),
    },
  })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  delete (navigator as { locks?: unknown }).locks
})

describe('选主降级链', () => {
  it('locks 可用时选 locks 模式', () => {
    locksRequest = () => new Promise(() => {})
    setupNotifyLeader()
    expect(notifyLeaderMode()).toBe('locks')
  })

  it('locks 不可用时降级 channel 认领模式', () => {
    locksRequest = null
    setupNotifyLeader()
    expect(notifyLeaderMode()).toBe('channel')
  })

  it('locks 与 BroadcastChannel 都不可用时维持直通（不去重）', () => {
    locksRequest = null
    vi.stubGlobal('BroadcastChannel', undefined as unknown as typeof BroadcastChannel)
    setupNotifyLeader()
    expect(notifyLeaderMode()).toBe('none')

    let fired = 0
    runAsAnnouncer('paid-A1', () => fired++)
    runAsAnnouncer('paid-A1', () => fired++)
    expect(fired).toBe(2)
  })

  it('重复初始化不改变既有模式', () => {
    locksRequest = () => new Promise(() => {})
    setupNotifyLeader()
    setupNotifyLeader()
    expect(notifyLeaderMode()).toBe('locks')
  })
})

describe('locks 模式', () => {
  it('未持锁的 tab 静默，持锁后开始播报（接管生效）', async () => {
    let grantLeader: (() => void) | undefined
    locksRequest = (_name, cb) =>
      new Promise((resolve) => {
        grantLeader = () => resolve(cb({}))
      })
    setupNotifyLeader()

    let fired = 0
    runAsAnnouncer('paid-B1', () => fired++)
    expect(fired).toBe(0)

    // leader tab 关闭，锁释放后本 tab 接管；resolve 的回调在微任务里执行，须 await
    await grantLeader?.()
    runAsAnnouncer('paid-B2', () => fired++)
    expect(fired).toBe(1)
  })

  it('leader 对同一事实只播报一次', () => {
    locksRequest = (_name, cb) => Promise.resolve(cb({}))
    setupNotifyLeader()

    let fired = 0
    runAsAnnouncer('paid-B3', () => fired++)
    runAsAnnouncer('paid-B3', () => fired++)
    expect(fired).toBe(1)
  })

  it('去重键集合有界（收银页长开不随订单量线性膨胀）', () => {
    locksRequest = (_name, cb) => Promise.resolve(cb({}))
    setupNotifyLeader()

    let fired = 0
    // 超过容量上限灌入：逐条都该播报（都是新事实），但内部集合不得无限增长
    for (let i = 0; i < 1200; i++) runAsAnnouncer(`paid-cap-${i}`, () => fired++)
    expect(fired).toBe(1200)
    // 最早写入的键已被淘汰，重新投递会再播一次——去重只在近期窗口内有效，属预期
    runAsAnnouncer('paid-cap-0', () => fired++)
    expect(fired).toBe(1201)
  })
})

describe('channel 认领模式', () => {
  it('本 tab 先认领则播报并向其他 tab 广播 claim', () => {
    setupNotifyLeader()

    let fired = 0
    runAsAnnouncer('paid-C1', () => fired++)
    expect(fired).toBe(0) // 抖动窗口内尚未认领

    vi.advanceTimersByTime(200)
    expect(fired).toBe(1)
    // claim 已广播（被其他 tab 收到）
    expect(FakeBroadcastChannel.instances[0]?.onmessage).toBeTruthy()
  })

  it('他人先认领（抖动窗口内收到 claim）则静默', () => {
    setupNotifyLeader()
    const ch = FakeBroadcastChannel.instances[0]

    let fired = 0
    runAsAnnouncer('paid-C2', () => fired++)
    // 另一 tab 抢先认领了同一事实
    ch.onmessage?.({ data: { key: 'paid-C2' } } as MessageEvent)
    vi.advanceTimersByTime(200)
    expect(fired).toBe(0)
  })

  it('claim 先于本 tab 播报到达时直接静默', () => {
    setupNotifyLeader()
    const ch = FakeBroadcastChannel.instances[0]
    ch.onmessage?.({ data: { key: 'paid-C3' } } as MessageEvent)

    let fired = 0
    runAsAnnouncer('paid-C3', () => fired++)
    vi.advanceTimersByTime(200)
    expect(fired).toBe(0)
  })

  it('不同 key 互不影响', () => {
    setupNotifyLeader()

    let fired = 0
    runAsAnnouncer('paid-D1', () => fired++)
    runAsAnnouncer('paid-D2', () => fired++)
    vi.advanceTimersByTime(200)
    expect(fired).toBe(2)
  })
})
