import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent } from 'vue'
import OrdersTab from '../OrdersTab.vue'
import { NInput, NModal, NButton as NButtonStub, NSpace } from './naiveStubs'
import { ORDER_STATUS } from '@/constants/order'

/**
 * OrdersTab 订单查询筛选单测（搜索能力完善）
 * 覆盖：挂载基础查询参数、主条件（订单号/状态）触发查询、时间快捷档（今天）起止日期
 * 与页码重置、翻页、更多筛选条件（账单号）组装、回显标签 × 清除、一键重置
 */

// --- API 替身 ---
const { getOrderList, getOrderDetail, cancelOrder, payPosOrderCash, verifyPickup, ensurePosCodePaySession, getProductList } =
  vi.hoisted(() => ({
    getOrderList: vi.fn(),
    getOrderDetail: vi.fn(),
    cancelOrder: vi.fn(),
    payPosOrderCash: vi.fn(),
    verifyPickup: vi.fn(),
    ensurePosCodePaySession: vi.fn(),
    getProductList: vi.fn(),
  }))

vi.mock('@/api/order', () => ({
  cancelOrder,
  ensurePosCodePaySession,
  getOrderDetail,
  getOrderList,
  payPosOrderCash,
  verifyPickup,
}))
vi.mock('@/api/product', () => ({ getProductList }))

const { useDialog, useMessage } = vi.hoisted(() => ({ useDialog: vi.fn(), useMessage: vi.fn() }))
// script 内 import 的 NButton/NEmpty/NTag（setup 绑定优先于全局注册，模板 <n-tag> 实际解析到这里）
// NTag 需真实行为：closable 渲染 tag-close 按钮、close 事件透传（回显标签 × 清除条件用例）
vi.mock('naive-ui', () => {
  const Pass = (template: string) =>
    defineComponent({ template, props: { type: String, size: String, bordered: Boolean, disabled: Boolean, loading: Boolean } })
  return {
    useDialog,
    useMessage,
    // request.ts 在模块加载期创建离散消息 API（响应拦截器统一弹错）
    createDiscreteApi: () => ({ message: { success: vi.fn(), warning: vi.fn(), info: vi.fn(), error: vi.fn() } }),
    NButton: Pass('<button type="button" class="n-button-stub"><slot /></button>'),
    NEmpty: Pass('<div class="n-empty-stub"><slot /></div>'),
    // 状态列 render 用 h(NSpace) 包裹标签，需要提供替身避免 undefined
    NSpace: Pass('<div class="n-space-stub"><slot /></div>'),
    NTag: defineComponent({
      props: { closable: Boolean, type: String, size: String },
      emits: ['close'],
      template:
        '<span class="n-tag-stub"><slot /><i v-if="closable" class="tag-close" @click="$emit(\'close\')" /></span>',
    }),
  }
})

// 模板中经全局注册解析的 n-* 组件替身
const globalStubs = {
  NSelect: {
    name: 'NSelect',
    props: { value: { default: null }, options: { type: Array, default: () => [] }, clearable: Boolean, placeholder: String },
    emits: ['update:value'],
    template: `<select class="n-select-stub" @change="$emit('update:value', Number($event.target.value))">
      <option v-for="o in options" :key="o.value" :value="o.value" :selected="o.value === value">{{ o.label }}</option>
    </select>`,
  },
  NDatePicker: {
    name: 'NDatePicker',
    props: { value: { default: null }, type: String, clearable: Boolean },
    emits: ['update:value'],
    template: '<input class="n-date-picker-stub" @change="$emit(\'update:value\', null)" />',
  },
  NInputNumber: {
    name: 'NInputNumber',
    props: { value: { default: null }, min: Number, clearable: Boolean, placeholder: String },
    emits: ['update:value'],
    template:
      '<input class="n-input-number-stub" :value="value ?? \'\'" @input="$emit(\'update:value\', Number($event.target.value))" />',
  },
  NDataTable: { name: 'NDataTable', props: ['data', 'columns', 'rowKey', 'loading'], template: '<div class="n-data-table-stub" />' },
  NPagination: {
    name: 'NPagination',
    props: { page: Number, pageSize: Number, itemCount: Number },
    emits: ['update:page', 'update:page-size'],
    template: '<div class="n-pagination-stub" />',
  },
  NSpin: { name: 'NSpin', props: { show: Boolean }, template: '<div class="n-spin-stub"><slot /></div>' },
  NIcon: { name: 'NIcon', template: '<i class="n-icon-stub"><slot /></i>' },
  // 文案替身：本测不覆盖文案样式，仅消除 "Failed to resolve component" 警告噪音
  NText: { name: 'NText', props: { type: String, depth: String }, template: '<span class="n-text-stub"><slot /></span>' },
}

