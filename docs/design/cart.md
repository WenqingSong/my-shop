# 购物车设计（Cart）

本文面向项目接手者，说明购物车（用户侧）的架构、数据模型、异常状态语义、用户隔离边界与错误码域。事实来源为 `cart-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

购物车回答「当前登录用户想把哪些 SKU 买多少」，承载用户侧购物车条目（`user_id` + `sku_id` 粒度）、数量、勾选状态与加购价格快照。购物车只归属当前登录用户；**不实现下单/结算、不扣减/预占库存**（结算与扣减延后到订单模块）。

边界：购物车是临时用户意图，**不作为 SKU 生命周期的强引用**（`sku_id` 采用软引用，不建 FK）；真正需要长期引用保护的是未来订单域。事实来源为单一 MySQL（`cart_items`）；无 Redis（购物车数据侧）、无 MQ、无异步、无缓存、无幂等键。

## 2. 数据模型

### 2.1 `cart_items`（购物车条目）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），取自 `Principal.UserID` |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `skus.id`（加购时应用层校验存在） |
| `quantity` | INT UNSIGNED | 非空，正整数，业务上限 999 |
| `price_snapshot` | INT UNSIGNED | 非空，加购时 `skus.price` 整数分快照（不锁价） |
| `selected` | TINYINT | 非空默认 1（`1`/`0`，API 出参 bool），勾选状态 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

唯一约束：`uk_user_sku (user_id, sku_id)`（同一用户同一 SKU 至多一条，兼作按用户查询索引）。

**软引用**：`user_id`/`sku_id` 均不建 FK。加购时校验 SKU 存在；SKU 被物理删除后条目保留为悬空引用，由查询识别并标识为不可购（`sku_deleted`）。

### 2.2 价格语义

`price_snapshot` 为加购时 `skus.price` 的整数分快照，**仅用于观察价格变化（`price_changed`），不代表锁价**。未来下单必须按订单模块规则重新确认成交价。名称/图片不存快照，查询时实时联查 `skus`/`products`。

## 3. 状态机与生命周期

购物车无状态机。条目生命周期为「加购 → 改数量/勾选 → 删除」；异常（下架/禁用/库存不足/SKU 删除）由查询时动态计算，不改变条目持久化状态：

- **加购**：仅当 SKU 存在、SKU `enabled`、商品 `on_shelf` 才成功；重复添加同 `(user_id, sku_id)` 原子累加数量（上限 999，超限拒绝）。
- **删除**：物理删除条目；`WHERE id AND user_id` 过滤，未命中 404。
- **异常标识**：不自动清理条目、不自动调整数量；由列表查询动态返回 `available`/`unavailable_reason`/`insufficient`/`price_changed`。

## 4. 业务不变量

- INV-001（归属与隔离）：每个购物车条目只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，跨用户查看/修改/删除返回 404 且无写入。
- INV-002（用户+SKU 唯一 + 原子累加）：同一用户对同一 SKU 至多一条，由 `uk_user_sku` 兜底；重复添加原子累加，并发重复加购不丢失更新（禁止无锁「先 SELECT 再 UPDATE」）；累加后超上限拒绝且不静默截断。
- INV-003（数量合法）：`quantity` 为正整数且 ≤ 999；0/负数/非整数/超上限返回 400 且原值不变。
- INV-004（可购校验）：加购仅当 SKU 存在、enabled、商品 on_shelf 才成功；否则稳定错误且无写入。
- INV-005（异常可观察）：列表动态标识 SKU 不存在（`sku_deleted`）、下架/禁用（`available`/`unavailable_reason`）、库存不足（`insufficient`）；不清理、不调整。
- INV-006（价格快照一致）：`price_snapshot` 为加购时 `skus.price` 快照；列表给出 `current_price` 与 `price_changed`。
- INV-007（软引用容忍 SKU 删除）：`sku_id` 无 FK，SKU 删除后条目保留并标识为 `sku_deleted`；不阻止 SKU 删除。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`cart_items`（条目与勾选状态，事实来源）；`skus`/`products`/`inventories` 只读引用（存在性/状态/价格/库存判定）。
- 成功语义：添加成功 = 条目持久化（新增或累加）；改数量/勾选/删除成功 = 变更持久化；列表成功 = 返回时点本人条目及实时异常标识。
- 原子性与并发：`cart_items` 单表写，无跨表事务。重复添加用原子累加：推荐「事务内先 `INSERT`，命中 1062 后 `SELECT ... FOR UPDATE` 校验 `cur + N ≤ 999` 再 `UPDATE`」，或 `INSERT ... ON DUPLICATE KEY UPDATE` 配合行锁；**禁止无锁「先 SELECT 再 UPDATE」**。改数量/勾选/删除用 `WHERE id AND user_id` + `RowsAffected`，未命中 404。
- SKU 物理删除：条目保留，查询 `LEFT JOIN` 识别 SKU 缺失 → `sku_deleted`，`current_price`/`stock`/`product_id` 为 null。
- 非幂等：累加/改数量语义下重试会重复累加，V1 不做业务幂等键（订单模块补）。
- 失败语义：认证 401、校验 400/404/409 均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## 6. 安全与权限边界

- 前台受保护路由挂载 `internal/cmd/routes_frontend.go` 的 `Auth` 分组（仅登录用户可访问）：

| 方法/路径 | 说明 |
| --- | --- |
| `GET /cart` | 当前用户全部条目（无分页） |
| `POST /cart/items` | 添加 SKU（`sku_id` 必填，`quantity` 默认 1） |
| `PUT /cart/items/:id` | 修改数量 |
| `PUT /cart/items/:id/selected` | 勾选/取消 |
| `DELETE /cart/items/:id` | 删除 |

- 身份信任：不信任请求自带身份；`Principal.UserID` 是唯一身份来源，所有操作经 `user_id` 过滤。
- 越权语义：不存在与越权统一 404（防枚举），不泄露他人条目存在性。
- 无管理员权限体系参与：购物车仅用户域，不涉及 `AdminAuth`/`RequirePermission`。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 8001 | CART_ITEM_NOT_FOUND（条目不存在或不属于当前用户） | 404 |
| 8002 | CART_INVALID_QUANTITY（数量非正整数或超上限） | 400 |
| 8003 | CART_SKU_UNAVAILABLE（商品下架或 SKU 禁用，不可加购） | 409 |

复用：`5001`（SKU 不存在，404）、`1001`（400）、`1002`（401）。购物车不新增 `5006`（SKU 删除不受购物车引用约束）。

## 8. 跨模块关系

- `cart_items.sku_id` → `skus.id`（软引用，无 FK）：加购时经 `ISku.GetByID` 校验存在与 `enabled`；SKU 删除后条目悬空，由查询标识 `sku_deleted`。
- `cart_items.user_id` → `users.id`（软引用，无 FK）：取自 `Principal.UserID`。
- 加购校验需读取商品状态：经 `IProduct.GetByID`（新增只读方法，返回任意 `status`）判定 `on_shelf`。
- 库存不足判定：经 `IInventory.Get` 读取当前库存（无记录 = 0），只读、不扣减。
- 建表经 golang-migrate 迁移（`20261001000006_cart_items.up.sql`），见 `migration.md`。
- 价格：`price_snapshot` 快照自 `skus.price`；`products.price` 与购物车无关（购物车为 SKU 粒度）。

## 9. Deferred / 已知留白

- **下单/结算延后**：勾选状态仅表示用户选择意图，不触发下单；结算时如何取价、何时真正扣减库存均由未来订单模块定义（快照不锁价，成交价以下单时订单模块规则为准）。
- **SKU 删除后的条目**：保留为不可购条目（`sku_deleted`），用户自行移除；不提供批量清理不可购条目（「购物车上限/批量操作」Out of Scope）。
- **幂等键缺失**：累加/改数量无去重，客户端重试会重复累加；订单模块接入时基于真实请求标识补幂等。
- **列表无分页**：V1 返回全部条目，量大时由后续任务加分页。
