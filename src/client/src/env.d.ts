/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

// uni-app 约定的无扩展名 App 引入（main.ts: import App from './App'）
declare module '*/App' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, never>, Record<string, never>, any>
  export default component
}

declare module 'uview-plus' {
  const uview: any
  export default uview
}

// qrcode-generator 本地库（src/pagesOrder/order/qrcode.min.js，无官方类型；置于分包目录避免打进主包）
declare module '*.min.js' {
  interface QrCodeInstance {
    addData(data: string, mode?: string): void
    make(): void
    createDataURL(cellSize?: number, margin?: number): string
    createSvgTag(cellSize?: number, margin?: number): string
  }
  function qrcode(typeNumber: number, errorCorrectionLevel: string): QrCodeInstance
  export default qrcode
}

// uni-app 页面全局注入的打开方事件通道（Vue3 setup 内可裸调，@dcloudio/types 未收录）
declare function getOpenerEventChannel(): {
  on(event: string, callback: (...args: any[]) => void): void
  emit(event: string, ...args: unknown[]): void
}

// 支付宝小程序运行时全局对象（uni 未映射 ext 配置 API，需直调；仅 MP-ALIPAY 运行时存在）
declare const my: {
  /** 读取模板小程序 ext.json 的 ext 字段（模板实例化时经 alipay.open.mini.version.upload 注入） */
  getExtConfigSync?: () => Record<string, unknown>
}

// 构建期版本常量（vite.config.ts define 注入，与 scripts/gen-version.js 同一优先级）
/** 应用版本号：剔除 v 前缀与预发布后缀（如 0.1.0） */
declare const __APP_VERSION__: string
/** 应用版本编号：git tag 数量（App versionCode 与统计口径） */
declare const __APP_VERSION_CODE__: number
/** git 原始版本串：含预发布后缀（如 v0.1.0-beta），仅关于页展示 */
declare const __APP_VERSION_RAW__: string
/** 构建时间（ISO 8601） */
declare const __APP_BUILD_TIME__: string