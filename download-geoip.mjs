#!/usr/bin/env node
/**
 * GeoLite2-City IP 定位数据库下载/更新脚本
 *
 * 数据来源：https://geo.wp-statistics.com/#databases（每周二/五更新，jsDelivr CDN 直链，无需鉴权）
 * 数据库文件不入 git 仓库（.gitignore 已忽略 *.mmdb*），由本脚本按需下载到 bin/data/geoip/。
 * 网关按文件 mtime 热重载数据库，替换后无需重启。
 *
 * 用法：
 *   node download-geoip.mjs              # 数据库 mtime 距今 3 天内则跳过（配合 crontab 定期执行）
 *   node download-geoip.mjs --force      # 强制重新下载
 *   node download-geoip.mjs --url <...>  # 覆盖下载地址（默认 GeoLite2-City，可换 dbip-city-lite）
 *   node download-geoip.mjs --dest <路径> # 覆盖输出路径（默认 bin/data/geoip/GeoLite2-City.mmdb）
 *
 * 定期更新（服务器 crontab，每天凌晨 4 点执行）：
 *   0 4 * * * cd /path/to/market && node download-geoip.mjs >> logs/download-geoip.log 2>&1
 */
import { existsSync, mkdirSync, renameSync, statSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gunzipSync } from 'node:zlib'

const args = process.argv.slice(2)

function argValue(name) {
  const i = args.indexOf(`--${name}`)
  return i >= 0 && args[i + 1] ? args[i + 1] : undefined
}
const hasFlag = (name) => args.includes(`--${name}`)

const DEFAULT_URL = 'https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz'
// 数据库每周二/五发布；默认 3 天内视为新鲜，避免无意义的每日重复下载
const FRESH_DAYS = 3

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)))
const url = argValue('url') || DEFAULT_URL
const dest = resolve(rootDir, argValue('dest') || join('bin', 'data', 'geoip', 'GeoLite2-City.mmdb'))
const force = hasFlag('force')

if (!force && existsSync(dest)) {
  const ageDays = (Date.now() - statSync(dest).mtimeMs) / 86400000
  if (ageDays < FRESH_DAYS) {
    console.log(`[geoip] 数据库为最新（${ageDays.toFixed(1)} 天前更新），跳过下载：${dest}`)
    process.exit(0)
  }
  console.log(`[geoip] 数据库已 ${ageDays.toFixed(1)} 天未更新，开始下载...`)
}

mkdirSync(dirname(dest), { recursive: true })

// 1. 下载 .gz
console.log(`[geoip] 下载 ${url}`)
const res = await fetch(url)
if (!res.ok) {
  console.error(`[geoip] 下载失败：HTTP ${res.status} ${res.statusText}`)
  process.exit(1)
}
const buf = Buffer.from(await res.arrayBuffer())
if (buf.length === 0) {
  console.error('[geoip] 下载失败：响应内容为空')
  process.exit(1)
}

// 2. 解压并校验（mmdb 的元数据段固定以 \xab\xcd\xefMaxMind.com 标识结尾，位于文件末尾）
const mmdb = gunzipSync(buf)
const METADATA_MARKER = Buffer.from([0xab, 0xcd, 0xef, ...Buffer.from('MaxMind.com', 'ascii')])
const tail = mmdb.subarray(Math.max(0, mmdb.length - 131072))
if (mmdb.length < 1024 || tail.indexOf(METADATA_MARKER) < 0) {
  console.error('[geoip] 解压后的文件不是合法的 mmdb 格式，放弃更新')
  process.exit(1)
}

// 3. 原子替换：先写临时文件再 rename，中断不会破坏现有数据库（网关仍可用旧文件）
const tmp = `${dest}.tmp`
try {
  writeFileSync(tmp, mmdb)
  renameSync(tmp, dest)
} catch (e) {
  try { unlinkSync(tmp) } catch { /* ignore */ }
  console.error(`[geoip] 写入失败：${e.message}`)
  process.exit(1)
}

console.log(`[geoip] 更新完成：${dest}（${(statSync(dest).size / 1048576).toFixed(1)} MB）`)
