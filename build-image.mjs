#!/usr/bin/env node
// market 一体化镜像（all-in-one）构建与发布脚本
//
// 镜像内容：MySQL 8.0 + Redis 7 + gateway + 五个微服务 + 前端静态资源
// （见 src/backend/deploy/Dockerfile.allinone），支持多架构（linux amd64/arm64）。
//
// 使用方法：
//   # 本机架构构建并载入本地 Docker（默认，用于本地验证）
//   node build-image.mjs
//
//   # 指定 tag 构建多架构并推送镜像仓库（发布）
//   node build-image.mjs --tag <registry>/market:1.0.0 --push
//
//   # 指定平台组合
//   node build-image.mjs --tag <registry>/market:1.0.0 --push \
//     --platforms linux/amd64,linux/arm64
//
//   # 不使用构建缓存
//   node build-image.mjs --tag <registry>/market:1.0.0 --push --no-cache
//
// 前置条件：已执行 `node build.mjs` 产出 bin/linux-{amd64,arm64} 与 bin/static；
// 多架构推送需本机 Docker 开启 containerd 镜像存储（Docker Desktop 设置项）。
//
// 参数：
//   --tag <name:tag>      镜像名（--push 时必填）
//   --push                构建后推送到镜像仓库（多架构发布的唯一途径）
//   --platforms <list>    逗号分隔，默认本机架构单平台（本地 load 不支持多架构）
//   --no-cache            不使用构建缓存

import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { spawnSync } from 'node:child_process';
import { needsShell } from './deps-tools.mjs';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

// ---------- 参数解析 ----------

let tag = '';
let push = false;
let noCache = false;
let platformsArg = '';
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i] === '--tag') tag = argv[++i] ?? '';
  else if (argv[i] === '--push') push = true;
  else if (argv[i] === '--no-cache') noCache = true;
  else if (argv[i] === '--platforms') platformsArg = argv[++i] ?? '';
  else {
    console.error(`未知参数: ${argv[i]}`);
    process.exit(1);
  }
}

if (push && !tag) {
  console.error('错误: --push 需要同时指定 --tag <registry>/<name>:<version>');
  process.exit(1);
}

// tag 白名单校验：下方 docker 调用在 Windows 上走 shell:true，未校验的参数
// 会被拼进 shell 执行（如 --tag 'x&&cmd'），故只允许镜像名的合法字符集
if (tag && !/^[a-zA-Z0-9][a-zA-Z0-9._\-/:]*$/.test(tag)) {
  console.error(`错误: --tag 含非法字符: ${tag}`);
  process.exit(1);
}

// 平台解析：默认本机架构（本地 Docker load 仅支持单平台）
const hostArch = os.arch() === 'arm64' ? 'arm64' : 'amd64';
let platforms = platformsArg
  ? platformsArg.split(',').map((s) => s.trim()).filter(Boolean)
  : [`linux/${hostArch}`];

if (!push && platforms.length > 1) {
  console.error('提示: 本地 Docker 仅能载入单平台镜像，已自动收敛为本机架构 linux/' + hostArch);
  console.error('      发布多架构镜像请加 --push（并先用 --tag 指定仓库地址）');
  platforms = [`linux/${hostArch}`];
}

for (const p of platforms) {
  if (!/^linux\/(amd64|arm64)$/.test(p)) {
    console.error(`错误: 不支持的平台 ${p}（一体化镜像仅支持 linux/amd64 与 linux/arm64）`);
    process.exit(1);
  }
}

// ---------- 产物预检 ----------

const DOCKERFILE = path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'Dockerfile.allinone');
// 镜像直接 COPY 预编译产物（见 Dockerfile.allinone COPY 列表），构建期不编译：
// 缺一个或旧于最新源码都会让 `docker compose up --build` / 本地 load 静默跑旧二进制
const IMAGE_BINARIES = [
  'gateway',
  'user-service',
  'product-service',
  'order-service',
  'cart-service',
  'payment-service',
  'market-admin',
  'geoip-download',
];
const required = [
  { file: DOCKERFILE, hint: '缺失 Dockerfile.allinone' },
  { file: path.join(ROOT_DIR, 'bin', 'static', 'admin'), hint: '缺失前端产物 bin/static/admin，请先执行构建' },
  { file: path.join(ROOT_DIR, 'bin', 'static', 'h5'), hint: '缺失前端产物 bin/static/h5，请先执行构建' },
  { file: path.join(ROOT_DIR, 'bin', 'config.allinone.yaml'), hint: '缺失 bin/config.allinone.yaml 配置模板' },
  { file: path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'entrypoint.sh'), hint: '缺失 entrypoint.sh' },
];
for (const p of platforms) {
  const arch = p.split('/')[1];
  for (const name of IMAGE_BINARIES) {
    required.push({
      file: path.join(ROOT_DIR, 'bin', `linux-${arch}`, name),
      hint: `缺失 linux/${arch} 后端产物 bin/linux-${arch}/${name}，请先执行 node build.mjs`,
    });
  }
}
for (const r of required) {
  if (!fs.existsSync(r.file)) {
    console.error(`错误: ${r.hint}`);
    process.exit(1);
  }
}

// 产物新旧校验：任一二进制早于 src/backend 下最新源码（.go/.proto/go.mod/go.sum），
// 说明改了代码但没重新 node build.mjs，直接失败提示重建，不静默打包旧产物
const SOURCE_EXTENSIONS = new Set(['.go', '.proto', '.mod', '.sum']);
function newestSourceMtime(dir) {
  let newest = 0;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === 'vendor' || e.name === 'tmp') continue;
      newest = Math.max(newest, newestSourceMtime(full));
    } else if (SOURCE_EXTENSIONS.has(path.extname(e.name))) {
      newest = Math.max(newest, fs.statSync(full).mtimeMs);
    }
  }
  return newest;
}
const newestSrc = newestSourceMtime(path.join(ROOT_DIR, 'src', 'backend'));
for (const p of platforms) {
  const arch = p.split('/')[1];
  for (const name of IMAGE_BINARIES) {
    const bin = path.join(ROOT_DIR, 'bin', `linux-${arch}`, name);
    if (fs.statSync(bin).mtimeMs < newestSrc) {
      console.error(`错误: bin/linux-${arch}/${name} 早于最新源码（改了 Go 代码但未重新编译），请先执行 node build.mjs`);
      process.exit(1);
    }
  }
}

if (!tag) tag = 'market:dev';

// ---------- buildx 构建 ----------

const args = ['buildx', 'build', '--platform', platforms.join(','), '-f', DOCKERFILE, '-t', tag];
if (noCache) args.push('--no-cache');
args.push(push ? '--push' : '--load');
args.push('.');

console.log(`==> 构建一体化镜像: ${tag}`);
console.log(`==> 平台: ${platforms.join(', ')}  模式: ${push ? 'push（发布）' : 'load（本地载入）'}`);
const r = spawnSync('docker', args, { stdio: 'inherit', cwd: ROOT_DIR, shell: needsShell('docker') });
if (r.status !== 0) {
  console.error('错误: 镜像构建失败');
  process.exit(r.status ?? 1);
}

console.log(`==> 完成: ${tag}`);
if (push) {
  console.log(`    服务器拉取: docker pull ${tag}`);
} else {
  console.log(`    本地运行验证示例见 Dockerfile.allinone 头部注释或 docs/需求文档/10-部署方案/03-生产环境部署.md`);
}
