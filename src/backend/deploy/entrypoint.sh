#!/bin/bash
# market 一体化镜像入口脚本（以 root 引导，业务进程降权到非 root 运行）
#
# 启动流程：
#   1. 校验必填环境变量（MYSQL_ROOT_PASSWORD / REDIS_PASSWORD）
#   2. 按 bind mount 目录属主自动授权（Linux 宿主无需手动 chown）
#   3. MySQL 数据目录为空时首启初始化（建 root 口令 + market 库），
#      随后启动 mysqld / redis-server 并等待就绪
#      （MySQL/Redis 监听 0.0.0.0，可达性由部署侧端口发布决定：示例 compose
#       发布侧绑定宿主机 127.0.0.1，本机/SSH 隧道可用第三方工具直连调试）
#   4. 按 gRPC 依赖顺序拉起 6 个业务进程（均以 uid 10001 运行，
#      崩溃由 supervise 循环自动拉起）
#   5. 调用 market-admin init 初始化超管账号（幂等，凭据输出到 stdout，
#      即 docker logs；未配置 MARKET_ADMIN_PASSWORD 时随机生成）。
#      非 production 且库为空时，init 同时经内嵌演示数据包导入演示种子
#      （替代原 init.dev.sql SQL 导入链路）
#   6. wait 常驻；收到 SIGTERM/SIGINT 向全部子进程广播 TERM 优雅退出
#
# 数据持久化（bind mount，容器/镜像重装不丢失）：
#   /var/lib/mysql      MySQL 数据
#   /data               Redis 数据
#   /app/uploads     附件上传目录（product 落盘 + gateway /uploads 共用）
#   /app/runtime        运行时配置快照 + 超管凭证文件
#   /app/logs           业务日志
#   /app/data/geoip     IP 定位库（可选，缺失时自动下载，失败优雅降级）
#   /app/verify         平台验证文件（微信域名归属校验，后台站点设置页生成；
#                       可不挂载，容器重建丢失属预期——后台重新保存即可）

set -u

APP_DIR=/app
RUNTIME_DIR=$APP_DIR/runtime
MYSQL_DATA=/var/lib/mysql
MYSQL_DATABASE=market
SHUTDOWN_MARKER=/tmp/.market-shutdown

# 默认 CONFIG_FILE 指向 market-admin 的私有配置副本（docker exec 场景使用；
# 各业务进程由 start_service 注入各自的副本路径）
export CONFIG_FILE=$RUNTIME_DIR/config.market-admin.yaml
export MARKET_ADMIN_CREDENTIALS_FILE=$RUNTIME_DIR/admin_credentials.json

log() { echo "[entrypoint] $(date '+%F %T') $*"; }

# ---------- 环境校验 ----------

if [ -z "${MYSQL_ROOT_PASSWORD:-}" ]; then
  log "错误: 未配置 MYSQL_ROOT_PASSWORD 环境变量，拒绝启动"
  exit 1
fi
if [ -z "${REDIS_PASSWORD:-}" ]; then
  log "错误: 未配置 REDIS_PASSWORD 环境变量，拒绝启动"
  exit 1
fi

# 业务进程读取口径：导出 MYSQL_PASSWORD 供 entrypoint 子进程（业务进程/初始化）
# 经 ApplyEnvOverrides 覆盖配置；注意 export 仅本进程链内生效，docker exec 新起
# 的 shell 不继承——exec 场景由 ApplyEnvOverrides 回退 MYSQL_ROOT_PASSWORD 兜底，
# 部署时也可显式注入同值 MYSQL_PASSWORD 保证口径一致
export MYSQL_PASSWORD="$MYSQL_ROOT_PASSWORD"

# docker start 复用容器文件系统：上次优雅停止遗留的标记必须先清除，
# 否则 supervise 循环开局即让路退出，mysqld/redis 根本不会启动
rm -f "$SHUTDOWN_MARKER" 2>/dev/null || true

# ---------- 工具函数 ----------

