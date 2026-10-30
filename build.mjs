#!/usr/bin/env node
// 社区商超后端构建脚本（多平台交叉编译）
//
// 1. 交叉编译 gateway、各微服务与 market-admin 管理工具，
//    按平台组合存放到 bin/{os}-{arch}/ 二级目录（与 CI Release 打包布局一致）：
//      bin/linux-amd64/  bin/linux-arm64/    Linux
//      bin/darwin-amd64/ bin/darwin-arm64/   macOS
//      bin/windows-amd64/ bin/windows-arm64/ Windows（.exe 后缀）
// 2. 编译完成后，自动将与本机环境一致的二进制复制一份到 bin/ 根目录，便于直接运行。
//
// 平台与架构在下方 PLATFORMS 列表中维护；指定 --fast 时仅编译当前宿主架构
// 对应的 Linux 平台（容器实际运行架构），无需显式传递平台参数。
//
// 依赖不全（go.sum 缺行、模块未下载）会在编译前预检并自动 go mod tidy 修复，
// 指定 --no-auto-fix 时只检查提示、不改动 go.mod / go.sum。
// 指定 --pull-deps 时编译前先把 go.mod 本地 replace 指向的依赖仓库
// （如 wego）拉取到最新（git fetch --tags --force + pull --ff-only，
// 脏工作区等状态跳过不阻断）；与 --no-auto-fix 互斥（两者都会改动本地文件，语义冲突）。
//
// 使用方法：node build.mjs [--fast] [--no-auto-fix] [--pull-deps]
//   --fast  快速模式：只编译当前宿主架构对应的 Linux 平台
//           （x64 → linux-amd64，arm64 → linux-arm64），供本地改码后
//           快速重建容器镜像做验证；其余平台跳过，已有产物不受影响

import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { execFile, execSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createContext, parseAutoFix, preflightGoModules, pullLocalReplaces, runWithRepair } from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const BUILD_DIR = path.join(ROOT_DIR, 'bin');
const BACKEND_DIR = path.join(ROOT_DIR, 'src', 'backend');

// 版本号注入路径：common/version.AppVersion（-ldflags -X 完整变量名）
const VERSION_VAR_PATH = 'github.com/jcbowen/market/src/backend/common/version.AppVersion';

// resolveAppVersion 解析构建时注入的项目版本号
// 取值优先级：环境变量 APP_VERSION（CI tag 发布时注入 GITHUB_REF_NAME / 手动指定）
// → 本地 git 仓库最近可达 tag（git describe，要求当前 HEAD 可达至少一个 tag）
// → 兜底 dev（无 git 或无 tag 的环境，如从源码包构建）
// 返回：
//   - string: 版本号（如 v0.1.3）
function resolveAppVersion() {
  if (process.env.APP_VERSION) return process.env.APP_VERSION;
  try {
    return execSync('git describe --tags --abbrev=0', { cwd: ROOT_DIR, encoding: 'utf8' }).trim();
  } catch {
    return 'dev';
  }
}

const APP_VERSION = resolveAppVersion();
const LDFLAGS = `-X ${VERSION_VAR_PATH}=${APP_VERSION}`;

const ctx = createContext({ rootDir: ROOT_DIR, autoFix: parseAutoFix(process.argv.slice(2)) });

// --fast 快速模式开关：本地改码后快速重建容器镜像用，
// 只编译当前宿主架构对应的 Linux 平台，其余平台跳过
const FAST = process.argv.slice(2).includes('--fast');

// --pull-deps 开关：编译前先拉取本地 replace 依赖仓库最新代码（默认关闭）；
// 与 --no-auto-fix 互斥——两者都改动本地文件，同时传入语义冲突，直接报错退出
const PULL_DEPS = process.argv.slice(2).includes('--pull-deps');
if (PULL_DEPS && !ctx.autoFix) {
  console.error('错误: --pull-deps 与 --no-auto-fix 互斥（拉取依赖仓库会改动本地代码，无法在只读模式下执行）');
  process.exit(1);
}

// 目标平台列表（OS/ARCH）：每个组合对应 bin/{os}-{arch}/ 一个目录，
// 覆盖 Linux/macOS/Windows × amd64/arm64，供 CI Release 打包与多架构 Docker 镜像构建取用
const PLATFORMS = [
  ['linux', 'amd64'],
  ['linux', 'arm64'],
  ['darwin', 'amd64'],
  ['darwin', 'arm64'],
  ['windows', 'amd64'],
  ['windows', 'arm64'],
];

// 实际编译的平台：--fast 时按宿主架构过滤出对应 Linux 平台——
// Docker Desktop/服务器容器运行的就是宿主同架构 Linux 产物
// （x64 → linux-amd64，Apple Silicon → linux-arm64），镜像构建的
// TARGETARCH 只会取该目录；普通模式编译全部平台。
// localPlatform 为函数声明（提升可用），定义见下方。
const TARGET_PLATFORMS = FAST
  ? PLATFORMS.filter(([o, a]) => o === 'linux' && a === localPlatform()[1])
  : PLATFORMS;

