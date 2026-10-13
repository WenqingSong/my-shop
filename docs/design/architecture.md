# SurgeCart 系统架构设计

本文面向项目接手者与初次阅读者，提供 SurgeCart（`my-shop`）的统一系统视角：分层结构、模块边界、核心业务流程、基础设施与当前能力边界。本文只描述「现状是什么」，各领域的状态机、业务不变量、一致性语义与公开协议沉淀在对应的领域设计文档（见文末索引），冲突时以领域设计文档与当前实现为准。

## 1. 系统整体架构

SurgeCart 基于 GoFrame v2 提供 HTTP API，MySQL 保存交易与身份数据，Redis 承担登录会话与秒杀准入层。整体呈「接入 → 分层业务 → 持久化」的经典结构：

```text
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
        │  Middleware：RequestID → Response    │
        │  认证授权：Auth / AdminAuth /        │
        │            RequirePermission          │
        │  Controller：HTTP 适配（api 结构体）  │
        └──────────────────────────────────────┘
                           │
                           ▼
        ┌──────────────────────────────────────┐
        │             业务模块                  │
        │  Service：对内业务接口定义           │
        │  Logic：业务规则与流程               │
        │  （IAM / 订单 / 库存 / 优惠券 / 退款  │
        │    秒杀 / CMS / 对象存储 …）         │
        └──────────────────────────────────────┘
                           │
              ┌────────────┴────────────┐
              ▼                         ▼
        ┌──────────┐            ┌──────────────┐
        │  MySQL   │            │    Redis      │
        │ 交易/身份 │            │ 会话/秒杀准入  │
        │ 权威事实   │            │ 派生缓存      │
        └──────────┘            └──────────────┘
```

分层职责（遵循 GoFrame 惯例，见 `AGENTS.md` §2）：

| 层 | 目录 | 职责 |
| --- | --- | --- |
| 接入 | `internal/middleware` | RequestID、统一响应包装、认证授权等横切行为 |
| API | `api/`、`internal/controller` | 请求/响应结构体与公开契约；HTTP 适配，不直接操作数据库 |
| 业务接口 | `internal/service` | 对内暴露业务能力的接口定义 |
| 业务规则 | `internal/logic` | 业务规则、流程、状态机与数据访问编排 |
| 数据访问 | `g.DB()`（DAO 由 Logic 内联使用） | 参数化查询与条件写入；DAO 不承载业务规则 |
| 横切/基础设施 | `internal/auth`、`internal/metrics`、`internal/storage`、`internal/codes` | JWT/会话、Prometheus 指标、文件存储、稳定错误码 |

关键边界：

- Controller 不直接操作数据库；Logic 不处理 Header/Cookie 等 HTTP 细节；业务规则集中在 Logic。
- `context.Context` 沿 Controller → Logic → 数据访问/外部依赖传播，不无理由替换为 `context.Background()`。
- MySQL 是订单、库存、已售数量的**权威事实来源**；Redis 是**派生缓存**（会话、秒杀预扣库存），不承担最终一致性事实。
- 后台扫描器（订单超时取消、秒杀对账、秒杀消费）由 `internal/cmd` 在 `serve` 启动时以 goroutine + ticker 拉起，进程内运行（详见 §4 与部署设计）。

## 2. 模块划分

