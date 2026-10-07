# Technical Contract

## Decision Status

APPROVED

## Problem

交付后端「推荐位（Recommendation）V1」核心闭环：管理员在后台管理推荐位（`recommend_positions`）与推荐商品关系（`recommend_items`），前台公开接口 `GET /recommendations/:code` 按稳定顺序只返回「启用推荐位中的可售商品」。商品下架不物理删除推荐关系，仅在前台查询时被过滤。

需要固化的设计选择集中在：推荐位标识（`code`）建模、删除 vs 禁用语义与级联处置、商品加入时的有效性边界（仅 on_shelf vs 任意存在商品）、前台「有效商品」定义、权限粒度、以及推荐位错误码域与 migration version 两个全局资源。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2，分层 `api/<module>/v1` → `internal/controller` → `internal/service`（接口 + Register）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层。最接近的单表参考为 `banner-v1`（`api/banner/v1/banner.go`、`internal/logic/banner/banner.go`、`internal/cmd/routes_admin.go` 的 `require("banner:*")`），两表参考为 `flash_sale`（`flash_sale_activities` + `flash_sale_activity_skus`，FK `ON DELETE CASCADE` + `uk_activity_sku` 唯一约束，见 `internal/migrations/sql/20261001000011_flash_sale.up.sql`）。
- VERIFIED：后台 RBAC 完整存在（`internal/middleware/auth.go` 的 `AdminAuth` + `RequirePermission`，`IsSuper` 放行、fail-closed 未命中 403；权限 code 幂等 seed 于 `internal/boot/seed.go` 的 `seedPermissionList`）。未认证 401、无权限 403，均不产生写入。后台读接口现状为「仅 AdminAuth、无读权限」（banner/product 一致）。
- VERIFIED：商品表 `products.status` 为 `0=draft / 1=on_shelf / 2=off_shelf`（`internal/migrations/sql/20261001000002_products.up.sql`）。`service.IProduct` 已暴露 `Exists(ctx,id)` 与 `GetByID(ctx,id)`（返回 `*v1.Product`，含字符串枚举 `Status`），可被推荐位逻辑复用做最小只读校验（跨模块只读，不修改商品模块行为）。
- VERIFIED：`products.status` 语义——`product.md` 明确「已绑定商品后分类被禁用：已有商品不受影响，前台仍按商品自身 `status` 过滤」，故前台「有效商品」无需校验分类启用；SKU 维度推荐超出本任务 Scope。
- VERIFIED：错误码集中在 `internal/codes/codes.go`（单一 const 块 + `codeTable`），域按 1000 对齐。`.agent/registry/error-codes.md` 已分配至域序 14（banner-v1，14000-14999，RESERVED）。推荐位为下一空闲域：**域序 15 → 15000-15999**（`domain_seq_next = max(14)+1`）。
- VERIFIED：迁移机制 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`。`.agent/registry/migrations.md` 最新 `20261001000014`（banners，RESERVED）。推荐位新增 1 个 migration：**version `20261001000016`**（`next = max(15)+1`，其中 `20261001000015` 已被并发任务 flash-sale-v3 预留），需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`（`TestSchemaStructureMatchesBaseline` 对结构严格等价校验，索引/列顺序敏感）。
- VERIFIED：事实来源为单一 MySQL，Redis 仅会话，无 MQ；推荐位为同步读写，无异步、无跨系统一致性。
- UNKNOWN：无。核心未知项均为需要 Owner 决定的业务语义（见 Recommendation 后的「Owner 需决定的问题」），不阻塞方案成型。

## Recommendation

RECOMMENDATION：沿 `banner-v1`（单表 CRUD/权限/排序语义）+ `flash_sale`（两表 FK 级联 + 唯一约束）的既有结构，建立「推荐位 + 推荐商品」两表模型；前台 `GET /recommendations/:code` 一次 JOIN `products` 返回推荐位元信息 + 可售商品快照。

具体设计建议（细节见 `Interfaces and Data`）：

