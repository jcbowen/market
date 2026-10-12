#!/usr/bin/env node
// 社区商超后端构建脚本（三平台交叉编译）
//
// 1. 交叉编译 gateway 及各微服务的 Windows / macOS / Linux 二进制，
//    按平台存放到 bin/ 下的二级目录：
//      bin/darwin/    macOS (arm64)
//      bin/linux/     Linux (arm64)
//      bin/windows/   Windows (amd64，.exe 后缀)
// 2. 编译完成后，自动将与本机环境一致的二进制复制一份到 bin/ 根目录，便于直接运行。
//
// 平台与架构在下方 PLATFORMS 列表中维护，无需在编译时传递环境参数。
//
// 使用方法：node build.mjs

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const BUILD_DIR = path.join(ROOT_DIR, 'bin');
const BACKEND_DIR = path.join(ROOT_DIR, 'src', 'backend');

// 目标平台列表（OS/ARCH），每个 OS 对应 bin/ 下一个二级目录
const PLATFORMS = [
  ['darwin', 'arm64'],
  ['linux', 'arm64'],
  ['windows', 'amd64'],
];

// 服务列表：二进制名
const SERVICES = [
  'gateway',
  'user-service',
  'product-service',
  'order-service',
  'cart-service',
  'payment-service',
];

// 服务二进制名 -> 源码目录
function srcDirOf(svc) {
  if (svc === 'gateway') return './gateway/';
  return `./services/${svc.replace(/-service$/, '')}/`;
}

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, {
    stdio: opts.capture ? ['ignore', 'pipe', 'pipe'] : 'inherit',
    cwd: opts.cwd || ROOT_DIR,
    env: opts.env ? { ...process.env, ...opts.env } : process.env,
    shell: process.platform === 'win32',
  });
  return r;
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

function buildOne(goos, arch, svc) {
  const ext = goos === 'windows' ? '.exe' : '';
  const out = path.join(BUILD_DIR, goos, svc + ext);
  const r = run('go', ['build', '-o', out, srcDirOf(svc)], {
    cwd: BACKEND_DIR,
    env: {
      GOPATH: ROOT_DIR,
      CGO_ENABLED: '0',
      GOOS: goos,
      GOARCH: arch,
    },
  });
  if (r.status !== 0) process.exit(r.status ?? 1);
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

// 逐平台编译
for (const [goos, arch] of PLATFORMS) {
  console.log(`==> 构建 ${goos}/${arch} ...`);
  safeCleanDir(path.join(BUILD_DIR, goos));
  fs.mkdirSync(path.join(BUILD_DIR, goos), { recursive: true });
  for (const svc of SERVICES) {
    console.log(`    构建 ${svc} ...`);
    buildOne(goos, arch, svc);
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
      path.join(BUILD_DIR, LOCAL_OS, svc + ext),
      path.join(BUILD_DIR, svc + ext),
    );
  }
} else {
  console.log(`==> 本机环境（${LOCAL_OS}/${LOCAL_ARCH}）不在构建列表中，跳过复制`);
}

console.log(`==> 构建完成，输出目录: ${BUILD_DIR}`);
