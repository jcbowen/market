import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DownloadCenter from '../DownloadCenter.vue'

/**
 * 下载中心实时化单测（方案 Item 3）
 * 覆盖：面板打开时轮询频率随 WS 通断自适应（15s / 3s）、导出任务终态事件即刻刷新
 * （本人任务才响应，打开刷列表、关闭刷角标）、卸载退订
 */

const api = vi.hoisted(() => ({
  countUndownloadedExports: vi.fn(),
  createOrderExportTask: vi.fn(),
  deleteOrderExportTask: vi.fn(),
  downloadOrderExportTask: vi.fn(),
  listOrderExportTasks: vi.fn(),
}))
vi.mock('@/api/order', () => api)

/** 组件订阅的实时事件处理器（type → 回调列表），供测试内手动投递 */
const realtime = vi.hoisted(() => ({
  handlers: new Map<string, Array<(data: any) => void>>(),
  connected: vi.fn(() => false),
  /** 退订次数计数（真实 realtime 每次退订调用各自返回的 disposer） */
  disposed: 0,
}))
vi.mock('@/utils/realtime', () => ({
  isRealtimeConnected: realtime.connected,
  onRealtime: (type: string, handler: (data: any) => void) => {
    realtime.handlers.set(type, [...(realtime.handlers.get(type) ?? []), handler])
    return () => {
      realtime.disposed += 1
      realtime.handlers.set(type, (realtime.handlers.get(type) ?? []).filter((item) => item !== handler))
    }
  },
}))

vi.mock('naive-ui', async () => {
  const vue = await import('vue')
  /** 通用替身：把可能用到的插槽都渲染出来，便于 findComponent 定位后触达交互 */
  const stub = (name: string) =>
    vue.defineComponent({
      name,
      setup(_props, { slots }) {
        return () => vue.h('div', { class: `stub-${name}` }, [slots.default?.(), slots.trigger?.(), slots.icon?.(), slots.extra?.()])
      },
    })
  return {
    NBadge: stub('NBadge'),
    NButton: stub('NButton'),
    NEmpty: stub('NEmpty'),
    NIcon: stub('NIcon'),
    NPagination: stub('NPagination'),
    NPopover: stub('NPopover'),
    NSpin: stub('NSpin'),
    NTag: stub('NTag'),
    NText: stub('NText'),
    NTooltip: stub('NTooltip'),
    useDialog: () => ({ warning: vi.fn() }),
    useMessage: () => ({ success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ admin: { id: 3, username: 'admin' }, isSuperAdmin: false, hasPermission: () => true }),
}))

/** 投递实时事件到组件订阅的处理器 */
function emitRealtime(type: string, data: unknown) {
  realtime.handlers.get(type)?.forEach((handler) => handler(data))
}

/** 打开面板（触发 v-model:show 与开合回调） */
async function openPanel(wrapper: ReturnType<typeof mount>) {
  await wrapper.findComponent({ name: 'NPopover' }).vm.$emit('update:show', true)
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  realtime.handlers.clear()
  realtime.disposed = 0
  realtime.connected.mockReturnValue(false)
  api.listOrderExportTasks.mockResolvedValue({ data: { code: 0, data: { list: [], total: 0, undownloaded: 0 } } })
  api.countUndownloadedExports.mockResolvedValue({ data: { code: 0, data: { count: 0 } } })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('下载中心实时化', () => {
  it('WS 已连时面板打开的轮询放宽到 15s，断开时保持 3s', async () => {
    vi.useFakeTimers()
    realtime.connected.mockReturnValue(true)
    const wrapper = mount(DownloadCenter)
    await openPanel(wrapper)
    api.listOrderExportTasks.mockClear()

    await vi.advanceTimersByTimeAsync(12000)
    expect(api.listOrderExportTasks).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.listOrderExportTasks).toHaveBeenCalledTimes(1)

    // 通道断开：周期回到 3s 兜底（连接状态事件到达即生效）
    emitRealtime('connection', { connected: false })
    await flushPromises()
    api.listOrderExportTasks.mockClear()
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.listOrderExportTasks).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('本人导出任务完成事件即刻刷新：打开刷列表、关闭刷角标', async () => {
    const wrapper = mount(DownloadCenter)
    await flushPromises()

    // 面板关闭：只刷角标
    api.countUndownloadedExports.mockClear()
    emitRealtime('export.task_changed', { task_id: 9, admin_id: 3, state: 'done' })
    await flushPromises()
    expect(api.countUndownloadedExports).toHaveBeenCalledTimes(1)

    // 面板打开：刷列表
    await openPanel(wrapper)
    api.listOrderExportTasks.mockClear()
    emitRealtime('export.task_changed', { task_id: 10, admin_id: 3, state: 'failed', error_msg: '生成导出数据失败' })
    await flushPromises()
    expect(api.listOrderExportTasks).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('他人任务的事件不响应（定向下发下的自校验）', async () => {
    const wrapper = mount(DownloadCenter)
    await flushPromises()
    api.countUndownloadedExports.mockClear()
    api.listOrderExportTasks.mockClear()

    emitRealtime('export.task_changed', { task_id: 11, admin_id: 4, state: 'done' })
    await flushPromises()
    expect(api.countUndownloadedExports).not.toHaveBeenCalled()
    expect(api.listOrderExportTasks).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('卸载时退订，事件不再触发刷新', async () => {
    const wrapper = mount(DownloadCenter)
    await flushPromises()

    wrapper.unmount()
    expect(realtime.disposed).toBe(2)

    api.countUndownloadedExports.mockClear()
    emitRealtime('export.task_changed', { task_id: 12, admin_id: 3, state: 'done' })
    await flushPromises()
    expect(api.countUndownloadedExports).not.toHaveBeenCalled()
  })
})
