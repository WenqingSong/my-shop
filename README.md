# my-shop

基于 [GoFrame v2](https://goframe.org) 的电商项目工程骨架。

本仓库当前仅包含工程初始化内容：可编译、可启动的 HTTP 服务骨架，以及 MySQL / Redis 依赖接入与启动连通性校验、健康检查接口。暂不包含任何具体业务模块。

## 环境要求

- Go 1.23+（开发使用 1.24）
- Docker 及 Docker Compose

## 目录结构

```
.
├── api/                    # 对外 API 定义（请求/响应结构体）
│   └── health/v1/          # 健康检查接口定义
├── internal/
│   ├── boot/               # 启动引导：配置注入与依赖连通性校验
│   ├── cmd/                # 命令行入口
│   ├── controller/         # 控制器
│   ├── logic/              # 业务逻辑实现
│   └── service/            # 服务接口定义
├── manifest/config/        # 配置文件
│   └── config.yaml
├── docker-compose.yml      # MySQL / Redis 依赖容器编排
├── main.go                 # 程序入口
└── go.mod
```

## 快速开始

推荐使用两阶段初始化（详见下文「本地开发两阶段初始化」）：

```bash
make init    # 第一阶段：准备 MySQL/Redis 容器与 .env（不要求七牛凭据、不启动后端）
# 编辑 .env 填入七牛真实配置后：
make up      # 第二阶段：加载 .env → 校验七牛配置与 bucket 可用性 → 启动后端
```

### 1. 拉起依赖容器（MySQL、Redis）

```bash
docker compose up -d
```

首次执行会拉取镜像并初始化，MySQL 初始化约需 10~30 秒。

### 2. 启动服务

```bash
go run main.go
```

服务启动时会校验 MySQL 与 Redis 连通性，成功后监听配置端口（默认 `:8000`）。

### 3. 访问健康检查接口

```bash
curl http://127.0.0.1:8000/health
# {"code":0,"message":"OK","data":{"status":"ok","time":"..."}}
```

## 构建与测试

```bash
go build ./...     # 编译
go vet ./...       # 静态检查
go test ./...      # 单元测试
```

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
SERVER_ADDRESS=:8080 DATABASE_DEFAULT_HOST=10.0.0.5 go run main.go
```

### 本地 .env 与敏感凭据

- 新增模板 `.env.example`（仅变量名/安全示例，可提交）；`make init` 会在 `.env` 不存在时自动从 `.env.example` 复制生成 `.env`（已存在则保留不覆盖），填写本地真实值（`.env` 已被 `.gitignore` 忽略，绝不提交）。
- 本地通过 `make up` / `make bootstrap` 启动时，`scripts/lib.sh` 自动加载根目录 `.env` 到环境变量；Docker Compose 亦会读取 `.env` 作变量替换。
- 敏感凭据（`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY` 等）**仅通过环境变量注入**：本地放 `.env`，CI/生产经平台 Secret / 环境变量注入。`manifest/config/config.yaml` 中这些字段保持空值，不写入任何真实凭据。

> 说明：`config.yaml` 与 `docker-compose.yml` 中的账号密码均为本地开发默认值，生产环境务必通过环境变量注入真实凭据，代码中不硬编码任何生产凭据。

## 启动连通性校验行为

- 服务启动时依次校验 MySQL、Redis 连通性，成功后才开始监听端口。
- 依赖尚未就绪时会以 1 秒为间隔重试，最长持续 `STARTUP_DEPENDENCY_TIMEOUT` 秒。
- 超时或失败时，进程会输出明确的错误日志并以非零状态退出，不会静默忽略。

## 本地开发两阶段初始化与真实 E2E

七牛云为 `serve` 启动的 required dependency：启动时会 fail-fast 校验配置（AK/SK/Bucket/Domain 存在性）并对指定 bucket 执行最小权限只读可用性检查（`GetBucketInfo`），任一失败进程非零退出。

- `make init`（第一阶段）：检查 Docker/Docker Compose → 启动 MySQL/Redis 容器 → 检查根目录 `.env`（不存在则从 `.env.example` 复制生成，已存在保留不覆盖）→ 提示编辑 `.env` 填七牛 AK/SK/Bucket/Domain 并执行 `make up`。本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret；幂等可重复执行。
- `make up`（第二阶段）：加载 `.env` → 校验 MySQL/Redis → 构建应用 → `my-shop qiniu check` 预检（结构 + 存在性 + 真实 bucket 可用性，失败非零退出、不启动后端）→ 迁移 → 启动后端（serve 自身再次 fail-fast）。
- `make test-storage`（独立真实 E2E）：走真实 HTTP 链路「注册 → 登录 → 签发 token → 直传真实 1×1 PNG → 校验 `final_url` HTTP 200 且 Content-Type=image/png → 用返回 key 删除测试对象」；缺真实凭据或任一环节失败 → 非零退出，不用 Mock/假凭据冒充通过。

`.env` 为未跟踪本地文件，不随 Git/分支/worktree/容器传播；构建与单元测试（`go build ./...`、`go test ./...`）不依赖 `.env` 存在即可通过。
