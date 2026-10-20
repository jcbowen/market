import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { getPublicSiteInfo } from '@/api/site'
import type { SiteBrand } from '@/api/site'

/** 未配置品牌时的默认名称，与历史文案保持一致 */
const DEFAULT_SITE_NAME = '社区商超'
const DEFAULT_SITE_NAME_SHORT = '商超'

/**
 * 站点品牌状态 Store
 * 从免登录的站点信息接口读取平台品牌（名称/logo/版权/备案），供侧边栏、登录页与关于页共用；
 * 浏览器标签标题与 favicon 由网关在服务端 HTML 中注入，前端不再改写
 */
export const useBrandingStore = defineStore('branding', () => {
  const brand = ref<SiteBrand>({
    site_name: '',
    logo_url: '',
    favicon_url: '',
    copyright_text: '',
    icp_number: '',
    seo_description: '',
    seo_keywords: '',
    base_url: '',
    version: 0,
  })
  const loaded = ref(false)

  const siteName = computed(() => brand.value.site_name || DEFAULT_SITE_NAME)
  const siteNameShort = computed(() => (brand.value.site_name ? brand.value.site_name.slice(0, 2) : DEFAULT_SITE_NAME_SHORT))
  const logoURL = computed(() => brand.value.logo_url)
  /** 页脚文案：版权 + 备案号，任一为空时只展示已配置部分 */
  const footerText = computed(() => {
    const parts = [brand.value.copyright_text, brand.value.icp_number].filter(Boolean)
    return parts.join(' · ')
  })

  /**
   * 加载站点品牌信息
   * 默认只拉一次；站点设置保存后传 force 重新拉取，使品牌位即时更新
   * @param force 是否忽略已加载状态强制刷新
   */
  async function load(force = false) {
    if (loaded.value && !force) return
    try {
      const res = await getPublicSiteInfo()
      if (res.data.code === 0 && res.data.data) {
        brand.value = res.data.data
      }
      loaded.value = true
    } catch {
      // 品牌为装饰性信息，拉取失败保持默认文案，不打断页面
    }
  }

  return { brand, siteName, siteNameShort, logoURL, footerText, load }
})
