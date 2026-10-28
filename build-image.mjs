#!/usr/bin/env node
// market 镜像构建与发布脚本（一体化 all-in-one + 多容器应用镜像）
//
// 两类镜像消费同一份 bin/ 产物（见 src/backend/deploy/）：
//   默认      —— Dockerfile.allinone：MySQL 8.0 + Redis 7 + 全部服务 + 前端，单容器一键起全栈
//   --app     —— Dockerfile.app：alpine 底座不含 MySQL/Redis，单镜像多容器部署
//                （compose 示例见 docker-compose.prod.yml），支持多架构（linux amd64/arm64）
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
//   # 本地重建基础镜像（一般无需执行：CI 发 tag 时按 Dockerfile.base 内容哈希
//   # 自动检测并构建；仅当本地想先于 CI 构建时使用，需 Docker Hub 登录 +
//   # containerd 镜像存储）
//   node build-image.mjs --build-base
//
//   # 构建多容器部署应用镜像（单镜像多容器，alpine 底座不含 MySQL/Redis，
//   # 见 src/backend/deploy/Dockerfile.app 与 docker-compose.prod.yml）；
//   # 与一体化镜像消费同一份 bin/ 产物，不依赖 market-base
//   node build-image.mjs --app
//   node build-image.mjs --app --tag <registry>/market-app:1.0.0 --push
//
// 基础镜像：Dockerfile.allinone 的 ubuntu + mysql/redis + appuser 层已固化为
// market-base 镜像（见 src/backend/deploy/Dockerfile.base）。本脚本按该文件的
// 内容 sha256 生成 tag（sha-<前12位>），先检测仓库是否已存在该 tag：
//   存在 → 直接 --build-arg 引用；不存在 → 报错提示（CI 会自动构建，或本地 --build-base）。
//
// 前置条件：已执行 `node build.mjs` 产出 bin/linux-{amd64,arm64} 与 bin/static；
// 多架构推送需本机 Docker 开启 containerd 镜像存储（Docker Desktop 设置项）。
//
// 参数：
//   --tag <name:tag>      镜像名（--push 时必填）
//   --push                构建后推送到镜像仓库（多架构发布的唯一途径）
//   --platforms <list>    逗号分隔，默认本机架构单平台（本地 load 不支持多架构）
//   --no-cache            不使用构建缓存
//   --build-base          构建（多架构）并推送 market-base 基础镜像
//   --app                 构建应用镜像（Dockerfile.app，多容器部署）而非一体化镜像

import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { needsShell } from './deps-tools.mjs';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

// ---------- 参数解析 ----------

let tag = '';
let push = false;
let noCache = false;
let buildBase = false;
let appMode = false;
let platformsArg = '';
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i] === '--tag') tag = argv[++i] ?? '';
  else if (argv[i] === '--push') push = true;
  else if (argv[i] === '--no-cache') noCache = true;
  else if (argv[i] === '--build-base') buildBase = true;
  else if (argv[i] === '--app') appMode = true;
  else if (argv[i] === '--platforms') platformsArg = argv[++i] ?? '';
  else {
    console.error(`未知参数: ${argv[i]}`);
    process.exit(1);
  }
}

if (buildBase && appMode) {
  console.error('错误: --build-base 与 --app 互斥（前者构建基础镜像，后者构建应用镜像）');
  process.exit(1);
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

// ---------- 基础镜像（market-base）解析 ----------

const DOCKERHUB_OWNER = process.env.DOCKERHUB_USERNAME || 'jcbowen';
const BASE_DOCKERFILE = path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'Dockerfile.base');
if (!fs.existsSync(BASE_DOCKERFILE)) {
  console.error(`错误: 缺失 ${BASE_DOCKERFILE}`);
  process.exit(1);
}
// 内容哈希 tag：Dockerfile.base 内容不变 → tag 不变 → 永远复用仓库里的现成层；
// 内容一旦改动 → 新 tag → CI/本地检测到缺失后自动重建
const BASE_TAG = `sha-${crypto.createHash('sha256').update(fs.readFileSync(BASE_DOCKERFILE)).digest('hex').slice(0, 12)}`;
const BASE_IMAGE = `docker.io/${DOCKERHUB_OWNER}/market-base:${BASE_TAG}`;
const BASE_LATEST = `docker.io/${DOCKERHUB_OWNER}/market-base:latest`;

function baseImageExists() {
  const r = spawnSync('docker', ['manifest', 'inspect', BASE_IMAGE], {
    encoding: 'utf8',
    shell: needsShell('docker'),
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  return r.status === 0;
}

function runDocker(args) {
  const r = spawnSync('docker', args, { stdio: 'inherit', cwd: ROOT_DIR, shell: needsShell('docker') });
  return r.status === 0;
}

// 仅构建并推送基础镜像，然后退出
if (buildBase) {
  console.log(`==> 构建并推送基础镜像: ${BASE_IMAGE}（同时更新 ${BASE_LATEST}）`);
  console.log('==> 平台: linux/amd64,linux/arm64（需 containerd 镜像存储 + Docker Hub 已登录）');
  if (!runDocker(['buildx', 'build', '--platform', 'linux/amd64,linux/arm64',
    '-f', BASE_DOCKERFILE, '-t', BASE_IMAGE, '-t', BASE_LATEST, '--push', '.'])) {
    console.error('错误: 基础镜像构建失败');
    process.exit(1);
  }
  console.log(`==> 基础镜像完成: ${BASE_IMAGE}`);
  process.exit(0);
}

// ---------- 产物预检 ----------

// 一体化镜像与 app 镜像消费同一份 bin/ 产物，Dockerfile/配置模板/入口脚本不同
const DOCKERFILE = appMode
  ? path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'Dockerfile.app')
  : path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'Dockerfile.allinone');
