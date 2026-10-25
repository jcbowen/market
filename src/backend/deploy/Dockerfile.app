# market 多容器部署应用镜像（单镜像多容器）
# 一个镜像包含全部 7 个业务二进制 + 前端静态资源 + geoip-download + 配置模板，
# 不含 MySQL/Redis（与 allinone 一体化镜像的本质区别，底座为轻量 alpine），
# 部署时由同一镜像以不同 SERVICE_NAME 跑多个容器（示例见根目录 docker-compose.prod.yml）：
#   user-service / product-service / order-service / cart-service / payment-service / gateway
# 常驻容器 + market-admin 一次性初始化任务（超管 init）。
#
# 构建前提：先执行 `node build.mjs` 产出 bin/linux-{amd64,arm64} 与 bin/static，
# 再构建（本地验证可用 node build-image.mjs --app）：
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -f src/backend/deploy/Dockerfile.app -t <registry>/market-app:1.0.0 --push .
#
# 与 Dockerfile.allinone 的分工：
#   allinone —— 单容器一键起全栈（含 MySQL/Redis），面向最简部署；
#   app      —— 多容器形态的业务进程镜像，MySQL/Redis 用官方镜像或外部实例，
#               换取单服务独立升级/重启、资源隔离与更小的单容器体积。
# 两者消费同一份 bin/ 产物，CI 同一 job 内并行构建，产物结构完全复用。
#
# 运行语义（入口见 entrypoint-app.sh）：
#   - SERVICE_NAME 环境变量选择要运行的二进制（必填，缺省拒绝启动）；
#   - CONFIG_FILE 缺省时从镜像内置模板 /app/config.yaml 复制出容器私有副本：
#     框架启动时会回写配置文件，多个容器不能共写一份挂载模板（与 allinone
#     entrypoint「每进程一份私有副本」同一原理）；
#   - CONFIG_FILE 显式指定（如 gateway 挂载持久化 runtime/config.yaml）时，
#     文件缺失才从模板播种，已存在则原样使用（支付设置等写回不丢）；
#   - 非 gateway 进程的副本覆写 service_name，debug_logs 进程级记录可区分来源；
#   - gateway 容器 /app/data/geoip 无 mmdb 时后台自动下载（失败优雅降级）。
#   - gateway 容器 /app/data/ssl 存放登录密码加密 RSA 私钥：首启自动生成落盘 0600，
#     挂载宿主目录后重建容器 key_id 不变（多实例部署必须挂载同一宿主文件）。
FROM alpine:3.19

ENV TZ=Asia/Shanghai

RUN apk add --no-cache ca-certificates tzdata mysql-client

# 非 root 运行：容器内以固定 UID 10001 运行业务进程，与 allinone/Dockerfile.service
# 同 UID。Linux 宿主挂载写目录（logs/attachment/runtime/geoip/ssl）需执行
# chown -R 10001:10001 <目录>（Docker Desktop/VirtioFS 无需处理）
RUN adduser -D -u 10001 appuser

WORKDIR /app

# TARGETARCH 由 BuildKit 按目标平台自动注入（amd64/arm64），从 build.mjs
# 输出的 bin/linux-{arch} 目录取对应架构产物
ARG TARGETARCH
COPY bin/linux-${TARGETARCH}/gateway ./gateway
COPY bin/linux-${TARGETARCH}/user-service ./user-service
COPY bin/linux-${TARGETARCH}/product-service ./product-service
COPY bin/linux-${TARGETARCH}/order-service ./order-service
COPY bin/linux-${TARGETARCH}/cart-service ./cart-service
COPY bin/linux-${TARGETARCH}/payment-service ./payment-service
COPY bin/linux-${TARGETARCH}/market-admin ./market-admin
COPY bin/linux-${TARGETARCH}/geoip-download ./geoip-download

# 前端静态资源（gateway 容器直接服务 /admin 与 H5）与配置模板（各容器自播种）
COPY bin/static ./static
COPY bin/config.docker.yaml ./config.yaml
COPY src/backend/deploy/entrypoint-app.sh /entrypoint-app.sh

# 演示种子：素材打进镜像由 gateway 直接服务 /uploads/seed/**（不能放 /app/attachment，
# 该目录部署时被宿主目录挂载、会遮蔽镜像内容）；init.dev.sql 与 seed-init.sh 供
# SERVICE_NAME=seed-init 一次性任务在 APP_ENV=stage 且库为空时导入
COPY deploy/seed-assets/products ./seed/products
COPY deploy/init.dev.sql ./seed/init.dev.sql
COPY src/backend/deploy/seed-init.sh /seed-init.sh

RUN chmod +x /entrypoint-app.sh /seed-init.sh ./gateway ./user-service ./product-service \
    ./order-service ./cart-service ./payment-service ./market-admin ./geoip-download \
    && chown -R appuser:appuser /app

USER appuser

# 健康检查端口经 APP_PORT 注入（shell 形式在运行时展开 ENV）：微服务容器由
# compose 按服务设置 50051~50055，gateway 设 18080；与 bin/config.docker.yaml
# 的 *_service.grpc_port / web_server.port 同源
ENV APP_PORT=50051
EXPOSE 50051

# 健康检查：gRPC/HTTP 端口 TCP 探活（busybox nc）；
# start-period 放宽到 120s：user-service 等首启需完成 AutoMigrate
HEALTHCHECK --interval=10s --timeout=3s --start-period=120s --retries=10 \
    CMD nc -z 127.0.0.1 ${APP_PORT} || exit 1

ENTRYPOINT ["/entrypoint-app.sh"]
