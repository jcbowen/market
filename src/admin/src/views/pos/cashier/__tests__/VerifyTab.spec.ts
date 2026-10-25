import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import VerifyTab from '../VerifyTab.vue'
import { NButton, naiveTestComponents } from './naiveStubs'

/**
 * VerifyTab 取货核销页单测（页面级键盘通道）
 * 覆盖：数字键录入 8 位码、回车查询命中/未找到、ESC 清空重扫三态（输入态/订单卡/核销成功）
 */

const { getOrderList, getOrderDetail, verifyPickup } = vi.hoisted(() => ({
  getOrderList: vi.fn(),
  getOrderDetail: vi.fn(),
  verifyPickup: vi.fn(),
}))

vi.mock('@/api/order', () => ({ getOrderList, getOrderDetail, verifyPickup }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSuperAdmin: true, hasPermission: () => true }),
}))

const { useMessage } = vi.hoisted(() => ({ useMessage: vi.fn() }))
vi.mock('naive-ui', () => ({ useMessage }))

const warningMock = vi.fn()

// VerifyTab 模板使用的展示类替身（纯透传插槽，不承载交互）
const NIcon = { template: '<i class="n-icon-stub"><slot /></i>' }
const NTag = { template: '<span class="n-tag-stub"><slot /></span>' }
const NAlert = { template: '<div class="n-alert-stub"><slot /></div>' }

// 组件挂载时注册 window keydown 监听：必须逐用例卸载，否则前一用例的
// 快捷键监听会响应后一用例的按键（真实组件 onBeforeUnmount 会移除）
const wrappers: VueWrapper[] = []
afterEach(() => {
  while (wrappers.length) wrappers.pop()!.unmount()
})

beforeEach(() => {
  vi.clearAllMocks()
  useMessage.mockReturnValue({ success: vi.fn(), warning: warningMock, info: vi.fn(), error: vi.fn() })
})

function mountTab() {
  const wrapper = mount(VerifyTab, {
    global: {
      components: { ...naiveTestComponents, NIcon, NTag, NAlert },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** 页面级键盘输入（无需聚焦任何元素，等价扫码枪/物理键盘） */
function typeKey(key: string) {
  window.dispatchEvent(new KeyboardEvent('keydown', { key }))
}

/** 键入 8 位取货码（第 8 位自动触发查询） */
async function typeCode(code = '12345678') {
  for (const digit of code) typeKey(digit)
  await flushPromises()
}

/** 已填码格数（filled 类的格子数） */
function filledCells(wrapper: ReturnType<typeof mountTab>) {
  return wrapper.findAll('.code-cell.filled').length
}

/** 可核销的订单详情 mock（自提单已支付） */
function mockHitOrder() {
  getOrderList.mockResolvedValue({ data: { code: 0, data: { list: [{ id: 55 }] } } })
  getOrderDetail.mockResolvedValue({
    data: {
      code: 0,
      data: {
        id: 55,
        order_no: 'O-055',
        user_name: '张三',
        delivery_type: 1,
        status: 2,
        status_text: '待自提',
        created_at: '2026-10-25 10:00:00',
        pay_amount: '12.80',
        items: [{ id: 1, product_name: '苹果', sku_name: '', quantity: 2, price: '6.40' }],
      },
    },
  })
}

describe('VerifyTab 键盘录入与查询', () => {
  it('数字键录入码格，退格删一位', async () => {
    const wrapper = mountTab()
    typeKey('1')
    typeKey('2')
    await flushPromises()
    expect(filledCells(wrapper)).toBe(2)

    typeKey('Backspace')
    await flushPromises()
    expect(filledCells(wrapper)).toBe(1)
  })

  it('回车查询命中：渲染订单卡', async () => {
    mockHitOrder()
    const wrapper = mountTab()
    await typeCode()
    expect(wrapper.text()).toContain('O-055')
  })

  it('回车查询未找到：未找到态 + 码位清空提示', async () => {
    getOrderList.mockResolvedValue({ data: { code: 0, data: { list: [] } } })
    const wrapper = mountTab()
    await typeCode()
    expect(wrapper.text()).toContain('未找到订单')
  })

  it('不足 8 位回车：提示完整码，不发请求', async () => {
    const wrapper = mountTab()
    typeKey('1')
    typeKey('Enter')
    await flushPromises()
    expect(warningMock).toHaveBeenCalled()
    expect(getOrderList).not.toHaveBeenCalled()
  })
})

describe('VerifyTab ESC 清空重扫', () => {
  it('输入态按 ESC：清空已填码格，隐藏「清空重扫」按钮', async () => {
    const wrapper = mountTab()
    typeKey('1')
    typeKey('2')
    await flushPromises()
    expect(wrapper.findAll('button').some((b) => b.text().includes('清空重扫'))).toBe(true)

    typeKey('Escape')
    await flushPromises()
    expect(filledCells(wrapper)).toBe(0)
    expect(wrapper.findAll('button').some((b) => b.text().includes('清空重扫'))).toBe(false)
  })

  it('订单卡态按 ESC：回到等待扫码初始态', async () => {
    mockHitOrder()
    const wrapper = mountTab()
    await typeCode()
    expect(wrapper.text()).toContain('O-055')

    typeKey('Escape')
    await flushPromises()
    expect(wrapper.text()).toContain('等待扫码')
    expect(filledCells(wrapper)).toBe(0)
  })

  it('核销成功态按 ESC：清空成功反馈回到初始态（连续扫码）', async () => {
    mockHitOrder()
    verifyPickup.mockResolvedValue({ data: { code: 0, data: {} } })
    const wrapper = mountTab()
    await typeCode()
    await wrapper.findAll('button').find((b) => b.text().includes('确认核销'))!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('核销成功')

    typeKey('Escape')
    await flushPromises()
    expect(wrapper.text()).toContain('等待扫码')
    expect(filledCells(wrapper)).toBe(0)
  })
})
