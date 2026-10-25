import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import OrderTab from '../OrderTab.vue'
import { naiveTestComponents } from './naiveStubs'

/**
 * OrderTab 商品搜索过滤单测（搜索能力完善）
 * 覆盖：中文名 / 拼音首字母 / 拼音全拼三通道匹配、无命中空态、仅看有货隐藏缺货、
 *       A-Z 首字母栏筛选、关键词与开关叠加、条码回车直达加购（扫码枪路径）
 */

// --- API 替身（挂载即拉取：在售商品/挂单/社区渠道配置/支付方式）---
const { getProductList, getProductByBarcode, getPosHeldOrders, getCommunityProfile, listPosPayMethods } = vi.hoisted(() => ({
  getProductList: vi.fn(),
  getProductByBarcode: vi.fn(),
  getPosHeldOrders: vi.fn(),
  getCommunityProfile: vi.fn(),
  listPosPayMethods: vi.fn(),
}))

vi.mock('@/api/product', () => ({ getProductByBarcode, getProductDetail: vi.fn(), getProductList }))
vi.mock('@/api/order', () => ({
  createPosHeldOrder: vi.fn(),
  createPosOrder: vi.fn(),
  deletePosHeldOrder: vi.fn(),
  ensurePosCodePaySession: vi.fn(),
  getPosHeldOrders,
  listPosPayMethods,
}))
vi.mock('@/api/marketing', () => ({ getCouponRecords: vi.fn() }))
vi.mock('@/api/user', () => ({ getCommunityProfile }))

const { useDialog, useMessage } = vi.hoisted(() => ({ useDialog: vi.fn(), useMessage: vi.fn() }))
vi.mock('naive-ui', () => ({ useDialog, useMessage }))

// 模板中未经 script 导入的 n-* 组件替身（真实环境由 main.ts 全局注册）
const globalStubs = {
  NTag: {
    name: 'NTag',
    props: { size: String, type: String, bordered: Boolean },
    template: '<span class="n-tag-stub"><slot /></span>',
  },
  NEmpty: {
    name: 'NEmpty',
    props: { description: String, size: String },
    template: '<div class="n-empty-stub">{{ description }}</div>',
  },
  NSpin: { name: 'NSpin', props: { show: Boolean }, template: '<div class="n-spin-stub"><slot /></div>' },
  NIcon: { name: 'NIcon', template: '<i class="n-icon-stub"><slot /></i>' },
  // 优惠券选择器替身：本测不覆盖券交互，仅消除 "Failed to resolve component" 警告噪音
  NSelect: { name: 'NSelect', props: { value: { default: null }, options: { type: Array, default: () => [] } }, template: '<select class="n-select-stub" />' },
  NImage: { name: 'NImage', props: { src: String }, template: '<img class="n-image-stub" :src="src" />' },
  NRadioGroup: { name: 'NRadioGroup', template: '<div class="n-radio-group-stub"><slot /></div>' },
  NRadioButton: {
    name: 'NRadioButton',
    props: { value: { type: [String, Number], required: false } },
    template: '<button class="n-radio-button-stub"><slot /></button>',
  },
  NInputNumber: {
    name: 'NInputNumber',
    props: { value: { default: null }, min: Number, max: Number, precision: Number, placeholder: String },
    emits: ['update:value'],
    template:
      '<input class="n-input-number-stub" :value="value ?? \'\'" :placeholder="placeholder" @input="$emit(\'update:value\', Number($event.target.value))" />',
  },
  NSwitch: {
    name: 'NSwitch',
    props: { value: { type: Boolean, default: false }, size: String },
    emits: ['update:value'],
    template: '<input type="checkbox" class="n-switch-stub" :checked="value" @change="$emit(\'update:value\', $event.target.checked)" />',
  },
}

/** 在售商品夹具（含一款缺货；两款可乐用于验证全拼/首字母均为包含匹配） */
const PRODUCTS = [
  { id: 1, name: '可乐', category_id: 10, category_name: '饮料', price: 3, min_price: 3, stock: 10, status: 1 },
  { id: 2, name: '雪碧', category_id: 10, category_name: '饮料', price: 3, min_price: 3, stock: 5, status: 1 },
  { id: 3, name: '苹果', category_id: 20, category_name: '水果', price: 5, min_price: 5, stock: 0, status: 1 },
  { id: 4, name: '可口可乐罐装', category_id: 10, category_name: '饮料', price: 3.5, min_price: 3.5, stock: 8, status: 1 },
]

// 组件挂载时注册 window keydown 监听：必须逐用例卸载，否则快捷键监听跨用例串扰
const wrappers: VueWrapper[] = []
afterEach(() => {
  while (wrappers.length) wrappers.pop()!.unmount()
})

beforeEach(() => {
  setActivePinia(createPinia())
  useMessage.mockReturnValue({ success: vi.fn(), warning: vi.fn(), info: vi.fn(), error: vi.fn() })
  useDialog.mockReturnValue({ warning: vi.fn(), success: vi.fn(), error: vi.fn() })
  getProductList.mockResolvedValue({ data: { code: 0, data: { list: PRODUCTS, total: PRODUCTS.length } } })
  getPosHeldOrders.mockResolvedValue({ data: { code: 0, data: { list: [] } } })
  getCommunityProfile.mockResolvedValue({ data: { code: 0, data: { enable_self_pickup: true, enable_delivery: true } } })
  // 仅现金渠道：付款码入口隐藏，聚焦搜索逻辑本身
  listPosPayMethods.mockResolvedValue({ data: { code: 0, data: { list: [{ pay_type: 4, label: '现金' }] } } })
})

