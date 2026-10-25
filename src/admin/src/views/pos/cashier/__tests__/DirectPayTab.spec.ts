import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DirectPayTab from '../DirectPayTab.vue'
import { naiveTestComponents } from './naiveStubs'

/**
 * DirectPayTab 直接收款页单测（新增 Tab 的核心逻辑）
 * 覆盖：金额输入解析（前导零/两位小数/重复小数点/退格）、双收款路径建单参数、
 * F2/F3 快捷键守卫、收款成功清零、无付款码渠道降级
 */

const { createPosOrder, listPosPayMethods } = vi.hoisted(() => ({
  createPosOrder: vi.fn(),
  listPosPayMethods: vi.fn(),
}))

vi.mock('@/api/order', () => ({ createPosOrder, listPosPayMethods, bindOrderUser: vi.fn() }))

// ReceiptModal 新增散客补绑入口后 import 链引入 stores/auth（→ api/auth → request.ts 顶层
// createDiscreteApi 依赖 naive-ui 完整包）与 UserPicker（→ api/user 同链），测试内统一阻断
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSuperAdmin: true, hasPermission: () => true }),
}))
vi.mock('@/api/user', () => ({ getUserList: vi.fn(), createUser: vi.fn() }))

const { useDialog, useMessage } = vi.hoisted(() => ({ useDialog: vi.fn(), useMessage: vi.fn() }))
vi.mock('naive-ui', () => ({ useDialog, useMessage }))

const warningMock = vi.fn()
const successMock = vi.fn()
/** dialog.warning 捕获的配置（onPositiveClick 由用例手动触发，模拟收银员点确认/回车；content 供文案断言） */
let capturedDialog: { content?: string; onPositiveClick?: () => void | Promise<void> } | null = null

// 组件挂载时注册 window keydown 监听：必须逐用例卸载，否则前一用例的
// 快捷键监听会响应后一用例的按键（真实组件 onBeforeUnmount 会移除）
const wrappers: VueWrapper[] = []
afterEach(() => {
  while (wrappers.length) wrappers.pop()!.unmount()
})

beforeEach(() => {
  vi.clearAllMocks()
  capturedDialog = null
  useMessage.mockReturnValue({ success: successMock, warning: warningMock, info: vi.fn(), error: vi.fn() })
  useDialog.mockImplementation(() => ({
    warning: (opts: { onPositiveClick?: () => void | Promise<void> }) => {
      capturedDialog = opts
      return {}
    },
  }))
  // 默认付款码渠道可用（后端两渠道齐备下发 pay_type=5）；仅现金渠道用例单独覆盖
  listPosPayMethods.mockResolvedValue({ data: { code: 0, data: { list: [{ pay_type: 5, label: '付款码支付' }] } } })
})

function mountTab() {
  const wrapper = mount(DirectPayTab, {
    global: {
      components: naiveTestComponents,
      stubs: { CodePayDialog: true, ReceiptModal: true, UserPicker: true },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** 点击小键盘字符（数字/小数点） */
async function tapPad(wrapper: ReturnType<typeof mountTab>, chars: string) {
  for (const ch of chars) {
    const btn = wrapper.findAll('button').find((b) => b.text() === ch)
    if (!btn) throw new Error(`找不到按键 ${ch}`)
    await btn.trigger('click')
  }
}

async function tapBackspace(wrapper: ReturnType<typeof mountTab>) {
  await wrapper.findAll('button').find((b) => b.text() === '⌫')!.trigger('click')
}

describe('DirectPayTab 金额输入', () => {
  it('小键盘点按输入金额', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '12.5')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('12.5')
  })

  it('前导零收敛：0 后再点数字直接替换', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '05')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('5')
  })

  it('小数点最多两位', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '1.234')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('1.23')
  })

  it('小数点不可重复', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '1.2.3')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('1.23')
  })

  it('空输入点小数点自动补 0 前缀', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '.')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('0.')
  })

  it('退格删一位；清空按钮清空全部', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '12')
    await tapBackspace(wrapper)
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('1')
    // 清空键在整数模式才有；金额模式用连续退格清空
    await tapBackspace(wrapper)
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('')
  })

  it('物理键盘数字/小数点/退格同样生效', async () => {
    const wrapper = mountTab()
    await flushPromises()
    const input = wrapper.find('input')
    await input.trigger('keydown', { key: '8' })
    await input.trigger('keydown', { key: '.' })
    await input.trigger('keydown', { key: '8' })
    await input.trigger('keydown', { key: 'Backspace' })
    expect((input.element as HTMLInputElement).value).toBe('8.')
  })

  it('金额无效时收款按钮禁用', async () => {
    const wrapper = mountTab()
    await flushPromises()
    // 付款码收款 + 现金收款均因金额为 0 禁用
    const disabled = wrapper.findAll('button').filter((b) => b.attributes('disabled') !== undefined)
    expect(disabled.length).toBe(2)
  })
})