/** 订单列表空响应（本测只关注查询参数组装，不渲染行） */
function mockEmptyPage(total = 0) {
  getOrderList.mockResolvedValue({ data: { code: 0, data: { list: [], total } } })
}

const wrappers: VueWrapper[] = []
afterEach(() => {
  while (wrappers.length) wrappers.pop()!.unmount()
})

beforeEach(() => {
  setActivePinia(createPinia())
  useMessage.mockReturnValue({ success: vi.fn(), warning: vi.fn(), info: vi.fn(), error: vi.fn() })
  useDialog.mockReturnValue({ warning: vi.fn(), success: vi.fn(), error: vi.fn() })
  mockEmptyPage()
})

function mountTab() {
  const wrapper = mount(OrdersTab, {
    global: {
      components: { NModal, NInput, NButton: NButtonStub, NSpace, ...globalStubs },
      stubs: { UserPicker: true, RefundModal: true, UserOrdersModal: true, OrderDetailModal: true, CodePayDialog: true },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** 最近一次 getOrderList 的查询参数 */
function lastQuery(): Record<string, unknown> {
  const calls = getOrderList.mock.calls
  return calls[calls.length - 1][0] as Record<string, unknown>
}

/** 展开更多筛选折叠区 */
async function openMoreFilters(wrapper: VueWrapper) {
  const btn = wrapper.findAll('button').find((b) => b.text().startsWith('更多筛选'))
  await btn!.trigger('click')
}

/** 点击指定文案的按钮（查询/今天/重置等） */
async function clickButton(wrapper: VueWrapper, text: string) {
  const btn = wrapper.findAll('button').find((b) => b.text() === text)
  expect(btn, `按钮「${text}」应存在`).toBeTruthy()
  await btn!.trigger('click')
}

/** 按占位符定位 n-input 替身输入框（与模板 DOM 顺序解耦，防插入新条件后静默错位） */
function inputByPlaceholder(wrapper: VueWrapper, placeholder: string) {
  const input = wrapper.findAll('input.n-input-stub').find((i) => i.attributes('placeholder') === placeholder)
  expect(input, `占位符「${placeholder}」的输入框应存在`).toBeTruthy()
  return input!
}

/** 本地时区 YYYY-MM-DD（与组件 fmtDate 同实现，用于快捷档断言） */
function fmtLocalDate(d: Date): string {
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const day = `${d.getDate()}`.padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

describe('OrdersTab 订单查询筛选', () => {
  it('挂载即按基础参数拉取第一页（未设条件字段为 undefined）', async () => {
    mountTab()
    await flushPromises()
    expect(getOrderList).toHaveBeenCalledTimes(1)
    const q = lastQuery()
    expect(q.page).toBe(1)
    expect(q.page_size).toBe(10)
    expect(q.order_no).toBeUndefined()
    expect(q.status).toBeUndefined()
    expect(q.user_id).toBeUndefined()
    expect(q.start_time).toBeUndefined()
  })

  it('输入订单号点查询：前缀关键词入参且页码回到第一页', async () => {
    const wrapper = mountTab()
    await flushPromises()

    // 先翻到第 2 页
    const pagination = wrapper.findComponent({ name: 'NPagination' })
    await pagination.vm.$emit('update:page', 2)
    await flushPromises()
    expect(lastQuery().page).toBe(2)

    // 输入订单号 → 查询：页码重置为 1，order_no 进入参数
    await wrapper.find('.order-no-input').setValue('POS2026')
    await clickButton(wrapper, '查询')
    await flushPromises()
    const q = lastQuery()
    expect(q.page).toBe(1)
    expect(q.order_no).toBe('POS2026')
  })

  it('状态筛选下拉选择待支付：status 进入查询参数并触发刷新', async () => {
    const wrapper = mountTab()
    await flushPromises()

    await wrapper.find('.status-select').setValue(String(ORDER_STATUS.PENDING_PAY))
    await flushPromises()
    expect(lastQuery().status).toBe(ORDER_STATUS.PENDING_PAY)
  })

  it('时间快捷档「今天」：start/end 为今天日期且页码重置；再点同档取消', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await openMoreFilters(wrapper)

    // 翻页后应用快捷档：应回到第 1 页并带上今天起止
    await wrapper.findComponent({ name: 'NPagination' }).vm.$emit('update:page', 3)
    await flushPromises()
    await clickButton(wrapper, '今天')
    await flushPromises()
    const today = fmtLocalDate(new Date())
    let q = lastQuery()
    expect(q.page).toBe(1)
    expect(q.start_time).toBe(today)
    expect(q.end_time).toBe(today)

    // 再点同档：取消快捷，时间条件清空
    await clickButton(wrapper, '今天')
    await flushPromises()
    q = lastQuery()
    expect(q.start_time).toBeUndefined()
    expect(q.end_time).toBeUndefined()
  })

  it('更多筛选内设置账单号：入参组装且「更多筛选」按钮显示条件计数', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await openMoreFilters(wrapper)

    await wrapper.find('input.w-input').setValue('PAY20261025001')
    await clickButton(wrapper, '查询')
    await flushPromises()
    expect(lastQuery().pay_no).toBe('PAY20261025001')

    // 收起后按钮角标显示 1 项条件
    await clickButton(wrapper, '收起筛选')
    const moreBtn = wrapper.findAll('button').find((b) => b.text().startsWith('更多筛选'))
    expect(moreBtn!.text()).toContain('1')
  })

  it('回显标签 × 可清除对应条件并刷新', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await openMoreFilters(wrapper)

    // 设置账单号 + 电话两个条件（按占位符定位）
    await inputByPlaceholder(wrapper, '支付单号 / 渠道交易号').setValue('PAY001')
    await inputByPlaceholder(wrapper, '手机号').setValue('13800138000')
    await clickButton(wrapper, '查询')
    await flushPromises()

    // 收起折叠区，出现回显标签；点「电话 13800138000」的 × 只清电话
    await clickButton(wrapper, '收起筛选')
    const tags = wrapper.findAll('.filter-tags .n-tag-stub')
    expect(tags.map((t) => t.text())).toEqual(['账单号 PAY001', '电话 13800138000'])
    await tags[1].find('.tag-close').trigger('click')
    await flushPromises()

    const q = lastQuery()
    expect(q.receiver_phone).toBeUndefined()
    expect(q.pay_no).toBe('PAY001')
  })

  it('重置按钮清空全部条件回到第一页', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await openMoreFilters(wrapper)

    await inputByPlaceholder(wrapper, '支付单号 / 渠道交易号').setValue('PAY001')
    await inputByPlaceholder(wrapper, '8 位取货码').setValue('87654321')
    await clickButton(wrapper, '查询')
    await flushPromises()
    expect(lastQuery().pay_no).toBe('PAY001')
    expect(lastQuery().pickup_code).toBe('87654321')

    await clickButton(wrapper, '重置')
    await flushPromises()
    const q = lastQuery()
    expect(q.page).toBe(1)
    expect(q.pay_no).toBeUndefined()
    expect(q.pickup_code).toBeUndefined()
    expect(q.order_no).toBeUndefined()
  })
})
