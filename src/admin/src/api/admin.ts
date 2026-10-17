import { request } from './request'
import type { ApiResult, AdminInfo, PageParams, PageResult, Role } from './types'

/**
 * 管理员管理相关 API
 */

/**
 * 获取员工列表（当前社区语境下的任职员工）
 * @param params 查询参数
 * @returns 分页员工列表
 */
export function getAdminList(params: PageParams & { keyword?: string }) {
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
 * 创建员工（当前社区任职）
 * username 已存在时复用该账号（不修改其密码），仅追加任职
 * @param data 员工数据
 * @returns 创建结果
 */
export function createAdmin(data: {
  username: string
  password?: string
  real_name?: string
  phone?: string
  role_ids: number[]
}) {
  return request<ApiResult<AdminInfo>>({
    url: '/api/v1/admin/admins',
    method: 'POST',
    data,
  })
}

/**
 * 更新员工（资料 + 当前社区角色任职）
 * @param id 员工 ID
 * @param data 员工数据
 * @returns 更新结果
 */
export function updateAdmin(
  id: number,
  data: {
    real_name?: string
    phone?: string
    status?: number
    role_ids?: number[]
  },
) {
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
 * 默认返回当前社区的角色；超管可传 scope=global 查看全局角色，
 * 或传 community_id 查看任意社区的角色（社区权限分配页使用）
 * @param params 查询参数
 * @returns 角色列表
 */
export function getRoleList(params?: { scope?: 'global'; community_id?: number }) {
  return request<ApiResult<Role[]>>({
    url: '/api/v1/admin/roles',
    method: 'GET',
    params,
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

/**
 * 获取社区权限分配详情（超管专用）
 * 返回该社区的权限上限（内置社区管理员角色权限集）及社区角色概览
 * @param id 社区 ID
 * @returns 权限上限与角色列表
 */
export function getCommunityPermissions(id: number) {
  return request<
    ApiResult<{
      community_id: number
      role_id: number
      permissions: string[]
      roles: Role[]
    }>
  >({
    url: `/api/v1/admin/communities/${id}/permissions`,
    method: 'GET',
  })
}

/**
 * 设置社区权限上限（超管专用）
 * 即更新该社区内置社区管理员角色的权限集，保存后运行时立即对社区全员生效
 * @param id 社区 ID
 * @param permissions 权限编码列表
 */
export function setCommunityPermissions(id: number, permissions: string[]) {
  return request<ApiResult<{ community_id: number; role_id: number }>>({
    url: `/api/v1/admin/communities/${id}/permissions`,
    method: 'PUT',
    data: { permissions },
  })
}

/**
 * 支付配置信息
 */
export interface PaymentSetting {
  enabled_channels: string[]
  wechat: WechatPayConfig
  alipay: AlipayPayConfig
  wechat_payment_ready: boolean
  alipay_ready: boolean
  wechat_cert_uploaded: boolean
  alipay_cert_uploaded: boolean
  updated_at: string
  suggested_urls: PaymentSuggestedURLs
  /** 适用客户端渠道勾选（mp/gzh/app/h5；空数组=全部客户端渠道可用，存量兼容口径） */
  wechat_client_channels: string[]
  alipay_client_channels: string[]
}

/**
 * 客户端渠道（渠道管理）
 * 后台统一维护各客户端访问凭证（AppID/AppSecret）、启用开关与排序；
 * 登录与支付链路按渠道取用凭证
 */
export interface ClientChannel {
  id: number
  /** 渠道标识：mp 微信小程序 / gzh 微信公众号 / app 开放平台App / h5 浏览器H5 */
  code: string
  name: string
  app_id: string
  app_secret: string
  enabled: boolean
  sort: number
  /** 凭证是否配置完整（h5 无需凭证恒为 true） */
  configured: boolean
  updated_at: string
}

/**
 * 回调 URL 配置引导
 * 由后端根据当前访问域名自动生成
 */
export interface PaymentSuggestedURLs {
  base_url: string
  wechat_notify_url: string
  alipay_notify_url: string
}

/**
 * 微信支付配置
 */
export interface WechatPayConfig {
  app_id: string
  app_secret: string
  mch_id: string
  api_v3_key: string
  cert_serial_no: string
  private_key: string
  notify_url: string
}

/**
 * 支付宝支付配置
 */
export interface AlipayPayConfig {
  app_id: string
  private_key: string
  alipay_public_key: string
  notify_url: string
  return_url: string
}

/**
 * 获取支付配置
 * @returns 支付配置详情
 */
export function getPaymentSetting() {
  return request<ApiResult<PaymentSetting>>({
    url: '/api/v1/admin/settings/payment',
    method: 'GET',
  })
}

/**
 * 更新支付配置
 * @param data 支付配置数据
 * @returns 更新结果
 */
export function updatePaymentSetting(data: PaymentSetting) {
  return request<ApiResult<{ success: boolean }>>({
    url: '/api/v1/admin/settings/payment',
    method: 'PUT',
    data,
  })
}

/**
 * 上传支付证书文件
 * 文件内容存入数据库，上传后支付配置即时热更新
 * @param channel 支付渠道：wechat / alipay
 * @param certType 证书类型：private_key
 * @param file 证书文件（.pem 等）
 * @returns 上传结果
 */
export function uploadPaymentCert(channel: string, certType: string, file: File) {
  const formData = new FormData()
  formData.append('channel', channel)
  formData.append('cert_type', certType)
  formData.append('file', file)
  return request<ApiResult<{ success: boolean; message: string }>>({
    url: '/api/v1/admin/settings/payment/cert',
    method: 'POST',
    data: formData,
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

/**
 * 获取客户端渠道列表（全部，含停用）
 * @returns 渠道列表
 */
export function getClientChannels() {
  return request<ApiResult<{ channels: ClientChannel[] }>>({
    url: '/api/v1/admin/settings/channels',
    method: 'GET',
  })
}

/**
 * 更新客户端渠道配置
 * app_secret 提交为空时服务端保留原值
 * @param id 渠道 ID
 * @param data 渠道配置数据
 * @returns 更新后的渠道信息
 */
export function updateClientChannel(id: number, data: Pick<ClientChannel, 'code' | 'name' | 'app_id' | 'app_secret' | 'enabled' | 'sort'>) {
  return request<ApiResult<ClientChannel>>({
    url: `/api/v1/admin/settings/channels/${id}`,
    method: 'PUT',
    data,
  })
}
