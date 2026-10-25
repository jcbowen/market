/**
 * 小程序 appid 参数化构建脚本（跨平台，Node 内置模块）
 *
 * 背景：manifest.json 的 mp-weixin.appid 编译期内置，交付源码若带卖方 appid
 * 等于送出卖方小程序身份。本脚本把 appid 改为构建时注入：
 * - 交付版 src/manifest.json 的 appid 为占位符（wx_your_appid_here）
 * - 构建/开发时必须通过 --appid 指定，或提供 src/manifest.local.json（gitignore，卖方本机专用）
 * - 缺参 / 格式不符一律报错退出，不允许静默使用卖方值
 *
 * 实现方式：manifest.json 含 uni-app 注释、不可整体 JSON.parse，故采用字符串替换
 * （appid 值替换 + 移除卖方 originalId 行），构建结束后恢复原内容（含异常/SIGINT 路径），
 * 保证 git 工作区不被弄脏；残留的 .bak 会在下次启动时自动恢复（防进程被 kill -9）。
 *
 * 用法：
 *   node scripts/build-mp.js --appid wx0123456789abcdef      # 构建（uni build -p mp-weixin）
 *   node scripts/build-mp.js --appid wx0123456789abcdef --dev  # 开发（uni -p mp-weixin，退出后恢复）
 *   node scripts/build-mp.js --dev                           # 卖方本机：读 manifest.local.json
 *
 * 配套（域名侧，见 12-授权体系/02-小程序发版方案.md 第 5 节）：
 *   - 交付版 .env.production 的 VITE_APP_API_BASE_URL 为占位符，客户填自己域名
 *   - 卖方本机用 .env.production.local（gitignore）覆盖，与本脚本的 manifest.local.json 对应
 */

const fs = require('fs')
const path = require('path')
const { spawnSync } = require('child_process')

const ROOT = path.resolve(__dirname, '..')
const MANIFEST_PATH = path.resolve(ROOT, 'src/manifest.json')
const MANIFEST_BAK_PATH = path.resolve(ROOT, 'src/.manifest.json.bak')
const MANIFEST_LOCAL_PATH = path.resolve(ROOT, 'src/manifest.local.json')
const UNI_BIN = path.resolve(ROOT, 'node_modules/@dcloudio/vite-plugin-uni/bin/uni.js')

/** 交付版占位符 appid（卖方真实 appid 不进仓库） */
const PLACEHOLDER_APPID = 'wx_your_appid_here'
/** 微信小程序 appid 格式：wx + 16 位小写十六进制 */
const APPID_PATTERN = /^wx[0-9a-f]{16}$/

function fail(message) {
  console.error(`[build-mp] 错误: ${message}`)
  process.exit(1)
}

/**
 * 解析命令行参数
 * @returns {{ appid: string|null, dev: boolean }}
 */
function parseArgs() {
  const args = process.argv.slice(2)
  const result = { appid: null, dev: false }
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--appid') {
      result.appid = args[++i] || null
    } else if (args[i] === '--dev') {
      result.dev = true
    } else if (args[i] === '--help' || args[i] === '-h') {
      printHelp()
      process.exit(0)
    } else {
      fail(`未知参数: ${args[i]}（可用 --appid <id> / --dev / --help）`)
    }
  }
  return result
}

function printHelp() {
  console.log(`小程序 appid 参数化构建脚本

用法:
  node scripts/build-mp.js --appid <appid> [--dev]

参数:
  --appid <appid>  小程序 appid（wx + 16 位小写十六进制，mp.weixin.qq.com → 开发设置查看）
  --dev            开发模式（uni -p mp-weixin），默认为构建模式（uni build -p mp-weixin）
  --help, -h       显示本帮助

appid 缺省时读取 src/manifest.local.json（gitignore，卖方本机专用），
该文件也不存在时报错退出——交付版不允许静默使用卖方 appid。`)
}

/**
 * 解析目标 appid：--appid 参数 > manifest.local.json（卖方本机）> 报错
 * @param {{ appid: string|null }} options
 * @returns {string}
 */
