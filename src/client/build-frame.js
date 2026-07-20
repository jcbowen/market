/**
 * H5 构建后处理脚本
 *
 * 将 uni-app 构建产物整理为：
 * - index.html：PC 端模拟手机边框入口（Frame 模式）
 * - app.html：真实的 uni-app H5 应用入口（App 模式）
 *
 * Frame 模式通过 iframe 加载 app.html，使内部 fixed 定位元素
 * 相对于 iframe 自身视口固定，避免受外层 transform 缩放影响。
 */

const fs = require('fs')
const path = require('path')

const h5Path = path.resolve(__dirname, '../../bin/static/h5')
const frameHtmlPath = path.resolve(__dirname, 'frame.html')

const appHtmlPath = path.join(h5Path, 'app.html')
const indexHtmlPath = path.join(h5Path, 'index.html')

if (!fs.existsSync(indexHtmlPath)) {
  console.error('[build-frame] 未找到构建产物 index.html，跳过处理')
  process.exit(1)
}

// 1. 将 vite 构建生成的 index.html 重命名为 app.html，作为真实应用入口
fs.renameSync(indexHtmlPath, appHtmlPath)
console.log('[build-frame] 已生成 app.html')

// 2. 将 frame.html 复制为 index.html，并替换 iframe 加载地址为 app.html
let frameHtml = fs.readFileSync(frameHtmlPath, 'utf8')
frameHtml = frameHtml.replace(/\.\/index\.html/g, './app.html')
fs.writeFileSync(indexHtmlPath, frameHtml)
console.log('[build-frame] 已生成 index.html（Frame 模式）')
