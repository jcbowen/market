#!/usr/bin/env node
// start-all.mjs
// 一键启动整个项目：依赖检查 -> 前后端构建 -> 镜像构建 -> 启动服务 -> 初始化数据
//
// 使用方法：
//   node start-all.mjs              # 常规启动（保留已有数据）
//   node start-all.mjs --clean      # 清理 MySQL/Redis 数据后重新启动
//   node start-all.mjs --skip-build # 跳过构建，直接使用已有镜像/二进制
//   node start-all.mjs --no-cache   # 构建镜像时不使用 Docker 缓存
//   node start-all.mjs --no-auto-fix # 依赖不全时只提示原因，不自动执行 go mod tidy / npm install
//
// 依赖不全的处理由 deps-tools.mjs 统一负责：
//   前端 node_modules 缺失或与 package-lock 不一致 -> 自动 npm install；
//   go.mod/go.sum 不一致（missing go.sum entry 等）-> 自动 go mod tidy 后重试；
//   replace 指向的本地模块缺失 -> 直接报出期望路径，交人工准备。

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import {
  buildComposeImages,
  createContext,
  ensureGeoipDatabase,
  ensureNpmDependencies,
  needsShell,
  runWithRepair,
} from './deps-tools.mjs';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

let CLEAN = false;
let SKIP_BUILD = false;
let NO_CACHE = false;
let AUTO_FIX = true;
for (const arg of process.argv.slice(2)) {
  if (arg === '--clean') CLEAN = true;
  else if (arg === '--skip-build') SKIP_BUILD = true;
  else if (arg === '--no-cache') NO_CACHE = true;
  else if (arg === '--no-auto-fix') AUTO_FIX = false;
  else {
    console.error(`未知参数: ${arg}`);
    process.exit(1);
  }
}

const ctx = createContext({ rootDir: ROOT_DIR, autoFix: AUTO_FIX });

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, {
    stdio: 'inherit',
    cwd: opts.cwd || ROOT_DIR,
    shell: needsShell(cmd),
  });
  if (r.status !== 0) process.exit(r.status ?? 1);
}

// 依赖检查（versionArgs 用于兼容不同 CLI 的版本命令，如 go 只支持 `go version`）
function checkCommand(cmd, name, versionArgs = ['--version']) {
  const r = spawnSync(cmd, versionArgs, {
    encoding: 'utf8',
    shell: needsShell(cmd),
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
    shell: needsShell('docker'),
  });
  if (r.status !== 0) {
    console.error('错误: Docker Compose 子命令不可用，请安装/启用 Docker Compose 插件（docker compose）');
    process.exit(1);
  }
}

// Docker 守护进程检测与自动启动
function dockerDaemonRunning() {
  const r = spawnSync('docker', ['info'], {
    encoding: 'utf8',
    shell: needsShell('docker'),
  });
  return r.status === 0;
}

async function ensureDockerRunning() {
  if (dockerDaemonRunning()) return;

  console.log('==> Docker 未运行，正在启动 Docker Desktop...');
  if (process.platform === 'darwin') {
    // macOS：依次尝试常见的应用名
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
    // Linux：尝试 systemd 启动 docker 服务
    const r = spawnSync('sudo', ['systemctl', 'start', 'docker'], { stdio: 'inherit' });
    if (r.status !== 0) {
      console.error('错误: 无法启动 docker 服务，请手动启动后重试');
      process.exit(1);
    }
  }

  // 等待守护进程就绪（Docker Desktop 冷启动可能需要较长时间）
  console.log('==> 等待 Docker 就绪...');
  for (let i = 1; i <= 120; i++) {
    if (dockerDaemonRunning()) {
      console.log('    Docker 已就绪');
      return;
    }
    await sleep(1000);
    if (i % 15 === 0) console.log(`    仍在等待 Docker 启动... (${i}s)`);
  }
  console.error('错误: Docker 启动超时（120 秒），请手动启动 Docker Desktop 后重试');
  process.exit(1);
}

checkCommand('docker', 'Docker');
await ensureDockerRunning();
checkDockerCompose();
checkCommand('go', 'Go', ['version']);
checkCommand('node', 'Node.js');
checkCommand('npm', 'npm');

// 前端子项目目录（依赖检查与构建共用）
const ADMIN_DIR = path.join(ROOT_DIR, 'src', 'admin');
const CLIENT_DIR = path.join(ROOT_DIR, 'src', 'client');

