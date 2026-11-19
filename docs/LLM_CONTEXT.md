# SurgeCart 项目上下文手册（面向 LLM / AI 代码代理）

> **文档性质**：本文件是 SurgeCart（`my-shop`）的独立事实手册，专门写给大型语言模型与自动化编码代理阅读，目标是让模型在不逐文件通读整个仓库的前提下，准确理解系统边界、关键不变量、失败语义与改动风险。
>
> **事实基线**：`main` @ `c60e432`（2026-11-19）。代码统计：`api/` + `internal/` 约 210 个 Go 文件，全仓 125 个 `*_test.go`，25 个 SQL migration，52 个 Kubernetes YAML，41 个 Shell 脚本。数字会随演进变化，只用于建立规模感。
>
> **与 README 的关系**：README 面向人类读者，强调定位、状态与快速开始；本文档面向模型，强调「事实在哪里、边界是什么、什么不能声称、改动会破坏什么」。两者冲突时以代码为准，并按本文 §1.3 的规则处理文档漂移。

---

## 0. 给 LLM 的使用说明（先读这一节）

### 0.1 推荐阅读顺序

| 目标 | 阅读顺序 |
| --- | --- |
| 快速建立全局认识 | §1 摘要 → §2 仓库地图 → §3 进程生命周期 |
| 改秒杀 / 异步 / Kafka | §7 秒杀链路 → §5 数据模型 → §11 部署 → §14.5 改动检查清单 |
| 改订单 / 库存 / 退款 / 优惠券 | §6 订单链路 → §5.3 状态码 → §14.1/14.2 |
| 改认证 / 权限 | §8 IAM → §4 路由 → §14.1 |
| 排查监控 / 性能 | §10 可观测性 → §12 测试 → §13 已知限制 |
| 回答「项目做了什么」 | §1 → §6 → §7 → §12.4 已验证矩阵 |

### 0.2 六条硬规则

1. **代码是事实来源**：`internal/`、`api/`、`manifest/`、`scripts/` 的实际行为优先于任何 Markdown。设计文档可能滞后，交付报告只代表报告生成时的那一刻。
2. **不要把「任务完成」等同于「生产可用」**：仓库明确只支持单机 / 单节点本地环境。任何「高可用 / 生产容量 / 生产已验证」的表述都缺少证据。
3. **区分 IMPLEMENTED 与 VERIFIED**：代码存在不等于在真实集群验证过。`NOT_VERIFIED` 必须如实保留，不能改写成「通过」。
4. **修改交易路径前先复述不变量**：秒杀五条不变量与订单一致性规则见 §7.1、§6.4。任何改动都必须说明它如何保持这些不变量。
5. **不要在文档或日志里写入敏感值**：数据库/Redis 密码、JWT secret、超管密码、七牛 AK/SK 只能以环境变量名出现。
6. **不要凭旧报告判断当前状态**：`docs/reports/*` 是带时间戳的证据，不是现状说明；当前状态以 `main` 的代码和测试为准。

### 0.3 建议的验证命令

```bash
go build ./...                                  # 编译所有包
go vet ./...                                    # 静态检查
make test-unit                                  # 不依赖 MySQL/Redis 的单元测试
make test-integration                           # 需先 make deps-start 或 make init/up
make test                                       # 完整门禁（需依赖；包含 K8s 清单与发布脚本断言）
bash scripts/k8s-infra.sh verify                # 需真实 kind 集群，验证 MySQL/Redis/Kafka
```

不要在没有 MySQL/Redis 的环境里把「测试被跳过」当成通过；本仓库的多个门禁刻意选择失败而非跳过。

---

## 1. 30 秒摘要

**SurgeCart** 是基于 GoFrame v2 的电商后端，围绕两条交易链路构建：

1. **普通订单**：服务端定价 + 快照 + 幂等键 + 单事务「订单 + 订单项 + 条件扣库存（+ 优惠券预占）」；状态机 `10 → 20 → 30 → 40/50`，以及取消 `10 → 60`、退款 `20 → 70` 两条出口。
2. **秒杀**：Redis + Lua 做准入闸门（资格、一人一单、幂等、预扣），MySQL 做最终事实来源；下单拆成「入队快速响应 + 后台异步落单」，有两种严格互斥的消费模式：`db_scanner`（MySQL 出队表扫描）与 `kafka`（MySQL Outbox + Kafka Producer/Consumer）。

**一致性总原则**：MySQL 是订单、库存、已售数量、请求生命周期的唯一权威事实来源；Redis 是派生缓存与快速失败层；Kafka 只传输「已受理请求需要落单」的事件，不承载业务状态。

**当前能力边界**：单机 Compose 五服务栈 + kind 单节点集群 + 单实例 Prometheus/Grafana。无生产集群、无多副本高可用、无 Ingress/TLS、无告警、无真实支付、无容量承诺。

---

## 2. 仓库地图

### 2.1 顶层目录

| 路径 | 职责 | 备注 |
| --- | --- | --- |
| `main.go` | 进程入口 | 仅导入 MySQL/Redis 驱动、注册 logic、调用 `cmd.Main` |
| `api/` | 对外 HTTP 契约 | 21 个文件，按领域分目录；请求结构体用 `g.Meta` 声明 `path`/`method` |
| `internal/cmd/` | 组合根 | 命令树、serve 装配、前后台路由注册、HTTP 生命周期 |
| `internal/boot/` | 启动装配 | 配置校验、依赖连通性、schema 就绪、超管/权限 seed |
| `internal/config/` | 统一配置访问层 | 契约注册表 `contract.go`；优先级「环境变量 > YAML > 代码默认值」 |
| `internal/controller/` | HTTP 适配 | 31 个文件；只做参数/身份/响应适配，不写业务规则 |
| `internal/service/` | 业务接口 | 21 个接口 + `RegisterXxx`，Logic 在 `init()` 注册 |
| `internal/logic/` | 业务实现 | 60 个文件；订单、秒杀、IAM、库存、优惠券、退款、CMS 等 |
| `internal/middleware/` | 横切行为 | RequestID、Metrics、Response、Auth/AdminAuth、RequirePermission |
| `internal/auth/` | JWT 与会话 | HS256、Redis session、refresh token 轮换 |
| `internal/codes/` | 业务错误码 | 码 → HTTP 状态 + 安全 message 的唯一码表 |
| `internal/migrations/` | 数据库迁移 | `//go:embed sql/*.sql`，golang-migrate + MySQL `GET_LOCK` |
| `internal/mq/kafka/` | Kafka 适配 | config/producer/consumer/lag；不含业务语义 |
| `internal/metrics/` | Prometheus 指标 | `flashsale_*` + `surgecart_*`，默认 Registry |
| `internal/worker/` | 后台循环 | 秒杀 Runtime 装配、订单超时取消、秒杀消费/对账扫描器 |
| `internal/storage/` | 本地静态存储 | 轮播图占位图与 `/storage/banners` 静态路由 |
| `internal/termination/` | 终止预算契约 | 校验 60s = preStop 5s + drain 10s + runtime 30s + 余量 15s |
| `internal/logx/` | 结构化日志 | 统一字段与 event 名称 |
| `manifest/config/` | 应用配置 | `config.yaml`，敏感字段保持空值 |
| `manifest/kubernetes/` | Kustomize 清单 | 应用、迁移 Job、基础设施、监控、发布锁示例 |
| `prometheus/` | Compose 监控 | 抓取配置与 p95/p99 recording rules |
| `nginx/` | 唯一对外入口 | 反向代理、请求 ID、`/metrics` 公共入口 404 |
| `scripts/` | 生命周期与门禁 | 41 个 Shell；Makefile 只是薄封装 |
| `test/` | 测试资产 | `e2e/`、`manifest/`、`container/`、`k6/`、`serveproc/` 等 |
| `docs/` | 设计与证据 | `design/` 长期设计、`development/` 运维说明、`reports/` 时间戳报告 |
| `.agent/` | Agent Workflow V4 | 历史任务记录与角色规范；不是运行时依赖 |

### 2.2 分层与依赖方向

```text
HTTP → middleware(RequestID → Metrics → Response) → controller → service(接口) → logic → g.DB()/g.Redis()/Kafka/Storage
```

| 层 | 允许做什么 | 禁止做什么 |
| --- | --- | --- |
| Controller | 读取 `Principal`、调用 Service、返回 `api` 结构体 | 直接写 SQL、承载状态机 |
| Service | 声明业务能力接口、注册实现 | 包含 HTTP 细节 |
| Logic | 业务规则、事务、状态机、外部依赖编排 | 直接读写 Header/Cookie |
| Middleware | 认证、授权、响应包装、指标、RequestID | 承载业务规则 |

`context.Context` 沿调用链传播。后台组件的运行期 ctx 由 `context.WithoutCancel` 剥离信号取消，关闭顺序由 `FlashSaleRuntime.Shutdown` 统一编排（见 §3.4）。

### 2.3 领域 → 关键文件

