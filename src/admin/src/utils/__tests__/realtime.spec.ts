import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * 实时通道连接层（utils/realtime.ts）用例
 *
 * 为什么单独锁这一层：后台的播报、工作台待办、下载中心、收银台、订单列表都是它的
 * 订阅者。两条不变量一旦破掉就会静默失效，且都很难在页面上直接看出来：
 * 1. 订阅关系与 socket 实例解耦——服务端在权限变更送达后会主动踢线（KickAdmins），
 *    连接层自动重连，若订阅挂在连接上，踢线之后页面就再也收不到任何事件；
 * 2. 单个订阅者抛异常不得影响其他订阅者——分发循环一旦被异常打断，
 *    排在后面的播报/刷新会整轮丢失，表现为「偶尔没声音」；
 * 3. 换社区必须真的换连——握手时的 community_id 决定推送范围，幂等早退会让页面
 *    收到旧社区推送（不经整页 reload 的切换入口会踩到）；
 * 4. 连接状态变化要让订阅方立即得知（含显式断开这类不触发 onclose 的路径），
 *    否则按「WS 在线」降频的兜底轮询会一直停在低频档。
 *
 * 运行方式：
 *   npm test（src/admin 目录）
 */

/** 认证 store 的替身：连接层只用到这三个成员 */
const { fakeAuth } = vi.hoisted(() => ({
  fakeAuth: {
    isAuthenticated: true,
    currentCommunityId: 1,
    fetchAdminInfo: vi.fn().mockResolvedValue(undefined),
    refreshAccessToken: vi.fn().mockResolvedValue(true),
  },
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => fakeAuth }))

/** WebSocket 替身：记录实例、可手动模拟服务端下发与关闭 */
class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  /** 全部创建过的实例，按顺序取用 */
  static instances: FakeWebSocket[] = []

  url: string
  readyState = FakeWebSocket.OPEN
  onopen: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: ((ev: unknown) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  /** 模拟服务端下发一条 JSON 消息 */
  serverSend(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) })
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED
    // 浏览器在下一个任务派发 close（同步置 readyState、异步回调），
    // 替身必须同构：否则「调用方已把 socket 置空」这类竞态会被测成假绿
    setTimeout(() => this.onclose?.({}), 0)
  }
}

/** 每个用例都要全新模块状态（realtime 是模块级单例），故重置后动态导入 */
async function importRealtime() {
  vi.resetModules()
  return import('@/utils/realtime')
}

/** 取最近一个连接实例并断言存在 */
function lastSocket(index = -1): FakeWebSocket {
  const sock = FakeWebSocket.instances.at(index)
  if (!sock) throw new Error('没有创建 WebSocket 连接')
  return sock
}

