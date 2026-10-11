import SparkMD5 from 'spark-md5'
import service, { request } from './request'
import type { ApiResult } from './types'

/**
 * 附件管理相关 API
 * 覆盖附件配置读写、本地模式图片上传与 OSS 模式预签名直传（presign/confirm）
 */

/** 附件存储配置 */
export interface AttachmentSetting {
  storage_type: 'local' | 'oss'
  visit_domain: string
  oss_access_key_id: string
  oss_access_key_secret: string
  oss_endpoint: string
  oss_bucket_name: string
  oss_custom_visit_domain: string
  max_size: number
  updated_at: string
}

/** 预签名响应 */
export interface AttachmentPresignResult {
  attach_id: string
  hit: boolean
  url: string
  method: string
  headers: Record<string, string>
  final_url: string
  remote_path: string
}

/** 附件上传完成后的信息 */
export interface AttachmentUploadResult {
  attach_id: string
  url: string
  remote_path: string
  size: number
  width: number
  height: number
}

/**
 * 获取附件存储配置（AccessKeySecret 已脱敏）
 * @returns 脱敏后的附件配置
 */
export function getAttachmentSetting() {
  return request<ApiResult<AttachmentSetting>>({
    url: '/api/v1/admin/settings/attachment',
    method: 'GET',
  })
}

/**
 * 保存附件存储配置
 * Secret 传空或掩码值时服务端保留原值
 * @param data 附件配置
 * @returns 保存结果
 */
export function updateAttachmentSetting(data: AttachmentSetting) {
  return request<ApiResult<{ success: boolean }>>({
    url: '/api/v1/admin/settings/attachment',
    method: 'PUT',
    data,
  })
}

/**
 * 本地模式图片上传（multipart）
 * @param file 图片文件
 * @returns 上传结果，url 为可直接访问的地址
 */
export function uploadImageLocal(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request<ApiResult<AttachmentUploadResult>>({
    url: '/api/v1/admin/upload/image',
    method: 'POST',
    data: form,
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

/**
 * 获取预签名上传 URL（OSS 直传模式）
 * @param params 上传参数：文件名、MD5、MIME、大小及图片宽高
 * @returns 预签名结果，hit=true 表示秒传命中
 */
export function attachmentPresign(params: {
  filename: string
  md5: string
  content_type?: string
  size?: number
  width?: number
  height?: number
}) {
  return request<ApiResult<AttachmentPresignResult>>({
    url: '/api/v1/admin/attachment/presign',
    method: 'POST',
    data: params,
  })
}

/**
 * 确认上传完成（OSS 直传后调用，补写附件记录并激活秒传）
 * @param params 确认参数：attach_id 与文件 MD5、大小、宽高
 * @returns 上传结果
 */
export function attachmentConfirm(params: {
  attach_id: string
  md5: string
  size?: number
  width?: number
  height?: number
}) {
  return request<ApiResult<AttachmentUploadResult>>({
    url: '/api/v1/admin/attachment/confirm',
    method: 'POST',
    data: params,
  })
}

/**
 * 计算文件内容的 MD5（32 位小写十六进制）
 * 分块读取文件以避免大文件一次性载入内存
 * @param file 文件对象
 * @returns MD5 字符串
 */
export function computeFileMD5(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const spark = new SparkMD5.ArrayBuffer()
    const chunkSize = 2 * 1024 * 1024
    const chunks = Math.ceil(file.size / chunkSize)
    let current = 0
    const reader = new FileReader()

    const loadNext = () => {
      const start = current * chunkSize
      const chunk = file.slice(start, Math.min(start + chunkSize, file.size))
      reader.readAsArrayBuffer(chunk)
    }

    reader.onload = (e) => {
      spark.append(e.target?.result as ArrayBuffer)
      current += 1
      if (current < chunks) {
        loadNext()
        return
      }
      resolve(spark.end())
    }
    reader.onerror = () => reject(new Error('读取文件失败'))

    loadNext()
  })
}

/**
 * 上传图片（自动按附件存储模式分流）
 * - local：multipart 直传后端，返回访问 URL
 * - oss：presign 获取预签名 URL → PUT 直传 OSS → confirm 确认，秒传命中时直接返回
 * @param file 图片文件
 * @param width 图片宽度（像素，可选）
 * @param height 图片高度（像素，可选）
 * @returns 可直接访问的图片 URL
 */
export async function uploadImage(file: File, width = 0, height = 0): Promise<string> {
  const settingRes = await getAttachmentSetting()
  const setting = settingRes.data.data

  if (setting.storage_type === 'oss') {
    const md5 = await computeFileMD5(file)
    const presignRes = await attachmentPresign({
      filename: file.name,
      md5,
      content_type: file.type || undefined,
      size: file.size,
      width,
      height,
    })
    const presign = presignRes.data.data

    // 秒传命中：对象已存在，直接使用最终 URL
    if (presign.hit) {
      return presign.final_url
    }

    await service.put(presign.url, file, { headers: presign.headers })
    const confirmRes = await attachmentConfirm({
      attach_id: presign.attach_id,
      md5,
      size: file.size,
      width,
      height,
    })
    return confirmRes.data.data.url || presign.final_url
  }

  const res = await uploadImageLocal(file)
  return res.data.data.url
}