| 模块 | 职责 | 主要实现位置 |
| --- | --- | --- |
| IAM（身份与权限） | 前台用户与后台管理员两套独立身份域；JWT 签发 + Redis 会话、Refresh Token 轮换与会话撤销；管理员 RBAC（角色/权限/超级管理员） | `internal/auth`、`internal/middleware`、`controller/iam`、`controller/admin`、`logic/iam`、`logic/admin` |
| 订单 | 普通订单下单（服务端定价、快照、幂等、状态机）、支付（模拟）、取消、发货、收货；购物车与收货地址作为下单前置 | `controller/order`、`controller/cart`、`controller/address`、`logic/order`、`logic/cart`、`logic/address` |
| 库存 | SKU 库存查询、增减与流水；条件扣减防超卖；与订单/秒杀共享库存事实 | `controller/inventory`、`controller/sku`、`logic/inventory`、`logic/sku` |
| 优惠券 | 优惠券模板管理与用户领券/查询（核销与订单的耦合边界见领域设计） | `controller/coupon`、`logic/coupon` |
| 退款 | 退款申请、审核、执行、撤销、关闭与审计 | `controller/refund`、`logic/refund` |
| 秒杀 | Redis + Lua 准入、MySQL 出队表异步落单、崩溃重投、幂等库存收敛、死信修复、限流/排队上限/熔断/对账与指标 | `controller/flashsale`、`logic/flashsale`、`internal/metrics` |
| CMS（内容与运营） | 轮播图、文章、推荐位、评价、数据大屏等运营/内容能力 | `controller/banner`、`controller/article`、`controller/recommendation`、`controller/review`、`controller/dashboard`、`logic/*` |
| 对象存储 | 静态资源存储边界：本地文件（轮播图占位图）与七牛云直传凭证签发 | `internal/storage`、`controller/upload`、`logic/upload` |

支撑能力说明（未单列为模块，服务于上述交易链路）：

- **商品目录**（分类/商品/SKU）：`controller/categories`、`controller/product`、`controller/sku`，是订单与秒杀共用的目录基础，前台查询公开、后台写操作经 `AdminAuth + RequirePermission`。
- **用户内容与社交**（评价/收藏/点赞）：`controller/review`、`controller/favorite`、`controller/like`，归入 CMS 运营能力。

路由组织（`internal/cmd/routes_frontend.go`、`routes_admin.go`）：

- **前台**：公开接口（注册/登录/刷新/分类/商品/轮播图/文章等）+ 登录用户接口（`Auth`）+ 登出（`AuthSignatureOnly`，仅验签幂等撤销）。
- **后台**：`/admin` 前缀，公开登录 + `AdminAuth` + `RequirePermission(code)` 双层保护；超级管理员 `IsSuper` 直接放行，其余按角色持有的权限 code 判断，DB 查询失败 fail-closed。

## 3. 核心业务流程

### 3.1 普通订单流程

```text
登录用户              服务端（Logic 层）
   │   POST /orders
   ├──────────────────────────►  1. 读取购物车选中项/直购参数
   │                             2. 服务端重读 SKU 价格，组装商品/价格/地址快照
   │                             3. 幂等键 + 数据库唯一约束防重复建单
   │                             4. 同一 MySQL 事务：
   │                                创建订单 + 订单项
   │                                条件扣减库存（RowsAffected 校验，防超卖）
   │                             5. 提交事务，返回订单
   │◄───────────────────────────
   │   POST /orders/:id/pay      → 状态机：模拟支付（Mock，不接真实支付机构）
   │   POST /orders/:id/cancel   → 取消并恢复库存（只恢复一次）
   │   POST /orders/:id/receive  → 确认收货
   │   POST /orders/:id/refund   → 发起退款（转退款模块）
```

- **超时取消**：后台扫描器（`startOrderCancelScanner`，默认每 60s）扫描 `status=待支付` 且已过期的订单，用「条件状态更新 + RowsAffected」原子闸门逐单取消并恢复库存，多实例并发下同一订单只被处理一次。
- **一致性**：服务端定价 + 快照防篡改；条件扣减 + `RowsAffected` 防超卖；取消/退款只恢复一次库存。
- 详见 `docs/design/order.md`、`docs/design/inventory.md`。

### 3.2 秒杀流程

