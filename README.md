# SurgeCart（my-shop）

## 项目介绍

SurgeCart（`my-shop`）是基于 [GoFrame v2](https://goframe.org) 的电商后端项目。项目以**普通订单**与**秒杀**为两条核心交易链路，覆盖前台用户与后台管理员的身份与权限体系，以及商品、购物车、库存、优惠券、退款、运营内容等电商核心域。

项目侧重业务正确性、一致性与故障恢复：MySQL 始终是交易事实的最终来源，关键写路径以事务、条件更新与状态机保证不超卖、不重复补偿；并以 Docker Compose 提供可复现的本地运行与测试环境。

## 核心能力

### 秒杀系统

按「先正确、后加速」逐代演进，每一步保留五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存）：

- **Redis + Lua 准入闸门**：活动与库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，无效请求在 Redis 层快速失败。
- **MySQL 出队表异步落单**：下单拆成「入队快速响应 + 后台消费落单」两阶段，队列以 MySQL 出队表（`FOR UPDATE SKIP LOCKED`）实现。
- **故障恢复**：幂等补偿、崩溃重投、死信修复与 append-only 审计。
- **容量保护与可观测**：用户级/活动级限流、排队软上限、熔断降级、Prometheus 指标与逐级压测脚本。

Redis 预扣仅作准入闸门，MySQL 始终是订单与已售数量的最终事实来源。详见 [秒杀设计](docs/design/flash-sale.md)。

### 订单一致性

- **服务端定价与快照**：下单时重读 SKU 价格，保存商品、价格、地址快照；下单后改价不影响既有订单。
- **幂等去重**：客户端幂等键 + 数据库唯一约束，同键同内容返回既有订单、不重复扣库存。
- **单事务落单**：创建订单 = 同一 MySQL 事务「订单 + 订单项 + 条件扣库存（+ 券预占）」，任一步失败整体回滚。
- **状态机 + 条件更新**：待支付 → 已支付 → 已发货 → 已收货 → 已完成，以及取消、退款两条出口；条件更新 + `RowsAffected` 保证不超卖、取消/退款只补偿一次库存。
- **超时取消**：后台扫描器逐单原子取消，多实例并发靠条件状态更新串行化。

详见 [订单设计](docs/design/order.md)、[库存设计](docs/design/inventory.md)。

### IAM / RBAC

- **有状态会话**：JWT（HS256）作为访问凭证，Redis 会话承载存在性与撤销标记；验签后还必须通过会话有效性校验。
- **Refresh Token 轮换**：支持轮换、Token Family 血缘与重放检测，refresh token 仅以哈希落库。
- **双身份域隔离**：前台用户与后台管理员使用独立凭据表、独立会话前缀与独立认证中间件。
- **细粒度 RBAC**：角色/权限/绑定四表模型，权限粒度 `资源:动作`（如 `order:ship`、`coupon:create`）；后台写接口经「认证 + 授权」双层中间件保护，逐接口校验权限。

详见 [IAM 设计](docs/design/iam.md)、[RBAC 设计](docs/design/rbac.md)。

### 工程化体系

- **容器化交付**：Docker 多阶段构建（`CGO_ENABLED=0` 静态编译）与非 root 运行，Compose 一键编排 nginx/app/MySQL/Redis/Prometheus。
- **版本化迁移**：golang-migrate 管理 schema，`migrate up` 幂等 + `GET_LOCK` 串行，schema 未就绪不对外。
- **凭据分离**：敏感值仅经环境变量注入（`${VAR:?}` 必填，无内置默认回退），`config.yaml` 敏感字段保持空值。
- **健康检查与优雅停机**：`/health` liveness、`/ready` readiness（DB/Redis 不可达返回 503），`SIGTERM` 取消根 ctx 后 drain 在途请求再退出。
- **可观测与 CI/CD**：Prometheus 秒杀指标与 recording rules；CNB 流水线覆盖测试、竞态检测、构建与镜像发布。

详见 [部署设计](docs/design/deployment.md)、[数据库迁移](docs/design/migration.md)。

### 业务能力一览

| 领域 | 能力 |
| --- | --- |
| 商品与库存 | 商品 SPU、分类树、SKU 独立售价、条件扣减防负库存与库存流水 |
| 购物车与订单 | 购物车/直接购买下单、服务端定价与快照、幂等去重、订单状态机、超时取消、发货与收货 |
| 优惠券 | 平台固定金额满减券：领取唯一、总量不超发、下单预占、核销、取消释放 |
| 退款 | 已支付未发货整单退款：申请/审核/执行状态机与 append-only 审计 |
| 运营能力 | 运营数据大屏、轮播图、推荐位、文章、评价、收藏、点赞 |

## 技术栈

| 层 | 技术选型 |
| --- | --- |
| 后端框架 | GoFrame v2 |
| 语言 | Go 1.23（纯 Go 依赖，支持 `CGO_ENABLED=0` 静态编译） |
| 数据库 | MySQL 8.0（业务数据与 migration 状态权威来源） |
| 缓存 | Redis 7（登录会话、秒杀准入闸门与派生缓存） |
| 对象存储 | 七牛云（上传直传凭证签发） |
| 反向代理 | Nginx（对外唯一 HTTP 入口） |
| 监控 | Prometheus（秒杀指标） |
| 容器化 | Docker + Docker Compose |
| CI/CD | CNB 流水线（`.cnb.yml`） |

## 系统架构

```
                客户端（浏览器 / App / 压测脚本）
                          │ HTTPS
                          ▼
              ┌─────────────────────────┐
              │  Nginx（唯一对外 HTTP 入口）│
              └─────────────────────────┘
                          │ :8000（Compose 内网）
                          ▼
        ┌──────────────────────────────────────┐
        │              API 层                   │
        │  Middleware：RequestID / 认证授权 /    │
        │  响应包装；Controller：HTTP 适配       │
        └──────────────────────────────────────┘
                          │
                          ▼
        ┌──────────────────────────────────────┐
        │             业务模块                  │
        │  Service（接口）→ Logic（规则/流程）    │
        │  IAM / 订单 / 库存 / 优惠券 / 退款 /   │
        │  秒杀 / CMS / 对象存储                │
        └──────────────────────────────────────┘
                          │
              ┌───────────┴───────────┐
              ▼                       ▼
        ┌──────────┐          ┌──────────────┐
        │  MySQL   │          │    Redis      │
        │ 交易/身份 │          │ 会话/秒杀准入  │
        │ 权威事实  │          │ 派生缓存      │
        └──────────┘          └──────────────┘
```

分层遵循 GoFrame 惯例：Controller 不直接操作数据库，DAO 不承载业务规则，Logic 不处理 HTTP 细节；`context.Context` 沿 Controller → Logic → 数据访问/外部依赖传播。MySQL 是订单、库存、已售数量的权威事实来源，Redis 为派生缓存；订单超时取消、秒杀消费与对账由应用内后台扫描器执行。

详见 [系统架构设计](docs/design/architecture.md)。

## 快速开始

### 开发环境镜像（可选）

为降低开发环境搭建成本，项目提供基于 JetBrains GoLand 的公开开发环境镜像，可用于搭建 SurgeCart 开发环境。

**镜像地址：**

```text
docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1
```

**拉取镜像：**

```bash
docker pull docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1
```

开发者可使用该镜像创建开发容器，在容器内获取 SurgeCart 源码并开展 Go 后端开发、调试与测试。

**注意事项：**

- 该镜像用于提供开发工具环境，不包含 SurgeCart 业务服务的完整部署。
- MySQL、Redis、Nginx 等依赖仍需按照下方「两阶段初始化」流程配置与启动。
- 具体开发容器的启动参数、端口和 Docker 权限要求，以该镜像的实际配置为准。

### 两阶段初始化

```bash
make init    # 第一阶段：准备 MySQL/Redis 容器与 .env（不启动后端）
# 编辑 .env 填入 MySQL 密码、JWT 密钥、七牛配置与超管密码后：
make up      # 第二阶段：构建并启动 nginx/app/MySQL/Redis/Prometheus
make health  # 校验全部容器健康
```

也可用 `make bootstrap` 一键完成（等价于 `make init` + `make up`）。

`make up` 会构建后端、Nginx 与 Prometheus 镜像并启动五个容器；app 容器 entrypoint 先执行幂等 migration，再启动 HTTP 服务（缺失关键配置 fail-fast）。Nginx 为对外唯一 HTTP 入口，默认 `http://127.0.0.1:8080/health`（`NGINX_PORT` 可覆盖）；`make down` 停止并移除容器、保留数据卷。

### 健康检查

```bash
curl http://127.0.0.1:8080/health   # liveness：进程存活即 200
```

## 配置说明

默认配置位于 [`manifest/config/config.yaml`](manifest/config/config.yaml)，可通过环境变量覆盖：配置键中的 `.` 替换为 `_` 并转为大写（如 `database.default.pass` → `DATABASE_DEFAULT_PASS`）。

敏感凭据（JWT 密钥、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码）**仅经环境变量注入**：本地放 `.env`（由 `make init` 从 `.env.example` 生成，`.env` 已被 Git 忽略），`config.yaml` 中敏感字段保持空值，`docker-compose.yml` 对必填项使用 `${VAR:?}` 语法，缺失即启动失败，无内置默认回退。

| 环境变量 | 说明 |
| --- | --- |
| `AUTH_JWT_SECRET` | JWT 签名密钥（HS256，必填，≥32 字节） |
| `DATABASE_DEFAULT_PASS` | MySQL 密码（必填） |
| `ADMIN_SUPER_PASSWORD` | 超级管理员初始密码（仅超管不存在时创建需要） |
| `QINIU_ACCESS_KEY` / `QINIU_SECRET_KEY` / `QINIU_BUCKET` / `QINIU_DOMAIN` | 七牛直传凭证与访问域名（启动 required dependency） |

完整配置项与默认值见 `manifest/config/config.yaml`。

## 开发命令

统一用 `make` 管理项目生命周期，`make help` 可查看全部目标。

| 命令 | 说明 |
| --- | --- |
| `make bootstrap` | 一键初始化：`make init` + `make up` |
| `make init` | 准备 MySQL/Redis 容器与 `.env` |
| `make up` | 构建并启动完整容器栈 |
| `make down` | 停止并移除容器（保留数据卷） |
| `make restart` | 先停后启 |
| `make status` | 查看各服务运行状态 |
| `make logs` | 查看容器日志 |
| `make health` | 校验各服务健康状态 |
| `make build` | 本地静态编译 `bin/my-shop`（与 Dockerfile 一致） |
| `make docker-build` | 构建发布镜像并打标签（短 SHA + `latest`） |
| `make migrate-up` | 应用所有未执行 migration |
| `make migrate-version` | 查看当前 migration 版本与 dirty 状态 |
| `make migrate-force` | 标记指定版本已应用（`make migrate-force VERSION=<14位时间戳>`） |
| `make config-check` | 启动前预检（七牛与超管条件校验） |
| `make clean` | 清理编译产物与临时文件 |

其余目标（`deps-start` / `docker-push` / `test-unit` / `test-integration` / `test-prometheus` / `test-container` / `test-nginx` / `test-lib`）见 `make help`。

## 测试

```bash
make test       # 统一入口：go vet + 单元测试 + 集成测试 + Registry 校验 + 容器化静态断言
make test-race  # 竞态检测
```

`make test` 依次执行 `go vet`、单元测试（不依赖 MySQL/Redis）、集成测试（需 MySQL/Redis）、Registry 一致性校验与容器化静态断言。集成测试前需先就绪依赖（`make init` 或 `make deps-start`）。独立目标：`test-unit` / `test-integration` / `test-race` / `test-prometheus` / `test-container` / `test-nginx` / `test-lib` / `test-storage`（真实存储 E2E，需七牛凭据）。

## 部署

- **本地/单机**：Docker Compose 编排 nginx/app/MySQL/Redis/Prometheus 五个服务，Nginx 为对外唯一入口，app 仅暴露 Compose 内网端口；`make init` + `make up` 两阶段拉起。
- **镜像发布**：`make docker-build` / `make docker-push` 构建并推送短 SHA + `latest` 双标签镜像（同源同 digest）。
- **CI/CD**：CNB 流水线（`.cnb.yml`）按「测试 → 竞态检测 → 构建 → 镜像发布」顺序执行，任一阶段失败即中断；`latest` 仅指向测试全绿的构建。

详细部署模型、启动生命周期、持久化与健康检查语义见 [部署设计](docs/design/deployment.md)。

## 文档索引

- [项目导览](docs/project-overview.md)：系统与两条核心交易链路的总览。
- [系统架构](docs/design/architecture.md)：分层结构、模块边界、核心业务流程与基础设施。
- 核心设计：[订单](docs/design/order.md)、[秒杀](docs/design/flash-sale.md)、[商品](docs/design/product.md)、[分类](docs/design/category.md)、[SKU](docs/design/sku.md)、[库存](docs/design/inventory.md)、[购物车](docs/design/cart.md)、[IAM](docs/design/iam.md)、[RBAC](docs/design/rbac.md)、[优惠券](docs/design/coupon.md)、[退款](docs/design/refund.md)。
- 部署与运维：[容器化部署](docs/design/deployment.md)、[数据库迁移](docs/design/migration.md)、[错误码](docs/design/error-codes.md)、[对象存储](docs/design/storage.md)。
- 运营与内容：[轮播图](docs/design/banner.md)、[文章](docs/design/article.md)、[推荐位](docs/design/recommendation.md)、[评价](docs/design/review.md)、[数据大屏](docs/design/dashboard.md)、[收藏](docs/design/favorite.md)、[点赞](docs/design/like.md)。

`docs/design/*` 记录持续维护的项目级长期设计，是了解各模块现状与边界的权威来源。