function resolveAppid(options) {
  if (options.appid) return options.appid

  if (fs.existsSync(MANIFEST_LOCAL_PATH)) {
    try {
      const local = JSON.parse(fs.readFileSync(MANIFEST_LOCAL_PATH, 'utf8'))
      const appid = local['mp-weixin'] && local['mp-weixin'].appid
      if (appid) {
        console.log(`[build-mp] 使用 manifest.local.json 中的 appid: ${appid}`)
        return appid
      }
      fail('manifest.local.json 中未找到 mp-weixin.appid 配置')
    } catch (e) {
      fail(`manifest.local.json 解析失败: ${e.message}`)
    }
  }

  fail(
    '缺少小程序 appid。请通过 --appid 指定，或创建 src/manifest.local.json（gitignore，卖方本机专用）。\n' +
      '      交付/客户环境示例: node scripts/build-mp.js --appid wx0123456789abcdef\n' +
      '      （客户 appid 请登录 mp.weixin.qq.com → 开发设置查看）'
  )
}

/** 去掉 JSON 注释（uni-app manifest 允许 块注释与行注释），仅供结构校验 */
function stripJsonComments(raw) {
  return raw
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/**
 * 定位 mp-weixin 节的字符串区间（注释感知的花括号配对）
 * 注意：manifest.json 顶部还有一处 uni-app 应用 appid（__UNI__ 前缀），
 * 全局正则替换会误中它——必须限定在 mp-weixin 节内操作。
 * @param {string} raw manifest 原文
 * @returns {{ start: number, end: number }} 节起始（含 "mp-weixin" 键）与结束（节后一位）
 */
function locateMpWeixinSection(raw) {
  const keyIndex = raw.indexOf('"mp-weixin"')
  if (keyIndex === -1) fail('manifest.json 中未找到 mp-weixin 配置节')
  const braceStart = raw.indexOf('{', keyIndex)
  if (braceStart === -1) fail('manifest.json 的 mp-weixin 节格式异常（未找到 {）')
  let depth = 0
  let inBlockComment = false
  let inLineComment = false
  for (let i = braceStart; i < raw.length; i++) {
    const ch = raw[i]
    const next = raw[i + 1]
    if (inLineComment) {
      if (ch === '\n') inLineComment = false
      continue
    }
    if (inBlockComment) {
      if (ch === '*' && next === '/') {
        inBlockComment = false
        i++
      }
      continue
    }
    if (ch === '/' && next === '*') {
      inBlockComment = true
      i++
      continue
    }
    if (ch === '/' && next === '/') {
      inLineComment = true
      i++
      continue
    }
    if (ch === '{') {
      depth++
    } else if (ch === '}') {
      depth--
      if (depth === 0) return { start: keyIndex, end: i + 1 }
    }
  }
  return fail('manifest.json 的 mp-weixin 节花括号不配对')
}

/** 提取 manifest 当前 mp-weixin appid */
function currentAppid(raw) {
  const { start, end } = locateMpWeixinSection(raw)
  const match = raw.slice(start, end).match(/"appid"\s*:\s*"([^"]*)"/)
  return match ? match[1] : null
}

/**
 * 在 mp-weixin 节内字符串替换 appid（保留原格式与注释），并移除卖方 originalId 行
 * @param {string} raw manifest 原文
 * @param {string} appid 目标 appid
 * @returns {string} 替换后的内容
 */
