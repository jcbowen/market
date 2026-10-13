/**
 * tabBar 页面路径常量生成脚本（跨平台）
 *
 * 从 src/pages.json 的 tabBar.list 读取页面路径，生成 src/utils/tab-bar-paths.ts，
 * 供 utils/navigate.ts 的 goPage 判断跳转方式使用，避免两处手工维护。
 *
 * 设计说明：
 * - pages.json 是 uni-app 编译期配置，运行时无官方跨端 API 读取 tabBar 列表，
 *   因此在编译期生成 TS 常量作为单一数据源。
 * - 生成文件提交 git 跟踪（clone 后无需先跑脚本即可构建），
 *   内容无变化时不写入，保持 git 工作区干净。
 * - 仅使用 Node 内置模块与 path/fs，Windows / macOS 通用。
 *
 * 用法：node scripts/gen-tab-bar-paths.mjs（dev/build 命令已自动前置执行）
 */

const fs = require('fs')
const path = require('path')

const pagesJsonPath = path.resolve(__dirname, '../src/pages.json')
const outputPath = path.resolve(__dirname, '../src/utils/tab-bar-paths.ts')

/** 生成文件头（提醒勿手动编辑） */
const GENERATED_HEADER = `/**
 * 本文件由 scripts/gen-tab-bar-paths.js 自动生成，请勿手动编辑
 * 数据来源：src/pages.json 的 tabBar.list
 * 重新生成：dev/build 命令自动执行，或手动运行 node scripts/gen-tab-bar-paths.js
 */

`

/**
 * 解析 pages.json 内容（容错处理 // 单行注释）
 * @param raw 文件原始内容
 * @returns 解析后的 JSON 对象
 */
function parsePagesJson(raw) {
  try {
    return JSON.parse(raw)
  } catch {
    // uni-app 的 pages.json 允许 // 注释，去掉后再解析
    const stripped = raw.replace(/^\s*\/\/.*$/gm, '')
    return JSON.parse(stripped)
  }
}

/**
 * 生成 TS 常量文件内容
 * @param paths tabBar 页面路径列表（不含前导斜杠）
 * @returns 完整文件内容
 */
function buildFileContent(paths) {
  const lines = paths.map((p) => `  '${p}',`)
  return `${GENERATED_HEADER}/** tabBar 页面路径列表（不含前导斜杠，需与 pages.json 保持一致） */\nexport const TAB_BAR_PATHS = [\n${lines.join('\n')}\n] as const\n`
}

function main() {
  const raw = fs.readFileSync(pagesJsonPath, 'utf8')
  const tabBar = parsePagesJson(raw).tabBar
  if (!tabBar || !Array.isArray(tabBar.list) || tabBar.list.length === 0) {
    throw new Error('pages.json 中未找到 tabBar.list 配置')
  }

  const paths = tabBar.list.map((item) => item.pagePath).filter(Boolean)
  if (paths.length === 0) {
    throw new Error('tabBar.list 中未找到任何 pagePath')
  }

  const content = buildFileContent(paths)
  // 换行统一为 LF 后比较，避免 Windows CRLF 导致的重复写入弄脏 git
  const existing = fs.existsSync(outputPath) ? fs.readFileSync(outputPath, 'utf8') : ''
  if (existing.replace(/\r\n/g, '\n') === content) {
    console.log(`[gen-tab-bar-paths] 无变化，跳过写入: ${path.relative(process.cwd(), outputPath)}`)
    return
  }
  fs.writeFileSync(outputPath, content, 'utf8')
  console.log(`[gen-tab-bar-paths] 已生成: ${path.relative(process.cwd(), outputPath)}（${paths.length} 个 tab 页面）`)
}

main()
