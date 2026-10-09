# SurgeCart（my-shop）

基于 [GoFrame v2](https://goframe.org) 的电商项目，以普通订单和秒杀为两条核心交易链路。仓库已包含前台用户与后台管理员身份、商品目录、购物车、库存、普通订单、秒杀，以及评价、收藏等扩展功能。当前重点是验证业务正确性与故障恢复；性能和部署结论以今后发布的实测记录为准。

> **第一次阅读？**先看[5 分钟项目导览](docs/project-overview.md)。长期设计见 [`docs/design/`](docs/design/)；`.agent/tasks/` 保留各任务的历史决策与验收证据。

## 当前实现

| 领域 | 仓库中的实现 |
| --- | --- |
| 身份与权限 | 前台用户 JWT + Redis 会话、Refresh Token 轮换与会话撤销；后台管理员独立身份域与 RBAC |
| 商品与购买 | 分类、商品、SKU、库存、地址、购物车；普通订单含服务端定价、快照、幂等、状态机与超时取消 |
| 秒杀 | V1 MySQL 正确性约束、V2 Redis + Lua 准入、V3 MySQL 出队表异步落单、V4 故障恢复与审计、V5 限流/排队上限/熔断/指标与逐级压测脚本 |
| 扩展能力 | 评价、收藏、点赞、轮播图、推荐位、文章等 |
| 工程基础 | 版本化数据库迁移、Docker Compose 开发依赖、测试与 Makefile 生命周期脚本 |

**能力边界：**普通订单使用 Mock 支付，没有接入真实支付机构；秒杀订单下单即成交，不走普通订单的支付/取消流程。秒杀异步队列使用 MySQL 出队表，没有引入外部 MQ。V5 已提供延迟分位数指标和逐级压测脚本，但仓库尚未发布包含环境、负载和结果的稳定性能报告；Kubernetes 部署也未形成已验证结论，因此这里不声明生产容量或 P99 目标已达成。

## 阅读路径

1. [项目导览](docs/project-overview.md)：了解系统和两条核心交易链路。
2. [普通订单设计](docs/design/order.md)与[秒杀设计](docs/design/flash-sale.md)：深入状态机、业务不变量、一致性与故障处理。
3. [订单核心逻辑验证](.agent/tasks/order-v1/core-logic.md)、[秒杀 V4 核心逻辑验证](.agent/tasks/flash-sale-v4/core-logic.md)与[秒杀 V5 交付证据](.agent/tasks/flash-sale-v5/delivery.md)：查看设计如何对应实现和测试。
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
├── prometheus/             # Prometheus 抓取配置（测试环境）
│   └── prometheus.yml
├── docker-compose.yml      # MySQL / Redis / Prometheus 开发依赖
├── Makefile                # 本地生命周期入口
├── main.go                 # 程序入口
└── go.mod
```

## 快速开始

### 推荐：两阶段初始化

推荐使用两阶段初始化，先准备依赖容器与 `.env`，再启动后端（详见下文「本地开发两阶段初始化与真实 E2E」）：

```bash
make init    # 第一阶段：准备 MySQL/Redis 容器与 .env（不要求七牛凭据、不启动后端）
# 编辑 .env 填入七牛配置后：
make up      # 第二阶段：加载 .env → 校验七牛配置与 bucket 可用性 → 启动后端
```

`make up` 会启动 MySQL/Redis/Prometheus 依赖容器、构建应用、执行七牛与超管预检、迁移并启动 HTTP 服务。默认访问 `http://127.0.0.1:8000/health`；`make down` 停止应用与依赖容器、保留数据卷；更多目标见 `make help`。

### 其他启动方式

`make bootstrap` 一键初始化开发环境：启动 MySQL/Redis、构建应用、执行数据库迁移并启动 HTTP 服务（首次启动需提供本地超级管理员密码）：

```bash
export ADMIN_SUPER_PASSWORD='your-local-password'
make bootstrap
make health
```

也可以手动启动。`serve` 只检查 schema，不自动建表，因此必须先执行迁移：

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
| `auth.jwt.secret` | `AUTH_JWT_SECRET` | 开发默认值 | JWT 签名密钥（HS256，生产必须替换为 ≥32 字节随机密钥） |
| `admin.super.username` | `ADMIN_SUPER_USERNAME` | `admin` | 超级管理员用户名 |
| `admin.super.password` | `ADMIN_SUPER_PASSWORD` | 空 | 超级管理员初始密码（仅超管不存在时创建需要，生产必须注入） |
| `startup.dependency.timeout` | `STARTUP_DEPENDENCY_TIMEOUT` | `30` | 启动依赖校验超时（秒） |
| `qiniu.access_key` | `QINIU_ACCESS_KEY` | 空 | 七牛 AccessKey（凭据标识，仅环境变量注入） |
| `qiniu.secret_key` | `QINIU_SECRET_KEY` | 空 | 七牛 SecretKey（机密，仅环境变量注入） |
| `qiniu.bucket` | `QINIU_BUCKET` | 空 | 七牛存储空间名（非敏感） |
| `qiniu.domain` | `QINIU_DOMAIN` | 空 | 七牛对外访问域名（非敏感） |
| `qiniu.region` | `QINIU_REGION` | `z2` | 七牛区域 |
| `qiniu.token_ttl` | `QINIU_TOKEN_TTL` | `3600` | 上传凭证有效期（秒） |
| `qiniu.max_file_size` | `QINIU_MAX_FILE_SIZE` | `10485760` | 单文件大小上限（字节） |

示例：

```bash
SERVER_ADDRESS=:8080 DATABASE_DEFAULT_HOST=10.0.0.5 go run . serve
```

### 本地 .env 与敏感凭据

- 新增模板 `.env.example`（仅变量名/安全示例，可提交）；`make init` 会在 `.env` 不存在时自动从 `.env.example` 复制生成 `.env`（已存在则保留不覆盖），填写本地真实值（`.env` 已被 `.gitignore` 忽略，绝不提交）。
- 本地通过 `make up` / `make bootstrap` 启动时，`scripts/lib.sh` 自动加载根目录 `.env` 到环境变量；Docker Compose 亦会读取 `.env` 作变量替换。
- 敏感凭据（`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY` 等）**仅通过环境变量注入**：本地放 `.env`，CI/生产经平台 Secret / 环境变量注入。`manifest/config/config.yaml` 中这些字段保持空值，不写入任何真实凭据。

> 说明：`config.yaml` 与 `docker-compose.yml` 中的账号密码均为本地开发默认值，生产环境务必通过环境变量注入真实凭据，代码中不硬编码任何生产凭据。

## 启动连通性校验行为

- 服务启动时校验 MySQL、Redis 与数据库 schema；迁移缺失或 dirty 时拒绝启动。
- 依赖尚未就绪时会以 1 秒为间隔重试，最长持续 `STARTUP_DEPENDENCY_TIMEOUT` 秒。
- 超时或失败时，进程会输出明确的错误日志并以非零状态退出，不会静默忽略。

## 指标与 Prometheus（测试环境）

秒杀指标（`flashsale_*`）默认关闭；需以 `flash_sale.metrics.enabled=true` 运行服务（环境变量 `FLASH_SALE_METRICS_ENABLED=true`）后，`GET /metrics` 才会以 Prometheus 文本格式暴露。Prometheus 随 `make up` 一并启动（端口 `9090`，可用 `PROMETHEUS_PORT` 覆盖），抓取配置见 `prometheus/prometheus.yml`，其指向宿主机 `:8000` 的 `/metrics` 并经 `host.docker.internal` 打通网络。

```bash
export ADMIN_SUPER_PASSWORD='your-local-password'
FLASH_SALE_METRICS_ENABLED=true make up
make health                       # 校验 App / MySQL / Redis / Prometheus 全部健康
curl http://127.0.0.1:9090/api/v1/targets   # 查看 my-shop 目标是否 UP
```

> 指标暴露范围与鉴权语义不变：仅在 `flash_sale.metrics.enabled=true` 时暴露，无独立鉴权入口；生产环境需保证该端点仅内网/监控网段可达。

## 本地开发两阶段初始化与真实 E2E

七牛云为 `serve` 启动的 required dependency：启动时会 fail-fast 校验配置（AK/SK/Bucket/Domain 存在性）并对指定 bucket 执行最小权限只读可用性检查（`GetBucketInfo`），任一失败进程非零退出。

- `make init`（第一阶段）：检查 Docker/Docker Compose → 启动 MySQL/Redis 容器 → 检查根目录 `.env`（不存在则从 `.env.example` 复制生成，已存在保留不覆盖）→ 提示编辑 `.env` 填七牛 AK/SK/Bucket/Domain 并执行 `make up`。本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret；幂等可重复执行。
- `make up`（第二阶段）：加载 `.env` → 校验 MySQL/Redis → 构建应用 → `my-shop qiniu check` 预检（结构 + 存在性 + 真实 bucket 可用性，失败非零退出、不启动后端）→ 迁移 → 启动后端（serve 自身再次 fail-fast）。
- `make test-storage`（独立真实 E2E）：走真实 HTTP 链路「注册 → 登录 → 签发 token → 直传真实 1×1 PNG → 校验 `final_url` HTTP 200 且 Content-Type=image/png → 用返回 key 删除测试对象」；缺真实凭据或任一环节失败 → 非零退出，不用 Mock/假凭据冒充通过。

`.env` 为未跟踪本地文件，不随 Git/分支/worktree/容器传播；构建与单元测试（`go build ./...`、`go test ./...`）不依赖 `.env` 存在即可通过。
