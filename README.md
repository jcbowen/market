# 玖祺社区商超系统（jc_market）

> 项目命名：中文名 **玖祺社区商超系统**，英文名 **jc_market**。仓库目录、镜像名、二进制名沿用 `market`（如 `jcbowen/market`），文档与对外介绍统一使用上述正式名称。

面向多社区运营的商超商城 + 门店收银台（POS）一体化系统：C 端用户下单（自提/配送/到店）、履约人员工作台（送货上门 + 自提核销）、管理后台（商品/库存/营销/订单/统计/多社区权限）、收银台代客下单与交班体系。社区维度数据隔离贯穿全部链路。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26+，gRPC 微服务（gateway + user/product/order/cart/payment 五个服务），GORM + MySQL 8 / Redis 7 |
| 管理端 | Vue 3 + TypeScript + Vite + Pinia（`src/admin`） |
| C 端 | uni-app 多端（H5 / 微信 / 支付宝小程序等，`src/client`） |
| 部署 | Docker Compose（镜像仅打包预编译产物，非 root 运行） |

## 目录结构

```
market/
├── src/backend/          # 后端
│   ├── gateway/          #   API 网关（18080：路由/鉴权/静态资源）
│   ├── services/         #   五个 gRPC 服务（50051-50055）
│   ├── common/           #   proto 定义、JWT、配置、公共组件
│   └── internal/         #   服务启动引导
├── src/admin/            # 管理端前端（Vue3）
├── src/client/           # C 端 uni-app
├── deploy/               # init.dev.sql（开发测试数据）、nginx/daemon 示例
├── bin/                  # 构建产物与运行时配置（不入 git，模板除外）
├── docs/                 # 项目文档（见下）
└── docker-compose.yml    # 本地/生产编排（口令经根目录 .env 注入）
```

## 快速开始

### 本地开发

```bash
# 一键启动（依赖检查 → 前后端构建 → 镜像构建 → 启动 → 测试数据初始化）
node start-all.mjs
```

| 入口 | 地址 | 默认账号 |
|---|---|---|
| 管理后台 | http://localhost:18080/b/ | admin / admin123（仅本地） |
| C 端 H5 | http://localhost:18080/s/ | 13800138000 / 验证码 123456 |
| 健康检查 | http://localhost:18080/health | HTTP 200 |

### 服务器快速部署（Docker 一体化镜像）

单容器承载 MySQL + Redis + 全部服务 + 前端，数据落宿主机 `./data/`，升级镜像不丢数据。服务器装好 Docker 后三步上线：

1. 新建部署目录与 `docker-compose.yml`（内容如下，**三个口令必改**）：

```yaml
services:
  market:
    # latest 随每次发版自动更新；需锁版本时到 GitHub Releases 页取最新 tag 替换
    image: jcbowen/market:latest        # 国内推荐 Docker Hub；备选 ghcr.io/jcbowen/market:latest
    container_name: market
    restart: unless-stopped
    stop_grace_period: 30s
    ports:
      - "18080:18080"
    environment:
      APP_ENV: production
      MYSQL_ROOT_PASSWORD: "改成强口令"      # 仅首次初始化生效，之后不可变更
      REDIS_PASSWORD: "改成强口令"
      JWT_SECRET: "改成至少32位随机字符串"
    volumes:
      - ./data/mysql:/var/lib/mysql
      - ./data/redis:/data
      - ./data/attachment:/app/attachment
      - ./data/runtime:/app/runtime
      - ./data/logs:/app/logs
    healthcheck:
      test: ["CMD", "bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/18080"]
      interval: 15s
      timeout: 5s
      retries: 10
      start_period: 120s
```

2. 启动并观察初始化日志（首启含 MySQL 初始化与自动迁移，初始超管密码输出在日志中）：

```bash
mkdir -p data/{mysql,redis,attachment,runtime,logs}
docker compose up -d && docker logs -f market
```

3. 访问 `http://<服务器IP>:18080/b/` 进入管理后台。

日常升级：`docker compose pull && docker compose up -d`，数据原样保留；回滚把镜像 tag 改回历史版本号（如 `v0.1.3`）再 `up -d` 即可。完整说明（HTTPS 反代、数据备份、初始账号管理、外部数据库等）见 [docs/需求文档/10-部署方案/03-生产环境部署.md](docs/需求文档/10-部署方案/03-生产环境部署.md)。

