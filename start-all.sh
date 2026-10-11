#!/bin/bash
# start-all.sh
# 一键启动整个项目：依赖检查 -> 前后端构建 -> 镜像构建 -> 启动服务 -> 初始化数据
#
# 使用方法：
#   ./start-all.sh              # 常规启动（保留已有数据）
#   ./start-all.sh --clean      # 清理 MySQL/Redis 数据后重新启动
#   ./start-all.sh --skip-build # 跳过构建，直接使用已有镜像/二进制
#   ./start-all.sh --no-cache   # 构建镜像时不使用 Docker 缓存

set -e

ROOT_DIR=$(cd "$(dirname "$0")" && pwd)
CLEAN=false
SKIP_BUILD=false
NO_CACHE=""

for arg in "$@"; do
  case "$arg" in
    --clean) CLEAN=true ;;
    --skip-build) SKIP_BUILD=true ;;
    --no-cache) NO_CACHE="--no-cache" ;;
    *) echo "未知参数: $arg"; exit 1 ;;
  esac
done

# 检查命令是否存在
check_command() {
  if ! command -v "$1" &>/dev/null; then
    echo "错误: 未找到 $1，请先安装 $2"
    exit 1
  fi
}

check_command docker "Docker"
check_command docker "Docker Compose"
check_command go "Go"
check_command node "Node.js"
check_command npm "npm"

# 安装前端依赖的辅助函数
install_deps_if_needed() {
  local dir="$1"
  if [ ! -d "${dir}/node_modules" ]; then
    echo "==> ${dir} 缺少 node_modules，正在安装依赖..."
    (cd "${dir}" && npm install)
  fi
}

# 本地附件共享目录（Docker bind mount 挂载点）：
# product-service 落盘与 gateway /uploads 静态服务共用，宿主目录不存在时先创建，
# 避免首次部署或目录被清理后挂载异常
mkdir -p "${ROOT_DIR}/bin/attachment"

# 构建阶段
if [ "$SKIP_BUILD" = false ]; then
  echo "==> 构建管理端..."
  install_deps_if_needed "${ROOT_DIR}/src/admin"
  (cd "${ROOT_DIR}/src/admin" && npm run build)

  echo "==> 构建 C 端 H5..."
  install_deps_if_needed "${ROOT_DIR}/src/client"
  (cd "${ROOT_DIR}/src/client" && npm run build:h5)

  echo "==> 构建后端二进制（Linux arm64）..."
  (cd "${ROOT_DIR}" && bash "${ROOT_DIR}/build.sh")

  echo "==> 构建 Docker 镜像..."
  (cd "${ROOT_DIR}" && docker compose build ${NO_CACHE})
else
  echo "==> 跳过构建阶段，使用已有镜像/二进制"
fi

# 停止并清理旧容器
if [ "$CLEAN" = true ]; then
  echo "==> 清理旧容器与数据卷..."
  (cd "${ROOT_DIR}" && docker compose down -v)
else
  echo "==> 停止旧容器..."
  (cd "${ROOT_DIR}" && docker compose down)
fi

# 启动服务
echo "==> 启动服务..."
(cd "${ROOT_DIR}" && docker compose up -d)

# 等待 MySQL 健康
echo "==> 等待 MySQL 就绪..."
for i in {1..60}; do
  if docker exec market-mysql mysqladmin -uroot -pmarket123 ping &>/dev/null; then
    echo "    MySQL 已就绪"
    break
  fi
  sleep 1
  if [ "$i" -eq 60 ]; then
    echo "错误: MySQL 启动超时"
    exit 1
  fi
done

# 等待后端服务完成表迁移
echo "==> 等待微服务完成自动迁移..."
for i in {1..60}; do
  if docker exec -i market-mysql mysql -uroot -pmarket123 market --default-character-set=utf8mb4 -e "SELECT 1 FROM categories LIMIT 1" &>/dev/null; then
    echo "    数据库表已就绪"
    break
  fi
  sleep 1
  if [ "$i" -eq 60 ]; then
    echo "警告: 等待数据库表就绪超时，继续执行初始化"
  fi
done

# 初始化测试数据
echo "==> 初始化测试数据..."
if docker exec -i market-mysql mysql -uroot -pmarket123 market --default-character-set=utf8mb4 -N -s -e "SELECT id FROM communities LIMIT 1" 2>/dev/null | grep -q '^[0-9]\+$'; then
  echo "    测试数据已存在，跳过初始化"
elif docker exec -i market-mysql mysql -uroot -pmarket123 market --default-character-set=utf8mb4 < "${ROOT_DIR}/deploy/init.sql"; then
  echo "    测试数据初始化完成"
else
  echo "警告: 测试数据初始化失败"
fi

# 等待 gateway 健康
echo "==> 等待 Gateway 就绪..."
for i in {1..60}; do
  if curl -s -o /dev/null -w '%{http_code}' http://localhost:18080/health | grep -q '^200$'; then
    echo "    Gateway 已就绪"
    break
  fi
  sleep 1
  if [ "$i" -eq 60 ]; then
    echo "错误: Gateway 启动超时"
    exit 1
  fi
done

echo ""
echo "=============================================="
echo "项目启动成功！"
echo "  管理后台: http://localhost:18080/b/"
echo "  C 端 H5:  http://localhost:18080/s/"
echo "  健康检查: http://localhost:18080/health"
echo "  默认管理员: admin / admin123"
echo "  默认用户:   13800138000 / 验证码 123456"
echo "=============================================="
