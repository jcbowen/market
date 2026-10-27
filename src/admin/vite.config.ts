import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// 配置文件所在目录：包已声明 type: module，配置按 ESM 加载，没有 __dirname
const rootDir = fileURLToPath(new URL('.', import.meta.url))

export default defineConfig({
  plugins: [vue()],
  base: '/b/',
  resolve: {
    alias: {
      '@': resolve(rootDir, 'src'),
    },
  },
  build: {
    outDir: resolve(rootDir, '../../bin/static/admin'),
    emptyOutDir: true,
    // 单 chunk 构建（~2.8 MB）是下方 codeSplitting:false 的直接结果，非异常，
    // 阈值抬到 3500 只是消除必然超标的体积警告；若未来恢复代码分包，应同步移除本项
    chunkSizeWarningLimit: 3500,
    // 开发阶段将动态路由组件内联到主 chunk，避免异步加载导致的组件渲染错乱
    // （vite 8 起 inlineDynamicImports 弃用，等价写法是 codeSplitting: false）
    rollupOptions: {
      output: {
        codeSplitting: false,
      },
    },
  },
  server: {
    port: 3001,
    proxy: {
      '/api': {
        target: 'http://localhost:18080',
        changeOrigin: true,
      },
    },
  },
})