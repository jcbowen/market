# market - 社区团购与门店收银一体化系统

面向多社区运营的团购商城 + 门店收银台（POS）一体化系统：C 端用户下单（自提/配送/到店）、履约人员工作台（送货上门 + 自提核销）、管理后台（商品/库存/营销/订单/统计/多社区权限）、收银台代客下单与交班体系。社区维度数据隔离贯穿全部链路。

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

```bash
# 一键启动（依赖检查 → 前后端构建 → 镜像构建 → 启动 → 测试数据初始化）
node start-all.mjs
```

| 入口 | 地址 | 默认账号 |
|---|---|---|
| 管理后台 | http://localhost:18080/b/ | admin / admin123（仅本地） |
| C 端 H5 | http://localhost:18080/s/ | 13800138000 / 验证码 123456 |
| 健康检查 | http://localhost:18080/health | HTTP 200 |

要点：

- 表结构全部由各服务启动时 **GORM AutoMigrate** 创建，无手写建表 SQL；`deploy/init.dev.sql` 仅含测试数据，**只限开发环境**，在服务启动建表之后导入。
- MySQL/Redis 口令与 `JWT_SECRET` 经项目根目录 `.env` 注入（`.env` 不入 git）；生产环境检测到默认 JWT 密钥会拒绝启动。
- 后端依赖 `jcbaseGo`/`wego` 本地 replace（与仓库平级放置）；CI 场景用 `go mod edit -dropreplace` 后走公共 proxy，详见部署文档。

## 文档索引

| 文档 | 内容 |
|---|---|
| [docs/需求文档/01-项目概述.md](docs/需求文档/01-项目概述.md) | 项目背景与目标 |
| [docs/需求文档/02-技术方案](docs/需求文档/02-技术方案) | 架构与技术选型 |
| [docs/需求文档/04-业务需求](docs/需求文档/04-业务需求) | 各模块业务需求 |
| [docs/需求文档/05-数据库设计](docs/需求文档/05-数据库设计) | 各服务表结构与索引 |
| [docs/需求文档/06-接口设计](docs/需求文档/06-接口设计) | C 端 / 管理端 HTTP 接口 |
| [docs/需求文档/07-基础设施](docs/需求文档/07-基础设施) | 端口、认证、日志等规范 |
| [docs/需求文档/09-gRPC接口设计](docs/需求文档/09-gRPC接口设计) | 五个服务的 proto 接口全量文档 |
| [docs/需求文档/10-部署方案](docs/需求文档/10-部署方案) | 环境依赖、本地启动、生产部署 |
| [docs/需求文档/11-开发计划.md](docs/需求文档/11-开发计划.md) | 迭代计划与已完成功能 |
| [docs/需求归档/](docs/需求归档/) | 已完结历史需求归档 |
| [CHANGELOG.md](CHANGELOG.md) | 变更记录 |
