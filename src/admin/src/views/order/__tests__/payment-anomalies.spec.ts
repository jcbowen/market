import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { KeepAlive, defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PaymentAnomalies from '../payment-anomalies.vue'

/**
 * 支付异常页实时刷新单测
 * 覆盖：挂载即拉取并订阅异常检出事件（重复注册须收敛为一条）/ 空闲时事件即刻刷 /
 *       在途请求期间到达的事件不丢弃（落定后补刷，连发合并为一次）/
 *       keep-alive 失活期间不响应且切回时补拉一次 / 卸载后退订
 * 本页没有轮询兜底：事件一旦在 loading 在途时被丢弃、或失活窗口内错过后不再补拉，
 * 列表就会一直停在旧数据，故两条兜底路径都要有用例钉住
 */

const api = vi.hoisted(() => ({
  getPaymentAnomalies: vi.fn(),
  handlePaymentAnomaly: vi.fn(),
}))

vi.mock('@/api/order', () => ({
  getPaymentAnomalies: api.getPaymentAnomalies,
  handlePaymentAnomaly: api.handlePaymentAnomaly,
}))

/** 页面订阅的实时事件处理器（type → 回调列表），供测试内手动投递 */
const realtime = vi.hoisted(() => ({
  handlers: new Map<string, Array<(data: unknown) => void>>(),
}))

vi.mock('@/utils/realtime', () => ({
  onRealtime: (type: string, handler: (data: unknown) => void) => {
    const list = realtime.handlers.get(type) ?? []
    list.push(handler)
    realtime.handlers.set(type, list)
    // 与真实实现一致：每次订阅返回各自移除自己的 disposer（重复注册不会把别人的订阅带走）
    return () => {
      realtime.handlers.set(type, (realtime.handlers.get(type) ?? []).filter((item) => item !== handler))
    }
  },
}))

// 页面直接 import 的 naive-ui 组件与 useDialog：替身须在模块图加载时就位，
// 故用异步工厂内部动态 import vue 造轻量组件（vi.hoisted 里引用 import 会踩 TDZ）
vi.mock('naive-ui', async () => {
  const { defineComponent } = await import('vue')
  return {
    NButton: defineComponent({ template: '<button><slot /></button>' }),
    NSpace: defineComponent({ template: '<div><slot /></div>' }),
    NTag: defineComponent({ template: '<span><slot /></span>' }),
    useDialog: () => ({ warning: vi.fn(), info: vi.fn(), success: vi.fn(), error: vi.fn() }),
  }
})

/** 子弹窗替身：详情/退款弹窗自身另有测试，此处不参与渲染 */
vi.mock('../detail.vue', () => ({
  default: defineComponent({ name: 'OrderDetailModalStub', render: () => h('div', 'detail-stub') }),
}))
vi.mock('../components/RefundModal.vue', () => ({
  default: defineComponent({ name: 'RefundModalStub', render: () => h('div', 'refund-stub') }),
}))

/** 模板内 n-* 组件的轻量替身（保留默认插槽，保证子树可渲染） */
const templateStubs = {
  NCard: defineComponent({ template: '<div class="n-card-stub"><slot /></div>' }),
  NSpace: defineComponent({ template: '<div><slot /></div>' }),
  NAlert: defineComponent({ template: '<div><slot /></div>' }),
  NText: defineComponent({ template: '<span><slot /></span>' }),
  NSelect: defineComponent({ template: '<div class="n-select-stub" />' }),
  NCheckbox: defineComponent({ template: '<div class="n-checkbox-stub" />' }),
  NButton: defineComponent({ template: '<button><slot /></button>' }),
  NTag: defineComponent({ template: '<span><slot /></span>' }),
  NDataTable: defineComponent({ template: '<div class="n-table-stub" />' }),
  NModal: defineComponent({ template: '<div><slot /></div>' }),
  NForm: defineComponent({ template: '<div><slot /></div>' }),
  NFormItem: defineComponent({ template: '<div><slot /></div>' }),
  NInput: defineComponent({ template: '<input />' }),
}

/** 列表接口的成功响应体 */
const listResponse = {
  data: {
    code: 0,
    data: { list: [], total: 0, pending: 0, can_view_raw: false, community_scope: 1 },
  },
}

/** 占位页签组件（切走时接替本页渲染，使其进入 keep-alive 缓存） */
const Placeholder = defineComponent({ name: 'Placeholder', render: () => h('div', '其他页签') })

/**
 * 挂载支付异常页到 keep-alive 宿主（复刻后台标签页缓存语义）
 * @returns 测试包裹器与页签切换器（true=异常页，false=其他页签，触发激活/失活）
 */
function mountPage(): { wrapper: VueWrapper; switchTab: (toAnomalies: boolean) => Promise<void> } {
  const onAnomalies = ref(true)
  const host = defineComponent({
    render: () => h(KeepAlive, null, () => h(onAnomalies.value ? PaymentAnomalies : Placeholder)),
  })
  const wrapper = mount(host, { global: { components: templateStubs } })
  const switchTab = async (toAnomalies: boolean) => {
    onAnomalies.value = toAnomalies
    await flushPromises()
  }
  return { wrapper, switchTab }
}

/** 投递一个支付异常检出事件 */
function emitAnomaly() {
  realtime.handlers.get('payment.anomaly_detected')?.forEach((handler) => handler({ order_no: 'PO1' }))
}

/** 列表接口调用次数 */
function listCalls(): number {
  return api.getPaymentAnomalies.mock.calls.length
}

/** 当前仍在订阅的异常事件处理器数 */
function subscribedHandlers(): number {
  return realtime.handlers.get('payment.anomaly_detected')?.length ?? 0
}

/**
 * 让列表请求挂起（模拟在途），按调用顺序放行
 * @returns release：放行最早的一个在途请求
 */
function holdRequests() {
  const queue: Array<(v: unknown) => void> = []
  api.getPaymentAnomalies.mockImplementation(
    () => new Promise((resolve) => queue.push(resolve)),
  )
  return {
    release() {
      queue.shift()?.(listResponse)
    },
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  realtime.handlers.clear()
  api.getPaymentAnomalies.mockResolvedValue(listResponse)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('支付异常页实时刷新', () => {
  it('挂载时拉取列表，且只保留一条异常事件订阅', async () => {
    const { wrapper } = mountPage()
    await flushPromises()

    // onMounted 已拉过一次，onActivated 的激活补刷要跳过首次，否则一次挂载发两个请求
    expect(listCalls()).toBe(1)
    // onMounted 与 onActivated 都会注册：重复注册须先退订旧的，否则一个事件刷两遍
    expect(subscribedHandlers()).toBe(1)
    wrapper.unmount()
  })

  it('空闲时收到异常事件即刻刷新一次', async () => {
    const { wrapper } = mountPage()
    await flushPromises()

    emitAnomaly()
    await flushPromises()
    expect(listCalls()).toBe(2)

    emitAnomaly()
    await flushPromises()
    expect(listCalls()).toBe(3)
    wrapper.unmount()
  })

  it('在途请求期间到达的事件不丢弃：本次落定后补刷一次', async () => {
    // 先挂起请求，再挂载——否则挂载的首次请求已被 mockResolvedValue 立即落定，测不到在途窗口
    const held = holdRequests()
    const { wrapper } = mountPage()
    await flushPromises()
    expect(listCalls()).toBe(1)

    emitAnomaly()
    await flushPromises()
    // 在途期间不再并发请求（并发会让两次结果相互覆盖）
    expect(listCalls()).toBe(1)

    held.release()
    await flushPromises()
    // 事件被记账，本次落定后补刷
    expect(listCalls()).toBe(2)
    held.release()
    await flushPromises()
    wrapper.unmount()
  })

  it('在途期间连发多个事件合并为一次补刷，补刷自身在途时继续记账', async () => {
    const held = holdRequests()
    const { wrapper } = mountPage()
    await flushPromises()

    for (let i = 0; i < 4; i++) {
      emitAnomaly()
    }
    await flushPromises()
    expect(listCalls()).toBe(1)

    held.release()
    await flushPromises()
    expect(listCalls()).toBe(2)

    // 补刷自身仍在途时到达的事件继续记账，不会无限自刷
    emitAnomaly()
    held.release()
    await flushPromises()
    expect(listCalls()).toBe(3)

    // 队列已清空：此后无事件则不再发请求
    held.release()
    await flushPromises()
    expect(listCalls()).toBe(3)
    wrapper.unmount()
  })

  it('在途时点击「查询」不并发，落定后补刷（用户操作与事件共用同一记账）', async () => {
    const held = holdRequests()
    const { wrapper } = mountPage()
    await flushPromises()

    const searchButton = wrapper.findAll('button').find((btn) => btn.text() === '查询')
    expect(searchButton).toBeTruthy()
    await searchButton!.trigger('click')
    await flushPromises()
    // 不抢发并发请求（两次结果相互覆盖，可能出现翻页后回显旧页）
    expect(listCalls()).toBe(1)

    held.release()
    await flushPromises()
    expect(listCalls()).toBe(2)
    held.release()
    await flushPromises()
    wrapper.unmount()
  })

  it('失活期间事件不触发刷新，切回时补拉一次并重新订阅', async () => {
    const { wrapper, switchTab } = mountPage()
    await flushPromises()

    await switchTab(false)
    expect(subscribedHandlers()).toBe(0)
    vi.clearAllMocks()
    api.getPaymentAnomalies.mockResolvedValue(listResponse)

    emitAnomaly()
    await flushPromises()
    expect(listCalls()).toBe(0)

    // 切回：失活窗口错过的检出靠激活补拉找回，订阅同时恢复
    await switchTab(true)
    expect(listCalls()).toBe(1)
    expect(subscribedHandlers()).toBe(1)

    emitAnomaly()
    await flushPromises()
    expect(listCalls()).toBe(2)
    wrapper.unmount()
  })

  it('卸载后退订，迟到的事件不再触发请求', async () => {
    const { wrapper } = mountPage()
    await flushPromises()

    wrapper.unmount()
    expect(subscribedHandlers()).toBe(0)

    const before = listCalls()
    emitAnomaly()
    await flushPromises()
    expect(listCalls()).toBe(before)
  })
})
