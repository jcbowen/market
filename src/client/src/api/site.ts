import { get } from './request'

/**
 * 站点品牌信息（免登录公开接口，仅非敏感字段）
 */
export interface SiteBrand {
  site_name: string
  logo_url: string
  favicon_url: string
  copyright_text: string
  icp_number: string
  seo_description: string
  seo_keywords: string
  base_url: string
  version: number
}

/**
 * 查询站点品牌信息
 * 用于页脚版权与备案号展示，接口异常时按「不展示」降级
 * @returns 站点品牌信息，失败返回 null
 */
export async function getSiteInfo(): Promise<SiteBrand | null> {
  try {
    const res = await get<SiteBrand>('/site-info')
    return res.data || null
  } catch {
    return null
  }
}
