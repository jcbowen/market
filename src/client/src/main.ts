import App from './App'

// #ifndef VUE3
import Vue from 'vue'
import './uni.promisify.adaptor'
Vue.config.productionTip = false
App.mpType = 'app'
const app = new Vue({
	...App
})
app.$mount()
// #endif

// #ifdef VUE3
import { createSSRApp } from 'vue'
import { createPinia } from 'pinia'
import uviewPlus from 'uview-plus'

/**
 * 创建并返回 uni-app 应用实例
 * 配置 Pinia 状态管理、uview-plus 组件库，支持 VUE2/VUE3 条件编译
 */
export function createApp() {
	const app = createSSRApp(App)
	app.use(createPinia())
	app.use(uviewPlus)
	return { app }
}
// #endif