```text
登录用户              服务端
   │  POST /flash-sales/:id/orders
   ├──────────────────────────►  1. 限流 / 排队软上限 / 熔断（V5 容量保护）
   │                             2. Redis + Lua 准入：校验时间窗、一人一单、
   │                                预扣 remaining（闸门，非最终事实）
   │                             3. 请求写入 MySQL 出队表（queued）
   │                             4. 立即返回受理结果
   │◄───────────────────────────
   │  GET /flash-sales/:id/orders/result → 查询最终落单结果

   （后台消费者 startFlashSaleConsumeScanner，默认每 1s）
   出队 queued 请求（FOR UPDATE SKIP LOCKED + 状态原子更新）
     → 事务落单 + 扣减真实库存 → 成功 / 失败 / 重试 / 死信

   （后台对账 startFlashSaleReconcileScanner，默认每 60s）
   remaining = total_stock - sold - inflight_queued，回补缺失预热
```

- **事实来源**：Redis 预扣是准入闸门，MySQL 始终是订单与已售数量的最终事实来源；出队表承担外部 MQ 的角色（见 §5 边界）。
- **故障恢复**：崩溃重投、幂等库存收敛、死信人工修复（`/admin/flash-sales/requests/:id/repair`）与审计。
- 详见 `docs/design/flash-sale.md`。

### 3.3 认证流程

```text
登录（用户/管理员两套，对称）：
  登录 → 校验账号密码 → 生成 JWT（含 sid）+ Redis 会话 → 返回 access/refresh
请求鉴权：
  请求携带 Bearer token
    → authenticateToken：验签 + exp/issuer（失败 401）
    → 解析 Principal（type 必须匹配，否则 403）
    → ValidateSession：Redis 会话有效性（失败 fail-closed 401）
    → 每请求查 users/admins：status==启用 且 auth_epoch 匹配（禁用即时失效）
    → 注入 Principal → 进入 handler
后台授权：
  AdminAuth 通过后，RequirePermission(code) 判断角色权限；
  超级管理员直接放行，DB 查询失败 fail-closed 500。
登出 / 刷新：
  登出仅验签（幂等撤销）；Refresh Token 轮换撤销旧会话。
```

- **身份域隔离**：前台用户（`type=user`）与后台管理员（`type=admin`）独立 token、独立会话与鉴权中间件，后台写操作再叠加权限校验。
- **fail-closed**：Redis/DB 查询失败一律拒绝放行，不依赖 JWT 过期或仅在登录时检查。
- 详见 `docs/design/iam.md`、`docs/design/rbac.md`。

## 4. 基础设施

### 4.1 Docker 与容器编排

统一经 `docker-compose.yml` 编排五个服务（详见 `docs/design/deployment.md`）：

| 服务 | 角色 | 端口 |
| --- | --- | --- |
| `nginx` | 唯一对外 HTTP 入口（反向代理，信任边界） | `${NGINX_PORT:-8080}` |
| `app` | GoFrame 后端（仅 Compose 内网 `app:8000`，不映射宿主机端口） | 8000（内网） |
| `mysql` | 权威数据存储（`mysql:8.0`） | `127.0.0.1:3306`（回环） |
| `redis` | 会话 / 秒杀准入缓存（`redis:7-alpine`） | `127.0.0.1:6379`（回环） |
| `prometheus` | 指标抓取与存储 | `${PROMETHEUS_PORT:-9090}` |

关键语义：

- 多阶段构建（Go 1.23 静态编译 → alpine + tzdata + ca-certificates），非 root 运行（`uid=10001`）。
- 敏感凭据（JWT secret、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码）仅经环境变量注入，无内置默认回退，缺失 fail-fast；`config.yaml` 敏感字段保持空值。
- 启动生命周期：entrypoint `migrate up`（幂等 + `GET_LOCK` 串行）→ `serve`（依赖重试 → schema 就绪只读校验 → seed → 七牛校验 → 扫描器 → 路由）。
- 持久化：`mysql_data` / `redis_data` / `app_storage` 三个 named volume。
- 优雅停机：`SIGINT/SIGTERM` 取消根 ctx → 扫描器停止领取新任务、在途单事务回滚或完成 → HTTP drain（`server.graceful_timeout`）→ 进程退出。

