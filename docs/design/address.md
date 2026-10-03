# 收货地址设计（Shipping Address）

本文面向项目接手者，说明前台用户「收货地址」的架构、数据模型、默认地址一致性语义与安全边界。事实来源为 `shipping-address-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

为前台登录用户提供「收货地址」能力：对自己的地址增删改查，支持每用户最多一个默认地址，严格按 `Principal.UserID` 隔离数据。地址详情为稳定可读字段，供未来订单模块「下单时保存地址快照」复制使用。

边界：不实现订单/快照表；不引入行政区划 code 与码表；不设地址数量上限；不实现地址标签/分组；不提供后台查看/管理用户地址；不改动 IAM/认证/会话语义。事实来源为单一 MySQL。

## 2. 数据模型

### 2.1 `addresses`（收货地址）

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

- 地区为**自由文本**（省/市/区三个 VARCHAR），不建 region 表、不存 region code；结构化地区留待未来独立演进。
- 默认地址唯一性由**生成列 + 唯一索引**在 DB 层保证：`default_key` 仅在 `is_default=1` 时等于 `user_id`（非空），否则为 NULL；MySQL 唯一索引允许多个 NULL，故「每用户最多一条 `is_default=1`」被数据库强约束，并发亦成立。
- `default_key` 为 VIRTUAL 生成列，**只读**，插入/更新不得写入该列。
- 技术取舍：采用 VIRTUAL（非 STORED）——MySQL 8.0 不允许 STORED 生成列引用「同时作为外键列」的 `user_id`（报 `1215 Cannot add foreign key constraint`）；VIRTUAL 保留 FK 与 `uk_user_default` 唯一约束，默认唯一语义不变。
- `user_id` 为归属锚点，`idx_user_id` 支撑按用户列表查询；FK `ON DELETE CASCADE` 为防御性（当前无用户删除接口，见 `iam.md`）。

建表经 golang-migrate（`20261001000005_addresses.up.sql`），见 `migration.md`。

## 3. 业务不变量

- INV-001（数据隔离 + 存在性不泄露）：任何地址详情/更新/删除均按 `WHERE id AND user_id=Principal.UserID` 过滤；不命中（不存在或他人）统一返回 7001（404），绝不返回该地址内容、绝不泄露归属。
- INV-002（默认地址唯一）：每用户最多一条 `is_default=1`，由 `uk_user_default`（生成列）DB 约束保证，并发下亦成立。
- INV-003（默认切换原子）：设新默认 = 同一事务内「取消旧默认 + 置新默认」，失败整体回滚；首条地址自动默认；删除默认地址后允许无默认、不自动提升。
- INV-004（身份不可伪造 + 未登录拒绝）：`user_id` 仅取自已认证 `Principal.UserID`，请求体不接受 `user_id`；未携带有效 token 访问任一地址接口返回 401（1002）且不产生任何写入。

## 4. 一致性模型与失败语义

- 事实来源：单一 MySQL `addresses`。Redis 仅经 `middleware.Auth` 校验会话，不参与地址数据；无 MQ、无异步、无跨系统事务。
- 创建成功 = 一条 `addresses` 记录持久化且 `user_id=Principal.UserID`；用户首条地址自动置默认。
- 查询成功 = 返回时点该用户地址数据；列表仅含本人地址（`WHERE user_id=Principal.UserID`）。
- 更新/删除：条件写入 + 核对 `RowsAffected`，0 行 → 7001（404），不产生其他写入。
- 设默认：同事务「取消旧默认 + 置新默认」；并发落败方命中 `uk_user_default` 1062 → 7002（409），不产生半成品。
- 删除不存在的地址返回 7001（404，非幂等成功，与 `sku` 删除语义一致）；重复删除他人/不存在地址 → 404，无副作用。
- DB 技术错误统一 1000（500）；不向客户端暴露底层错误。
- V1 不实现业务幂等键（幂等延后订单模块，与 `inventory` 一致）。

## 5. 安全与权限边界

- 所有地址接口挂载 `middleware.Auth`（前台用户身份域，`type=user`），无 `RequirePermission`（前台用户侧无 RBAC，见 `AGENTS.md` 第 11 节「权限/RBAC 留白」）。
- 身份信任：`user_id` 仅取自已认证 `Principal.UserID`，不接受请求体/参数中的 `user_id`；`Principal` 是唯一身份来源。
- 数据隔离：详情/更新/删除一律 `WHERE id AND user_id=Principal.UserID`，不存在与「他人地址」统一 404，避免存在性/归属 Oracle。
- 未认证（无/非法/过期/会话失效 token）→ 401（1002），fail-closed，无写入。

## 6. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 7001 | ADDRESS_NOT_FOUND（不存在或非本人地址，统一不泄露） | 404 |
| 7002 | ADDRESS_DEFAULT_CONFLICT（并发设置默认地址唯一冲突） | 409 |

复用：`1001`（字段校验失败，400）、`1002`（未认证，401）、`1000`（内部错误，500）。

## 7. 跨模块关系

- `addresses.user_id` → `users.id`（FK `ON DELETE CASCADE`）；`user_id` 为数据归属锚点，隔离依赖 `middleware.Principal.UserID`。
- 前台路由挂载于 `internal/cmd/routes_frontend.go`（`middleware.Auth`），路径无版本前缀（对齐 `/me`、`/categories`、`/products`）。
- 地址详情字段（`recipient_name`/`phone`/`province`/`city`/`district`/`detail`）为稳定可读字段，未来订单模块读取后复制形成快照；订单/快照表不在本模块。
- 错误码域 7000-7999 归地址域所有。

## 8. Deferred / 已知留白

- 无行政区划 code 与码表；配送范围、运费、仓储路由等需要结构化地区的能力，由未来独立任务引入 region code，不在本模块提前扩展。
- 无地址数量上限、无邮编、无标签/分组。
- 并发「设默认」落败方返回 409（7002），客户端需提示重试；属可接受的 V1 语义。
- 手机号校验限定中国大陆号码 `^1[3-9]\d{9}$`；海外号码支持需扩展校验。