| 领域 | Controller | Logic | 关键设计文档 |
| --- | --- | --- | --- |
| IAM / RBAC | `controller/iam`、`controller/admin` | `logic/iam`、`logic/admin` | `docs/design/iam.md`、`rbac.md` |
| 商品 / 分类 / SKU | `controller/product`、`categories`、`sku` | `logic/product`、`categories`、`sku` | `product.md`、`category.md`、`sku.md` |
| 库存 | `controller/inventory` | `logic/inventory` | `inventory.md` |
| 购物车 / 地址 | `controller/cart`、`address` | `logic/cart`、`address` | `cart.md`、`address.md` |
| 订单 | `controller/order` | `logic/order/order.go` | `order.md` |
| 优惠券 | `controller/coupon` | `logic/coupon` | `coupon.md` |
| 退款 | `controller/refund` | `logic/refund` | `refund.md` |
| 秒杀 | `controller/flashsale` | `logic/flashsale/*` | `flash-sale.md` |
| 运营内容 | `controller/banner`、`article`、`recommendation`、`review`、`favorite`、`like`、`dashboard` | `logic/*` | 对应 `docs/design/*.md` |
| 对象存储 | `controller/upload` | `logic/upload` | `storage.md` |

---

## 3. 进程与生命周期

### 3.1 命令入口

`internal/cmd/cmd.go`：

| 命令 | 行为 |
| --- | --- |
| `my-shop`（无参数）/ `serve` | 启动 HTTP 服务：Bootstrap → 七牛校验 → 秒杀 Runtime → 终止预算 → 路由 → 有界 drain |
| `migrate up` | 执行所有未应用 migration；不启动 HTTP/Redis/Kafka/Runtime |
| `migrate force <version>` | 只改 `schema_migrations`，不执行 SQL；仅人工恢复 dirty / baseline 接管 |
| `migrate version` | 只读查看当前版本与 dirty |
| `qiniu check` | 校验七牛配置与 bucket 可用性；失败非零退出 |
| `admin check` | 校验超管创建条件；失败非零退出 |
| `legacy-migrate <dry-run|import>` | 历史数据 staging → 目标库 |
| `migrate up` 的隐藏约束 | 只依赖 MySQL；`Preflight` 在建立连接前完成配置校验 |

### 3.2 `serve` 启动顺序（不可随意调换）

1. `boot.Bootstrap`：
   - `config.Validate`：逐项解析全部已登记配置，非法 bool/int/duration 立即失败；
   - `config.ValidateExternalEndpoints`：严格模式下要求 DB host/name、Redis address 由环境变量显式提供且非回环；
   - 装配 server / database / redis；数据库 DSN 注入 `time_zone`（由 `dashboard.timezone` 推导，拒绝含 DST 的时区）；
   - `auth.Secret`：JWT 密钥缺失或 < 32 字节即失败；
   - `waitForDependencies`：MySQL `PingMaster` + Redis `PING`，1s 间隔重试，默认 30s 超时；
   - `checkSchemaReady`：只读校验 `schema_migrations`，dirty 或落后即失败（不执行 DDL）；
   - `seedSuperAdmin`（仅超管不存在时创建）→ `seedPermissions`（按 code 幂等）。
2. `service.Upload().ValidateConfig`：七牛是启动 required dependency，结构/存在性/真实 bucket 可用性任一失败即 fail-fast。
3. `runtimeContext = context.WithoutCancel(ctx)`：后台链路不继承信号取消，避免 SIGTERM 绕过有序关闭。
4. `worker.StartFlashSaleRuntime`：按 `flash_sale.dispatch_mode` 启动唯一一套消费入口；配置矛盾或 Kafka 配置非法即启动失败。
5. `termination.Load`：校验终止预算不小于 `preStop + HTTP drain + runtime shutdown + 安全余量`；不足即启动失败。
6. 初始化本地 banner 存储（seed 占位图 + `/storage/banners` 静态路由）。
7. 指标开关开启时挂载 `GET /metrics`（在统一 JSON 响应包装之外）。
8. 注册中间件与路由：`RequestID`（外）→ `Metrics`（中）→ `Response`（内）；绑定 `/health`、`/ready`、前台/后台路由。
9. `serveHTTP`：受控 listener 启动；收到信号后停收新连接并在 `server.graceful_timeout` 内有界 drain。
10. `defer runtime.Shutdown()`：按 Publisher → Consumer → Producer → Scanner 顺序停止，统一受 `flash_sale.shutdown_timeout` 约束。

### 3.3 请求生命周期

```text
Nginx（生成/透传 X-Request-ID）
  → RequestID：读取/生成 ID、回写响应头、请求结束输出 JSON 访问日志
  → Metrics：in-flight +1；Next 后读取最终状态码记录 counter/histogram
  → Response：成功 {code:0,message:"OK",data}；失败按 codes 映射 HTTP 状态 + {code,message,data:null}
  → Auth / AdminAuth / RequirePermission（按路由分组）
  → Controller → Service → Logic
```

关键点：

- `Metrics` 必须在 `Response` 外层，否则读不到最终状态码；且不统计 `/metrics` 自身。
- `Response` 遇到已有缓冲/已写内容会跳过包装（静态文件、`/metrics` 文本）。
- `/health`、`/ready` 也经统一包装；`/ready` 失败返回 code `1005` 与 HTTP 503。
- `RequestID` 优先使用 Nginx 传入的 `X-Request-ID`，直连时自生成。

### 3.4 优雅停机与终止预算

- HTTP drain 默认 10s（`server.graceful_timeout`）：先停收新连接，等待在途请求；超时后强制关闭并确认停止。
- 后台关闭顺序：先停只读采样循环（Outbox 积压、Kafka Lag），再 **Publisher → Consumer → Producer → Scanner**。
  - Publisher 先停，避免继续领取 Outbox；
  - Consumer 停止并退出消费组；
  - Producer 最后关闭，因为 Consumer 的死信投递依赖它；
  - Scanner 覆盖订单超时取消、秒杀消费/对账。
- 终止预算：`60s >= preStop 5s + HTTP drain 10s + runtime shutdown 30s + safety 15s`。同一组数值必须与 `docker-compose.yml` 的 `stop_grace_period`、K8s `terminationGracePeriodSeconds`、`lifecycle.*` 配置一致，由 `internal/termination` 测试断言。

---

## 4. HTTP 契约与路由

### 4.1 统一响应

```json
{ "code": 0, "message": "OK", "data": { } }
```

- 客户端靠 `code` 判型，不依赖 `message` 文本。
- 错误码域固定：通用 1000-1999、IAM 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999、地址 7000-7999、购物车 8000-8999、订单 9000-9999、评价 10000-10999、收藏 11000-11999、秒杀 12000-12999、点赞 13000-13999、轮播图 14000-14999、推荐位 15000-15999、文章 16000-16999、上传 17000-17999、退款 18000-18999、优惠券 19000-19999。
- 码表唯一来源：`internal/codes/codes.go`。新增错误码必须同时加入 `codeTable`，并由 `scripts/check-registry.sh` 检查域漂移。
- `/metrics` 是 Prometheus 文本格式，**不**经统一包装。

### 4.2 前台路由（`internal/cmd/routes_frontend.go`）

公开（无需 token）：

| 方法 | 路径 |
| --- | --- |
| POST | `/register`、`/login`、`/refresh` |
| GET | `/categories`、`/categories/:id` |
| GET | `/products`、`/products/:id`、`/products/:id/reviews` |
| GET | `/likes/count` |
| GET | `/banners` |
| GET | `/recommendations/:code` |
| GET | `/articles`、`/articles/:id`、`/articles/:id/like/count` |

登录用户（`middleware.Auth`）：

| 领域 | 路径 |
| --- | --- |
| 身份 | `GET /me` |
| 地址 | `POST/GET /addresses`、`GET/PUT/DELETE /addresses/:id` |
| 购物车 | `GET /cart`、`POST /cart/items`、`PUT /cart/items/:id`、`PUT /cart/items/:id/selected`、`DELETE /cart/items/:id` |
| 订单 | `POST /orders`、`GET /orders`、`GET /orders/:id`、`POST /orders/:id/pay`、`POST /orders/:id/cancel`、`POST /orders/:id/receive`、`POST /orders/:id/refund` |
| 退款 | `GET /refunds`、`GET /refunds/:id`、`POST /refunds/:id/revoke` |
| 秒杀 | `POST /flash-sales/:id/orders`、`GET /flash-sales/:id/orders/result` |
| 评价 | `POST /reviews`、`GET /my/reviews`、`PUT /reviews/:id`、`DELETE /reviews/:id` |
| 收藏 | `POST /favorites`、`DELETE /favorites/:product_id`、`GET /favorites`、`GET /favorites/check` |
| 点赞 | `POST /likes`、`DELETE /likes/:product_id`、`GET /likes/check` |
| 文章 | `POST /articles`、`PUT/DELETE /articles/:id`、`GET /my/articles`、文章点赞/收藏及其 check、`GET /my/articles/favorites` |
| 优惠券 | `POST /coupons/:template_id/claim`、`GET /my/coupons` |
| 会话 | `GET /sessions`、`DELETE /sessions/:sid`、`POST /sessions/revoke-others`、`POST /sessions/revoke-all` |
| 上传 | `GET /qiniu/upload/token` |

`POST /logout` 仅验签（`AuthSignatureOnly`），保证重复登出能幂等撤销。

### 4.3 后台路由（`internal/cmd/routes_admin.go`）

