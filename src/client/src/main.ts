import App from './App'

// #ifndef VUE3
// Vue2 分支为条件编译保留（本项目固定 Vue3 构建）：vue3 类型无 Vue2 默认导出
// @ts-ignore vue2 运行时存在默认导出，vue3 类型声明不包含
import Vue from 'vue'
import './uni.promisify.adaptor'
Vue.config.productionTip = false
// @ts-ignore mpType 为 Vue2 专属挂载约定
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
