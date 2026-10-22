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