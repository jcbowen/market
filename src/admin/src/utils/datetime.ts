/**
 * 时间显示格式化工具
 * 后端 time.Time 序列化为 RFC3339(如 2026-10-11T23:59:55+08:00),
 * 各页面统一经 formatDateTime 转为 "2026-10-11 23:59:55" 展示。
 */

/**
 * 格式化时间为 YYYY-MM-DD HH:mm:ss
 * 直接截取 RFC3339 字符串(含服务器本地时区偏移),不做时区换算;
 * 空值返回 '-',非时间文本(如"明天上午"类时段描述)原样返回
 * @param value 时间字符串(RFC3339 或其他文本)
 * @returns 格式化结果
 * @example
 * formatDateTime('2026-10-11T23:59:55+08:00') // '2026-10-11 23:59:55'
 * formatDateTime('2026-10-11T23:59:55.123+08:00') // '2026-10-11 23:59:55'
 * formatDateTime('') // '-'
 * formatDateTime('明天上午') // '明天上午'
 */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '-'
  // RFC3339:日期 T 时间(可能带毫秒);无 T 的非时间字符串原样返回
  if (!value.includes('T')) return value
  return value.slice(0, 19).replace('T', ' ')
}
