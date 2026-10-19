import { request } from './request'
import type { ApiResult } from './types'

/**
 * 系统信息（管理后台「关于」页）
 */
export interface SystemAbout {
  /** 项目版本号：编译时经 -ldflags 注入 git tag，未注入（本地源码运行）时为 dev */
  version: string
  /** 运行模式：production 生产环境 / development 开发环境（APP_ENV 环境变量，未设置视为开发） */
  app_env: string
  /** 部署运行方式（运行时自动检测）：docker 容器内运行 / binary 二进制直接运行 */
  deploy_mode: string
  /** MySQL 服务器版本（运行时真实查询），查询失败时为空串 */
  mysql_version: string
  /** Redis 服务器版本（运行时真实查询），查询失败时为空串 */
  redis_version: string
}

/**
 * 获取系统信息
 * 登录即可访问（不要求具体权限），数据供「关于」页面展示
 * @returns 系统信息（版本号 + MySQL/Redis 版本）
 */
export function getAbout() {
  return request<ApiResult<SystemAbout>>({
    url: '/api/v1/admin/about',
    method: 'GET',
  })
}
