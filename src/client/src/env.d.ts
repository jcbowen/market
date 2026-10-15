/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
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