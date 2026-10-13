/**
 * 拼接省/市/区（可含详细地址）为地址文本
 * 直辖市采用「市与省同名」口径存储（如 重庆市/重庆市/两江新区），
 * 相邻重复的部分只保留一份，避免展示成「重庆市重庆市两江新区」
 * @param province 省名
 * @param city 市名
 * @param district 区县名
 * @param detail 详细地址（可选，直接拼在末尾）
 */
export function joinRegionAddress(
  province?: string,
  city?: string,
  district?: string,
  detail?: string
): string {
  const parts = [province, city, district].map((p) => (p || '').trim()).filter(Boolean)
  const deduped = parts.filter((p, i) => i === 0 || p !== parts[i - 1])
  if (detail?.trim()) deduped.push(detail.trim())
  return deduped.join('')
}