// 服务列表：二进制名（geoip-download 为 GeoIP 数据库下载工具，非服务，
// 编译进产物供 allinone 镜像 entrypoint 与服务器 crontab 直接使用）
const SERVICES = [
  'gateway',
  'user-service',
  'product-service',
  'order-service',
  'cart-service',
  'payment-service',
  'market-admin',
  'geoip-download',
];

// 服务二进制名 -> 源码目录
function srcDirOf(svc) {
  if (svc === 'gateway') return './gateway/';
  if (svc === 'market-admin') return './cmd/market-admin/';
  if (svc === 'geoip-download') return './cmd/geoip-download/';
  return `./services/${svc.replace(/-service$/, '')}/`;
}

// safeCleanDir 安全删除指定的平台构建目录。
// 仅当目标路径严格位于 BUILD_DIR 内时才执行删除，
// 避免构建目录变量异常（为空或被指向项目外）时误删系统文件。
function safeCleanDir(target) {
  const resolved = path.resolve(target);
  if (resolved.startsWith(path.resolve(BUILD_DIR) + path.sep)) {
    fs.rmSync(resolved, { recursive: true, force: true });
  } else {
    console.error(`错误：拒绝删除构建目录之外的路径: ${resolved}`);
    process.exit(1);
  }
}

/**
 * 交叉编译单个服务，失败时按依赖诊断自动修复并重试。
 *
 * @param {string} goos 目标系统（darwin/linux/windows）
 * @param {string} arch 目标架构（amd64/arm64）
 * @param {string} svc  服务二进制名（gateway、user-service 等）
 * @returns {void} 编译成功即返回
 * @throws 依赖修复后仍失败时由 runWithRepair 结束进程
 * @example buildOne('linux', 'amd64', 'gateway');
 */
async function buildOne(goos, arch, svc) {
  const ext = goos === 'windows' ? '.exe' : '';
  const out = path.join(BUILD_DIR, `${goos}-${arch}`, svc + ext);
  await runWithRepair(ctx, 'go', ['build', '-ldflags', LDFLAGS, '-o', out, srcDirOf(svc)], {
    cwd: BACKEND_DIR,
    env: { ...ctx.goEnv, GOOS: goos, GOARCH: arch },
    step: `构建 ${svc}（${goos}/${arch}）`,
  });
}

// 识别本机环境
function localPlatform() {
  let localOs = process.platform; // darwin / linux / win32
  if (localOs === 'win32') localOs = 'windows';
  const m = os.arch(); // arm64 / x64 ...
  const localArch = m === 'x64' ? 'amd64' : m === 'arm64' ? 'arm64' : m;
  return [localOs, localArch];
}

// 有限并发执行任务队列：并发度取 min(limit, 任务数)
async function runLimited(limit, tasks) {
  const queue = [...tasks];
  await Promise.all(
    Array.from({ length: Math.min(limit, queue.length) }, async () => {
      while (queue.length) await queue.shift()();
    }),
  );
}

// 直接编译（不走依赖自动修复）：并发路径使用，失败只记录不修复，
// 失败项收集后由 runWithRepair 串行重试——修复含 go mod tidy，不能并发写 go.mod/go.sum
// env 必须展开 process.env：execFile 不经 shell，命令名靠 PATH 解析，
// 只给 ctx.goEnv（GOPATH/CGO_ENABLED）会连 go 本身都找不到，报 spawn go ENOENT——
// 现象是并发阶段每个服务都失败、再被串行重试全部"修好"，等于并发提速从未生效
function buildOnePlain(goos, arch, svc) {
  const ext = goos === 'windows' ? '.exe' : '';
  const out = path.join(BUILD_DIR, `${goos}-${arch}`, svc + ext);
  return new Promise((resolve, reject) => {
    execFile(
      'go',
      ['build', '-ldflags', LDFLAGS, '-o', out, srcDirOf(svc)],
      { cwd: BACKEND_DIR, env: { ...process.env, ...ctx.goEnv, GOOS: goos, GOARCH: arch } },
      (err) => (err ? reject(err) : resolve()),
    );
  });
}

// 同平台内服务级并行度：8 个服务共享同一份 Go 编译缓存，
// 首个服务（gateway 串行预热）最贵，其余并发提速约 2~3 倍
const SERVICE_PARALLEL = Math.max(2, Math.min(4, os.cpus().length));

fs.mkdirSync(path.join(BUILD_DIR, 'static'), { recursive: true });

// --pull-deps：编译前先把本地 replace 依赖仓库拉到最新；本地 replace 代码
// 直接参与编译，拉取失败的仓库继续编译会用旧代码，因此存在失败项时终止，
// 由人工按 pull-deps.mjs 的详细输出处理后重跑
if (PULL_DEPS) {
  const { results } = await pullLocalReplaces(ctx);
  if (results.some((r) => r.status === 'failed')) {
    console.error('错误: 本地依赖仓库拉取存在失败项，为避免用旧代码编译已终止（可先单独运行 node pull-deps.mjs 定位）');
    process.exit(1);
  }
  console.log('');
}

