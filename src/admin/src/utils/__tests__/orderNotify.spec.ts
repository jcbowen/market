import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'

/**
 * 订单事件播报层（utils/orderNotify.ts）用例
 *
 * 锁的是「事件到达 → 人听到的话术」这条用户可感知链路：连接层拆出去之后
 * （utils/realtime.ts），播报层只剩「订阅 + 发声」，最容易在重构中被弄丢的就是
 * 到店单/线上单与退款申请/退款成功两组话术口径，以及重复注册导致的连声播报。
 *
 * 提示音走 WebAudio（happy-dom 无 AudioContext，内部已按不可用降级），
 * 浏览器通知按 document.hidden 判定，本文件不涉及，不做断言。
 *
 * 运行方式：
 *   npm test（src/admin 目录）
 */

/** 捕获注册进来的事件处理函数：type → 处理函数列表 */
const { listeners } = vi.hoisted(() => ({
  listeners: new Map<string, Array<(data: unknown) => void>>(),
}))

vi.mock('@/utils/realtime', () => ({
  onRealtime: (type: string, handler: (data: unknown) => void) => {
    const arr = listeners.get(type) ?? []
    arr.push(handler)
    listeners.set(type, arr)
    return () => {
      listeners.set(type, (listeners.get(type) ?? []).filter((h) => h !== handler))
    }
  },
  isRealtimeConnected: () => true,
}))

/** 语音合成替身 */
const { speakMock, cancelMock } = vi.hoisted(() => ({
  speakMock: vi.fn(),
  cancelMock: vi.fn(),
}))

/** 按 type 取出播报层注册的最后一个处理函数 */
function handlerOf(type: string) {
  const arr = listeners.get(type)
  if (!arr || arr.length === 0) throw new Error(`未注册 ${type} 订阅`)
  return arr[arr.length - 1]
}

/** 取本次播报的文本 */
function spokenText() {
  const call = speakMock.mock.calls.at(-1)
  return call ? (call[0] as { text: string }).text : ''
}

beforeEach(async () => {
  listeners.clear()
  speakMock.mockClear()
  cancelMock.mockClear()
  vi.resetModules()
  vi.stubGlobal('speechSynthesis', { speak: speakMock, cancel: cancelMock, getVoices: vi.fn(() => []) })
  vi.stubGlobal('SpeechSynthesisUtterance', class {
    text: string
    lang = ''
    volume = 1
    onerror: ((e: { error: string }) => void) | null = null
    constructor(text: string) {
      this.text = text
    }
  })
  // 固定为直通模式（选主三级降级全不可用）：本文件测话术口径，要求播报同步完成；
  // 选主语义由 notifyLeader.spec.ts 覆盖
  vi.stubGlobal('BroadcastChannel', undefined as unknown as typeof BroadcastChannel)
  Object.defineProperty(navigator, 'locks', { configurable: true, get: () => undefined })
})

afterEach(() => {
  vi.unstubAllGlobals()
  delete (navigator as { locks?: unknown }).locks
})

describe('订阅注册', () => {
  it('注册 order.paid 与 order.refund 两类订阅，重复调用不叠加', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()
    setupOrderNotify()

    expect(listeners.get('order.paid')).toHaveLength(1)
    expect(listeners.get('order.refund')).toHaveLength(1)
  })
})

describe('播报话术', () => {
  it('线上单按「新订单」话术并含金额', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()

    handlerOf('order.paid')({ order_no: 'A1', pay_amount: 12.345, delivery_type: 2 })

    expect(spokenText()).toBe('您有新订单，金额12.35元，请及时处理')
  })

  it('到店单（delivery_type=3）按「到店收款」话术', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()

    handlerOf('order.paid')({ order_no: 'A2', pay_amount: 8, delivery_type: 3 })

    expect(spokenText()).toBe('您有一笔到店收款，金额8元')
  })

  it('C 端申请退款提醒处理，退款成功另为一句', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()
    const handler = handlerOf('order.refund')

    handler({ order_no: 'A3', refund_no: 'R1', amount: 5, action: 'apply' })
    const applyText = spokenText()
    handler({ order_no: 'A3', refund_no: 'R1', amount: 5, action: 'completed' })

    expect(applyText).toBe('您有一笔退款申请，金额5元，请及时处理')
    expect(spokenText()).toBe('订单已退款，金额5元')
  })

  it('播报前先清队列，避免异常未结束的上一条把新语句排到永远不出声', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()

    handlerOf('order.paid')({ order_no: 'A4', pay_amount: 1, delivery_type: 1 })

    expect(cancelMock).toHaveBeenCalled()
    expect(speakMock).toHaveBeenCalledTimes(1)
  })

  it('载荷缺失时不播报（服务端异常下发不炸前端）', async () => {
    const { setupOrderNotify } = await import('@/utils/orderNotify')
    setupOrderNotify()

    handlerOf('order.paid')(undefined)

    expect(speakMock).not.toHaveBeenCalled()
  })
})
