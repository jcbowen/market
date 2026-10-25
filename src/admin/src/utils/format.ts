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

/**
 * 金额展示统一出口（元，固定两位小数、带千分位、null 容错为 0）
 * 全部金额渲染一律调用本函数，禁止再裸写 toFixed(2)：
 * 金额类型后续迁移 decimal 时只需改这一处
 * @param value 金额（元），允许 null/undefined
 * @returns 格式化结果
 * @example
 * formatAmount(1234.5) // '1,234.50'
 * formatAmount(null) // '0.00'
 */
export function formatAmount(value: number | null | undefined): string {
  return formatMoneyFixed(Number(value ?? 0))
}
