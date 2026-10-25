#!/bin/sh
# market 演示种子导入（一次性任务，POSIX sh / busybox ash）
#
# 用途：
#   APP_ENV=stage（线上演示/灰度）时把镜像内置的演示种子导入空库，语义与本地
#   start-all.mjs 一致：只在 communities 表为空时导入，已有业务数据一律跳过，
#   不覆盖、不重复写入。
#
# 触发方式：
#   - app 多容器部署：docker-compose.prod.yml 的 seed-init 一次性服务
#     （SERVICE_NAME=seed-init 由 entrypoint-app.sh 转交本脚本）
#   - allinone 单容器：entrypoint.sh 在超管初始化前直接调用等价逻辑
#
# 环境变量：
#   APP_ENV             必须为 stage 才导入；其它取值（含 production）直接跳过
#   MYSQL_ROOT_PASSWORD 数据库口令：经 MYSQL_PWD 传入 mysql 客户端，
#                       不写进命令行参数（进程列表可见，容器内亦按同一口径处理）
#   DB_HOST / DB_PORT   数据库地址，缺省 mysql:3306（compose 服务名）
#
# 退出码：
#   0  导入成功，或本就不需要导入（非 stage、库中已有数据）
#   1  等待数据库/表就绪超时、或 SQL 执行失败——交给编排侧暴露失败，避免静默空库
set -u

# ---------- 0. 环境判定 ----------

if [ "${APP_ENV:-}" != "stage" ]; then
  echo "[seed-init] APP_ENV='${APP_ENV:-}' 非 stage，跳过演示种子导入"
  exit 0
fi

MYSQL_HOST=${DB_HOST:-mysql}
MYSQL_PORT=${DB_PORT:-3306}
MYSQL_DATABASE=${DB_NAME:-market}
SEED_FILE=/app/seed/init.dev.sql

if [ ! -s "$SEED_FILE" ]; then
  echo "[seed-init] 错误: 种子文件 $SEED_FILE 缺失或为空（镜像构建残缺）" >&2
  exit 1
fi

# 口令只经环境变量交给客户端，命令行参数在进程列表里明文可见
export MYSQL_PWD="${MYSQL_ROOT_PASSWORD:-}"

# 统一的客户端调用：--protocol=TCP 避免误连本地 socket
mysql_cli() {
  mysql --protocol=TCP -h "$MYSQL_HOST" -P "$MYSQL_PORT" -uroot "$@"
}

# ---------- 1. 等待数据库与表就绪 ----------

echo "[seed-init] 等待 MySQL 就绪（$MYSQL_HOST:$MYSQL_PORT）..."
ready=0
for _ in $(seq 1 120); do
  if mysqladmin --protocol=TCP -h "$MYSQL_HOST" -P "$MYSQL_PORT" -uroot ping >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "[seed-init] 错误: 等待 MySQL 超时（120s）" >&2
  exit 1
fi

# 表由各微服务启动时 AutoMigrate 创建：等 categories 可查询再判定/导入，
# 否则空库首启会在建表完成前误判并失败
echo "[seed-init] 等待微服务完成表迁移..."
migrated=0
for _ in $(seq 1 300); do
  if mysql_cli -N -s "$MYSQL_DATABASE" -e 'SELECT 1 FROM categories LIMIT 1' >/dev/null 2>&1; then
    migrated=1
    break
  fi
  sleep 1
done
if [ "$migrated" != "1" ]; then
  echo "[seed-init] 错误: 等待表迁移超时（300s）" >&2
  exit 1
fi

# ---------- 2. 已有业务数据则跳过 ----------

COUNT=$(mysql_cli -N -s "$MYSQL_DATABASE" -e 'SELECT COUNT(*) FROM communities' 2>/dev/null || echo 0)
if [ "${COUNT:-0}" != "0" ]; then
  echo "[seed-init] 库中已有 $COUNT 个社区，视为已初始化，跳过演示种子导入"
  exit 0
fi

# ---------- 3. 导入 ----------

echo "[seed-init] 空库，开始导入演示种子 $SEED_FILE ..."
if mysql_cli --default-character-set=utf8mb4 "$MYSQL_DATABASE" < "$SEED_FILE"; then
  echo "[seed-init] 演示种子导入完成"
else
  echo "[seed-init] 错误: 演示种子导入失败（上一条 SQL 报错见 mysql 输出）" >&2
  exit 1
fi
