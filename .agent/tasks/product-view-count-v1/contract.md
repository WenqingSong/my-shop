# Technical Contract

## Decision Status

APPROVED

（2026-10-06 Owner 确认全部 4 项决定，均与 Recommendation 一致：`products.view_count` 列（不建独立表）；fail-hard；仅前台公开详情触发 +1、前后台列表与后台详情不触发、详情与列表均返回 view_count；「有效浏览」= 商品存在且 on_shelf + 计数 UPDATE 成功，不要求最终 HTTP 响应写出与计数事务绑定。Contract 与 Task 兼容，转 APPROVED。）

## Problem

交付「商品浏览量计数」V1：用户浏览前台公开商品详情时，系统同步原子自增该商品的累计浏览量，并在详情（和列表）返回累计浏览量字段；并发浏览下计数不丢失；不存在/非上架商品不产生计数写入。

任务边界：事实来源为单一 MySQL（无 MQ、无异步、无 Redis 计数）；计数粒度为商品 SPU（`products.id`）；V1 默认总浏览量（不去重，去重明确 Out of Scope）；计数为同步写（无缓存/MQ 基础设施可复用）；不改动商品模块既有行为（除计数所需最小改动）。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层。证据：`internal/logic/product/product.go`、`internal/service/product.go`。
- VERIFIED：前台公开详情 `GET /products/:id` 链路为 `controller.product.Detail`（`internal/controller/product/product.go:25`）→ `service.Product().Detail(ctx, id)` → `logic.product.Detail`（`internal/logic/product/product.go:98`）→ `s.load(ctx, id, onlyOnShelf=true)`；公开、无 token，挂载于 `internal/cmd/routes_frontend.go:44`。`load` 对不存在或非 `on_shelf` 返回 `CodeProductNotFound`（4001/404），`onlyOnShelf=true` 时 SQL 强制 `WHERE status=1`。
- VERIFIED：`products` 表现有字段 `id/name/brand/category_id/price/main_image/detail/status/created_at/updated_at`，无浏览量字段；`updated_at` 为 `DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`（`internal/migrations/sql/20261001000002_products.up.sql:17`）——任何不显式赋值的 UPDATE 都会触发 `updated_at` 自动更新。对外 `Product` 结构（`api/product/v1/product.go:20`）无 `view_count`。
- VERIFIED：`Product` 结构被前台 `List/Detail` 与后台 `AdminList/AdminDetail` 及 `Create/Update/OnShelf/OffShelf` 响应共用（`ListRes.Items []*Product`、`DetailRes`/`AdminDetailRes` 内嵌 `Product`）；内部 `product` 结构 + `toProduct`（`internal/logic/product/product.go:63,470`）为详情与列表的公共组装点，`load`/`queryList` 均经 `toProduct` 出参。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ、无异步、无 cron。计数若走同步写会落在热读路径（前台详情），存在写放大考量（任务 `task.md` 已注明）。
- VERIFIED：迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000011_flash_sale`（`RESERVED`）。`internal/migrations/migrations_test.go` 硬编码 `latestMigrationVersion=20261001000011`、`businessTables`（22 张表）、`expectedSchema`（仅快照 13 张「核心」表：users/categories/admins/roles/permissions/admin_roles/role_permissions/orders/order_items/reviews/flash_sale 3 表/favorites，**不含 `products`**）。已合入的 baseline/products 等迁移为 `ACTIVE`，**禁止回改**。
- VERIFIED：错误码集中 `internal/codes/codes.go`：商品域 4000-4999（`4001` 商品不存在→404），通用域 1000-1999（`1001` 参数→400、`1000` 内部→500）。全项目无任何浏览量计数字段/表/模块。
- VERIFIED：原子自增/条件更新已有成熟模式：`internal/logic/inventory/inventory.go:95` 用 `INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)`、`:138` 用 `UPDATE ... SET quantity = quantity - ? WHERE ... AND quantity >= ?` + `RowsAffected`；`internal/logic/product/product.go:328`（`transition`）用「条件 UPDATE + `RowsAffected`」判定存在/非法迁移。计数自增可复用同一模式。
- VERIFIED：Registry 现状——`migrations.md` 最大 version = `20261001000011`（flash_sale，RESERVED）；`error-codes.md` 最大域序 = 12（flash-sale-v1，12000-12999）。下一 migration version = `20261001000012`。
- UNKNOWN：无阻塞性事实缺口。以下为待 Owner 确认的设计选择（非事实缺口）：数据模型（列 vs 独立表）、计数失败语义（fail-hard vs best-effort）、计数触发点与展示位置的范围。

## Recommendation

RECOMMENDATION：在 `products` 增加 `view_count BIGINT UNSIGNED NOT NULL DEFAULT 0` 列；前台公开详情 `GET /products/:id` 以「单条条件 UPDATE 原子自增 + `RowsAffected`」完成「可见性校验 + 计数 + 404 判定」三合一，计数失败走 fail-hard（500）；`view_count` 加入共享 `Product` 结构，详情与列表（前台+后台）均返回；后台/admin 详情/列表**不**触发计数；V1 为总浏览量（不去重）；无需新增错误码域（复用 4001/1001/1000）。

关键取舍（两个，需 Owner 确认）：

- **数据模型：`products.view_count` 列 vs 独立 `product_views` 表**。列方案最小、单条原子自增即可保证并发不丢、读取零额外 JOIN，但同步 UPDATE 落在热读路径（写放大）且必须显式处理 `updated_at` 自动更新副作用；独立表方案解耦主数据写放大、便于未来去重/明细/按天统计，但 V1 无需明细（Out of Scope）、读取需额外查询/JOIN、且若存明细行会产生无界表增长。推荐列方案（符合 `Design Impact = UPDATE`、改动最小、V1 总浏览量语义下独立表收益为零）。
- **计数失败语义：fail-hard vs best-effort**。fail-hard（计数失败→详情 500/1000）最简、不静默吞错、保证「详情成功 ⇔ 计数成功」；best-effort（计数失败→log 后仍返回详情）提升热读可用性，但会「详情 200 而计数悄悄丢失」，且违背 `AGENTS.md`「不静默忽略有意义的错误」。单 MySQL 下 SELECT 与 UPDATE 共享命运（DB 整体不可用时两者都 500），fail-hard 的可用性代价极小。推荐 fail-hard。

分项推荐：

1. **计数语义**：V1 总浏览量，每次前台详情访问 +1，不去重（用户/会话/IP 维度均 Out of Scope）。无「今日/累计」多口径。
2. **数据模型**：`ALTER TABLE products ADD COLUMN view_count BIGINT UNSIGNED NOT NULL DEFAULT 0`（新迁移，非回改 baseline）。`BIGINT UNSIGNED` 上限 ~1.8e19，实际不会溢出。不建索引（V1 无按浏览量排序/排行，Out of Scope）。
3. **计数触发点**：仅前台公开详情 `GET /products/:id` 对 `on_shelf` 商品计数；后台 `GET /admin/products/:id`、前台/后台列表均**不**计数（后台浏览不污染前台统计、列表不等于「浏览详情」）。
4. **展示位置**：`view_count` 加入共享 `Product` 结构，前台详情/列表、后台详情/列表、以及 Create/Update/OnShelf/OffShelf 响应均返回（只读字段，新建商品为 0）。不新增独立统计接口。
5. **计数写入与 404 语义（三合一原子）**：详情请求先执行 `UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ? AND status = 1` 并核对 `RowsAffected`：`=0` → 商品不存在或非上架 → 返回 4001/404 且无计数写入；`=1` → 计数已原子 +1，再复用既有 `load`（读回自增后的最新值）+ `ListByProduct` 组装详情。`view_count = view_count + 1` 恒改变值，故 `RowsAffected` 语义（受影响行）与匹配行一致，判定可靠。
6. **`updated_at` 保护**：自增 UPDATE 必须显式 `updated_at = updated_at`，否则 `ON UPDATE CURRENT_TIMESTAMP` 会把每次浏览都记为「商品最近更新」，破坏列表按 `updated_at` 排序等既有语义。
7. **计数失败语义**：fail-hard——计数 UPDATE 失败（DB 技术错误）→ 详情 500/1000，不吞错、不返回旧计数。
8. **错误码**：不新增错误码域。复用 `4001`（404）、`1001`（400）、`1000`（500）。

## Selected Design

Owner 已确认（2026-10-06）全部设计问题，均与 Recommendation 一致：

1. **数据模型（Q1）**：`products.view_count BIGINT UNSIGNED NOT NULL DEFAULT 0`（`ALTER TABLE` 新增，不建独立浏览记录表）。`BIGINT UNSIGNED` 上限 ~1.8e19，实际不溢出；不建索引。
2. **计数失败语义（Q2）**：fail-hard——计数 UPDATE 失败（DB 技术错误）→ 详情 500/1000，不吞错、不返回旧计数。
3. **触发点与展示（Q3）**：仅前台公开详情 `GET /products/:id` 访问 `on_shelf` 商品时 +1；前后台列表、后台详情均不触发计数。`view_count` 加入共享 `Product` 结构，详情与列表（前台+后台）均返回。
4. **有效浏览语义（Q4）**：商品存在且 `on_shelf`、计数 UPDATE 成功即视为一次有效浏览；不要求「最终 HTTP 响应成功写出」与计数事务绑定——计数 UPDATE 独立提交，后续 `load`/SKU 组装失败（500）时计数不回滚。

其余（原子自增三合一、`updated_at = updated_at` 保护、复用 4001/1001/1000、总浏览量不去重等）按 Recommendation 落实，不再重新展开。

## Interfaces and Data

### 数据模型（golang-migrate 新增单文件迁移，非回改 baseline）

迁移文件：`internal/migrations/sql/20261001000012_product_view_count.up.sql`（version = `max(20261001000011)+1`，见 Global Resource Reservation）。DDL 不使用 `IF NOT EXISTS`：

```sql
ALTER TABLE products ADD COLUMN view_count BIGINT UNSIGNED NOT NULL DEFAULT 0;
```

- 仅加列，不新增表，故 `migrations_test.go` 的 `businessTables`（仍 22 张）与 `expectedSchema`（不含 `products` 表）**均不变**；仅 `latestMigrationVersion` → `20261001000012`。
- 不建索引、不改 `updated_at` 定义、不改 FK。

### API 契约（`api/product/v1/product.go`，共享 `Product` 结构加字段）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `view_count` | int64 | 累计浏览量（只读；前台详情/列表、后台详情/列表及写接口响应均返回，新建为 0） |

- `Product` 结构新增 `ViewCount int64 \`json:"view_count"\``；内部 `product` 结构同步新增 `ViewCount`，`toProduct` 映射 `ViewCount: r.ViewCount`。`load`/`queryList` 经既有 `*product` Scan 自动填充，无需新增查询字段声明。

