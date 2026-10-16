/**
 * 动态测试数据生成器
 * 所有测试数据带时间戳/随机后缀，保证可重复执行且不与既有数据冲突；
 * 管理端造数统一使用 AUTO-TEST 前缀便于清理。
 */

/** 常用 C 端测试手机号（init.dev.sql 预置用户） */
export const PRESET_PHONE = '13800138000';

/** 随机手机号池前缀（测试专用号段） */
const PHONE_PREFIXES = ['139', '150', '176'];
/** 号码种子：进程内自增，保证单次运行唯一 */
let phoneSeq = Math.floor(Date.now() / 1000) % 100_000_000;

/**
 * 生成唯一的 11 位测试手机号
 * - 函数名: uniquePhone
 * - 参数: 无
 * - 返回: string，如 17679214372（号段 + 8 位自增序号）
 * - 异常: 无；跨进程重启种子基于秒级时间戳，极端情况号码复用已有用户（建号幂等，不影响断言）
 * - 使用示例: const phone = uniquePhone();
 */
export function uniquePhone(): string {
  phoneSeq++;
  const prefix = PHONE_PREFIXES[phoneSeq % PHONE_PREFIXES.length];
  return prefix + String(phoneSeq).slice(-8).padStart(8, '0');
}

/**
 * 生成带 AUTO-TEST 前缀的唯一名称
 * - 函数名: autoName
 * - 参数: name — 业务名称，如 '商品'
 * - 返回: string，如 'AUTO-TEST 商品 1718000000000'
 * - 异常: 无
 * - 使用示例: const productName = autoName('商品');
 */
export function autoName(name: string): string {
  return `AUTO-TEST ${name} ${Date.now()}`;
}

/**
 * 生成通用唯一后缀（用于 SKU 名、地址等）
 * - 函数名: uniqueSuffix
 * - 参数: 无
 * - 返回: string，如 'ts1718000000000x3'
 * - 异常: 无
 * - 使用示例: const skuName = `规格-${uniqueSuffix()}`;
 */
export function uniqueSuffix(): string {
  return `ts${Date.now()}x${Math.floor(Math.random() * 10)}`;
}
