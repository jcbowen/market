/**
 * 数值展示格式化工具
 * 统计页等场景的金额/数字统一出口，避免裸浮点直接渲染（如 5.0649999999999995）
 */

/**
 * 格式化金额（千分位，最多两位小数，不补零）
 * @param value 金额
 * @returns 格式化结果
 * @example
 * formatMoney(5.0649999999999995) // '5.06'
 * formatMoney(12345.6) // '12,345.6'
 */
export function formatMoney(value: number): string {
  return (value ?? 0).toLocaleString('zh-CN', { minimumFractionDigits: 0, maximumFractionDigits: 2 })
}

/**
 * 格式化金额（固定两位小数，带千分位），适合表格列对齐
 * @param value 金额
 * @returns 格式化结果
 * @example
 * formatMoneyFixed(5.065) // '5.07'
 */
export function formatMoneyFixed(value: number): string {
  return (value ?? 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}
