import { request } from './request'
import type { ApiResult, AdminInfo, PageParams, PageResult, Role } from './types'

/**
 * 管理员管理相关 API
 */

/**
 * 获取管理员列表
 * @param params 查询参数
 * @returns 分页管理员列表
 */
export function getAdminList(params: PageParams & { keyword?: string; role_id?: number }) {
  return request<ApiResult<PageResult<AdminInfo>>>({
    url: '/api/v1/admin/admins',
    method: 'GET',
    params,
  })
}

/**
 * 获取管理员详情
 * @param id 管理员 ID
 * @returns 管理员详情
 */
export function getAdminDetail(id: number) {
  return request<ApiResult<AdminInfo>>({
    url: `/api/v1/admin/admins/${id}`,
    method: 'GET',
  })
}

/**
 * 创建管理员
 * @param data 管理员数据
 * @returns 创建结果
 */
export function createAdmin(data: Partial<AdminInfo> & { password: string }) {
  return request<ApiResult<AdminInfo>>({
    url: '/api/v1/admin/admins',
    method: 'POST',
    data,
  })
}

/**
 * 更新管理员
 * @param id 管理员 ID
 * @param data 管理员数据
 * @returns 更新结果
 */
export function updateAdmin(id: number, data: Partial<AdminInfo>) {
  return request<ApiResult<AdminInfo>>({
    url: `/api/v1/admin/admins/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除管理员
 * @param id 管理员 ID
 * @returns 删除结果
 */
export function deleteAdmin(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/admins/${id}`,
    method: 'DELETE',
  })
}

/**
 * 修改管理员密码
 * @param id 管理员 ID
 * @param data 密码数据
 * @returns 修改结果
 */
export function changeAdminPassword(id: number, data: { old_password: string; new_password: string }) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/admins/${id}/password`,
    method: 'PUT',
    data,
  })
}

/**
 * 获取角色列表
 * @returns 角色列表
 */
export function getRoleList() {
  return request<ApiResult<Role[]>>({
    url: '/api/v1/admin/roles',
    method: 'GET',
  })
}

/**
 * 创建角色
 * @param data 角色数据
 * @returns 创建结果
 */
export function createRole(data: Partial<Role>) {
  return request<ApiResult<Role>>({
    url: '/api/v1/admin/roles',
    method: 'POST',
    data,
  })
}

/**
 * 更新角色
 * @param id 角色 ID
 * @param data 角色数据
 * @returns 更新结果
 */
export function updateRole(id: number, data: Partial<Role>) {
  return request<ApiResult<Role>>({
    url: `/api/v1/admin/roles/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 删除角色
 * @param id 角色 ID
 * @returns 删除结果
 */
export function deleteRole(id: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/roles/${id}`,
    method: 'DELETE',
  })
}
