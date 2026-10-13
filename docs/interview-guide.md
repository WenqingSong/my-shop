# SurgeCart 技术说明

> 本文是项目技术解读文档，面向面试官快速理解 SurgeCart 的设计取舍。它不替代开发手册；内容均来自现有代码与 `docs/design/*`，只描述「现状是什么」。更细的状态机、不变量与边界请以文末阅读路径中的领域设计文档为准。

## 项目定位

**解决的问题**：SurgeCart（`my-shop`）是一个基于 GoFrame v2 的电商后端。它以**普通订单**与**秒杀**两条交易链路为核心，围绕「高并发下不超卖、不重复扣减、故障可恢复」这一正确性与一致性难题展开，同时补齐前台用户与后台管理员的完整身份权限体系，以及商品、库存、优惠券、退款、运营内容等电商核心域。

**技术范围**：Go 1.23 + GoFrame v2（HTTP/路由/配置/ORM）；MySQL 8.0 为权威事实来源；Redis 7 承担登录会话与秒杀准入闸门（派生缓存）；Nginx 作为唯一对外 HTTP 入口；Prometheus 提供秒杀指标；Docker Compose 编排交付；七牛云提供对象存储直传凭证。

项目的核心命题不是「堆功能」，而是**在单实例、无外部 MQ 的前提下，用数据库约束 + 条件写入 + 异步队列把一致性和故障恢复做扎实**，并用明确的能力边界保持真实（支付为 Mock、秒杀队列用 MySQL 出队表、无 K8s）。

## 整体架构

后端呈「接入 → 分层业务 → 持久化」的经典结构：

```text
Controller（HTTP 适配）
    ↓
Service（对内业务接口定义）
    ↓
Logic（业务规则、状态机、流程编排）
    ↓
Storage（数据访问：MySQL / Redis）
```

| 层 | 位置 | 职责 |
| --- | --- | --- |
| Controller | `api/` + `internal/controller` | 请求/响应结构体与公开契约，HTTP 适配，不直接操作数据库 |
| Service | `internal/service` | 对内暴露业务能力的接口定义 |
| Logic | `internal/logic` | 业务规则、状态机、事务编排与数据访问 |
| Storage | `g.DB()`（DAO 在 Logic 内联使用） + Redis | 参数化查询、条件写入；DAO 不承载业务规则 |

横切能力（`internal/middleware`、`internal/auth`、`internal/metrics`、`internal/codes`、`internal/storage`）承载认证授权、统一响应、指标、错误码与文件存储。

关键边界：

- Controller 不直接操作数据库；Logic 不处理 Header/Cookie 等 HTTP 细节；`context.Context` 沿调用链传播。
- MySQL 是订单、库存、已售数量的**权威事实来源**；Redis 是**派生缓存**（会话、秒杀预扣库存），不承担最终事实。
- 后台扫描器（订单超时取消、秒杀消费、秒杀对账）在 `serve` 启动时以 goroutine + ticker 拉起，进程内运行，靠「条件状态更新 + RowsAffected」/「FOR UPDATE SKIP LOCKED」保证多实例并发安全。

## 核心设计

### 秒杀链路

秒杀按「先正确、后加速」逐代演进（V1 MySQL → V2 Redis Lua → V3 异步 → V4 故障恢复 → V5 容量保护），每一步都保留五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存），MySQL 始终是最终事实来源。

**为什么异步？**

秒杀的瞬时洪峰若走同步下单，会把全部请求直接压到 MySQL，数据库成为瓶颈。V3 把「下单」拆成两阶段：HTTP 内完成 Redis 闸门预扣 + 写入 MySQL 出队表（`flash_sale_order_requests`，`status=queued`）后立即返回受理结果；后台消费者（默认每 1s）出队并真正落单。这样请求响应时间与库存扣减解耦，数据库压力被削峰。出队表用「`FOR UPDATE SKIP LOCKED` + 状态原子更新」实现，**不引入外部 MQ**，是一个明确的能力边界。

**Redis Lua 做什么？**

活动元数据与秒杀库存预热到 Redis 后，抢购 Lua 脚本在 Redis 内原子完成：空值标记检查 → 活动存在/启用/时间窗检查 → 售罄检查 → 幂等检查 → 一人一单检查 → 剩余库存预扣（`remaining--`）。目的让售罄、穿透、未预热等无效请求在 Redis 层**快速失败**，减少打到 MySQL 的无效请求。Redis 预扣只是**闸门**，不是事实来源。

