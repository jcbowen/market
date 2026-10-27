import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { KeepAlive, defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Dashboard from '../index.vue'

/**
 * 工作台待办实时刷新单测（方案 Item 1）
 * 覆盖：待办与图表接口拆分（事件只刷待办，不放大到全量请求）/
 *       冷却窗口前沿立即刷 + 连发合并为一次补刷 / keep-alive 失活与卸载后停止刷新并退订
 */

const api = vi.hoisted(() => ({
  getDashboardStatistics: vi.fn(),
  getOrderStatistics: vi.fn(),
  getProductRankings: vi.fn(),
  getRevenueReport: vi.fn(),
  getPaymentAnomalies: vi.fn(),
  getRefundList: vi.fn(),
  getStockWarnings: vi.fn(),
}))

vi.mock('@/api/statistics', () => ({
  getDashboardStatistics: api.getDashboardStatistics,
  getOrderStatistics: api.getOrderStatistics,
  getProductRankings: api.getProductRankings,
  getRevenueReport: api.getRevenueReport,
}))
vi.mock('@/api/order', () => ({
  getPaymentAnomalies: api.getPaymentAnomalies,
  getRefundList: api.getRefundList,
}))
vi.mock('@/api/inventory', () => ({ getStockWarnings: api.getStockWarnings }))

/** 工作台订阅的实时事件处理器（type → 回调列表），供测试内手动投递 */
const realtime = vi.hoisted(() => ({
  handlers: new Map<string, Array<(data: unknown) => void>>(),
  /** 退订次数计数（与真实 onRealtime 一致：每次订阅返回各自移除自己的 disposer） */
  disposed: 0,
}))

vi.mock('@/utils/realtime', () => ({
  onRealtime: (type: string, handler: (data: unknown) => void) => {
    const list = realtime.handlers.get(type) ?? []
    list.push(handler)
    realtime.handlers.set(type, list)
    return () => {
      realtime.disposed += 1
      realtime.handlers.set(type, (realtime.handlers.get(type) ?? []).filter((item) => item !== handler))
    }
  },
  isRealtimeConnected: () => true,
  connectRealtime: vi.fn(),
  disconnectRealtime: vi.fn(),
}))

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    admin: { nickname: '管理员', username: 'admin' },
    hasPermission: () => true,
    currentCommunityId: 1,
    isAuthenticated: true,
  }),
}))

/** naive-ui 全局组件在测试环境的轻量替身（保留默认插槽，保证子树可渲染） */
const naiveStubs = {
  NBadge: defineComponent({ render: () => h('div', { class: 'n-badge-stub' }) }),
  NButton: defineComponent({ template: '<button><slot /></button>' }),
  NCard: defineComponent({ template: '<div class="n-card-stub"><slot /></div>' }),
  NEmpty: defineComponent({ template: '<div class="n-empty-stub" />' }),
  NGrid: defineComponent({ template: '<div><slot /></div>' }),
  NGridItem: defineComponent({ template: '<div><slot /></div>' }),
  NIcon: defineComponent({ template: '<i><slot /></i>' }),
  NProgress: defineComponent({ template: '<div class="n-progress-stub" />' }),
  NSpace: defineComponent({ template: '<div><slot /></div>' }),
}

/** 占位页签组件（切走时接替工作台渲染，使其进入 keep-alive 缓存） */
const Placeholder = defineComponent({ name: 'Placeholder', render: () => h('div', '其他页签') })

/**
 * 挂载工作台到 keep-alive 宿主（复刻后台标签页缓存语义）
 * 每次调用独立持有「当前页签」状态，避免用例间相互串扰
 * @returns 测试包裹器与页签切换器（true=工作台，false=其他页签，触发激活/失活）
 */
function mountDashboard(): { wrapper: VueWrapper; switchTab: (toDashboard: boolean) => Promise<void> } {
  const onDashboard = ref(true)
  const host = defineComponent({
    render: () => h(KeepAlive, null, () => h(onDashboard.value ? Dashboard : Placeholder)),
  })
  const wrapper = mount(host, { global: { components: naiveStubs } })
  const switchTab = async (toDashboard: boolean) => {
    onDashboard.value = toDashboard
    await flushPromises()
  }
  return { wrapper, switchTab }
}

/** 投递一个实时事件到页面订阅的处理器（首页待办订阅的三个事件类型） */
function emitRealtime(type: 'order.paid' | 'order.refund' | 'payment.anomaly_detected') {
  realtime.handlers.get(type)?.forEach((handler) => handler({ order_no: 'PO1' }))
}

/**
 * 待办区累计请求数（一次 loadTodos = 订单统计 1 + 售后 2 + 支付异常 1 + 库存预警 1）
 * @returns 各待办接口的调用次数合计
 */
