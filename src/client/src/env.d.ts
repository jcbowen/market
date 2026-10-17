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

// qrcode-generator 本地静态库（src/static/qrcode.min.js，无官方类型）
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