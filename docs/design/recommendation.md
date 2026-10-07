# 推荐位设计（Recommendation）

本文面向项目接手者，说明「推荐位」核心闭环的架构、数据模型、唯一性与排序语义、前台过滤规则、权限边界与错误码域。事实来源为 `recommendation-v1` 的 APPROVED Contract 与最终实现。

## 1. 职责与边界

推荐位回答「运营在指定位置展示哪些商品」，承载推荐位主数据（`recommend_positions`）与推荐商品关系（`recommend_items`），实现：后台推荐位 CRUD（创建/查看/修改/删除/禁用）、推荐商品管理（添加/移除/调整排序）、前台公开接口 `GET /recommendations/:code`（按稳定顺序只返回启用推荐位中的可售商品）。

边界：V1 无推荐算法/个性化/机器学习/排序模型，无商品推荐策略（点击/销量/热度等）；无前端可视化编排；无定时上下架时间窗；无曝光/点击统计与 A/B 测试；不做 SKU 维度推荐与跨推荐位聚合。事实来源为单一 MySQL；Redis 仅会话；无 MQ；推荐位为同步读写、无异步。

## 2. 数据模型

### 2.1 `recommend_positions`（推荐位主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `code` | VARCHAR(64) | 非空，唯一（`uk_code`），稳定业务标识（前台按 code 定位），格式 `[a-z0-9][a-z0-9-]{0,63}`，创建后不可变 |
| `name` | VARCHAR(64) | 非空，trim 后非空 |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`UNIQUE uk_code(code)`、`idx_status(status)`。

### 2.2 `recommend_items`（推荐商品关系）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `position_id` | BIGINT UNSIGNED | 非空，FK → `recommend_positions(id)` `ON DELETE CASCADE` |
| `product_id` | BIGINT UNSIGNED | 非空，软引用（无 FK，存在性由应用层经 `service.Product().Exists` 校验） |
| `sort` | INT | 非空默认 0，展示顺序（升序） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`UNIQUE uk_position_product(position_id, product_id)`（同推荐位同商品唯一，并发兜底）、`idx_position_sort(position_id, sort)`（前台查询）。

### 2.3 状态映射

| DB `status` | 语义 |
| --- | --- |
| `1` | 启用（公开，前台可查询） |
| `0` | 禁用（临时下线，前台不返回，后台可见） |

### 2.4 排序语义

前台查询 `ORDER BY sort ASC, id ASC`：`sort` 升序为主序，同值按 `id` 升序兜底，保证稳定确定性排序。`sort` 允许重复，无需唯一约束。

后台调整排序为**全量重排**：`PUT /admin/recommend-positions/:id/items/sort` 提交的有序 `product_ids` 必须恰好覆盖该推荐位全部已加入商品（集合相等，无缺漏/多余/重复），按提交顺序全量写入 `sort`；不一致返回 15006。

## 3. 商品可售性与过滤规则

- 添加推荐商品时仅校验「商品存在」（`service.Product().Exists`，不存在 → 4001 404、无写入），不限制商品状态；`draft`/`off_shelf` 商品可提前配置进推荐位。
- 前台 `GET /recommendations/:code` 经 JOIN `products` 过滤 `products.status=on_shelf`，返回商品实时快照（`product_id`/`name`/`main_image`/`price`）；商品下架不物理删除 `recommend_items` 关系，仅前台过滤、后台仍可见。
- 「有效商品」仅 `products.status=on_shelf`，不校验分类启用、SKU（商品模块语义：分类禁用不影响已上架商品，前台按商品自身 status 过滤）。

## 4. 业务不变量

- INV-001（前台可见性与稳定排序）：`GET /recommendations/:code` 仅返回 `status=1` 推荐位中 `products.status=on_shelf` 的商品，按 `sort` 升序、同值按 `id` 升序；禁用推荐位/下架商品即时不出现在结果中。
- INV-002（同推荐位同商品唯一）：同一推荐位下同一商品至多一条关系，DB 唯一约束 `uk_position_product` 兜底，并发重复提交也被拒绝（15005）且不产生重复关系。
- INV-003（权限边界）：后台写操作需 `AdminAuth` + 对应权限 code（`IsSuper` 放行）；未认证 401、无权限 403，且不产生任何 DB 写入。
- INV-004（商品加入有效性）：添加推荐商品前仅校验「商品存在」，不存在被拒（复用 4001）且无写入；前台只展示 on_shelf。
- INV-005（下架不物理删除关系）：商品下架后 `recommend_items` 关系保留，前台查询过滤、后台查询仍可见。
- INV-006（删除级联）：物理删除推荐位后，其全部 `recommend_items` 一并删除（FK CASCADE），不留孤儿。
- INV-007（全量重排覆盖）：调整排序提交的 `product_ids` 必须恰好覆盖该推荐位全部已加入商品（集合相等、无缺漏/多余/重复），否则被拒（15006）且不产生任何 `sort` 写入；成功时按提交顺序全量写入 `sort`（原子）。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL（`recommend_positions` 主数据 + `recommend_items` 从属）；商品可售性在查询时经 JOIN `products.status` 判定，`products` 为只读引用。无 Redis 写、无 MQ、无异步、无跨系统一致性。
- 创建推荐位成功 = 单条 `INSERT recommend_positions`；`code` 冲突（1062）→ 15002。
- 添加商品成功 = 单条 `INSERT recommend_items`；先校验商品存在（复用 4001）→ 写关系；重复（1062 on uk_position_product）→ 15005。
- 更新推荐位：先按 `id` 查存在性（不存在 → 15001 404），不依据 `RowsAffected` 判断存在性（幂等保存），`code` 不可变。
- 删除推荐位：条件删除 + 核对 `RowsAffected`（=0 → 15001 404）；级联删除 items 由 FK CASCADE 保证。
- 调整排序：先校验推荐位存在（不存在 → 15001 404），再校验提交的 `product_ids` 集合与推荐位现有商品集合相等（缺漏/多余/重复 → 15006 409），通过后在同一事务内按提交顺序全量写入 `sort`（原子，不产生部分写入）；空推荐位提交空列表 → 幂等成功（no-op）。
- 前台查询：推荐位不存在或 `status=0` → 返回空 items（`code:0`、`items:[]`，不区分「不存在」与「禁用」，避免向公开接口泄露内部状态）；DB 技术错误 → 1000（500）。
- 失败语义：未认证 401、无权限 403、不存在 404（15001/15004）、重复/冲突 409（15002/15005/15006）、非法输入 400（15003），均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## 6. 安全与权限边界

