import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import NewDataAlert from '../NewDataAlert.vue'

/**
 * 「有新数据」提示条单测（方案 Item 6）
 * 覆盖：事件只计数不刷新 / 点击才派发 refresh 并隐藏 / 宿主重新加载后归零 / 卸载退订
 */

/** 组件订阅的事件处理器（type → 回调列表），供测试内手动投递 */
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
}))

/** 投递一个实时事件 */
function emitRealtime(type: string) {
  realtime.handlers.get(type)?.forEach((handler) => handler({ order_no: 'PO1' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  realtime.handlers.clear()
  realtime.disposed = 0
})

/**
 * 挂载提示条
 * @param types 订阅的事件类型
 * @param loadedAt 宿主列表最近加载时间戳
 */
function mountAlert(types: string[] = ['order.paid', 'order.refund'], loadedAt = 1) {
  return mount(NewDataAlert, { props: { types, loadedAt } })
}

describe('NewDataAlert', () => {
  it('无事件时不渲染，累计后显示条数', async () => {
    const wrapper = mountAlert()
    expect(wrapper.text()).toBe('')

    emitRealtime('order.paid')
    emitRealtime('order.refund')
    emitRealtime('order.paid')
    await flushPromises()

    expect(wrapper.text()).toBe('有 3 条新数据，点击刷新')
    // 只计数，不自行发请求：刷新由宿主通过 refresh 承接
    expect(wrapper.emitted('refresh')).toBeUndefined()
    wrapper.unmount()
  })

  it('点击后归零并通知宿主刷新', async () => {
    const wrapper = mountAlert()
    emitRealtime('order.paid')
    await flushPromises()

    await wrapper.trigger('click')

    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.text()).toBe('')
    wrapper.unmount()
  })

  it('宿主重新加载（loadedAt 变化）后陈旧计数作废', async () => {
    const wrapper = mountAlert(['order.refund'])
    emitRealtime('order.refund')
    await flushPromises()
    expect(wrapper.text()).toContain('有 1 条新数据')

    // 宿主自己重载了列表（如切页、点查询），提示条不该继续挂着
    await wrapper.setProps({ loadedAt: Date.now() })
    expect(wrapper.text()).toBe('')

    emitRealtime('order.refund')
    await flushPromises()
    expect(wrapper.text()).toContain('有 1 条新数据')
    wrapper.unmount()
  })

  it('卸载时退订，事件不再累计', async () => {
    const wrapper = mountAlert()
    wrapper.unmount()

    expect(realtime.disposed).toBe(2)
    // 退订即从订阅表摘除，事件不再投递给已卸载组件
    expect([...realtime.handlers.values()].reduce((sum, list) => sum + list.length, 0)).toBe(0)
    emitRealtime('order.paid')
    await flushPromises()
    expect(wrapper.text()).toBe('')
  })
})