describe('DirectPayTab 现金收款', () => {
  it('确认后建无商品到店单：pay_type=4、manual_amount=金额、items 空', async () => {
    createPosOrder.mockResolvedValue({ data: { code: 0, data: { order_no: 'D-001' } } })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '8.8')

    await wrapper.findAll('button').find((b) => b.text().includes('现金收款'))!.trigger('click')
    // 确认弹窗出现，收银员回车/点确认
    expect(capturedDialog?.onPositiveClick).toBeTruthy()
    await capturedDialog!.onPositiveClick!()

    expect(createPosOrder).toHaveBeenCalledWith({
      user_id: 0,
      delivery_type: 3,
      pay_type: 4,
      manual_amount: 8.8,
      items: [],
    })
    expect(successMock).toHaveBeenCalled()
    // 收款成功后金额清零（连续作业）
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('')
  })

  it('建单失败时不提示成功、金额保留', async () => {
    createPosOrder.mockResolvedValue({ data: { code: 500, message: '没有进行中的班次' } })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '5')
    await wrapper.findAll('button').find((b) => b.text().includes('现金收款'))!.trigger('click')
    await capturedDialog!.onPositiveClick!()
    await flushPromises()
    expect(successMock).not.toHaveBeenCalled()
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('5')
  })
})

describe('DirectPayTab 付款码收款', () => {
  it('建待支付单后打开扫码弹窗（凭据透传）', async () => {
    listPosPayMethods.mockResolvedValue({ data: { code: 0, data: { list: [{ pay_type: 5, label: '付款码支付' }] } } })
    createPosOrder.mockResolvedValue({
      data: { code: 0, data: { id: 96, order_no: 'D-096', pay_amount: 6.6, payment_no: 'ZF096' } },
    })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '6.6')

    await wrapper.findAll('button').find((b) => b.text().includes('付款码收款'))!.trigger('click')
    await flushPromises()

    expect(createPosOrder).toHaveBeenCalledWith({
      user_id: 0,
      delivery_type: 3,
      pay_type: 5,
      manual_amount: 6.6,
      items: [],
    })
    const dialogStub = wrapper.findComponent({ name: 'CodePayDialog' })
    expect(dialogStub.props('show')).toBe(true)
    expect(dialogStub.props('orderId')).toBe(96)
    expect(dialogStub.props('paymentNo')).toBe('ZF096')
    expect(dialogStub.props('amount')).toBe(6.6)
  })

  it('仅现金渠道时付款码入口隐藏，Enter 走现金确认', async () => {
    listPosPayMethods.mockResolvedValue({ data: { code: 0, data: { list: [{ pay_type: 4, label: '现金' }] } } })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '3')
    expect(wrapper.findAll('button').some((b) => b.text().includes('付款码收款'))).toBe(false)

    await wrapper.find('input').trigger('keydown', { key: 'Enter' })
    expect(capturedDialog?.onPositiveClick).toBeTruthy()
  })
})