function patchManifest(raw, appid) {
  const { start, end } = locateMpWeixinSection(raw)
  let section = raw.slice(start, end)
  if (!/"appid"\s*:\s*"[^"]*"/.test(section)) {
    fail('manifest.json 的 mp-weixin 节中未找到 appid 配置')
  }
  section = section.replace(/("appid"\s*:\s*")[^"]*(")/, `$1${appid}$2`)
  // originalId 是卖方公众号原始 ID，不属于交付内容；整行删除（appid 为节内首个键，删后结构仍合法）
  section = section.replace(/^[ \t]*"originalId"\s*:\s*"[^"]*",?[ \t]*\r?\n/m, '')
  return raw.slice(0, start) + section + raw.slice(end)
}

/** 结构校验：注释剥离后必须可 parse 且 mp-weixin.appid 已生效 */
function validatePatched(patched, appid) {
  let parsed
  try {
    parsed = JSON.parse(stripJsonComments(patched))
  } catch (e) {
    fail(`改写后的 manifest.json 结构校验失败: ${e.message}`)
  }
  if (!parsed['mp-weixin'] || parsed['mp-weixin'].appid !== appid) {
    fail('改写后的 manifest.json 中 mp-weixin.appid 未生效')
  }
}

/** 恢复上次异常退出残留的 manifest（进程被 kill -9 时 .bak 会留下） */
function recoverStaleBackup() {
  if (!fs.existsSync(MANIFEST_BAK_PATH)) return
  try {
    fs.writeFileSync(MANIFEST_PATH, fs.readFileSync(MANIFEST_BAK_PATH, 'utf8'), 'utf8')
    fs.unlinkSync(MANIFEST_BAK_PATH)
    console.log('[build-mp] 检测到上次异常退出的残留备份，已恢复 manifest.json')
  } catch (e) {
    fail(`残留备份恢复失败，请手动检查 src/manifest.json 与 src/.manifest.json.bak: ${e.message}`)
  }
}

/**
 * 执行 Node 脚本（同步，stdio 继承）
 * @param {string} scriptPath 脚本绝对路径
 */
function runNodeScript(scriptPath) {
  const result = spawnSync(process.execPath, [scriptPath], { stdio: 'inherit', cwd: ROOT })
  if (result.status !== 0) fail(`脚本执行失败: ${path.relative(ROOT, scriptPath)}`)
}

/**
 * 执行 uni CLI（build 或 dev）
 * @param {boolean} dev true=开发模式
 * @returns {number} 退出码
 */
function runUni(dev) {
  const uniArgs = dev ? ['-p', 'mp-weixin'] : ['build', '-p', 'mp-weixin']
  console.log(`[build-mp] 执行 uni ${uniArgs.join(' ')} ...`)
  const result = spawnSync(process.execPath, [UNI_BIN, ...uniArgs], { stdio: 'inherit', cwd: ROOT })
  return result.status === null ? 1 : result.status
}

function main() {
  if (!fs.existsSync(UNI_BIN)) {
    fail(`未找到 uni CLI: ${path.relative(ROOT, UNI_BIN)}，请先执行 npm install`)
  }

  const options = parseArgs()
  const appid = resolveAppid(options)

  if (!APPID_PATTERN.test(appid)) {
    fail(`appid 格式不正确: ${appid}（应为 wx + 16 位小写十六进制，如 wxdb854c7f606605f5）`)
  }

  recoverStaleBackup()
  const original = fs.readFileSync(MANIFEST_PATH, 'utf8')
  const current = currentAppid(original)

  let patched = null
  if (current === appid) {
    // 幂等：manifest 已是目标 appid，无需改写
    console.log(`[build-mp] manifest.json appid 已为 ${appid}，跳过改写`)
  } else {
    if (current === PLACEHOLDER_APPID) {
      console.log('[build-mp] 检测到交付版占位符 appid，替换为目标 appid')
    } else if (current) {
      console.log(`[build-mp] manifest.json 当前 appid 为 ${current}，替换为 ${appid}`)
    }
    patched = patchManifest(original, appid)
    validatePatched(patched, appid)
    fs.writeFileSync(MANIFEST_BAK_PATH, original, 'utf8')
    fs.writeFileSync(MANIFEST_PATH, patched, 'utf8')
    console.log(`[build-mp] 已将 manifest.json appid 置为 ${appid}（结束后自动恢复仓库版本）`)
  }

  // 保证退出时恢复 manifest（含 Ctrl+C / SIGTERM）
  let restored = false
  const restore = () => {
    if (restored) return
    restored = true
    if (fs.existsSync(MANIFEST_BAK_PATH)) {
      try {
        fs.writeFileSync(MANIFEST_PATH, fs.readFileSync(MANIFEST_BAK_PATH, 'utf8'), 'utf8')
        fs.unlinkSync(MANIFEST_BAK_PATH)
        console.log('[build-mp] manifest.json 已恢复为仓库版本')
      } catch (e) {
        console.error(`[build-mp] 警告: manifest.json 恢复失败（${e.message}），请检查 src/.manifest.json.bak`)
      }
    }
  }
  process.on('SIGINT', () => {
    restore()
    process.exit(130)
  })
  process.on('SIGTERM', () => {
    restore()
    process.exit(143)
  })

  try {
    // 与原 npm 脚本保持一致：先生成 tabBar 路径常量，再执行 uni
    runNodeScript(path.resolve(ROOT, 'scripts/gen-tab-bar-paths.js'))
    const exitCode = runUni(options.dev)
    restore()
    process.exit(exitCode)
  } catch (e) {
    restore()
    throw e
  }
}

main()