1. **数据模型**：`recommend_positions`（`id`、唯一 `code`、`name`、`status`）+ `recommend_items`（`position_id` FK CASCADE、`product_id`、`sort`、唯一 `(position_id, product_id)`）。`code` 为稳定业务标识（唯一、创建后不可变），前台按 `code` 定位。
2. **删除/禁用**：禁用 = `status=0`（软下线，关系保留）；删除 = 物理删除并级联删除 `recommend_items`（FK `ON DELETE CASCADE`）。
3. **商品有效性**：加入推荐位时**仅校验商品存在**（复用 `CodeProductNotFound` 4001），不强制 on_shelf；前台查询仅过滤 `status=on_shelf`。这样既满足 T07「下架后保留关系、前台过滤」，又允许运营在下架/未上架阶段预配推荐位。
4. **前台「有效商品」**：仅 `products.status=on_shelf`，不校验分类启用/SKU。
5. **权限粒度**：`recommend:create` / `recommend:update` / `recommend:delete` / `recommend:item`（添加/移除/排序）四码；后台读沿用「仅 AdminAuth、无读权限」。
6. **错误码域**：15000-15999；migration：20261001000016。

关键取舍：**商品加入时「仅校验存在」而非「强制 on_shelf」**。这是本次最影响业务语义的选择——推荐位的价值在于运营可提前编排，商品上架状态是动态的，加入时的状态约束既无法阻止商品日后下架（仍需前台过滤兜底），又会阻挡「先排期、后上架」的正常运营流。代价是可能出现「加入了 draft 商品、前台暂时不可见」的情况，但由前台过滤保证不泄漏、后台可见可管理。

## Selected Design

Owner 已确认（2026-10-07）四项关键设计，作为本任务权威约束：

1. **商品加入边界**：添加推荐商品时仅校验「商品存在」（复用 `CodeProductNotFound` 4001），不限制 on_shelf；`draft`/`off_shelf` 商品可提前配置进推荐位。前台 `GET /recommendations/:code` 永远只返回 `products.status=on_shelf` 的商品。
2. **删除语义**：推荐位支持物理删除（`DELETE`，FK `ON DELETE CASCADE` 清空 `recommend_items`）；同时保留 `status=0` 禁用用于临时下线（关系保留、前台不返回、后台可见）。
3. **权限粒度**：`recommend:update` 管理推荐位本身（名称/状态，不改变 code）；`recommend:item` 管理推荐商品（添加/移除/调整排序）；另有 `recommend:create`、`recommend:delete`。后台读沿用「仅 AdminAuth、无读权限」。
4. **前台返回结构**：`GET /recommendations/:code` 返回推荐位元信息（`code`/`name`）+ 商品实时快照列表（JOIN `products`，快照 `product_id`/`name`/`main_image`/`price`/`sort`），仅 on_shelf，按 `sort,id` 稳定排序。

数据模型、API 契约、错误码域（15000-15999，5 个 code）、权限 seed 与全局资源见 `Interfaces and Data` 与 `Business Invariants`。

## Interfaces and Data

### 数据模型（migration `20261001000016_recommend.up.sql`）

