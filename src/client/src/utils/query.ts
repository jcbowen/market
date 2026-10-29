/**
 * URL query 拼装工具
 * 小程序运行时（微信 iOS JSCore / 支付宝容器）没有 URLSearchParams 全局对象，
 * 直接使用会在点击事件中抛 ReferenceError，因此统一用本方法拼 query 串。
 */

/** 可参与拼 query 的值类型：undefined/null/空串会被跳过 */
export type QueryValue = string | number | undefined | null

/**
 * 把键值对象拼成 query 串（不含开头的 ?）
 * - undefined / null / 空串 / 空数组的键会被跳过
 * - key 与 value 均经 encodeURIComponent 编码
 * @param params 参数对象，如 { id: 1, keyword: '牛奶', ids: [1,2] }
 * @returns query 串，如 "id=1&keyword=%E7%89%9B%E5%A5%B6&ids=1%2C2"；无有效参数时返回空串
 * @example
 * buildQueryString({ id: 1, redirect: undefined }) // "id=1"
 * goPage(`/pages/order/confirm?${buildQueryString({ productId: 3 })}`)
 */
export function buildQueryString(
  params: Record<string, QueryValue | QueryValue[]>,
): string {
  const parts: string[] = []
  Object.entries(params).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') return
    if (Array.isArray(value)) {
      if (!value.length) return
      parts.push(`${encodeURIComponent(key)}=${value.map(v => encodeURIComponent(String(v))).join(',')}`)
      return
    }
    parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
  })
  return parts.join('&')
}

/**
 * 在路径后拼 query 串，无有效参数时返回原路径
 * @param path 页面路径，如 /pages/order/confirm
 * @param params 参数对象，见 buildQueryString
 * @returns 带参路径，如 /pages/order/confirm?productId=1
 */
export function withQuery(path: string, params: Record<string, QueryValue | QueryValue[]>): string {
  const qs = buildQueryString(params)
  return qs ? `${path}?${qs}` : path
}