- 前台公开接口（无 token）：`GET /recommendations/:code`（仅启用位中的可售商品）。
- 后台查询（`AdminAuth`，无读权限，与商品/库存/轮播图查询一致）：`GET /admin/recommend-positions`（全部状态）、`GET /admin/recommend-positions/:id`（详情含 items）。
- 后台写（`AdminAuth` + `RequirePermission`）：
  - `POST /admin/recommend-positions` → `recommend:create`
  - `PUT /admin/recommend-positions/:id` → `recommend:update`（改 name/status，不改变 code）
  - `DELETE /admin/recommend-positions/:id` → `recommend:delete`（物理删除 + 级联）
  - `POST /admin/recommend-positions/:id/items` → `recommend:item`（添加商品）
  - `DELETE /admin/recommend-positions/:id/items/:product_id` → `recommend:item`（移除商品）
  - `PUT /admin/recommend-positions/:id/items/sort` → `recommend:item`（全量重排，`product_ids` 必须覆盖全部已加入商品）
- 权限 code（seed 登记 `internal/boot/seed.go`）：`recommend:create`、`recommend:update`、`recommend:delete`、`recommend:item`。
- 身份信任：后台写仅管理员（含超管 `IsSuper` 放行），经 `RequirePermission`；前台公开接口无需身份。

## 7. 错误码域（15000-15999，域序 15）

| code | 语义 | HTTP |
| --- | --- | --- |
| 15001 | RECOMMEND_POSITION_NOT_FOUND（推荐位不存在） | 404 |
| 15002 | RECOMMEND_POSITION_CODE_EXISTS（推荐位 code 已存在） | 409 |
| 15003 | RECOMMEND_INVALID_INPUT（code/name/sort/status/product_id 非法） | 400 |
| 15004 | RECOMMEND_ITEM_NOT_FOUND（推荐商品关系不存在） | 404 |
| 15005 | RECOMMEND_ITEM_DUPLICATE（同一推荐位重复添加同一商品） | 409 |
| 15006 | RECOMMEND_ITEM_SORT_MISMATCH（排序商品列表与现有商品集合不一致，须覆盖全部已加入商品） | 409 |

复用：`4001`（商品不存在，404）、`1001`（参数格式兜底，400）、`1002`（401）、`1003`（403）、`1000`（500）。

## 8. 跨模块关系

- `recommend_items.product_id` → `products.id`（软引用，无 FK）：商品存在性由应用层经 `service.Product().Exists` / `GetByID` 只读校验，不修改商品模块行为；商品可售性在前台查询时经 JOIN `products.status` 判定。
- 建表经 golang-migrate 迁移（`20261001000016_recommend.up.sql`），见 `migration.md`。
- 权限 code `recommend:create/update/delete/item` 经 `internal/boot/seed.go` 登记，授权模型见 `rbac.md`。
- 错误码域 `15000-15999` 的编码模型与分配规则见 `error-codes.md`，分配状态以 `.agent/registry/error-codes.md` 为权威。

## 9. Deferred / 已知留白

- 推荐算法、千人千面、个性化推荐、机器学习/排序模型、商品推荐策略（点击/销量/热度等自动推荐）。
- 推荐位/推荐商品的定时上下架时间窗、生效时间窗。
- 推荐位曝光/点击统计、效果分析、A/B 测试。
- 商品搜索/筛选、SKU 维度推荐、跨推荐位聚合推荐。
- 前台对「推荐位不存在」与「禁用」统一返回空（不泄露内部状态），如需区分属后续 Contract 修订。
- `code` 创建后不可变，如需重命名 code 属后续 Contract 修订 + 数据迁移。
