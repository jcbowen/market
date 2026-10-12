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

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline/promises';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));
const DIST_DIR = path.join(ROOT_DIR, 'bin', 'release');

// 菜单项
let BUILD_ADMIN = false;
let BUILD_CLIENT = false;
let BUILD_BACKEND = false;
let BUILD_DOCKER = false;

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, {
    stdio: 'inherit',
    cwd: opts.cwd || ROOT_DIR,
    shell: process.platform === 'win32',
  });
  if (r.status !== 0) process.exit(r.status ?? 1);
}

// 安装前端依赖（若缺失）
function installDepsIfNeeded(dir) {
  if (!fs.existsSync(path.join(dir, 'node_modules'))) {
    console.log(`==> ${dir} 缺少 node_modules，正在安装依赖...`);
    run('npm', ['install'], { cwd: dir });
  }
}

// 构建各模块
async function buildAdmin() {
  console.log('==> 构建 admin 管理端...');
  installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'admin'));
  run('npm', ['run', 'build'], { cwd: path.join(ROOT_DIR, 'src', 'admin') });
}

async function buildClient() {
  console.log('==> 构建 client C 端 H5...');
  installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'client'));
  run('npm', ['run', 'build:h5'], { cwd: path.join(ROOT_DIR, 'src', 'client') });
}

function buildBackend() {
  console.log('==> 构建后端二进制（Linux arm64）...');
  run(process.execPath, [path.join(ROOT_DIR, 'build.mjs')]);
}

function buildDocker() {
  console.log('==> 构建 Docker 镜像...');
  run('docker', ['compose', 'build']);
}

// 打包最终产物
function packageRelease() {
  console.log('');
  console.log('==> 打包发布产物...');
  fs.rmSync(DIST_DIR, { recursive: true, force: true });
  fs.mkdirSync(path.join(DIST_DIR, 'bin', 'static'), { recursive: true });

  // 复制二进制与静态资源
  if (fs.existsSync(path.join(ROOT_DIR, 'bin'))) {
    for (const name of fs.readdirSync(path.join(ROOT_DIR, 'bin'))) {
      fs.cpSync(path.join(ROOT_DIR, 'bin', name), path.join(DIST_DIR, 'bin', name), { recursive: true, force: true });
    }
  }

  // 复制部署相关文件
  fs.copyFileSync(path.join(ROOT_DIR, 'docker-compose.yml'), path.join(DIST_DIR, 'docker-compose.yml'));
  copyIfExists('start-all.mjs');
  copyIfExists('deploy.mjs');
  copyIfExists('build.mjs');
  if (fs.existsSync(path.join(ROOT_DIR, 'deploy'))) {
    fs.cpSync(path.join(ROOT_DIR, 'deploy'), path.join(DIST_DIR, 'deploy'), { recursive: true });
  }

  // 生成版本信息
  let commit = 'unknown';
  const g = spawnSync('git', ['rev-parse', '--short', 'HEAD'], { encoding: 'utf8', shell: process.platform === 'win32' });
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
if (BUILD_BACKEND) buildBackend();
if (BUILD_DOCKER) buildDocker();

// 执行打包
if (/^[Yy]$/.test(doPackage)) packageRelease();

console.log('');
console.log('==============================================');
console.log('编译打包流程结束');
console.log('==============================================');