### 代码接口（复用既有入口，仅加计数逻辑）

- `logic.product.Detail`：在 `s.load` 之前新增「条件自增 UPDATE + `RowsAffected` 判定」，`=0` 返回 `CodeProductNotFound`（404）；`=1` 继续既有 `load` + `service.Sku().ListByProduct` 流程。`IProduct` 接口签名**不变**（`Detail(ctx, id) (*v1.DetailRes, error)`）。
- 计数 UPDATE 用 `g.DB().Exec(ctx, "UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ? AND status = ?", id, statusOnShelf)`（复用 `inventory` 的 raw Exec 原子模式），并核对 `RowsAffected`。
- 不改 `controller`、`service` 接口、`routes_frontend.go`/`routes_admin.go` 注册（路由不变）。

### 错误码

不新增。复用：`4001`（商品不存在/非上架 → 404）、`1001`（参数非法 → 400）、`1000`（内部错误 → 500）。

## Business Invariants

- INV-001（计数不丢失/并发正确）：并发访问同一 `on_shelf` 商品详情，`view_count` 最终值 = 初始值 + 成功详情请求数，由单条原子自增 `UPDATE ... SET view_count = view_count + 1` 保证（InnoDB 行锁串行化，无 lost update）。
- INV-002（仅 on_shelf 计数，404 无写入）：仅 `status=1` 的商品被计数；不存在或非 on_shelf 详情返回 404（4001）且 `RowsAffected=0`、无计数写入。
- INV-003（计数不改 updated_at）：计数自增不得改变 `products.updated_at`（UPDATE 显式 `updated_at = updated_at`），保持「最近更新时间」语义不变。
- INV-004（后台不计数）：后台 `GET /admin/products/:id`、前后台列表均不触发计数，前台统计不被后台浏览污染。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`products.view_count` 是浏览量的唯一事实来源（同步计数）；无 Redis/MQ/异步/跨系统事务。
- 详情成功 = 商品存在且 on_shelf，且 `view_count` 已原子 +1，响应中的 `view_count` 为自增后的最新值；详情 404 = 商品不存在或非 on_shelf，且无计数写入；详情 500 = 计数自增或读取遇 DB 技术错误（fail-hard，不吞错、不返回旧计数）。
- 有效浏览定义：商品存在且 `on_shelf`、计数 UPDATE 成功即构成一次有效浏览；计数 UPDATE 独立提交（不在 DB 事务内与后续 `load`/SKU 组装/响应写出绑定），最终 HTTP 响应写出失败不影响计数成立。
- 并发与原子性：计数为单表单行条件 UPDATE，无跨表事务；原子自增天然串行化并发写；「可见性校验 + 计数」合并为同一条条件 UPDATE，消除「先查再写」的 TOCTOU 窗口。
- 非幂等/重复语义：浏览计数天然非幂等（每次成功请求 +1）；客户端重试会重复计数，此为总浏览量语义下的预期行为（不去重）。
- 部分完成：无多步写，不存在部分完成；UPDATE 与后续 `load`/`ListByProduct` 分属独立语句，若 UPDATE 成功但后续 SKU 组装失败（500），计数已 +1 且不回滚——语义上「用户已浏览详情，计数成立」，可接受（计数为弱一致指标，非强一致业务账）。
- DB 技术错误统一 `CodeInternalError`（1000/500），不泄漏底层细节。