# prepare_dir 确保目录存在且属主为指定 uid（属主已正确时跳过递归 chown，
# 避免大附件目录每次启动都全量扫描）
prepare_dir() {
  local path=$1 uid=$2
  mkdir -p "$path" 2>/dev/null || true
  local cur
  cur=$(stat -c %u "$path" 2>/dev/null || echo -1)
  if [ "$cur" != "$uid" ]; then
    chown -R "$uid:$uid" "$path" 2>/dev/null || true
  fi
}

# sql_escape 转义 SQL 字符串字面量中的反斜杠与单引号
sql_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g"
}

# sed_escape 转义 sed 替换串中的特殊字符（& \ | 分隔符）
sed_escape() {
  printf '%s' "$1" | sed -e 's/[&|\\]/\\&/g'
}

# wait_port 轮询等待 TCP 端口可连接（bash /dev/tcp，镜像无 nc）
wait_port() {
  local host=$1 port=$2 name=$3 timeout=${4:-120}
  for ((i = 1; i <= timeout; i++)); do
    if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  log "错误: 等待 $name ($host:$port) 超时（${timeout}s）"
  return 1
}

PIDS=()

# supervise 后台守护：目标进程退出后自动重启（优雅停止时经标记文件让路）
supervise() {
  local name=$1
  shift
  (
    while true; do
      [ -f "$SHUTDOWN_MARKER" ] && exit 0
      "$@"
      [ -f "$SHUTDOWN_MARKER" ] && exit 0
      echo "[supervise] $name 进程退出（code=$?），3 秒后自动重启..." >&2
      sleep 3
    done
  ) &
  PIDS+=($!)
}

# start_service 以 appuser(10001) 运行业务进程（cwd /app，日志写入 /app/logs），
# 每个进程注入自己的私有配置副本
start_service() {
  local name=$1 bin=$2
  log "启动 $name ..."
  supervise "$name" runuser -u appuser -- \
    env CONFIG_FILE="$RUNTIME_DIR/config.$name.yaml" bash -c "cd $APP_DIR && exec ./$bin"
}

# mysql_running 判断 mysqld 进程是否仍在运行（pid 文件存在性不可靠，
# 直接扫描 /proc 的进程名）
mysql_running() {
  local p
  for p in /proc/[0-9]*/comm; do
    [ "$(cat "$p" 2>/dev/null)" = "mysqld" ] && return 0
  done
  return 1
}