| 分组 | 接口 | 保护 |
| --- | --- | --- |
| 公开 | `POST /admin/login` | 无 |
| 仅验签 | `POST /admin/logout` | `AdminAuthSignatureOnly` |
| 认证 | `GET /admin/me` | `AdminAuth` |
| 用户 | `GET /admin/users/:id/status` | `user:read` |
| 用户 | `PUT /admin/users/:id/status` | `user:status` |
| 管理员 | `POST /admin/admins`、`PUT /admin/admins/:id/status`、`DELETE /admin/admins/:id`、角色绑定/解绑 | `admin:create` / `admin:disable` / `admin:delete` / `admin:assign_role` |
| 角色 | `POST/GET /admin/roles`、`PUT/DELETE /admin/roles/:id`、权限绑定/解绑 | `role:create` / `role:list` / `role:update` / `role:delete` / `role:assign_permission` |
| 权限 | `POST/GET /admin/permissions`、`PUT/DELETE /admin/permissions/:id` | `permission:create` / `permission:list` / `permission:update` / `permission:delete` |
| 分类写 | `POST /categories`、`PUT/DELETE /categories/:id` | `category:create` / `category:update` / `category:delete` |
| 商品读 | `GET /admin/products`、`GET /admin/products/:id` | 仅 `AdminAuth` |
| 商品写 | `POST /admin/products`、`PUT /admin/products/:id`、上/下架 | `product:create` / `product:update` / `product:on_shelf` / `product:off_shelf` |
| SKU | `POST /admin/skus`、`PUT/DELETE /admin/skus/:id` | `sku:create` / `sku:update` / `sku:delete` |
| 库存读 | `GET /admin/inventories/:sku_id`、`GET /admin/inventories/:sku_id/logs` | 仅 `AdminAuth` |
| 库存写 | `POST /admin/inventories/:sku_id/increase`、`.../deduct` | `inventory:increase` / `inventory:deduct` |
| 订单 | `POST /admin/orders/:id/ship`、`GET /admin/orders`、`GET /admin/orders/stats`、`GET /admin/orders/:id` | `order:ship` / `order:list` / `order:view` |
| 退款 | `GET /admin/refunds`、`GET /admin/refunds/:id`、审计、审核、执行、关闭 | `refund:list` / `refund:view` / `refund:review` / `refund:execute` |
| 秒杀 | `POST /admin/flash-sales`、`PUT /admin/flash-sales/:id`、请求修复与审计 | `flash_sale:create` / `flash_sale:update` / `flash_sale:repair` |
| 评价 | `POST /admin/reviews/:id/take-down` | `review:take_down` |
| 轮播图 | 查询仅 `AdminAuth`；写 `banner:create/update/delete` |  |
| 推荐位 | 查询仅 `AdminAuth`；写 `recommend:create/update/delete/item` |  |
| 大屏 | `/admin/dashboard/overview|orders/trend|orders/status|products/top|flash-sales` | `dashboard:view` |
| 优惠券 | `POST/GET /admin/coupons`、`POST /admin/coupons/:id/stop` | `coupon:create` / `coupon:list` / `coupon:stop` |

注意：`routes_admin.go` 中的路径必须与 `api/*/v1` 结构体的 `g.Meta path` 完全一致，否则 GoFrame 绑定会覆盖显式路径，造成路由漂移。

### 4.4 健康检查语义（冻结，不要扩散）

| 端点 | 语义 | 依赖 |
| --- | --- | --- |
| `GET /health` | liveness：进程能响应即 200 | 不访问 MySQL/Redis/Kafka |
| `GET /ready` | readiness：HTTP 写入路径依赖的强一致存储可达 | MySQL `PingMaster` + Redis `PING`；任一失败 503，fail-closed；结果进程内缓存 3s |
| `/metrics` | 指标采集 | 开关打开才挂载；Nginx 公共入口返回 404 |

**禁止**把 Kafka 连接、Consumer Group 状态、Outbox 积压加入 `/health` 或 `/ready`。Kafka 故障的正确表现是「异步处理延迟增加」，而不是把 HTTP 入口摘流。

---

## 5. 数据模型

### 5.1 表分组

| 分组 | 表 | 说明 |
| --- | --- | --- |
| 身份与权限 | `users`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`、`refresh_tokens`、`user_status_audits` | 前台用户与后台管理员是两套身份域 |
| 商品目录 | `categories`、`products`、`skus`、`inventories`、`inventory_logs` | 分类树、SPU、SKU、库存与流水 |
| 交易 | `addresses`、`cart_items`、`orders`、`order_items`、`refund_requests`、`refund_audits`、`coupon_templates`、`user_coupons` | 订单金额单位为整数分 |
| 秒杀 | `flash_sale_activities`、`flash_sale_activity_skus`、`flash_sale_orders`、`flash_sale_order_requests`、`flash_sale_request_audits` | 请求表同时承担出队队列与 Outbox |
| 运营内容 | `reviews`、`favorites`、`product_likes`、`banners`、`articles`、`article_likes`、`article_favorites`、`recommend_positions`、`recommend_items` | 评价为软删除（status=3） |
| 运维 | `legacy_id_map` | 历史数据迁移映射 |

### 5.2 关键唯一约束（正确性的最后防线）

| 表 | 唯一键 | 语义 |
| --- | --- | --- |
| `users` | `uk_username` | 用户名唯一 |
| `admins` | `uk_admin_username` | 管理员用户名唯一 |
| `permissions` | `uk_permission_code` | 权限 code 唯一 |
| `cart_items` | `uk_user_sku(user_id, sku_id)` | 同一 SKU 在购物车只有一行 |
| `orders` | `uk_order_no`、`uk_user_idempotency(user_id, idempotency_key)` | 订单号 + 幂等去重 |
| `order_items` | 无业务唯一键；`fk_order_items_order` 级联删除 | 订单项随订单删除 |
| `reviews` | `uk_order_item(order_item_id)` | 同一订单项只能评价一次 |
| `favorites` / `product_likes` | `uk_user_product(user_id, product_id)` | 一人一收藏/一赞 |
| `article_likes` / `article_favorites` | `uk_user_article(user_id, article_id)` | 一人一赞/一收藏 |
| `user_coupons` | `uk_user_template(user_id, template_id)` | 同一用户同一模板只能领一次 |
| `refund_requests` | `uk_active(active_key)` | 同一订单只允许一个有效退款申请 |
| `flash_sale_activity_skus` | `uk_activity_sku(activity_id, sku_id)` | 活动×SKU 绑定唯一 |
| `flash_sale_orders` | `uk_flash_order_no`、`uk_flash_idempotency(user_id, idempotency_key)`、`uk_flash_one_per_user(activity_id, sku_id, user_id)` | 订单号/幂等/一人一单 |
| `flash_sale_order_requests` | `uk_request_idempotency(user_id, idempotency_key)` | 异步请求幂等 |
| `recommend_positions` | `uk_code` | 推荐位 code 唯一 |
| `recommend_items` | `uk_position_product(position_id, product_id)` | 推荐位内商品唯一 |
| `legacy_id_map` | `uk_source_entity_old(source, entity_type, old_id)` | 历史映射幂等 |

外键极少：除 `order_items.order_id` 外，业务表普遍使用软引用，靠「快照自足 + 事务内条件更新」保证一致性，而不是级联约束。

### 5.3 状态码字典

| 域 | 字段 | 取值 |
| --- | --- | --- |
| 订单 | `orders.status` | 10 待支付 / 20 已支付 / 30 已发货 / 40 已收货 / 50 已完成 / 60 已取消 / 70 已退款 |
| 订单 | `orders.cancel_reason` | 1 用户取消 / 2 超时取消 |
| 退款 | `refund_requests.status` | 10 申请中 / 20 待退款 / 30 退款失败 / 40 审核拒绝 / 50 已撤销 / 60 退款成功 / 70 已关闭 |
| 秒杀活动 | `flash_sale_activities.status` | 0 disabled / 1 enabled；「已结束」由 `end_time` 派生 |
| 秒杀请求 | `flash_sale_order_requests.status` | 0 queued / 1 success / 2 failed（业务终态）/ 3 dead（技术失败超限/待修复） |
| 优惠券 | `user_coupons.status` | 10 available / 20 locked（下单预占）/ 30 used |
| 优惠券模板 | `coupon_templates.status` | 1 active / 0 stopped |
| 商品 | API `status` | `draft` / `on_shelf` / `off_shelf`；DB 0=draft、1=on_shelf、2=off_shelf |
| SKU | API `status` | `enabled` / `disabled` |
| 评价 | `reviews.status` | 1 published / 2 taken_down / 3 deleted（软删除） |
| 用户/管理员 | `users.status`、`admins.status` | 1 启用 / 0 禁用 |

---

## 6. 普通订单链路

### 6.1 下单事务

入口：`POST /orders` → `logic/order.Create`。

1. 读取购物车勾选项或直购参数；
2. 校验地址归属、SKU 启用、商品在售；
3. **服务端重读 SKU 价格**，组装商品/价格/地址快照；
4. 计算请求指纹 `sha256(source|address_id|coupon_id|sku_id:qty...（按 sku_id 升序）)`；
5. 同一 MySQL 事务：
   - 插入 `orders` + `order_items`；
   - 条件扣减 `inventories`（`qty >= n` + `RowsAffected`）；
   - 若使用优惠券，调用 `Coupon.LockInTx` 原子预占（`status=available → locked`）；
   - 清空已下单的购物车条目；
6. 提交后返回订单。

### 6.2 状态机

```text
10 待支付 ──pay──► 20 已支付 ──ship──► 30 已发货 ──receive──► 40 已收货 ──► 50 已完成
     │                  │
     ├─cancel──► 60 已取消（恢复库存，仅一次）
     └─timeout─► 60 已取消（后台扫描器）
                        │
                   refund──► 70 已退款（恢复库存，仅一次）
