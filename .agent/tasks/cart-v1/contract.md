# Technical Contract

## Decision Status

APPROVED

（2026-10-04 Owner 确认全部 5 项决定：加购价格快照（字段名 `price_snapshot`，不锁价）、异常只标识不清理不调整、重复添加原子累加上限 999、越权/不存在统一 404、购物车对 SKU 采用软引用（不建 FK、不加 5006）。其中第 5 项否决了 Analyst 原「FK RESTRICT」推荐，改为软引用。Contract 与 Task 兼容，转 APPROVED。）

## Problem

交付用户侧购物车 V1：登录用户可将有效 SKU 加入购物车、修改数量、删除条目、查看列表、勾选/取消勾选；购物车只归属当前登录用户；列表能标识「SKU 不存在 / SKU 禁用 / 商品下架 / 价格变化 / 库存不足」等可观察状态；为后续订单/结算预留稳定的购物车数据与查询语义（本任务不实现下单、不扣减库存）。

任务边界：购物车为单一 MySQL 事实来源（无 MQ、无异步、无缓存、无幂等键）；购物车属于临时用户意图，**不作为 SKU 生命周期的强引用**（`sku_id` 采用软引用，不建 FK）；不改动 IAM/商品/SKU/库存/分类模块既有行为（除购物车引用所需的最小只读查询）；不实现前端页面。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3（Go 1.23），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层（`internal/logic/logic.go`、`internal/service/*.go`）。
- VERIFIED：用户身份经 `internal/middleware/auth.go` 的 `Auth` 解析 `type=user` token 并注入 `Principal{UserID, Sid}`；controller 经 `middleware.PrincipalFromContext(ctx)` 读取（`internal/controller/iam/iam.go:32` 示范）。前台受保护路由挂载于 `internal/cmd/routes_frontend.go` 的 `user.Middleware(middleware.Auth)` 分组（当前仅 `/me`）；购物车路由应挂此分组下。
- VERIFIED：`products` 表（`status` TINYINT 0=draft/1=on_shelf/2=off_shelf、`price` INT UNSIGNED 分、`name`/`main_image`）与 `skus` 表（`product_id` FK→products RESTRICT、`price` INT UNSIGNED、`status` 1=enabled/0=disabled、`name`）已存在（`20261001000002_products.up.sql`、`...003_skus.up.sql`）。`products.price` 与 `skus.price` 并存且独立，购物车为 SKU 粒度，有效单价取 `skus.price`。
- VERIFIED：`inventories`（`sku_id` 唯一、`quantity` INT UNSIGNED，无记录=0）已存在（`...004_inventory.up.sql`）；`IInventory.Get(ctx, skuID)` 返回当前库存（无记录=0，SKU 不存在返回 5001）；库存扣减语义已明确留给订单模块，本任务只读库存数量。
- VERIFIED：`ISku`（`internal/service/sku.go`）有 `Exists`/`ListByProduct` 但**无「按 id 取单个 SKU 全字段」的只读方法**；`IProduct` 有 `Exists`/`Detail`（前台 `Detail` 仅 on_shelf，否则 404，无法用于读取 off_shelf 商品状态）但**无「按 id 取商品全状态」的只读方法**。购物车需为二者新增最小只读方法（见 Interfaces and Data）。
- VERIFIED：错误码集中 `internal/codes/codes.go`：通用 1000-1005、IAM 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999；**购物车域 7000-7999 空闲**。统一响应 `{code,message,data}`，客户端靠 code 判型，HTTP 状态由 `codes.HTTPStatus` 映射（`internal/middleware/response.go`）。
- VERIFIED：迁移机制 golang-migrate v4，当前最新版本 `20261001000004_inventory`；`serve` 不自动执行（`my-shop migrate up`）。`internal/migrations/migrations_test.go` 硬编码 `latestMigrationVersion = 20261001000004` 与 `businessTables`（按 FK 依赖倒序 drop），新增 `cart_items` 必须同步更新。
- VERIFIED：SKU 删除为物理删除，命中 FK 1451（存在库存记录/流水）→ `5005 SKU_HAS_INVENTORY`（409）。本任务对 `cart_items.sku_id` 采用软引用（不建 FK），因此**不改变 SKU 删除行为**，购物车条目不参与 SKU 删除的 FK 保护。
- VERIFIED：事实来源单一 MySQL；Redis 仅用于会话；无 MQ、无订单模块。`users` 表存在且无删除接口。
- UNKNOWN：无阻塞性 UNKNOWN。数量上限具体值（999）与异常标识字段命名均为待固化设计（已由 Owner 确认，见 Selected Design）。

