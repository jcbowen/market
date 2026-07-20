#!/bin/bash
# build-select.sh
# 交互式选择编译部分并最终打包
#
# 支持选择：
#   1. admin 管理端
#   2. client C 端 H5
#   3. backend 后端服务
#   4. docker 镜像
#   5. 全部
#   0. 退出

set -e

ROOT_DIR=$(cd "$(dirname "$0")" && pwd)
DIST_DIR="${ROOT_DIR}/bin/release"

# 菜单项
BUILD_ADMIN=false
BUILD_CLIENT=false
BUILD_BACKEND=false
BUILD_DOCKER=false

# 安装前端依赖的辅助函数
install_deps_if_needed() {
  local dir="$1"
  if [ ! -d "${dir}/node_modules" ]; then
    echo "==> ${dir} 缺少 node_modules，正在安装依赖..."
    (cd "${dir}" && npm install)
  fi
}

# 构建各模块
build_admin() {
  echo "==> 构建 admin 管理端..."
  install_deps_if_needed "${ROOT_DIR}/src/admin"
  (cd "${ROOT_DIR}/src/admin" && npm run build)
}

build_client() {
  echo "==> 构建 client C 端 H5..."
  install_deps_if_needed "${ROOT_DIR}/src/client"
  (cd "${ROOT_DIR}/src/client" && npm run build:h5)
}

build_backend() {
  echo "==> 构建后端二进制（Linux arm64）..."
  (cd "${ROOT_DIR}" && bash "${ROOT_DIR}/build.sh")
}

build_docker() {
  echo "==> 构建 Docker 镜像..."
  (cd "${ROOT_DIR}" && docker compose build)
}

# 打包最终产物
package_release() {
  echo ""
  echo "==> 打包发布产物..."
  rm -rf "${DIST_DIR}"
  mkdir -p "${DIST_DIR}/bin/static"

  # 复制二进制与静态资源
  if [ -d "${ROOT_DIR}/bin" ]; then
    cp -r "${ROOT_DIR}/bin/"* "${DIST_DIR}/bin/" 2>/dev/null || true
  fi

  # 复制部署相关文件
  cp "${ROOT_DIR}/docker-compose.yml" "${DIST_DIR}/"
  cp "${ROOT_DIR}/start-all.sh" "${DIST_DIR}/"
  cp "${ROOT_DIR}/deploy.sh" "${DIST_DIR}/" 2>/dev/null || true
  cp "${ROOT_DIR}/build.sh" "${DIST_DIR}/" 2>/dev/null || true
  cp -r "${ROOT_DIR}/deploy" "${DIST_DIR}/" 2>/dev/null || true

  # 生成版本信息
  cat > "${DIST_DIR}/VERSION" <<EOF
build_time: $(date '+%Y-%m-%d %H:%M:%S')
commit: $(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')
platform: linux/arm64
EOF

  echo "    打包完成: ${DIST_DIR}"
}

# 交互式菜单
echo "=============================================="
echo "   社区商超 - 交互式编译打包脚本"
echo "=============================================="
echo ""
echo "请选择要编译的模块（可多次选择，输入数字）："
echo "  1) admin 管理端"
echo "  2) client C 端 H5"
echo "  3) backend 后端服务"
echo "  4) Docker 镜像"
echo "  5) 全部"
echo "  0) 退出"
echo ""

while true; do
  read -rp "请输入选项 [0-5]: " choice
  case "$choice" in
    1) BUILD_ADMIN=true ;;
    2) BUILD_CLIENT=true ;;
    3) BUILD_BACKEND=true ;;
    4) BUILD_DOCKER=true ;;
    5)
      BUILD_ADMIN=true
      BUILD_CLIENT=true
      BUILD_BACKEND=true
      BUILD_DOCKER=true
      break
      ;;
    0) echo "已退出"; exit 0 ;;
    *) echo "无效选项，请重新输入" ;;
  esac

  read -rp "是否继续选择？(y/n): " cont
  if [[ "$cont" =~ ^[Nn]$ ]]; then
    break
  fi
  echo ""
done

# 确认是否打包
read -rp "是否打包最终产物到 bin/release/ 目录？(y/n): " do_package

# 执行构建
if [ "$BUILD_ADMIN" = true ]; then build_admin; fi
if [ "$BUILD_CLIENT" = true ]; then build_client; fi
if [ "$BUILD_BACKEND" = true ]; then build_backend; fi
if [ "$BUILD_DOCKER" = true ]; then build_docker; fi

# 执行打包
if [[ "$do_package" =~ ^[Yy]$ ]]; then
  package_release
fi

echo ""
echo "=============================================="
echo "编译打包流程结束"
echo "=============================================="
