/**
 * 页面数据通用缓存（storage 持久化 + TTL 过期）
 * 服务于「缓存先渲染 + 请求异步刷新」（stale-while-revalidate）模式：
 * 页面加载时先读缓存立即渲染（含已过期数据，供弱网兜底），再发网络请求，
 * 成功后写缓存并覆盖视图；请求失败时旧数据仍留在页面，避免回访用户白屏。
 * 适用：读多变低频数据（分类、Banner、商品列表、社区信息等）
 */

/** 缓存读取结果 */
export interface PageCacheEntry<T> {
  data: T
  /** true=TTL 内的新鲜数据；false=已过期数据（仅作兜底展示，调用方需仍发起刷新请求） */
  fresh: boolean
}

/** 缓存信封结构：time 为写入时间戳，用于 TTL 判定 */
interface CacheEnvelope<T> {
  time: number
  data: T
}

/**
 * 读取页面数据缓存
 * @param key 缓存 key（建议带社区 ID 等作用域后缀做数据隔离）
 * @param ttl 有效期（毫秒）
 * @returns 未存储或解析失败返回 null；过期数据返回 fresh=false 供调用方兜底判断
 */
export function readPageCache<T>(key: string, ttl: number): PageCacheEntry<T> | null {
  try {
    const raw = uni.getStorageSync(key)
    if (!raw) return null
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
    const envelope = parsed as Partial<CacheEnvelope<T>> | null
    if (!envelope || typeof envelope.time !== 'number' || envelope.data === undefined) return null
    return { data: envelope.data as T, fresh: Date.now() - envelope.time < ttl }
  } catch {
    return null
  }
}

/**
 * 写入页面数据缓存
 * 存储不可用时静默降级，不影响主流程
 * @param key 缓存 key
 * @param data 待缓存数据
 */
export function writePageCache<T>(key: string, data: T): void {
  try {
    uni.setStorageSync(key, JSON.stringify({ time: Date.now(), data } satisfies CacheEnvelope<T>))
  } catch {
    // 静默降级：缓存写失败仅影响下次秒开，不阻断当前流程
  }
}

/**
 * 移除页面数据缓存（如社区切换后清理旧作用域数据）
 * @param key 缓存 key
 */
export function removePageCache(key: string): void {
  try {
    uni.removeStorageSync(key)
  } catch {
    // ignore
  }
}

/**
 * 使用示例
 * ```ts
 * const CACHE_TTL = 10 * 60 * 1000
 * const key = `page_cache_categories_${communityId}`
 * // 1. 缓存先渲染（过期数据也展示，仅作兜底）
 * const cached = readPageCache<Category[]>(key, CACHE_TTL)
 * if (cached) categories.value = cached.data
 * // 2. 无论如何发请求刷新，成功后写缓存并覆盖
 * const res = await getCategories()
 * categories.value = res.data || []
 * writePageCache(key, categories.value)
 * ```
 */
