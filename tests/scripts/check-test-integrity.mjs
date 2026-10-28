#!/usr/bin/env node
/**
 * 测试完整性检查（B3：消除假绿）
 *
 * 背景：本套件里有大量 `test.skip(!sku, '环境缺 SKU')` 式的前置跳过。跳过本身合理，
 * 但如果没人统计，「一条都没真跑」和「全跑通」在 list 报告里都表现为没有失败，
 * 这就是假绿。本脚本读取 Playwright JSON 报告，把 skip 按原因分组量化，
 * 超过预算即以非 0 退出，强制每次验收都回答「这些用例到底跑没跑」。
 *
 * 分类口径（每条 skip 只归一类）：
 *   - `[前置]`：造数/环境不满足，计入 `--max` 预算（假绿风险项）；
 *   - `[凭据]`：必须有第三方渠道凭据才能跑，逐条登记在 CREDENTIAL_GATED_CASES，不计预算但要打印；
 *   - `[其他]`：条件型跳过（例如反向用例不适用），不计预算。
 * 状态口径：每个用例只认最后一次尝试的结果（与测试运行器一致，重试后通过算通过），
 * 但「首次尝试未通过、重试后才通过」的抖动用例会单独点名告警——重试已收敛为 1，
 * 出现抖动就是有真实偶发问题；串行分组整组重跑不计入抖动（判据见 flaky 过滤处注释）。
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

/**
 * 第三方凭据依赖用例白名单（key = `${file} :: ${title}`）。
 * 这类分支必须有真实微信/支付宝渠道配置才跑得起来（本地测试服只有现金渠道），
 * 造数失败改显式失败同样无意义——它们不计入造数预算，但必须在此逐条显式登记，
 * 新增一条要走评审（否则预算门形同虚设）。上线部署环境配好渠道后应把它们移出白名单实测。
 */
const CREDENTIAL_GATED_CASES = new Set([
  'api/29-payment-idempotent.spec.ts :: （条件）待支付单重复发起同渠道支付复用同一支付单号',
  'api/80-bind-order-user.spec.ts :: 4. 待支付单拒绝绑定（10004，应走「继续收款」时选会员）',
]);

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
 * - 返回: { file, title, status, reason, attempts }[] —— status 取该 spec 最后一次运行的状态
 */
function collect(suites, file = '') {
  const out = [];
  for (const suite of suites ?? []) {
    const fileName = suite.file ?? file;
    for (const spec of suite.specs ?? []) {
      for (const testCase of spec.tests ?? []) {
        const results = testCase.results ?? [];
        // 只认最后一次尝试：重试成功即视为通过（与 Playwright 自身口径一致，抖动算通过）。
        // 逐次统计会把首次尝试的失败与串行续跑的跳过算成真实结果，
        // 一条抖动就虚报「1 失败 + 若干跳过」，与报告统计自相矛盾
        const last = results[results.length - 1];
        if (!last) continue;
        // 此前是否有尝试未通过：serial 分组里一条失败会让整组重跑，
        // 同组其余用例的两次尝试都是 passed，它们不是 flaky，不该被点名
        const hadUnpassedAttempt = results.slice(0, -1).some((r) => r.status !== 'passed');
        out.push({
          file: fileName,
          title: spec.title,
          status: last.status,
          reason: reasonOf(last),
          attempts: results.length,
          hadUnpassedAttempt,
        });
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

/**
 * skip 分类标签
 * - 函数名: labelOf
 * - 参数: c — {file,title} 用例定位；reason — skip 原因文案
 * - 返回: string，'[前置]'（造数/环境不满足，计入预算）/ '[凭据]'（第三方渠道凭据依赖，白名单登记）/ '[其他]'
 * - 异常: 无
 * - 使用示例: const label = labelOf({file, title}, '环境无可领优惠券');
 */
function labelOf(c, reason) {
  if (CREDENTIAL_GATED_CASES.has(`${c.file} :: ${c.title}`)) return '[凭据]';
  return FIXTURE_GAP_PATTERN.test(reason || '') ? '[前置]' : '[其他]';
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

  /** 用例唯一键：报告里的 file + title，与 CREDENTIAL_GATED_CASES 对齐 */
  const caseKey = (c) => `${c.file} :: ${c.title}`;
  const labeled = skipped.map((c) => ({ ...c, label: labelOf(c, c.reason) }));
  const credCount = labeled.filter((c) => c.label === '[凭据]').length;
  const gapCount = labeled.filter((c) => c.label === '[前置]').length;

  const groups = new Map();
  for (const s of labeled) {
    const key = s.reason || '（无原因）';
    const g = groups.get(key) ?? { count: 0, label: s.label, samples: [] };
    // 同一原因文案可能同时覆盖凭据依赖与造数缺失两种用例：一律按更严的 '[前置]' 归类，避免漏计预算
    if (s.label === '[前置]') g.label = '[前置]';
    g.count++;
    if (g.samples.length < 3) g.samples.push(caseKey(s));
    groups.set(key, g);
  }

  console.log(
    `[check-test-integrity] 用例 ${cases.length}：通过 ${passed.length}，失败 ${failed.length}，跳过 ${skipped.length}` +
      `（其中前置/造数不满足 ${gapCount}，第三方凭据依赖 ${credCount}）；` +
      `报告 stats: expected=${stats.expected ?? '-'} skipped=${stats.skipped ?? '-'}`,
  );

  if (groups.size > 0) {
    console.log('[check-test-integrity] 跳过明细（按原因聚合）：');
    for (const [reason, g] of [...groups.entries()].sort((a, b) => b[1].count - a[1].count)) {
      console.log(`  ${g.label} ${g.count} 条 · ${reason}`);
      for (const sample of g.samples) console.log(`        ${sample}`);
    }
  }
  if (credCount > 0) {
    console.log(
      `[check-test-integrity] 提示：${credCount} 条为第三方渠道凭据依赖（已在脚本白名单登记），` +
        '不计入造数预算，但本轮未产生断言价值；部署环境配好微信/支付宝渠道后应移出白名单实测。',
    );
  }

  // 抖动（首次尝试未通过、重试后才通过）不判失败，但必须点名：本套件重试已收敛为 1，
  // 出现抖动说明有真实的偶发问题（造数撞号、时序依赖、限流），不能让它躲在「绿色」里。
  // 判据用「此前有未通过的尝试」而不是「尝试数大于 1」——串行分组里一条失败会整组重跑，
  // 同组其余用例两次尝试都通过，把它们也算抖动会把 1 条报成 11 条，掩盖真实规模
  const flaky = cases.filter((c) => c.attempts > 1 && c.status === 'passed' && c.hadUnpassedAttempt);
  if (flaky.length > 0) {
    console.warn(`[check-test-integrity] 告警：${flaky.length} 条用例重试后才通过（抖动），请查根因：`);
    for (const f of flaky) console.warn(`        ${f.file} :: ${f.title}（尝试 ${f.attempts} 次）`);
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