## Allowed / Forbidden Changes

允许：

- 新增迁移 `20261001000012_product_view_count.up.sql`（`ALTER TABLE products ADD COLUMN view_count ...`）；同步更新 `migrations_test.go` 的 `latestMigrationVersion` → `20261001000012`（`businessTables`/`expectedSchema` 不变）。
- 修改 `api/product/v1/product.go`（`Product` 加 `view_count`）、`internal/logic/product/product.go`（`product` 结构加 `ViewCount`、`toProduct` 映射、`Detail` 加计数 UPDATE）、对应测试。
- 复用 `4001`/`1001`/`1000`，不新增错误码。

禁止：

- 不回改已合入的 baseline/products/... 迁移（`20261001000002_products.up.sql` 保持不动）；不把 `view_count` 写进既有 CREATE TABLE。
- 不改动商品模块既有接口语义、路由、状态机、错误语义（`Detail`/`List`/`AdminDetail`/`AdminList` 的既有 404/可见性/排序行为不变）；不改变 `updated_at` 的既有语义。
- 不引入 Redis 缓存计数、MQ 异步刷库、独立 `product_views` 明细表、SKU 粒度计数、去重浏览、按天/周期统计、排行榜/排序、独立统计接口、限流/防刷。
- 不新增错误码域；不在 SQL 中拼接前端输入（`id` 走参数化绑定）。

