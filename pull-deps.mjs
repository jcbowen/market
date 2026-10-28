#!/usr/bin/env node
// pull-deps.mjs
// 一键拉取本地依赖仓库的最新代码（Windows / macOS 通用）
//
// 使用方法：
//   node pull-deps.mjs
//   或在 VS Code 中运行任务「pull-deps（拉取本地依赖最新代码）」
//
// 依赖清单不写死：直接解析 src/backend/go.mod 中 replace 指向本地目录的条目（解析与
// 拉取逻辑统一在 deps-tools.mjs，与 checkLocalReplaces / --pull-deps 共用同一实现）。
// 逐个仓库先执行 git fetch --tags --force 补齐远端 tag，再执行 git pull --ff-only：
//   - 目录缺失 / 不是 git 仓库 → 记为失败（项目必然构建不过）
//   - 分离头指针 / 当前分支无上游 / 工作区有未提交改动 → 跳过并说明原因
//   - 本地与远端已分叉 → pull --ff-only 失败，保留现场，不自动 merge / stash
// 单个仓库失败不影响其余仓库，全部处理完后再汇总，有失败时以退出码 1 结束；
// 每个仓库额外输出最新 tag、提交摘要与提交时间，便于换机器后快速确认依赖版本。

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createContext, describeHead, pullLocalReplaces } from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

/**
 * 入口：调用 deps-tools 统一拉取流程 → 逐仓库补充展示 HEAD 版本信息 → 打印汇总。
 *
 * @returns {Promise<void>} 处理完全部仓库后结束；存在失败仓库时 process.exitCode 置 1
 * @throws 不抛异常，统一转成错误日志
 * @example node pull-deps.mjs
 */
async function main() {
  const ctx = createContext({ rootDir: ROOT_DIR, autoFix: true });
  const { results } = await pullLocalReplaces(ctx, async (result) => {
    const info = await describeHead(result.dir);
    if (!info) return;
    // 跳过（脏工作区 / 分离头指针 / 无上游）时没有与远端核对，标注为本地状态以免误读成远端最新
    const local = result.status === 'skipped' ? '本地' : '最新';
    console.log(`      ${local} tag：${info.tag}`);
    console.log(`      ${local}提交：${info.subject}`);
    console.log(`      提交时间：${info.time}`);
  });
  if (results.length === 0) return;

  const countOf = (status) => results.filter((r) => r.status === status).length;
  console.log('');
  console.log(
    `汇总：已更新 ${countOf('updated')} / 已是最新 ${countOf('current')} / ` +
      `跳过 ${countOf('skipped')} / 失败 ${countOf('failed')}`,
  );
  if (countOf('failed') > 0) {
    console.error('  失败项请按上方原始输出定位（网络或凭证问题可先手动进入该仓库执行 git pull 验证）');
    process.exitCode = 1;
  }
}

await main();
