/**
 * 动态测试数据生成器
 * 所有测试数据带时间戳/随机后缀，保证可重复执行且不与既有数据冲突；
 * 管理端造数统一使用 AUTO-TEST 前缀便于清理。
 */

import { appendFileSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';

/** 常用 C 端测试手机号（演示种子包预置用户） */
export const PRESET_PHONE = '13800138000';

/**
 * 本轮创建的 C 端会员登记表（JSONL，一行一条）
 * 会员没有删除接口（后台只有 PUT /admin/users/:id/status），所以回收口径是「登记 + 停用」：
 * 用例经 clientLogin 造出的每个会员都落表，globalTeardown 读取后逐个停用，
 * 让脏数据停在可识别、不可登录的状态；要彻底清行只能重建库（演示种子包）。
 * 预置账号（PRESET_PHONE）与人工使用的固定账号一律不登记，避免把共享数据停用。
 *
 * 路径锚定用 `__dirname`（helpers/ 的上一级即 tests/）：Playwright 把 TS 转成 CommonJS 执行，
 * 这里写 `import.meta.url` 会在收集阶段直接报 "Cannot use 'import.meta' outside a module"。
 */
export const TEST_USER_REGISTRY = join(__dirname, '..', 'test-results', 'test-users.jsonl');

/** 登记表一行：会员 ID、手机号、所属社区（停用时需带对应社区语境头） */
export interface TestUserRecord {
  user_id: number;
  phone: string;
  community_id: number;
}

/**
 * 登记一个本轮创建的测试会员
 * - 函数名: recordTestUser
 * - 参数: rec — 会员 ID / 手机号 / 社区 ID
 * - 返回: void
 * - 异常: 写文件失败时抛错（登记表缺失会让回收失去依据，宁可显式失败也不静默）
 * - 使用示例: recordTestUser({ user_id: info.user.id, phone, community_id: info.user.community_id });
 */
export function recordTestUser(rec: TestUserRecord): void {
  const dir = dirname(TEST_USER_REGISTRY);
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  appendFileSync(TEST_USER_REGISTRY, `${JSON.stringify(rec)}\n`, 'utf8');
}

/**
 * 读取本轮登记表（globalTeardown 用）
 * - 函数名: readTestUserRegistry
 * - 参数: 无
 * - 返回: TestUserRecord[]，按会员 ID 去重（同号重登会产生多条）；文件不存在时返回空数组
 * - 异常: 单行 JSON 解析失败时跳过该行并保留其余，不让回收整体中断
 * - 使用示例: const users = readTestUserRegistry();
 */
export function readTestUserRegistry(): TestUserRecord[] {
  if (!existsSync(TEST_USER_REGISTRY)) return [];
  const seen = new Set<number>();
  const out: TestUserRecord[] = [];
  for (const line of readFileSync(TEST_USER_REGISTRY, 'utf8').split('\n')) {
    if (!line.trim()) continue;
    try {
      const rec = JSON.parse(line) as TestUserRecord;
      if (!rec.user_id || seen.has(rec.user_id)) continue;
      seen.add(rec.user_id);
      out.push(rec);
    } catch {
      // 半行（进程被强杀时可能截断）忽略即可，回收尽力而为
    }
  }
  return out;
}

/**
 * 删除登记表（回收完成后调用，避免下一轮重复停用上一轮的会员）
 * - 函数名: clearTestUserRegistry
 * - 参数: 无
 * - 返回: void
 * - 异常: force 删除，文件不存在不报错
 * - 使用示例: clearTestUserRegistry();
 */
export function clearTestUserRegistry(): void {
  rmSync(TEST_USER_REGISTRY, { force: true });
}

/** 随机手机号池前缀（测试专用号段） */
const PHONE_PREFIXES = ['139', '150', '176'];
/** 号码种子：秒级时间戳基数 + 进程随机偏移，规避同一秒加载的多个 spec 文件取到同号 */
let phoneSeq = (Math.floor(Date.now() / 1000) + Math.floor(Math.random() * 100_000)) % 100_000_000;

/**
 * 生成唯一的 11 位测试手机号
 * - 函数名: uniquePhone
 * - 参数: 无
 * - 返回: string，如 17679214372（号段 + 8 位自增序号）
 * - 异常: 无；种子含随机偏移，跨文件/跨进程同号概率可忽略；
 *   若仍复用已有用户，建号走幂等不影响登录类断言，但依赖「首次注册」的默认值类断言会失败
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