## Verification Requirements

- INV-001 → MySQL + `-race`：并发 N 次访问同一 on_shelf 商品详情，断言最终 `view_count` = 初始值 + N（不丢失）（AC-002）。
- INV-002 → MySQL：访问不存在/off_shelf/draft 商品详情，断言 404/4001 且 `products.view_count` 无变化（AC-005）。
- INV-003 → MySQL：浏览后断言 `products.updated_at` 未被浏览改变（仅被真实 update/上下架改变）。
- INV-004 → MySQL：后台 admin 详情访问后断言 `view_count` 不增加。
- AC-001/AC-003 → MySQL：访问 on_shelf 详情，断言正常 200 且响应 `view_count` = 访问后最新累计值。
- AC-004 → MySQL：断言 `view_count` 出现在前台详情/列表与后台详情/列表响应中。
- AC-006 → MySQL：`latestMigrationVersion=20261001000012` 更新后 `go test ./internal/migrations/` 通过；迁移幂等（再次 Up 不重跑）；`products` 含 `view_count` 列（`bigint unsigned`、非空、默认 0）。
- AC-007 → 文档审查：`docs/design/product.md` 沉淀浏览量数据模型、计数语义、并发正确性边界与展示协议，与 APPROVED Contract、最终实现一致。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL 集成验证需容器就绪（`docker compose up -d`）。

