import { test, expect } from '@playwright/test';
import { execSync } from 'node:child_process';

/**
 * 模块 86：market-admin 演示种子补导（init --rerun）
 * 覆盖（2026-10-29 随「删库重建」口径与 init 可见性改造补齐）：
 *  1. `init --rerun` 逐条幂等复跑全部演示段：退出码 0 且失败条目为 0；
 *  2. 连续两次 `--rerun` 第二次「新建 0」——证明补导不会重复插行（ReRunDemoSections 幂等键生效）；
 *  3. 普通 `init` 在已初始化库上走空库短路，输出必须指向 `--rerun`
 *     （旧文案写的是「可重新运行 init 幂等补导」，与实际行为不符，已被本用例锁死）。
 * 依赖本地 docker 容器 market-gateway（内嵌 market-admin，口令经容器环境变量继承，用例不触碰凭据）；
 * 容器不可用时整组 skip，不视为失败。
 */

const GATEWAY = 'market-gateway';

/**
 * 在网关容器内执行 market-admin
 * - 函数名: runMarketAdmin
 * - 参数: args 子命令与参数数组（如 ['init', '--rerun']）
 * - 返回: { code, output } —— code 为退出码，output 为 stdout+stderr 合并文本
 * - 异常: 不抛出；docker 不可用返回 { code: -1, output: '' }，由调用方 skip
 * - 使用示例: const r = runMarketAdmin(['init', '--rerun', '--timeout', '60']);
 */
function runMarketAdmin(args: string[]): { code: number; output: string } {
  const cmd = `docker exec ${GATEWAY} ./market-admin ${args.join(' ')}`;
  try {
    const stdout = execSync(`${cmd} 2>&1`, { encoding: 'utf8', stdio: 'pipe', maxBuffer: 64 * 1024 * 1024 });
    return { code: 0, output: stdout };
  } catch (e) {
    const err = e as { status?: number; stdout?: string; stderr?: string; message?: string };
    // 容器/命令本身不存在（未起栈）：status 为空，调用方据此 skip
    if (err.status === undefined || err.status === null) return { code: -1, output: '' };
    return { code: err.status, output: `${err.stdout ?? ''}${err.stderr ?? ''}` };
  }
}

/** 网关容器与 market-admin 是否可用（不可用则整组 skip） */
function adminAvailable(): boolean {
  try {
    execSync(`docker exec ${GATEWAY} ./market-admin --help 2>&1`, { stdio: 'pipe' });
    return true;
  } catch (e) {
    // 用法错误（退出码 2）说明二进制在、只是不接受 --help：仍视为可用
    const err = e as { code?: number; stdout?: string };
    return err.code === 2 || (err.stdout ?? '').includes('用法');
  }
}

const available = adminAvailable();

test.describe('演示种子补导（init --rerun）', () => {
  test.skip(!available, '本地 market-gateway 容器不可用，跳过补导用例');
  // 单次复跑要逐条判重（仅区划段就 3209 条），默认 60s 不够；
  // 逐条日志量大，合并输出按 64MB 缓冲，避免 execSync 因 maxBuffer 截断误报失败
  test.setTimeout(300_000);

  test('幂等复跑无失败条目', () => {
    const r = runMarketAdmin(['init', '--rerun', '--timeout', '60']);
    expect(r.code, `补导退出码非 0，输出：\n${r.output}`).toBe(0);
    expect(r.output).toContain('演示种子补导完成');
    // 失败条目必须为 0：形如「新建 0 / 跳过 123 / 失败 0」
    expect(r.output).toMatch(/补导完成：新建 \d+ \/ 跳过 \d+ \/ 失败 0/);
  });

  test('连续两次复跑第二次不新建（幂等键生效）', () => {
    const first = runMarketAdmin(['init', '--rerun', '--timeout', '60']);
    expect(first.code, `第一次补导失败，输出：\n${first.output}`).toBe(0);
    const second = runMarketAdmin(['init', '--rerun', '--timeout', '60']);
    expect(second.code, `第二次补导失败，输出：\n${second.output}`).toBe(0);
    const m = second.output.match(/补导完成：新建 (\d+) \/ 跳过 (\d+) \/ 失败 (\d+)/);
    expect(m, `未解析到补导计数，输出尾部：\n${second.output.slice(-500)}`).not.toBeNull();
    expect(Number(m![1]), '第二次补导仍新建条目，说明幂等判重失效').toBe(0);
    expect(Number(m![3])).toBe(0);
  });

  test('普通 init 在已初始化库上指向 --rerun', () => {
    const r = runMarketAdmin(['init', '--timeout', '60']);
    // 已有社区 → 空库短路（视为已初始化）；此路径退出码应为 0（正常跳过，非失败）
    expect(r.code, `init 退出码非 0，输出：\n${r.output}`).toBe(0);
    expect(r.output).toContain('视为已初始化');
    expect(r.output, '旧文案「可重新运行 init 幂等补导」与实际行为不符，须指向 --rerun').toContain('--rerun');
  });
});