```

- `Pay`：Mock 支付；条件更新 `WHERE status=10`，幂等；已过期订单先懒取消。
- `Cancel` / `CancelExpired`：条件更新 `WHERE status=10`，命中者才恢复库存；多实例并发只有一个成功。
- `Ship`：管理员，`WHERE status=20`；若存在有效退款申请（10/20/30）则拒绝发货（退款与发货互斥）。
- `Receive`：同一事务内 `30 → 40 → 50`，原子推进，避免中间态停留。
- `CompleteRefundInTx`：退款执行成功时在**同一事务**内 `20 → 70` 并恢复库存，供退款模块调用。

### 6.3 超时取消

- 后台扫描器 `StartOrderCancelScanner`，默认每 60s，单轮最多 100 单。
- 条件：`status=10 AND expire_at <= NOW()`。
- 每单使用「条件状态更新 + `RowsAffected`」原子闸门，命中者才恢复库存；重复扫描不会重复补偿。

### 6.4 不变量

- **服务端定价**：不信任客户端价格；成交价来自下单时的 SKU 读取与快照。
- **快照自足**：订单项保存 SKU/商品名、主图、成交价；商品或 SKU 后续修改/删除不影响历史订单。
- **幂等**：`（user_id, idempotency_key）` 唯一；同键同指纹返回既有订单，不同指纹返回 `9006`。
- **不超卖**：条件扣减 + `RowsAffected`；`inventories` 数值非负。
- **补偿一次**：取消/退款都用条件状态更新作为闸门，只有命中者恢复库存。
- **事务边界**：订单、订单项、库存扣减、优惠券预占必须同事务；任一步失败整体回滚。

### 6.5 优惠券联动

- 领取：模板 `status=1` + 时间窗 + `issued_quantity < total_quantity` 条件更新，原子防超发；`user_coupons` 唯一键防重复领取。
- 下单：事务内 `LockInTx` 将券从 `available` 原子改为 `locked` 并绑定 `order_id`；失败则整体回滚。
- 支付：同一事务内将券从 `locked` 核销为 `used`，重复支付不重复核销。
- 取消：将券从 `locked` 释放回 `available`（清 `order_id`/`locked_at`），只释放一次。当前退款路径不负责释放优惠券，退款后的券状态沿用支付时的 `used`。
- 券的可用性（门槛、有效期）在锁定时用 SQL 判定，避免 Go↔MySQL 时区漂移。

---

## 7. 秒杀链路（项目最复杂的部分）

### 7.1 五条业务不变量（任何改动都必须保持）

| 编号 | 不变量 | 最终兜底 |
| --- | --- | --- |
| INV-001 | 库存不为负 | `UPDATE ... SET sold=sold+1 WHERE sold<total_stock` + `INT UNSIGNED` |
| INV-002 | 成功订单数 ≤ 初始库存 | 订单插入与库存扣减同事务；`COUNT(orders)=sold` |
| INV-003 | 一人一单 | `uk_flash_one_per_user(activity_id, sku_id, user_id)` |
| INV-004 | 失败不建单、不扣库存、不留半成品 | 单事务 + 保存点回滚 |
| INV-005 | 同一幂等键不重复扣库存 | `uk_flash_idempotency(user_id, idempotency_key)` |

扩展约束：服务端定价与快照（INV-006）、事务原子性（INV-007）、管理员权限（INV-008）、Redis 预扣不超预热库存、异步请求生命周期唯一、补偿幂等、人工修复与审计原子。

### 7.2 Redis key 与 Lua 闸门

| Key | 类型 | 内容/TTL |
| --- | --- | --- |
| `flashsale:activity:{activityID}` | Hash | `status` / `start` / `end`；TTL = `end - now + 60s`，最小 1s |
| `flashsale:stock:{activityID}:{skuID}` | String | 剩余可预扣量；同一 TTL |
| `flashsale:bought:{activityID}:{skuID}:{userID}` | String | 一人一单快速标记 |
| `flashsale:idem:{userID}:{idempotencyKey}` | String | 请求幂等快速标记，值为 `request_hash` |
| `flashsale:soldout:{activityID}:{skuID}` | String | 售罄快速失败标记 |
| `flashsale:null:{activityID}` | String | 空值/负缓存，TTL 60s |
| `flashsale:queued:{activityID}:{skuID}` | String | 排队软上限近似计数（可选） |

Lua 脚本 `flashSaleGateScript` 原子完成：

```text
空值标记 → 活动存在/启用/时间窗 → 售罄标记 → 幂等 → 一人一单
→ 剩余库存检查 → DECR 预扣 → 写入 bought/idem 标记 → GATE_PASSED
```

返回码：`NOT_FOUND`、`NOT_IN_WINDOW`、`SOLD_OUT`、`ALREADY_PURCHASED`、`IDEMPOTENT_HIT`、`IDEMPOTENT_CONFLICT`、`SKU_NOT_BOUND`、`GATE_PASSED`。

关键语义：

- 幂等检查**先于**一人一单：同键同指纹重试返回既有请求状态，而不是 12004。
- `GATE_PASSED` 只代表「预扣成功」，**不代表下单成功**；MySQL 失败必须补偿。
- Redis 不可用或 Lua 执行失败 → **fail-closed** 503，不落请求、不预扣、不同步落单。
- 补偿使用「按 MySQL 权威值幂等 SET」：`remaining = total_stock - sold - inflight_queued`，不用 `INCR` 回补，避免重复消费/崩溃重放造成超预扣。

### 7.3 下单请求路径（`POST /flash-sales/:id/orders`）

1. 校验 `sku_id > 0`、`idempotency_key` 非空；
2. 计算 `request_hash = sha256(activity_id|sku_id)`；
3. 容量保护（闸门之前，无副作用）：
   - 用户级固定窗口限流 → `12009/429`；
   - 活动级固定窗口限流 → `12009/429`；
   - 排队软上限 `GET flashsale:queued:*` → `12010/429`；
4. 进程内熔断器：Open 直接 `1005/503`，不进入闸门；
5. 执行 Lua 闸门；
6. 按闸门结果分流：
   - `NOT_FOUND` → `12001`；`NOT_IN_WINDOW` → `12002`；`SOLD_OUT` → `12003`；`SKU_NOT_BOUND` → `1001`；`IDEMPOTENT_CONFLICT` → `12005`；
   - `ALREADY_PURCHASED`：先查 MySQL 是否存在成功订单或未终态请求；真已购 → `12004`；孤儿标记 → 清除 `bought` 后继续入队；
   - `IDEMPOTENT_HIT`：按 `user_id + idempotency_key` 查请求；存在且同 hash → 返回既有状态；存在但异 hash → `12005`；不存在（孤儿标记）→ 清除标记后继续入队；
   - `GATE_PASSED` → 入队；
7. 入队：插入 `flash_sale_order_requests(status=0)`；撞 `uk_request_idempotency` 时读回既有请求；失败时统一在此处补偿预扣并清标记（调用方不得二次补偿）；
8. 返回 `status=queued`，不下单、不等消费。

### 7.4 异步消费：两种互斥模式

`flash_sale.dispatch_mode`：

| 模式 | 组件 | 出队方式 | 适用 |
| --- | --- | --- | --- |
| `db_scanner`（默认） | 仅 MySQL Scanner | `status=queued AND next_attempt_at <= NOW()` + `FOR UPDATE SKIP LOCKED` | 本地开发，不依赖 Kafka |
| `kafka` | Outbox Publisher + Kafka Consumer | 按消息 `request_id` + `FOR UPDATE` | 需要外部消息管道；必须同时 `kafka.enabled=true` |

互斥原因：两者都会处理同一批 `status=queued` 行，同时运行会争抢行锁、重复消耗 `retry_count`。配置矛盾（模式与开关不一致）启动即失败，不静默退回另一模式。

消费事务核心（两条入口共用）：

```text
BEGIN
  SELECT Request FOR UPDATE        -- Scanner 用 SKIP LOCKED 领取；Kafka 按 id 锁行
  检查 status（只有 queued 执行业务）
  重试预算检查（retry_count > 3 → dead/exhausted）
  resolveSku（SKU/商品可用性 + 名称/主图快照）
  SAVEPOINT
  INSERT flash_sale_orders
  UPDATE flash_sale_activity_skus SET sold=sold+1 WHERE sold<total_stock
  任一失败 → ROLLBACK TO SAVEPOINT
  UPDATE Request status=success/failed（+ flash_order_id）
COMMIT
```

关键点：

- **状态闸门**：`status != queued` 一律跳过。这是 Kafka 重复投递、Offset 提交失败重投、人工修复后重发同一条事件时唯一的业务幂等闸门。
- **`event_id` 不是去重台账**：它由 `request_id` 确定性生成；人工修复后会再次发布同一 `event_id`，永久去重台账会把合法请求误判为重复。
- **技术失败不写终态**：业务事务干净回滚，再由独立短事务推进 `retry_count` 与 `next_attempt_at`，避免把可恢复的基础设施故障固化成业务失败。
- **保存点**：订单与库存必须同生共死，任一失败回滚到保存点后才允许写 `failed`。

### 7.5 状态机、重试与死信

```text
queued(0) ──成功──► success(1)（与订单同事务）
    │
    ├──业务失败──► failed(2)（终态，不自动重试）
    │
    └──技术失败──► retry_count+1 + next_attempt_at 退避
                         │
                         └─ retry_count > 3 ──► dead(3)（死信/待修复）
