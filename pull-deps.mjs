#!/usr/bin/env node
// pull-deps.mjs
// 一键拉取本地依赖仓库的最新代码（Windows / macOS 通用）
//
// 使用方法：
//   node pull-deps.mjs
//   或在 VS Code 中运行任务「pull-deps（拉取本地依赖最新代码）」
//
// 依赖清单不写死：直接解析 src/backend/go.mod 中 replace 指向本地目录的条目，
// 以后新增本地 replace 无需改本脚本。逐个仓库执行 git pull --ff-only：
//   - 目录缺失 / 不是 git 仓库 → 记为失败（项目必然构建不过）
//   - 分离头指针 / 当前分支无上游 / 工作区有未提交改动 → 跳过并说明原因
//   - 本地与远端已分叉 → pull --ff-only 失败，保留现场，不自动 merge / stash
// 单个仓库失败不影响其余仓库，全部处理完后再汇总，有失败时以退出码 1 结束；
// 每个仓库额外输出最新 tag、提交摘要与提交时间，便于换机器后快速确认依赖版本。

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runChecked } from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const BACKEND_DIR = path.join(ROOT_DIR, 'src', 'backend');

/**
 * 解析 go.mod 中 replace 到本地相对目录的模块清单。
 * 只识别单行 `replace 模块 => 相对路径` 写法，与 deps-tools.mjs 的 checkLocalReplaces 保持一致；
 * replace 到模块版本（非本地目录）与 replace 块写法不在此处处理。
 *
 * @param {string} modFile go.mod 的绝对路径
 * @returns {Array<{module: string, raw: string, dir: string}>} module 模块名；raw 为 go.mod 里的原始写法；dir 为解析后的绝对路径
 * @throws 不主动抛异常，go.mod 不存在由调用方提前校验
 * @example collectLocalReplaces(path.join(BACKEND_DIR, 'go.mod'))
 *          // [{ module: 'github.com/jcbowen/wego', raw: '../../../wego', dir: 'C:\\...\\mine\\weGo' }]
 */
function collectLocalReplaces(modFile) {
  const text = fs.readFileSync(modFile, 'utf8');
  const entries = [];
  for (const line of text.split('\n')) {
    const match = /^\s*replace\s+(\S+)\s*=>\s*(\.{1,2}\/[^\s]+)\s*$/.exec(line);
    if (!match) continue;
    entries.push({ module: match[1], raw: match[2], dir: path.resolve(BACKEND_DIR, match[2]) });
  }
  return entries;
}

/**
 * 在指定仓库目录执行 git 命令并收集输出。
 * 关闭交互式凭证输入（GIT_TERMINAL_PROMPT=0）：任务在 VS Code 面板里跑，
 * 一旦弹出账号密码提示就会一直挂住且看不见输入框，不如直接失败并给出原始报错。
 *
 * @param {string} cwd 仓库目录（绝对路径）
 * @param {string[]} args git 子命令及参数
 * @returns {Promise<{ok: boolean, output: string}>} ok 为退出码是否 0；output 为 stdout/stderr 合并文本（已 trim）
 * @throws 不抛异常，spawn 失败按 ok=false 返回
 * @example await git('/path/to/wego', ['rev-parse', 'HEAD'])
 */
async function git(cwd, args) {
  const result = await runChecked('git', args, {
    cwd,
    mode: 'quiet',
    env: { GIT_TERMINAL_PROMPT: '0' },
  });
  return { ok: result.ok, output: result.output.trim() };
}

/**
 * 拉取单个依赖仓库的最新代码。
 * 不做任何破坏性操作：不 stash、不 reset、不 merge；工作区不干净时直接跳过。
 *
 * @param {{module: string, raw: string, dir: string}} dep 依赖仓库信息（collectLocalReplaces 的元素）
 * @returns {Promise<{name: string, status: 'updated'|'current'|'skipped'|'failed', message: string, detail?: string}>}
 *          name 为用于展示的模块短名；status 供汇总统计；message 为一行结论；detail 为失败时的原始 git 输出
 * @throws 不抛异常
 * @example
 *   const r = await updateRepo({ module: 'github.com/jcbowen/wego', raw: '../../../wego', dir: '...' });
 *   if (r.status === 'failed') console.error(r.detail);
 */
