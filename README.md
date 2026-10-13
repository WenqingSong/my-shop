# SurgeCart（my-shop）

基于 [GoFrame v2](https://goframe.org) 的电商后端项目。以**普通订单**与**秒杀**为两条核心交易链路，覆盖前台用户与后台管理员的完整身份与权限体系，以及商品、购物车、库存、优惠券、退款、运营内容等电商核心域。项目侧重业务正确性、一致性与故障恢复，并以 Docker Compose 提供可复现的本地运行与测试环境。

## 核心亮点

- **秒杀**：Redis + Lua 准入闸门、MySQL 出队表异步落单、故障恢复与容量保护（限流/熔断/指标/压测），MySQL 始终是最终事实来源，五个业务不变量全程可验证。
- **订单一致性**：服务端定价与快照、幂等去重、单事务「订单 + 条件扣库存」、状态机 + 条件更新，保证不超卖、取消/退款只补偿一次。
- **IAM / RBAC**：JWT + Redis 有状态会话、Refresh Token 轮换与重放检测、前台用户与后台管理员双身份域隔离、后台细粒度 RBAC（`资源:动作` 权限码）。
- **云工程能力**：Docker 多阶段构建与非 root 运行、版本化迁移、凭据分离、优雅停机、`/health`/`/ready` 探针、Prometheus 指标与 CI/CD 流水线。

## 项目简介

| 领域 | 能力 |
| --- | --- |
| 普通订单链路 | 购物车/直接购买下单、服务端定价与快照、幂等去重、订单状态机、超时取消、Mock 支付、发货与收货 |
| 秒杀链路 | Redis + Lua 准入闸门、MySQL 出队表异步落单、故障恢复与审计、限流/熔断/指标与压测 |
| 商品与库存 | 商品 SPU（分类/展示价/图册/详情）、上下架状态机、分类树、SKU 独立售价、条件扣减防负库存与库存流水 |
| IAM / RBAC | 前台用户 JWT + Redis 有状态会话、Refresh Token 轮换与重放检测；后台管理员独立身份域与 RBAC 授权 |
| 优惠券 | 平台固定金额满减券：领取唯一、总量不超发、下单预占、支付核销、取消释放 |
| 退款 | 已支付未发货普通订单整单退款：申请/审核/执行状态机与 append-only 审计 |
| 运营能力 | 运营数据大屏、轮播图、推荐位、文章、评价、收藏、点赞 |
| 工程化能力 | 版本化数据库迁移、Docker 多阶段构建与 Compose 编排、Nginx 统一入口、Prometheus 指标、Makefile 生命周期脚本与 CI |

## 技术架构

| 层 | 技术选型 | 说明 |
| --- | --- | --- |
| 后端框架 | GoFrame v2 | HTTP API、路由、配置、ORM |
| 语言 | Go 1.23 | 纯 Go 依赖，支持 `CGO_ENABLED=0` 静态编译 |
| 数据库 | MySQL 8.0 | 业务数据与 migration 状态的权威事实来源 |
| 缓存 | Redis 7 | 登录会话、秒杀准入闸门与派生缓存 |
| 对象存储 | 七牛云 | 上传直传凭证签发（`final_url`） |
| 反向代理 | Nginx | 对外唯一 HTTP 入口，统一请求 ID 与客户端 IP 信任边界 |
| 监控 | Prometheus | 秒杀指标（QPS / 成功率 / 拒绝率 / p95 / p99） |
| 容器化 | Docker + Docker Compose | 多阶段构建、非 root 运行、nginx/app/MySQL/Redis/Prometheus 编排 |
| CI/CD | CNB 流水线（`.cnb.yml`） | 测试、竞态检测、构建、镜像发布 |

## 核心能力

### 普通订单与一致性

- **服务端定价与快照**：下单时重读 SKU 价格，保存商品、价格、地址快照；金额分原价/优惠/应付三列锁定，下单后改价不影响既有订单。
- **幂等去重**：客户端幂等键 + 数据库唯一约束（`uk_user_idempotency`），同键同内容返回既有订单、不重复扣库存。
- **状态机**：待支付 → 已支付 → 已发货 → 已收货 → 已完成，以及取消、退款两条出口；非法迁移稳定拒绝。
- **事务一致性**：创建订单 = 单 MySQL 事务「订单 + 订单项 + 条件扣库存 +（券预占）」，任一步失败整体回滚；条件更新 + `RowsAffected` 保证不超卖、取消/退款只恢复一次库存。
- **超时取消**：后台扫描器扫描 `待支付且已过期` 订单逐单原子取消，查询/支付入口懒取消兜底，多实例并发靠条件状态更新串行化。

### 秒杀架构

秒杀按「先正确、后加速」逐代演进，每一步保留五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存），MySQL 始终是最终事实来源：

- **V1（MySQL 正确性）**：条件库存扣减 + 唯一约束，先证明不超卖与幂等。
- **V2（Redis + Lua 准入）**：活动/库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，售罄与穿透请求在 Redis 层快速失败。Redis 预扣仅是闸门，最终正确性由 MySQL 兜底。
- **V3（异步落单）**：把下单拆成「入队快速响应 + 后台消费落单」两阶段，队列用 **MySQL 出队表**（`FOR UPDATE SKIP LOCKED` 出队），不引入外部 MQ。
- **V4（故障恢复）**：幂等补偿、崩溃重投、活动结束终态收敛、死信人工修复与 append-only 审计。
- **V5（容量保护与可观测）**：用户级/活动级限流、排队软上限、Redis 闸门熔断降级、Prometheus 指标与逐级压测脚本。

### 商品与库存

- **商品 SPU 主数据**：名称、品牌、分类、基础展示价、图册、详情，价格整数分；`草稿 → 上架 → 下架` 状态机（创建强制草稿、普通更新不改状态、前台仅 `on_shelf` 可见），浏览量原子自增。
- **分类树**：最多 3 级树形分类，同级名唯一、循环引用与超层校验、软下线；删除前校验无子分类且无关联商品（FK 兜底）。
- **SKU**：商品 1—N SKU 独立售价（与 SPU 展示价解耦），同商品内名称唯一，物理删除 + 稳定引用键（自增不重用）。
- **库存**：`sku_id` 1:1 库存记录，条件扣减（`WHERE quantity >= N` + `RowsAffected`）防负库存，append-only 变更流水，后台增扣与查询按 `inventory:*` 权限。

### IAM（身份认证）

- **JWT + Redis 有状态会话**：JWT（HS256）作为访问凭证，Redis 会话承载存在性与撤销标记，受保护接口验签后还必须通过会话有效性校验。
- **Refresh Token 轮换**：仅前台用户域，支持轮换、Token Family 血缘与重放（reuse）检测，refresh token 仅以 SHA-256 哈希落库。
- **两个隔离身份域**：前台用户（`users`）与后台管理员（`admins`）使用独立凭据表、独立 token 类型、独立会话前缀与独立认证中间件。
- **会话管理**：会话列表、撤销指定/其他/全部会话，管理员禁用用户时联动撤销会话与 refresh family 并递增认证版本。

### RBAC（授权）

- 后台管理员经「认证（`AdminAuth`）+ 授权（`RequirePermission`）」双层中间件保护，严格区分 401（未认证）与 403（已认证但无权）。
- 角色/权限/绑定四表模型，权限粒度 `资源:动作`（如 `order:ship`、`coupon:create`、`refund:review`）。
- 超级管理员唯一且不可变、自操作防护；后台写接口逐接口校验权限 code，无权限零写入。

### 优惠券

- 平台通用固定金额满减券（整数分），覆盖模板创建/停止、用户领取、下单用券。
- 领取唯一（每用户每模板至多一张）、总量不超发（条件更新 + `RowsAffected`）。
- 券状态机：可用 → 已预占 → 已使用，取消/超时释放回到可用；订单创建与券预占同事务，退款不返券。

### 退款

- 覆盖「已支付、未发货」普通订单的整单全额退款（金额由服务端派生，不接受客户端金额）。
- 状态机：申请中 → 审核（同意/拒绝）→ 执行（成功/失败）→ 关闭/撤销，全程 append-only 审计。
- 退款成功 = 订单 `已支付 → 已退款` + 恢复库存（同一事务，只补偿一次）；单一有效申请唯一约束 + 退款与发货互斥。

### 运营与内容

- **运营数据大屏**：用户/商品/订单/秒杀的只读聚合统计（概览、趋势、状态分布、销量 TOP、秒杀运营），严格区分订单、金额、销量口径。
- **内容与互动**：轮播图、推荐位、文章，以及商品的评价、收藏、点赞。

### 工程化与云原生能力

- **可复现交付**：Docker 多阶段构建（`CGO_ENABLED=0` 静态编译）、非 root 运行、`docker-compose.yml` 一键编排 nginx/app/MySQL/Redis/Prometheus，`make init/up/health` 两阶段初始化。
- **版本化迁移**：golang-migrate 管理 schema，`migrate up` 幂等 + `GET_LOCK` 串行，启动前置迁移，schema 未就绪不对外。
- **凭据与配置分离**：敏感值仅经环境变量注入（`${VAR:?}` 必填，无内置默认回退），`config.yaml` 敏感字段保持空值。
- **健康检查与优雅停机**：`/health` liveness（不依赖 DB/Redis）、`/ready` readiness（DB/Redis 不可达返回 503）、`SIGTERM` 取消根 ctx 后 drain 在途请求再退出。
- **可观测**：Prometheus 秒杀指标（QPS/成功率/拒绝率/p95/p99）+ recording rules；Nginx 统一 Request ID 贯通访问日志。
- **CI/CD**：CNB 流水线（测试 → 竞态检测 → 构建 → 镜像发布，短 SHA + latest 双标签），`latest` 仅指向测试全绿的构建。

## 能力边界

为保持真实，以下边界需要明确说明：

- **支付为 Mock**：普通订单的支付、退款执行均不接入真实支付机构，没有资金到账、对账与资金回调链路。
- **秒杀使用 MySQL 出队表，不是 MQ**：异步下单队列以 MySQL 表 + 应用内消费者实现，未引入 RabbitMQ/Kafka 等外部消息队列。
- **Kubernetes 尚未实现**：仅提供 Docker Compose 单实例编排；没有 K8s 部署清单、Ingress、TLS 与多实例负载均衡（`/ready` 端点已落地，可作为未来 readinessProbe 目标）。
- **未发布稳定性能指标**：已提供秒杀延迟分位数指标与逐级压测脚本，但仓库尚未发布包含明确环境、负载与结果的稳定性能报告，不声明生产容量或 P99 目标。
- 秒杀订单「下单即成交」，不走普通订单的支付/取消/退款流程；秒杀库存为普通可售库存的预分配配额（当前不自动划拨）。
- 优惠券仅支持平台通用固定金额满减，不支持百分比折扣、品类券与多券叠加。

## 快速开始

### 推荐：两阶段初始化

```bash
make init    # 第一阶段：准备 MySQL/Redis 容器与 .env（不要求七牛凭据、不启动后端）
# 编辑 .env 填入 MySQL 密码、JWT 密钥、七牛配置与超管密码后：
make up      # 第二阶段：构建并启动 nginx/app/MySQL/Redis/Prometheus 容器
make health  # 校验全部容器健康
```

如需一键完成，可用 `make bootstrap`（等价于依次执行 `make init` + `make up`）。

`make up` 会构建后端、Nginx 与 Prometheus 镜像并启动 nginx/app/MySQL/Redis/Prometheus 五个容器：app 容器 entrypoint 先执行幂等 migration（`migrate up`），再由 `serve` 校验七牛配置与超管创建条件并启动 HTTP 服务（缺失 fail-fast）。Nginx 为对外唯一 HTTP 入口，默认访问 `http://127.0.0.1:8080/health`（`NGINX_PORT` 可覆盖）；`make down` 停止并移除容器、保留数据卷；更多目标见 `make help`。

### 生命周期命令

统一用 `make` 管理项目生命周期，`make help` 可随时查看全部目标：

| 命令 | 说明 |
| --- | --- |
| `make bootstrap` | 一键初始化：`make init` + `make up`（准备依赖容器与 `.env` → 构建并启动完整容器栈） |
| `make init` | 第一阶段：准备 MySQL/Redis 容器与 `.env`（不要求七牛凭据、不启动后端） |
| `make up` | 第二阶段：构建并启动完整容器栈（nginx/app/mysql/redis/prometheus） |
| `make down` | 停止并移除容器（保留 MySQL/Redis/本地存储数据卷） |
| `make restart` | 先停后启，恢复服务可用 |
| `make status` | 查看 App / MySQL / Redis / Prometheus 运行状态 |
| `make logs` | 查看应用与依赖容器日志 |
| `make health` | 校验 App / MySQL / Redis / Prometheus 健康状态 |
| `make test` | 统一测试入口：`go vet` + 单元测试 + 集成测试 + Registry 校验 + 容器化静态断言 |
| `make test-race` | 执行 `go test -race`（进程内共享内存并发逻辑） |
| `make test-storage` | 独立真实存储 E2E（需真实七牛凭据） |
| `make build` | 本地静态编译 `bin/my-shop`（`CGO_ENABLED=0` / `-trimpath` / `-ldflags="-s -w"`，与 Dockerfile 一致） |
| `make docker-build` | 构建发布镜像并打标签（短 SHA + `latest`，双标签同源） |
| `make migrate-up` | 应用所有未执行 migration（`my-shop migrate up`） |
| `make config-check` | 启动前统一预检：`qiniu check` + `admin check`（七牛与超管条件校验） |
| `make clean` | 清理编译产物与临时文件（不删除源码与持久数据） |

更细粒度的目标（`deps-start` / `docker-push` / `migrate-version` / `migrate-force` / `test-prometheus` / `test-container` / `test-nginx` / `test-lib`）见 `make help`。

开发命令已全面容器化：应用与依赖统一经 Docker Compose 运行，杜绝宿主机与容器双启动；宿主机直接启动仅保留为调试手段（见下）。

### 宿主机手动启动（非容器化，仅调试用）

`serve` 只检查 schema、不自动建表，因此必须先执行迁移；`config.yaml` 已清空敏感默认值，需显式注入 JWT 密钥与 MySQL 密码：

```bash
docker compose up -d mysql redis
export DATABASE_DEFAULT_PASS='<MySQL root 密码>'
export AUTH_JWT_SECRET='<你的随机 ≥32 字节密钥>'
export ADMIN_SUPER_PASSWORD='<超管初始密码>'
go run . migrate up
go run . serve
```

服务默认监听 `:8000`。

### 访问健康检查接口

```bash
curl http://127.0.0.1:8080/health   # liveness，返回 {"code":0,"message":"OK","data":{"status":"ok",...}}
curl http://127.0.0.1:8000/ready    # readiness，DB/Redis 不可达时返回 503
```

## 目录结构

```
.
├── api/                        # 对外 API 契约（请求/响应结构体）
├── internal/
│   ├── auth/                   # JWT 签发/校验、会话与 refresh token 工具
│   ├── boot/                   # 启动装配、依赖/schema 就绪检查、超管与权限 seed
│   ├── cmd/                    # 命令行入口、前后台路由、后台扫描器
│   ├── codes/                  # 集中错误码
│   ├── controller/             # HTTP 控制器
│   ├── logic/                  # 业务逻辑
│   ├── metrics/                # 秒杀 Prometheus 指标
│   ├── middleware/             # 认证、权限、响应、请求 ID 等中间件
│   ├── migrations/             # 版本化数据库迁移（golang-migrate）
│   ├── service/                # 服务接口定义
│   └── storage/                # 本地文件存储
├── docs/
│   ├── design/                 # 项目级长期设计（订单/秒杀/IAM/RBAC/优惠券/退款等）
│   ├── tasks/                  # 历史任务资料
│   └── project-overview.md     # 项目导览
├── manifest/config/config.yaml # 默认配置（敏感字段留空，经环境变量注入）
├── nginx/                      # Nginx 反向代理镜像与配置
├── prometheus/                 # Prometheus 抓取配置与 recording rules
├── scripts/                    # 生命周期脚本与秒杀压测脚本
├── storage/                    # 本地文件存储（运行时生成）
├── .cnb.yml                    # CNB CI 流水线
├── .env.example                # 环境变量模板（不含真实凭据）
├── docker-compose.yml          # nginx/app/MySQL/Redis/Prometheus 编排
├── Dockerfile                  # 后端多阶段构建（非 root 运行）
├── Makefile                    # 本地生命周期入口
├── main.go                     # 程序入口
└── go.mod / go.sum
```

## 配置与环境变量

默认配置位于 `manifest/config/config.yaml`，可通过环境变量覆盖关键配置项（无需修改代码或配置文件）。命名规则：配置键中的 `.` 替换为 `_` 并转为大写。

| 配置键 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `server.address` | `SERVER_ADDRESS` | `:8000` | HTTP 监听地址 |
| `server.graceful_timeout` | `SERVER_GRACEFUL_TIMEOUT` | `10` | 优雅关闭 HTTP drain 超时（秒） |
| `database.default.type` | `DATABASE_DEFAULT_TYPE` | `mysql` | 数据库类型 |
| `database.default.host` | `DATABASE_DEFAULT_HOST` | `127.0.0.1` | MySQL 主机（容器内为 `mysql`） |
| `database.default.port` | `DATABASE_DEFAULT_PORT` | `3306` | MySQL 端口 |
| `database.default.user` | `DATABASE_DEFAULT_USER` | `root` | MySQL 账号 |
| `database.default.pass` | `DATABASE_DEFAULT_PASS` | 空 | MySQL 密码（敏感，**必填**，仅环境变量注入，无默认回退） |
| `database.default.name` | `DATABASE_DEFAULT_NAME` | `my_shop` | 数据库名 |
| `database.default.charset` | `DATABASE_DEFAULT_CHARSET` | `utf8mb4` | 连接字符集 |
| `database.default.debug` | `DATABASE_DEFAULT_DEBUG` | `false` | SQL 调试日志开关 |
| `redis.default.address` | `REDIS_DEFAULT_ADDRESS` | `127.0.0.1:6379` | Redis 地址（容器内为 `redis:6379`） |
| `redis.default.db` | `REDIS_DEFAULT_DB` | `0` | Redis DB |
| `redis.default.pass` | `REDIS_DEFAULT_PASS` | 空 | Redis 密码 |
| `auth.jwt.secret` | `AUTH_JWT_SECRET` | 空 | JWT 签名密钥（HS256，敏感，**必填**，≥32 字节，无默认回退） |
| `auth.session.ttl` | `AUTH_SESSION_TTL` | `3600` | 会话 TTL（秒） |
| `auth.refresh.ttl` | `AUTH_REFRESH_TTL` | `2592000` | refresh token 有效期（秒，30 天） |
| `admin.super.username` | `ADMIN_SUPER_USERNAME` | `admin` | 超级管理员用户名 |
| `admin.super.password` | `ADMIN_SUPER_PASSWORD` | 空 | 超级管理员初始密码（仅超管不存在时创建需要） |
| `dashboard.timezone` | `DASHBOARD_TIMEZONE` | `Asia/Shanghai` | 运营统计日期切分时区（仅无 DST 时区） |
| `order.pay_timeout` | `ORDER_PAY_TIMEOUT` | `900` | 支付超时时长（秒） |
| `order.cancel_scan_interval` | `ORDER_CANCEL_SCAN_INTERVAL` | `60` | 超时未支付订单扫描周期（秒） |
| `flash_sale.reconcile_scan_interval` | `FLASH_SALE_RECONCILE_SCAN_INTERVAL` | `60` | 秒杀缓存对账/回补周期（秒） |
| `flash_sale.consume_scan_interval` | `FLASH_SALE_CONSUME_SCAN_INTERVAL` | `1` | 秒杀异步下单消费周期（秒） |
| `flash_sale.metrics.enabled` | `FLASH_SALE_METRICS_ENABLED` | `false` | 秒杀指标开关（开启后暴露 `GET /metrics`） |
| `storage.local.root` | `STORAGE_LOCAL_ROOT` | `./storage` | 本地文件存储根目录（容器内 `/app/storage`） |
| `qiniu.access_key` | `QINIU_ACCESS_KEY` | 空 | 七牛 AccessKey（启动 required dependency） |
| `qiniu.secret_key` | `QINIU_SECRET_KEY` | 空 | 七牛 SecretKey（敏感） |
| `qiniu.bucket` | `QINIU_BUCKET` | 空 | 七牛存储空间名（启动 required dependency） |
| `qiniu.domain` | `QINIU_DOMAIN` | 空 | 七牛对外访问域名（启动 required dependency） |
| `qiniu.region` | `QINIU_REGION` | `z2` | 七牛区域 |
| `qiniu.token_ttl` | `QINIU_TOKEN_TTL` | `3600` | 上传凭证有效期（秒） |
| `qiniu.max_file_size` | `QINIU_MAX_FILE_SIZE` | `10485760` | 单文件大小上限（字节） |
| `startup.dependency.timeout` | `STARTUP_DEPENDENCY_TIMEOUT` | `30` | 启动依赖校验超时（秒） |

秒杀容量保护相关（限流/排队软上限/熔断）默认关闭，启用与阈值见 `config.yaml` 的 `flash_sale.rate_limit` / `queue_capacity` / `circuit_breaker` 段，压测或生产环境建议开启。

### 敏感凭据与 `.env`

- 模板 `.env.example`（仅变量名，可提交）；`make init` 在 `.env` 不存在时自动复制生成 `.env`（已存在则保留不覆盖）。`.env` 已被 `.gitignore` 忽略，绝不提交。
- 敏感凭据（`MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY` 等）**仅通过环境变量注入**：本地放 `.env`，CI/生产经平台 Secret 注入。`manifest/config/config.yaml` 中的敏感字段保持空值，镜像内亦不含凭据。
- `docker-compose.yml` 对 `MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET` 使用 `${VAR:?}` 必填语法，缺失即启动失败，**无内置默认回退**。

## 构建与运维命令（Makefile 统一入口）

本地构建、镜像构建、数据库迁移与启动前预检通过统一 Makefile 目标完成（`make help` 可查看全部目标）：

| 命令 | 说明 |
| --- | --- |
| `make build` | 本地静态编译到 `bin/my-shop`，统一参数 `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`，与 `Dockerfile` 构建行为一致；`GOOS`/`GOARCH` 可覆盖（缺省为宿主机原生，保证 `migrate` 等命令可直接运行；需 Linux 部署产物用 `GOOS=linux make build`） |
| `make docker-build` | 构建发布镜像并打标签（镜像名由 CNB 变量派生，短 SHA + `latest` 双标签同源） |
| `make migrate-up` | 应用所有未执行 migration（`my-shop migrate up`，幂等 + `GET_LOCK` 串行） |
| `make migrate-version` | 查看当前 migration 版本与 dirty 状态（只读） |
| `make migrate-force` | 标记指定版本已应用（不执行 SQL，baseline 接管 / dirty 恢复）：`make migrate-force VERSION=<14位时间戳>` |
| `make config-check` | 启动前统一预检 `qiniu check` + `admin check`（七牛 required 配置与 bucket 可用性、超管创建条件） |

`make migrate-*` / `make config-check` 复用 `make build` 的统一产物 `bin/my-shop`，需依赖容器已就绪（`make init`）且 `.env` 已填 `MYSQL_ROOT_PASSWORD`；本地直连 MySQL 时 `DATABASE_DEFAULT_PASS` 会自动回退到该值（与 app 容器同一 root 凭据）。

## 构建与测试

```bash
make test         # 统一测试入口：go vet + 单元测试 + 集成测试 + Registry 校验 + 容器化静态断言
make build        # 本地静态编译 bin/my-shop（统一参数见上文）
```

`make test` 依次执行 `go vet`、单元测试（无 MySQL/Redis）、集成测试（需 MySQL/Redis）、Registry 一致性校验与容器化静态断言（凭据分离、构建上下文、镜像与编排模型）。集成测试前需先就绪依赖（`make init` 或 `make deps-start` 启动 MySQL/Redis）；测试注入开发测试 MySQL 凭据（`DATABASE_DEFAULT_PASS=root`，仅供本地/CI 测试），不影响本地 `.env`。`make docker-build` 构建发布镜像并打短 SHA 与 `latest` 双标签（同源），镜像发布与回滚语义见 [`docs/design/deployment.md`](docs/design/deployment.md)。

## 指标与可观测性

秒杀指标（`flashsale_*`）默认关闭；以 `FLASH_SALE_METRICS_ENABLED=true` 运行服务后，`GET /metrics` 以 Prometheus 文本格式暴露 QPS / 成功率 / 拒绝率 / p95 / p99（标签为低基数稳定枚举，不含用户/订单/活动 ID）。Prometheus 随 `make up` 一并启动（端口 `9090`，`PROMETHEUS_PORT` 可覆盖），抓取配置见 `prometheus/prometheus.yml`，抓取目标为 Compose DNS 名 `app:8000`（与 app 同处 Compose 网络，不再经宿主机 `host.docker.internal` 打通），并加载 `prometheus/rules/flashsale_latency.yml` recording rules 固化下单接口 p95/p99。

```bash
FLASH_SALE_METRICS_ENABLED=true make up   # 在 .env 设置该变量等价
make health                       # 校验 Nginx / App / MySQL / Redis / Prometheus 全部健康
curl http://127.0.0.1:9090/api/v1/targets   # 查看 my-shop 目标是否 UP
```

> `/metrics` 仅在开关开启时暴露，无独立鉴权入口；生产环境需保证该端点仅内网/监控网段可达。

## 文档导航

- [项目导览](docs/project-overview.md)：系统与两条核心交易链路的总览。
- 核心设计：[普通订单](docs/design/order.md)、[秒杀](docs/design/flash-sale.md)、[商品](docs/design/product.md)、[分类](docs/design/category.md)、[SKU](docs/design/sku.md)、[库存](docs/design/inventory.md)、[IAM](docs/design/iam.md)、[RBAC](docs/design/rbac.md)、[优惠券](docs/design/coupon.md)、[退款](docs/design/refund.md)。
- 部署与运维：[容器化部署模型](docs/design/deployment.md)、[数据库迁移](docs/design/migration.md)、[错误码](docs/design/error-codes.md)。

`docs/design/*` 记录持续维护的项目级长期设计，是了解各模块现状与边界的权威来源。
