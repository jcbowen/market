/**
 * H5 构建后处理脚本（跨平台）
 *
 * 将 uni-app 构建产物从 dist/build/h5 移动到 ../../bin/static/h5，
 * 然后调用 build-frame.js 生成 PC 端模拟手机边框入口，最后清理 dist 目录。
 */

const fs = require('fs')
const path = require('path')

const projectRoot = path.resolve(__dirname, '../../..')
const sourceDir = path.resolve(__dirname, '../dist/build/h5')
const targetDir = path.join(projectRoot, 'bin/static/h5')
const distDir = path.resolve(__dirname, '../dist')

// 递归删除目录
function removeDir(dir) {
  if (!fs.existsSync(dir)) return
  fs.rmSync(dir, { recursive: true, force: true })
}

// 递归移动目录（跨平台替代 mv）
// Windows 下 fs.renameSync 跨目录移动非空目录容易因权限/非空限制失败，
// 因此采用"复制到目标 + 删除源目录"的安全实现。
function moveDir(src, dest) {
  if (!fs.existsSync(src)) {
    throw new Error(`源目录不存在: ${src}`)
  }
  removeDir(dest)
  fs.mkdirSync(path.dirname(dest), { recursive: true })
  fs.cpSync(src, dest, { recursive: true, force: true })
  removeDir(src)
}

// 1. 清理旧产物
removeDir(targetDir)

// 2. 移动构建产物
moveDir(sourceDir, targetDir)
console.log('[build-h5] 已移动构建产物到', targetDir)

// 3. 生成 Frame 模式入口
require('../build-frame')

// 4. 清理 dist 目录
removeDir(distDir)
console.log('[build-h5] 已清理 dist 目录')