```

| 参数 | Scanner | Kafka |
| --- | --- | --- |
| 重试预算 | `maxConsumeRetryAttempts = 3` | 同一列 `retry_count`，同一预算 |
| 退避 | 5s → 10s → 20s，封顶 60s | 1s → 2s → 4s，封顶 5s |
| 超限处理 | 直接在 MySQL 落 `dead` | **先**把消息投递到 DLQ 并获得 Broker ACK，**后** `MarkRequestDead` 落 `dead` |
| 顺序原因 | Scanner 无死信主题 | 若先落 dead 而 DLQ 发送失败，请求会消失 |

失败分类（`classifyConsumeError`）：

- 业务失败（写 `failed`）：活动不存在/不在时间窗/库存不足/已购/幂等冲突/SKU 不可用/参数非法/SKU 或商品不存在；
- 技术失败（重试）：数据库错误、连接中断、死锁、未知错误；
- 唯一键分歧：`uk_flash_one_per_user` → 已购；`uk_flash_idempotency` → 幂等冲突；`uk_flash_order_no` → 撞号，技术重试。

终态提交后的副作用（`applyConsumeOutcome`）：

- 离开 `queued` → 排队计数 -1；
- `failed`/`dead` → `convergeStockAndMarkers`：按权威值幂等 SET 剩余量 + 清除 `bought`/`idem` 标记；
- 成功/失败写结构化日志与指标。

### 7.6 Outbox（仅 Kafka 模式）

`flash_sale_order_requests` 同时是 Outbox：

- Publisher 领取条件：`status=queued AND published_at IS NULL`，且 `next_publish_at` 到期，且租约为空/过期；
- 领取写入 `publish_claim_token` + `publish_lease_until`（数据库级租约，多实例互斥）；
- 发布成功后**才**写 `published_at`、`published_partition`、`published_offset`；
- 发布语义 at-least-once；租约过期可被其他 Publisher 重新领取；
- 消费侧会在消费事务内用「已从 Kafka 消费」这一事实补写 `published_at`（条件 `published_at IS NULL`），修复「Broker ACK 后 Publisher 标记失败、Consumer 先落终态」的窗口；
- 发布失败写 `next_publish_at` 做业务退避，并记录 `last_publish_error`；
- 发布循环：batch 默认 100、poll 200ms、错误退避 2s 指数增长封顶 30s；只有「本轮完全无进展」才退避，部分失败但仍有进展时保持正常节奏。

### 7.7 对账与缓存收敛

后台 `ReconcileCache`，默认每 60s：

```text
remaining = max(0, total_stock - sold - inflight_queued)
```

- `inflight_queued = COUNT(flash_sale_order_requests WHERE activity_id=? AND status=queued)`，按 SKU 分组，计入在途预扣；
- 活跃活动：回补/刷新 `activity`、`stock`、`queued` 计数，清 `soldout`；
- 已结束且仍在 60s grace 窗口：收敛终态；在途归零后失效活动元数据/售罄/排队计数，并置空值标记；
- 收敛使用幂等 `SET`，重复执行结果不变；Redis 失败只记日志，不改变 MySQL 事实。

### 7.8 容量保护

| 机制 | 开关 | 行为 | 失败策略 |
| --- | --- | --- | --- |
| 用户级限流 | `flash_sale.rate_limit.enabled` | 固定窗口计数，超限 `12009/429` | Redis 计数失败 fail-open（放行到闸门） |
| 活动级限流 | 同上 | 同上 | 同上 |
| 排队软上限 | `flash_sale.queue_capacity.enabled` | `queued` 近似计数 ≥ 上限 → `12010/429` | Redis 失败 fail-open；对账收敛权威值 |
| 熔断器 | `flash_sale.circuit_breaker.enabled` | 连续失败 → Open → 快速 `1005/503`；Half-Open 探测 | 进程内状态机，多实例各自独立；降级为快速失败，不降级到同步 MySQL |
| 闸门 | 无开关 | 售罄/不存在/时间窗/已购/幂等快速失败 | **fail-closed** |

注意：限流与排队上限是**容量保护**，不是正确性边界；不超卖仍由 MySQL 条件更新与唯一约束保证。

### 7.9 人工修复与审计

- 接口：`POST /admin/flash-sales/requests/:id/repair`，权限 `flash_sale:repair`。
- 仅允许 `dead → queued`；条件更新 `WHERE status=dead` 保证并发只迁移一次。
- 同事务写入 `flash_sale_request_audits`（append-only，无 UPDATE/DELETE 接口），记录操作者、前后状态、原因。
- 修复时同步重置发布状态（`published_at`、claim token、租约、退避、尝试次数），让请求重新可被 Publisher 领取；旧 Publisher 的迟到标记会因 token 不匹配而落空。
- `failed` 是业务终态，不提供重新排队；只有 `dead` 可修复。

### 7.10 Kafka 消息契约与运行语义

- Topic：`surgecart.flashsale.order.requested.v1`；DLQ：`surgecart.flashsale.order.requested.v1.dlq`；消费组：`surgecart-flashsale-order-worker-v1`。
- 消息 Key：`<activity_id>:<sku_id>`，同一活动 SKU 进入同一分区；单 SKU 的消费并行度上限为「同 key 落在同一分区」所决定（通常为 1）。
- 事件类型：`flashsale.order.requested`，`schema_version=1`。
- `event_id = flashsale.requested:<request_id>`，重发不变。
- 消息字段只用于「定位 + 一致性校验」：`request_id`、`user_id`、`activity_id`、`sku_id`、`idempotency_key`、`request_hash`。**不包含**价格、库存、已售数量，也绝不作为落单依据。
- 消费方校验身份字段与 MySQL 行不一致 → 永久错误，进 DLQ，不改数据库。
- Topic 必须显式创建（Broker `KAFKA_AUTO_CREATE_TOPICS_ENABLE=false`）：`docs/development/kafka.md` 的手动本地示例是 12 分区 / RF=1；K8s 基础设施 Job 默认 `TOPIC_ORDER_REQUESTED_PARTITIONS=1` / RF=1。两者都只是本地单节点配置。
- 健康检查与 Kafka 解耦：Kafka 故障不摘 HTTP；异步状态靠指标观测。

---

## 8. IAM / RBAC

### 8.1 Access token 与会话

- JWT：HS256，issuer `surgecart`，access token 固定 1h，claims 含 `sub`（用户/管理员 id）、`sid`、`type`（`user`/`admin`）。
- 密钥：`auth.jwt.secret` / `AUTH_JWT_SECRET`，至少 32 字节，缺失或过短启动失败。
- 有状态会话（Redis）：
  - 用户：`iam:session:{sid}`（Hash：`user_id`、`revoked`、`login_at`、`user_agent`、`ip`、`auth_epoch`）；
  - 管理员：`iam:admin:session:{sid}`；
  - 用户会话索引：`iam:user:{userID}:sessions`（ZSET，用于「撤销其它/全部会话」）。
- 鉴权判定顺序：
  1. Bearer 格式 → 验签 + exp/issuer；
  2. `type` 必须匹配（用户接口 type≠user → 403；管理员接口 type≠admin → 403）；
  3. 会话存在且未撤销；
  4. 每请求查 `users`/`admins`：`status=1` 且 `auth_epoch` 匹配；
  5. 注入 Principal。
- **fail-closed**：Redis/DB 查询失败一律拒绝，不依赖「JWT 未过期」。
- 登出：`AuthSignatureOnly` / `AdminAuthSignatureOnly`，只验签，幂等撤销；即使会话已撤销也能到达 handler。

### 8.2 Refresh token 轮换

- 仅前台用户域；refresh token 只以哈希落库（`refresh_tokens.token_hash`）。
- 字段：`family_id`、`parent_id`、`generation`、`sid`、`auth_epoch`、`expires_at`、`revoked_at`、`revoked_reason`。
- 判定顺序：未知/已撤销 → INVALID；已轮换再次提交 → REUSE（全量撤销该 family）；过期 → EXPIRED；否则轮换。
- 轮换 = session upsert + 事务内条件 UPDATE 旧 token 为 rotated + INSERT 新后代 + 签新 access token。
- 禁用用户或 `auth_epoch` 落后 → 401，无任何副作用。

### 8.3 授权

- 权限模型：`admins → admin_roles → role_permissions → permissions`。
- 权限粒度 `资源:动作`，如 `order:ship`、`coupon:create`。
- `RequirePermission(code)`：超级管理员 `IsSuper` 直接放行；否则查权限集合；未命中 403；DB 查询失败 500（fail-closed，不进 handler）。
- 启动时 `seedPermissions` 幂等写入标准权限；超级管理员由 `seedSuperAdmin` 在不存在时用 `ADMIN_SUPER_PASSWORD` 创建。

### 8.4 双身份域隔离

| 维度 | 前台用户 | 后台管理员 |
| --- | --- | --- |
| 凭据表 | `users` | `admins` |
| token type | `user` | `admin` |
| 会话前缀 | `iam:session:` | `iam:admin:session:` |
| 中间件 | `Auth` / `AuthSignatureOnly` | `AdminAuth` / `AdminAuthSignatureOnly` |
| 授权 | 数据归属隔离（只操作本人） | RBAC 权限码 |

---

## 9. 配置系统

### 9.1 读取优先级

```text
环境变量（契约注册表中显式登记的 Env） > manifest/config/config.yaml > 代码默认值
```

- 统一入口：`internal/config` 的 `String/Bool/Int/Int64/Duration/StringList`。
- 环境变量名**必须**登记在 `internal/config/contract.go`，不依赖 GoFrame 的「下划线 → 点号」推导。
- 已知陷阱：GoFrame 的 `GetEffective/GetWithEnv` 会把 `flash_sale.metrics.enabled` 归一化成 `flash.sale.metrics.enabled`，导致 YAML 永远不命中。本仓库的 loader 直接使用不做归一化的 `gcfg.Config.Get`，不要改回 GoFrame 默认读取方式。
- 类型解析严格：非法 bool/int/duration 返回带配置项与环境变量名的错误，不静默回退。

### 9.2 新增配置的三处一致性

新增配置必须同时修改：

1. `internal/config/contract.go`：登记 `Spec{Key, Env, Kind, Default, Sensitive, Consumer, Description}`；
2. `manifest/config/config.yaml`：写出同路径；
3. `docs/development/configuration.md`：补充行。

三者一致性由 `internal/config/contract_test.go` 断言。`make test` 会检查。

### 9.3 敏感配置（禁止写入日志/文档/响应）

| 配置 | 环境变量 |
| --- | --- |
| DB 密码 | `DATABASE_DEFAULT_PASS` |
| Redis 密码 | `REDIS_DEFAULT_PASS` |
| JWT 密钥 | `AUTH_JWT_SECRET` |
| 超管初始密码 | `ADMIN_SUPER_PASSWORD` |
| 七牛 AK/SK | `QINIU_ACCESS_KEY` / `QINIU_SECRET_KEY` |

敏感字段在 `config.yaml` 中保持空值；Compose 使用 `${VAR:?}` 必填，缺失即启动失败，无内置默认回退。

### 9.4 常用配置分组

| 分组 | 关键项 |
| --- | --- |
| `server` | `address`、`graceful_timeout` |
| `lifecycle` | `pre_stop_delay`、`termination_grace_period` |
| `readiness` | `probe_timeout`、`cache_ttl` |
| `database.default` | `type/host/port/user/pass/name/charset/debug` |
| `redis.default` | `address/db/pass` |
| `auth` | `jwt.secret`、`session.ttl`、`refresh.ttl` |
| `admin.super` | `username`、`password` |
| `order` | `pay_timeout`（默认 900s）、`cancel_scan_interval`（60s） |
| `flash_sale` | `dispatch_mode`、`shutdown_timeout`、`reconcile_scan_interval`（60s）、`consume_scan_interval`（1s） |
| `flash_sale.kafka` | `enabled`、`brokers`、`topic`、`dlq_topic`、`consumer_group`、`publisher.*` |
| `flash_sale.rate_limit` | `enabled`、用户/活动 `max_requests` 与 `window_seconds` |
| `flash_sale.queue_capacity` | `enabled`、`max_queued_per_sku`、`counter_ttl_seconds` |
| `flash_sale.circuit_breaker` | `enabled`、`failure_threshold`、`open_timeout_seconds`、`half_open_max_probes` |
| `flash_sale.metrics` | `enabled` |
| `storage.local` | `root` |
| `qiniu` | `access_key/secret_key/bucket/domain/region/token_ttl/max_file_size/allowed_*` |
| `deployment` | `require_external_endpoints`（K8s 严格模式） |
| `startup.dependency` | `timeout` |

### 9.5 Kubernetes 严格模式

`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true` 时，启动前要求：

- `DATABASE_DEFAULT_HOST`、`DATABASE_DEFAULT_NAME`、`REDIS_DEFAULT_ADDRESS` 必须由环境变量显式提供；
- 不得指向 `localhost` / `127.0.0.1` / `::1` 等回环地址；
- 校验发生在建立任何数据库/Redis 连接之前。

生产 overlay 的 ConfigMap 必须设置该值为 true。

---

## 10. 可观测性

### 10.1 指标开关与暴露路径

- 开关：`flash_sale.metrics.enabled` / `FLASH_SALE_METRICS_ENABLED`，默认 **false**。
- 开启后挂载 `GET /metrics`，使用默认 Prometheus Registry，包含业务指标、应用指标、Go Runtime / Process Collector。
- 抓取路径：Compose 内 `app:8000/metrics`；K8s 内 `surgecart.surgecart.svc.cluster.local:8000/metrics`。
- Nginx 公共入口对 `/metrics` 返回 404；不对外暴露。

### 10.2 指标清单（名称与标签是契约）

| 指标 | 类型 | 标签 | 含义 |
| --- | --- | --- | --- |
| `flashsale_requests_total` | CounterVec | `interface`(order/consume)、`result` | 秒杀请求/消费处理总数 |
| `flashsale_request_duration_seconds` | HistogramVec | 同上 | 耗时，用于 p95/p99 |
| `flashsale_outbox_claimed_total` | Counter | 无 | Outbox 领取数 |
| `flashsale_outbox_lease_reclaimed_total` | Counter | 无 | 领取时回收过期租约数 |
| `flashsale_outbox_published_total` | Counter | 无 | Broker 确认发布数 |
| `flashsale_outbox_publish_failed_total` | CounterVec | `reason` | 发布失败数 |
| `flashsale_outbox_retried_total` | Counter | 无 | 写入退避等待重试数 |
| `flashsale_outbox_mark_skipped_total` | CounterVec | `reason` | 条件标记未命中数 |
| `flashsale_outbox_publish_round_duration_seconds` | Histogram | 无 | 单轮发布耗时 |
| `flashsale_consumer_records_total` | Counter | 无 | 拉取消息数（含重复） |
| `flashsale_consumer_processed_total` | CounterVec | `result` | 消费处置结果 |
| `flashsale_consumer_retries_total` | Counter | 无 | 技术失败重试数 |
| `flashsale_consumer_dlq_total` | CounterVec | `reason` | 死信确认数 |
| `flashsale_consumer_commit_failures_total` | Counter | 无 | 位移提交失败数 |
| `flashsale_consumer_restarts_total` | Counter | 无 | Consumer 意外退出重建数 |
| `flashsale_consumer_processing_seconds` | HistogramVec | `result` | 单条消息处理耗时 |
| `flashsale_consumer_joined` | Gauge（可观测后才导出） | 无 | 是否已加入消费组；仅 kafka 模式 |
| `flashsale_outbox_backlog_pending` | Gauge（可观测后才导出） | 无 | 待发布积压；仅 kafka 模式 |
| `surgecart_http_requests_total` | CounterVec | `method`、`route`（模板）、`status` | 全站 HTTP 请求数 |
| `surgecart_http_request_duration_seconds` | HistogramVec | 同上 | 全站 HTTP 耗时 |
| `surgecart_http_requests_in_flight` | Gauge | 无 | 当前在途请求数 |
| `surgecart_kafka_consumer_lag` | GaugeVec | `consumer_group`、`topic`、`partition` | 分区 Lag（messages） |
| `surgecart_sampler_up` | GaugeVec | `sampler`(outbox/kafka_lag) | 采样器健康 |
| `surgecart_outbox_oldest_age_seconds` | Gauge（可观测后才导出） | 无 | 最老待发布事件年龄 |

`result` 取值固定：`queued`、`success`、`gate_rejected`、`rate_limited`、`queue_full`、`failed`、`dead`、`duplicate`、`invalid`、`error`。DLQ reason 固定：`parse_error`、`schema_version`、`event_type`、`invalid_field`、`identity_mismatch`、`request_missing`、`retry_exhausted`。

标签基数约束：**禁止**把 URL、用户/订单/活动/商品 ID、查询参数放进标签。HTTP 未命中路由统一记为 `unmatched`，状态码非法记为 `unknown`。

### 10.3 Prometheus / Grafana

- Compose：`prometheus/prometheus.yml`，15s 抓取 `app:8000/metrics`；`prometheus/rules/flashsale_latency.yml` 产出 `flashsale_order_request_duration_p95_seconds` / `p99_seconds`。
- K8s：`manifest/kubernetes/monitoring/prometheus/overlays/local`（Prometheus 2.53.0，单副本，PVC 5Gi/本地 7d 保留，ClusterIP）。
- Grafana：`manifest/kubernetes/monitoring/grafana/overlays/local`（11.2.2，单副本，PVC 2Gi，数据源与看板 Provisioning；14 面板 `SurgeCart Overview`）。
- 明确未交付：Alertmanager、告警规则、通知渠道、ServiceMonitor/Operator、HA、Ingress/NodePort、远程存储。
- 采样设计：`/metrics` 抓取路径**不查库**。Outbox 积压/最老年龄由进程内 5s 采样器查询；Kafka Lag 由独立 Kafka 客户端 10s 采样。采样失败只置 `sampler_up=0`，业务指标保留上一次有效值，不伪造 0。

### 10.4 已记录的监控缺陷（未修复）

T5-04 在真实 kind 集群实测：

- `surgecart_outbox_oldest_age_seconds` 在 `dashboard.timezone=Asia/Shanghai` 下恒定多报 8 小时；
- 根因：`NOW(6)`（MySQL 会话时区 `+08:00`）与 `created_at`（驱动写入的 UTC 墙钟）混用；
- 状态：**未修复**，仅在报告中记录。回答相关问题时不得声称已解决。

---

## 11. 部署与运维

### 11.1 Docker Compose

| 服务 | 角色 | 端口/网络 |
| --- | --- | --- |
| `nginx` | 唯一对外 HTTP 入口 | `${NGINX_PORT:-8080}` → 容器 80 |
| `app` | Go 后端 | 仅 Compose 内网 `app:8000`，不映射宿主机端口 |
| `mysql` | 权威数据 | `127.0.0.1:${MYSQL_PORT:-3306}` |
| `redis` | 会话 / 秒杀准入 | `127.0.0.1:${REDIS_PORT:-6379}` |
| `prometheus` | 指标抓取 | `${PROMETHEUS_PORT:-9090}` |
| `kafka` | 可选（profile `kafka`） | `127.0.0.1:${KAFKA_PORT:-29092}` |

应用容器加固：非 root `10001:10001`、只读根文件系统、`/tmp` tmpfs、`cap_drop: ALL`、`no-new-privileges`、`stop_grace_period: 60s`。入口脚本先 `migrate up`（幂等 + `GET_LOCK`），再 `exec serve`。

### 11.2 Kubernetes 体系

| 入口 | 路径 | 内容 |
| --- | --- | --- |
| 应用 | `manifest/kubernetes/overlays/{local,production}` | Namespace/ConfigMap/PVC/Deployment/Service |
| 迁移 | `manifest/kubernetes/migration/overlays/{local,production}` | 独立 Job，只注入数据库凭据 |
| 基础设施 | `manifest/kubernetes/infrastructure/` | MySQL/Redis/Kafka（单副本，Kafka KRaft） |
| 监控 | `manifest/kubernetes/monitoring/{prometheus,grafana}/overlays/local` | 独立命名空间 `monitoring` |
| 发布锁示例 | `manifest/kubernetes/examples/deploy-lock/` | Lease |

本地 kind 流程：

```bash
bash scripts/k8s-cluster.sh create
bash scripts/k8s-infra.sh secrets
bash scripts/k8s-infra.sh create
bash scripts/k8s-infra.sh verify
docker build -t surgecart:<tag> .
kind load docker-image surgecart:<tag> --name surgecart-local
kubectl apply -k manifest/kubernetes/migration/overlays/local
kubectl apply -k manifest/kubernetes/overlays/local
```

### 11.3 生产发布顺序（由 `scripts/deploy-k8s.sh` 强制）

```text
构建镜像 → 获取发布锁（Lease）→ 版本降级保护 → 执行迁移 Job
→ 应用 Deployment → 等待 Available/Ready → 发布成功判定 → 释放锁
```

- 迁移与应用解耦：K8s 中应用镜像默认入口是 `serve`，不执行 DDL；`migrate up` 由独立 Job 执行。
- 迁移并发：MySQL 会话级 `GET_LOCK`，等待 10s 超时即明确失败，可由 Job 重试；失败进入 `dirty`，不自动 `force`。
- `serve` 的只读 schema 校验是最后防线。
- 发布锁剩余风险（fencing 等）见 `docs/design/deploy-lock.md`，不要声称「已完全消除」。

### 11.4 迁移机制

- 文件：`internal/migrations/sql/{14位时间戳}_{name}.up.sql`，`//go:embed` 内嵌，运行时不依赖工作目录。
- 版本号数字升序即时间升序；不要修改已应用的迁移文件。
- `migrate up` 幂等；`migrate force` 只改 `schema_migrations`，不执行/校验/修复 DDL，仅人工使用。
- 当前 25 个 SQL 文件（含一个 `.down.sql`），最新版本 `20261001000024`。