// docker exec 辅助（静默执行，返回是否成功）
function dockerExec(args, env = {}) {
  const r = spawnSync('docker', ['exec', ...args], {
    encoding: 'utf8',
    shell: needsShell('docker'),
    env: { ...process.env, ...env },
  });
  return { ok: r.status === 0, stdout: r.stdout || '' };
}

// 从项目根目录 .env 读取 KEY=VALUE（compose 同源口令：MySQL root 密码等）
// 返回：
//   - Map<string, string>: 解析出的键值对（忽略空行与 # 注释）
function loadDotEnv() {
  const envFile = path.join(ROOT_DIR, '.env');
  const map = new Map();
  if (!fs.existsSync(envFile)) return map;
  for (const line of fs.readFileSync(envFile, 'utf8').split(/\r?\n/)) {
    const m = line.match(/^\s*([A-Z0-9_]+)\s*=\s*(.*)\s*$/);
    if (m && !line.trim().startsWith('#')) map.set(m[1], m[2].replace(/^["']|["']$/g, ''));
  }
  return map;
}

const dotenv = loadDotEnv();

// 从 bin/config.docker.yaml 解析网关对外端口（web_server.port）。
// compose 中 gateway 的宿主/容器为同端口映射，配置文件是唯一事实来源，
// 避免启动成功提示中的 URL 与实际端口脱节。
// 返回：
//   - string: 网关端口（解析失败时回退默认值 18080）
function loadGatewayPort() {
  const configFile = path.join(ROOT_DIR, 'bin', 'config.docker.yaml');
  try {
    const m = fs.readFileSync(configFile, 'utf8').match(/^web_server:\s*$(?:\s*\n)?\s*port:\s*(\d+)/m);
    if (m) return m[1];
  } catch { /* 配置缺失或读取失败时使用默认端口 */ }
  return '18080';
}

// 开发测试账号（与 deploy/init.dev.sql 头注释同步维护；库内密码为 bcrypt 哈希，
// 明文仅存在于 SQL 注释与文档，此处常量化集中管理，修改测试数据时两处同步）
const DEV_ADMIN_ACCOUNT = 'admin / admin123';
const DEV_USER_ACCOUNT = '13800138000 / 验证码 123456';

// MySQL root 密码与 docker-compose.yml 同源（.env 注入），缺失时提前报错，
// 避免 mysql 客户端拿空密码反复失败或误用历史弱口令
const MYSQL_ROOT_PASSWORD = dotenv.get('MYSQL_ROOT_PASSWORD') || process.env.MYSQL_ROOT_PASSWORD || '';
if (!MYSQL_ROOT_PASSWORD) {
  console.error('错误: 未在项目根目录 .env 中配置 MYSQL_ROOT_PASSWORD（docker-compose.yml 同源要求）');
  process.exit(1);
}

// mysql 口令经 docker exec 的环境继承传入（-e MYSQL_PWD 不带值，取值自本进程 env），
// 不再写 -p<口令> 到命令行：命令行参数在宿主 ps/进程列表里明文可见，
// Windows 下还会被拼进 shell 字符串
const MYSQL_EXEC_ARGS = ['-e', 'MYSQL_PWD'];
const MYSQL_EXEC_ENV = { MYSQL_PWD: MYSQL_ROOT_PASSWORD };

// 本地附件共享目录（Docker bind mount 挂载点）：
// product-service 落盘与 gateway /uploads 静态服务共用，宿主目录不存在时先创建，
// 避免首次部署或目录被清理后挂载异常
fs.mkdirSync(path.join(ROOT_DIR, 'bin', 'attachment'), { recursive: true });

// 构建阶段
let FRONTEND_REBUILT = false;
if (!SKIP_BUILD) {
  console.log('==> 构建管理端...');
  await ensureNpmDependencies(ctx, ADMIN_DIR);
  await runWithRepair(ctx, 'npm', ['run', 'build'], { cwd: ADMIN_DIR, step: '构建管理端' });

  console.log('==> 构建 C 端 H5...');
  await ensureNpmDependencies(ctx, CLIENT_DIR);
  await runWithRepair(ctx, 'npm', ['run', 'build:h5'], { cwd: CLIENT_DIR, step: '构建 C 端 H5' });
  FRONTEND_REBUILT = true;

  console.log('==> 构建后端二进制（Linux arm64）...');
  // 依赖预检与 go mod tidy 自愈在 build.mjs 内部完成，这里透传开关保持一致
  const backendArgs = [path.join(ROOT_DIR, 'build.mjs'), ...(AUTO_FIX ? [] : ['--no-auto-fix'])];
  await runWithRepair(ctx, process.execPath, backendArgs, { step: '构建后端二进制', allowRepair: false });

  console.log('==> 构建 Docker 镜像...');
  await buildComposeImages(ctx, NO_CACHE ? ['--no-cache'] : []);
} else {
  console.log('==> 跳过构建阶段，使用已有镜像/二进制');
}

// GeoIP 定位库属于可选运行数据，缺失时补下载，失败只告警不阻断启动
await ensureGeoipDatabase(ctx);

// 清理仅在 --clean 时执行；默认保留 MySQL/Redis 容器运行状态，
// 业务服务容器由下方 up -d 依据镜像变化自动按需重建
if (CLEAN) {
  console.log('==> 清理旧容器与数据卷...');
  run('docker', ['compose', 'down', '-v']);
}

// 启动服务
console.log('==> 启动服务...');
await runWithRepair(ctx, 'docker', ['compose', 'up', '-d'], { cwd: ROOT_DIR, step: '启动服务' });

// 前端构建脚本会先 rm -rf 产物目录，仍在运行的 gateway 容器绑定的是被删掉的旧 inode，
// 挂载点因此变成空目录（表现为 /s/ 404）；restart 让容器重新解析 bind mount
if (FRONTEND_REBUILT) {
  console.log('==> 重新挂载前端产物（重启 Gateway）...');
  await runWithRepair(ctx, 'docker', ['compose', 'restart', 'gateway'], { cwd: ROOT_DIR, step: '重启 Gateway' });
}

// 等待 MySQL 健康
console.log('==> 等待 MySQL 就绪...');
for (let i = 1; i <= 60; i++) {
  if (dockerExec([...MYSQL_EXEC_ARGS, 'market-mysql', 'mysqladmin', '-uroot', 'ping'], MYSQL_EXEC_ENV).ok) {
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
    ...MYSQL_EXEC_ARGS,
    '-i', 'market-mysql', 'mysql', '-uroot', 'market',
    '--default-character-set=utf8mb4', '-e', 'SELECT 1 FROM categories LIMIT 1',
  ], MYSQL_EXEC_ENV).ok) {
    console.log('    数据库表已就绪');
    break;
  }
  await sleep(1000);
  if (i === 60) {
    console.log('警告: 等待数据库表就绪超时，继续执行初始化');
  }
}

// 初始化测试数据（仅开发环境：脚本含弱口令管理员与测试业务数据，生产环境禁止导入）
console.log('==> 初始化测试数据...');
if (dotenv.get('APP_ENV') === 'production' || process.env.APP_ENV === 'production') {
  console.log('    已跳过：APP_ENV=production 生产环境禁止导入开发测试数据（deploy/init.dev.sql）');
} else {
  const exists = dockerExec([
    ...MYSQL_EXEC_ARGS,
    '-i', 'market-mysql', 'mysql', '-uroot', 'market',
    '--default-character-set=utf8mb4', '-N', '-s', '-e', 'SELECT id FROM communities LIMIT 1',
  ], MYSQL_EXEC_ENV);
  if (exists.ok && /^\d+\s*$/m.test(exists.stdout.trim())) {
    console.log('    测试数据已存在，跳过初始化');
  } else {
    const sql = fs.readFileSync(path.join(ROOT_DIR, 'deploy', 'init.dev.sql'), 'utf8');
    const r = spawnSync('docker', [
      'exec', ...MYSQL_EXEC_ARGS, '-i', 'market-mysql', 'mysql', '-uroot', 'market',
      '--default-character-set=utf8mb4',
    ], {
      input: sql,
      encoding: 'utf8',
      shell: needsShell('docker'),
      env: { ...process.env, ...MYSQL_EXEC_ENV },
    });
    if (r.status === 0) {
      console.log('    测试数据初始化完成');
    } else {
      console.log('警告: 测试数据初始化失败');
    }
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
const gatewayPort = loadGatewayPort();
console.log(`  管理后台: http://localhost:${gatewayPort}/b/`);
console.log(`  C 端 H5:  http://localhost:${gatewayPort}/s/`);
console.log(`  健康检查: http://localhost:${gatewayPort}/health`);
console.log(`  默认管理员: ${DEV_ADMIN_ACCOUNT}`);
console.log(`  默认用户:   ${DEV_USER_ACCOUNT}`);
console.log('==============================================');
