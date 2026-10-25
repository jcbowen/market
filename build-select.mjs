#!/usr/bin/env node
// build-select.mjs
// 交互式选择编译部分并最终打包
//
// 支持选择：
//   1. admin 管理端
//   2. client C 端 H5
//   3. backend 后端服务
//   4. docker 镜像
//   5. 全部
//   0. 退出
//
// 依赖不全时的检查与自愈同 start-all.mjs（见 deps-tools.mjs），
// 传 --no-auto-fix 可关闭自动 npm install / go mod tidy。

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline/promises';
import { fileURLToPath } from 'node:url';
import {
  buildComposeImages,
  createContext,
  ensureGeoipDatabase,
  ensureNpmDependencies,
  needsShell,
  parseAutoFix,
  runWithRepair,
} from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const DIST_DIR = path.join(ROOT_DIR, 'bin', 'release');
const ctx = createContext({ rootDir: ROOT_DIR, autoFix: parseAutoFix(process.argv.slice(2)) });

// 菜单项
let BUILD_ADMIN = false;
let BUILD_CLIENT = false;
let BUILD_BACKEND = false;
let BUILD_DOCKER = false;

// 前端子项目目录（依赖检查与构建共用）
const ADMIN_DIR = path.join(ROOT_DIR, 'src', 'admin');
const CLIENT_DIR = path.join(ROOT_DIR, 'src', 'client');

// 构建各模块
async function buildAdmin() {
  console.log('==> 构建 admin 管理端...');
  await ensureNpmDependencies(ctx, ADMIN_DIR);
  await runWithRepair(ctx, 'npm', ['run', 'build'], { cwd: ADMIN_DIR, step: '构建 admin 管理端' });
}

async function buildClient() {
  console.log('==> 构建 client C 端 H5...');
  await ensureNpmDependencies(ctx, CLIENT_DIR);
  await runWithRepair(ctx, 'npm', ['run', 'build:h5'], { cwd: CLIENT_DIR, step: '构建 client C 端 H5' });
}

async function buildBackend() {
  console.log('==> 构建后端二进制（Linux arm64）...');
  // build.mjs 内部完成 Go 模块预检与自愈，这里透传开关
  const args = [path.join(ROOT_DIR, 'build.mjs'), ...(ctx.autoFix ? [] : ['--no-auto-fix'])];
  await runWithRepair(ctx, process.execPath, args, { step: '构建后端二进制', allowRepair: false });
}

async function buildDocker() {
  console.log('==> 构建 Docker 镜像...');
  await ensureGeoipDatabase(ctx);
  await buildComposeImages(ctx);
}

