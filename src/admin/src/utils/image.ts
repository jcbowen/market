/**
 * 图片相关通用工具
 */

import { uploadImage } from '@/api/attachment'

/**
 * 读取图片宽高
 * @param file 图片文件
 * @returns [宽, 高]，解析失败返回 [0, 0]
 */
export function readImageSize(file: File): Promise<[number, number]> {
  return new Promise((resolve) => {
    const img = new Image()
    const objectURL = URL.createObjectURL(file)
    img.onload = () => {
      URL.revokeObjectURL(objectURL)
      resolve([img.naturalWidth, img.naturalHeight])
    }
    img.onerror = () => {
      URL.revokeObjectURL(objectURL)
      resolve([0, 0])
    }
    img.src = objectURL
  })
}

/**
 * 将 Date 对象格式化为 YYYY-MM-DD HH:mm:ss 字符串
 * @param date 日期对象
 * @returns 格式化后的时间字符串
 */
export function formatDateTime(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  )
}

/**
 * 上传单张图片文件（读取宽高后按存储模式分流上传）
 * @param file 图片文件
 * @returns 上传成功后的图片 URL
 * @throws Error 上传失败或未返回地址时抛出（业务错误已由 axios 拦截器提示过则原样抛出 axios 错误）
 */
export async function uploadImageFile(file: File): Promise<string> {
  const [width, height] = await readImageSize(file)
  const url = await uploadImage(file, width, height)
  if (!url) {
    throw new Error('上传成功但未返回图片地址')
  }
  return url
}

/**
 * 判断错误是否为已被 axios 拦截器统一提示过的请求错误
 * @param err 错误对象
 * @returns 已提示返回 true（调用方无需重复弹错）
 */
export function isHandledRequestError(err: unknown): boolean {
  return err instanceof Error && 'isAxiosError' in err
}
