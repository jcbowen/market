#!/usr/bin/env node
// deps-tools.mjs
// 启动/构建脚本共用的依赖检查与自愈工具
//
// 设计目标：让 start-all.mjs / deploy.mjs / build.mjs / build-select.mjs 在遇到
// 「依赖不全」时不必人工逐条排查——先捕获命令输出判定错误原因，能自动补齐的
// （Go 模块、npm 依赖、GeoIP 库、Docker 镜像、过期容器状态）补齐后重试一次，
// 补不了的（本地 replace 模块缺失、网络受限、端口被占用）给出明确的下一步指引。
//
// 所有写操作（go mod tidy、npm install、docker compose down）都可由 --no-auto-fix 关闭，
// 关闭后只做检查与提示，不改动 go.mod / go.sum / node_modules / 容器。

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

/** 修复动作标识：与 DIAGNOSTIC_RULES 中的 repair 字段对应 */
const REPAIR = {
  none: null,
  goTidy: 'go-tidy',
  npmInstall: 'npm-install',
  composePull: 'compose-pull',
  composeDown: 'compose-down',
};

/**
 * 错误诊断规则表，按优先级从上到下匹配（首个命中即生效）。
 * 网络类放在最前：网络不通时 go mod tidy 也必然失败，不应反复尝试写操作。
 *
 * @type {Array<{kind: string, title: string, patterns: RegExp[], repair: string|null, hints: string[]}>}
 */
