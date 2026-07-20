import { createApp } from 'vue'
import { createPinia } from 'pinia'
import naive from 'naive-ui'
import App from './App.vue'
import router from './router'

/**
 * 创建并挂载后台管理应用
 * 配置 Naive UI、Pinia、Vue Router
 */
const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(naive)
app.mount('#app')