# shutdown 优雅停止：广播 TERM 给除自身外的全部进程，等待 mysqld 落盘后退出
shutdown() {
  trap - TERM INT
  log "收到停止信号，正在通知全部进程优雅退出..."
  touch "$SHUTDOWN_MARKER" 2>/dev/null || true
  local p pid
  for p in /proc/[0-9]*; do
    pid=${p#/proc/}
    if [ "$pid" != "$$" ]; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  # 等待 mysqld 退出（InnoDB 落盘），最多 15 秒；
  # 运行容器建议配置 --stop-timeout 30 给足优雅退出时间
  for i in $(seq 1 15); do
    mysql_running || break
    sleep 1
  done
  log "已退出"
  exit 0
}
trap shutdown TERM INT

# ---------- 目录授权 ----------

prepare_dir "$RUNTIME_DIR" 10001
prepare_dir "$APP_DIR/uploads" 10001
prepare_dir "$APP_DIR/logs" 10001
prepare_dir "$APP_DIR/data/geoip" 10001
# 登录密码加密 RSA 私钥目录：gateway 首启自动生成落盘（bin/config.allinone.yaml 的
# password_rsa_key_file 指向此处），挂载宿主目录后容器重建/升级镜像密钥不丢
prepare_dir "$APP_DIR/data/ssl" 10001
# 平台验证文件目录：gateway 伪静态下发的落盘目标（/app 属 root，非 root 的 gateway
# 无权就地 mkdir，须在此预创建授权；verifyfs.Save 自身另有 MkdirAll 兜底）
prepare_dir "$APP_DIR/verify" 10001
prepare_dir "$MYSQL_DATA" "$(id -u mysql)"
prepare_dir /data "$(id -u redis)"

# ---------- GeoIP 定位库（可选，非致命） ----------

# 首次部署 /app/data/geoip 为空时后台自动下载（下载器对 3 天内文件自动跳过，
# 每次容器启动调用一次无副作用）；失败不影响启动——gateway 对数据库缺失
# 本身优雅降级（IP 定位返回空），就绪后按 mtime 热加载，无需重启。
# --dest 必须显式传容器内绝对路径：下载器默认 dest 相对 cwd 解析为 bin/data/geoip
# （发布包约定，网关二进制在 bin/ 下），与容器内 /app/data/geoip 的布局不符。
# 目录可写是下载的前提（docker run 示例已不再 :ro 挂载）；后续更新可由宿主机
# crontab 直接跑发布包内二进制，或 docker exec 触发。
if ! compgen -G "$APP_DIR/data/geoip/*.mmdb" >/dev/null; then
  log "GeoIP 定位库缺失，后台执行 geoip-download（就绪前 IP 定位优雅降级）..."
  # 子 shell 后台包装：下载进程的 stdout/stderr 仍只落 geoip-download.log，
  # 但退出码非 0 时向前台（docker logs）补一行明确告警——否则下载失败在
  # docker logs 全程无感知，只能进容器翻日志文件才能发现
  (
    if ! runuser -u appuser -- bash -c "cd $APP_DIR && exec ./geoip-download --dest $APP_DIR/data/geoip/GeoLite2-City.mmdb" \
        >> "$APP_DIR/logs/geoip-download.log" 2>&1; then
      log "警告: GeoIP 下载失败（退出码非 0），IP 定位将持续优雅降级；明细见 $APP_DIR/logs/geoip-download.log，重启容器会自动重试"
    fi
  ) &
fi

# 运行时配置：每个进程一份私有副本（关键！）。框架加载配置时会回写文件，
# 6 个进程共享同一文件会产生回写竞态——后写者覆盖、半写被并发读取导致
# 解析失败回落结构体默认值（dbname=dbname / grpc_port=0 → 服务崩溃），
# 与 compose 部署「每容器 cp 一份私有 config.runtime.yaml」同一原理
#
# 源配置机制：/app/runtime/config.yaml（宿主机 ./data/runtime/config.yaml，
# bind mount 持久化）是用户可编辑的唯一配置源。首次启动从镜像内置模板
# 生成，此后每次启动以它为模板分发各进程副本——用户只改这一份文件并重启
# 容器即全量生效。命名刻意避开 config.*.yaml 通配，不被下方口令回填 sed
# 触碰，保持「密码空占位、由环境变量注入」的安全模型。
# 内容无效（空文件/缺关键节）时告警并回退内置模板分发，避免重蹈
# 「挂载空文件 → 配置解析失败回落默认值 → 服务崩溃」的事故
SOURCE_CONFIG=$RUNTIME_DIR/config.yaml
if [ -s "$SOURCE_CONFIG" ] \
   && grep -q '^web_server:' "$SOURCE_CONFIG" \
   && grep -q '^db:' "$SOURCE_CONFIG" \
   && grep -q '^redis:' "$SOURCE_CONFIG"; then
  CONFIG_TEMPLATE=$SOURCE_CONFIG
  log "使用宿主机源配置: $SOURCE_CONFIG"
else
  if [ -f "$SOURCE_CONFIG" ]; then
    log "警告: 源配置 $SOURCE_CONFIG 内容无效（空文件或缺少 web_server/db/redis 配置节），本次使用内置模板分发"
  fi
  CONFIG_TEMPLATE=/app/config.yaml
  # 首启生成源配置供后续编辑；空文件（如误创建）会被内置模板内容覆盖修复
  if [ ! -s "$SOURCE_CONFIG" ]; then
    cp -f /app/config.yaml "$SOURCE_CONFIG"
    log "已生成源配置 $SOURCE_CONFIG（宿主机 ./data/runtime/config.yaml，可直接编辑，重启容器生效）"
  fi
fi

# ---------- JWT 密钥 ----------
# 密钥模板留空且不入仓库，必须在各进程启动前定下来：容器内 7 个进程共享同一环境，
# 在此集中决定一次，避免各自随机导致签发的 token 互不认。
# 优先级：docker run -e JWT_SECRET > 宿主机持久化文件 > 首次随机生成并持久化
#（持久化文件随 runtime 目录 bind mount 落宿主机，容器重建不使既有 token 失效）
if [ -z "$JWT_SECRET" ]; then
  if [ -s "$RUNTIME_DIR/jwt_secret" ]; then
    JWT_SECRET=$(cat "$RUNTIME_DIR/jwt_secret")
    log "JWT 密钥取自持久化文件 $RUNTIME_DIR/jwt_secret"
  else
    JWT_SECRET=$(head -c 48 /dev/urandom | od -An -tx1 | tr -d ' \n')
    printf '%s' "$JWT_SECRET" > "$RUNTIME_DIR/jwt_secret"
    chmod 600 "$RUNTIME_DIR/jwt_secret" 2>/dev/null || true
    log "已随机生成 JWT 密钥并持久化到 $RUNTIME_DIR/jwt_secret"
  fi
fi
export JWT_SECRET

SERVICES_LIST="user-service product-service order-service cart-service payment-service gateway market-admin"
for svc in $SERVICES_LIST; do
  cp -f "$CONFIG_TEMPLATE" "$RUNTIME_DIR/config.$svc.yaml"
  # debugger.service_name（debug_logs 日志来源标识）：gateway 读它注册请求日志与调试页面，
  # payment/order 读它初始化进程级记录实例（与 gateway 共享 debug_logs 表）；
  # 模板为 7 个进程共用一份——非 gateway 副本统一覆写为本进程名，
  # 避免所有副本都顶着 gateway 的标识；源配置无此行时 sed 不命中（回落结构体默认值），无害
  if [ "$svc" != "gateway" ]; then
    sed -i "s/^\([[:space:]]*service_name:\).*/\1 $svc/" "$RUNTIME_DIR/config.$svc.yaml"
  fi
done

# 密钥回填（仅 JWT）：框架回写发生在环境变量覆盖之前（文件恒为空占位）。
# db/redis 口令不再回写——业务进程经 start_service 继承 entrypoint export 的
# MYSQL_PASSWORD / REDIS_PASSWORD；docker exec 新起的 shell 不继承该 export，
# 由 ApplyEnvOverrides 回退 MYSQL_ROOT_PASSWORD 兜底（容器层必有），明文口令
# 不再落到宿主机 runtime 目录的配置副本里。JWT 是密钥且 entrypoint 会自动生成
# （不在 Config.Env 里），docker exec 启动的 market-admin 过 EnsureSecureJWT
# 校验时必须能读到它；写入副本与 $RUNTIME_DIR/jwt_secret 同值，不新增暴露面。
JWT_ESC=$(sed_escape "$JWT_SECRET")
for f in "$RUNTIME_DIR"/config.*.yaml; do
  sed -i "/^jwt:/,/^log:/ s/secret: \"\"/secret: \"$JWT_ESC\"/" "$f"
  chmod 600 "$f" 2>/dev/null || true
done
chown -R appuser:appuser "$RUNTIME_DIR" 2>/dev/null || true

# ---------- Redis ----------

# 监听 0.0.0.0：端口是否可达由部署侧端口发布决定（示例 compose 发布侧绑定
# 宿主机 127.0.0.1），宿主机第三方工具可经映射直连调试（口令 REDIS_PASSWORD）
log "启动 Redis ..."
supervise redis runuser -u redis -- \
  redis-server --bind 0.0.0.0 --port 6379 --dir /data \
    --requirepass "$REDIS_PASSWORD" \
    --save 900 1 --save 300 10 --save 60 10000

# ---------- MySQL ----------

FIRST_BOOT=0
if [ ! -d "$MYSQL_DATA/mysql" ]; then
  FIRST_BOOT=1
  log "MySQL 数据目录为空，执行首次初始化（root 口令来自 MYSQL_ROOT_PASSWORD）..."
  if ! mysqld --initialize-insecure --user=mysql --datadir="$MYSQL_DATA"; then
    log "错误: MySQL 初始化失败"
    exit 1
  fi
fi

# 监听 0.0.0.0：首启已建 root@'%' 账号，宿主机第三方工具可经端口映射直连调试
log "启动 MySQL ..."
supervise mysqld mysqld --user=mysql --datadir="$MYSQL_DATA" \
  --bind-address=0.0.0.0 --port=3306 --mysqlx=OFF --skip-name-resolve

# 等待 socket 就绪（首启初始化后的启动可能需要数十秒）
log "等待 MySQL 就绪..."
for i in $(seq 1 120); do
  if mysqladmin --protocol=socket -uroot ping >/dev/null 2>&1; then
    log "MySQL 已就绪"
    break
  fi
  if [ "$i" = "120" ]; then
    log "错误: MySQL 启动超时（120s），请查看容器日志定位原因"
    exit 1
  fi
  sleep 1
done

if [ "$FIRST_BOOT" = "1" ]; then
  # 首启引导：设置 root 口令（localhost + %，业务进程与运维均走 TCP）、建库
  ESC=$(sql_escape "$MYSQL_ROOT_PASSWORD")
  SQL=$(printf "ALTER USER 'root'@'localhost' IDENTIFIED BY '%s'; CREATE USER IF NOT EXISTS 'root'@'%%' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON *.* TO 'root'@'%%' WITH GRANT OPTION; CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; FLUSH PRIVILEGES;" "$ESC" "$ESC" "$MYSQL_DATABASE")
  if ! mysql -uroot -e "$SQL"; then
    log "错误: MySQL 首启引导 SQL 执行失败"
    exit 1
  fi
fi

# 每次启动都验证 TCP root 登录：业务进程与 market-admin 均经 TCP 连接；
# 数据目录已初始化但口令与 MYSQL_ROOT_PASSWORD 不一致时给出明确指引。
# 口令经 MYSQL_PWD 传入（与 start-all.mjs 同口径）：`-p<口令>` 会出现在进程
# 参数列表里，同机其他用户 ps 即可看到明文。
if ! MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=TCP -h127.0.0.1 -uroot -e 'SELECT 1' >/dev/null 2>&1; then
  log "错误: MySQL root 口令与 MYSQL_ROOT_PASSWORD 不匹配（数据目录已存在旧口令）"
  log "      处理方式: 改回原 MYSQL_ROOT_PASSWORD 启动后修改口令，或清空数据目录重新初始化（数据将丢失，请先备份）"
  exit 1
fi
MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=TCP -h127.0.0.1 -uroot \
  -e "CREATE DATABASE IF NOT EXISTS $MYSQL_DATABASE CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci" >/dev/null 2>&1

# ---------- 业务进程（按 gRPC 依赖顺序） ----------

start_service user-service user-service
start_service product-service product-service
start_service order-service order-service

# cart 依赖 product、payment 依赖 order、gateway 依赖全部，端口就绪后再拉起
wait_port 127.0.0.1 50052 product-service 120 || exit 1
start_service cart-service cart-service
wait_port 127.0.0.1 50053 order-service 120 || exit 1
start_service payment-service payment-service

wait_port 127.0.0.1 50051 user-service 120 || exit 1
wait_port 127.0.0.1 50054 cart-service 120 || exit 1
wait_port 127.0.0.1 50055 payment-service 120 || exit 1

start_service gateway gateway
wait_port 127.0.0.1 18080 gateway 120 || exit 1
log "全部服务已启动，网关监听 :18080"

# ---------- 超管初始化（幂等，非致命） ----------
# 同时负责演示种子导入（非 production 且库为空时，见 cmd/market-admin/seed.go）；
# 必须在业务进程全部就绪后执行——init 内部等待各表迁移完成后再落种子，
# 且先导种子再兜底默认社区，避免空库被「默认社区」占用导致演示数据误判跳过

log "初始化超管账号（等待 user-service 完成自动迁移，最长 300 秒）..."
if runuser -u appuser -- \
   env CONFIG_FILE="$RUNTIME_DIR/config.market-admin.yaml" \
   bash -c "cd $APP_DIR && exec ./market-admin init --timeout 300"; then
  log "超管初始化流程结束（凭据见上方输出，或执行 docker exec <容器名> ./market-admin 查看）"
else
  log "警告: 超管初始化未完成，可稍后手动执行: docker exec -it <容器名> ./market-admin"
fi

# ---------- 常驻等待 ----------

wait
