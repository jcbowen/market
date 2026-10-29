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

/**
 * 解码单个 query 参数值
 * 小程序端 onLoad 的 query 不保证自动解码（H5 路由已解码一次），此处统一做防御性解码：
 * - 值不含 % 时原样返回，H5 端重复调用幂等无害
 * - 含非法转义序列时保持原值，由调用方/类型转换兜底
 */
export function decodeQueryValue(value: unknown): string {
  const raw = value === undefined || value === null ? '' : String(value)
  if (!raw.includes('%')) return raw
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

/**
 * 从 onLoad query 中取数值参数
 * @param query onLoad 收到的 query 对象
 * @param key 参数名
 * @param fallback 参数缺失/非法时的回退值，默认 0
 */
export function queryNumber(
  query: Record<string, string | undefined> | undefined | null,
  key: string,
  fallback = 0,
): number {
  const raw = query?.[key]
  if (raw === undefined || raw === null || raw === '') return fallback
  const n = Number(decodeQueryValue(raw))
  return Number.isFinite(n) ? n : fallback
}

/**
 * 从 onLoad query 中取逗号分隔的 ID 列表参数（配合 buildQueryString 的数组拼接口径）
 * 解码后按逗号切分，仅保留正整数，异常片段直接丢弃
 */
export function queryIdList(
  query: Record<string, string | undefined> | undefined | null,
  key: string,
): number[] {
  const raw = query?.[key]
  if (!raw) return []
  return decodeQueryValue(raw)
    .split(',')
    .map(Number)
    .filter((n) => Number.isInteger(n) && n > 0)
}