beforeEach(() => {
  FakeWebSocket.instances = []
  vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
  fakeAuth.isAuthenticated = true
  fakeAuth.currentCommunityId = 1
  fakeAuth.fetchAdminInfo.mockClear()
  fakeAuth.refreshAccessToken.mockClear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('事件订阅与分发', () => {
  it('连接后按 type 分发载荷给订阅者', async () => {
    const realtime = await importRealtime()
    const paid: unknown[] = []
    realtime.onRealtime('order.paid', (data) => paid.push(data))
    realtime.connectRealtime()

    lastSocket().serverSend({ type: 'order.paid', data: { order_no: 'A001', pay_amount: 12.5 } })

    expect(paid).toHaveLength(1)
    expect((paid[0] as { order_no: string }).order_no).toBe('A001')
  })

  it('退订函数生效后不再收到事件', async () => {
    const realtime = await importRealtime()
    const refund: unknown[] = []
    const off = realtime.onRealtime('order.refund', (data) => refund.push(data))
    realtime.connectRealtime()

    lastSocket().serverSend({ type: 'order.refund', data: { refund_no: 'R1' } })
    off()
    lastSocket().serverSend({ type: 'order.refund', data: { refund_no: 'R2' } })

    expect(refund).toHaveLength(1)
  })

  it('未知 type 与 hello 不打断分发，也不抛错', async () => {
    const realtime = await importRealtime()
    const paid: unknown[] = []
    realtime.onRealtime('order.paid', (data) => paid.push(data))
    realtime.connectRealtime()
    const sock = lastSocket()

    expect(() => sock.serverSend({ type: 'hello' })).not.toThrow()
    expect(() => sock.serverSend({ type: 'some.future.event', data: { x: 1 } })).not.toThrow()
    expect(() => sock.serverSend('不是 JSON')).not.toThrow()
    sock.serverSend({ type: 'order.paid', data: { order_no: 'A002' } })

    expect(paid).toHaveLength(1)
  })

  it('某个订阅者抛异常不影响同类型的其他订阅者', async () => {
    const realtime = await importRealtime()
    const seen: string[] = []
    realtime.onRealtime('order.paid', () => {
      throw new Error('订阅者内部故障')
    })
    realtime.onRealtime('order.paid', () => seen.push('second'))

    realtime.connectRealtime()
    lastSocket().serverSend({ type: 'order.paid', data: { order_no: 'A003' } })

    expect(seen).toEqual(['second'])
  })

  it('同一连接上重复注册同一函数只生效一次', async () => {
    const realtime = await importRealtime()
    let count = 0
    const handler = () => {
      count += 1
    }
    realtime.onRealtime('order.paid', handler)
    realtime.onRealtime('order.paid', handler)

    realtime.connectRealtime()
    lastSocket().serverSend({ type: 'order.paid', data: { order_no: 'A004' } })

    expect(count).toBe(1)
  })
})

describe('订阅与 socket 实例解耦（踢线重连场景）', () => {
  it('断开并重建连接后，既有订阅者仍能收到事件', async () => {
    vi.useFakeTimers()
    const realtime = await importRealtime()
    const paid: unknown[] = []
    realtime.onRealtime('order.paid', (data) => paid.push(data))
    realtime.connectRealtime()
    expect(FakeWebSocket.instances).toHaveLength(1)

    // 服务端权限变更送达后踢线：onclose 触发，连接层自行重连
    lastSocket().close()
    await vi.advanceTimersByTimeAsync(2100)
    expect(FakeWebSocket.instances.length).toBeGreaterThan(1)
    // 中途断开期间的事件收不到（尽力而为语义），重连后的事件必须能收到
    lastSocket().serverSend({ type: 'order.paid', data: { order_no: 'A005' } })

    expect(paid.map((d) => (d as { order_no: string }).order_no)).toEqual(['A005'])
  })

  it('社区变化由 connectRealtime 自行换连，订阅关系保持不变', async () => {
    vi.useFakeTimers()
    const realtime = await importRealtime()
    const paid: unknown[] = []
    const states: unknown[] = []
    realtime.onRealtime('order.paid', (data) => paid.push(data))
    realtime.onRealtime('connection', (data) => states.push(data))

    realtime.connectRealtime()
    const first = lastSocket()
    first.onopen?.({})
    // 宿主（App.vue 的社区监听）只负责在就绪时调用 connectRealtime，
    // 换社区不再要求外部先 disconnect——此前正是漏在这一步：对 OPEN 连接幂等早退，
    // 连接仍按旧 community_id 收推送，而 HTTP 已切到新社区
    fakeAuth.currentCommunityId = 2
    realtime.connectRealtime()

    const newest = lastSocket()
    expect(newest.url).toContain('community_id=2')
    newest.onopen?.({})
    expect(FakeWebSocket.instances).toHaveLength(2)

    // 旧连接的 close 稍后才派发：已被新连接替换，既不自动重连也不误报断开
    await vi.advanceTimersByTimeAsync(5000)
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(states).toEqual([{ connected: true }, { connected: true }])

    newest.serverSend({ type: 'order.paid', data: { order_no: 'A006' } })
    expect(paid).toHaveLength(1)
  })

  it('同社区重复建连幂等，不会多开一条（重连调度器复用同入口）', async () => {
    const realtime = await importRealtime()
    realtime.connectRealtime()
    realtime.connectRealtime()

    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('显式断开即刻补发 connection:false，不等迟到的 close 回调', async () => {
    vi.useFakeTimers()
    const realtime = await importRealtime()
    const states: unknown[] = []
    realtime.onRealtime('connection', (data) => states.push(data))

    realtime.connectRealtime()
    lastSocket().onopen?.({})
    realtime.disconnectRealtime()

    // 断开即降级兜底频率不能等到 close 派发之后：该回调因 socket 已置空而静默退出，
    // 不补发就会让消费方（下载中心轮询间隔）一直停在「在线」低频口径
    expect(states).toEqual([{ connected: true }, { connected: false }])
    expect(realtime.isRealtimeConnected()).toBe(false)
    await vi.advanceTimersByTimeAsync(10000)
    expect(states).toHaveLength(2)
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('未登录或未选定社区时不建连', async () => {
    const realtime = await importRealtime()
    fakeAuth.isAuthenticated = false
    realtime.connectRealtime()
    fakeAuth.isAuthenticated = true
    fakeAuth.currentCommunityId = 0
    realtime.connectRealtime()

    expect(FakeWebSocket.instances).toHaveLength(0)
  })
})

describe('连通状态可供轮询降频判定', () => {
  it('连接就绪为 true，关闭后为 false', async () => {
    const realtime = await importRealtime()
    expect(realtime.isRealtimeConnected()).toBe(false)

    realtime.connectRealtime()
    expect(realtime.isRealtimeConnected()).toBe(true)

    realtime.disconnectRealtime()
    expect(realtime.isRealtimeConnected()).toBe(false)
  })
})

describe('权限变更的会话维护', () => {
  it('连发多条权限变更事件，防抖窗口内只刷新一次快照', async () => {
    vi.useFakeTimers()
    const realtime = await importRealtime()
    realtime.connectRealtime()
    const sock = lastSocket()

    sock.serverSend({ type: 'admin.permission_changed', data: { community_ids: [1] } })
    await vi.advanceTimersByTimeAsync(1000)
    sock.serverSend({ type: 'admin.permission_changed', data: { community_ids: [1] } })
    await vi.advanceTimersByTimeAsync(1500)

    // 真防抖：第二条事件重置窗口后仍会触发刷新，尾部事件不被吞掉
    expect(fakeAuth.fetchAdminInfo).toHaveBeenCalledTimes(1)
  })

  it('权限变更同时转交订阅者，供页面自行处理', async () => {
    const realtime = await importRealtime()
    const events: unknown[] = []
    realtime.onRealtime('admin.permission_changed', (data) => events.push(data))
    realtime.connectRealtime()

    lastSocket().serverSend({ type: 'admin.permission_changed', data: { admin_ids: [7] } })

    expect(events).toHaveLength(1)
  })
})