**如何避免超卖？**

三层兜底：

1. Redis 侧：Lua 原子 DECR 保证预扣计数不为负（`remaining ≥ 0`）。
2. MySQL 侧：条件扣减 `WHERE sold < total_stock` + `RowsAffected` 校验，这是防超卖的最终兜底。
3. 唯一约束：`uk_flash_one_per_user`（一人一单）、`uk_flash_idempotency`（幂等键）杜绝重复订单与重复扣减，且「成功订单数 = sold」在同一事务内保证。

**如何保证最终一致？**

Redis 与 MySQL 之间是「两步、非跨系统原子」：Lua 预扣成功后若 MySQL 失败，由补偿收敛；若进程崩溃于「预扣成功、请求未持久化」之间，由后台对账修正。对账扫描器（默认每 60s）以**权威值收敛**方式将 Redis 刷成 MySQL 事实——`remaining = total_stock - sold - inflight_queued`（幂等 `SET`，其中 `inflight_queued` 计入在途 `queued` 请求，避免误回补导致超预扣）。故障恢复还包括：崩溃重投（行锁随事务回滚释放，请求回到 `queued` 重新领取，唯一约束保证只产生一次业务效果）、死信人工修复（`dead→queued` + append-only 审计）。

### 订单系统

普通订单与秒杀不同：它走完整的「下单 → 支付（Mock）→ 发货 → 收货 → 完成」，并含取消、退款两条出口。

**为什么服务端计算价格？**

客户端提交的价格不可信。下单时服务端**重读 `skus.price`** 求和，金额按 `original_amount`（优惠前）/`discount_amount`（优惠）/`total_amount`（应付）三列快照锁定，同时快照商品名、SKU 名、主图与收货地址。这样下单后改价、改模板、改地址都不影响既有订单，快照自足保证 SKU 或地址被删除后订单仍可读。

**如何保证库存一致？**

创建订单 = 单 MySQL 事务「写 orders + 写 order_items + 条件扣库存 +（若用券）预占券」，任一步失败整体回滚。扣库存用条件更新 `WHERE quantity >= N` + `RowsAffected` 校验，杜绝并发超卖；取消/退款只恢复一次库存，用「条件状态更新 + RowsAffected」原子闸门保证并发下仅一次生效。

**如何处理状态变化？**

订单状态机：`待支付(10) → 已支付(20) → 已发货(30) → 已收货(40) → 已完成(50)`，外加 `已取消(60)`（待支付时用户取消/超时取消）与 `已退款(70)`（已支付未发货，由退款模块执行）两条出口。非法迁移稳定拒绝（`9002`）且状态不变。超时未支付取消由后台扫描器（默认每 60s）扫描 `待支付且已过期` 订单逐单原子取消，查询/支付入口懒取消兜底，多实例并发靠条件状态更新串行化。

### IAM/RBAC

身份体系分两个互相隔离的身份域：前台用户（`users` + `type=user`）与后台管理员（`admins` + `type=admin`），各自独立凭据表、独立 token 类型、独立会话前缀与独立认证中间件。

**为什么 JWT 需要 Redis？**

JWT（HS256）是无状态签名凭证，签出后**不可撤销**。若只用 JWT，「登出」「禁用」「撤销会话」都无法即时生效。因此采用「无状态 JWT 签名 + 有状态 Redis 会话」的混合模型：JWT 携带 `sid` 作为桥，Redis 会话（`iam:session:{sid}`）承载存在性 + 撤销标记 + 用户绑定 + `auth_epoch`。受保护接口在验签 + 校验 `exp` 之后，还必须通过 Redis 会话有效性校验才放行；Redis/DB 查询失败一律 fail-closed（401），绝不放行可能已撤销的 token。登出即把会话逻辑标记为 `revoked`，实现 token 可撤销。

**Refresh Token 如何设计？**

仅前台用户域，长期凭证以 **SHA-256 哈希**落 MySQL（明文只出现一次，不进日志/持久介质）。核心模型「一次登录 = 一个 sid 会话 + 一个 refresh family」：`family_id` 血缘 + `parent_id` 自引用 + `generation` 代际。轮换时在同一事务内「旧 token 置 `rotated` + 新后代 INSERT」，复用同一 sid；并发下靠 InnoDB 行锁串行化条件 UPDATE，后到者重判为重放（reuse），触发**全量撤销**（撤销该用户全部 refresh families + 全部会话，强制重新认证）。绝对过期 30 天，不滑动。

