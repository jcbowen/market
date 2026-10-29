import { defineConfig } from 'vite'
import uni from '@dcloudio/vite-plugin-uni'
import { execSync } from 'node:child_process'

/**
 * 构建期版本号解析（与 scripts/gen-version.js、后端 build.mjs 同一优先级，
 * 未共享模块是为规避 vite config 加载 CJS 脚本的转译格式坑，两处需保持同步）：
 * APP_VERSION 环境变量（CI tag 时注入）→ git describe 最近可达 tag → 兜底 dev
 * @returns {string} 原始版本串（如 v0.1.0-beta）
 */
function resolveRawVersion(): string {
  if (process.env.APP_VERSION) return process.env.APP_VERSION
  try {
    return execSync('git describe --tags --abbrev=0', { encoding: 'utf8' }).trim()
  } catch {
    return 'dev'
  }
}

/**
 * 读取 git tag 数量作为版本码（App versionCode 与统计口径），无 git 环境返回 0
 * @returns {number}
 */
function resolveVersionCode(): number {
  try {
    const tags = execSync('git tag -l', { encoding: 'utf8' })
    return tags.split(/\r?\n/).filter(Boolean).length
  } catch {
    return 0
  }
}

// 版本号收敛：剔除 v 前缀与 - 预发布后缀（v0.1.0-beta → 0.1.0，iOS 强制 x.y.z）
const RAW_VERSION = resolveRawVersion()
const APP_VERSION = RAW_VERSION.replace(/^v/i, '').split('-')[0] || 'dev'
const APP_VERSION_CODE = resolveVersionCode()

// https://vitejs.dev/config/
export default defineConfig({
  define: {
    // 编译期版本常量：utils/version.ts 与关于页消费；RAW 串含预发布后缀仅作展示
    __APP_VERSION__: JSON.stringify(APP_VERSION),
    __APP_VERSION_CODE__: JSON.stringify(APP_VERSION_CODE),
    __APP_VERSION_RAW__: JSON.stringify(RAW_VERSION),
    __APP_BUILD_TIME__: JSON.stringify(new Date().toISOString()),
  },
  plugins: [uni()],
  css: {
    preprocessorOptions: {
      scss: {
        api: 'modern-compiler',
        silenceDeprecations: ['legacy-js-api', 'import'],
      },
    },
  },
  optimizeDeps: {
    include: ['uview-plus'],
  },
  server: {
    proxy: {
      '/api/v1': {
        target: 'http://localhost:18080',
        changeOrigin: true,
      },
    },
  },
})
