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
 * 拉取全量区划（管理端树形页用）
 * 省市区县三级约 3400 条，单页拉不满时分页补齐（status=0 不过滤状态，状态由前端本地筛）；
 * 拉全量后前端本地建树/搜索，避免筛选时反复请求
 */
export async function getAllRegions(): Promise<Region[]> {
  const pageSize = 1000
  const first = await getRegionList({ page: 1, page_size: pageSize, status: 0 })
  const data = first.data.data
  const all: Region[] = [...data.list]
  const pages = Math.ceil(data.total / pageSize)
  for (let p = 2; p <= pages; p++) {
    const res = await getRegionList({ page: p, page_size: pageSize, status: 0 })
    all.push(...res.data.data.list)
  }
  return all
}

/**
 * 创建区划更新检查任务（存在进行中任务时幂等返回该任务）
 */
export function createRegionUpdateTask() {
  return request<ApiResult<RegionUpdateTask>>({
    url: '/api/v1/admin/regions/update/check',
    method: 'POST',
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
