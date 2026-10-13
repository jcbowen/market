import { listRegionChildren } from '@/api/user'
import type { Region } from '@/api/user'

/**
 * 行政区划数据缓存
 * - 省级列表：持久化到本地存储（7 天过期），避免每次进入地址编辑页都重新请求
 * - 市/区县列表：模块级内存缓存，跨页面实例复用（页面关闭不丢，App 会话内有效）
 * - 区划数据变更频率极低，过期自动重拉即为重刷能力，无需手动刷新入口
 */

/** 省级列表本地存储 key */
const PROVINCE_CACHE_KEY = 'region_provinces_cache'
/** 省级缓存有效期：7 天 */
const PROVINCE_CACHE_TTL = 7 * 24 * 60 * 60 * 1000

/** 市级缓存：父级区划代码 → 市列表 */
const cityCache = new Map<string, Region[]>()
/** 区县缓存：父级区划代码 → 区县列表 */
const districtCache = new Map<string, Region[]>()

/**
 * 读取本地存储中的省级列表缓存
 * @returns 过期或解析失败返回 null，由调用方重新请求
 */
function readProvinceCache(): Region[] | null {
  try {
    const raw = uni.getStorageSync(PROVINCE_CACHE_KEY)
    if (!raw) return null
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
    if (!Array.isArray(parsed?.list)) return null
    if (Date.now() - (Number(parsed.time) || 0) >= PROVINCE_CACHE_TTL) return null
    return parsed.list as Region[]
  } catch {
    return null
  }
}

/**
 * 将省级列表写入本地存储（写入失败静默降级，仅本次会话内使用内存数据）
 * @param list 省级区划列表
 */
function writeProvinceCache(list: Region[]): void {
  try {
    uni.setStorageSync(PROVINCE_CACHE_KEY, JSON.stringify({ time: Date.now(), list }))
  } catch {
    // 静默降级：存储不可用时不影响正常流程
  }
}

/**
 * 获取省级行政区划列表（带 7 天本地持久化缓存，过期自动重拉）
 * @returns 省级区划列表
 * @throws 请求失败且无可用缓存时抛出上层错误
 */
export async function loadProvinces(): Promise<Region[]> {
  const cached = readProvinceCache()
  if (cached) return cached

  const res = await listRegionChildren('0')
  const list = res.data || []
  if (list.length) writeProvinceCache(list)
  return list
}

/**
 * 获取市级行政区划列表（模块级内存缓存，跨页面实例复用）
 * @param code 省级区划代码
 * @returns 市级区划列表（直辖市省下直挂区县，本函数仍返回该列表，由调用方按 level 判断）
 * @throws 请求失败时抛出上层错误
 */
export async function loadCities(code: string): Promise<Region[]> {
  if (!cityCache.has(code)) {
    const res = await listRegionChildren(code)
    cityCache.set(code, res.data || [])
  }
  return cityCache.get(code)!
}

/**
 * 获取区县级行政区划列表（模块级内存缓存，跨页面实例复用）
 * @param code 市级（或直辖市省级）区划代码
 * @returns 区县级区划列表
 * @throws 请求失败时抛出上层错误
 */
export async function loadDistricts(code: string): Promise<Region[]> {
  if (!districtCache.has(code)) {
    const res = await listRegionChildren(code)
    districtCache.set(code, res.data || [])
  }
  return districtCache.get(code)!
}
