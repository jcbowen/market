import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePosStore } from '../pos'

/**
 * 收银台共享状态单测
 * 重点：跨 Tab 待引用清单（订单查询「引用到收银台」→ 商品下单消费）的写入/取走即清语义
 */

const { getCurrentPosShift } = vi.hoisted(() => ({ getCurrentPosShift: vi.fn() }))
vi.mock('@/api/order', () => ({ getCurrentPosShift }))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('usePosStore 待引用清单', () => {
  it('setPendingImport 写入、consumePendingImport 取走即清', () => {
    const store = usePosStore()
    const items = [{ sku_id: 11, quantity: 2, product_name: '可乐 500ml', price: 3.5 }]
    store.setPendingImport(items)

    expect(store.pendingImport).toHaveLength(1)
    // 消费方（商品下单 Tab 挂载）取走后清单清空，第二次消费为空数组
    expect(store.consumePendingImport()).toEqual(items)
    expect(store.pendingImport).toHaveLength(0)
    expect(store.consumePendingImport()).toEqual([])
  })

  it('二次引用覆盖上一次清单（不追加，以最后一次为准）', () => {
    const store = usePosStore()
    store.setPendingImport([{ sku_id: 1, quantity: 1, product_name: 'A', price: 1 }])
    store.setPendingImport([
      { sku_id: 2, quantity: 1, product_name: 'B', price: 2 },
      { sku_id: 3, quantity: 3, product_name: 'C', spec_name: '大瓶', price: 9.9 },
    ])
    const consumed = store.consumePendingImport()
    expect(consumed).toHaveLength(2)
    expect(consumed[0]).toMatchObject({ sku_id: 2, product_name: 'B' })
    expect(consumed[1]).toMatchObject({ sku_id: 3, spec_name: '大瓶', price: 9.9 })
  })

  it('clearShift 一并清空待引用清单与班次', () => {
    const store = usePosStore()
    store.setPendingImport([{ sku_id: 1, quantity: 1, product_name: 'A', price: 1 }])
    store.clearShift()
    expect(store.pendingImport).toHaveLength(0)
    expect(store.currentShift).toBeNull()
  })
})

describe('usePosStore 班次加载', () => {
  it('id=0 归一为 null（未开工）', async () => {
    getCurrentPosShift.mockResolvedValue({ data: { data: { id: 0 } } })
    const store = usePosStore()
    await store.loadCurrentShift()
    expect(store.currentShift).toBeNull()
    expect(store.loaded).toBe(true)
  })

  it('进行中班次正常记录', async () => {
    getCurrentPosShift.mockResolvedValue({ data: { data: { id: 5, status: 1 } } })
    const store = usePosStore()
    await store.loadCurrentShift()
    expect(store.currentShift).toMatchObject({ id: 5 })
  })

  it('请求失败降级为未开工，不抛出', async () => {
    getCurrentPosShift.mockRejectedValue(new Error('network'))
    const store = usePosStore()
    await expect(store.loadCurrentShift()).resolves.toBeUndefined()
    expect(store.currentShift).toBeNull()
  })
})
