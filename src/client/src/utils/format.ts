/**
 * 格式化工具函数
 * 包含价格、时间等常用格式化方法
 */

/**
 * 将价格格式化为人民币字符串
 * @param price 价格（单位：元）
 * @param prefix 货币前缀，默认 ¥
 * @returns 格式化后的价格字符串，如 ¥99.00
 */
export function formatPrice(price: number | string, prefix = '¥'): string {
  const num = Number(price)
  if (Number.isNaN(num)) return `${prefix}0.00`
  return `${prefix}${num.toFixed(2)}`
}

/**
 * 格式化日期时间
 * @param date 日期字符串、时间戳或 Date 对象
 * @param format 输出格式，默认 yyyy-MM-dd HH:mm:ss
 * @returns 格式化后的时间字符串
 */
export function formatDate(
  date: string | number | Date,
  format = 'yyyy-MM-dd HH:mm:ss',
): string {
  const d = new Date(date)
  if (Number.isNaN(d.getTime())) return ''

  const pad = (n: number) => String(n).padStart(2, '0')
  const map: Record<string, string> = {
    'yyyy': String(d.getFullYear()),
    'MM': pad(d.getMonth() + 1),
    'dd': pad(d.getDate()),
    'HH': pad(d.getHours()),
    'mm': pad(d.getMinutes()),
    'ss': pad(d.getSeconds()),
  }

  return format.replace(/yyyy|MM|dd|HH|mm|ss/g, (key) => map[key])
}

/**
 * 格式化手机号，中间四位隐藏
 * @param phone 手机号
 * @returns 如 138****8888
 */
export function maskPhone(phone: string): string {
  if (!phone || phone.length !== 11) return phone
  return `${phone.slice(0, 3)}****${phone.slice(7)}`
}

/**
 * 格式化两点间距离（Haversine 球面距离）
 * @param from 定位原点经纬度
 * @param to 目标点经纬度
 * @returns 如 "850m"/"1.2km"，任一坐标缺失时返回空串
 */
export function formatDistance(
  from: { longitude: number; latitude: number } | null | undefined,
  to: { longitude: number; latitude: number } | null | undefined,
): string {
  if (!from?.longitude || !from?.latitude || !to?.longitude || !to?.latitude) return ''

  const rad = Math.PI / 180
  const lat1 = from.latitude * rad
  const lat2 = to.latitude * rad
  const dLat = (to.latitude - from.latitude) * rad
  const dLon = (to.longitude - from.longitude) * rad
  const h =
    Math.sin(dLat / 2) ** 2 + Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLon / 2) ** 2
  const km = 2 * 6371 * Math.asin(Math.sqrt(h))
  return km < 1 ? `${Math.round(km * 1000)}m` : `${km.toFixed(1)}km`
}
