#!/usr/bin/env node
// start-all.mjs
// 一键启动整个项目：依赖检查 -> 前后端构建 -> 镜像构建 -> 启动服务 -> 初始化数据
//
// 使用方法：
//   node start-all.mjs              # 常规启动（保留已有数据）
//   node start-all.mjs --clean      # 清理 MySQL/Redis 数据后重新启动
//   node start-all.mjs --skip-build # 跳过构建，直接使用已有镜像/二进制
//   node start-all.mjs --no-cache   # 构建镜像时不使用 Docker 缓存

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

let CLEAN = false;
let SKIP_BUILD = false;
let NO_CACHE = false;
for (const arg of process.argv.slice(2)) {
  if (arg === '--clean') CLEAN = true;
  else if (arg === '--skip-build') SKIP_BUILD = true;
  else if (arg === '--no-cache') NO_CACHE = true;
  else {
    console.error(`未知参数: ${arg}`);
    process.exit(1);
  }
}

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, {
    stdio: 'inherit',
    cwd: opts.cwd || ROOT_DIR,
    shell: process.platform === 'win32',
  });
  if (r.status !== 0) process.exit(r.status ?? 1);
}

// 依赖检查
function checkCommand(cmd, name) {
  const r = spawnSync(cmd, ['--version'], {
    encoding: 'utf8',
    shell: process.platform === 'win32',
  });
  if (r.status !== 0) {
    console.error(`错误: 未找到 ${cmd}，请先安装 ${name}`);
    process.exit(1);
  }
}

// 检查 Docker Compose 子命令是否可用（docker 已存在时调用，用于区分「docker 缺失」与「compose 插件缺失」两种情况）
function checkDockerCompose() {
  const r = spawnSync('docker', ['compose', 'version'], {
    encoding: 'utf8',
    shell: process.platform === 'win32',
  });
  if (r.status !== 0) {
    console.error('错误: Docker Compose 子命令不可用，请安装/启用 Docker Compose 插件（docker compose）');
    process.exit(1);
  }
}

checkCommand('docker', 'Docker');
checkDockerCompose();
checkCommand('go', 'Go');
checkCommand('node', 'Node.js');
checkCommand('npm', 'npm');

// 安装前端依赖（若缺失）
function installDepsIfNeeded(dir) {
  if (!fs.existsSync(path.join(dir, 'node_modules'))) {
    console.log(`==> ${dir} 缺少 node_modules，正在安装依赖...`);
    run('npm', ['install'], { cwd: dir });
  }
}

// docker exec 辅助（静默执行，返回是否成功）
function dockerExec(args) {
  const r = spawnSync('docker', ['exec', ...args], {
    encoding: 'utf8',
    shell: process.platform === 'win32',
  });
  return { ok: r.status === 0, stdout: r.stdout || '' };
}

// 本地附件共享目录（Docker bind mount 挂载点）：
// product-service 落盘与 gateway /uploads 静态服务共用，宿主目录不存在时先创建，
// 避免首次部署或目录被清理后挂载异常
fs.mkdirSync(path.join(ROOT_DIR, 'bin', 'attachment'), { recursive: true });

// 构建阶段
if (!SKIP_BUILD) {
  console.log('==> 构建管理端...');
  installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'admin'));
  run('npm', ['run', 'build'], { cwd: path.join(ROOT_DIR, 'src', 'admin') });

  console.log('==> 构建 C 端 H5...');
  installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'client'));
  run('npm', ['run', 'build:h5'], { cwd: path.join(ROOT_DIR, 'src', 'client') });

  console.log('==> 构建后端二进制（Linux arm64）...');
  run(process.execPath, [path.join(ROOT_DIR, 'build.mjs')]);

  console.log('==> 构建 Docker 镜像...');
  run('docker', ['compose', 'build', ...(NO_CACHE ? ['--no-cache'] : [])]);
} else {
  console.log('==> 跳过构建阶段，使用已有镜像/二进制');
}

// 清理仅在 --clean 时执行；默认保留 MySQL/Redis 容器运行状态，
// 业务服务容器由下方 up -d 依据镜像变化自动按需重建
if (CLEAN) {
  console.log('==> 清理旧容器与数据卷...');
  run('docker', ['compose', 'down', '-v']);
}

// 启动服务
console.log('==> 启动服务...');
run('docker', ['compose', 'up', '-d']);

// 等待 MySQL 健康
console.log('==> 等待 MySQL 就绪...');
for (let i = 1; i <= 60; i++) {
  if (dockerExec(['market-mysql', 'mysqladmin', '-uroot', '-pmarket123', 'ping']).ok) {
    console.log('    MySQL 已就绪');
    break;
  }
  await sleep(1000);
  if (i === 60) {
    console.error('错误: MySQL 启动超时');
    process.exit(1);
  }
}

// 等待后端服务完成表迁移
console.log('==> 等待微服务完成自动迁移...');
for (let i = 1; i <= 60; i++) {
  if (dockerExec([
    '-i', 'market-mysql', 'mysql', '-uroot', '-pmarket123', 'market',
    '--default-character-set=utf8mb4', '-e', 'SELECT 1 FROM categories LIMIT 1',
  ]).ok) {
    console.log('    数据库表已就绪');
    break;
  }
  await sleep(1000);
  if (i === 60) {
    console.log('警告: 等待数据库表就绪超时，继续执行初始化');
  }
}

// 初始化测试数据
console.log('==> 初始化测试数据...');
const exists = dockerExec([
  '-i', 'market-mysql', 'mysql', '-uroot', '-pmarket123', 'market',
  '--default-character-set=utf8mb4', '-N', '-s', '-e', 'SELECT id FROM communities LIMIT 1',
]);
if (exists.ok && /^\d+\s*$/m.test(exists.stdout.trim())) {
  console.log('    测试数据已存在，跳过初始化');
} else {
  const sql = fs.readFileSync(path.join(ROOT_DIR, 'deploy', 'init.sql'), 'utf8');
  const r = spawnSync('docker', [
    'exec', '-i', 'market-mysql', 'mysql', '-uroot', '-pmarket123', 'market',
    '--default-character-set=utf8mb4',
  ], { input: sql, encoding: 'utf8', shell: process.platform === 'win32' });
  if (r.status === 0) {
    console.log('    测试数据初始化完成');
  } else {
    console.log('警告: 测试数据初始化失败');
  }
}

// 等待 Gateway 健康
console.log('==> 等待 Gateway 就绪...');
for (let i = 1; i <= 60; i++) {
  try {
    const res = await fetch('http://localhost:18080/health');
    if (res.status === 200) {
      console.log('    Gateway 已就绪');
      break;
    }
  } catch { /* 未就绪，继续等待 */ }
  await sleep(1000);
  if (i === 60) {
    console.error('错误: Gateway 启动超时');
    process.exit(1);
  }
}

console.log('');
console.log('==============================================');
console.log('项目启动成功！');
console.log('  管理后台: http://localhost:18080/b/');
console.log('  C 端 H5:  http://localhost:18080/s/');
console.log('  健康检查: http://localhost:18080/health');
console.log('  默认管理员: admin / admin123');
console.log('  默认用户:   13800138000 / 验证码 123456');
console.log('==============================================');