## Recommendation

RECOMMENDATION：单表 `cart_items`（`user_id`+`sku_id` 联合唯一），`sku_id`/`user_id` 采用软引用（不建 FK）；价格采用「加购快照价 `price_snapshot` + 查询时对比当前价」；下架/禁用/库存不足/SKU 不存在四类异常在查询时动态计算为条目级标识，不自动清理、不自动调整数量；重复添加同 SKU 用数据库原子累加（上限 999，超限拒绝）；删除/改数量/勾选按 `id AND user_id` 过滤，未命中统一 404（防枚举）。

关键取舍：**「快照价 + 查询时动态对比」而非「纯实时价」**——只有快照价才能在价格变化时给出可观察的 `price_changed` 标识，满足 Goal 对异常「可观察状态标识」的要求；纯实时价会让价格静默变化、无法标识。代价是 `cart_items` 需持久化一个 `price_snapshot` 列，查询需联查当前 `skus.price` 对比。

分项推荐：

1. **数据模型**：`cart_items` 表 `id`、`user_id`、`sku_id`、`quantity INT UNSIGNED`、`price_snapshot INT UNSIGNED`（加购时 `skus.price` 快照）、`selected TINYINT NOT NULL DEFAULT 1`、`created_at`/`updated_at`；`UNIQUE uk_user_sku (user_id, sku_id)` 兜底「用户+SKU 最多一条」。**不建 FK**（软引用，SKU 删除后条目允许成为不可购条目）。不存储名称/图片快照（查询时实时联查）。
2. **价格语义**：快照价模型。`price_snapshot` 记录加购时 `skus.price`，**仅用于观察价格变化，不锁价**；未来下单按订单模块规则重新确认成交价。列表返回 `price_snapshot` + `current_price`（实时）+ `price_changed`。
3. **异常标识形态**：查询时动态计算。条目级 `available`（SKU 存在且 enabled 且商品 on_shelf）、`unavailable_reason`（`""`｜`"sku_deleted"`｜`"disabled"`｜`"off_shelf"`）、`stock`（实时库存，无记录=0）、`insufficient`（`quantity > stock`）。不自动清理不可购条目、不自动调整数量。
4. **重复添加与数量边界**：同一 `(user_id, sku_id)` 重复添加累加数量；`quantity` 正整数且 ≤ `maxQuantity=999`；累加后超上限明确拒绝（400），不静默截断；`quantity=0` 是参数错误（400），不等价删除。**并发下必须用数据库原子累加（如 `INSERT ... ON DUPLICATE KEY UPDATE` 或事务内 `SELECT ... FOR UPDATE` + 更新），禁止无锁「先 SELECT 再 UPDATE」导致丢失更新；`uk_user_sku` 为最终兜底。**
5. **删除/越权语义**：删除、改数量、勾选均 `WHERE id=? AND user_id=?`，`RowsAffected=0` 统一返回 `7001 CartItemNotFound`（404），同时覆盖「不存在」「已删除」「属于他人」，不泄露他人数据存在性。列表只返回 `user_id` 过滤后的本人条目。
6. **SKU/商品删除对购物车的影响**：`cart_items.sku_id` 软引用（无 FK）。SKU 物理删除后，已有购物车条目保留但成为不可购条目，查询时识别 SKU 不存在并返回 `unavailable_reason="sku_deleted"`，用户可自行移除。**不新增 `5006 SKU_HAS_CART_ITEMS`，不改动 SKU 删除逻辑。**
7. **路由**：前台受保护（`Auth`）分组下 `GET /cart`、`POST /cart/items`、`PUT /cart/items/:id`、`PUT /cart/items/:id/selected`、`DELETE /cart/items/:id`。
8. **错误码**：`7001 CartItemNotFound`(404)、`7002 CartInvalidQuantity`(400)、`7003 CartSkuUnavailable`(409，商品下架或 SKU 禁用不可加购)；SKU 不存在复用 `5001`(404)。