function todosCalls(): number {
  return (
    api.getOrderStatistics.mock.calls.length +
    api.getRefundList.mock.calls.length +
    api.getPaymentAnomalies.mock.calls.length +
    api.getStockWarnings.mock.calls.length
  )
}

/** 一次全量刷新（loadAll）的待办请求数 */
const TODOS_CALLS_PER_LOAD = 5

beforeEach(() => {
  vi.clearAllMocks()
  realtime.handlers.clear()
  realtime.disposed = 0
  api.getDashboardStatistics.mockResolvedValue({ data: { code: 0, data: {} } })
  api.getOrderStatistics.mockResolvedValue({
    data: { code: 0, data: { pending_pay: 1, pending_ship: 2, pending_receive: 3 } },
  })
  api.getRefundList.mockResolvedValue({ data: { code: 0, data: { total: 5, list: [] } } })
  api.getPaymentAnomalies.mockResolvedValue({ data: { code: 0, data: { pending: 2, list: [] } } })
  api.getStockWarnings.mockResolvedValue({ data: { code: 0, data: [] } })
  api.getRevenueReport.mockResolvedValue({ data: { code: 0, data: [] } })
  api.getProductRankings.mockResolvedValue({ data: { code: 0, data: [] } })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('工作台待办实时刷新', () => {
  it('挂载时拉取全部区块，并按待办/图表拆分接口', async () => {
    const { wrapper } = mountDashboard()
    await flushPromises()

    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD)
    // 图表区：概览 + 营收 + 热销
    expect(api.getDashboardStatistics).toHaveBeenCalledTimes(1)
    expect(api.getRevenueReport).toHaveBeenCalledTimes(1)
    expect(api.getProductRankings).toHaveBeenCalledTimes(1)
    // 订阅影响待办的三类事件（订单支付/退款/支付异常检出）
    expect([...realtime.handlers.keys()].sort()).toEqual(['order.paid', 'order.refund', 'payment.anomaly_detected'])
    wrapper.unmount()
  })

  it('收到订单事件只刷待办，不重拉图表接口', async () => {
    const { wrapper } = mountDashboard()
    await flushPromises()

    emitRealtime('order.paid')
    await flushPromises()

    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD * 2)
    expect(api.getDashboardStatistics).toHaveBeenCalledTimes(1)
    expect(api.getRevenueReport).toHaveBeenCalledTimes(1)
    expect(api.getProductRankings).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('支付异常检出事件同样触发待办刷新（并入冷却合并）', async () => {
    const { wrapper } = mountDashboard()
    await flushPromises()
    vi.useFakeTimers()

    emitRealtime('payment.anomaly_detected')
    await flushPromises()
    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD * 2)
    // 图表接口不受异常事件影响
    expect(api.getDashboardStatistics).toHaveBeenCalledTimes(1)

    // 与订单事件共享同一冷却窗口：窗口内连发不再放大请求数，窗口结束补刷一次
    emitRealtime('order.paid')
    await flushPromises()
    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD * 2)
    await vi.advanceTimersByTimeAsync(8000)
    await flushPromises()
    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD * 3)
    wrapper.unmount()
  })

  it('冷却窗口内连发多单合并为一次补刷', async () => {
    const { wrapper } = mountDashboard()
    await flushPromises()
    vi.useFakeTimers()
    const baseline = todosCalls()

    for (let i = 0; i < 5; i++) {
      emitRealtime(i % 2 === 0 ? 'order.paid' : 'order.refund')
    }
    await flushPromises()
    // 前沿立即刷一次，窗口内后续事件不再发请求
    expect(todosCalls()).toBe(baseline + TODOS_CALLS_PER_LOAD)

    await vi.advanceTimersByTimeAsync(8000)
    await flushPromises()
    // 窗口结束补刷一次，保证最后一单的变更可见
    expect(todosCalls()).toBe(baseline + TODOS_CALLS_PER_LOAD * 2)
    wrapper.unmount()
  })

  it('切走页签（失活）后事件不再触发刷新，切回时全量刷新', async () => {
    const { wrapper, switchTab } = mountDashboard()
    await flushPromises()

    await switchTab(false)
    vi.clearAllMocks()

    emitRealtime('order.paid')
    await flushPromises()
    expect(todosCalls()).toBe(0)

    // 切回工作台：待办与图表各刷一次
    await switchTab(true)
    expect(todosCalls()).toBe(TODOS_CALLS_PER_LOAD)
    expect(api.getDashboardStatistics).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('卸载时退订并停止刷新', async () => {
    const { wrapper } = mountDashboard()
    await flushPromises()

    wrapper.unmount()
    expect(realtime.disposed).toBe(3)

    const before = todosCalls()
    emitRealtime('order.paid')
    await flushPromises()
    expect(todosCalls()).toBe(before)
  })
})