### 服务器部署（二进制，无 Docker）

服务器无法使用 Docker 时，直接下载 CI 发布的二进制包（含 6 个服务二进制 + `config.yaml` 模板 + 前端静态资源）：

```bash
VERSION=v0.1.3   # 替换为 GitHub Releases 页最新 tag；按服务器架构选择 amd64 / arm64
wget "https://github.com/jcbowen/market/releases/download/${VERSION}/market-${VERSION}-linux-amd64.tar.gz"
tar -xzf "market-${VERSION}-linux-amd64.tar.gz" -C /opt
mv "/opt/market-${VERSION}-linux-amd64" /opt/market
```

准备 MySQL（空库 `market`、utf8mb4，**无需建表**——服务启动时 GORM AutoMigrate 自动建表）与 Redis，编辑 `/opt/market/config.yaml` 填入生产配置（db/redis/jwt 等）后启动：

```bash
cd /opt/market && mkdir -p logs
for svc in user-service product-service order-service cart-service payment-service gateway; do
  CONFIG_FILE=/opt/market/config.yaml nohup ./${svc} > logs/${svc}.log 2>&1 &
done
```

初始后台账号、systemd 托管、支付/短信配置等完整说明见 [docs/需求文档/10-部署方案/03-生产环境部署.md](docs/需求文档/10-部署方案/03-生产环境部署.md)。

要点：

- 表结构全部由各服务启动时 **GORM AutoMigrate** 创建，无手写建表 SQL；`deploy/init.dev.sql` 仅含测试数据，**只限开发环境**，在服务启动建表之后导入。
- MySQL/Redis 口令与 `JWT_SECRET` 经项目根目录 `.env` 注入（`.env` 不入 git）；生产环境检测到默认 JWT 密钥会拒绝启动。
- 后端依赖 `jcbaseGo`/`wego` 本地 replace（与仓库平级放置）；CI 场景用 `go mod edit -dropreplace` 后走公共 proxy，详见部署文档。

## 文档索引

| 文档 | 内容 |
|---|---|
| [docs/需求文档/01-项目概述.md](docs/需求文档/01-项目概述.md) | 项目背景与目标 |
| [docs/需求文档/02-技术方案](docs/需求文档/02-技术方案) | 架构与技术选型 |
| [docs/需求文档/03-目录结构.md](docs/需求文档/03-目录结构.md) | 目录结构规范 |
| [docs/需求文档/04-业务需求](docs/需求文档/04-业务需求) | 各模块业务需求 |
| [docs/需求文档/05-数据库设计](docs/需求文档/05-数据库设计) | 各服务表结构与索引 |
| [docs/需求文档/06-接口设计](docs/需求文档/06-接口设计) | C 端 / 管理端 HTTP 接口 |
| [docs/需求文档/07-基础设施](docs/需求文档/07-基础设施) | 端口、认证、日志等规范 |
| [docs/需求文档/08-前端路由设计](docs/需求文档/08-前端路由设计) | 管理端 / C 端前端路由 |
| [docs/需求文档/09-gRPC接口设计](docs/需求文档/09-gRPC接口设计) | 五个服务的 proto 接口全量文档 |
| [docs/需求文档/10-部署方案](docs/需求文档/10-部署方案) | 环境依赖、本地启动、生产部署 |
| [docs/需求文档/11-开发计划.md](docs/需求文档/11-开发计划.md) | 迭代计划与已完成功能 |

## 授权与商业使用

本仓库**不是免费开源项目**：源码公开仅便于个人学习、评估与商业授权洽谈，**商业使用须取得付费书面授权**。完整条款见 [LICENSE](LICENSE)。

| 场景 | 是否需付费授权 |
|---|---|
| 个人学习、研究、教学 | 免费 |
| 非商业场景下自行部署、修改（仅限自用，不对外分发） | 免费 |
| 经营/生产环境部署（含自营门店收银、社区商超运营） | 需授权 |
| SaaS、托管、代运营等对外提供服务 | 需授权 |
| 二次开发后交付客户（项目制、外包、定制） | 需授权 |
| 随硬件预装或与商品、服务、会员权益捆绑销售 | 需授权 |
| 企业内部商业运营 | 需授权 |
| 再分发源码、二进制、镜像或静态资源 | 需授权 |

商业授权咨询：bowen@jiuchet.com。项目依赖的第三方组件仍适用其各自的开源许可证。