## Selected Design

Owner 已确认（2026-10-04）全部设计问题：

1. **价格快照（Q2）**：采用「加购价格快照 + 查询时当前价格对比」方案。快照字段明确命名为 `price_snapshot`，避免与当前 SKU 售价混淆；快照仅用于观察价格变化，**不代表锁价**，未来下单仍按订单模块规则重新确认成交价格。
2. **异常语义（Q3）**：只标识、不自动清理、不自动调整数量。SPU 下架、SKU disabled、库存不足时保留条目，由查询结果返回对应不可购状态；价格变化单独返回 `price_changed`，不等同于不可购。
3. **重复添加与并发（Q4）**：同一 `(user_id, sku_id)` 重复添加累加数量，`quantity` 最大 999；超上限明确拒绝、不静默截断。并发不变量：重复加购必须使用数据库原子累加/原子 upsert，禁止无锁「先 SELECT quantity 再 UPDATE」；联合唯一约束 `uk_user_sku` 作为最终兜底。
4. **用户隔离（Q5）**：购物车属用户自有资源。详情修改、删除、勾选按 `id AND Principal.UserID` 定位；不存在与越权统一返回 404，不泄露他人条目是否存在。
5. **SKU 删除交互（Q6）**：不接受「购物车条目通过 FK RESTRICT 阻止 SKU 物理删除」。购物车是临时用户意图，不作为 SKU 生命周期强引用；真正需要长期引用保护的是未来订单域。`cart_items.sku_id` 采用软引用（不建 FK）；SKU 删除后已有条目允许成为不可购条目，查询识别 SKU 不存在并返回稳定的 unavailable/not-found 状态，用户可自行移除。不新增 `5006 SKU_HAS_CART_ITEMS`。

其余（路由、错误码域 7000-7999、加购校验等）按 Recommendation 落实，不再重新展开。

## Interfaces and Data

### 数据表（golang-migrate 新增单文件迁移）

迁移文件：`internal/migrations/sql/20261001000005_cart_items.up.sql`（version 紧随 inventory `...004`；落地时若被占用取下一个更大 14 位时间戳）。DDL 不用 `IF NOT EXISTS`。

