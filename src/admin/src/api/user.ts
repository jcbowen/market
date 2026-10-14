import { request } from './request'
import type { ApiResult, Community, PageParams, PageResult, User } from './types'

/**
 * 用户管理相关 API
 */

/**
 * 获取社区列表
 * @param params 查询参数
 * @returns 分页社区列表
 */
export function getCommunityList(params: PageParams & { keyword?: string; status?: number }) {
  return request<ApiResult<PageResult<Community>>>({
    url: '/api/v1/admin/communities',
    method: 'GET',
    params,
  })
}

/**
 * 获取社区详情
 * @param id 社区 ID
 * @returns 社区详情
 */
export function getCommunity(id: number) {
  return request<ApiResult<Community>>({
    url: `/api/v1/admin/communities/${id}`,
    method: 'GET',
  })
}

/**
 * 创建社区
 * @param data 社区数据
 * @returns 创建结果
 */
export function createCommunity(data: Partial<Community>) {
  return request<ApiResult<Community>>({
    url: '/api/v1/admin/communities',
    method: 'POST',
    data,
  })
}

/**
 * 更新社区
 * @param id 社区 ID
 * @param data 社区数据
 * @returns 更新结果
 */
export function updateCommunity(id: number, data: Partial<Community>) {
  return request<ApiResult<Community>>({
    url: `/api/v1/admin/communities/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除社区
 * @param id 社区 ID
 * @returns 删除结果
 */
export function deleteCommunity(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/communities/${id}`,
    method: 'DELETE',
  })
}

/**
 * 获取当前社区门店设置
 * 目标社区取当前社区语境（X-Community-Id 请求头）
 * @returns 社区详情
 */
export function getCommunityProfile() {
  return request<ApiResult<Community>>({
    url: '/api/v1/admin/community/profile',
    method: 'GET',
  })
}

/**
 * 更新当前社区门店设置（白名单字段）
 * 仅可修改联系电话/负责人/自提点地址/配送配置/营业时间/社区外配送开关；
 * 社区名称、地址、经纬度、负责人电话等由超管在社区管理维护，服务端会忽略
 * @param data 白名单字段
 * @returns 更新后的社区详情
 */
export function updateCommunityProfile(data: Partial<Community>) {
  return request<ApiResult<Community>>({
    url: '/api/v1/admin/community/profile',
    method: 'PUT',
    data,
  })
}

/**
 * 获取用户列表
 * @param params 查询参数
 * @returns 分页用户列表
 */
export function getUserList(params: PageParams & { keyword?: string; community_id?: number; status?: number }) {
  return request<ApiResult<PageResult<User>>>({
    url: '/api/v1/admin/users',
    method: 'GET',
    params,
  })
}
