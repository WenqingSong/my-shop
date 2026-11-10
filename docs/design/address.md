# 收货地址设计（Shipping Address）

本文面向项目接手者，说明前台用户「收货地址」的架构、数据模型、默认地址一致性语义与安全边界。事实来源为 `shipping-address-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

为前台登录用户提供「收货地址」能力：对自己的地址增删改查，支持每用户最多一个默认地址，严格按 `Principal.UserID` 隔离数据。地址详情为稳定可读字段，供未来订单模块「下单时保存地址快照」复制使用。

边界：不实现订单/快照表；不设地址数量上限；不实现地址标签/分组；不提供后台查看/管理用户地址；不改动 IAM/认证/会话语义。事实来源为单一 MySQL。

地区字典 `regions`（省/市/区三级）由 migration `20261110000003_regions` 建立结构，并已由 B1-03-04 从历史
`system_city` 导入 3938 条数据（`id` 复用 `city_id`、`level` 0/1/2 → 1/2/3）。B1-04-01 起：
`addresses` 增加可空 `region_id`（指向 `regions.level=3` 区/县节点），同时**保留**省/市/区自由文本列以兼容
历史地址与旧客户端；`region_id` 为软引用，不建外键（与 `regions` 既有约定一致）。

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
| `region_id` | BIGINT UNSIGNED NULL | 结构化地区引用，指向 `regions.level=3`（区/县）；NULL=历史地址或未使用结构化地区；`idx_region_id` |
| `detail` | VARCHAR(255) | 非空，详细地址，trim 后 1~255 字符 |
| `is_default` | TINYINT | 非空默认 0（`1=默认`、`0=非默认`） |
| `default_key` | BIGINT UNSIGNED | VIRTUAL 生成列 `IF(is_default=1, user_id, NULL)`，`uk_user_default` 唯一 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

- 地区仍是省/市/区三个 VARCHAR（兼容历史地址与旧客户端），同时新增可空 `region_id` 指向地区字典；
  **写入 `region_id` 时**省/市/区文本由字典权威名称覆盖，不信任客户端提交的地区文本；
  **不写 `region_id` 时**（历史地址、旧客户端）自由文本行为完全不变。
- 默认地址唯一性由**生成列 + 唯一索引**在 DB 层保证：`default_key` 仅在 `is_default=1` 时等于 `user_id`（非空），否则为 NULL；MySQL 唯一索引允许多个 NULL，故「每用户最多一条 `is_default=1`」被数据库强约束，并发亦成立。
- `default_key` 为 VIRTUAL 生成列，**只读**，插入/更新不得写入该列。
- 技术取舍：采用 VIRTUAL（非 STORED）——MySQL 8.0 不允许 STORED 生成列引用「同时作为外键列」的 `user_id`（报 `1215 Cannot add foreign key constraint`）；VIRTUAL 保留 FK 与 `uk_user_default` 唯一约束，默认唯一语义不变。
- `user_id` 为归属锚点，`idx_user_id` 支撑按用户列表查询；FK `ON DELETE CASCADE` 为防御性（当前无用户删除接口，见 `iam.md`）。

建表经 golang-migrate（`20261001000005_addresses.up.sql`），见 `migration.md`。

### 2.2 `regions`（地区字典，只读结构）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增；导入时允许显式指定遗留 `system_city.city_id` |
| `parent_id` | BIGINT UNSIGNED | 非空默认 0；省级为 0（沿用 `categories` 的「0 表示无父级」约定） |
| `level` | TINYINT | 非空；`1=省`、`2=市`、`3=区/县` |
| `name` | VARCHAR(64) | 非空，地区名称 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_parent (parent_id)`、`idx_level (level)`。不建自引用外键（无法表达省级 `parent_id=0`）、不建名称唯一约束
（历史数据存在重名/占位节点）；层级与父子一致性由导入工具与后续应用层校验。

**ID 规则**：`regions.id` 直接复用 `system_city.city_id`（导入时显式写入），使 `addresses`/历史订单中已有的地区 id
无需二次映射；不得在导入时重新编号。建表迁移只建结构，数据由 B1-03-04 历史导入任务写入。

