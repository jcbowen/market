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

/** 数据清理预检的单个数据域统计 */
export interface DataCleanupSection {
  key: string
  name: string
  description: string
  /** 将清理的记录行数 */
  row_count: number
  /** 将删除的文件数（仅附件域） */
  file_count: number
}

/** 数据清理预检报告 */
export interface DataCleanupPreview {
  sections: DataCleanupSection[]
}

/** 数据清理结果条目（域级或附件文件级） */
export interface DataCleanupItem {
  section_key: string
  name: string
  /** cleaned=已清理 failed=失败 */
  status: string
  message?: string
  rows?: number
}

/** 数据清理结果 */
export interface DataCleanupResult {
  sections_total: number
  sections_failed: number
  rows_deleted: number
  files_deleted: number
  files_failed: number
  details: DataCleanupItem[]
}

/** 执行清理必须输入的确认文案（与服务端约定一致） */
export const DATA_CLEANUP_CONFIRM_TEXT = '确认清空全部业务数据'

/**
 * 数据清理预检：逐域统计将清理的行数（只读）
 */
export function previewDataCleanup() {
  return request<ApiResult<DataCleanupPreview>>({
    url: '/api/v1/admin/data-cleanup/preview',
    method: 'GET',
  })
}

/**
 * 执行数据清理（不可逆）
 * @param confirmText 强确认文案，必须与 DATA_CLEANUP_CONFIRM_TEXT 完全一致
 */
export function runDataCleanup(confirmText: string) {
  return request<ApiResult<DataCleanupResult>>({
    url: '/api/v1/admin/data-cleanup/run',
    method: 'POST',
    data: { confirm_text: confirmText },
  })
}
