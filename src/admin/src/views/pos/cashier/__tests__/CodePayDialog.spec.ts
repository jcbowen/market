import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CodePayDialog from '../CodePayDialog.vue'
import { naiveTestComponents } from './naiveStubs'

/**
 * CodePayDialog 付款码扫码弹窗状态机单测（收银台收款核心链路）
 * 覆盖：0 元拦截 / 渠道拒绝清码重扫 / 扣款成功 emit / PAYING 轮询 / WS 事件加速核实 /
 *       改现金出口 / 关闭守卫
 */

const { payPosOrderCode, getPosOrderPayStatus, payPosOrderCash } = vi.hoisted(() => ({
  payPosOrderCode: vi.fn(),
  getPosOrderPayStatus: vi.fn(),
  payPosOrderCash: vi.fn(),
}))

vi.mock('@/api/order', () => ({ payPosOrderCode, getPosOrderPayStatus, payPosOrderCash }))

/** 弹窗订阅的实时事件处理器（type → 回调列表），供测试内手动投递 */
const { realtimeHandlers, isRealtimeConnected } = vi.hoisted(() => ({
  realtimeHandlers: new Map<string, Array<(data: unknown) => void>>(),
  isRealtimeConnected: vi.fn(() => false),
}))

vi.mock('@/utils/realtime', () => ({
  isRealtimeConnected,
  onRealtime: (type: string, handler: (data: unknown) => void) => {
    realtimeHandlers.set(type, [...(realtimeHandlers.get(type) ?? []), handler])
    return () => realtimeHandlers.delete(type)
  },
}))

const { useMessage } = vi.hoisted(() => ({ useMessage: vi.fn() }))
vi.mock('naive-ui', () => ({ useMessage }))

const warningMock = vi.fn()
beforeEach(() => {
  vi.clearAllMocks()
  realtimeHandlers.clear()
  // 默认按「WS 断开」口径（兜底 2s 轮询），需要在线口径的用例自行覆盖
  isRealtimeConnected.mockReturnValue(false)
  useMessage.mockReturnValue({ warning: warningMock, info: vi.fn(), success: vi.fn(), error: vi.fn() })
})

/** 投递一个实时事件到弹窗订阅的处理器 */
function emitRealtime(type: string, data: Record<string, unknown>) {
  realtimeHandlers.get(type)?.forEach((handler) => handler(data))
}

/** 默认弹窗入参（待支付订单凭据） */
const baseProps = {
  show: true,
  orderId: 300,
  orderNo: 'POSTEST-D',
  amount: 12.5,
  paymentNo: 'ZFPOSTEST',
  pickupCode: '8888',
}

function mountDialog(props: Partial<typeof baseProps> = {}) {
  return mount(CodePayDialog, {
    props: { ...baseProps, ...props },
    global: { components: naiveTestComponents },
  })
}

/** 提交付款码（在扫码输入框键入后回车） */
async function submitCode(wrapper: ReturnType<typeof mountDialog>, code = '286688') {
  const input = wrapper.find('input')
  await input.setValue(code)
  await input.trigger('keydown', { key: 'Enter' })
  await flushPromises()
}

afterEach(() => {
  vi.useRealTimers()
})

