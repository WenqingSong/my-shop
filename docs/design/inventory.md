# 库存设计（Inventory）

本文面向项目接手者，说明「普通库存」的架构、数据模型、并发/一致性与边界。事实来源为 `inventory-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

在 SKU 之上，基于 `sku_id` 建立独立的「普通库存」模型：库存查询、初始化/增加、条件扣减（充足才成功）、防负库存、库存变更流水。核心扣减逻辑封装为可复用 service 方法（`Deduct`），供未来订单模块复用。

边界：不引入订单/购物车、不引入 MQ/幂等键/补偿、不做库存锁定/预占、不做多仓/批次、不做缓存库存、不改前端；事实来源为单一 MySQL。

## 2. 数据模型

### 2.1 `inventories`（库存主数据，1:1 `sku_id`）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `sku_id` | BIGINT UNSIGNED | 非空，唯一 `uk_sku_id`，FK → `skus.id` `ON DELETE RESTRICT` |
| `quantity` | INT UNSIGNED | 非空默认 0，整数个件，`≥0` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

**无记录 = `quantity 0`**：不自动建 0 记录，首次 `increase` 才建立记录（惰性建模，不侵入 SKU 创建）。

### 2.2 `inventory_logs`（库存变更流水）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `sku_id` | BIGINT UNSIGNED | 非空，FK → `skus.id` `ON DELETE RESTRICT` |
| `change_type` | TINYINT | `1=increase`、`2=deduct`（API 出参 `"increase"`/`"deduct"`） |
| `change_qty` | INT UNSIGNED | 正变更量（delta） |
| `before_qty` | INT UNSIGNED | 变更前数量 |
| `after_qty` | INT UNSIGNED | 变更后数量 |
| `operator_admin_id` | BIGINT UNSIGNED | 可空，软引用（无 FK，取自操作者管理员 id） |
| `reason` | VARCHAR(255) | 非空默认 `''` |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_sku_id`。每次成功变更记一条，失败（库存不足）不记。

## 3. 业务不变量

- INV-001（防负库存）：任何成功的增加/扣减后 `inventories.quantity ≥ 0`，并发扣减亦不例外。
- INV-002（充足才成功）：扣减仅在 `quantity ≥ N` 时成功；不足返回 6001 且库存与流水均不变。
- INV-003（流水与库存一致）：每次成功的增加/扣减在同事务内产生一条流水，`after_qty = before_qty ± change_qty` 且等于变更后当前库存；失败（库存不足）不产生成功流水。
- INV-004（1:1 归属）：`inventories.sku_id` 唯一，每 SKU 至多一条库存记录；变更只作用于指定 SKU，不串号。
- INV-005（RBAC）：未认证 401、无对应 `inventory:*` 权限 403 且无写入、持有权限（含超管）成功。
- INV-006（SKU 引用有效）：`sku_id` 必须指向存在的 SKU（否则 5001/404）；FK RESTRICT 保证库存记录不悬空。

## 4. 一致性模型与失败语义

- 事实来源：单一 MySQL。`skus`（SKU 存在性与稳定键）、`inventories`（当前库存数量，事实来源）、`inventory_logs`（审计流水，只追加）。
- 成功语义：查询成功代表返回时点 `sku_id` 的库存数量（无记录 = 0）；增加/扣减成功代表该次变更已持久化且对应流水已落库（同事务）。
- 原子性：每次成功的增加/扣减 = 「库存变更 + 一条流水」同事务提交；任一失败整体回滚。
- **增加**：原子 upsert（`INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + N`），`uk_sku_id` 唯一约束兜底并发，保证首次创建/并发 increase 不重复、不丢增量；`before_qty = after_qty - N` 在同一事务内取锁后最新值。
- **扣减**：条件更新 `UPDATE ... SET quantity = quantity - N WHERE sku_id = ? AND quantity >= N` + 核对 `RowsAffected`；命中 0 行返回 6001，事务回滚，不写流水、库存不变。防负库存以条件更新为主、`INT UNSIGNED` 类型为兜底。
- 校验失败（SKU 不存在 5001、数量非法 6002、未认证 401、未授权 403）：写入前拒绝且无写入。
- DB 技术错误统一 `1000` 500；SKU 删除命中 FK 1451 → 5005（409）。
- 并发目标：并发扣减同一 SKU 最终 `quantity ≥ 0`、成功扣减总量 = 初始 − 最终、成功次数 ≤ ⌊初始/单次扣减量⌋；并发增加总量正确（无丢失更新）。
- **非幂等**：V1 不实现业务幂等键，`Deduct` 只保证原子条件扣减与防负库存，不宣称业务请求幂等。`request_id`/order idempotency 延后订单模块。

## 5. 安全与权限边界

- 后台查询（仅 `AdminAuth`，无读权限）：`GET /admin/inventories/:sku_id`、`GET /admin/inventories/:sku_id/logs`。
- 后台写操作：`AdminAuth` + `RequirePermission`：
  - `POST /admin/inventories/:sku_id/increase` → `inventory:increase`
  - `POST /admin/inventories/:sku_id/deduct` → `inventory:deduct`
- 前台不公开任何库存接口；不新增 `inventory:list` 读权限。
- `operator_admin_id` 不建 FK（软引用，避免与 `admins` 删除耦合）。

## 6. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 6001 | INVENTORY_INSUFFICIENT | 409 |
| 6002 | INVENTORY_INVALID_QUANTITY | 400 |
| 5005 | SKU_HAS_INVENTORY（SKU 存在库存记录，不能删除） | 409 |

复用：`5001`（SKU 不存在，404）、`1001`（400）、`1002`（401）、`1003`（403）。

## 7. 跨模块关系

- `inventories.sku_id`、`inventory_logs.sku_id` → `skus.id`（FK `ON DELETE RESTRICT`）：SKU 一旦有库存记录或流水即不可物理删除，SKU 删除接口返回 5005，见 `sku.md`。
- 库存绑定 `sku_id` 而非 `product_id`；`skus` 不含 `stock`。
- `quantity` 增加量/扣减量均为正整数（`≥1`），非法返回 6002，无额外业务上限（`INT UNSIGNED` 兜底）。
- 建表经 golang-migrate 迁移（`20261001000004_inventory.up.sql`），见 `migration.md`。

## 8. Deferred / 已知留白

- 「设置绝对量/盘点修正」不提供单步接口，需组合增加/扣减完成；盘点/出入库单据在 Out of Scope。
- 幂等键缺失：扣减无去重，未来订单模块接入时必须补 `request_id`/`deduction_no` 防重复扣减（当前无订单模块、后台入口，风险可接受）。
- 流水无分页（V1 全量返回，按 `id` 倒序）；量大时由后续任务加分页。
