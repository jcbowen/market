/**
 * 用户相关 API
 * 包含用户信息、收货地址、社区切换等
 */

import { get, post, put, del, upload } from './request'
import type { PageData } from './request'
import type { UserInfo, Address, Community } from './types'

/**
 * 获取当前登录用户信息
 * @returns 用户信息
 */
export function getUserInfo() {
  return get<UserInfo>('/user/profile')
}

/**
 * 更新当前登录用户基础资料（头像/昵称/性别/生日）
 * 服务端口径：头像/昵称/生日非空才覆盖；性别仅传 1/2 时覆盖，传 0 不修改（C 端无法改回「未知」）
 * @param data 基础资料（birthday 为 YYYY-MM-DD，空串表示不修改；avatar 为附件 URL，非空才提交）
 */
export function updateUserProfile(data: { nickname?: string; avatar?: string; gender?: number; birthday?: string }) {
  return put<UserInfo>('/user/profile', data as Record<string, unknown>)
}

/**
 * 上传头像图片
 * @param filePath 本地临时文件路径（H5 为 blob URL，小程序为临时路径）
 * @returns 附件 ID 与可访问 URL（avatar 字段直接使用 url）
 */
export function uploadUserAvatar(filePath: string) {
  return upload<{ attach_id: number; url: string }>('/upload/image', filePath)
}

/**
 * 切换当前用户所属社区
 * @param communityId 社区 ID
 */
export function updateCommunity(communityId: number) {
  return put('/user/community', { community_id: communityId })
}

/**
 * 获取收货地址列表
 * @returns 地址数组
 */
export function getAddressList() {
  return get<Address[]>('/user/addresses')
}

/**
 * 创建收货地址
 * @param data 地址数据
 */
export function createAddress(data: Omit<Address, 'id'>) {
  return post<Address>('/user/addresses', data as Record<string, unknown>)
}

/**
 * 更新收货地址
 * @param id 地址 ID
 * @param data 地址数据
 */
export function updateAddress(id: number, data: Partial<Address>) {
  return put<Address>(`/user/addresses/${id}`, data as Record<string, unknown>)
}

/**
 * 删除收货地址
 * @param id 地址 ID
 */
export function deleteAddress(id: number) {
  return del(`/user/addresses/${id}`)
}

/**
 * 获取社区详情
 * @param id 社区 ID
 */
export function getCommunity(id: number) {
  return get<Community>(`/communities/${id}`)
}

/**
 * IP 归属地定位结果（服务端按本地 GeoLite2-City 数据库解析出口 IP，城市级精度）
 */
export interface IpLocation {
  longitude: number
  latitude: number
  country?: string
  country_name?: string
  city?: string
}

/** 行政区划条目（省/市/区县三级，GB/T 2260 口径） */
export interface Region {
  code: string
  name: string
  parent_code: string
  /** 层级：1 省 2 市 3 区县 */
  level: number
  /** 状态：1 正常 2 停用 */
  status: number
}

/** IP 定位反查行政区划结果（城市级精度，区县需用户补选） */
export interface RegionLocateResult {
  longitude: number
  latitude: number
  province: Region | null
  city: Region | null
}

/**
 * 查询下级区划（公开接口，供地区选择器逐级懒加载）
 * @param parentCode 父级区划代码，'0' 表示省级
 */
export function listRegionChildren(parentCode: string) {
  return get<Region[]>('/regions/children', { parent_code: parentCode })
}

/**
 * 按出口 IP 定位反查行政区划（公开接口，城市级精度）
 */
export function locateRegion() {
  return get<RegionLocateResult | null>('/regions/locate')
}

/**
 * 按出口 IP 定位大致经纬度（best-effort 兜底）
 * 服务端未就绪或未命中时 data 为 null
 */
export function getIpLocation() {
  return get<IpLocation | null>('/geoip/locate')
}

/**
 * 获取社区列表（支持分页与坐标筛选）
 * @param params 分页与坐标参数
 */
export function getCommunityList(params?: {
  page?: number
  page_size?: number
  longitude?: number
  latitude?: number
  keyword?: string
}) {
  return get<PageData<Community>>('/communities', params as Record<string, unknown>)
}

/**
 * 按 ID 获取完整社区信息
 * 后端只有列表接口，故取列表后本地匹配。
 * 登录响应与用户信息只携带 community_id / community_name，缺 delivery_fee、min_order_amount
 * 等字段，而结算页运费与起送门槛依赖它们，因此凡是需要写入 store 的社区都必须走本方法，
 * 禁止用字面量拼装残缺对象（会被持久化，导致运费恒为 0）
 * @param id 社区 ID
 * @returns 完整社区对象；id 为空、未匹配到或请求失败时返回 null
 */
export async function getCommunityById(id: number): Promise<Community | null> {
  if (!id) return null
  try {
    const res = await getCommunityList({ page: 1, page_size: 100 })
    return res.data?.list?.find((item) => item.id === id) || null
  } catch {
    return null
  }
}