// 打包最终产物
function packageRelease() {
  console.log('');
  console.log('==> 打包发布产物...');
  fs.rmSync(DIST_DIR, { recursive: true, force: true });
  fs.mkdirSync(path.join(DIST_DIR, 'bin', 'static'), { recursive: true });

  // 复制二进制与静态资源：白名单口径，只入服务器所需的构建产物与配置。
  // 严禁整目录搬运 bin/——其中 data/ssl（密码传输 RSA 私钥）、runtime（支付配置等
  // 运行态回写）、logs、attachment（用户上传）均为本机运行数据，随包分发即泄漏密钥，
  // 服务器上应全新落盘（首启自动生成私钥、挂载目录自动创建）。
  // darwin/windows 产物与 bin 根下的本机调试二进制与 Linux 部署无关，不入包。
  const BIN_ALLOWLIST = new Set([
    'static',
    'linux-arm64',
    'linux-amd64',
    'config.yaml',
    'config.docker.yaml',
    'config.allinone.yaml',
  ]);
  if (fs.existsSync(path.join(ROOT_DIR, 'bin'))) {
    for (const name of fs.readdirSync(path.join(ROOT_DIR, 'bin'))) {
      if (!BIN_ALLOWLIST.has(name)) continue;
      fs.cpSync(path.join(ROOT_DIR, 'bin', name), path.join(DIST_DIR, 'bin', name), { recursive: true, force: true });
    }
  }

  // 复制部署相关文件
  fs.copyFileSync(path.join(ROOT_DIR, 'docker-compose.yml'), path.join(DIST_DIR, 'docker-compose.yml'));
  copyIfExists('start-all.mjs');
  copyIfExists('deploy.mjs');
  copyIfExists('build.mjs');
  // 依赖自愈工具被上面三个入口依赖，必须一并入包；
  // GeoIP 下载器为 Go 二进制，已随 bin/ 产物一并复制，无需单独打包
  copyIfExists('deps-tools.mjs');
  if (fs.existsSync(path.join(ROOT_DIR, 'deploy'))) {
    // init.dev.sql 是开发种子数据（超管 admin/admin123、C 端固定验证码 123456），
    // 不随发布包分发：生产环境一旦被手工导入即得到已知超管口令
    fs.cpSync(path.join(ROOT_DIR, 'deploy'), path.join(DIST_DIR, 'deploy'), {
      recursive: true,
      filter: (src) => path.basename(src) !== 'init.dev.sql',
    });
  }

  // 生成版本信息
  let commit = 'unknown';
  const g = spawnSync('git', ['rev-parse', '--short', 'HEAD'], { encoding: 'utf8', shell: needsShell('git') });
  if (g.status === 0 && g.stdout) commit = g.stdout.trim();
  fs.writeFileSync(DIST_DIR + path.sep + 'VERSION', [
    `build_time: ${new Date().toLocaleString('sv-SE').replace('T', ' ')}`,
    `commit: ${commit}`,
    'platform: linux/arm64',
    '',
  ].join('\n'));

  console.log(`    打包完成: ${DIST_DIR}`);
}

function copyIfExists(name) {
  const src = path.join(ROOT_DIR, name);
  if (fs.existsSync(src)) fs.copyFileSync(src, path.join(DIST_DIR, name));
}

// 交互式菜单
console.log('==============================================');
console.log('   社区商超 - 交互式编译打包脚本');
console.log('==============================================');
console.log('');
console.log('请选择要编译的模块（可多次选择，输入数字）：');
console.log('  依赖缺失时会自动补齐（npm install / go mod tidy），如需关闭请加 --no-auto-fix');
console.log('  1) admin 管理端');
console.log('  2) client C 端 H5');
console.log('  3) backend 后端服务');
console.log('  4) Docker 镜像');
console.log('  5) 全部');
console.log('  0) 退出');
console.log('');

const rl = readline.createInterface({ input: process.stdin, output: process.stdout });

while (true) {
  const choice = (await rl.question('请输入选项 [0-5]: ')).trim();
  if (choice === '1') BUILD_ADMIN = true;
  else if (choice === '2') BUILD_CLIENT = true;
  else if (choice === '3') BUILD_BACKEND = true;
  else if (choice === '4') BUILD_DOCKER = true;
  else if (choice === '5') {
    BUILD_ADMIN = BUILD_CLIENT = BUILD_BACKEND = BUILD_DOCKER = true;
    break;
  } else if (choice === '0') {
    console.log('已退出');
    rl.close();
    process.exit(0);
  } else {
    console.log('无效选项，请重新输入');
    continue;
  }

  const cont = (await rl.question('是否继续选择？(y/n): ')).trim();
  if (/^[Nn]$/.test(cont)) break;
  console.log('');
}

const doPackage = (await rl.question('是否打包最终产物到 bin/release/ 目录？(y/n): ')).trim();
rl.close();

// 执行构建
if (BUILD_ADMIN) await buildAdmin();
if (BUILD_CLIENT) await buildClient();
if (BUILD_BACKEND) await buildBackend();
if (BUILD_DOCKER) await buildDocker();

// 执行打包
if (/^[Yy]$/.test(doPackage)) packageRelease();

console.log('');
console.log('==============================================');
console.log('编译打包流程结束');
console.log('==============================================');
