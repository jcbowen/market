import { request } from './request'
import type { ApiResult } from './types'

/** 数据包条目摘要 */
export interface DataPackageSection {
  key: string
  name: string
  description: string
  item_count: number
}

/** 数据包 */
export interface DataPackage {
  key: string
  name: string
  description: string
  sections: DataPackageSection[]
}

/** 预检/执行结果条目 */
export interface DataImportItem {
  section_key: string
  item_key: string
  name: string
  /** 预检动作：create / skip / depend_missing；执行时复用为状态 */
  action: string
  detail: string
  /** 预检默认勾选态 */
  selected: boolean
  /** 执行状态：created / skipped / failed（预检时为空） */
  status?: string
  message?: string
}

/** 预检报告 */
export interface DataImportPreview {
  items: DataImportItem[]
}

/** 导入结果 */
export interface DataImportResult {
  created: number
  skipped: number
  failed: number
  details: DataImportItem[]
}

/**
 * 数据包列表（含各条目摘要计数）
 */
export function listDataPackages() {
  return request<ApiResult<{ packages: DataPackage[] }>>({
    url: '/api/v1/admin/data-import/packages',
    method: 'GET',
  })
}

/**
 * 导入预检
 * @param packageKey 包标识
 * @param selectedKeys 勾选条目 key；空数组 = 全选
 * @param targetUserPhone 指定导入会员手机号（用户券/购物车落到该会员名下；空 = 未指定）
 */
export function previewDataImport(packageKey: string, selectedKeys: string[], targetUserPhone = '') {
  return request<ApiResult<DataImportPreview>>({
    url: '/api/v1/admin/data-import/preview',
    method: 'POST',
    data: { package_key: packageKey, selected_keys: selectedKeys, target_user_phone: targetUserPhone },
  })
}

/**
 * 执行导入
 * @param packageKey 包标识
 * @param selectedKeys 勾选条目 key；空数组 = 全选
 * @param excludedKeys 预检后手动排除的条目 key
 * @param onSale 导入后商品状态：true 上架 / false 下架
 */
export function importDataPackage(
  packageKey: string,
  selectedKeys: string[],
  excludedKeys: string[],
  onSale: boolean,
  targetUserPhone = '',
) {
  return request<ApiResult<DataImportResult>>({
    url: '/api/v1/admin/data-import/import',
    method: 'POST',
    data: {
      package_key: packageKey,
      selected_keys: selectedKeys,
      excluded_keys: excludedKeys,
      on_sale: onSale,
      target_user_phone: targetUserPhone,
    },
  })
}