### 4.2 CI/CD

CNB CI（`.cnb.yml`）：

- **`pull_request` → `pr-check`**：`docker compose up -d mysql redis` → `make test` → `make test-race`。
- **`push` → `main-ci`**（单 job 顺序 stage）：依赖启动 → `make test` → `make test-race` → `go build` → `docker build`（短 SHA 标签 + `latest` 标签，同源同 digest）→ `docker push`（先短 SHA 后 `latest`）→ 原仓库专属的 GitHub 镜像同步。
- 制品库：`docker.cnb.cool/framework-station/my-shop`（由 CNB 内置变量派生），凭据用 CNB 内置 token，流水线不出现硬编码密码。
- 回滚用历史短 SHA 标签，`latest` 不作为回滚锚点。

本地生命周期经 `Makefile`（薄封装转发到 `scripts/`）：`make init/up/down/status/logs/health` 与 `make test/test-race/test-prometheus/test-container/test-nginx/test-storage`。

### 4.3 Prometheus

- 指标仅覆盖秒杀域（`flashsale_*`：`flashsale_requests_total`、`flashsale_request_duration_seconds`），按 `interface/result` 低基数维度区分，用于 QPS / 成功率 / 拒绝率 / p95 / p99。
- 默认关闭；`FLASH_SALE_METRICS_ENABLED=true`（`flash_sale.metrics.enabled`）时才暴露 `GET /metrics`（Prometheus 文本格式，位于统一 JSON 响应包装之外）。
- 抓取目标为 Compose DNS 名 `app:8000`（不经 Nginx、不经宿主机端口）；`prometheus/prometheus.yml` 附带 p95/p99 recording rule（`promtool check rules` 校验）。Nginx 公共入口对 `/metrics` 返回 404。

## 5. 当前边界

- **无 K8s 部署**：容器化已落地（非 root、`/health` liveness、`/ready` readiness、优雅退出），但不产出 Deployment/Pod/HPA/Service/Ingress 等编排清单；`/ready` 仅作为未来 K8s readinessProbe 目标，尚未形成已验证的 Kubernetes 部署结论。
- **无真实支付**：普通订单支付为 Mock，不接入真实支付机构、无资金链路；秒杀订单下单即成交，不走普通订单的支付/取消流程。
- **无外部 MQ**：秒杀异步队列使用 MySQL 出队表（`FOR UPDATE SKIP LOCKED` + 状态原子更新 + 重试/死信/对账），未引入 RabbitMQ/Kafka 等外部消息中间件。
- **单实例部署**：后台扫描器与秒杀消费的多实例并发安全已由「条件状态更新 + RowsAffected」「FOR UPDATE SKIP LOCKED」保证，但当前仅声明单实例部署，多实例负载均衡/高可用不在已验证范围。
- **性能结论未发布**：已提供延迟分位数指标与逐级压测脚本，但仓库未发布包含环境、负载与结果的稳定性能报告，不声明生产容量或 P99 目标已达成。

## 6. 相关设计文档索引

| 领域 | 设计文档 |
| --- | --- |
| 部署 / 容器 / 网络 | `docs/design/deployment.md` |
| IAM / 会话 | `docs/design/iam.md` |
| RBAC | `docs/design/rbac.md` |
| 订单 | `docs/design/order.md` |
| 库存 | `docs/design/inventory.md` |
| 秒杀 | `docs/design/flash-sale.md` |
| 优惠券 | `docs/design/coupon.md` |
| 退款 | `docs/design/refund.md` |
| 对象存储 | `docs/design/storage.md` |
| 错误码 | `docs/design/error-codes.md` |

项目级长期设计以 `docs/design/*` 为权威；历史任务决策与验收证据在 `.agent/tasks/*`，不应仅凭旧任务清单判断当前功能状态。
