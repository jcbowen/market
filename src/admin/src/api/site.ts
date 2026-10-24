import { request } from './request'
import type { ApiResult } from './types'

/**
 * 站点品牌公开信息 API
 * 免登录接口，供登录页与首屏品牌位读取
 */

/** 站点品牌信息（仅非敏感字段） */
export interface SiteBrand {
  site_name: string
  logo_url: string
  favicon_url: string
  footer_html: string
  seo_description: string
  seo_keywords: string
  base_url: string
  version: number
}

/**
 * 获取公开站点信息
 * @returns 站点品牌信息
 */
export function getPublicSiteInfo() {
  return request<ApiResult<SiteBrand>>({
    url: '/api/v1/site-info',
    method: 'GET',
  })
}
