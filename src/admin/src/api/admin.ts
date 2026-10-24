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
  /** 创建时绑定的会员 ID（0=不绑定；绑定后该会员在任职社区即履约人员） */
  user_id?: number
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
 * @param data 员工数据（user_id 三态：未传=不改动；0=解绑；>0=绑定/换绑）
 * @returns 更新结果
 */
export function updateAdmin(
  id: number,
  data: {
    real_name?: string
    phone?: string
    status?: number
    role_ids?: number[]
    /** 绑定的会员 ID（三态：未传=不改动；0=解绑；>0=绑定/换绑） */
    user_id?: number | null
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
 * 默认返回当前社区的角色；超管可传 scope=all 跨社区总览（含所属社区名与任职人数），
 * 或传 community_id 查看任意社区的角色（社区权限分配页使用）
 * @param params 查询参数
 * @returns 角色列表
 */
export function getRoleList(params?: { scope?: 'all'; community_id?: number }) {
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
  /** 支付宝配置自检失败原因，空表示自检通过（就绪 = 字段齐全且自检通过）；仅服务端出参，保存载荷无需携带 */
  alipay_ready_message?: string
  /** 微信支付 V3 API 密钥是否已配置（密钥明文不回传，保存时留空即保留原值）；仅服务端出参 */
  wechat_api_v3_configured?: boolean
  wechat_cert_uploaded: boolean
  alipay_cert_uploaded: boolean
  /** 支付宝证书模式三证书上传状态（证书内容不回传，仅回传标志）；仅服务端出参 */
  alipay_app_cert_uploaded?: boolean
  alipay_public_cert_uploaded?: boolean
  alipay_root_cert_uploaded?: boolean
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
  /** 服务端不回传明文，恒为空串；存量凭证以 configured 标志判断，保存空值时服务端保留原值 */
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
 * 支付宝加签方式
 * - public 公钥模式：AppID + 应用私钥 + 支付宝公钥
 * - cert 证书模式：AppID + 应用私钥 + 应用公钥证书 + 支付宝公钥证书 + 支付宝根证书
 * 与服务端 config.AlipayMode* 常量一致；存量数据为空串，前端按 public 处理
 */
export type AlipaySignMode = 'public' | 'cert'

/**
 * 支付宝支付配置
 */
export interface AlipayPayConfig {
  app_id: string
  private_key: string
  /** 加签方式，见 AlipaySignMode */
  mode: string
  alipay_public_key: string
  notify_url: string
  return_url: string
  /** 网关地址（仅协议+域名），空串=生产环境 openapi.alipay.com；填沙箱网关即切沙箱联调 */
  gateway_url: string
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
 * 文件内容存入数据库，上传后支付配置即时热更新；
 * 后台「粘贴内容写入」入口复用本函数（前端把文本包成 .pem File 提交，服务端校验口径一致）
 * @param channel 支付渠道：wechat / alipay
 * @param certType 证书类型：private_key（应用私钥）、app_cert（应用公钥证书）、
 *                 alipay_public_cert（支付宝公钥证书）、root_cert（支付宝根证书），
 *                 后三类仅支付宝证书模式使用
 * @param file 证书文件（.pem/.crt/.key/.txt）
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

/** 站点设置（平台品牌与部署域名） */
export interface SiteSetting {
  base_url: string
  site_name: string
  logo_url: string
  favicon_url: string
  footer_html: string
  seo_description: string
  seo_keywords: string
  updated_at: string
  version: number
  /** 当前生效的部署域名（环境变量优先于本页配置） */
  effective_base_url: string
  /** 生效域名来源：db 站点设置（显式保存后恒为最高优先级）/ env 环境变量兜底（本页未配置时回填默认值）/ request 未配置按请求现推 */
  base_url_source: 'env' | 'db' | 'request'
  /** 以下为按生效域名派生的回调地址，仅出参 */
  wechat_notify_url: string
  alipay_notify_url: string
  alipay_return_url: string
}

/**
 * 获取站点设置
 * @returns 站点设置详情与派生的回调地址
 */
export function getSiteSetting() {
  return request<ApiResult<SiteSetting>>({
    url: '/api/v1/admin/settings/site',
    method: 'GET',
  })
}

/**
 * 保存站点设置
 * 覆盖式写入：字段提交空串即清空该配置
 * @param data 站点设置数据（仅品牌字段）
 * @returns 保存结果与新版本号
 */
export function updateSiteSetting(data: Pick<SiteSetting, 'base_url' | 'site_name' | 'logo_url' | 'favicon_url' | 'footer_html' | 'seo_description' | 'seo_keywords'>) {
  return request<ApiResult<{ success: boolean; version: number }>>({
    url: '/api/v1/admin/settings/site',
    method: 'PUT',
    data,
  })
}