```sql
CREATE TABLE cart_items (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        BIGINT UNSIGNED NOT NULL,
  sku_id         BIGINT UNSIGNED NOT NULL,
  quantity       INT UNSIGNED    NOT NULL,
  price_snapshot INT UNSIGNED    NOT NULL,
  selected       TINYINT         NOT NULL DEFAULT 1,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_sku (user_id, sku_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `quantity` 正整数，业务上限 `maxQuantity = 999`；`price_snapshot` 整数分（加购时 `skus.price` 快照，不锁价）；`selected` DB TINYINT（1/0），API 出参 bool。
- **软引用**：`user_id`/`sku_id` 均不建 FK。加购时应用层校验 SKU 存在，`sku_id` 写入后有效；SKU 被物理删除后条目保留为悬空引用，由查询识别。
- `uk_user_sku (user_id, sku_id)` 既兜底「同一用户同一 SKU 至多一条」，又作为按用户查询的索引（`user_id` 前导列）。
- 同步更新 `internal/migrations/migrations_test.go`：`latestMigrationVersion` → `20261001000005`；`businessTables` 最前方插入 `"cart_items"`（无 FK 依赖，但为稳妥仍置于最前 drop）。

### 列表响应字段（`api/cart/v1`）

`CartItem`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | int64 | 条目 id |
| `sku_id` | int64 | SKU id |
| `quantity` | int64 | 数量 |
| `selected` | bool | 勾选状态 |
| `price_snapshot` | int64 | 加购时 `skus.price` 快照（整数分，不锁价） |
| `product_id` | *int64 | 所属商品 id；SKU 被删除时为 null |
| `product_name` | string | 商品名；SKU 被删除时为空 |
| `product_main_image` | string | 商品主图；SKU 被删除时为空 |
| `sku_name` | string | SKU 名；SKU 被删除时为空 |
| `current_price` | *int64 | 当前 `skus.price`；SKU 被删除时为 null |
| `price_changed` | bool | `current_price != nil && current_price != price_snapshot`（价格变化，独立于不可购） |
| `available` | bool | SKU 存在 && enabled && 商品 on_shelf |
| `unavailable_reason` | string | `""`（可用）｜`"sku_deleted"`（SKU 不存在）｜`"disabled"`（SKU 禁用）｜`"off_shelf"`（商品下架） |
| `stock` | *int64 | 当前库存（无记录=0）；SKU 被删除时为 null |
| `insufficient` | bool | `available && stock != nil && quantity > stock`（库存不足） |
| `created_at` / `updated_at` | time | 时间戳 |

### 路由（`internal/cmd/routes_frontend.go`，`Auth` 分组）

| 方法/路径 | 说明 |
| --- | --- |
| `GET /cart` | 当前用户全部条目（无分页） |
| `POST /cart/items` | 添加 SKU（body：`sku_id` 必填，`quantity` 可选默认 1） |
| `PUT /cart/items/:id` | 修改数量（body：`quantity`） |
| `PUT /cart/items/:id/selected` | 勾选/取消（body：`selected` bool） |
| `DELETE /cart/items/:id` | 删除 |

### 代码接口

- 新增 `api/cart/v1`、`internal/controller/cart`、`internal/service/cart.go`（`ICart` + `RegisterCart`）、`internal/logic/cart`（`init()` 注册）；`internal/logic/logic.go` 注册 cart 包。
- `ICart` 接口：`List(ctx, userID) (*v1.ListRes, error)`；`Add(ctx, userID, req) (*v1.AddRes, error)`；`UpdateQuantity(ctx, userID, itemID, quantity) (*v1.UpdateQuantityRes, error)`；`UpdateSelected(ctx, userID, itemID, selected) (*v1.UpdateSelectedRes, error)`；`Delete(ctx, userID, itemID) error`。
- Controller 经 `middleware.PrincipalFromContext(ctx)` 取 `UserID` 传入 service；所有操作以 `UserID` 为唯一身份来源。
- 跨模块最小只读方法（不改既有方法语义）：
  - `ISku` 新增 `GetByID(ctx, id) (*v1.Sku, error)`（返回全字段，含 `status`，不存在返回 nil）。
  - `IProduct` 新增 `GetByID(ctx, id) (*v1.Product, error)`（返回全字段、含任意 `status`，不存在返回 nil；用于读取 off_shelf 商品状态）。
  - `IInventory.Get` 已满足库存读取，无需改动。
- 加购流程：校验 `quantity`（1..999）→ `ISku.GetByID` 判存在（5001）与 enabled（7003）→ `IProduct.GetByID` 判 on_shelf（7003）→ 取 `price_snapshot = sku.price` → 原子累加写入（见 Failure and Consistency Semantics）。

### 错误码新增（`internal/codes/codes.go`）

| code | 语义 | HTTP |
| --- | --- | --- |
| 7001 | CART_ITEM_NOT_FOUND（条目不存在或不属于当前用户） | 404 |
| 7002 | CART_INVALID_QUANTITY（数量非正整数或超上限） | 400 |
| 7003 | CART_SKU_UNAVAILABLE（商品下架或 SKU 禁用，不可加购） | 409 |

复用：`5001`（SKU 不存在 → 404）、`1001`（参数错误 → 400）、`1002`（401）。**不新增 `5006`**。

## Business Invariants

- INV-001（归属与隔离）：每个购物车条目只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，跨用户查看/修改/删除返回 404 且无写入。
- INV-002（用户+SKU 唯一 + 原子累加）：同一用户对同一 SKU 至多一条条目，由 `uk_user_sku` 唯一约束兜底；重复添加原子累加，**并发重复加购不丢失更新（禁止无锁「先 SELECT 再 UPDATE」）**；累加后超上限拒绝且不静默截断。
- INV-003（数量合法）：`quantity` 为正整数且 ≤ 999；0/负数/非整数/超上限返回 400 且原值不变。
- INV-004（可购校验）：加购仅当 SKU 存在、SKU enabled、商品 on_shelf 才成功；任一不满足返回稳定错误且无写入。
- INV-005（异常可观察）：列表对每条目动态标识 SKU 不存在（`sku_deleted`）、下架/禁用（`available`/`unavailable_reason`）、库存不足（`insufficient`）；不自动清理条目、不自动调整数量。
- INV-006（价格快照一致）：`cart_items.price_snapshot` 为加购时 `skus.price` 的整数分快照（不锁价）；列表同时给出 `current_price` 与 `price_changed`，价格变化可被观察。
- INV-007（软引用容忍 SKU 删除）：`cart_items.sku_id` 无 FK，SKU 被物理删除后条目保留并标识为 `sku_deleted`（不可购），用户可移除；不阻止 SKU 删除、不产生 5006。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`cart_items`（购物车条目与勾选状态，事实来源）；`skus`/`products`/`inventories` 为只读引用（存在性/状态/价格/库存判定，非事实来源）。
- 成功语义：添加成功代表条目已持久化（新增或累加）；改数量/勾选/删除成功代表变更已持久化；列表成功代表返回时点本人全部条目及其实时计算的异常标识。
- 原子性与并发：`cart_items` 单表写，无跨表事务。**重复添加用原子累加**：推荐「事务内先 `INSERT`，命中 1062 后 `SELECT ... FOR UPDATE` 读当前量、校验 `cur + N ≤ 999` 后 `UPDATE quantity = cur + N`」（行锁下校验上限，超限回滚 7002）；或 `INSERT ... ON DUPLICATE KEY UPDATE` 完成原子累加、上限校验配合行锁进行。**禁止无锁「先 SELECT 再 UPDATE」**。`uk_user_sku` 唯一约束兜底并发初次插入。改数量/勾选/删除用 `WHERE id=? AND user_id=?` + 核对 `RowsAffected`，未命中返回 404。
- 校验失败（SKU 不存在 5001、商品下架/SKU 禁用 7003、数量非法 7002、未认证 401）：写入前拒绝且无写入。
- 可购校验与写入之间存在 TOCTOU 窗口（校验通过后 SKU/商品被下架）：允许——列表在下一次查询即标识为不可购，本任务不做加购时刻的强一致锁定。
- SKU 物理删除：`cart_items.sku_id` 软引用，条目保留；查询 `LEFT JOIN` 识别 SKU 缺失 → `available=false`、`unavailable_reason="sku_deleted"`、`current_price`/`stock`/`product_id` 为 null，用户可自行移除。
- 非幂等：累加/改数量语义下，客户端重试同一请求会重复累加（与 `inventory-v1` 的 `Deduct` 同立场，V1 不做业务幂等键，Out of Scope 明确排除）。
- DB 技术错误统一 `CodeInternalError` 500，不泄漏底层细节。
- 无 Redis（购物车数据侧）、无 MQ、无异步、无跨系统事务。

## Allowed / Forbidden Changes

允许：
- 新增迁移 `20261001000005_cart_items.up.sql`；同步更新 `migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`（`cart_items` 置最前）。
- 新增 `api/cart/v1`、`internal/controller/cart`、`internal/service/cart.go`、`internal/logic/cart`、错误码 7001/7002/7003、`routes_frontend.go` 购物车路由。
- 为 `ISku` 新增 `GetByID`、为 `IProduct` 新增 `GetByID`（最小只读扩展，不改既有方法语义）。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 不改动已合入的 baseline/products/skus/inventory 迁移及迁移机制本身；不回退到 `boot.go` 建表。
- 不改动 `skus`/`products`/`inventories` 表结构；不改动 IAM/商品/SKU/库存/分类既有接口语义（**SKU 删除逻辑完全不动**）。
- 不引入订单/结算、库存扣减/预占、MQ、缓存、幂等键、补偿、购物车上限、批量操作、匿名购物车合并。
- 不为 `cart_items.sku_id`/`user_id` 建 FK（软引用）；不新增 `5006` 错误码。
- 不自动清理不可购条目、不自动调整数量、不在加购时锁定或扣减库存。
- 不 fail-open 放行未认证请求；不在 SQL 中拼接前端输入；不把 `user_id`/`sku_id` 信任为客户端可控身份（一律以 `Principal.UserID` 为准）。

## Verification Requirements

- INV-001 → MySQL + Redis：用户 B 查/改/删用户 A 的条目，断言 404 且查库无写入；两个真实用户注册登录验证。
- INV-002 → MySQL + `-race`：同用户重复添加同 SKU，断言仅一条记录且数量累加；**并发重复加购（多 goroutine 同 `(user_id, sku_id)`）断言最终数量 = 各次累加之和（无丢失更新）**，且 `uk_user_sku` 下仍唯一。
- INV-003 → MySQL：数量 0/负数/非整数/超上限（含累加超限）断言 400 且库中数量不变。
- INV-004 → MySQL：SKU 不存在 5001、商品下架/SKU 禁用 7003，断言无写入。
- INV-005 → MySQL：加购后经后台下架商品/禁用 SKU/扣减库存使 `quantity > stock`，再查列表断言 `available`/`unavailable_reason`/`insufficient` 正确，且条目仍在（未清理）、数量未变。
- INV-006 → MySQL：加购后经后台改价，断言 `price_snapshot` 不变、`current_price`=新价、`price_changed=true`。
- INV-007 → MySQL：加购后删除该 SKU，断言 SKU 删除成功（无 FK 阻塞），购物车条目仍在，列表返回 `available=false`、`unavailable_reason="sku_deleted"`、`current_price`/`stock` 为 null。
- AC-006 → MySQL：勾选/取消后断言 `selected` 持久化且列表可见。
- 迁移结构 → MySQL：`latestMigrationVersion`/`businessTables` 更新后 `go test ./internal/migrations/` 通过。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL/Redis 集成验证需容器就绪（`docker compose up -d`）。

## Open Risks

- 软引用悬空：SKU 物理删除后 `cart_items` 保留悬空 `sku_id`，由查询识别并标识 `sku_deleted`；应用层必须始终容忍 SKU 缺失（`LEFT JOIN`），不得因联查失败报 500。
- 非幂等：累加/改数量语义下客户端重试会重复累加；V1 无幂等键（Out of Scope），订单模块接入时再补。
- 列表无分页：V1 返回全部条目（任务未要求分页，「购物车上限」Out of Scope）；条目多时后续任务加分页。
- 加购校验与写入存在 TOCTOU 窗口，允许「校验后下架」在下一查询才体现为不可购。

## Owner Decision Record

Owner 于 2026-10-04 确认：

1. **价格快照**：采用「加购价格快照 + 查询时当前价格对比」，字段名 `price_snapshot`；快照仅用于观察价格变化，不代表锁价，未来下单按订单模块规则重新确认成交价。
2. **异常语义**：只标识、不自动清理、不自动调整数量；下架/禁用/库存不足保留条目并返回不可购状态；价格变化单独返回 `price_changed`。
3. **重复添加与并发**：`(user_id, sku_id)` 重复添加累加，`quantity` 上限 999，超限明确拒绝不截断；重复加购必须用数据库原子累加/原子 upsert，禁止无锁「先 SELECT 再 UPDATE」；`uk_user_sku` 最终兜底。
4. **用户隔离**：改/删/勾选按 `id AND Principal.UserID` 定位；不存在与越权统一 404，不泄露他人条目存在性。
5. **SKU 删除交互（否决原 FK RESTRICT 推荐）**：购物车不作为 SKU 生命周期强引用，`cart_items.sku_id` 采用软引用（不建 FK）；SKU 删除后条目成为不可购条目，查询识别 SKU 不存在并返回 `sku_deleted`；不新增 `5006`，不改动 SKU 删除逻辑。

适用范围：`cart-v1` 本任务。Design Impact = `NEW`，目标长期设计 `docs/design/cart.md`（已随 APPROVED 产出）。

待办：
- [x] 修正 `task.md` 声明 `Design Impact = NEW`（2026-10-04）。
- [x] 记录 Owner 决定并转 `APPROVED`（2026-10-04）。
- [x] 基于 APPROVED Contract 产出 `docs/design/cart.md`（2026-10-04）。