## Global Resource Reservation

| 类别 | 派生值 | 依据 | 状态 |
| --- | --- | --- | --- |
| migration version | `20261001000012` | `max(已记录 version)=20261001000011`（flash_sale）+1 | 待 Owner 在 `develop` 写入 `RESERVED` |
| 错误码域 | 无需新增 | 复用 4001/1001/1000 | — |

Reservation Proposal（`WAITING_FOR_OWNER_ACTION`）：需 Owner 以「只改 Registry 的 commit」将以下 `RESERVED` 行写入 `develop`（Analyst 不直接改 Registry）：

- `migrations.md` 追加：`| 20261001000012 | product_view_count | product-view-count-v1 | RESERVED | products.view_count 浏览量计数字段 |`

（错误码域无需新增，不产生 Registry 变更。）

## Open Risks

- 写放大：计数为同步 UPDATE，落在前台详情热读路径，高并发下对 `products` 单行产生行锁竞争。V1 接受（单 MySQL、无异步基础设施）；若未来出现热点商品瓶颈，需引入异步/缓存计数（当前 Out of Scope）。
- `updated_at` 副作用已用显式 `updated_at = updated_at` 规避；若实现遗漏，会出现「浏览刷新更新时间」的隐性 bug（INV-003 已加测试锁定）。
- 计数非幂等：客户端重试会重复计数，总浏览量语义下可接受，但「精确 UV 统计」类需求需去重（Out of Scope）。

## Owner Decision Record

Owner 于 2026-10-06 批准（ACCEPT），适用范围为 product-view-count-v1 全部 Goal/AC：

1. **数据模型**：`products.view_count BIGINT UNSIGNED NOT NULL DEFAULT 0`，不建独立浏览记录表。
2. **计数失败语义**：fail-hard，浏览量计数失败则详情请求失败（500）。
3. **触发点/展示**：仅前台公开商品详情访问触发 +1；前后台列表与后台详情不触发；详情和列表均返回 `view_count`。
4. **有效浏览语义**：商品存在且处于上架状态、计数 UPDATE 成功即视为一次有效浏览，不要求将「最终 HTTP 响应成功写出」与计数事务绑定。

批准当前 Contract 推荐方案；进入实现前按 Workflow 先完成 Registry Reservation（migration `20261001000012`）。Design Impact = `UPDATE`，目标长期设计 `docs/design/product.md`（随 APPROVED 同步更新）。
