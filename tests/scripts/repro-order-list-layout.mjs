/**
 * 订单列表布局复现脚本（临时诊断用）
 * mock 后端接口 + 注入登录态，打开订单列表页并测量 DOM 高度，验证 scroll-view 高度塌陷
 * 运行：node tests/scripts/repro-order-list-layout.mjs
 */
import { chromium } from 'playwright'

const ORDERS = {
  code: 0,
  message: 'ok',
  data: {
    list: [
      {
        id: 101, order_no: '20261030001', status: 2, status_text: '待自提',
        delivery_type: 1, pay_type: 3, source: 1,
        total_amount: 5.5, discount_amount: 0, delivery_fee: 0, pay_amount: 5.5,
        community_name: '盘龙金茂悦', created_at: '2026-10-30 07:00:00',
        items: [{ product_id: 1, sku_id: 1, product_name: '乐游 原味薯片', spec_name: '原味薯片', price: 5.5, quantity: 1, product_image: '' }],
      },
      {
        id: 102, order_no: '20261030002', status: 1, status_text: '待支付',
        delivery_type: 2, pay_type: 1, source: 1,
        total_amount: 6.9, discount_amount: 0, delivery_fee: 0, pay_amount: 6.9,
        community_name: '盘龙金茂悦', created_at: '2026-10-30 07:10:00',
        expire_at: '2026-10-30 08:30:00',
        items: [{ product_id: 2, sku_id: 2, product_name: '麦香源 巧克力夹心饼干', spec_name: '夹心饼干', price: 6.9, quantity: 1, product_image: '' }],
      },
    ],
    total: 2,
  },
}

const CHANNELS = {
  code: 0,
  message: 'ok',
  data: {
    channels: [
      { channel: 'wechat', name: '微信支付' },
      { channel: 'alipay', name: '支付宝' },
      { channel: 'cash', name: '货到付款' },
    ],
  },
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 390, height: 844 } })

// 注意：后注册的 route 优先级更高，先注册兜底，再注册具体接口 mock
await page.route('**/api/v1/**', (route) => route.fulfill({ json: { code: 0, message: 'ok', data: null } }))
await page.route('**/api/v1/orders?**', (route) => route.fulfill({ json: ORDERS }))
await page.route('**/api/v1/orders/102**', (route) =>
  route.fulfill({ json: { code: 0, message: 'ok', data: ORDERS.data.list[1] } }))
await page.route('**/api/v1/payments/channels**', (route) => route.fulfill({ json: CHANNELS }))

await page.goto('http://localhost:5173/s/#/pages/login/login')
await page.waitForTimeout(3000)
// 用页面内的 uni 运行时写登录态，保证存储格式与真实登录一致
await page.evaluate(() => {
  window.uni.setStorageSync('access_token', 'fake-token-for-repro')
  window.uni.setStorageSync('user_info', { id: 1, phone: '13800000000', nickname: '诊断' })
})
// store 启动时读取一次 storage，写完必须整页 reload 才能让 pinia 里 token 生效
await page.reload()
await page.waitForTimeout(3000)

await page.goto('http://localhost:5173/s/#/pagesOrder/order/list')
await page.waitForTimeout(4000)

await page.screenshot({ path: '.workbuddy/tmp/repro-list.png' })

const metrics = await page.evaluate(() => {
  const pick = (el) => {
    if (!el) return null
    const r = el.getBoundingClientRect()
    const cs = getComputedStyle(el)
    return {
      tag: el.tagName?.toLowerCase?.() || el.constructor?.name,
      cls: el.className?.baseVal ?? String(el.className ?? ''),
      top: Math.round(r.top), height: Math.round(r.height),
      display: cs.display, flex: cs.flex, overflowY: cs.overflowY,
      position: cs.position, minHeight: cs.minHeight,
    }
  }
  const body = document.body
  return {
    innerHeight: window.innerHeight,
    innerWidth: window.innerWidth,
    vhVar: getComputedStyle(document.documentElement).getPropertyValue('--window-top'),
    pageEl: pick(document.querySelector('.order-list-page')),
    tabsEl: pick(document.querySelector('.order-list-page > *')),
    scrollViews: [...document.querySelectorAll('.order-list-page uni-scroll-view, .order-list-page scroll-view')].map(pick),
    uniPageBody: pick(document.querySelector('uni-page-body')),
    uniPageWrapper: pick(document.querySelector('uni-page-wrapper')),
    pageChildren: [...(document.querySelector('.order-list-page')?.children || [])].map(pick),
    scrollInner: [...document.querySelectorAll('.order-list, .order-list *')].slice(0, 6).map(pick),
    cardCount: document.querySelectorAll('.order-card').length,
    actions: (() => {
      const el = document.querySelector('.order-actions')
      if (!el) return null
      const cs = getComputedStyle(el)
      const btn = el.querySelector('uni-button')
      const bcs = btn ? getComputedStyle(btn) : null
      const inner = btn ? btn.querySelector('*') : null
      return {
        actionsDisplay: cs.display,
        justifyContent: cs.justifyContent,
        btnClass: btn?.className,
        btnWidth: bcs?.width,
        btnDisplay: bcs?.display,
        btnMargin: bcs?.margin,
        btnFlex: bcs?.flex,
        innerTag: inner?.tagName,
        innerClass: inner?.className,
        innerStyle: inner?.getAttribute('style'),
        btnStyle: btn?.getAttribute('style'),
      }
    })(),
    loadmore: pick(document.querySelector('.order-list-page u-loadmore, .order-list-page uni-view.u-loadmore')),
  }
})
console.log(JSON.stringify(metrics, null, 2))

// --- 订单详情页：验证支付方式卡片直接渲染 ---
await page.goto('http://localhost:5173/s/#/pagesOrder/order/detail?id=102')
await page.waitForTimeout(4000)
await page.screenshot({ path: '.workbuddy/tmp/repro-detail.png', fullPage: true })
const detailMetrics = await page.evaluate(() => ({
  paymentCard: !!document.querySelector('.payment-card'),
  optionCount: document.querySelectorAll('.payment-option').length,
  checkedCount: document.querySelectorAll('.payment-radio.checked').length,
  payBtnVisible: !!document.querySelector('.footer-bar'),
}))
console.log('DETAIL:', JSON.stringify(detailMetrics))

await browser.close()
