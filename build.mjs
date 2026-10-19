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
// 平台与架构在下方 PLATFORMS 列表中维护，无需在编译时传递环境参数。
//
// 依赖不全（go.sum 缺行、模块未下载）会在编译前预检并自动 go mod tidy 修复，
// 指定 --no-auto-fix 时只检查提示、不改动 go.mod / go.sum。
//
// 使用方法：node build.mjs [--no-auto-fix]

import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { createContext, parseAutoFix, preflightGoModules, runWithRepair } from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const BUILD_DIR = path.join(ROOT_DIR, 'bin');
const BACKEND_DIR = path.join(ROOT_DIR, 'src', 'backend');

const ctx = createContext({ rootDir: ROOT_DIR, autoFix: parseAutoFix(process.argv.slice(2)) });

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

// 服务列表：二进制名
const SERVICES = [
  'gateway',
  'user-service',
  'product-service',
  'order-service',
  'cart-service',
  'payment-service',
  'market-admin',
];

// 服务二进制名 -> 源码目录
function srcDirOf(svc) {
  if (svc === 'gateway') return './gateway/';
  if (svc === 'market-admin') return './cmd/market-admin/';
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
  await runWithRepair(ctx, 'go', ['build', '-o', out, srcDirOf(svc)], {
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

fs.mkdirSync(path.join(BUILD_DIR, 'static'), { recursive: true });

// 编译前预检 Go 模块依赖（本地 replace 路径、go.mod/go.sum 一致性）
console.log('==> 预检 Go 模块依赖...');
await preflightGoModules(ctx);

// 逐平台组合编译
for (const [goos, arch] of PLATFORMS) {
  console.log(`==> 构建 ${goos}/${arch} ...`);
  safeCleanDir(path.join(BUILD_DIR, `${goos}-${arch}`));
  fs.mkdirSync(path.join(BUILD_DIR, `${goos}-${arch}`), { recursive: true });
  for (const svc of SERVICES) {
    console.log(`    构建 ${svc} ...`);
    await buildOne(goos, arch, svc);
  }
}

// 复制本机对应的二进制到 bin/ 根目录
const [LOCAL_OS, LOCAL_ARCH] = localPlatform();
const localMatch = PLATFORMS.find(([o, a]) => o === LOCAL_OS && a === LOCAL_ARCH);

if (localMatch) {
  const ext = LOCAL_OS === 'windows' ? '.exe' : '';
  console.log(`==> 复制本机（${LOCAL_OS}/${LOCAL_ARCH}）二进制到 ${BUILD_DIR}${path.sep} ...`);
  for (const svc of SERVICES) {
    fs.copyFileSync(
      path.join(BUILD_DIR, `${LOCAL_OS}-${LOCAL_ARCH}`, svc + ext),
      path.join(BUILD_DIR, svc + ext),
    );
  }
} else {
  console.log(`==> 本机环境（${LOCAL_OS}/${LOCAL_ARCH}）不在构建列表中，跳过复制`);
}

console.log(`==> 构建完成，输出目录: ${BUILD_DIR}`);
