# Technical Contract

## Decision Status
APPROVED

## Problem

交付前台用户「收货地址」能力：登录用户对自己的地址做增删改查，支持默认地址，严格按 `Principal.UserID` 隔离数据。为未来订单模块「下单时保存地址快照」预留稳定可读的地址详情，但本次不实现订单与快照。

本任务存在四个真正影响数据模型、安全边界与并发可靠性的设计选择：
1. 地址字段结构（收货人、手机号、省/市/区、详细地址）与地区表示方式（自由文本 vs 结构化码表）；
2. 默认地址语义（首条是否自动默认、删除默认后是否允许无默认）；
3. 默认地址唯一性在并发下的实现机制；
4. 数据隔离边界（访问他人地址/不存在地址的统一错误语义）。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register*`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层（见 `internal/service/inventory.go`、`internal/logic/inventory/inventory.go`、`internal/logic/logic.go`）。
- VERIFIED：`users` 表已存在（`internal/migrations/sql/20261001000001_baseline.up.sql`：`id`、`username`、`password_hash`、时间戳）；`addresses` 表不存在；无订单相关表。
- VERIFIED：前台受保护接口用 `middleware.Auth`（验签 + exp + Redis 会话校验）注入 `Principal{UserID, Sid}`；Controller 经 `middleware.PrincipalFromContext(ctx)` 取当前用户（`internal/controller/iam/iam.go` 的 `Me`）。前台用户侧目前仅有 `/me`、`/logout`，无「用户自有资源」CRUD 先例。
- VERIFIED：错误码集中在 `internal/codes/codes.go`，通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5005、库存 6001-6002；地址域 7000-7999 空闲。
- VERIFIED：迁移 golang-migrate v4（`internal/migrations/sql/{14位时间戳}_{title}.up.sql`），当前最新 `20261001000004_inventory`；`serve` 不自动迁移。
- VERIFIED：事实来源单一 MySQL；Redis 仅用于会话（`middleware.Auth`）；无 MQ、无异步、无订单模块。
- VERIFIED：并发/唯一性先例——`inventory` 用「条件更新 + `RowsAffected`」+ `UNIQUE` 兜底防负库存（`internal/logic/inventory/inventory.go`）；`sku` 用 FK `ON DELETE RESTRICT` + 1062/1451 错误映射（`internal/logic/sku/sku.go`）。
- UNKNOWN：无「用户自有资源」CRUD 与「每用户唯一字段」的既有实现可复用，默认地址唯一性需新设计。
- 备注：`task.md` 的 Review Baseline 记录 base commit `5f160ab…`（分支 `feat/address`），与当前 Git 状态（分支 `develop`，HEAD `1db733d…`，working tree clean）不一致，属 Task Builder 基线记录过期，不阻塞本分析。

## Recommendation

RECOMMENDATION：采用「自由文本地区 + DB 生成的唯一键保证默认地址唯一 + 统一 404 数据隔离」的单一方案。

### 数据模型

新增迁移 `20261001000005_addresses.up.sql`：

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，`idx_user_id` 索引，FK → `users.id` `ON DELETE CASCADE` |
| `recipient_name` | VARCHAR(32) | 非空，收货人姓名，trim 后 1~32 字符 |
| `phone` | VARCHAR(20) | 非空，手机号，正则 `^1[3-9]\d{9}$` |
| `province` | VARCHAR(32) | 非空，省（自由文本） |
| `city` | VARCHAR(32) | 非空，市（自由文本） |
| `district` | VARCHAR(32) | 非空，区（自由文本） |
| `detail` | VARCHAR(255) | 非空，详细地址，trim 后 1~255 字符 |
| `is_default` | TINYINT | 非空默认 0（`1=默认`、`0=非默认`） |
| `default_key` | BIGINT UNSIGNED | VIRTUAL 生成列 `IF(is_default=1, user_id, NULL)`，`uk_user_default` 唯一 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

- 地区采用**自由文本**（省/市/区三个 VARCHAR 字段），不引入区划码表、不建 region 表——码表数据与选择器在 Out of Scope，自由文本满足「稳定可读供未来快照」，且避免扩大 Scope。
- 默认地址唯一性由**生成列 + 唯一索引**在 DB 层保证：`default_key` 仅在 `is_default=1` 时等于 `user_id`（非空），否则为 NULL；MySQL 唯一索引允许多个 NULL，因此「每用户最多一条 `is_default=1`」被数据库强约束，并发亦成立。
- `default_key` 采用 **VIRTUAL**（非 STORED）生成列：MySQL 8.0 不允许 STORED 生成列引用「同时作为外键列」的 `user_id`（报 `1215 Cannot add foreign key constraint`）；改用 VIRTUAL 后保留 FK 且唯一索引 `uk_user_default` 语义不变。
- `user_id` 为数据归属锚点，加 `idx_user_id` 支撑按用户列表查询；FK `ON DELETE CASCADE` 为防御性（当前无用户删除接口，见 iam.md）。

### 接口形态

挂载 `internal/cmd/routes_frontend.go`，`middleware.Auth` 保护（路径无版本前缀，对齐 `/me`、`/categories`）：

| 方法 | 路径 | 语义 |
| --- | --- | --- |
| POST | `/addresses` | 创建地址 |
| GET | `/addresses` | 查询本人地址列表 |
| GET | `/addresses/:id` | 查询本人地址详情 |
| PUT | `/addresses/:id` | 更新本人地址 |
| DELETE | `/addresses/:id` | 删除本人地址 |

请求体**不含、不接受** `user_id`；归属仅取自已认证 `Principal.UserID`。

### 默认地址语义

- 每用户最多一个默认地址（DB 约束保证）。
- 创建：若该用户当前**无任何地址**（首条），无论客户端是否传 `is_default`，均自动置为默认；非首条时 `is_default` 默认 0，客户端可显式请求 1（触发「设默认」）。
- 更新：允许把地址设为默认（同事务先取消旧默认再置新默认）；允许把默认地址取消默认（`is_default=0`），此时用户可处于无默认状态。
- 删除：物理删除；删除默认地址后**允许无默认**，不自动提升其他地址为默认。

### 数据隔离与错误语义

- 详情/更新/删除一律 `WHERE id=? AND user_id=?`（`user_id` 取自 Principal），0 行命中统一返回 `7001`（404）——不区分「不存在」与「他人地址」，避免泄露地址存在性与归属（满足 AC-003）。
- 默认唯一性并发冲突（`uk_user_default` 命中 1062）映射为 `7002`（409）。

### 错误码（地址域 7000-7999，仅新增两个）

| code | 语义 | HTTP |
| --- | --- | --- |
| 7001 | ADDRESS_NOT_FOUND（不存在或非本人地址，统一不泄露） | 404 |
| 7002 | ADDRESS_DEFAULT_CONFLICT（并发设置默认地址唯一冲突） | 409 |

字段校验（收货人/详细地址为空或超长、手机号格式非法、省市区为空）复用 `1001`（400）；未认证复用 `1002`（401）。不新增地址数量上限（Out of Scope）。

关键取舍：**生成列唯一索引**（而非应用层事务 + `FOR UPDATE` 或 nullable `is_default`）作为默认唯一性的主保证——语义上 `is_default` 保持干净布尔，DB 声明式强约束（符合 AGENTS.md §6「能由数据库可靠保证的重要约束，使用约束而非先查再写」），并发下无需人为锁序；代价是 schema 多一个只读生成列，写入时不得包含 `default_key`，且并发「设默认」的落败方返回 409 而非静默重排。

## Selected Design

经 Owner 确认，采用本 Contract「Recommendation」中的单一方案，关键选择固化如下：

- 地区采用**自由文本**：`province` / `city` / `district` 三个 VARCHAR 字段，不引入行政区划 code 与码表；结构化地区留待未来独立演进。
- **首条地址自动设为默认**：用户创建首条地址时，无论是否传 `is_default`，均自动置为默认。
- **删除默认地址后允许无默认**：删除默认地址不自动提升其他地址为默认，允许用户暂时无默认地址；后续需要默认地址的业务场景由调用方要求用户显式选择。
- **每用户最多一个默认地址**：由 DB 生成列 `default_key` + `uk_user_default` 唯一索引兜底，并发亦成立。
- **数据隔离**：用户自有资源的查询/更新/删除始终基于 `Principal.UserID`，请求体不接受 `user_id`。
- **他人地址与不存在地址统一返回 404（7001）**，不泄露资源存在性。

## Interfaces and Data

- 新增 `api/address/v1`：请求/响应结构，`g.Meta` 声明 `path`/`method`；响应复用 `{code,message,data}` 与既有响应格式。
- 新增 `internal/service` 的 `IAddress` 接口 + `RegisterAddress`；`internal/logic/address`（`init()` 注册）；`internal/controller/address`。
- 新增 `internal/logic/logic.go` 的 blank import。
- 新增迁移 `internal/migrations/sql/20261001000005_addresses.up.sql`（version 接 `20261001000004` 之后，不使用 `IF NOT EXISTS`）。
- `addresses` 表字段与约束见上文数据模型；`default_key` 为 VIRTUAL 生成列，仅可读，插入/更新不得写入该列。
- 公开字段名稳定英文：`id`、`recipient_name`、`phone`、`province`、`city`、`district`、`detail`、`is_default`、`created_at`、`updated_at`。

## Business Invariants

- INV-001（数据隔离 + 存在性不泄露）：任何地址详情/更新/删除均按 `WHERE id AND user_id=Principal.UserID` 过滤；不命中（不存在或他人）统一返回 7001（404），绝不返回该地址内容、绝不泄露归属。
- INV-002（默认地址唯一）：每用户最多一条 `is_default=1`，由 `uk_user_default`（生成列）DB 约束保证，并发下亦成立。
- INV-003（默认切换原子）：设新默认 = 同一事务内「取消旧默认 + 置新默认」，失败整体回滚；删除默认地址后允许无默认、不自动提升。
- INV-004（身份不可伪造 + 未登录拒绝）：`user_id` 仅取自已认证 `Principal.UserID`，请求体不接受 `user_id`；未携带有效 token 访问任一地址接口返回 401（1002）且不产生任何写入。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL `addresses`。Redis 仅经 `middleware.Auth` 校验会话，不参与地址数据；无 MQ、无异步、无跨系统事务。
- 创建成功 = 一条 `addresses` 记录持久化且 `user_id=Principal.UserID`；首条或请求默认时，同事务内完成默认设置。
- 查询成功 = 返回时点该用户地址数据；列表仅含本人地址。
- 更新/删除：条件写入 + 核对 `RowsAffected`，0 行 → 7001（404），不产生其他写入。
- 设默认：同事务「取消旧默认 + 置新默认」；并发落败方命中 `uk_user_default` 1062 → 7002（409），不产生半成品（旧默认保持或回滚）。
- 删除不存在的地址返回 7001（404，非幂等成功，与既有 `sku` 删除语义一致）；重复删除他人/不存在地址 → 404，无副作用。
- DB 技术错误统一 1000（500）；不向客户端暴露底层错误。
- V1 不实现业务幂等键（与 `inventory` 一致，幂等延后订单模块）。

## Allowed / Forbidden Changes

- 允许：新增 `addresses` 迁移、`api/address/v1`、`internal/controller/address`、`internal/logic/address`、`internal/service` 的 `IAddress`、地址域错误码 7001/7002、`routes_frontend.go` 挂载 `/addresses`（Auth 保护）、对应测试、`docs/design/address.md`（Design Impact = NEW，见下）。
- 禁止：改动 IAM/认证/会话语义；改动既有公开接口、错误码或响应格式；实现订单/快照表；引入区划码表或 region 数据；增加地址数量上限；接受客户端 `user_id`；改动后台路由。

## Design Impact

- Design Impact：NEW（新增业务模块与长期数据模型/公开协议/错误码域）。
- Design Artifact：`docs/design/address.md`（Contract `APPROVED` 后、Coder 实现前由 Analyst 写入）。
- 说明：`task.md` 未显式声明 Design Impact 节，此处由 Analyst 校正为 NEW；不改变 Scope，无需回 Task Builder。

## Verification Requirements

- INV-001 → 需 MySQL：用户 A、B 各建地址；A 用自己 token 对 B 的地址 id 调 detail/update/delete，断言 7001/404 且 B 地址无变化；查询不存在 id 同样 404。
- INV-002 → 需 MySQL + `-race`：并发将不同地址设为默认，断言任意时刻 `SELECT COUNT(*) FROM addresses WHERE user_id=? AND is_default=1` ≤ 1。
- INV-003 → 需 MySQL：设新默认后旧默认 `is_default=0`；删除默认地址后该用户无默认（不自动提升）。
- INV-004 → 需 Redis：无 token 访问任一地址接口断言 401/1002，且 `addresses` 表无新增。
- 逐条覆盖 AC-001~AC-008；集成测试走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用两个真实用户断言隔离，并核对拒绝场景无意外 DB 写入。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 并发「设默认」落败方返回 409（7002），客户端需能处理该错误并提示重试；属可接受的 V1 语义。
- 手机号校验限定中国大陆号码 `^1[3-9]\d{9}$`；若未来需支持海外号码需扩展校验（本任务按中文项目默认）。
- 地址字段无邮编/标签，V1 不引入；未来快照如需更多字段，另行扩展迁移。

## Owner Decision Record

Owner 于 2026-10-04 确认以下决定（均与 Task 兼容，不改变 Goal/Scope/AC）：

1. V1 地区采用自由文本 `province`/`city`/`district`，不引入行政区划 code 与码表；结构化地区留待未来独立演进。
2. 首条地址自动设为默认地址。
3. 删除默认地址后允许用户暂时不存在默认地址，不自动将其它地址提升为默认。
4. 每用户最多一个默认地址，由 DB 唯一约束兜底。
5. 用户自有资源查询/更新/删除始终基于 `Principal.UserID`。
6. 他人地址与不存在地址统一返回 404，不泄露资源存在性。

另由 Task Builder 修正 `task.md` 过时的 Review Baseline；Contract `APPROVED` 后，按 Design Governance 先产出 `docs/design/address.md`，再进入 Coder。

### CONTRACT_REVISION（2026-10-04，Owner 已确认）

Cleaner 审查（`CHANGES_REQUIRED`，CLEAN-001）发现：Contract 与 `docs/design/address.md` 将 `default_key` 描述为 **STORED** 生成列，而最终迁移实现为 **VIRTUAL**（`20261001000005_addresses.up.sql`）。经核实（MySQL 8.0 实测）：STORED 生成列引用「同时作为外键列」的 `user_id` 报 `1215 Cannot add foreign key constraint`；改用 VIRTUAL 后保留 FK 且唯一索引 `uk_user_default` 语义不变。实现正确，故本次修订将 Contract 中 `default_key` 的「STORED」更正为「VIRTUAL」，并补注取舍原因；不改动接口、不变量（INV-001~004）、错误码与一致性语义。原 6 项 Owner 决定保持不变。状态恢复为 `WAITING_FOR_OWNER_APPROVAL`，待 Owner 确认。

Owner 于 2026-10-04 确认本次 CONTRACT_REVISION：将 `default_key` 生成列类型由 STORED 修订为 VIRTUAL。确认依据：STORED 引用外键列在 MySQL 8.0 报 1215；VIRTUAL 保留 FK 与 `uk_user_default` 唯一约束；业务语义、接口、不变量、错误码均不变；当前实现已是 VIRTUAL 且 build/test/race 通过，不修改生产代码。Contract 状态恢复为 `APPROVED`。
