#!/bin/bash
# 社区商超生产发布构建脚本
# 1. 安装前端依赖（若缺失）
# 2. 构建前端（管理端 + C 端 H5）
# 3. 构建后端二进制（Linux arm64 交叉编译）
# 4. 构建 Docker 镜像（最终镜像仅包含编译产物，不含源码）
#
# 使用方法：
#   ./deploy.sh              # 完整构建
#   ./deploy.sh --up         # 构建完成后启动服务
#   ./deploy.sh --no-cache   # 构建镜像时不使用缓存

set -e

ROOT_DIR=$(cd "$(dirname "$0")" && pwd)

# 解析可选参数
UP_AFTER_BUILD=false
NO_CACHE=""
for arg in "$@"; do
  case "$arg" in
    --up) UP_AFTER_BUILD=true ;;
    --no-cache) NO_CACHE="--no-cache" ;;
    *) echo "未知参数: $arg"; exit 1 ;;
  esac
done

# 安装前端依赖的辅助函数
install_deps_if_needed() {
  local dir="$1"
  if [ ! -d "${dir}/node_modules" ]; then
    echo "==> 检测到 ${dir} 缺少 node_modules，正在安装依赖..."
    (cd "${dir}" && npm install)
  fi
}

echo "==> 构建管理端..."
install_deps_if_needed "${ROOT_DIR}/src/admin"
cd "${ROOT_DIR}/src/admin"
npm run build

echo "==> 构建 C 端 H5..."
install_deps_if_needed "${ROOT_DIR}/src/client"
cd "${ROOT_DIR}/src/client"
npm run build:h5

echo "==> 构建后端二进制（Linux arm64 交叉编译）..."
cd "${ROOT_DIR}"
bash "${ROOT_DIR}/build.sh"

echo "==> 准备本地附件共享目录（不存在时自动创建）..."
mkdir -p "${ROOT_DIR}/bin/attachment"

echo "==> 构建 Docker 镜像..."
cd "${ROOT_DIR}"
docker compose build ${NO_CACHE}

echo "==> 发布构建完成"
echo "    镜像列表："
docker images | grep market || true

if [ "$UP_AFTER_BUILD" = true ]; then
  echo "==> 启动服务..."
  docker compose up -d
fi