// 镜像直接 COPY 预编译产物（见 Dockerfile.allinone / Dockerfile.app COPY 列表），
// 构建期不编译：缺一个或旧于最新源码都会让 `docker compose up --build` /
// 本地 load 静默跑旧二进制
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
  { file: DOCKERFILE, hint: appMode ? '缺失 Dockerfile.app' : '缺失 Dockerfile.allinone' },
  { file: path.join(ROOT_DIR, 'bin', 'static', 'admin'), hint: '缺失前端产物 bin/static/admin，请先执行构建' },
  { file: path.join(ROOT_DIR, 'bin', 'static', 'h5'), hint: '缺失前端产物 bin/static/h5，请先执行构建' },
  appMode
    ? { file: path.join(ROOT_DIR, 'bin', 'config.docker.yaml'), hint: '缺失 bin/config.docker.yaml 配置模板' }
    : { file: path.join(ROOT_DIR, 'bin', 'config.allinone.yaml'), hint: '缺失 bin/config.allinone.yaml 配置模板' },
  appMode
    ? { file: path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'entrypoint-app.sh'), hint: '缺失 entrypoint-app.sh' }
    : { file: path.join(ROOT_DIR, 'src', 'backend', 'deploy', 'entrypoint.sh'), hint: '缺失 entrypoint.sh' },
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
//
// 范围取舍（两条刻意排除，均有依据）：
//   - *_test.go 不参与 go build 产物（构建时被编译器排除），改测试/注释不应
//     触发 8 个二进制 × 双平台全量重编，故新鲜度判定跳过测试文件；
//   - entrypoint.sh / Dockerfile.allinone / config.allinone.yaml 是镜像构建期
//     直接 COPY 的文件（见 Dockerfile.allinone），不进 bin/linux-* 产物、也不该
//     要求先跑 build.mjs——buildx 对 COPY 层按文件内容哈希失效，内容一变该层
//     自动重建，天然比 mtime 判定更可靠，故不纳入本校验（缺失检查仍在上文 required）。
const SOURCE_EXTENSIONS = new Set(['.go', '.proto', '.mod', '.sum']);
function newestSourceMtime(dir) {
  let newest = 0;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === 'vendor' || e.name === 'tmp') continue;
      newest = Math.max(newest, newestSourceMtime(full));
    } else if (SOURCE_EXTENSIONS.has(path.extname(e.name)) && !e.name.endsWith('_test.go')) {
      // 跳过 *_test.go：测试文件不进二进制产物，仅其变更不应强制全量重编
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

// 前端产物新鲜度：bin/static/admin、bin/static/h5 早于对应前端源码目录最新文件时，
// 说明改了前端但没重跑构建——镜像会静默打包旧页面（此前只检查目录是否存在）。
// 判据与后端一致（比较 mtime）：构建产物随每次 build 重新写入，源码改动必然更新其 mtime。
function newestFileMtime(dir) {
  let newest = 0;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === 'node_modules' || e.name === 'dist' || e.name === 'unpackage') continue;
      newest = Math.max(newest, newestFileMtime(full));
    } else {
      newest = Math.max(newest, fs.statSync(full).mtimeMs);
    }
  }
  return newest;
}
const FRONTEND_CHECKS = [
  { out: path.join(ROOT_DIR, 'bin', 'static', 'admin'), src: path.join(ROOT_DIR, 'src', 'admin', 'src'), build: 'cd src/admin && npm run build' },
  { out: path.join(ROOT_DIR, 'bin', 'static', 'h5'), src: path.join(ROOT_DIR, 'src', 'client', 'src'), build: 'cd src/client && npm run build:h5' },
];
for (const c of FRONTEND_CHECKS) {
  const outName = path.relative(ROOT_DIR, c.out);
  const newestOut = newestFileMtime(c.out);
  if (newestOut === 0) {
    console.error(`错误: ${outName} 为空，请先执行前端构建（${c.build}）`);
    process.exit(1);
  }
  const newestFront = newestFileMtime(c.src);
  if (newestOut < newestFront) {
    console.error(`错误: ${outName} 早于前端源码（改了页面但未重新构建），请先执行 ${c.build}`);
    process.exit(1);
  }
}

if (!tag) tag = appMode ? 'market-app:dev' : 'market:dev';

// ---------- buildx 构建 ----------

// 基础镜像存在性检查（仅一体化镜像：FROM market-base；app 镜像基于 alpine
// 官方多架构镜像，无此依赖）：缺失则明确报错，静默回退 latest 会跑旧基础层
if (!appMode) {
  if (!baseImageExists()) {
    console.error(`错误: 基础镜像 ${BASE_IMAGE} 尚不存在（Dockerfile.base 已变更且未构建）`);
    console.error('       处理方式二选一：');
    console.error('       1. 推荐：提交并推送 v* tag，由 CI 的 release-base job 自动构建后再发业务镜像');
    console.error('       2. 本地执行：node build-image.mjs --build-base');
    process.exit(1);
  }
  console.log(`==> 基础镜像: ${BASE_IMAGE}`);
}

const args = ['buildx', 'build', '--platform', platforms.join(','), '-f', DOCKERFILE, '-t', tag];
if (!appMode) args.push('--build-arg', `BASE_IMAGE=${BASE_IMAGE}`);
if (noCache) args.push('--no-cache');
args.push(push ? '--push' : '--load');
args.push('.');

console.log(`==> 构建${appMode ? '应用' : '一体化'}镜像: ${tag}`);
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