**权限如何控制？**

后台写接口经「认证 + 授权」双层中间件保护：`AdminAuth`（验签 + `type=admin` + 会话 + `admins.status`）+ `RequirePermission(code)`（RBAC）。RBAC 授权采用四表模型（`roles`、`permissions`、`admin_roles`、`role_permissions`），身份管理使用 `admins` 表。权限粒度 `资源:动作`（如 `order:ship`、`refund:review`、`flash_sale:create`）。超级管理员 `IsSuper` 直接放行且唯一不可变；普通管理员按角色持有的权限 code 命中放行、未命中 403；DB 授权查询失败 fail-closed（500，绝不进 handler）。严格区分 401（未认证）与 403（已认证但无权）。

### 工程体系

**Makefile 为什么存在？**

Makefile 是项目生命周期的**统一入口**，本身只做薄封装、复杂逻辑转发到 `scripts/*.sh`。开发者、Agent、CI 共用同一套命令（`make init/up/down/status/logs/health/test/...`），避免「宿主机与容器双启动」、避免每人维护一套手写命令，保证本地与 CI 行为一致（`make build` 与 Dockerfile 使用相同的静态编译参数）。

**CI 做什么？**

CNB 流水线（`.cnb.yml`）分两条：

- `pull_request → pr-check`：启动 MySQL/Redis 依赖 → `make test`（go vet + 单元 + 集成 + Registry/容器化/Nginx/Prometheus 静态断言）→ `make test-race`（竞态检测）。
- `push → main-ci`（单 job 顺序 stage）：依赖启动 → `make test` → `make test-race` → 通过 Makefile 统一执行构建与镜像发布（`make build` → `make docker-build` → `make docker-push`，短 SHA + `latest` 双标签同源、先短 SHA 后 `latest`）→ 原仓库专属的 GitHub 镜像同步。

任一 stage 失败即中断，`latest` 只指向「测试 + 构建 + 推送全绿」的构建，回滚用历史短 SHA 标签、不依赖 `latest`。流水线不出现硬编码密码（凭据用 CNB 内置 token）。

**测试如何分层？**

按「是否依赖真实 MySQL/Redis」切分，`make test` 是完整门禁：

1. **静态检查**：`go vet` + lib.sh 加载语义校验。
2. **单元测试**（`test-unit`）：纯内存、无 MySQL/Redis，覆盖 auth/metrics/health/dashboard 及部分混合包中可抽离的纯函数逻辑。
3. **集成测试**（`test-integration`）：依赖真实 MySQL/Redis，`go test -p 1 ./...` 串行执行（各包共享同一批表，避免跨包并行数据竞争）。
4. **静态断言**：Registry 一致性校验（migration version / 错误码域）、容器化配置（凭据分离/构建上下文）、Nginx 反向代理配置、Prometheus 抓取配置。
5. **竞态检测**：`test-race`（`go test -race`，进程内共享内存并发逻辑）。
6. **独立 E2E**：`test-storage` 需要真实七牛凭据，签发→直传→校验→删除。

## 面试阅读路径

按以下顺序阅读，可建立从「全貌」到「深度」的认知：

```text
README.md
    ↓
architecture.md
    ↓
flash-sale.md
    ↓
iam.md
    ↓
deployment.md
```

| 顺序 | 文档 | 回答什么问题 |
| --- | --- | --- |
| 1 | `README.md` | 项目是做什么的、有哪些核心亮点、如何跑起来 |
| 2 | `docs/design/architecture.md` | 整体分层、模块边界、核心业务流程、基础设施与当前能力边界 |
| 3 | `docs/design/flash-sale.md` | 秒杀为什么异步、Redis Lua 与对账、五个不变量、故障恢复与容量保护 |
| 4 | `docs/design/iam.md` | JWT + Redis 会话、Refresh Token 轮换与重放检测、身份域隔离 |
| 5 | `docs/design/deployment.md` | 容器化部署模型、凭据分离、启动/停机生命周期、CI/CD 与回滚 |

补充阅读：`docs/design/rbac.md`（授权模型）、`docs/design/order.md`（订单状态机与一致性）、`docs/design/inventory.md`（条件扣减防超卖）。`docs/design/*` 是持续维护的项目级长期设计，是了解各模块现状与边界的权威来源。