### 2.3 地区只读模块（region，B1-04-01）

API（全部公开只读，无 `Auth`，无写入端点）：

| 方法/路径 | 说明 |
| --- | --- |
| `GET /regions?parent_id={id}` | 查询直接子级；`parent_id` 缺省/0 返回省级；负数 1001；不存在的正数返回空列表 |
| `GET /regions/tree` | 返回省/市/区三级树：`items` 为省级节点数组（嵌套 `children`），`total` 为全部节点数（3938） |
| `GET /regions/:id` | 返回节点自身与 `province`/`city`/`district` 三级链路（链上不存在为 null）；不存在 1001 |

树查询实现：**单次** `SELECT id, parent_id, level, name FROM regions ORDER BY id` 全量读取，
在内存按 `parent_id` 建树，避免逐节点查询的 N+1 SQL。构建时强校验（数据不完整即 1000，不输出残缺树）：

- 省级必须 `parent_id=0` 且 `level=1`；
- 子节点父级必须存在，且 `parent.level == 子节点.level - 1`；
- 主键唯一（无重复节点）；全部节点必须被某个根覆盖（无孤儿、无环）。

同级节点按 id 升序，响应顺序稳定。地址写入时通过 `ResolveDistrict` 单次 JOIN 校验
`region_id` 为 `level=3` 且省-市-区链路完整，并取字典名称回填地址文本。

## 3. 业务不变量

- INV-001（数据隔离 + 存在性不泄露）：任何地址详情/更新/删除均按 `WHERE id AND user_id=Principal.UserID` 过滤；不命中（不存在或他人）统一返回 7001（404），绝不返回该地址内容、绝不泄露归属。
- INV-002（默认地址唯一）：每用户最多一条 `is_default=1`，由 `uk_user_default`（生成列）DB 约束保证，并发下亦成立。
- INV-003（默认切换原子 + 串行化）：设新默认 = 同一事务内「`SELECT users.id ... FOR UPDATE` 锁用户行 → 取消旧默认 → 置新默认」，
  失败整体回滚；首条地址自动默认（判重同样在用户行锁内）；删除默认地址后允许无默认、不自动提升。
  行锁把同一用户的默认变更串行化，`uk_user_default` 仍是最终防线（两层保护）。
- INV-004（身份不可伪造 + 未登录拒绝）：`user_id` 仅取自已认证 `Principal.UserID`，请求体不接受 `user_id`；未携带有效 token 访问任一地址接口返回 401（1002）且不产生任何写入。
- INV-005（结构化地区合法）：`region_id` 若提供则必须存在、`level=3`、且省-市-区链路完整（`province.level=1 && parent_id=0`、
  `city.level=2`）；省/市/区文本以字典为准。`region_id` 允许 NULL（历史地址），读写均不强制。
- INV-006（文本与 ID 一致）：写入 `region_id` 时省/市/区文本由字典覆盖；更新自由文本或显式 `region_id=0` 时清空 `region_id`，
  避免出现「文本与结构化地区不一致」的混合状态。

## 4. 一致性模型与失败语义

- 事实来源：单一 MySQL `addresses`。Redis 仅经 `middleware.Auth` 校验会话，不参与地址数据；无 MQ、无异步、无跨系统事务。
- 创建成功 = 一条 `addresses` 记录持久化且 `user_id=Principal.UserID`；用户首条地址自动置默认。
- 查询成功 = 返回时点该用户地址数据；列表仅含本人地址（`WHERE user_id=Principal.UserID`）。
- 更新/删除：条件写入 + 核对 `RowsAffected`，0 行 → 7001（404），不产生其他写入。
- 设默认：同事务先 `SELECT ... FOR UPDATE` 锁定 `users` 行，再「取消旧默认 + 置新默认」；
  同一用户的默认变更因此串行执行，正常路径不会互相冲突；极端情况下（如外部绕过行锁写入）
  落败方命中 `uk_user_default` 1062 → 7002（409），不产生半成品。