const DIAGNOSTIC_RULES = [
  {
    kind: 'network',
    title: '网络受限，无法获取依赖',
    patterns: [
      /connection refused/i, /no such host/i, /i\/o timeout/i, /TLS handshake timeout/i,
      /dial tcp/i, /Client\.Do\(GET/i, /read: connection aborted/i,
      /ETIMEDOUT|ECONNRESET|ENOTFOUND/i, /Failed to (?:fetch|download)/i,
    ],
    repair: REPAIR.none,
    hints: [
      '确认本机能访问外网或内网代理；Go 模块可切国内代理：go env -w GOPROXY=https://goproxy.cn,direct',
      'npm 可切国内镜像：npm config set registry https://registry.npmmirror.com',
      'Docker 镜像加速参考 deploy/docker-daemon.json.example，配置后重启 Docker',
    ],
  },
  {
    kind: 'toolchain-missing',
    title: '缺少必需的命令行工具链',
    patterns: [/(?:^|[\s/])(?:go|node|npm|npx|docker|git): command not found/i,
      /(?:^|[\s/])(?:go|node|npm|npx|docker)\.: The term .* is not recognized/i],
    repair: REPAIR.none,
    hints: ['脚本不代为安装工具链，版本要求见 docs/需求文档/10-部署方案/01-环境依赖.md'],
  },
  {
    kind: 'go-local-replace',
    title: 'go.mod 的 replace 指向的本地模块目录不存在',
    patterns: [/replacement directory .* does not exist/i,
      /go: [^\n]*: (?:\.\.[/.\\][^\s]*): (?:no such file or directory|cannot find)/i],
    repair: REPAIR.none,
    hints: ['按提示把缺失模块放到期望路径（或修改 go.mod 的 replace 路径）后重试'],
  },
  {
    kind: 'go-module-incomplete',
    title: 'Go 模块依赖不完整或 go.mod/go.sum 不一致',
    patterns: [
      /missing go\.sum entry/i, /updates to go\.mod needed/i, /go\.mod missing .* directive/i,
      /no required module provides package/i, /module lookup disabled/i,
      /cannot find module providing package/i, /missing module(?: provide| in go\.sum)/i,
      /go: [^\n]*module [^\n]*: (?:unknown revision|does not contain)/i,
    ],
    repair: REPAIR.goTidy,
    hints: ['需人工确认时可执行：cd src/backend && go mod tidy'],
  },
  {
    kind: 'npm-missing-module',
    title: '前端依赖缺失（node_modules 不完整或与 package.json 不一致）',
    patterns: [
      /Cannot find module ['"][^.\/][^'"]*['"]/, /ERR_MODULE_NOT_FOUND/, /MODULE_NOT_FOUND/,
      /Module not found/i, /command not found/i, /is not installed/i,
    ],
    repair: REPAIR.npmInstall,
    hints: ['需人工确认时可执行：npm install'],
  },
  {
    kind: 'image-pull',
    title: 'Docker 基础镜像缺失或拉取失败',
    patterns: [/failed to resolve reference/i, /pull access denied/i, /manifest unknown/i,
      /short read/i, /unexpected EOF/i],
    repair: REPAIR.composePull,
    hints: ['私有仓库需先 docker login；公共镜像拉不动时配置 registry mirror 后重试'],
  },
  {
    // Docker Desktop 重启或网络被清理后，残留容器仍引用旧网络 ID，up 会直接失败；
    // down（不带 -v，保留数据卷）重建容器即可恢复
    kind: 'container-state-stale',
    title: '容器状态过期（残留容器引用了已删除的 Docker 网络或容器）',
    patterns: [/failed to set up container networking/i, /network [0-9a-f]{12,} not found/i,
      /Error response from daemon: (?:removal|cannot unlink) of container/i,
      /endpoint .* does not belong to/i],
    repair: REPAIR.composeDown,
    hints: ['脚本会自动 docker compose down（保留数据卷）后重试；需人工确认时可执行：docker compose down && docker compose up -d'],
  },
  {
    kind: 'port-conflict',
    title: '服务端口已被其他进程占用',
    patterns: [/address already in use/i, /Bind for 0\.0\.0\.0:\d+ failed/i,
      /port is already allocated/i],
    repair: REPAIR.none,
    hints: ['查看占用进程：lsof -i :18080（macOS）/ netstat -ano | findstr :18080（Windows）',
      '或修改 docker-compose.yml 中对应服务的 ports 映射后重试'],
  },
];

const UNKNOWN_RULE = {
  kind: 'unknown',
  title: '未能判定具体原因',
  patterns: [],
  repair: REPAIR.none,
  hints: ['请查看上方原始输出定位问题，修复后重跑脚本'],
};

/**
 * 创建工具上下文，供本模块所有函数使用。
 *
 * @param {{ rootDir: string, autoFix?: boolean }} options
 *        rootDir 项目根目录（绝对路径）；autoFix 是否允许自动修复，默认 true
 * @returns {{ rootDir: string, backendDir: string, autoFix: boolean, goEnv: Record<string,string> }}
 * @example const ctx = createContext({ rootDir: ROOT_DIR, autoFix: !noAutoFix });
 */
export function createContext({ rootDir, autoFix = true }) {
  const backendDir = path.join(rootDir, 'src', 'backend');
  return {
    rootDir,
    backendDir,
    autoFix,
    // 与 build.mjs 保持一致：模块缓存放在项目内 pkg/ 下，避免污染全局 GOPATH
    goEnv: { GOPATH: rootDir, CGO_ENABLED: '0' },
  };
}

/**
 * 执行命令，支持三种输出模式。
 *
 * 默认用 tty（stdio: 'inherit'）：Docker Compose / vite / uni 都按 stdout 是否为 TTY
 * 决定输出形态，一旦接 pipe 就退化成逐行打印容器事件与进度，终端里全是重复行。
 * tty 模式拿不到输出文本，所以只在需要判定失败原因时才用 capture 模式跑。
 *
 * @param {string} cmd 可执行文件名
 * @param {string[]} args 参数列表
 * @param {{cwd?: string, env?: Record<string,string>, mode?: 'tty'|'capture'|'quiet'}} [opts]
 *        tty=直通终端（默认，保留原生进度渲染）；capture=实时回显并收集文本；quiet=只收集不回显
 * @returns {Promise<{ok: boolean, status: number|null, output: string}>} output 仅 capture/quiet 模式有内容
 * @example await runChecked('go', ['list', '-m', 'all'], { mode: 'quiet' });
 */
export function runChecked(cmd, args, opts = {}) {
  const mode = opts.mode || 'tty';
  if (mode === 'tty') {
    return new Promise((resolve) => {
      const child = spawn(cmd, args, {
        cwd: opts.cwd || process.cwd(),
        env: opts.env ? { ...process.env, ...opts.env } : process.env,
        stdio: 'inherit',
        shell: process.platform === 'win32',
      });
      child.on('error', (err) => resolve({ ok: false, status: 127, output: String(err.message) }));
      child.on('close', (code) => resolve({ ok: code === 0, status: code, output: '' }));
    });
  }

  const echo = mode === 'capture';
  return new Promise((resolve) => {
    const child = spawn(cmd, args, {
      cwd: opts.cwd || process.cwd(),
      env: opts.env ? { ...process.env, ...opts.env } : process.env,
      shell: process.platform === 'win32',
    });

    let output = '';
    const collect = (chunk) => {
      output += chunk;
      if (echo) process.stdout.write(chunk);
    };
    child.stdout.on('data', collect);
    child.stderr.on('data', collect);
    child.on('error', (err) => resolve({ ok: false, status: 127, output: String(err.message) }));
    child.on('close', (code) => resolve({ ok: code === 0, status: code, output }));
  });
}

/**
 * 依据命令输出判定失败原因。
 *
 * @param {string} output 合并后的 stdout/stderr 文本
 * @returns {typeof UNKNOWN_RULE} 命中的诊断规则；无命中返回 kind='unknown'
 * @example diagnose('go: missing go.sum entry for module...').kind // 'go-module-incomplete'
 */
export function diagnose(output) {
  const text = String(output || '');
  return DIAGNOSTIC_RULES.find((rule) => rule.patterns.some((re) => re.test(text))) || UNKNOWN_RULE;
}

/**
 * 打印诊断结论与建议，然后结束进程（脚本无法继续的场景统一出口）。
 *
 * @param {string} step 失败的步骤名称，用于日志定位
 * @param {string} output 命令原始输出
 * @returns {never} 始终 process.exit(1)
 */
function failWith(step, output) {
  const rule = diagnose(output);
  console.error('');
  console.error(`错误: ${step} 失败 —— ${rule.title}`);
  for (const line of rule.hints) console.error(`  · ${line}`);
  if (output.trim()) {
    console.error('---- 原始输出 ----');
    console.error(output.trim());
    console.error('----------------');
  }
  process.exit(1);
}

/**
 * 执行命令，失败时按诊断结果自动修复并重试一次。
 *
 * 输出策略：正常路径一律 tty 直通，保住 compose / vite / uni 的原生进度渲染；
 * 只有 tty 失败后才用 capture 重跑一次取错误文本做归类（compose 与构建命令都幂等，
 * 重跑无副作用；若重跑反而成功，说明是瞬时故障，直接按成功处理）。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @param {string} cmd 可执行文件名
 * @param {string[]} args 参数列表
 * @param {{cwd?: string, env?: Record<string,string>, step?: string, allowRepair?: boolean}} [opts]
 *        step 步骤名（日志用）；allowRepair=false 时跳过自愈直接失败
 * @returns {Promise<void>} 成功即返回；失败或关闭自动修复时结束进程
 * @throws 不抛异常，统一以 process.exit(1) 结束
 * @example await runWithRepair(ctx, 'npm', ['run', 'build'], { cwd: adminDir, step: '构建管理端' });
 */
export async function runWithRepair(ctx, cmd, args, opts = {}) {
  const step = opts.step || `${cmd} ${args.join(' ')}`;
  const runOpts = { cwd: opts.cwd, env: opts.env };

  const first = await runChecked(cmd, args, runOpts);
  if (first.ok) return;

  // tty 模式拿不到文本，重跑一次只为取错误输出做原因归类
  const detail = await runChecked(cmd, args, { ...runOpts, mode: 'capture' });
  if (detail.ok) {
    console.log(`    ${step} 重试后成功（首次为瞬时故障），继续后续步骤`);
    return;
  }

  const rule = diagnose(detail.output);
  if (!rule.repair || opts.allowRepair === false) failWith(step, detail.output);
  if (!ctx.autoFix) {
    console.error('');
    console.error(`错误: ${step} 失败 —— ${rule.title}`);
    console.error(`  可执行「${rule.repair}」修复，但当前已指定 --no-auto-fix，未自动改动依赖`);
    for (const line of rule.hints) console.error(`  · ${line}`);
    process.exit(1);
  }

  console.log(`==> ${step} 失败原因：${rule.title}，自动执行 ${rule.repair} 后重试`);
  const repaired = await applyRepair(ctx, rule.repair, opts.cwd);
  if (!repaired.ok) failWith(`${rule.repair}（自动修复）`, repaired.output);

  const second = await runChecked(cmd, args, { ...runOpts, mode: 'capture' });
  if (second.ok) return;
  failWith(`${step}（${rule.repair} 重试后）`, second.output);
}

/**
 * 执行具体修复动作。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @param {string} repair 修复标识，取自 REPAIR
 * @param {string} [cwd] npm-install 时的工作目录（前端子项目目录）；compose/go 类修复用固定目录
 * @returns {Promise<{ok: boolean, output: string}>} 修复命令的执行结果
 */
async function applyRepair(ctx, repair, cwd) {
  if (repair === REPAIR.goTidy) {
    return runChecked('go', ['mod', 'tidy'], { cwd: ctx.backendDir, env: ctx.goEnv });
  }
  if (repair === REPAIR.npmInstall) {
    return runChecked('npm', ['install'], { cwd });
  }
  if (repair === REPAIR.composePull) {
    return runChecked('docker', ['compose', 'pull'], { cwd: ctx.rootDir });
  }
  // 只重建容器，不加 -v，MySQL/Redis 数据卷保持不动
  if (repair === REPAIR.composeDown) {
    return runChecked('docker', ['compose', 'down'], { cwd: ctx.rootDir });
  }
  return { ok: false, output: `未知的修复动作标识: ${repair}` };
}

/**
 * 解析 go.mod 中 replace 指向的本地目录，缺失时立即终止。
 *
 * 本地 replace 模块（jcbaseGo、wego）只能由使用者准备，脚本无法代劳，
 * 因此在构建前就报清楚「缺哪个、放哪去」，而不是让 go build 抛一长串堆栈。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @returns {void} 全部就绪则静默返回
 * @throws 结束进程：存在缺失的本地 replace 目录时 exit(1)
 * @example checkLocalReplaces(ctx);
 */
export function checkLocalReplaces(ctx) {
  const modFile = path.join(ctx.backendDir, 'go.mod');
  if (!fs.existsSync(modFile)) {
    console.error(`错误: 未找到 go.mod（${modFile}）`);
    process.exit(1);
  }

  const text = fs.readFileSync(modFile, 'utf8');
  const missing = [];
  for (const line of text.split('\n')) {
    const match = /^\s*replace\s+\S+\s*=>\s*(\.{1,2}\/[^\s]+)\s*$/.exec(line);
    if (!match) continue;
    const target = path.resolve(ctx.backendDir, match[1]);
    if (fs.existsSync(path.join(target, 'go.mod'))) continue;
    missing.push(`  · ${match[1].trim()}（期望路径: ${target}）`);
  }
  if (missing.length === 0) return;

  console.error('错误: go.mod 的 replace 指向的本地模块目录不存在，请先把这些模块放到期望路径：');
  for (const item of missing) console.error(item);
  process.exit(1);
}

/**
 * 构建后端前的 Go 模块预检：校验 go.mod/go.sum 一致性并补齐模块缓存。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @returns {void} 依赖就绪则返回
 * @throws 无法自动修复时结束进程
 * @example await preflightGoModules(ctx); // 在交叉编译之前调用
 */
export async function preflightGoModules(ctx) {
  checkLocalReplaces(ctx);

  // `go list -m all` 只做模块解析，不编译，能提前暴露 go.sum 缺行与 replace 路径问题
  const check = await runChecked('go', ['list', '-m', 'all'], {
    cwd: ctx.backendDir, env: ctx.goEnv, mode: 'quiet',
  });
  if (check.ok) return;

  const rule = diagnose(check.output);
  if (rule.repair !== REPAIR.goTidy) failWith('Go 模块预检', check.output);
  if (!ctx.autoFix) {
    console.error('错误: Go 模块依赖不完整（go.mod/go.sum 需同步），当前已指定 --no-auto-fix，未自动执行 go mod tidy');
    console.error('  可手动执行：cd src/backend && go mod tidy');
    process.exit(1);
  }

  console.log('==> Go 模块依赖不完整，执行 go mod tidy 补齐...');
  const tidy = await runChecked('go', ['mod', 'tidy'], { cwd: ctx.backendDir, env: ctx.goEnv });
  if (!tidy.ok) failWith('go mod tidy', tidy.output);

  const download = await runChecked('go', ['mod', 'download'], { cwd: ctx.backendDir, env: ctx.goEnv });
  if (!download.ok) failWith('go mod download', download.output);

  const recheck = await runChecked('go', ['list', '-m', 'all'], {
    cwd: ctx.backendDir, env: ctx.goEnv, mode: 'quiet',
  });
  if (!recheck.ok) failWith('Go 模块预检（go mod tidy 后）', recheck.output);
  console.log('    Go 模块依赖已就绪');
}

/**
 * 生成目录的展示名：项目内用相对路径，项目外用绝对路径。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @param {string} dir 目标目录
 * @returns {string} 用于日志的目录标识
 */
function dirLabel(ctx, dir) {
  const rel = path.relative(ctx.rootDir, dir);
  return rel.startsWith('..') ? dir : rel;
}

/**
 * 安装前端依赖（缺失或与 package.json 不一致时）。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @param {string} dir 前端子项目目录（含 package.json）
 * @returns {Promise<void>} 依赖已满足则静默返回
 * @throws npm install 失败时结束进程
 * @example await ensureNpmDependencies(ctx, path.join(ROOT_DIR, 'src', 'admin'));
 */
export async function ensureNpmDependencies(ctx, dir) {
  const pkg = path.join(dir, 'package.json');
  if (!fs.existsSync(pkg)) {
    console.error(`错误: 未找到 package.json（${pkg}）`);
    process.exit(1);
  }

  const reason = await missingNpmReason(dir);
  if (!reason) return;
  const label = dirLabel(ctx, dir);

  if (!ctx.autoFix) {
    console.error(`错误: ${label} ${reason}，当前已指定 --no-auto-fix，未自动安装依赖`);
    console.error(`  可手动执行：cd ${label} && npm install`);
    process.exit(1);
  }

  console.log(`==> ${label} ${reason}，正在安装依赖...`);
  const install = await runChecked('npm', ['install'], { cwd: dir });
  if (!install.ok) failWith(`npm install（${label}）`, install.output);
}

/**
 * 判断 node_modules 是否处于不可用状态。
 *
 * 不用 mtime 判断「lock 是否比 node_modules 新」：git checkout 会刷新
 * package-lock.json 的时间戳，导致依赖其实完好也触发无意义的 npm install。
 * 这里只信两个可靠信号——目录不存在，以及 npm ls 报出的明确缺失。
 *
 * @param {string} dir 前端子项目目录
 * @returns {Promise<string|null>} 需要安装时返回原因描述，无需安装返回 null
 * @example await missingNpmReason(path.join(ROOT_DIR, 'src', 'client')) // null 表示依赖可用
 */
async function missingNpmReason(dir) {
  const modulesDir = path.join(dir, 'node_modules');
  if (!fs.existsSync(modulesDir)) return '缺少 node_modules';

  // 只认明确的缺失/损坏标记，peer 依赖之类的警告不应触发重装
  const ls = await runChecked('npm', ['ls', '--depth=0'], { cwd: dir, mode: 'quiet' });
  if (/(^|\s)missing: |UNMET DEPENDENCY|ENOTEMPTY/.test(ls.output)) {
    return 'node_modules 与 package.json 不一致';
  }
  return null;
}

/**
 * 确保 GeoIP 定位库存在（缺失时调用 download-geoip.mjs 下载）。
 *
 * 定位库属于可选运行数据：缺失只影响 IP 定位，不该阻塞整体启动流程，
 * 因此这里只告警不终止，网关侧对缺失场景本身有兜底。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @returns {Promise<void>} 始终返回，不结束进程
 * @example await ensureGeoipDatabase(ctx);
 */
export async function ensureGeoipDatabase(ctx) {
  const dir = path.join(ctx.rootDir, 'bin', 'data', 'geoip');
  const hasDb = fs.existsSync(dir) && fs.readdirSync(dir).some((name) => name.endsWith('.mmdb'));
  if (hasDb) return;

  if (!ctx.autoFix) {
    console.warn('警告: 缺少 GeoIP 定位库（bin/data/geoip/*.mmdb），已指定 --no-auto-fix，IP 定位功能将降级');
    console.warn('  可手动执行：node download-geoip.mjs');
    return;
  }

  console.log('==> 缺少 GeoIP 定位库，正在下载...');
  const r = await runChecked(process.execPath, [path.join(ctx.rootDir, 'download-geoip.mjs')], {
    cwd: ctx.rootDir,
  });
  if (!r.ok) {
    console.warn('警告: GeoIP 定位库下载失败，IP 定位功能将降级（可稍后手动执行 node download-geoip.mjs）');
    return;
  }
  console.log('    GeoIP 定位库已就绪');
}

/**
 * 构建 Docker 镜像，镜像拉取失败时 pull 后重试一次。
 *
 * @param {ReturnType<createContext>} ctx 工具上下文
 * @param {string[]} [extraArgs] 追加参数（如 --no-cache）
 * @returns {Promise<void>} 成功返回；失败时结束进程
 * @example await buildComposeImages(ctx, NO_CACHE ? ['--no-cache'] : []);
 */
export async function buildComposeImages(ctx, extraArgs = []) {
  await runWithRepair(ctx, 'docker', ['compose', 'build', ...extraArgs], {
    cwd: ctx.rootDir,
    step: '构建 Docker 镜像',
  });
}

/**
 * 解析 --no-auto-fix 开关（各入口脚本共用）。
 *
 * @param {string[]} argv 命令行参数（process.argv.slice(2)）
 * @returns {boolean} true 表示允许自动修复
 * @example const autoFix = parseAutoFix(process.argv.slice(2));
 */
export function parseAutoFix(argv) {
  return !argv.includes('--no-auto-fix');
}
