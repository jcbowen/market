import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// 收银台前端单测配置：独立于 vite.config.ts（构建产物配置不含测试项，互不干扰）
// 用法：npm test（单次）/ npm run test:watch（增量开发）
const rootDir = fileURLToPath(new URL('.', import.meta.url))

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(rootDir, 'src'),
    },
  },
  test: {
    // happy-dom：组件挂载需要真实 DOM（window/document），又比 node 环境贴近浏览器
    environment: 'happy-dom',
    include: ['src/**/__tests__/**/*.spec.ts'],
    // 断言全部走显式 expect，无快照
    restoreMocks: true,
    // 关闭 IDE 集成 API 监听（vitest 5 默认会在用户目录写 secret token，无需该能力）
    api: false,
    // vmThreads 池：forks/threads 池的 worker 引导在受限环境会超时，vm 模式实测稳定
    pool: 'vmThreads',
  },
})
