#!/bin/bash
# 社区商超后端构建脚本
# 用于编译 gateway 及各微服务二进制

set -e

ROOT_DIR=$(cd "$(dirname "$0")" && pwd)
BUILD_DIR="${ROOT_DIR}/bin"

mkdir -p "${BUILD_DIR}/static"

echo "==> 开始构建 gateway（Linux arm64）..."
(cd "${ROOT_DIR}/src/backend" && GOPATH="${ROOT_DIR}" CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "${BUILD_DIR}/gateway" ./gateway/)

echo "==> 开始构建微服务（Linux arm64）..."
for service in user product order cart payment; do
  echo "    构建 ${service}-service..."
  (cd "${ROOT_DIR}/src/backend" && GOPATH="${ROOT_DIR}" CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "${BUILD_DIR}/${service}-service" ./services/${service}/)
done

echo "==> 构建完成，输出目录: ${BUILD_DIR}"