async function updateRepo(dep) {
  const name = dep.module.split('/').pop();

  if (!fs.existsSync(path.join(dep.dir, '.git'))) {
    return {
      name,
      status: 'failed',
      message: `目录不存在或不是 git 仓库（期望路径：${dep.dir}）`,
    };
  }

  const branch = await git(dep.dir, ['rev-parse', '--abbrev-ref', 'HEAD']);
  if (!branch.ok) return { name, status: 'failed', message: '无法读取当前分支', detail: branch.output };
  if (branch.output === 'HEAD') {
    return { name, status: 'skipped', message: '处于分离头指针状态，切回分支后重跑' };
  }

  const upstream = await git(dep.dir, ['rev-parse', '--abbrev-ref', '--symbolic-full-name', '@{u}']);
  if (!upstream.ok) {
    return {
      name,
      status: 'skipped',
      message: `分支 ${branch.output} 未设置上游，已跳过（可先执行 git branch --set-upstream-to=origin/${branch.output}）`,
    };
  }

  const dirty = await git(dep.dir, ['status', '--porcelain']);
  if (!dirty.ok) return { name, status: 'failed', message: '无法读取工作区状态', detail: dirty.output };
  if (dirty.output) {
    const count = dirty.output.split('\n').filter(Boolean).length;
    return {
      name,
      status: 'skipped',
      message: `有 ${count} 处未提交改动，已跳过（提交或暂存后重跑）`,
    };
  }

  const before = await git(dep.dir, ['rev-parse', 'HEAD']);
  if (!before.ok) return { name, status: 'failed', message: '无法读取 HEAD 提交', detail: before.output };

  const pull = await git(dep.dir, ['pull', '--ff-only']);
  if (!pull.ok) {
    return {
      name,
      status: 'failed',
      message: `git pull --ff-only 失败（本地与 ${upstream.output} 已分叉或远端被改写，需人工处理）`,
      detail: pull.output,
    };
  }

  const after = await git(dep.dir, ['rev-parse', 'HEAD']);
  if (!after.ok) return { name, status: 'failed', message: '拉取后无法读取 HEAD 提交', detail: after.output };
  if (after.output === before.output) {
    return { name, status: 'current', message: `${branch.output} 与 ${upstream.output} 一致` };
  }

  const count = await git(dep.dir, ['rev-list', '--count', `${before.output}..${after.output}`]);
  const commits = count.ok && count.output ? `，${count.output} 个提交` : '';
  return {
    name,
    status: 'updated',
    message: `${branch.output}：${before.output.slice(0, 7)} → ${after.output.slice(0, 7)}${commits}`,
  };
}

const STATUS_TEXT = {
  updated: '已更新',
  current: '已是最新',
  skipped: '跳过',
  failed: '失败',
};

/**
 * 读取仓库 HEAD 的 tag、提交摘要与提交时间，用于在结果里展示版本信息。
 * tag 取离 HEAD 最近的标签；HEAD 已越过该标签时补上落后的提交数，避免把旧 tag 误当成当前版本。
 *
 * @param {string} dir 仓库目录（绝对路径）
 * @returns {Promise<{tag: string, subject: string, time: string}|null>} 非 git 仓库或读取失败返回 null
 * @throws 不抛异常
 * @example await describeHead('/path/to/wego')
 *          // { tag: 'v0.2.0（+2 个提交）', subject: 'chore: ...', time: '2026-10-25 10:00:00 +0800' }
 */
async function describeHead(dir) {
  if (!fs.existsSync(path.join(dir, '.git'))) return null;

  // 用 NUL 分隔提交摘要与时间：摘要本身可能含空格、冒号等，用普通分隔符会切错
  const log = await git(dir, ['log', '-1', '--format=%s%x00%ci']);
  if (!log.ok || !log.output) return null;
  const [subject, time] = log.output.split('\x00');

  const tag = await git(dir, ['describe', '--tags', '--abbrev=0']);
  let tagText = '无';
  if (tag.ok && tag.output) {
    tagText = tag.output;
    const ahead = await git(dir, ['rev-list', '--count', `${tag.output}..HEAD`]);
    if (ahead.ok && ahead.output && ahead.output !== '0') tagText += `（+${ahead.output} 个提交）`;
  }

  return { tag: tagText, subject, time };
}

/**
 * 入口：读取 go.mod 的本地 replace 清单 → 逐个拉取 → 打印结果与汇总。
 *
 * @returns {Promise<void>} 处理完全部仓库后结束；存在失败仓库时 process.exitCode 置 1
 * @throws 不抛异常，统一转成错误日志
 * @example node pull-deps.mjs
 */
async function main() {
  const modFile = path.join(BACKEND_DIR, 'go.mod');
  if (!fs.existsSync(modFile)) {
    console.error(`错误: 未找到 go.mod（${modFile}）`);
    process.exitCode = 1;
    return;
  }

  const deps = collectLocalReplaces(modFile);
  if (deps.length === 0) {
    console.log('go.mod 没有指向本地目录的 replace，无需更新');
    return;
  }

  console.log(`==> 拉取本地依赖最新代码（${deps.length} 个，各仓库执行 git pull --ff-only）`);
  const results = [];
  for (const dep of deps) {
    const result = await updateRepo(dep);
    console.log(`  [${STATUS_TEXT[result.status]}] ${result.name}（${dep.raw}）：${result.message}`);

    // 跳过（脏工作区 / 分离头指针 / 无上游）时没有与远端核对，标注为本地状态以免误读成远端最新
    const info = await describeHead(dep.dir);
    if (info) {
      const local = result.status === 'skipped' ? '本地' : '最新';
      console.log(`      ${local} tag：${info.tag}`);
      console.log(`      ${local}提交：${info.subject}`);
      console.log(`      提交时间：${info.time}`);
    }
    if (result.detail) {
      for (const line of result.detail.split('\n')) console.error(`      ${line}`);
    }
    results.push(result);
  }

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
