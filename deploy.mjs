#!/usr/bin/env node
// 社区商超生产发布构建脚本
// 1. 安装前端依赖（若缺失）
// 2. 构建前端（管理端 + C 端 H5）
// 3. 构建后端二进制（Linux arm64 交叉编译）
// 4. 构建 Docker 镜像（最终镜像仅包含编译产物，不含源码）
//
// 使用方法：
//   node deploy.mjs              # 完整构建
//   node deploy.mjs --up         # 构建完成后启动服务
//   node deploy.mjs --no-cache   # 构建镜像时不使用缓存

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT_DIR = path.dirname(fileURLToPath(import.meta.url));

// 解析可选参数
let UP_AFTER_BUILD = false;
let NO_CACHE = false;
for (const arg of process.argv.slice(2)) {
  if (arg === '--up') UP_AFTER_BUILD = true;
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

// 安装前端依赖（若缺失）
function installDepsIfNeeded(dir) {
  if (!fs.existsSync(path.join(dir, 'node_modules'))) {
    console.log(`==> 检测到 ${dir} 缺少 node_modules，正在安装依赖...`);
    run('npm', ['install'], { cwd: dir });
  }
}

console.log('==> 构建管理端...');
installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'admin'));
run('npm', ['run', 'build'], { cwd: path.join(ROOT_DIR, 'src', 'admin') });

console.log('==> 构建 C 端 H5...');
installDepsIfNeeded(path.join(ROOT_DIR, 'src', 'client'));
run('npm', ['run', 'build:h5'], { cwd: path.join(ROOT_DIR, 'src', 'client') });

console.log('==> 构建后端二进制（Linux arm64 交叉编译）...');
run(process.execPath, [path.join(ROOT_DIR, 'build.mjs')]);

console.log('==> 准备本地附件共享目录（不存在时自动创建）...');
fs.mkdirSync(path.join(ROOT_DIR, 'bin', 'attachment'), { recursive: true });

console.log('==> 构建 Docker 镜像...');
run('docker', ['compose', 'build', ...(NO_CACHE ? ['--no-cache'] : [])]);

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
  run('docker', ['compose', 'up', '-d']);
}
