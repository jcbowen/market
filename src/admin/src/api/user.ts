import { request } from './request'
import { requestWithEncryptedPasswords } from '@/utils/crypto'
import type { ApiResult, Account, AccountAssignment, Community, FrequentProduct, PageParams, PageResult, User } from './types'

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
 * 服务端按当前社区语境（X-Community-Id 请求头）过滤会员-社区关联
 * @param params 查询参数
 * @returns 分页用户列表
 */
export function getUserList(params: PageParams & { keyword?: string; status_filter?: number }) {
  return request<ApiResult<PageResult<User>>>({
    url: '/api/v1/admin/users',
    method: 'GET',
    params,
  })
}

/**
 * 注册会员（幂等，归属当前社区）
 * 手机号已注册（含其他社区老会员）时不报错：服务端复用既有会员并自动关联当前社区
 * @param data 会员基础信息（手机号必填，生日选填 YYYY-MM-DD）
 * @param data.scene 注册场景：admin 后台会员管理 / pos 收银台（仅影响关联来源标记）
 * @returns 会员信息（is_new=true 表示本次新注册，false 表示复用既有会员）
 */
export function createUser(data: {
  phone: string
  nickname?: string
  gender?: number
  birthday?: string
  scene?: 'admin' | 'pos'
}) {
  return request<ApiResult<User & { is_new?: boolean }>>({
    url: '/api/v1/admin/users',
    method: 'POST',
    data,
  })
}

/**
 * 获取会员详情（会员订单弹窗信息卡数据源）
 * 服务端按当前社区语境校验会员关联，跨社区会员按不存在处理（超管不限）
 * @param id 会员 ID
 * @returns 会员详情
 */
export function getUser(id: number) {
  return request<ApiResult<User>>({
    url: `/api/v1/admin/users/${id}`,
    method: 'GET',
  })
}

/**
 * 编辑会员基础资料（昵称/性别/生日）
 * 表单为全量提交：gender 与 birthday 全量覆盖（birthday 传空串表示清空），nickname 空串不修改
 * @param id 会员 ID
 * @param data 编辑数据
 * @returns 更新后的会员信息
 */
export function updateUser(id: number, data: { nickname?: string; gender?: number; birthday?: string }) {
  return request<ApiResult<User>>({
    url: `/api/v1/admin/users/${id}`,
    method: 'PUT',
    data,
  })
}

/**
 * 拉黑/恢复会员
 * 拉黑后该会员 C 端登录与所有已登录请求均被拦截
 * @param id 会员 ID
 * @param status 0 拉黑 1 恢复
 * @returns 操作结果
 */
export function setUserStatus(id: number, status: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/users/${id}/status`,
    method: 'PUT',
    data: { status },
  })
}

/**
 * 获取会员常买商品排行（会员订单弹窗板块）
 * 按该会员有效订单（已支付及之后，排除待支付/取消/退款）统计，按购买次数降序
 * @param id 会员 ID
 * @param params 分页参数
 * @returns 分页常买商品列表
 */
export function getUserFrequentProducts(id: number, params: PageParams) {
  return request<ApiResult<PageResult<FrequentProduct>>>({
    url: `/api/v1/admin/users/${id}/frequent-products`,
    method: 'GET',
    params,
  })
}

/**
 * 获取本社区履约人员列表（订单指派弹窗数据源）
 * 履约人员 = 本社区任职且绑定会员的员工（配置入口在员工编辑页）
 * @returns 履约人员列表（user_id/nickname/phone）
 */
export function getFulfillers() {
  return request<ApiResult<{ user_id: number; nickname: string; phone: string }[]>>({
    url: '/api/v1/admin/fulfillers',
    method: 'GET',
  })
}

/**
 * 获取当前账号可进入的社区列表（社区选择页用）
 * 普通管理员返回其任职社区；超管返回全部社区（含停业，附状态标识）
 * @returns 社区简表
 */
export function getMyCommunities() {
  return request<ApiResult<Pick<Community, 'id' | 'name' | 'status'>[]>>({
    url: '/api/v1/admin/my/communities',
    method: 'GET',
  })
}

/**
 * 获取全局账号列表（超管）
 * @param params 查询参数
 * @returns 分页账号列表
 */
export function getAccountList(params: PageParams & { keyword?: string }) {
  return request<ApiResult<PageResult<Account>>>({
    url: '/api/v1/admin/accounts',
    method: 'GET',
    params,
  })
}

/**
 * 新增后台账号（超管）
 * 仅创建账号本身，不在任何社区任职；任职由各社区员工管理页分配
 * 初始密码经 RSA 公钥加密后传输
 * @param data 账号信息
 * @returns 新建账号
 */
export function createAccount(data: { username: string; password: string; real_name?: string; phone?: string }) {
  return requestWithEncryptedPasswords(
    async (enc) => ({
      username: data.username,
      real_name: data.real_name,
      phone: data.phone,
      ...(await enc.encryptFields({ password: data.password })),
    }),
    (body) =>
      request<ApiResult<Account>>({
        url: '/api/v1/admin/accounts',
        method: 'POST',
        data: body,
      }),
  )
}

/**
 * 停用/启用后台账号（超管）
 * @param id 账号 ID
 * @param status 0 停用 1 启用
 * @returns 操作结果
 */
export function updateAccountStatus(id: number, status: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/accounts/${id}/status`,
    method: 'PUT',
    data: { status },
  })
}

