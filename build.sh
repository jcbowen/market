#!/bin/bash
# 社区商超后端构建脚本（三平台交叉编译）
#
# 1. 交叉编译 gateway 及各微服务的 Windows / macOS / Linux 二进制，
#    按平台存放到 bin/ 下的二级目录：
#      bin/darwin/    macOS (arm64)
#      bin/linux/     Linux (arm64)
#      bin/windows/   Windows (amd64，.exe 后缀)
# 2. 编译完成后，自动将与本机环境一致的二进制复制一份到 bin/ 根目录，便于直接运行。
#
# 平台与架构在下方 PLATFORMS 列表中维护，无需在编译时传递环境参数。

set -e

ROOT_DIR=$(cd "$(dirname "$0")" && pwd)
BUILD_DIR="${ROOT_DIR}/bin"
BACKEND_DIR="${ROOT_DIR}/src/backend"

# 目标平台列表（OS/ARCH），每个 OS 对应 bin/ 下一个二级目录
PLATFORMS=(
  "darwin/arm64"
  "linux/arm64"
  "windows/amd64"
)

# 服务列表：二进制名
SERVICES=(gateway user-service product-service order-service cart-service payment-service)

# 服务二进制名 -> 源码目录
src_dir_of() {
  case "$1" in
    gateway) echo "./gateway/" ;;
    *) echo "./services/${1%-service}/" ;;
  esac
}

build_one() {
  local os="$1" arch="$2" svc="$3"
  local out="${BUILD_DIR}/${os}/${svc}"
  [ "${os}" = "windows" ] && out="${out}.exe"
  (cd "${BACKEND_DIR}" && GOPATH="${ROOT_DIR}" CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -o "${out}" "$(src_dir_of "${svc}")")
}

mkdir -p "${BUILD_DIR}/static"

# 逐平台编译
for platform in "${PLATFORMS[@]}"; do
  os="${platform%%/*}"
  arch="${platform##*/}"
  echo "==> 构建 ${os}/${arch} ..."
  rm -rf "${BUILD_DIR:?}/${os}"
  mkdir -p "${BUILD_DIR}/${os}"
  for svc in "${SERVICES[@]}"; do
    echo "    构建 ${svc} ..."
    build_one "${os}" "${arch}" "${svc}"
  done
done

# 识别本机环境，并复制对应的二进制到 bin/ 根目录
LOCAL_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${LOCAL_OS}" in
  *mingw*|*msys*|*cygwin*) LOCAL_OS="windows" ;;
esac
LOCAL_ARCH="$(uname -m)"
case "${LOCAL_ARCH}" in
  x86_64|amd64)   LOCAL_ARCH="amd64" ;;
  arm64|aarch64)  LOCAL_ARCH="arm64" ;;
esac

LOCAL_DIR=""
for platform in "${PLATFORMS[@]}"; do
  os="${platform%%/*}"; arch="${platform##*/}"
  if [ "${os}" = "${LOCAL_OS}" ] && [ "${arch}" = "${LOCAL_ARCH}" ]; then
    LOCAL_DIR="${os}"
  fi
done

if [ -n "${LOCAL_DIR}" ]; then
  echo "==> 复制本机（${LOCAL_OS}/${LOCAL_ARCH}）二进制到 ${BUILD_DIR}/ ..."
  for svc in "${SERVICES[@]}"; do
    ext=""
    [ "${LOCAL_DIR}" = "windows" ] && ext=".exe"
    cp "${BUILD_DIR}/${LOCAL_DIR}/${svc}${ext}" "${BUILD_DIR}/${svc}${ext}"
  done
else
  echo "==> 本机环境（${LOCAL_OS}/${LOCAL_ARCH}）不在构建列表中，跳过复制"
fi

echo "==> 构建完成，输出目录: ${BUILD_DIR}"