### 11.5 脚本与 Make 目标

`Makefile` 是薄封装，真实逻辑在 `scripts/`。常用目标：

| 目标 | 作用 |
| --- | --- |
| `make init` / `make up` / `make bootstrap` | 两阶段初始化 / 完整启动 / 一键 |
| `make down` / `restart` / `status` / `logs` / `health` | 生命周期 |
| `make build` / `docker-build` / `docker-push` | 本地静态编译 / 镜像构建与推送 |
| `make migrate-up` / `migrate-version` / `migrate-force` | 迁移 |
| `make deploy-k8s` | 生产发布（锁 + 迁移 Job + Deployment） |
| `make test*` | 各类门禁（见 §12） |

---

## 12. 测试与质量门禁

### 12.1 `make test` 的实际阶段

`scripts/test.sh` 依次执行：

1. `go vet ./...`；
2. `test-lib.sh`（lib.sh 加载语义回归）；
3. `test-unit.sh`（不依赖 MySQL/Redis）；
4. `test-integration.sh`（需 MySQL/Redis）；
5. `test-migration.sh`（真实 MySQL + 多进程：并发锁/锁超时/中途终止）；
6. `check-registry.sh`（migration 版本冲突 + 错误码域漂移）；
7. `test-prometheus.sh`（抓取配置与 rules）；
8. `test-container.sh`（Dockerfile / compose / 凭据分离 / 运行加固静态断言）；
9. `test-k8s-manifest.sh`（真实 Kustomize 构建 + 产物断言 + API Schema）；
10. `test-deploy-k8s.sh`（真实执行 `deploy-k8s.sh` + fake kubectl）；
11. `test-k8s-infra.sh`（基础设施清单契约 + restricted PSS + Secret 边界）；
12. `test-k8s-cluster.sh`（集群脚本行为，fake kind/kubectl）；
13. `test-nginx.sh`（反向代理配置）。

