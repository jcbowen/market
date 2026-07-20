import { chromium } from '/Users/bowen/.nvm/versions/node/v18.20.5/lib/node_modules/playwright/index.mjs'

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 375, height: 750 } })

await page.goto('http://localhost:18080/s/app.html#/', { waitUntil: 'networkidle' })
await page.waitForTimeout(2000)

const before = await page.evaluate(() => {
  const head = document.querySelector('uni-page-head')
  const tabbar = document.querySelector('uni-tabbar')
  return {
    headRect: head?.getBoundingClientRect(),
    headStyle: head ? window.getComputedStyle(head) : null,
    tabbarRect: tabbar?.getBoundingClientRect(),
    scrollY: window.scrollY,
  }
})

console.log('滚动前:', JSON.stringify(before, null, 2))

await page.evaluate(() => window.scrollTo(0, 300))
await page.waitForTimeout(500)

const after = await page.evaluate(() => {
  const head = document.querySelector('uni-page-head')
  const tabbar = document.querySelector('uni-tabbar')
  return {
    headRect: head?.getBoundingClientRect(),
    headStyle: head ? window.getComputedStyle(head) : null,
    tabbarRect: tabbar?.getBoundingClientRect(),
    scrollY: window.scrollY,
  }
})

console.log('滚动后:', JSON.stringify(after, null, 2))

await browser.close()