describe('DirectPayTab 会员选择', () => {
  const memberA = {
    id: 11,
    nickname: '张三',
    phone: '13800000001',
    community_id: 1,
    community_name: '测试社区',
    status: 1,
    created_at: '2026-10-25 10:00:00',
  }
  const memberB = {
    id: 22,
    nickname: '',
    phone: '13900000022',
    community_id: 1,
    community_name: '测试社区',
    status: 1,
    created_at: '2026-10-25 10:00:00',
  }

  /** 经 UserPicker 弹窗选会员（点击选会员/更换会员按钮后由弹窗 select 回传） */
  async function pickMember(wrapper: ReturnType<typeof mountTab>, u: typeof memberA) {
    await wrapper.findAll('button').find((b) => b.text().includes('会员') && !b.text().includes('收款'))!.trigger('click')
    const picker = wrapper.findComponent({ name: 'UserPicker' })
    picker.vm.$emit('select', u)
    await flushPromises()
  }

  it('默认散客标签；选会员后展示会员名，可关闭回散客', async () => {
    const wrapper = mountTab()
    await flushPromises()
    expect(wrapper.text()).toContain('散客')

    await pickMember(wrapper, memberA)
    expect(wrapper.text()).toContain('张三')
    expect(wrapper.text()).not.toContain('散客')

    // 标签关闭按钮：清回散客
    await wrapper.find('.n-tag-close').trigger('click')
    expect(wrapper.text()).toContain('散客')
  })

  it('选会员后现金收款：建单带 user_id，确认文案含会员名，小票快照写入会员', async () => {
    createPosOrder.mockResolvedValue({ data: { code: 0, data: { order_no: 'D-002' } } })
    const wrapper = mountTab()
    await flushPromises()
    await pickMember(wrapper, memberA)
    await tapPad(wrapper, '8.8')

    await wrapper.findAll('button').find((b) => b.text().includes('现金收款'))!.trigger('click')
    expect(capturedDialog?.content).toContain('会员 张三')
    await capturedDialog!.onPositiveClick!()

    expect(createPosOrder).toHaveBeenCalledWith(
      expect.objectContaining({ user_id: 11, pay_type: 4, manual_amount: 8.8 })
    )
    const receipt = wrapper.findComponent({ name: 'ReceiptModal' }).props('receipt')
    expect(receipt.user_id).toBe(11)
    expect(receipt.member_label).toBe('张三')
    // 收款成功后保留会员选择（连续收款），金额清零
    expect(wrapper.text()).toContain('张三')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('')
  })

  it('选会员后付款码建单带 user_id；换会员后同金额不复用放弃的遗留单', async () => {
    createPosOrder
      .mockResolvedValueOnce({ data: { code: 0, data: { id: 101, order_no: 'D-101', pay_amount: 6.6, payment_no: 'ZF101' } } })
      .mockResolvedValueOnce({ data: { code: 0, data: { id: 102, order_no: 'D-102', pay_amount: 6.6, payment_no: 'ZF102' } } })
    const wrapper = mountTab()
    await flushPromises()
    await pickMember(wrapper, memberA)
    await tapPad(wrapper, '6.6')
    await wrapper.findAll('button').find((b) => b.text().includes('付款码收款'))!.trigger('click')
    await flushPromises()
    expect(createPosOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({ user_id: 11, pay_type: 5 }))

    // 放弃扫码（弹窗关闭并遗留待支付单），换会员后同金额再发起：不得复用 A 名下的单
    const dialogStub = wrapper.findComponent({ name: 'CodePayDialog' })
    dialogStub.vm.$emit('update:show', false)
    dialogStub.vm.$emit('close-abandoned')
    await flushPromises()
    await pickMember(wrapper, memberB)
    await wrapper.findAll('button').find((b) => b.text().includes('付款码收款'))!.trigger('click')
    await flushPromises()
    expect(createPosOrder).toHaveBeenCalledTimes(2)
    expect(createPosOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({ user_id: 22 }))
  })

  it('散客路径回归：不选会员时建单仍为 user_id=0', async () => {
    createPosOrder.mockResolvedValue({ data: { code: 0, data: { order_no: 'D-003' } } })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '2')
    await wrapper.findAll('button').find((b) => b.text().includes('现金收款'))!.trigger('click')
    await capturedDialog!.onPositiveClick!()
    expect(createPosOrder).toHaveBeenCalledWith(
      expect.objectContaining({ user_id: 0, pay_type: 4, manual_amount: 2 })
    )
  })
})

describe('DirectPayTab F2/F3 快捷键', () => {
  it('F3 触发现金确认弹窗', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '2')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'F3' }))
    expect(capturedDialog?.onPositiveClick).toBeTruthy()
  })

  it('F2 触发付款码建单', async () => {
    createPosOrder.mockResolvedValue({
      data: { code: 0, data: { id: 97, order_no: 'D-097', pay_amount: 2, payment_no: 'ZF097' } },
    })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '2')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2' }))
    await flushPromises()
    expect(createPosOrder).toHaveBeenCalled()
  })

  it('未输入金额时快捷键只提示不放行', async () => {
    const wrapper = mountTab()
    await flushPromises()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'F3' }))
    expect(warningMock).toHaveBeenCalled()
    expect(createPosOrder).not.toHaveBeenCalled()
    expect(capturedDialog).toBeNull()
  })

  it('扫码弹窗打开期间快捷键忽略（防重复建单）', async () => {
    createPosOrder.mockResolvedValue({
      data: { code: 0, data: { id: 96, order_no: 'D-096', pay_amount: 6.6, payment_no: 'ZF096' } },
    })
    const wrapper = mountTab()
    await flushPromises()
    await tapPad(wrapper, '6.6')
    await wrapper.findAll('button').find((b) => b.text().includes('付款码收款'))!.trigger('click')
    await flushPromises()

    // 弹窗已开，再按 F2 不得重复建单
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2' }))
    await flushPromises()
    expect(createPosOrder).toHaveBeenCalledTimes(1)
  })
})
