import { request } from './request'
import type { ApiResult, PageParams, PageResult } from './types'

/**
 * 行政区划相关 API
 * 公开接口供区划选择器懒加载与 IP 定位；管理接口供区划管理页
 */

/** 行政区划条目 */
export interface Region {
  code: string
  name: string
  parent_code: string
  /** 层级：1 省 2 市 3 区县 */
  level: number
  /** 状态：1 正常 2 停用 */
  status: number
}

/** 区划选择器选中路径（省/市/区） */
export interface RegionSelection {
  province: Region | null
  city: Region | null
  district: Region | null
}

/** IP 定位反查区划结果 */
export interface RegionLocateResult {
  longitude: number
  latitude: number
  province: Region | null
  city: Region | null
}

/** 区划更新差异条目 */
export interface RegionDiffItem {
  code: string
  /** 库内当前名称（新增场景为上游名称） */
  name: string
  /** 更名后的新名称 */
  new_name?: string
  parent_code: string
  level: number
}

/** 区划更新任务 */
export interface RegionUpdateTask {
  id: number
  /** 1 待执行 2 拉取中 3 比对中 4 完成 5 失败 */
  status: number
  source: string
  total_remote: number
  total_local: number
  added_count: number
  updated_count: number
  removed_count: number
  added: RegionDiffItem[] | null
  updated: RegionDiffItem[] | null
  removed: RegionDiffItem[] | null
  error: string
  applied: boolean
  created_at: string
  finished_at: string
}

/**
 * 查询下级区划（公开接口，供选择器懒加载）
 * @param parentCode 父级区划代码，'0' 表示省级
 */
export function listRegionChildren(parentCode: string) {
  return request<ApiResult<Region[]>>({
    url: '/api/v1/regions/children',
    method: 'GET',
    params: { parent_code: parentCode },
  })
}

/**
 * 按出口 IP 定位反查行政区划（公开接口）
 * 城市级精度：命中市级时同时返回上级省份
 */
export function locateRegion() {
  return request<ApiResult<RegionLocateResult | null>>({
    url: '/api/v1/regions/locate',
    method: 'GET',
  })
}

/**
 * 管理端分页查询区划列表
 * @param params 筛选与分页参数
 */
export function getRegionList(
  params: PageParams & { keyword?: string; parent_code?: string; level?: number; status?: number }
) {
  return request<ApiResult<PageResult<Region>>>({
    url: '/api/v1/admin/regions',
    method: 'GET',
    params,
  })
}

/**
 * 分页拉取全量区划（底层实现，直读数据库无缓存）
 * 省市区县三级约 3400 条，分页补齐（status=0 不过滤状态，状态由前端本地筛）
 *
 * 注意：网关 parsePageParams 会把 page_size 钳制到 maxPageSize=100，
 * 传更大的值也会被压回 100——因此这里固定按 100 请求，
 * 并以首页实际返回条数作为有效页大小计算总页数，并行拉取剩余页，
 * 直至累计条数覆盖 total（此前按 1000 估算只拉到 4 页 ~400 条，数据缺失）
 */
async function fetchAllRegions(): Promise<Region[]> {
  const requestSize = 100
  const first = await getRegionList({ page: 1, page_size: requestSize, status: 0 })
  const data = first.data.data
  const all: Region[] = [...data.list]
  const pageSizeEff = all.length > 0 ? all.length : requestSize
  const pages = Math.ceil(data.total / pageSizeEff)
  if (pages > 1) {
    const rest = await Promise.all(
      Array.from({ length: pages - 1 }, (_, i) =>
        getRegionList({ page: i + 2, page_size: requestSize, status: 0 })
      )
    )
    for (const res of rest) all.push(...res.data.data.list)
  }
  return all
}

/** 全量区划 localStorage 缓存 key（仅供 RegionPicker 等选择器场景使用） */
const REGION_ALL_CACHE_KEY = 'admin_region_all_cache'
/** 缓存有效期：7 天（区划数据低频变更，仅作兜底重刷） */
const REGION_ALL_CACHE_TTL = 7 * 24 * 60 * 60 * 1000

/** 模块级内存缓存（同一会话内复用，避免重复读 localStorage 与 JSON 解析） */
let regionAllCache: Region[] | null = null

/**
 * localStorage 是否可用
 * 项目当前为纯客户端 SPA，浏览器环境必然存在；
 * 此判断用于兼容未来可能的 SSR 迁移或 Node 环境单测，避免 ReferenceError
 */
function hasLocalStorage(): boolean {
  return typeof localStorage !== 'undefined'
}