describe('CodePayDialog', () => {
  it('渲染金额、订单号与扫码输入框', () => {
    const wrapper = mountDialog()
    expect(wrapper.find('.codepay-amount').text()).toContain('￥12.50')
    expect(wrapper.find('.codepay-order').text()).toContain('POSTEST-D')
    expect(wrapper.find('input').attributes('placeholder')).toContain('付款码')
  })

  it('0 元订单提交直接失败提示，不调扣款接口', async () => {
    const wrapper = mountDialog({ amount: 0 })
    await submitCode(wrapper)
    expect(payPosOrderCode).not.toHaveBeenCalled()
    expect(wrapper.find('.codepay-tip').text()).toContain('不支持 0 元')
    expect(wrapper.emitted('paid')).toBeUndefined()
  })

  it('扣款参数携带订单 ID、支付单号与付款码', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAID', pay_type: 2 } } })
    const wrapper = mountDialog()
    await submitCode(wrapper, '286688')
    expect(payPosOrderCode).toHaveBeenCalledWith(300, { payment_no: 'ZFPOSTEST', auth_code: '286688' })
  })

  it('渠道确定性拒绝（FAILED）：显示渠道信息、清码、弹窗不关', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'FAILED', message: '余额不足' } } })
    const wrapper = mountDialog()
    await submitCode(wrapper)
    expect(wrapper.find('.codepay-tip').text()).toContain('余额不足')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('paid')).toBeUndefined()
    expect(wrapper.emitted('update:show')).toBeUndefined()
  })

  it('扣款成功（PAID）：按实际渠道 emit paid 并透传取货码', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAID', pay_type: 2 } } })
    const wrapper = mountDialog()
    await submitCode(wrapper)
    expect(wrapper.emitted('paid')).toEqual([[{ payLabel: '支付宝', via: 'code', pickupCode: '8888' }]])
  })

  it('扣款成功（PAID）未知渠道回「付款码支付」兜底文案', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAID', pay_type: 99 } } })
    const wrapper = mountDialog({ pickupCode: undefined })
    await submitCode(wrapper)
    expect(wrapper.emitted('paid')![0][0]).toMatchObject({ payLabel: '付款码支付', pickupCode: undefined })
  })

  it('PAYING 用户支付中：轮询查询到已收款后 emit paid（轮询路径文案）', async () => {
    vi.useFakeTimers()
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: true } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)
    // PAYING 不立即出结果，状态提示进入轮询文案
    expect(wrapper.find('.codepay-tip').text()).toContain('支付中')
    expect(wrapper.emitted('paid')).toBeUndefined()

    // 2 秒后第一轮轮询命中已收款 → 收工
    await vi.advanceTimersByTimeAsync(2000)
    expect(getPosOrderPayStatus).toHaveBeenCalledWith(300, 'ZFPOSTEST')
    expect(wrapper.emitted('paid')).toEqual([[{ payLabel: '付款码支付', via: 'code', pickupCode: '8888' }]])
  })

  it('PAYING 轮询超 3 分钟未定：停止轮询并提示改走现金或订单查询', async () => {
    vi.useFakeTimers()
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: false } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)
    // 连续推进 3 分钟（每轮 2s），期间保持未收款
    for (let i = 0; i < 95; i++) {
      await vi.advanceTimersByTimeAsync(2000)
    }
    expect(wrapper.find('.codepay-tip').text()).toContain('改用现金')
    expect(wrapper.emitted('paid')).toBeUndefined()
  })

  it('PAYING 期间收到本单 order.paid：立刻核实并收工（不等轮询）', async () => {
    vi.useFakeTimers()
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: true } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)
    expect(wrapper.emitted('paid')).toBeUndefined()

    emitRealtime('order.paid', { order_no: 'POSTEST-D' })
    await flushPromises()

    // 资金结论仍来自权威查询，事件只负责叫醒
    expect(getPosOrderPayStatus).toHaveBeenCalledWith(300, 'ZFPOSTEST')
    expect(wrapper.emitted('paid')).toEqual([[{ payLabel: '付款码支付', via: 'code', pickupCode: '8888' }]])
    // 收工后清掉定时器：不再重复查询、不重复出小票
    await vi.advanceTimersByTimeAsync(10000)
    expect(getPosOrderPayStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('paid')).toHaveLength(1)
  })

  it('同社区其他订单的支付事件不触发本弹窗核实', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: true } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)

    emitRealtime('order.paid', { order_no: 'POSTEST-OTHER' })
    await flushPromises()
    expect(getPosOrderPayStatus).not.toHaveBeenCalled()
    expect(wrapper.emitted('paid')).toBeUndefined()
  })

  it('事件与在途轮询重叠只收工一次（不出两张小票）', async () => {
    vi.useFakeTimers()
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: true } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)

    // 同一轮内连投两次：两笔核实同时在途
    emitRealtime('order.paid', { order_no: 'POSTEST-D' })
    emitRealtime('order.paid', { order_no: 'POSTEST-D' })
    await flushPromises()
    expect(wrapper.emitted('paid')).toHaveLength(1)

    await vi.advanceTimersByTimeAsync(10000)
    expect(wrapper.emitted('paid')).toHaveLength(1)
  })

  it('旧单核实结果晚到且已换单：整份丢弃，不给新单出小票', async () => {
    vi.useFakeTimers()
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    // 第一笔（D）的核实挂起，第二笔（E）的核实直接判未收款
    const pendingResolvers: Array<(v: unknown) => void> = []
    getPosOrderPayStatus
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            pendingResolvers.push(resolve)
          }),
      )
      .mockResolvedValue({ data: { code: 0, data: { paid: false } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)
    emitRealtime('order.paid', { order_no: 'POSTEST-D' })
    await flushPromises()
    expect(getPosOrderPayStatus).toHaveBeenCalledTimes(1)

    // 收银员关窗换单：paidEmitted 复位、订单号换成 POSTEST-E
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, orderNo: 'POSTEST-E', orderId: 301, paymentNo: 'ZFPOTHER' })
    expect(wrapper.emitted('paid')).toBeUndefined()

    // D 的渠道事实在 E 的弹窗上下文里返回 —— 只能丢弃，否则小票与取货码张冠李戴
    pendingResolvers[0]?.({ data: { code: 0, data: { paid: true } } })
    await flushPromises()
    expect(wrapper.emitted('paid')).toBeUndefined()

    await vi.advanceTimersByTimeAsync(10000)
    expect(wrapper.emitted('paid')).toBeUndefined()
  })

  it('WS 在线时轮询间隔放宽到 5s（断开时保持 2s 兜底）', async () => {
    vi.useFakeTimers()
    isRealtimeConnected.mockReturnValue(true)
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'PAYING' } } })
    getPosOrderPayStatus.mockResolvedValue({ data: { code: 0, data: { paid: false } } })

    const wrapper = mountDialog()
    await submitCode(wrapper)

    await vi.advanceTimersByTimeAsync(2000)
    expect(getPosOrderPayStatus).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3000)
    expect(getPosOrderPayStatus).toHaveBeenCalledTimes(1)
  })

  it('扣款失败改用现金：调 pay-cash 后 emit paid（现金路径）', async () => {
    payPosOrderCash.mockResolvedValue({ data: { code: 0, data: { paid: true } } })
    const wrapper = mountDialog()
    await wrapper.findAll('button').find((b) => b.text().includes('改用现金'))!.trigger('click')
    await flushPromises()
    expect(payPosOrderCash).toHaveBeenCalledWith(300)
    expect(wrapper.emitted('paid')).toEqual([[{ payLabel: '现金', via: 'cash', pickupCode: '8888' }]])
  })

  it('改现金失败：停留弹窗不出结果（可重试）', async () => {
    payPosOrderCash.mockResolvedValue({ data: { code: 500, message: '关闭原待支付单失败' } })
    const wrapper = mountDialog()
    await wrapper.findAll('button').find((b) => b.text().includes('改用现金'))!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('paid')).toBeUndefined()
  })

  it('idle 状态主动关闭：emit update:show(false) + close-abandoned', async () => {
    const wrapper = mountDialog()
    await wrapper.findAll('button').find((b) => b.text().includes('暂不收款'))!.trigger('click')
    expect(wrapper.emitted('update:show')).toEqual([[false]])
    expect(wrapper.emitted('close-abandoned')).toHaveLength(1)
  })

  it('submitting 中不允许关闭（防漏单），仅提示', async () => {
    // 扣款请求悬挂：保持 submitting 态
    payPosOrderCode.mockReturnValue(new Promise(() => {}))
    const wrapper = mountDialog()
    await wrapper.find('input').setValue('286688')
    await wrapper.find('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()

    // 按钮在 submitting 中已被禁用（DOM 层拦截），父组件调用暴露的 onRequestClose 亦被状态机拦截
    expect(
      wrapper.findAll('button').find((b) => b.text().includes('暂不收款'))!.attributes('disabled'),
    ).toBeDefined()
    await (wrapper.vm as unknown as { onRequestClose: () => void }).onRequestClose()
    expect(warningMock).toHaveBeenCalled()
    expect(wrapper.emitted('update:show')).toBeUndefined()
    expect(wrapper.emitted('close-abandoned')).toBeUndefined()
  })

  it('弹窗重新打开时重置状态（failed → idle、清码）', async () => {
    payPosOrderCode.mockResolvedValue({ data: { code: 0, data: { state: 'FAILED', message: '余额不足' } } })
    const wrapper = mountDialog()
    await submitCode(wrapper)
    expect(wrapper.find('.codepay-tip').text()).toContain('余额不足')

    // 关闭再打开：状态机复位
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.find('.codepay-tip').text()).not.toContain('余额不足')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('')
  })
})
