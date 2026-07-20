import { chromium } from '/Users/bowen/.nvm/versions/node/v18.20.5/lib/node_modules/playwright/index.mjs'
import fs from 'fs'

const url = 'http://localhost:18080/s/'
const outputDir = '/Users/bowen/projects/mine/market/scripts'

// 确保输出目录存在
if (!fs.existsSync(outputDir)) {
  fs.mkdirSync(outputDir, { recursive: true })
}

const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } })
const page = await context.newPage()

console.log('访问:', url)
await page.goto(url, { waitUntil: 'networkidle' })
await page.waitForTimeout(2000)

// 截图
const screenshotPath = `${outputDir}/phone-frame-iframe-prod.png`
await page.screenshot({ path: screenshotPath, fullPage: false })
console.log('截图已保存:', screenshotPath)

// 检查关键元素
const frameElement = await page.$('#app-frame')
const frameShell = await page.$('#frame-shell')

let result = {}
if (!frameShell || !frameElement) {
  result = { error: '未找到 frame-shell 或 app-frame' }
} else {
  const shellRect = await frameShell.boundingBox()
  const frameRect = await frameElement.boundingBox()

  // 通过 Playwright 的 frame 对象访问 iframe 内部
  const allFrames = page.frames().map(f => f.url())
  console.log('所有 frame URLs:', JSON.stringify(allFrames, null, 2))
  const iframe = page.frames().find(f => f.url().includes('/app.html'))
  let iframeInfo = { found: false }
  if (iframe) {
    await iframe.waitForLoadState('networkidle')
    // 等待 Vue 应用渲染完成
    try {
      await iframe.waitForSelector('#app', { timeout: 10000 })
    } catch (e) {
      console.log('等待 #app 超时')
    }
    await page.waitForTimeout(2000)

    // 截取 iframe 内部页面内容，便于排查
    const iframeBodyHtml = await iframe.evaluate(() => document.body.innerHTML.substring(0, 500)).catch(err => err.message)
    console.log('iframe body 前 500 字符:', iframeBodyHtml)

    const hasApp = await iframe.locator('#app').count() > 0
    const pageHead = iframe.locator('uni-page-head')
    const tabbar = iframe.locator('uni-tabbar')
    const pageHeadBox = await pageHead.boundingBox().catch(() => null)
    const tabbarBox = await tabbar.boundingBox().catch(() => null)
    iframeInfo = {
      found: true,
      hasAppContent: hasApp,
      pageHeadRect: pageHeadBox,
      tabbarRect: tabbarBox,
      iframeBodyPreview: iframeBodyHtml,
    }
  }

  result = {
    shellSize: shellRect,
    frameSize: frameRect,
    iframeInfo,
  }
}

console.log('检测结果:', JSON.stringify(result, null, 2))

// 验证滚动时 fixed 元素是否固定
const iframe = page.frames().find(f => f.url().includes('/app.html'))
if (iframe) {
  // 在 iframe 内部使用 getBoundingClientRect 获取相对于 iframe viewport 的位置
  const before = await iframe.evaluate(() => {
    const pageHead = document.querySelector('uni-page-head')
    const tabbar = document.querySelector('uni-tabbar')
    return {
      pageHeadTop: pageHead?.getBoundingClientRect().top,
      tabbarBottom: tabbar?.getBoundingClientRect().bottom,
      scrollY: window.scrollY,
    }
  })

  // 在 iframe 内部滚动
  await iframe.evaluate(() => window.scrollTo(0, 300))
  await page.waitForTimeout(500)

  const after = await iframe.evaluate(() => {
    const pageHead = document.querySelector('uni-page-head')
    const tabbar = document.querySelector('uni-tabbar')
    return {
      pageHeadTop: pageHead?.getBoundingClientRect().top,
      tabbarBottom: tabbar?.getBoundingClientRect().bottom,
      scrollY: window.scrollY,
    }
  })

  console.log('滚动前后 fixed 元素位置（iframe 坐标系）:', JSON.stringify({ before, after }, null, 2))
}

await browser.close()
