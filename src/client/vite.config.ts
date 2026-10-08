import { defineConfig } from 'vite'
import uni from '@dcloudio/vite-plugin-uni'

// https://vitejs.dev/config/
export default defineConfig({
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