/** 读取 localStorage 中的持久化缓存，无缓存或解析失败返回 null */
function readRegionAllPersisted(): { list: Region[]; time: number } | null {
  if (!hasLocalStorage()) return null
  try {
    const raw = localStorage.getItem(REGION_ALL_CACHE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed?.list)) return null
    return { list: parsed.list as Region[], time: Number(parsed.time) || 0 }
  } catch {
    return null
  }
}

/** 将全量区划写入 localStorage（写入失败如隐私模式/容量不足时静默降级为无持久化） */
function persistRegionAll(list: Region[]): void {
  if (!hasLocalStorage()) return
  try {
    localStorage.setItem(REGION_ALL_CACHE_KEY, JSON.stringify({ time: Date.now(), list }))
  } catch {
    // 静默降级：仅本次会话内使用内存缓存
  }
}

/**
 * 清除全量区划缓存（内存 + localStorage）
 * 仅影响选择器（RegionPicker）的缓存数据；行政区划管理页不使用缓存，无需清理。
 * 区划更新任务覆盖入库后应调用，避免选择器继续展示旧数据
 */
export function clearAllRegionsCache(): void {
  regionAllCache = null
  if (hasLocalStorage()) localStorage.removeItem(REGION_ALL_CACHE_KEY)
}

/**
 * 拉取全量区划（带缓存，RegionPicker 等选择器场景用）
 * 读取顺序：模块级内存缓存 → localStorage（7 天内有效）→ 接口分页拉全量并回写缓存；
 * 缓存未过期时不再发请求，跨页面挂载复用
 */
export async function getAllRegions(): Promise<Region[]> {
  if (regionAllCache) return regionAllCache

  const persisted = readRegionAllPersisted()
  if (persisted && Date.now() - persisted.time < REGION_ALL_CACHE_TTL) {
    regionAllCache = persisted.list
    return regionAllCache
  }

  const list = await fetchAllRegions()
  regionAllCache = list
  persistRegionAll(list)
  return list
}

/**
 * 拉取全量区划（直读数据库，行政区划管理页专用）
 * 管理页必须始终展示库内真实数据——此前带缓存曾导致导入最新数据后
 * 页面仍展示旧快照（如搜不到新增的重庆数据）；全量请求仅 ~300KB，可接受
 */
export async function getAllRegionsFresh(): Promise<Region[]> {
  return fetchAllRegions()
}

/**
 * 创建区划更新检查任务（存在进行中任务时幂等返回该任务）
 * @param sourceType 数据源类型：default 常规多源镜像（默认）/ official 民政部官方源（兜底修正，较慢）
 */
export function createRegionUpdateTask(sourceType: 'default' | 'official' = 'default') {
  return request<ApiResult<RegionUpdateTask>>({
    url: '/api/v1/admin/regions/update/check',
    method: 'POST',
    params: { source_type: sourceType },
  })
}

/**
 * 查询区划更新任务详情（含差异明细，轮询用）
 * @param id 任务 ID
 */
export function getRegionUpdateTask(id: number) {
  return request<ApiResult<RegionUpdateTask>>({
    url: `/api/v1/admin/regions/update/tasks/${id}`,
    method: 'GET',
  })
}

/**
 * 将已完成任务的差异覆盖入库
 * @param id 任务 ID
 */
export function applyRegionUpdateTask(id: number) {
  return request<ApiResult<{ id: number }>>({
    url: `/api/v1/admin/regions/update/tasks/${id}/apply`,
    method: 'POST',
  })
}

/**
 * 按省/市/区名称逐级匹配区划代码路径（存量数据回显用）
 * 名称匹配不到时对应层级返回 null，后续层级终止
 * @param province 省名
 * @param city 市名
 * @param district 区名
 */
export async function matchRegionPathByNames(
  province: string,
  city: string,
  district: string
): Promise<RegionSelection> {
  const empty: RegionSelection = { province: null, city: null, district: null }
  if (!province) return empty

  const provinces = (await listRegionChildren('0')).data.data || []
  const prov = provinces.find((r) => r.name === province) || null
  if (!prov) return empty

  empty.province = prov
  if (!city && !district) return empty

  const cities = (await listRegionChildren(prov.code)).data.data || []
  const c = cities.find((r) => r.name === city && r.level === 2) || null
  if (!c) {
    // 直辖市或存量市级名无法匹配（如市级名与省名相同、"市辖区"）：
    // 区县名能直接命中省下子级时，按省→区县两级路径返回（city 为 null）
    if (district) {
      const d = cities.find((r) => r.name === district && r.level === 3) || null
      if (d) empty.district = d
    }
    return empty
  }

  empty.city = c
  if (!district) return empty

  const districts = (await listRegionChildren(c.code)).data.data || []
  const d = districts.find((r) => r.name === district) || null
  if (d) empty.district = d
  return empty
}