### 12.2 独立测试目标

| 目标 | 依赖 | 说明 |
| --- | --- | --- |
| `test-unit` | 无 | 纯内存单元测试 |
| `test-integration` | MySQL/Redis | 真实集成测试 |
| `test-race` | MySQL/Redis | 熔断器状态机 + Outbox 并发/租约/修复竞态（隔离库 `my_shop_outbox_race`） |
| `test-migration` | 真实 MySQL + 多进程 | 迁移锁与恢复语义 |
| `test-kafka-e2e` | 专用 MySQL/Redis/Kafka | 真实 Kafka 秒杀端到端，含可选故障注入 |
| `test-serve-lifecycle` | 真实进程 | SIGTERM 与重启收敛 |
| `test-container-runtime` | Docker | 真实镜像构建 + 非 root/只读/SIGTERM/drain/退出码 |
| `test-k8s-manifest` | kubectl | Kustomize 构建与产物断言 |
| `test-deploy-k8s` | 无集群 | 发布锁/降级保护/成功判定 |
| `test-k8s-infra` | kubectl | 基础设施清单契约 |
| `test-k8s-cluster` | 无集群 | fake kind/kubectl |
| `test-k8s-infra-runtime` | Docker | 容器级等价验证，**不是** Kubernetes 验证 |
| `test-storage` | 真实七牛凭据 | 签发 → 直传 PNG → 校验 → 删除 |
| `test-nginx` | 无 | Nginx 信任边界/重试/DNS/日志关联 |
| `test-prometheus` | 无 | 抓取配置与 recording rules |
| `test-lib` | 无 | lib.sh 空值不覆盖默认值等 |

### 12.3 测试资产分布

| 路径 | 内容 |
| --- | --- |
| `test/e2e/` | 真实 HTTP 链路（订单、秒杀 V2/V3/V5、退款、优惠券、IAM 隔离、Kafka E2E、serve drain） |
| `test/manifest/` | Kustomize、基础设施、RBAC、监控、Grafana 静态断言 |
| `test/container/` | 容器运行门禁与测试证书 |
| `test/k6/` | 性能/可靠性脚本（smoke、read-load、flashsale-order、数据准备、结果汇总） |
| `test/serveproc/` | 进程级 serve 生命周期测试构建 |
| `scripts/kafka-reliability/` | 积压、Broker 停机、崩溃重投、DLQ 实验脚本 |

### 12.4 已验证 / 未验证矩阵（必须如实保留）

| 能力 | 状态 | 证据/限制 |
| --- | --- | --- |
| Compose 单机五服务栈 | 已验证 | 启动、健康检查、迁移、业务链路 |
| kind 单节点基础设施 | 已验证（T3-01） | Node Ready、PVC Bound、MySQL `SELECT 1`、Redis `PING`、Kafka 生产消费回读 |
| 应用 K8s 部署 + 两种消费模式 | 已验证（T3-02） | `db_scanner` 与 `kafka` 的真实业务链路 |
| Prometheus K8s 抓取 | 已验证（T4-03） | Target up、真实序列 |
| Grafana Provisioning/面板查询 | 已验证（T4-04） | API 层验证；Kafka 非零积压曲线未观测；UI 截图 NOT_VERIFIED |
| k6 Smoke / 只读负载 | 已验证（T5-01/T5-02） | 1/5/10 VU，本地单节点 |
| `kafka` 模式高并发正确性 | 已验证（T5-03） | 不超卖、一人一单、幂等、异步收敛、Lag=0 |
| `db_scanner` 模式高并发正确性 | **NOT_VERIFIED** | T5-03 明确未验证 |
| Kafka 积压/停机/崩溃/DLQ | 已验证（T5-04） | 4 组故障注入；毫秒级「MySQL 已提交、Offset 未提交」窗口 NOT_VERIFIED |
| 生产容量 / 多副本 / 高可用 | **未验证** | 无相关实验与数据 |
| 告警 | **未实现** | 无 Alertmanager/规则/通知 |
| 真实支付 | **未实现** | 普通订单支付为 Mock |

