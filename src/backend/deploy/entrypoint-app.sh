#!/bin/sh
# market 多容器应用镜像入口（单进程模式，POSIX sh / busybox ash）
#
# 与 allinone 的 entrypoint.sh 分工：
#   allinone —— root 引导起 MySQL/Redis + 按依赖顺序拉起 6 个进程 + supervise 守护；
#   本脚本   —— 只起一个进程（SERVICE_NAME 选择），启动顺序/依赖等待/重启策略
#               全部交给 compose（depends_on + healthcheck + restart）。
#
# 启动流程：
#   1. 校验 SERVICE_NAME 并映射到二进制（缺省或无法识别直接拒绝启动）
#   2. 准备配置文件（见 Dockerfile.app 头注释：缺省复制容器私有副本，
#      显式 CONFIG_FILE 仅在缺失时从模板播种）
#   3. 非 gateway 进程覆写副本的 service_name（debug_logs 来源标识）
#   4. gateway 容器缺失 GeoIP 库时后台自动下载（失败优雅降级，不阻塞启动）
#   5. exec 目标二进制（PID 1 即业务进程，SIGTERM 直达，优雅退出交给 Docker）
#
# 环境变量（凭据/拓扑经 common/config.ApplyEnvOverrides 覆盖配置文件，
# 由 compose 的 environment 注入，本脚本不处理）：
#   SERVICE_NAME 必填：user-service / product-service / order-service /
#                cart-service / payment-service / gateway / market-admin
#                （亦接受短名 user / product / order / cart / payment）
#   CONFIG_FILE  可选：显式指定时该文件缺失才从 /app/config.yaml 播种；
#                缺省每次启动从模板复制 /app/config.runtime.yaml
#   APP_PORT     可选：健康检查探测端口（HEALTHCHECK 运行时展开）

set -eu

APP_DIR=/app
CONFIG_SRC=$APP_DIR/config.yaml

# ---------- 1. SERVICE_NAME 解析 ----------

SERVICE="${SERVICE_NAME:-}"
case "$SERVICE" in
  user)    SERVICE=user-service ;;
  product) SERVICE=product-service ;;
  order)   SERVICE=order-service ;;
  cart)    SERVICE=cart-service ;;
  payment) SERVICE=payment-service ;;
esac

case "$SERVICE" in
  user-service|product-service|order-service|cart-service|payment-service|gateway|market-admin) ;;
  *)
    echo "[entrypoint-app] 错误: 未设置或无法识别的 SERVICE_NAME='${SERVICE_NAME:-}'" >&2
    echo "[entrypoint-app] 可选值: user-service / product-service / order-service / cart-service / payment-service / gateway / market-admin" >&2
    exit 1
    ;;
esac

# ---------- 2. 配置文件 ----------

# 框架加载配置时会回写文件：多容器共用一份挂载模板会产生回写竞态（后写者
# 覆盖、半写被并发读取导致解析失败回落默认值），故各容器只写自己的副本。
# 模板缺失视为镜像构建残缺，直接失败而非静默裸奔
if [ ! -s "$CONFIG_SRC" ]; then
  echo "[entrypoint-app] 错误: 配置模板 $CONFIG_SRC 缺失或为空" >&2
  exit 1
fi

# CONFIG_FILE 来源优先级：
#   显式指定（如 gateway 的持久化 runtime/config.yaml）> 容器内私有副本。
# 显式指定且已存在时原样使用——支付设置等运行时写回不因重启丢失；
# 显式指定但缺失（首启/误删）时从模板播种修复，避免「挂载空文件 →
# 解析失败回落默认值 → 服务崩溃」（与 allinone entrypoint 同一口径）
if [ -n "${CONFIG_FILE:-}" ]; then
  if [ ! -s "$CONFIG_FILE" ]; then
    mkdir -p "$(dirname "$CONFIG_FILE")"
    cp -f "$CONFIG_SRC" "$CONFIG_FILE"
    echo "[entrypoint-app] 配置文件 $CONFIG_FILE 缺失，已从模板播种"
  fi
else
  CONFIG_FILE=$APP_DIR/config.runtime.yaml
  export CONFIG_FILE
  cp -f "$CONFIG_SRC" "$CONFIG_FILE"
fi

# ---------- 3. service_name 覆写（debug_logs 进程级来源标识） ----------

# 各容器副本源自同一模板（值均为 gateway）；非 gateway 副本统一覆写为本进程名，
# 否则 debug_logs 表里的进程记录全部顶着 gateway 标识无法区分来源。
# 模板无此行时 sed 不命中（回落结构体默认值），无害
if [ "$SERVICE" != "gateway" ]; then
  sed -i "s/^\([[:space:]]*service_name:\).*/\1 $SERVICE/" "$CONFIG_FILE"
fi

# ---------- 4. GeoIP 定位库（仅 gateway，非致命） ----------

# /app/data/geoip 挂载可写；缺失时后台下载（3 天内文件自动跳过），失败仅
# IP 定位优雅降级。busybox sh 无 compgen，用 ls glob 探测
if [ "$SERVICE" = "gateway" ] && ! ls "$APP_DIR"/data/geoip/*.mmdb >/dev/null 2>&1; then
  mkdir -p "$APP_DIR/data/geoip" "$APP_DIR/logs"
  echo "[entrypoint-app] GeoIP 定位库缺失，后台执行 geoip-download（就绪前 IP 定位优雅降级）..."
  (
    if ! "$APP_DIR/geoip-download" --dest "$APP_DIR/data/geoip/GeoLite2-City.mmdb" \
        >> "$APP_DIR/logs/geoip-download.log" 2>&1; then
      echo "[entrypoint-app] 警告: GeoIP 下载失败（退出码非 0），IP 定位将持续优雅降级；明细见 $APP_DIR/logs/geoip-download.log" >&2
    fi
  ) &
fi

# ---------- 5. 启动目标进程 ----------

echo "[entrypoint-app] 启动 $SERVICE（CONFIG_FILE=$CONFIG_FILE）..."
cd "$APP_DIR"
exec "./$SERVICE" "$@"