- 地区校验：`region_id` 在事务外只读校验（字典只读且稳定）；不存在/非区县/链路断裂 → 1001（400），无写入。
- 删除不存在的地址返回 7001（404，非幂等成功，与 `sku` 删除语义一致）；重复删除他人/不存在地址 → 404，无副作用。
- DB 技术错误统一 1000（500）；不向客户端暴露底层错误。
- V1 不实现业务幂等键（幂等延后订单模块，与 `inventory` 一致）。

## 5. 安全与权限边界

- 所有地址接口挂载 `middleware.Auth`（前台用户身份域，`type=user`），无 `RequirePermission`（前台用户侧无 RBAC，见 [engineering-guidelines.md](engineering-guidelines.md) 第 11 节「当前能力与任务边界」）。
- 身份信任：`user_id` 仅取自已认证 `Principal.UserID`，不接受请求体/参数中的 `user_id`；`Principal` 是唯一身份来源。
- 数据隔离：详情/更新/删除一律 `WHERE id AND user_id=Principal.UserID`，不存在与「他人地址」统一 404，避免存在性/归属 Oracle。
- 未认证（无/非法/过期/会话失效 token）→ 401（1002），fail-closed，无写入。
- 地区接口为公开只读字典（省份/城市/区县），无用户数据、无写入口；不做鉴权，也不接受任何用户身份参数。

## 6. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 7001 | ADDRESS_NOT_FOUND（不存在或非本人地址，统一不泄露） | 404 |
| 7002 | ADDRESS_DEFAULT_CONFLICT（并发设置默认地址唯一冲突） | 409 |

复用：`1001`（字段校验失败，400；**含 `region_id` 不存在/非区县级/父子链断裂**）、`1002`（未认证，401）、`1000`（内部错误，500）。

地区接口不新增错误码：非法 `parent_id`/不存在的地区 id 统一 `1001`（400）；字典数据不完整（孤儿/重复/链路断裂）为 `1000`（500），不输出残缺数据。

## 7. 跨模块关系

- `addresses.user_id` → `users.id`（FK `ON DELETE CASCADE`）；`user_id` 为数据归属锚点，隔离依赖 `middleware.Principal.UserID`。
- 前台路由挂载于 `internal/cmd/routes_frontend.go`（`middleware.Auth`），路径无版本前缀（对齐 `/me`、`/categories`、`/products`）。
- 地址详情字段（`recipient_name`/`phone`/`province`/`city`/`district`/`detail`）为稳定可读字段，未来订单模块读取后复制形成快照；订单/快照表不在本模块。
- `regions` 为独立只读字典，`addresses.region_id` 为软引用（无 FK）；`addresses` 同时保存自由文本与可空 `region_id`，
  `regions` 的结构与 ID 规则见 §2.2，只读 API 与树查询见 §2.3。
- `internal/logic/address` 通过 `service.Region().ResolveDistrict` 取得省/市/区权威名称，不直接查询 `regions` 表，
  避免地区规则在地址模块重复实现。
- 错误码域 7000-7999 归地址域所有。

## 8. Deferred / 已知留白

- `addresses.region_id` 仅为「区/县级」单字段引用；不支持一个地址跨多个区县，也不做省/市/区三列 ID 拆分。
- 配送范围、运费、仓储路由等依赖结构化地区的能力仍由后续任务实现，不在本模块提前扩展。
- `regions` 不提供 code/经纬度/邮政编码等扩展字段；若后续需要行政区划代码，按 additive 方式加列，不改变 `id` 规则。
- 历史地址不做 `region_id` 批量回填（无法可靠猜测历史文本对应节点）；仅在新写入时使用结构化地区。
- 地区字典全量约 3938 行，树接口一次全量读取并在内存构建；若未来字典规模显著增长，需要改为按父级懒加载或缓存。
- 无地址数量上限、无邮编、无标签/分组。
- 并发「设默认」落败方返回 409（7002），客户端需提示重试；属可接受的 V1 语义。
- 手机号校验限定中国大陆号码 `^1[3-9]\d{9}$`；海外号码支持需扩展校验。
