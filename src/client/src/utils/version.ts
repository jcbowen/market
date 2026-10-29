/**
 * 运行时版本信息聚合
 * 版本号来自构建期注入（vite.config.ts define，与 scripts/gen-version.js 同一优先级），
 * 运行环境与版本环境按平台运行时判定，供请求 header（X-App-*）与关于页共用
 */

/** 运行环境：dev=本地运行（uni dev），release=发布版（uni build） */
export type AppEnv = 'dev' | 'release'

/** 版本环境：小程序开发版/体验版/正式版，gray 为支付宝灰度版 */
export type AppStage = 'develop' | 'trial' | 'release' | 'gray'

/** 应用版本号（剔除 v 前缀与预发布后缀，如 0.1.0） */
export const APP_VERSION: string = __APP_VERSION__

/** 应用版本码（git tag 数量，App versionCode 与统计口径） */
export const APP_VERSION_CODE: number = __APP_VERSION_CODE__

/** git 原始版本串（含预发布后缀，如 v0.1.0-beta），仅关于页展示 */
export const APP_VERSION_RAW: string = __APP_VERSION_RAW__

/** 构建时间（ISO 8601） */
export const APP_BUILD_TIME: string = __APP_BUILD_TIME__

/** 运行环境：按编译模式判定（HBuilderX「运行」=dev、「发行」=release，见 uni-app 运行环境文档） */
export const APP_ENV: AppEnv = import.meta.env.DEV ? 'dev' : 'release'

/**
 * 解析版本环境（仅小程序有此概念）
 * uni.getAccountInfoSync().miniProgram.envVersion 官方文档支持微信/支付宝小程序
 * （支付宝多 gray 灰度版，京东不支持；H5/App 无此概念走兜底 release）。
 * envVersion 经 String 中转：@dcloudio/types 的字面量联合未含 gray，直收窄比较会报 TS2367
 * @returns AppStage
 */
function resolveAppStage(): AppStage {
  // #ifdef MP-WEIXIN || MP-ALIPAY
  try {
    const env = String(uni.getAccountInfoSync()?.miniProgram?.envVersion || '')
    if (env === 'develop' || env === 'trial' || env === 'gray') return env
    return 'release'
  } catch {
    // 取不到账号信息（极端环境）按正式版兜底
    return 'release'
  }
  // #endif
  return 'release'
}

/** 版本环境：小程序按运行时判定（开发版/体验版/正式版），H5/App 恒为 release */
export const APP_STAGE: AppStage = resolveAppStage()

/** 运行环境中文文案（关于页展示） */
export const APP_ENV_LABEL: Record<AppEnv, string> = {
  dev: '本地开发',
  release: '发布版',
}

/** 版本环境中文文案（关于页展示） */
export const APP_STAGE_LABEL: Record<AppStage, string> = {
  develop: '开发版',
  trial: '体验版',
  release: '正式版',
  gray: '灰度版',
}

/**
 * 构建版本请求头（X-App-* 全集），request.ts 的请求与上传统一携带，
 * 后端可据此做版本统计、问题排查与灰度判断
 * @returns 头名到值的映射（值均为字符串，满足 uni.request header 类型）
 * @example header: { ...buildVersionHeaders() }
 */
export function buildVersionHeaders(): Record<string, string> {
  return {
    'X-App-Version': APP_VERSION,
    'X-App-Version-Code': String(APP_VERSION_CODE),
    'X-App-Env': APP_ENV,
    'X-App-Stage': APP_STAGE,
  }
}