---

## 13. 已知限制与陷阱

### 13.1 能力边界

- 单机 / 单节点：Compose 五服务栈、kind 单节点；K8s 应用与监控均单副本。
- 无 Ingress/TLS/HPA；Service 为 `ClusterIP`，访问靠 `port-forward`。
- MySQL/Redis/Kafka 均单副本；Kafka KRaft 单节点，RF=1、min.insync.replicas=1。
- 无真实支付、无资金链路；秒杀订单「下单即成交」，不走支付/取消/退款。
- 无生产容量结论；T5-02 只有本地 1/5/10 VU 只读负载。

### 13.2 代码/文档陷阱

| 陷阱 | 说明 |
| --- | --- |
| 设计文档可能滞后 | 例如 `docs/design/architecture.md` §5 仍写「无外部 MQ」，但 Kafka 已实现；以代码为准 |
| Topic 分区数在不同环境不一致 | `docs/development/kafka.md` 手动示例 12 分区；K8s 基础设施默认 1 分区；不要混用，也不要把任一方当作生产配置 |
| `event_id` 不是幂等台账 | 人工修复后会重发同一 `event_id`；业务幂等必须回到 MySQL 请求行状态 |
| `GATE_PASSED` 不是下单成功 | 只是 Redis 预扣；MySQL 失败必须补偿 |
| Scanner 与 Consumer 不能同时跑 | `dispatch_mode` 严格互斥，同时运行会争抢行锁、重复消耗重试预算 |
| `migrate force` 危险 | 只改版本记录，不校验真实 schema |
| `/metrics` 默认关闭 | 不设置 `FLASH_SALE_METRICS_ENABLED=true` 时不会挂载 |
| `/ready` 不检查 Kafka | 不要把 Kafka 故障加入 readiness |
| Outbox 最老年龄时区缺陷 | 恒定多报 8 小时，未修复 |
| 多实例仅部分验证 | 条件更新/唯一约束设计为并发安全，但未做多副本真实压测 |

### 13.3 安全边界

- 所有敏感值仅经环境变量注入；`config.yaml` 敏感字段为空。
- 应用容器非 root、只读根文件系统、drop ALL capabilities、no-new-privileges。
- JWT + Redis 会话；禁用用户即时失效（`auth_epoch`）；权限查询 fail-closed。
- 七牛 AK/SK 只在服务端使用；上传凭证有 TTL、大小、扩展名、MIME 限制。
- 发布锁存在 fencing 残余风险（见 `docs/design/deploy-lock.md` §10.1），不是「绝对互斥」。

---

## 14. 常见修改配方

### 14.1 新增一个 HTTP 接口

1. 在 `api/<domain>/v1/` 定义请求/响应结构体，`g.Meta` 写完整 `path` 与 `method`；
2. 在 `internal/controller/<domain>/` 增加 handler，读取 Principal，调用 Service；
3. 在 `internal/service/<domain>.go` 增加接口方法，并在 Logic 的 `init()` 注册实现；
4. 在 `internal/logic/<domain>/` 实现业务规则（事务、条件更新、错误码）；
5. 在 `internal/cmd/routes_frontend.go` 或 `routes_admin.go` 注册路由；后台写接口必须加 `RequirePermission(code)`；
6. 如需新错误码，加入 `internal/codes/codes.go` 的常量与 `codeTable`；
7. 补测试：`test/e2e/` 或对应包的 `_test.go`；
8. 运行 `go vet ./...` 与相关测试。

### 14.2 新增数据库迁移

1. 新建 `internal/migrations/sql/{YYYYMMDDHHMMSS}_{name}.up.sql`（14 位时间戳，数字升序）；
2. 不要修改已应用的迁移；如需回滚，按现有约定提供 `.down.sql`（仓库并非每个迁移都有 down）；
3. 执行 `make migrate-up`；用 `make migrate-version` 确认 `dirty=false`；
4. 若新增表/列被业务使用，同步更新 `docs/design/*` 与 `docs/LLM_CONTEXT.md` 的数据模型；
5. 运行 `make test-migration` 与 `scripts/check-registry.sh`。

### 14.3 新增配置项

1. `internal/config/contract.go` 登记 `Spec`；
2. `manifest/config/config.yaml` 写出同路径；
3. `docs/development/configuration.md` 补行；
4. 如为敏感项，标记 `Sensitive: true`，并在 Compose/K8s 用 Secret 注入；
5. 运行 `go test ./internal/config/`（契约一致性）。

### 14.4 新增指标

1. 在 `internal/metrics/` 定义 Counter/Gauge/Histogram，标签必须是低基数枚举；
2. 在 `init()` 注册到默认 Registry；
3. 所有 `Observe` 前检查 `Enabled(ctx)`；
4. 不要在 `/metrics` 抓取路径查数据库；需要查库的指标用后台采样器写入 Gauge；
5. 更新 `prometheus/rules`（如需 recording rule）与本文档指标清单；
6. 运行 `make test-prometheus`、`make test-k8s-manifest`。

### 14.5 修改秒杀逻辑前的检查清单

- [ ] 五条不变量仍然成立？最终兜底是否仍是 MySQL 条件更新 + 唯一约束？
- [ ] Redis 是否仍然只是派生缓存？MySQL 失败时补偿是否幂等（SET 而非 INCR）？
- [ ] 状态闸门是否仍在事务内、锁行之后？是否仍只处理 `queued`？
- [ ] 技术失败是否仍未写终态？重试预算是否仍由独立短事务推进？
- [ ] `db_scanner` 与 `kafka` 是否仍严格互斥？关闭顺序是否仍为 Publisher → Consumer → Producer → Scanner？
- [ ] `event_id` 是否仍未被当作永久去重台账？
- [ ] 死信顺序是否仍是「先 DLQ ACK、后 dead 终态」？
- [ ] 对账公式是否仍计入 `inflight_queued`？
- [ ] 是否补了 scanner 与 kafka 两条路径的测试？
- [ ] 是否更新了 `docs/design/flash-sale.md` 与本文档？

---

## 15. 术语表

| 术语 | 含义 |
| --- | --- |
| 闸门 / Gate | Redis + Lua 的准入预扣逻辑，快速失败层 |
| 预扣 | Redis `DECR remaining`，只是准入，不是最终库存事实 |
| 出队表 | `flash_sale_order_requests`，兼作异步队列与 Outbox |
| Outbox | 请求表上的发布状态列（`published_at`/租约/退避），保证事件最终发布 |
| DLQ | 死信 Topic，承载不可处理/重试耗尽的消息 |
| inflight | `status=queued` 的请求数，对账时计入已占用库存 |
| 一人一单 | 同一 `(activity_id, sku_id, user_id)` 至多一个成功订单 |
| 幂等键 | 客户端提供的 `idempotency_key`，配合唯一约束防重复 |
| 快照 | 下单时保存的商品名/价格/地址等，历史订单不随源数据变化 |
| Principal | 认证后注入请求上下文的身份（用户/管理员） |
| 双身份域 | 前台用户与后台管理员使用独立表、独立 token、独立会话 |
| 发布锁 | 基于 K8s Lease 的发布互斥机制，不是绝对 fencing |
| NOT_VERIFIED | 未在真实环境验证，不能表述为通过 |

---

## 16. 证据与延伸阅读

| 主题 | 文档 |
| --- | --- |
| 项目导览 | `docs/project-overview.md` |
| 系统架构 | `docs/design/architecture.md` |
| 秒杀设计（权威细节） | `docs/design/flash-sale.md` |
| 订单设计 | `docs/design/order.md` |
| 库存 / 优惠券 / 退款 | `docs/design/inventory.md`、`coupon.md`、`refund.md` |
| IAM / RBAC | `docs/design/iam.md`、`rbac.md` |
| 部署 / 容器安全 | `docs/design/deployment.md`、`docs/development/container-security.md` |
| Kubernetes / 迁移 / 发布锁 | `docs/development/kubernetes.md`、`migrations.md`、`docs/design/deploy-lock.md` |
| Kafka 开发说明 | `docs/development/kafka.md` |
| Prometheus / Grafana | `docs/development/prometheus.md`、`grafana.md` |
| 配置矩阵 | `docs/development/configuration.md` |
| 错误码 | `docs/design/error-codes.md` |
| k6 框架 | `test/k6/README.md` |
| 阶段一集成 | `docs/reports/stage1-main-integration/STAGE1_MAIN_INTEGRATION_REPORT.md` |
| T4-03 / T4-04 | `docs/reports/t4-03-prometheus-deployment/`、`t4-04-grafana-dashboard/` |
| T5-02 / T5-03 / T5-04 | `docs/reports/t5-02-load-performance-analysis/`、`t5-03-high-concurrency-correctness/`、`t5-04-kafka-backlog-failure-recovery/` |

**最后提醒**：本文件也会随代码演进。若发现本文与代码不一致，请以代码为准，并在修改代码的同一个变更中更新本文与相关设计文档；不要把历史报告中的 `PASS` 直接复制成当前结论。
