#!/usr/bin/env node
/**
 * 测试完整性检查（B3：消除假绿）
 *
 * 背景：本套件里有大量 `test.skip(!sku, '环境缺 SKU')` 式的前置跳过。跳过本身合理，
 * 但如果没人统计，「一条都没真跑」和「全跑通」在 list 报告里都表现为没有失败，
 * 这就是假绿。本脚本读取 Playwright JSON 报告，把 skip 按原因分组量化，
 * 超过预算即以非 0 退出，强制每次验收都回答「这些用例到底跑没跑」。
 *
 * - 函数名: main
 * - 参数: argv — `--report <path>`（默认 test-results/results.json）、
 *         `--max <n>`（默认 0：验收口径不允许任何 skip）、
 *         `--budget <n>`（本地开发放宽阈值，与 --max 同义，取后出现者）
 * - 返回: 无（通过 process.exit 表达结果：0 通过，1 超预算或报告不可用）
 * - 异常: 不抛出；报告缺失/JSON 损坏一律按「无法证明用例跑过」处理，直接失败
 * - 使用示例: npx playwright test api/ && node scripts/check-test-integrity.mjs --max 0
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

/** skip 原因归类：命中即视为「前置/造数不满足」，属于需要人看的假绿风险项 */
const FIXTURE_GAP_PATTERN = /造数|环境|未创建|未就绪|未定位|未建立|未配置|无库存|无在售|不满足|缺 ?SKU|缺 ?地址/;

function parseArgs(argv) {
  const opts = { report: 'test-results/results.json', max: 0 };
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i];
    if (key === '--report') opts.report = argv[++i];
    else if (key === '--max' || key === '--budget') opts.max = Number(argv[++i]);
    else if (key.startsWith('--report=')) opts.report = key.slice('--report='.length);
    else if (key.startsWith('--max=')) opts.max = Number(key.slice('--max='.length));
    else if (key.startsWith('--budget=')) opts.max = Number(key.slice('--budget='.length));
  }
  if (!Number.isFinite(opts.max)) opts.max = 0;
  return opts;
}

/**
 * 递归收集 spec 用例结果
 * - 返回: { file, title, status, reason }[] —— status 取该 spec 最后一次运行的状态
 */
function collect(suites, file = '') {
  const out = [];
  for (const suite of suites ?? []) {
    const fileName = suite.file ?? file;
    for (const spec of suite.specs ?? []) {
      for (const testCase of spec.tests ?? []) {
        for (const result of testCase.results ?? []) {
          out.push({
            file: fileName,
            title: spec.title,
            status: result.status,
            reason: reasonOf(result),
          });
        }
      }
    }
    out.push(...collect(suite.suites, fileName));
  }
  return out;
}

/** 取 skip 原因：优先 annotations 里的 skip 描述，退化到 error.message */
function reasonOf(result) {
  const annotations = result.annotations ?? [];
  const hit = annotations.find((a) => a.type === 'skip' && a.description);
  if (hit) return hit.description;
  return result.error?.message ?? '';
}

function main() {
  const opts = parseArgs(process.argv.slice(2));
  const reportPath = resolve(process.cwd(), opts.report);

  let report;
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch (e) {
    console.error(`[check-test-integrity] 读取报告失败：${e.message}`);
    console.error('[check-test-integrity] 报告缺失或损坏时无法证明用例真的跑过，按失败处理。');
    console.error(`[check-test-integrity] 生成方式：npx playwright test api/ --config=playwright.config.ts（config 已内置 json reporter 输出 ${opts.report}）`);
    process.exit(1);
  }

  const cases = collect(report.suites);
  const stats = report.stats ?? {};
  const skipped = cases.filter((c) => c.status === 'skipped');
  const failed = cases.filter((c) => c.status === 'failed' || c.status === 'timedOut' || c.status === 'interrupted');
  const passed = cases.filter((c) => c.status === 'passed');

  const groups = new Map();
  for (const s of skipped) {
    const key = s.reason || '（无原因）';
    const g = groups.get(key) ?? { count: 0, fixtureGap: FIXTURE_GAP_PATTERN.test(key), samples: [] };
    g.count++;
    if (g.samples.length < 3) g.samples.push(`${s.file} :: ${s.title}`);
    groups.set(key, g);
  }

  const gapEntries = [...groups.entries()].filter(([, g]) => g.fixtureGap);
  const gapCount = gapEntries.reduce((sum, [, g]) => sum + g.count, 0);

  console.log(
    `[check-test-integrity] 用例 ${cases.length}：通过 ${passed.length}，失败 ${failed.length}，跳过 ${skipped.length}` +
      `（其中前置/造数不满足 ${gapCount}）；报告 stats: expected=${stats.expected ?? '-'} skipped=${stats.skipped ?? '-'}`,
  );

  if (groups.size > 0) {
    console.log('[check-test-integrity] 跳过明细（按原因聚合）：');
    for (const [reason, g] of [...groups.entries()].sort((a, b) => b[1].count - a[1].count)) {
      console.log(`  ${g.fixtureGap ? '[前置]' : '[其他]'} ${g.count} 条 · ${reason}`);
      for (const sample of g.samples) console.log(`        ${sample}`);
    }
  }

  if (failed.length > 0) {
    console.error(`[check-test-integrity] 存在 ${failed.length} 条失败用例，测试本身已不通过。`);
    process.exit(1);
  }
  if (gapCount > opts.max) {
    console.error(
      `[check-test-integrity] 前置/造数类跳过 ${gapCount} 条，超出预算 ${opts.max}：` +
        '这些用例没有产生任何断言价值，链路坏了也会显示绿。请修复环境后重跑，或在用例里把造数失败改为显式失败。',
    );
    process.exit(1);
  }
  console.log('[check-test-integrity] 通过。');
}

main();