function mountTab() {
  const wrapper = mount(OrderTab, {
    global: {
      components: { ...naiveTestComponents, ...globalStubs },
      stubs: { UserPicker: true, CodePayDialog: true, ReceiptModal: true },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** 当前商品网格中的商品名列表（过滤结果断言） */
async function cardNames(wrapper: VueWrapper): Promise<string[]> {
  await flushPromises()
  return wrapper.findAll('.product-card .p-name').map((n) => n.text())
}

/** 向搜索框输入关键词（v-model 驱动本地过滤，与扫码枪键入等价） */
async function typeKeyword(wrapper: VueWrapper, kw: string) {
  await wrapper.find('.search').setValue(kw)
}

/** 切换「仅看有货」开关 */
async function toggleInStock(wrapper: VueWrapper, on: boolean) {
  await wrapper.find('.stock-filter input').setValue(on)
}

describe('OrderTab 商品搜索过滤', () => {
  it('挂载后拉取在售商品并渲染全部卡片', async () => {
    const wrapper = mountTab()
    expect(await cardNames(wrapper)).toEqual(['可乐', '雪碧', '苹果', '可口可乐罐装'])
  })

  it('中文关键词按商品名匹配', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await typeKeyword(wrapper, '可乐')
    expect(await cardNames(wrapper)).toEqual(['可乐', '可口可乐罐装'])
  })

  it('字母关键词按拼音首字母匹配（xb → 雪碧）', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await typeKeyword(wrapper, 'xb')
    expect(await cardNames(wrapper)).toEqual(['雪碧'])
  })

  it('字母关键词按拼音全拼匹配（kele → 可乐/可口可乐罐装）', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await typeKeyword(wrapper, 'kele')
    expect(await cardNames(wrapper)).toEqual(['可乐', '可口可乐罐装'])
  })

  it('字母关键词全拼与首字母均不命中时展示空态', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await typeKeyword(wrapper, 'xyz')
    expect(await cardNames(wrapper)).toEqual([])
    expect(wrapper.find('.n-empty-stub').exists()).toBe(true)
  })

  it('A-Z 首字母栏按商品名首字母筛选（K → 两款可乐）', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await wrapper.findAll('.letter').find((l) => l.text() === 'K')!.trigger('click')
    expect(await cardNames(wrapper)).toEqual(['可乐', '可口可乐罐装'])
  })

  it('仅看有货：开启隐藏缺货商品，关闭后恢复展示', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await toggleInStock(wrapper, true)
    expect(await cardNames(wrapper)).toEqual(['可乐', '雪碧', '可口可乐罐装'])
    await toggleInStock(wrapper, false)
    expect(await cardNames(wrapper)).toHaveLength(4)
  })

  it('仅看有货与关键词叠加：缺货商品即使命中也隐藏', async () => {
    const wrapper = mountTab()
    await flushPromises()
    await toggleInStock(wrapper, true)
    await typeKeyword(wrapper, 'pingguo')
    expect(await cardNames(wrapper)).toEqual([])
    await toggleInStock(wrapper, false)
    expect(await cardNames(wrapper)).toEqual(['苹果'])
  })
})

describe('OrderTab 条码回车直达', () => {
  it('纯数字关键词回车走条码精确查询，命中直接加购并清空输入', async () => {
    getProductByBarcode.mockResolvedValue({
      data: {
        code: 0,
        data: {
          product: { id: 1, name: '可乐' },
          sku: { id: 11, product_id: 1, sku_code: 'KL-330', spec_name: '330ml', price: 3, stock: 10 },
        },
      },
    })
    const wrapper = mountTab()
    await flushPromises()

    await typeKeyword(wrapper, '6901234567890')
    await wrapper.find('.search').trigger('keyup.enter')
    await flushPromises()

    expect(getProductByBarcode).toHaveBeenCalledWith('6901234567890')
    // 命中加购：小票出现该 SKU，搜索框清空（连续扫码）
    expect(wrapper.findAll('.cart-row')).toHaveLength(1)
    expect(wrapper.find('.cart-name').text()).toContain('可乐')
    expect((wrapper.find('.search').element as HTMLInputElement).value).toBe('')
  })

  it('条码未命中：保留关键词回落本地商品名过滤', async () => {
    // request 拦截器统一弹错误提示，组件内静默回落
    getProductByBarcode.mockRejectedValue(new Error('未找到该条码对应的商品'))
    const wrapper = mountTab()
    await flushPromises()

    await typeKeyword(wrapper, '999999999')
    await wrapper.find('.search').trigger('keyup.enter')
    await flushPromises()

    // 999999999 非纯字母、不含于任何商品名 → 本地过滤无结果
    expect(await cardNames(wrapper)).toEqual([])
    expect((wrapper.find('.search').element as HTMLInputElement).value).toBe('999999999')
  })
})
