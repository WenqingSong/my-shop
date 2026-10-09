# SurgeCart（my-shop）

基于 [GoFrame v2](https://goframe.org) 的电商项目，以普通订单和秒杀为两条核心交易链路。仓库已包含前台用户与后台管理员身份、商品目录、购物车、库存、普通订单、秒杀，以及评价、收藏等扩展功能。当前重点是验证业务正确性与故障恢复；性能和部署结论以今后发布的实测记录为准。

> **第一次阅读？**先看[5 分钟项目导览](docs/project-overview.md)。长期设计见 [`docs/design/`](docs/design/)；`.agent/tasks/` 保留各任务的历史决策与验收证据。

## 当前实现

| 领域 | 仓库中的实现 |
| --- | --- |
| 身份与权限 | 前台用户 JWT + Redis 会话、Refresh Token 轮换与会话撤销；后台管理员独立身份域与 RBAC |
| 商品与购买 | 分类、商品、SKU、库存、地址、购物车；普通订单含服务端定价、快照、幂等、状态机与超时取消 |
| 秒杀 | V1 MySQL 正确性约束、V2 Redis + Lua 准入、V3 MySQL 出队表异步落单、V4 故障恢复与审计 |
| 扩展能力 | 评价、收藏、点赞、轮播图、推荐位、文章等 |
| 工程基础 | 版本化数据库迁移、Docker Compose 开发依赖、测试与 Makefile 生命周期脚本 |

**能力边界：**普通订单使用 Mock 支付，没有接入真实支付机构；秒杀订单下单即成交，不走普通订单的支付/取消流程。秒杀异步队列使用 MySQL 出队表，没有引入外部 MQ。Kubernetes 部署与 P99 压测数据尚未在本仓库形成可复现的公开结论，因此这里不声明生产容量或性能指标。

## 阅读路径

1. [项目导览](docs/project-overview.md)：了解系统和两条核心交易链路。
2. [普通订单设计](docs/design/order.md)与[秒杀设计](docs/design/flash-sale.md)：深入状态机、业务不变量、一致性与故障处理。
3. [订单核心逻辑验证](.agent/tasks/order-v1/core-logic.md)、[秒杀 V4 核心逻辑验证](.agent/tasks/flash-sale-v4/core-logic.md)与[秒杀 V4 交付证据](.agent/tasks/flash-sale-v4/delivery.md)：查看设计如何对应实现和测试。
4. [Agent Workflow 设计](docs/design/agent-workflow.md)：了解项目如何记录决策、审查实现并保留验收证据。

`docs/design/*` 记录持续维护的项目级设计；`.agent/tasks/*` 是按任务留存的历史资料。阅读历史任务时，应以当前代码和长期设计核对现状。

## 环境要求

- Go 1.23+（开发使用 1.24）
- Docker 及 Docker Compose
- 使用 `make` 脚本时需要 Bash；Windows 可在 WSL 中运行

## 目录结构

```
.
├── api/                    # 对外 API 定义（请求/响应结构体）
├── internal/
│   ├── boot/               # 配置、依赖与数据库 schema 就绪检查
│   ├── cmd/                # 命令行入口、路由与后台扫描器
│   ├── controller/         # 控制器
│   ├── logic/              # 业务逻辑实现
│   ├── service/            # 服务接口定义
│   └── migrations/         # 版本化数据库迁移
├── docs/design/            # 项目级长期设计
├── .agent/tasks/           # 任务决策与验收证据
├── manifest/config/        # 配置文件
│   └── config.yaml
├── docker-compose.yml      # MySQL / Redis 开发依赖
├── Makefile                # 本地生命周期入口
├── main.go                 # 程序入口
└── go.mod
```

## 快速开始

### 推荐：统一生命周期脚本

首次启动需要自行提供本地超级管理员密码。`make bootstrap` 会启动 MySQL/Redis、构建应用、执行数据库迁移，再启动 HTTP 服务。

```bash
export ADMIN_SUPER_PASSWORD='your-local-password'
make bootstrap
make health
```

默认访问 `http://127.0.0.1:8000/health`。`make down` 停止应用与依赖容器、保留数据卷；更多目标见 `make help`。

### 手动启动

`serve` 只检查 schema，不自动建表。因此必须先执行迁移：

```bash
docker compose up -d
go run . migrate up
export ADMIN_SUPER_PASSWORD='your-local-password'
go run . serve
```

首次拉起容器后，应等 MySQL 和 Redis 就绪再执行迁移。服务默认监听 `:8000`。

### 访问健康检查接口

```bash
curl http://127.0.0.1:8000/health
# {"code":0,"message":"OK","data":{"status":"ok","time":"..."}}
```

## 构建与测试

```bash
go build ./...     # 编译
go vet ./...       # 静态检查
go test -p 1 ./... # 测试包共享本地 MySQL/Redis，串行执行
```

测试前应启动依赖并完成迁移；`make test` 运行 `vet` 和串行测试。

## 配置与环境变量覆盖

默认配置位于 `manifest/config/config.yaml`，可通过环境变量覆盖关键配置项（无需修改代码或配置文件）。

环境变量命名规则：配置键中的 `.` 替换为 `_` 并转为大写。

| 配置键 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `server.address` | `SERVER_ADDRESS` | `:8000` | HTTP 监听地址 |
| `database.default.type` | `DATABASE_DEFAULT_TYPE` | `mysql` | 数据库类型 |
| `database.default.host` | `DATABASE_DEFAULT_HOST` | `127.0.0.1` | MySQL 主机 |
| `database.default.port` | `DATABASE_DEFAULT_PORT` | `3306` | MySQL 端口 |
| `database.default.user` | `DATABASE_DEFAULT_USER` | `root` | MySQL 账号 |
| `database.default.pass` | `DATABASE_DEFAULT_PASS` | `root` | MySQL 密码 |
| `database.default.name` | `DATABASE_DEFAULT_NAME` | `my_shop` | 数据库名 |
| `redis.default.address` | `REDIS_DEFAULT_ADDRESS` | `127.0.0.1:6379` | Redis 地址 |
| `redis.default.db` | `REDIS_DEFAULT_DB` | `0` | Redis DB |
| `redis.default.pass` | `REDIS_DEFAULT_PASS` | 空 | Redis 密码 |
| `auth.jwt.secret` | `AUTH_JWT_SECRET` | 开发用默认值 | JWT 签名密钥；生产必须覆盖 |
| `admin.super.password` | `ADMIN_SUPER_PASSWORD` | 空 | 首次创建超级管理员时必填 |
| `startup.dependency.timeout` | `STARTUP_DEPENDENCY_TIMEOUT` | `30` | 启动依赖校验超时（秒） |

示例：

```bash
SERVER_ADDRESS=:8080 DATABASE_DEFAULT_HOST=10.0.0.5 go run . serve
```

> 说明：`config.yaml` 与 `docker-compose.yml` 中的账号密码均为本地开发默认值，生产环境务必通过环境变量注入真实凭据，代码中不硬编码任何生产凭据。

## 启动连通性校验行为

- 服务启动时校验 MySQL、Redis 与数据库 schema；迁移缺失或 dirty 时拒绝启动。
- 依赖尚未就绪时会以 1 秒为间隔重试，最长持续 `STARTUP_DEPENDENCY_TIMEOUT` 秒。
- 超时或失败时，进程会输出明确的错误日志并以非零状态退出，不会静默忽略。
