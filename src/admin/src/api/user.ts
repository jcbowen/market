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
