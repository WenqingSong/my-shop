# SurgeCart（my-shop）

## 项目介绍

SurgeCart（`my-shop`）是基于 [GoFrame v2](https://goframe.org) 的电商后端项目。项目以**普通订单**与**秒杀**为两条核心交易链路，覆盖前台用户与后台管理员的身份与权限体系，以及商品、购物车、库存、优惠券、退款、运营内容等电商核心域。

项目侧重业务正确性、一致性与故障恢复：MySQL 始终是交易事实的最终来源，关键写路径以事务、条件更新与状态机保证不超卖、不重复补偿；并以 Docker Compose 与本地 kind Kubernetes 提供可复现的运行、验证与测试环境。

## 项目状态

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| 任务一 | Kafka 秒杀改造（B1–B4.1） | **已完成**（已集成 main） |
| 任务二 | Kubernetes 适配（T2-01 – T2-07） | **已完成**（已集成 main；T2-07 的集群级验证由 T3-01 在真实 kind 集群补齐） |
| 任务三 | 本地 Kubernetes 部署与真实业务链路验证（T3-01、T3-02） | **已完成**（已集成 main） |
| 任务四 | Prometheus 基础监控 | **开发中**（未集成，main 不含任务四产物） |
| 任务五 | k6 性能与可靠性测试 | **待开始** |
| 阶段二 | 真实电商业务演进与后续云原生架构建设 | **即将启动** |

当前工程能力是**单机 / 单节点本地环境**：Docker Compose 五服务栈与 kind 单节点集群。仓库不包含生产集群、多副本高可用、Ingress/TLS 或容量压测结论。

## 核心能力

### 秒杀系统

按「先正确、后加速」逐代演进，每一步保留五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存）：

- **Redis + Lua 准入闸门**：活动与库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，无效请求在 Redis 层快速失败。
- **异步落单（两种互斥模式）**：下单拆成「入队快速响应 + 后台消费落单」两阶段。默认 `db_scanner` 以 MySQL 出队表（`FOR UPDATE SKIP LOCKED`）轮询消费；`kafka` 模式改由 MySQL Request 表兼作 Outbox，经 Kafka 发布/消费，详见[秒杀消息链路](#kafka-秒杀消息链路)。
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

- **容器化交付**：Docker 多阶段构建（`CGO_ENABLED=0` 静态编译、基础镜像 patch 版本固定），非 root + 只读根文件系统 + `cap_drop: ALL` + `no-new-privileges` 运行，Compose 一键编排 nginx/app/MySQL/Redis/Prometheus。
- **版本化迁移**：golang-migrate 管理 schema，`migrate up` 幂等 + `GET_LOCK` 串行，schema 未就绪不对外。
- **凭据分离**：敏感值仅经环境变量注入（`${VAR:?}` 必填，无内置默认回退），`config.yaml` 敏感字段保持空值。
- **健康检查与优雅停机**：`/health` liveness、`/ready` readiness（DB/Redis 不可达返回 503），`SIGTERM` 后先 stop accepting 并 drain 在途请求（有界），再按 Publisher → Consumer → Producer → Scanner 关闭后台组件。
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
| 语言 | Go 1.27.3（镜像构建工具链；`go.mod` go 指令 1.26.0；纯 Go 依赖，`CGO_ENABLED=0` 静态编译） |
| 数据库 | MySQL 8.0（业务数据与 migration 状态权威来源） |
| 缓存 | Redis 7（登录会话、秒杀准入闸门与派生缓存） |
| 消息队列 | Kafka 3.9.1（KRaft 单节点，本地；秒杀请求事件的传输管道，可选模式） |
| 对象存储 | 七牛云（上传直传凭证签发） |
| 反向代理 | Nginx（对外唯一 HTTP 入口） |
| 监控 | Prometheus（秒杀指标与 p95/p99 recording rules；Kubernetes 基础监控开发中） |
| 容器化 | Docker + Docker Compose |
| 容器编排 | Kubernetes（Kustomize；本地 kind 单节点集群） |
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

### Kafka 秒杀消息链路

`kafka` 模式下，秒杀请求的异步落单链路如下（详见 [Kafka 开发说明](docs/development/kafka.md)）：

```text
POST /flash-sales/:id/orders
        │
        ▼
Redis + Lua 准入闸门（资格 / 一人一单 / 幂等 / 预扣）
        │ 通过
        ▼
MySQL flash_sale_order_requests（status=queued，受理行兼作 Outbox）
        │
        ▼  Outbox Publisher（扫描未发布 queued 行）
Topic surgecart.flashsale.order.requested.v1   key = <activity_id>:<sku_id>
        │
        ▼  Consumer（消费组，按分区并行）
MySQL 事务内按 request_id 加锁读权威数据 → 条件扣减 → 建单
        │
        ├─ 成功：请求终态 + 订单落库（唯一约束兜底）
        └─ 失败：重试 / 退避 → 超预算进入 DLQ surgecart.flashsale.order.requested.v1.dlq
```

关键约定：

- MySQL 是订单、已售数量与请求生命周期的唯一权威来源；Kafka 只传输「已受理请求需要落单」事件，不承载业务状态。
- 消息 Key 固定为 `<activity_id>:<sku_id>`，同一活动 SKU 进入同一分区；单 SKU 的消费并行度上限为 1，这是当前明确接受的取舍。
- 业务幂等回到 MySQL 请求行状态判断，不把 `event_id` 当作永久去重台账；`event_id` 由 `request_id` 确定性生成。
- 本地 Topic 为 12 分区、副本 1；单节点 KRaft 只用于本地联调，不代表生产高可用。
- `db_scanner` 与 `kafka` 严格互斥，切换需重启进程；默认 `db_scanner`，不需要安装 Kafka。

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

### 本地 Kubernetes（kind）

前置：Docker 与 kubectl 可用。集群为 kind **单节点**（默认名 `surgecart-local`），仅用于本地验证，不是生产集群。

```bash
bash scripts/k8s-cluster.sh create    # 创建集群（已存在则跳过，不重建）
bash scripts/k8s-infra.sh secrets     # 从 .env 读取 MYSQL_ROOT_PASSWORD / DATABASE_DEFAULT_PASS 创建 Secret
bash scripts/k8s-infra.sh create      # 部署 MySQL / Redis / Kafka 并等待 Topic 初始化 Job
bash scripts/k8s-infra.sh verify      # 集群内验证：SELECT 1 / PING / 生产消费回读
```

应用发布使用本地 overlay（镜像先在本地构建并 `kind load`，顺序固定为先迁移、后应用）：

```bash
docker build -t surgecart:<短SHA>-t3-02 .
kind load docker-image surgecart:<短SHA>-t3-02 --name surgecart-local
kubectl apply -k manifest/kubernetes/migration/overlays/local
kubectl apply -k manifest/kubernetes/overlays/local   # 需先把 QINIU_* 占位符替换为本机值
kubectl -n surgecart get deploy,pods
```

本地 overlay 的镜像 tag 同时写在 `kustomization.yaml` 与 `version-patch.yaml`，重新构建镜像后需同步更新；访问通过 `kubectl port-forward` 到 `Service/surgecart`。`bash scripts/k8s-cluster.sh destroy` 删除集群并丢失 PVC 数据。部署细节与限制见 [本地 Kubernetes 基础设施](docs/development/local-infrastructure.md)。

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
| `make test-container-runtime` | 真实 Docker 镜像与容器生命周期门禁（构建/非 root/只读/SIGTERM/drain/退出码） |
| `make test-k8s-manifest` | 校验 Kustomize 部署体系（真实构建 + 构建产物断言 + API Schema；需 kubectl） |
| `make test-k8s-infra` | 校验 MySQL/Redis/Kafka 基础设施清单（restricted PSS / Secret 边界；无需集群） |
| `make test-k8s-cluster` | 集群脚本行为门禁（fake kind/kubectl：创建幂等 / 状态 / 销毁确认；无需集群） |
| `make test-deploy-k8s` | 发布脚本行为门禁（真实执行 `deploy-k8s.sh` + fake kubectl：发布锁/降级保护/成功判定；无需集群） |
| `make deploy-k8s` | Kubernetes 发布（production overlay）：发布锁 → 迁移 Job → 发布成功判定（需集群与 kubectl） |
| `make migrate-up` | 应用所有未执行 migration |
| `make migrate-version` | 查看当前 migration 版本与 dirty 状态 |
| `make migrate-force` | 标记指定版本已应用（`make migrate-force VERSION=<14位时间戳>`） |
| `make config-check` | 启动前预检（七牛与超管条件校验） |
| `make clean` | 清理编译产物与临时文件 |

其余目标（`deps-start` / `docker-push` / `test-unit` / `test-integration` / `test-prometheus` / `test-container` / `test-container-runtime` / `test-nginx` / `test-lib`）见 `make help`。

## 测试

```bash
make test                    # 统一入口：go vet + 单元测试 + 集成测试 + Registry 校验 + 容器化静态断言
make test-race               # 竞态检测
make test-container-runtime  # 真实镜像构建 + 容器生命周期（非 root/只读/SIGTERM/drain/退出码）
make test-kafka-e2e         # 真实 Kafka 秒杀端到端（需专用 MySQL/Redis/Kafka）
make test-serve-lifecycle   # 进程级 serve 生命周期（真实 SIGTERM 与重启收敛）
```

`make test` 依次执行 `go vet`、单元测试（不依赖 MySQL/Redis）、集成测试（需 MySQL/Redis）、数据库迁移门禁（真实 MySQL + 多进程）、Registry 一致性校验、容器化静态断言、Kustomize 部署体系校验（真实构建 + 构建产物断言 + API Schema，需要 kubectl）与发布脚本行为门禁（真实执行 `deploy-k8s.sh` + fake kubectl，无需集群）。集成测试前需先就绪依赖（`make init` 或 `make deps-start`）。独立目标：`test-unit` / `test-integration` / `test-race` / `test-migration` / `test-prometheus` / `test-container` / `test-container-runtime` / `test-k8s-manifest` / `test-deploy-k8s` / `test-nginx` / `test-lib` / `test-storage`（真实存储 E2E，需七牛凭据）。容器构建、运行安全、扫描与排障详见 [容器构建与运行安全](docs/development/container-security.md)，迁移执行与并发锁详见 [数据库迁移运维](docs/development/migrations.md)，Kubernetes 发布流程详见 [Kubernetes 部署体系](docs/development/kubernetes.md)。

## Task 管理（Agent Workflow）

当前框架为 Agent Workflow `4.0.0`（上游发布线 `refs/tags/v4.0.0`，升级于 2026-11-05，见[升级报告](docs/reports/2026-11-05-agent-workflow-upgrade-v4.0.0.md)）。V4 删除了 Workflow Engine、`state.yaml`、五个 Gate 与 Stage 推导：工作流由 Lead Agent 组织，事实写在 Markdown 与 Git 里，机器只做静态校验。历史 Task 平铺在 `.agent/tasks/<task-id>/`（29 个 V3 记录作为冻结证据保留），22 个更早的历史任务隔离在 `.agent/tasks/_archive/`。人类可读导航与兼容说明见 [Task 管理入口](docs/task-management/README.md)：

- [Task 目录](docs/task-management/task-catalog.md)：51 项的状态、领域、交付与证据（快照 2026-10-18）。
- [Task 关系图](docs/task-management/task-relationships.md)：版本演进、前置依赖与业务关联。
- [Owner 决策清单](docs/task-management/owner-decisions.md)：仍需人类决定的事项。

```bash
.agent/adoption/workflow-adoption verify --target .   # 框架文件与哈希校验（V4 唯一的机器校验）
.agent/tools/git-facts --json                         # 只读 Git 事实：分支 / HEAD / 上游 / 领先落后
```

新任务由 [TaskBuilderPrompt](.agent/roles/TaskBuilderPrompt.md) 建档到 `.agent/tasks/<task-slug>/`，使用六份 Markdown 记录（`task.md`、`plan.md`、`handoff.md`、`findings.md`、`delivery.md`、`decisions.md`）；角色是能力边界、由 Lead 按需调用，入口见 [`.agent/README.md`](.agent/README.md) 与 [WorkflowAgentDirective](.agent/specs/WorkflowAgentDirective.md)。

29 个在用历史任务是 V3 证据：其 `state.yaml`、`route` 与 Gate 结论均为冻结事实，V4 不解析、不续用，也不得为通过旧校验补写字段。`.agent/engine/`、`.agent/bin/workflow-check`、`.agent/workflow.yaml` 已随 V4 删除；`.agent/registry/*.md` 保留为普通 Markdown 文档，不再有机器 schema。

## 部署

- **本地/单机**：Docker Compose 编排 nginx/app/MySQL/Redis/Prometheus 五个服务，Nginx 为对外唯一入口，app 仅暴露 Compose 内网端口；`make init` + `make up` 两阶段拉起。
- **镜像发布**：`make docker-build` / `make docker-push` 构建并推送短 SHA + `latest` 双标签镜像（同源同 digest）。
- **CI/CD**：CNB 流水线（`.cnb.yml`）按「测试 → 竞态检测 → 构建 → 镜像发布」顺序执行，任一阶段失败即中断；`latest` 仅指向测试全绿的构建。
- **Kubernetes（Kustomize，单副本）**：两个独立构建入口——应用 `manifest/kubernetes/overlays/production`、迁移 `manifest/kubernetes/migration/overlays/production`。发布顺序固定为「构建镜像 → 获取发布锁（Lease）→ 版本降级保护 → 执行迁移 Job → 应用 Deployment → 发布成功判定 → 释放锁」，由 `make deploy-k8s`（`scripts/deploy-k8s.sh`）强制；清单校验用 `make test-k8s-manifest`，发布脚本行为用 `make test-deploy-k8s`。默认 `ClusterIP`，不含 Ingress/TLS（无真实域名与证书方案）。详见 [Kubernetes 部署体系](docs/development/kubernetes.md) 与 [发布锁设计](docs/design/deploy-lock.md)。
- **本地 Kubernetes（kind）**：单节点 kind 集群 + 集群内 MySQL/Redis/Kafka（均单副本，Kafka 为 KRaft 单节点），应用 1 副本、PVC 5Gi、`restricted` Pod Security。T3-01 在真实集群完成基础设施验证（Node Ready、PVC Bound、MySQL `SELECT 1`、Redis `PING`、Kafka 生产消费回读），T3-02 完成应用部署与 `db_scanner` / `kafka` 两种模式的真实业务链路验证。**这是本地验证环境，不是生产部署**：无多节点、无高可用、无容量结论。见 [本地 Kubernetes 基础设施](docs/development/local-infrastructure.md) 与[验证报告](docs/reports/t3-02-application-deployment-validation/E2E_REPORT.md)。
- **秒杀消费模式**：`flash_sale.dispatch_mode` 二选一——`db_scanner`（默认，仅 MySQL 扫描器）或 `kafka`（Redis 准入 + MySQL Outbox + Kafka Publisher/Consumer，需同时设置 `kafka.enabled=true`）。两种模式严格互斥，切换需重启进程；默认模式不需要安装 Kafka。详见 [Kafka 开发说明](docs/development/kafka.md#10-b4serve-集成与运行闭环)。

详细部署模型、启动生命周期、持久化与健康检查语义见 [部署设计](docs/design/deployment.md)；Kubernetes 资源、配置边界与发布命令见 [Kubernetes 部署体系](docs/development/kubernetes.md)。

## 监控建设进度

| 能力 | 现状 |
| --- | --- |
| 应用指标 | 已实现秒杀相关 Prometheus 指标；`/metrics` 仅在 `flash_sale.metrics.enabled=true` 时暴露 |
| 本地 Compose 抓取 | 已配置：`prometheus/prometheus.yml` 以 15s 间隔抓取 `app:8000/metrics` |
| 秒杀延迟 recording rules | 已配置 p95 / p99（`prometheus/rules/flashsale_latency.yml`），由 `make test-prometheus` 校验 |
| Kubernetes 基础监控（任务四） | **开发中**，未集成；main 不含任务四的监控清单、告警或看板 |
| k6 性能与可靠性测试（任务五） | **待开始**；现有指标与脚本不构成性能结论 |

现有能力仅为**秒杀链路的指标暴露与本地抓取**，不包含 Kubernetes 监控栈、告警规则、看板或压测结论。

## 文档索引

- [项目导览](docs/project-overview.md)：系统与两条核心交易链路的总览。
- [系统架构](docs/design/architecture.md)：分层结构、模块边界、核心业务流程与基础设施。
- 核心设计：[订单](docs/design/order.md)、[秒杀](docs/design/flash-sale.md)、[商品](docs/design/product.md)、[分类](docs/design/category.md)、[SKU](docs/design/sku.md)、[库存](docs/design/inventory.md)、[购物车](docs/design/cart.md)、[IAM](docs/design/iam.md)、[RBAC](docs/design/rbac.md)、[优惠券](docs/design/coupon.md)、[退款](docs/design/refund.md)。
- 部署与运维：[容器化部署](docs/design/deployment.md)、[Kubernetes 部署体系](docs/development/kubernetes.md)、[本地 Kubernetes 基础设施](docs/development/local-infrastructure.md)、[容器构建与运行安全](docs/development/container-security.md)、[数据库迁移（机制）](docs/design/migration.md)、[数据库迁移（运维 / K8s Job）](docs/development/migrations.md)、[错误码](docs/design/error-codes.md)、[对象存储](docs/design/storage.md)。
- 交付报告：[T2-06.1.3 审计](docs/reports/t2-06-1-3-rbac-renewal-budget/FINAL-AUDIT.md)、[T2-06.1.4 部署安全加固](docs/reports/t2-06-1-4-deploy-safety-hardening/IMPLEMENTATION_REPORT.md)、[T2-06.1.5 RBAC 最终集成](docs/reports/t2-06-1-5-rbac-resource-name-final/FINAL_INTEGRATION_REPORT.md)、[T2-07 本地集群依赖](docs/reports/t2-07-local-cluster-dependencies/IMPLEMENTATION_REPORT.md)、[T3-01 集群验证](docs/reports/t3-01-local-kubernetes-validation/VALIDATION_REPORT.md)、[T3-02 应用与业务链路验证](docs/reports/t3-02-application-deployment-validation/E2E_REPORT.md)。
- 开发环境：[Kafka 开发说明](docs/development/kafka.md)（B4 已完成：两种运行模式、serve 装配、Publisher/Consumer 生命周期、F-03/F-04 修复、优雅关闭与真实端到端门禁）。
- Task 管理：[Task 管理入口](docs/task-management/README.md)、[Task 目录](docs/task-management/task-catalog.md)、[Task 关系图](docs/task-management/task-relationships.md)、[Owner 决策清单](docs/task-management/owner-decisions.md)。
- 运营与内容：[轮播图](docs/design/banner.md)、[文章](docs/design/article.md)、[推荐位](docs/design/recommendation.md)、[评价](docs/design/review.md)、[数据大屏](docs/design/dashboard.md)、[收藏](docs/design/favorite.md)、[点赞](docs/design/like.md)。

`docs/design/*` 记录持续维护的项目级长期设计，是了解各模块现状与边界的权威来源。