```sql
CREATE TABLE recommend_positions (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code       VARCHAR(64)     NOT NULL,
  name       VARCHAR(64)     NOT NULL,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE recommend_items (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  position_id BIGINT UNSIGNED NOT NULL,
  product_id  BIGINT UNSIGNED NOT NULL,
  sort        INT             NOT NULL DEFAULT 0,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_position_product (position_id, product_id),
  KEY idx_position_sort (position_id, sort),
  CONSTRAINT fk_recommend_item_position FOREIGN KEY (position_id) REFERENCES recommend_positions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `code`：格式 `[a-z0-9][a-z0-9-]{0,63}`（小写字母/数字/连字符，1~64），唯一、创建后不可变。`product_id` 为软引用（无 FK，存在性由应用层经 `service.Product().Exists` 校验）。

### API 契约

前台（公开，无 token）：

- `GET /recommendations/:code` → 推荐位 `code`/`name` + `items[]`（仅 on_shelf 商品快照：`product_id`/`name`/`main_image`/`price`/`sort`），`ORDER BY sort ASC, id ASC` 稳定排序；推荐位不存在或禁用 → 返回空或 404（见失败语义）。

后台（`/admin`，`AdminAuth` + `RequirePermission`）：

- `GET /admin/recommend-positions`、`GET /admin/recommend-positions/:id`（仅 AdminAuth，全部状态，详情含 items）
- `POST /admin/recommend-positions`（`recommend:create`）
- `PUT /admin/recommend-positions/:id`（`recommend:update`，改 name/status，不改变 code）
- `DELETE /admin/recommend-positions/:id`（`recommend:delete`，物理删除 + 级联 items）
- `POST /admin/recommend-positions/:id/items`（`recommend:item`，添加商品，body `{product_id, sort?}`）
- `DELETE /admin/recommend-positions/:id/items/:product_id`（`recommend:item`，移除商品）
- `PUT /admin/recommend-positions/:id/items/sort`（`recommend:item`，调整排序，body 有序 `product_id` 列表）

### 错误码域（15000-15999，域序 15）

| code | 常量 | 语义 | HTTP |
| --- | --- | --- | --- |
| 15001 | CodeRecommendPositionNotFound | 推荐位不存在 | 404 |
| 15002 | CodeRecommendPositionCodeExists | 推荐位 code 已存在 | 409 |
| 15003 | CodeRecommendInvalidInput | 参数非法（code/name/sort/status/product_id） | 400 |
| 15004 | CodeRecommendItemNotFound | 推荐商品关系不存在 | 404 |
| 15005 | CodeRecommendItemDuplicate | 同一推荐位重复添加同一商品 | 409 |

复用：`4001`（商品不存在）、`1001`（参数格式兜底）、`1002`（401）、`1003`（403）、`1000`（500）。

### 权限 code（seed 登记 `internal/boot/seed.go`）

`recommend:create`（创建推荐位）、`recommend:update`（更新推荐位）、`recommend:delete`（删除推荐位）、`recommend:item`（添加/移除/调整排序）。

### 全局资源清单（待 Owner 批准后写入 Registry）

- 错误码域：`15000-15999`（域序 15，拥有方 recommendation-v1，语义「推荐位 Recommend」）
- migration version：`20261001000016`（title `recommend`，`recommend_positions` + `recommend_items`）

## Business Invariants

- INV-001（前台可见性与稳定排序）：`GET /recommendations/:code` 仅返回 `status=1` 推荐位中 `products.status=on_shelf` 的商品，按 `sort` 升序、同值按 `id` 升序；禁用推荐位/下架商品即时不出现在结果中。
- INV-002（同推荐位同商品唯一）：同一推荐位下同一商品至多一条关系，DB 唯一约束 `uk_position_product` 兜底，并发重复提交也被拒绝（15005）且不产生重复关系。
- INV-003（权限边界）：后台写操作需 `AdminAuth` + 对应权限 code（`IsSuper` 放行）；未认证 401、无权限 403，且不产生任何 DB 写入。
- INV-004（商品加入有效性）：添加推荐商品前仅校验「商品存在」（draft/on_shelf/off_shelf 均可加入），不存在被拒（复用 4001）且无写入；前台只展示 on_shelf。
- INV-005（下架不物理删除关系）：商品下架后 `recommend_items` 关系保留，前台查询过滤、后台查询仍可见。
- INV-006（删除级联）：物理删除推荐位后，其全部 `recommend_items` 一并删除（FK CASCADE），不留孤儿。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL（`recommend_positions` 主数据 + `recommend_items` 从属）；商品可售性在查询时经 JOIN `products.status` 判定，`products` 为只读引用。无 Redis 写、无 MQ、无异步、无跨系统一致性。
- 创建推荐位成功 = 单条 `INSERT recommend_positions`；`code` 冲突（1062）→ 15002。
- 添加商品成功 = 单条 `INSERT recommend_items`；校验商品存在（复用 4001）→ 写关系；重复（1062 on uk_position_product）→ 15005。
- 更新推荐位：先按 `id` 查存在性（不存在 → 15001 404），不依据 `RowsAffected` 判断存在性（幂等保存），`code` 不可变。
- 删除推荐位：条件删除 + 核对 `RowsAffected`（=0 → 15001 404）；级联删除 items 由 FK CASCADE 保证。
- 前台查询：推荐位不存在或 `status=0` → 返回空 items（`code:0`、`items:[]`，不区分「不存在」与「禁用」，避免向公开接口泄露内部状态）；DB 技术错误 → 1000（500）。
- 失败语义：未认证 401、无权限 403、不存在 404（15001/15004）、重复 409（15002/15005）、非法输入 400（15003），均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## Allowed / Forbidden Changes

- 允许：新增 `recommend_positions`/`recommend_items` 两表迁移与 `recommend` 模块（api/controller/service/logic/路由/seed）；新增 15000-15999 错误码域；新增 `docs/design/recommendation.md`；同步更新 `migrations_test.go`。
- 允许：推荐位逻辑经 `service.Product().Exists` / `GetByID` 做最小只读校验（不新增商品模块写行为）。
- 禁止：修改既有商品/分类/IAM/RBAC/banner 等模块的公开接口、错误语义或数据契约（除 seed 新增推荐位权限 code）。
- 禁止：前台接口引入 token 校验；禁止引入 Redis 缓存/MQ/异步；禁止 SKU 维度、跨推荐位聚合、推荐算法等超出 Scope 的能力。

## Verification Requirements

- INV-001 → 集成（MySQL）：预置启用/禁用推荐位 + 可售/下架商品，无 token 请求 `GET /recommendations/:code`，断言仅含启用位中的 on_shelf 商品、`sort,id` 稳定排序、禁用位返回空。
- INV-002 → 集成（MySQL）：重复添加被拒（15005）且关系不重复；并发提交由 `uk_position_product` 兜底。
- INV-003 → 集成（MySQL + 真实管理员）：未认证 401、无权限 403、均无写入（`RegisterAdminRoutes` + `AdminAuth`/`RequirePermission`）。
- INV-004 → 集成（MySQL）：添加不存在商品被拒（4001）且无写入；draft/off_shelf 商品可加入但前台不展示。
- INV-005 → 集成（MySQL）：商品下架后关系保留、前台不返回、后台仍可见。
- INV-006 → 集成（MySQL）：删除推荐位后 `recommend_items` 级联删除。
- AC-001/AC-009 → 迁移幂等 + `migrations_test.go`（`latestMigrationVersion`/`businessTables`/`expectedSchema`）+ `docs/design/recommendation.md` 与实现一致。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL/Redis 容器就绪。

## Open Risks

- 前台对「推荐位不存在」与「禁用」统一返回空（不泄露内部状态），牺牲了「未配置该 code」的可观测性；如需区分，属后续 Contract 修订。
- `code` 创建后不可变，若未来需要重命名 code，需 Contract 修订 + 数据迁移。
- 商品快照字段（name/main_image/price）依赖 `products` 现有列，若商品模块后续改列需同步检查前台 JOIN。

## Owner Decision Record

- 2026-10-07，Owner 确认四项设计选择：
  1. 商品加入边界 → 只校验商品存在；draft/off_shelf 也能提前配置；前台永远只展示 on_shelf。
  2. 删除语义 → 支持物理删除；FK CASCADE 清推荐关系；同时保留 disabled 用于临时下线。
  3. 权限 → `recommend:update` 管推荐位本身，`recommend:item` 管推荐商品。
  4. 前台接口 → 返回推荐位元信息 + 商品实时快照。
- 适用范围：本任务 recommendation-v1 全部实现；不改变既有模块公开接口与错误语义。
- 与 Task 兼容性：四项均落在 Task AC 允许范围内（AC-002 删除/禁用、AC-005 存在性校验、AC-008 权限、AC-006 前台查询），无需修改 Task。
