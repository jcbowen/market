#!/usr/bin/env node
// 社区商超生产发布构建脚本
// 1. 检查并补齐前端依赖（缺失或损坏时自动 npm install）
// 2. 构建前端（管理端 + C 端 H5）
// 3. 构建后端二进制（Linux arm64 交叉编译）
// 4. 构建 Docker 镜像（最终镜像仅包含编译产物，不含源码）
//
// 依赖不全时由 deps-tools.mjs 判定原因并自动补齐（npm install / go mod tidy / 镜像 pull）后重试，
// 与 start-all.mjs 行为一致。
//
// 使用方法：
//   node deploy.mjs              # 完整构建
//   node deploy.mjs --up         # 构建完成后启动服务
//   node deploy.mjs --no-cache   # 构建镜像时不使用缓存
//   node deploy.mjs --no-auto-fix # 依赖不全时只提示原因，不自动执行修复

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  buildComposeImages,
  createContext,
  ensureGeoipDatabase,
  ensureNpmDependencies,
  runWithRepair,
} from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

// 解析可选参数
let UP_AFTER_BUILD = false;
let NO_CACHE = false;
let AUTO_FIX = true;
for (const arg of process.argv.slice(2)) {
  if (arg === '--up') UP_AFTER_BUILD = true;
  else if (arg === '--no-cache') NO_CACHE = true;
  else if (arg === '--no-auto-fix') AUTO_FIX = false;
  else {
    console.error(`未知参数: ${arg}`);
    process.exit(1);
  }
}

const ctx = createContext({ rootDir: ROOT_DIR, autoFix: AUTO_FIX });

// 同步等待 1 秒（仅用于 Docker 守护进程的就绪轮询）
function sleepSync() {
  if (process.platform === 'win32') {
    spawnSync('timeout', ['/t', '1', '/nobreak'], { shell: true, stdio: 'ignore' });
  } else {
    spawnSync('sleep', ['1']);
  }
}

// Docker 守护进程检测与自动启动（同步版）
function dockerDaemonRunning() {
  const r = spawnSync('docker', ['info'], {
    encoding: 'utf8',
    shell: process.platform === 'win32',
  });
  return r.status === 0;
}

function ensureDockerRunning() {
  if (dockerDaemonRunning()) return;

  console.log('==> Docker 未运行，正在启动 Docker Desktop...');
  if (process.platform === 'darwin') {
    let opened = false;
    for (const app of ['Docker', 'Docker Desktop']) {
      const r = spawnSync('open', ['-a', app]);
      if (r.status === 0) { opened = true; break; }
    }
    if (!opened) {
      console.error('错误: 无法启动 Docker Desktop，请手动启动后重试');
      process.exit(1);
    }
  } else if (process.platform === 'win32') {
    const exe = 'C:/Program Files/Docker/Docker/Docker Desktop.exe';
    if (!fs.existsSync(exe)) {
      console.error(`错误: 未找到 Docker Desktop（${exe}），请手动启动后重试`);
      process.exit(1);
    }
    spawn(exe, [], { detached: true, stdio: 'ignore' }).unref();
  } else {
    const r = spawnSync('sudo', ['systemctl', 'start', 'docker'], { stdio: 'inherit' });
    if (r.status !== 0) {
      console.error('错误: 无法启动 docker 服务，请手动启动后重试');
      process.exit(1);
    }
  }

  console.log('==> 等待 Docker 就绪...');
  for (let i = 1; i <= 120; i++) {
    if (dockerDaemonRunning()) {
      console.log('    Docker 已就绪');
      return;
    }
    sleepSync();
    if (i % 15 === 0) console.log(`    仍在等待 Docker 启动... (${i}s)`);
  }
  console.error('错误: Docker 启动超时（120 秒），请手动启动 Docker Desktop 后重试');
  process.exit(1);
}

ensureDockerRunning();

// 前端子项目目录（依赖检查与构建共用）
const ADMIN_DIR = path.join(ROOT_DIR, 'src', 'admin');
const CLIENT_DIR = path.join(ROOT_DIR, 'src', 'client');

console.log('==> 构建管理端...');
await ensureNpmDependencies(ctx, ADMIN_DIR);
await runWithRepair(ctx, 'npm', ['run', 'build'], { cwd: ADMIN_DIR, step: '构建管理端' });

console.log('==> 构建 C 端 H5...');
await ensureNpmDependencies(ctx, CLIENT_DIR);
await runWithRepair(ctx, 'npm', ['run', 'build:h5'], { cwd: CLIENT_DIR, step: '构建 C 端 H5' });

console.log('==> 构建后端二进制（Linux arm64 交叉编译）...');
// 依赖预检与 go mod tidy 自愈在 build.mjs 内部完成，这里透传开关保持一致
await runWithRepair(
  ctx,
  process.execPath,
  [path.join(ROOT_DIR, 'build.mjs'), ...(AUTO_FIX ? [] : ['--no-auto-fix'])],
  { step: '构建后端二进制', allowRepair: false },
);

console.log('==> 准备本地附件共享目录（不存在时自动创建）...');
fs.mkdirSync(path.join(ROOT_DIR, 'bin', 'attachment'), { recursive: true });

// GeoIP 定位库缺失只告警，不阻断发布构建
await ensureGeoipDatabase(ctx);

console.log('==> 构建 Docker 镜像...');
await buildComposeImages(ctx, NO_CACHE ? ['--no-cache'] : []);

console.log('==> 发布构建完成');
console.log('    镜像列表：');
const imgs = spawnSync('docker', ['images'], {
  cwd: ROOT_DIR,
  encoding: 'utf8',
  shell: process.platform === 'win32',
});
for (const line of (imgs.stdout || '').split('\n')) {
  if (line.includes('market')) console.log(line);
}

if (UP_AFTER_BUILD) {
  console.log('==> 启动服务...');
  await runWithRepair(ctx, 'docker', ['compose', 'up', '-d'], { cwd: ROOT_DIR, step: '启动服务' });
  // 前端产物目录被重建后，运行中的 gateway 仍绑定旧 inode，restart 才会重新解析 bind mount
  console.log('==> 重新挂载前端产物（重启 Gateway）...');
  await runWithRepair(ctx, 'docker', ['compose', 'restart', 'gateway'], { cwd: ROOT_DIR, step: '重启 Gateway' });
}
