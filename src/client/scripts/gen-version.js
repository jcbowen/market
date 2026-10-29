/**
 * C 端版本号同步脚本（跨平台，仅 Node 内置模块）
 *
 * 构建时从 git 读取版本号，同步改写 src/manifest.json 顶层的
 * versionName / versionCode，供 App 云打包（HBuilderX 读 manifest）与
 * 后续 miniprogram-ci 上传使用；改写结果随代码提交，不做临时恢复
 * （与 appid 的"临时改写-恢复"策略不同：版本号变更本就是应入库的内容）。
 *
 * 版本来源优先级（与后端 build.mjs 一致）：
 *   APP_VERSION 环境变量（CI tag 发布时注入 GITHUB_REF_NAME / 手动指定）
 *   → git describe --tags --abbrev=0（本地最近可达 tag）
 *   → 兜底 dev（无 git / 无 tag 的环境）
 *
 * 格式收敛（各平台约束，见 docs/需求文档/12-授权体系/02-小程序发版方案.md）：
 *   - versionName：剔除 v 前缀与 - 预发布后缀（v0.1.0-beta → 0.1.0）。
 *     iOS App Store 强制 x.y.z 三段纯数字，Android 国内市场审核兼容性参差，统一收敛
 *   - versionCode：git tag 数量（整数递增，App versionCode 与统计口径）
 *   - 完整原始版本串经 vite define 注入运行时，仅在关于页展示
 *
 * 运行时版本号不依赖本脚本：vite.config.ts 的 define 独立解析同一优先级
 * （两处逻辑需保持同步，未共享模块是为规避 vite config 加载 CJS 的格式坑）。
 *
 * 用法：
 *   node scripts/gen-version.js            # 同步 manifest.json 并打印版本信息
 *   node scripts/gen-version.js --check    # 仅打印版本信息，不改写 manifest
 */

const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const ROOT = path.resolve(__dirname, '..')
const MANIFEST_PATH = path.resolve(ROOT, 'src/manifest.json')

function fail(message) {
  console.error(`[gen-version] 错误: ${message}`)
  process.exit(1)
}

/**
 * 执行 git 命令，失败返回 null（无 git / 无 tag 的环境静默兜底，不阻断构建）
 * @param {string[]} args git 参数列表
 * @returns {string|null}
 */
function execGit(args) {
  try {
    return execFileSync('git', args, { cwd: ROOT, encoding: 'utf8' }).trim()
  } catch {
    return null
  }
}

/**
 * 解析原始版本串：APP_VERSION 环境变量 > git describe 最近可达 tag > 兜底 dev
 * @returns {string} 如 v0.1.0-beta / v0.1.3 / dev
 */
function resolveRawVersion() {
  if (process.env.APP_VERSION) return process.env.APP_VERSION
  const tag = execGit(['describe', '--tags', '--abbrev=0'])
  return tag || 'dev'
}

/**
 * 由原始版本串生成 versionName：剔除 v 前缀与 - 预发布后缀
 * @param {string} raw 原始版本串（如 v0.1.0-beta）
 * @returns {string} 如 0.1.0；收敛后不符合 x.y.z 数字段时兜底 dev
 */
function versionNameFromRaw(raw) {
  const name = raw.replace(/^v/i, '').split('-')[0]
  return /^\d+(\.\d+)*$/.test(name) ? name : 'dev'
}

/**
 * 由 git tag 数量生成 versionCode：每打一个 tag 递增 1
 * @returns {number} tag 数量；无 git 环境返回 0
 */
function versionCodeFromTags() {
  const tags = execGit(['tag', '-l'])
  return tags ? tags.split(/\r?\n/).filter(Boolean).length : 0
}

/**
 * 同步改写 manifest.json 顶层的 versionName / versionCode（幂等：已是目标值则跳过）
 * manifest 含 uni-app 注释不可整体 JSON.parse，采用字段级字符串替换
 * @param {string} versionName 目标版本号（如 0.1.0）
 * @param {number} versionCode 目标版本码（tag 数量）
 */
function syncManifest(versionName, versionCode) {
  if (!fs.existsSync(MANIFEST_PATH)) {
    fail(`未找到 manifest.json: ${path.relative(ROOT, MANIFEST_PATH)}`)
  }
  const raw = fs.readFileSync(MANIFEST_PATH, 'utf8')
  const vnRe = /("versionName"\s*:\s*")[^"]*(")/
  const vcRe = /("versionCode"\s*:\s*")[^"]*(")/
  if (!vnRe.test(raw) || !vcRe.test(raw)) {
    fail('manifest.json 顶层未找到 versionName / versionCode 字段')
  }
  const patched = raw.replace(vnRe, `$1${versionName}$2`).replace(vcRe, `$1${versionCode}$2`)
  if (patched === raw) {
    console.log(`[gen-version] manifest.json 已为 ${versionName} / ${versionCode}，跳过改写`)
    return
  }
  fs.writeFileSync(MANIFEST_PATH, patched, 'utf8')
  console.log(`[gen-version] 已同步 manifest.json: versionName=${versionName} versionCode=${versionCode}（变更随代码提交）`)
}

function main() {
  const checkOnly = process.argv.slice(2).includes('--check')
  const raw = resolveRawVersion()
  const versionName = versionNameFromRaw(raw)
  const versionCode = versionCodeFromTags()

  console.log(`[gen-version] 版本来源: ${process.env.APP_VERSION ? 'APP_VERSION 环境变量' : 'git tag'}`)
  console.log(`[gen-version] 原始版本: ${raw}`)
  console.log(`[gen-version] versionName: ${versionName} / versionCode: ${versionCode}`)
  if (!checkOnly) {
    syncManifest(versionName, versionCode)
  }
}

main()