/**
 * 重置后台账号密码（超管，免旧密码）
 * 新密码经 RSA 公钥加密后传输
 * @param id 账号 ID
 * @param newPassword 新密码
 * @returns 操作结果
 */
export function resetAccountPassword(id: number, newPassword: string) {
  return requestWithEncryptedPasswords(
    async (enc) => await enc.encryptFields({ new_password: newPassword }),
    (body) =>
      request<ApiResult<null>>({
        url: `/api/v1/admin/accounts/${id}/password`,
        method: 'PUT',
        data: body,
      }),
  )
}

/**
 * 查询账号任职的社区与角色列表（超管，任职社区数弹窗用）
 * 按社区 ID 升序返回；超管账号返回空列表（不受社区限制）
 * @param id 账号 ID
 * @returns 按社区分组的任职列表
 */
export function getAccountAssignments(id: number) {
  return request<ApiResult<AccountAssignment[]>>({
    url: `/api/v1/admin/accounts/${id}/assignments`,
    method: 'GET',
  })
}

/**
 * 查询社区现任管理员列表（超管，社区编辑页用）
 * @param communityId 社区 ID
 * @returns 管理员列表
 */
export function getCommunityAdmins(communityId: number) {
  return request<ApiResult<Account[]>>({
    url: `/api/v1/admin/communities/${communityId}/admins`,
    method: 'GET',
  })
}

/**
 * 指定社区管理员（超管）
 * username 已存在时复用账号；授予社区内置管理员角色（每社区仅一人，自动替换原任职者）
 * 初始密码经 RSA 公钥加密后传输（留空 = 复用账号不设密）
 * @param communityId 社区 ID
 * @param data 账号信息
 * @returns 操作结果
 */
export function addCommunityAdmin(
  communityId: number,
  data: { username: string; password?: string; real_name?: string; phone?: string },
) {
  return requestWithEncryptedPasswords(
    async (enc) => ({
      username: data.username,
      real_name: data.real_name,
      phone: data.phone,
      ...(data.password ? await enc.encryptFields({ password: data.password }) : {}),
    }),
    (body) =>
      request<ApiResult<null>>({
        url: `/api/v1/admin/communities/${communityId}/admins`,
        method: 'POST',
        data: body,
      }),
  )
}

/**
 * 移除社区管理员任职（超管）
 * @param communityId 社区 ID
 * @param adminId 管理员账号 ID
 * @returns 操作结果
 */
export function removeCommunityAdmin(communityId: number, adminId: number) {
  return request<ApiResult<null>>({
    url: `/api/v1/admin/communities/${communityId}/admins/${adminId}`,
    method: 'DELETE',
  })
}