// 编译前格式闸门：gofmt -l 有输出即终止构建并列出待格式化文件。
// 放这里而非 git hook：三台开发机（win10 / macOS / win11）的 hook 需各自安装、
// core.hooksPath 与 --no-verify 都能绕过，而 build.mjs 是所有人都必经的编译入口，
// CI 的 backend job 也走它，闸门放在唯一入口才有约束力
async function checkGofmt() {
  const { stdout, stderr } = await new Promise((resolve, reject) => {
    execFile('gofmt', ['-l', '.'], { cwd: BACKEND_DIR, env: { ...process.env, ...ctx.goEnv } }, (err, stdout, stderr) =>
      err ? reject(err) : resolve({ stdout, stderr }),
    );
  }).catch((err) => {
    console.error(`错误: gofmt 检查执行失败（go 工具链自带该命令，请确认 go 在 PATH 中）: ${err.message}`);
    process.exit(1);
  });
  const bad = stdout.trim().split('\n').filter(Boolean);
  if (bad.length) {
    console.error(`错误: ${bad.length} 个 Go 文件格式不符合 gofmt（请在 src/backend 执行 gofmt -w .）:`);
    for (const f of bad) console.error(`  - ${f}`);
    process.exit(1);
  }
  if (stderr.trim()) console.error(`gofmt 提示: ${stderr.trim()}`);
}

// 编译前预检 Go 模块依赖（本地 replace 路径、go.mod/go.sum 一致性）
console.log('==> 预检 Go 代码格式...');
await checkGofmt();

console.log('==> 预检 Go 模块依赖...');
await preflightGoModules(ctx);

// 注入版本号统一展示，便于确认产物版本来源
console.log(`==> 注入版本号: ${APP_VERSION}`);

// 逐平台组合编译（平台间串行——各平台编译缓存互不相通；平台内服务并行）
if (FAST) {
  console.log(`==> 快速模式（--fast）：仅编译 ${TARGET_PLATFORMS.map(([o, a]) => `${o}/${a}`).join('、')} ...`);
}
for (const [goos, arch] of TARGET_PLATFORMS) {
  console.log(`==> 构建 ${goos}/${arch} ...`);
  safeCleanDir(path.join(BUILD_DIR, `${goos}-${arch}`));
  fs.mkdirSync(path.join(BUILD_DIR, `${goos}-${arch}`), { recursive: true });

  // gateway 串行先行：预热本平台的依赖编译缓存，并让依赖修复（若触发）安全串行
  console.log('    构建 gateway ...');
  await buildOne(goos, arch, 'gateway');

  // 其余服务有限并发；失败项收集后走 runWithRepair 串行重试（可自动 go mod tidy）
  const rest = SERVICES.filter((svc) => svc !== 'gateway');
  const failed = [];
  let firstError = null;
  await runLimited(
    SERVICE_PARALLEL,
    rest.map((svc) => async () => {
      try {
        await buildOnePlain(goos, arch, svc);
      } catch (err) {
        // 保留首个失败的原始错误文本：并发阶段的错误此前被丢弃，重试若再失败
        // 只能看到重试侧的输出，编译错误原文（go build stderr）无从查起
        if (!firstError) firstError = { svc, err };
        failed.push(svc);
      }
    }),
  );
  for (const svc of failed) {
    if (firstError && firstError.svc === svc) {
      console.error(`    并发阶段首个失败（${svc}）原文：`);
      console.error(String(firstError.err?.stderr || firstError.err?.message || firstError.err));
    }
    console.log(`    重试（含依赖修复）: ${svc} ...`);
    await buildOne(goos, arch, svc);
  }
}

// 复制本机对应的二进制到 bin/ 根目录（仅在本次实际编译的平台中查找：
// --fast 只产出 Linux 容器平台二进制，本机平台未编译时不应拿旧产物冒充新构建）
const [LOCAL_OS, LOCAL_ARCH] = localPlatform();
const localMatch = TARGET_PLATFORMS.find(([o, a]) => o === LOCAL_OS && a === LOCAL_ARCH);

if (localMatch) {
  const ext = LOCAL_OS === 'windows' ? '.exe' : '';
  console.log(`==> 复制本机（${LOCAL_OS}/${LOCAL_ARCH}）二进制到 ${BUILD_DIR}${path.sep} ...`);
  for (const svc of SERVICES) {
    fs.copyFileSync(
      path.join(BUILD_DIR, `${LOCAL_OS}-${LOCAL_ARCH}`, svc + ext),
      path.join(BUILD_DIR, svc + ext),
    );
  }
} else if (FAST) {
  console.log(`==> 快速模式仅编译容器平台产物，本机（${LOCAL_OS}/${LOCAL_ARCH}）二进制未更新，跳过复制`);
} else {
  console.log(`==> 本机环境（${LOCAL_OS}/${LOCAL_ARCH}）不在构建列表中，跳过复制`);
}

console.log(`==> 构建完成，输出目录: ${BUILD_DIR}`);
