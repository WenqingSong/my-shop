# 拼团与砍价设计（Marketing）

本文说明拼团与砍价的长期数据模型、状态、跨模块边界与关键不变量。**B1-03-03 只落数据库结构**：
没有开团/参团/成团、发起砍价/助力/成功判定、超时扫描、订单计价或真实支付的业务代码。
**B1-04-07 已在同一批表上落地砍价业务**（活动管理、发起、助力、成功/失败/过期与查询，见 §3.6–§3.10），
拼团仍只有结构。任何实现必须先遵守本文的不变量，再更新 `pricing.md`/`order.md`。

## 1. 职责与边界

- 拼团回答「哪个 SKU 以什么拼团价、几人成团、团的有效期、谁开团、谁参与、团成功还是失败/过期」。
- 砍价回答「哪个 SKU 的原始价/目标价是多少、用户实例当前价是多少、谁助力过、还能助力几次、实例成功/失败/过期」。
- 事实来源：单一 MySQL。Redis 仅会话；不引入 MQ、分布式锁或跨系统事务。
- 不拥有库存：拼团/砍价都**不建活动库存列**，也不读写 `inventories`/`inventory_logs`/`flash_sale_*`。
  成交时的库存扣减仍走现有普通库存链路（`IInventory.DeductInTx`），秒杀库存模型完全不受影响。
- 不拥有支付：Mock 支付沿用 `orders` 的现有支付状态；拼团/砍价只通过 `order_id` 关联订单。
- 不拥有订单金额：订单金额仍由 `orders`/`order_items` 快照承载；营销活动只在自身表保存价格快照，
  不新增 `orders.*_id` 列，也不改写既有订单金额语义。

## 2. 拼团模型

### 2.1 `group_buy_activities`（拼团活动）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `product_id` | BIGINT UNSIGNED | 非空，FK → `products.id` ON DELETE RESTRICT |
| `sku_id` | BIGINT UNSIGNED | 非空，FK → `skus.id` ON DELETE RESTRICT；活动价格作用于该 SKU |
| `name` | VARCHAR(64) | 非空，活动名称 |
| `group_price` | INT UNSIGNED | 非空，拼团价（整数分） |
| `group_size` | INT UNSIGNED | 非空，成团人数（含团长，≥2 由应用校验） |
| `team_ttl_minutes` | INT UNSIGNED | 非空，开团后的有效分钟数 |
| `start_time`/`end_time` | DATETIME | 非空，活动起止 |
| `status` | TINYINT | 非空默认 1，`1=启用`、`0=停用` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_product_id`、`idx_sku_id`、`idx_status_time(status, start_time, end_time)`。
活动不设「已结束」冗余终态：`NOW()` 与 `end_time` 比较即派生；`status` 仅表达运营启停。

### 2.2 `group_buy_teams`（团实例）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `activity_id` | BIGINT UNSIGNED | 非空，FK → `group_buy_activities.id` ON DELETE RESTRICT |
| `leader_user_id` | BIGINT UNSIGNED | 非空，团长 user id（软引用，无 FK） |
| `group_price` | INT UNSIGNED | 非空，开团时的拼团价快照（同团成员价格一致） |
| `required_size` | INT UNSIGNED | 非空，开团时的成团人数快照 |
| `joined_count` | INT UNSIGNED | 非空默认 1，当前有效参与人数（含团长） |
| `status` | TINYINT | 非空默认 10，见状态机 |
| `expire_at` | DATETIME | 非空，开团时间 + `team_ttl_minutes` 快照 |
| `closed_at` | DATETIME | 可空，进入终态的时间（成团/失败/过期共用） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_activity_status(activity_id, status)`、`idx_leader_user(leader_user_id)`、
`idx_status_expire(status, expire_at)`。

价格与人数必须快照：活动在开团后改价/改人数，不得改变已开团的支付价与成团要求。

### 2.3 `group_buy_participants`（参与记录）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `team_id` | BIGINT UNSIGNED | 非空，FK → `group_buy_teams.id` ON DELETE RESTRICT |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `order_id` | BIGINT UNSIGNED | 可空，软引用 `orders.id`（无 FK）；`uk_order_id` 唯一 |
| `status` | TINYINT | 非空默认 10，`10=有效`、`20=已退出` |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即参团时间 |
| `updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` ON UPDATE |

索引：`PRIMARY(id)`、`uk_team_user(team_id, user_id)` UNIQUE、`uk_order_id(order_id)` UNIQUE、
`idx_user_time(user_id, created_at)`。

- 团长也必须有参与记录（`user_id = group_buy_teams.leader_user_id`），不另设 `is_leader` 列；
  团长身份以 `leader_user_id` 为准，参与关系以本表为准。
- `uk_team_user` 保证同一用户在同一团只能有一条参与记录；`uk_order_id` 保证一个订单只能参团一次
  （MySQL 唯一索引允许多个 NULL：未生成订单的占位行可有多条）。
- 团队终态不逐行改写参与记录；团 `status` 是团队结果的权威事实，参与 `status` 只表达「有效/已退出」。

### 2.4 团队状态机

| from | to | 触发 | 说明 |
| --- | --- | --- | --- |
| 10 进行中 | 20 已成团 | `joined_count >= required_size` | 条件更新 + `closed_at=NOW()` |
| 10 进行中 | 30 已失败 | 团长退出/运营取消/支付失败等业务决定 | 条件更新 + `closed_at=NOW()` |
| 10 进行中 | 40 已过期 | `expire_at <= NOW()` 且未满员 | 由扫描或懒判定推进 + `closed_at=NOW()` |

终态：20/30/40。终态不可回到 10；非法迁移拒绝且无副作用。`expire_at` 只表示截止时间，
是否已过期以 `status` 为准（B1-04 的扫描/懒判定负责推进），避免把「时间比较」散落在读取路径。

### 2.5 并发与容量

参团必须在同一事务内：

1. 条件更新团：`UPDATE group_buy_teams SET joined_count = joined_count + 1
   WHERE id=? AND status=10 AND joined_count < required_size AND expire_at > NOW()`；
2. `RowsAffected=0` → 团满/已终态/已过期，拒绝参团；
3. 插入 `group_buy_participants`；`uk_team_user`/`uk_order_id` 撞键（1062）→ 整体回滚，`joined_count` 回滚；
4. 若 `joined_count` 达到 `required_size`，同事务条件更新 `status=20, closed_at=NOW()`。

`joined_count` 是容量事实，不能用「事后 `COUNT(*)`」代替并发闸门；参与记录与计数器在同一事务内保持一致。
团人数不能超过 `required_size`（条件更新保证），也不会为负（`INT UNSIGNED`）。

## 3. 砍价模型

### 3.1 `bargain_activities`（砍价活动）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `product_id` | BIGINT UNSIGNED | 非空，FK → `products.id` ON DELETE RESTRICT |
| `sku_id` | BIGINT UNSIGNED | 非空，FK → `skus.id` ON DELETE RESTRICT |
| `name` | VARCHAR(64) | 非空，活动名称 |
| `start_price` | INT UNSIGNED | 非空，原始价（整数分） |
| `floor_price` | INT UNSIGNED | 非空，目标价/底价（整数分，≤ start_price 由应用校验） |
| `help_amount` | INT UNSIGNED | 非空，每次助力的名义砍减额（Mock 规则，整数分） |
| `max_help_count` | INT UNSIGNED | 非空，单个实例最多被助力次数 |
| `start_time`/`end_time` | DATETIME | 非空，活动起止 |
| `status` | TINYINT | 非空默认 1，`1=启用`、`0=停用` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_product_id`、`idx_sku_id`、`idx_status_time(status, start_time, end_time)`。

### 3.2 `bargain_instances`（用户砍价实例）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `activity_id` | BIGINT UNSIGNED | 非空，FK → `bargain_activities.id` ON DELETE RESTRICT |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `start_price` | INT UNSIGNED | 非空，创建实例时的原始价快照 |
| `floor_price` | INT UNSIGNED | 非空，创建实例时的目标价快照 |
| `current_price` | INT UNSIGNED | 非空，当前价（从 `start_price` 递减，不得低于 `floor_price`） |
| `helped_count` | INT UNSIGNED | 非空默认 0，已成功助力次数 |
| `max_help_count` | INT UNSIGNED | 非空，创建实例时的助力次数上限快照 |
| `status` | TINYINT | 非空默认 10，见状态机 |
| `expire_at` | DATETIME | 非空，创建实例时对活动 `end_time` 的快照 |
| `completed_at` | DATETIME | 可空，砍价成功时间；失败/过期不写 |
| `order_id` | BIGINT UNSIGNED | 可空，软引用 `orders.id`；`uk_order_id` 唯一 |
| `active_key` | VARCHAR(64) GENERATED STORED | `status=10` 时 `"activity_id:user_id"`，否则 NULL |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_active_instance(active_key)` UNIQUE、`uk_order_id(order_id)` UNIQUE、
`idx_activity_id`、`idx_user_status(user_id, status)`、`idx_status_expire(status, expire_at)`。

- `active_key` + 唯一键保证「同一用户同一活动至多一个进行中实例」，并发重复发起 1062；
  失败/过期后 `active_key` 变 NULL，允许重新发起，历史实例保留（与 `refund_requests.active_key` 同一模式）。
- 一个订单只能对应一个砍价实例（`uk_order_id`，NULL 可多条）。
- 实例创建时快照 `start_price`/`floor_price`/`max_help_count`/`expire_at`；活动改价/延期不影响已创建实例。

### 3.3 `bargain_help_records`（助力记录）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `instance_id` | BIGINT UNSIGNED | 非空，FK → `bargain_instances.id` ON DELETE RESTRICT |
| `helper_user_id` | BIGINT UNSIGNED | 非空，助力者 user id（软引用） |
| `cut_amount` | INT UNSIGNED | 非空，本次实际砍减额（可小于 `help_amount`，用于补足差额） |
| `price_before` | INT UNSIGNED | 非空，本次助力前价格 |
| `price_after` | INT UNSIGNED | 非空，本次助力后价格（≥ `floor_price`） |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即助力时间 |

索引：`PRIMARY(id)`、`uk_instance_helper(instance_id, helper_user_id)` UNIQUE、`idx_helper_time(helper_user_id, created_at)`。

- 同一用户对同一实例只能助力一次（数据库唯一键）；不同用户可分别助力。
- 助力记录是价格变化的逐笔凭据：`cut_amount` 与 `price_before/price_after` 必须由写入方保证
  `price_after = price_before - cut_amount` 且 `price_after >= floor_price`（DB 只保证非负）。
- 助力记录只追加；不提供修改/删除业务路径。

### 3.4 实例状态机

| from | to | 触发 | 说明 |
| --- | --- | --- | --- |
| 10 进行中 | 20 成功 | `current_price <= floor_price` 或达到成功条件 | `completed_at=NOW()` |
| 10 进行中 | 30 失败 | 用户放弃/活动提前终止/运营关闭 | 不写 `completed_at` |
| 10 进行中 | 40 已过期 | `expire_at <= NOW()` 且未成功 | 不写 `completed_at` |

终态：20/30/40。`expire_at` 是截止时间，是否过期以 `status` 为准（B1-04 扫描或懒判定推进）。
成功判定必须与最后一次助力的条件更新同事务，避免「价格到目标价但状态仍进行中」的中间态对外可见。

### 3.5 并发与重复助力

助力必须在同一事务内：

1. 条件更新实例：
   `UPDATE bargain_instances
      SET current_price = GREATEST(floor_price, current_price - ?),
          helped_count = helped_count + 1
    WHERE id=? AND status=10 AND helped_count < max_help_count AND current_price > floor_price`；
2. `RowsAffected=0` → 已终态/已过期/次数用尽/已到底价，拒绝助力；
3. 插入 `bargain_help_records`；撞 `uk_instance_helper`（1062）→ 整体回滚，价格与次数一并回滚；
4. 若 `current_price <= floor_price`，同事务条件更新 `status=20, completed_at=NOW()`。

`help_amount` 是名义额；最后一次可只砍剩余差额（`cut_amount = price_before - floor_price`），
因此 `cut_amount` 允许小于 `help_amount`，但 `price_after` 不得低于 `floor_price`。
`max_help_count` 用条件更新限制，`uk_instance_helper` 是并发重复请求的最终防线。

### 3.6 砍价实现落地（B1-04-07）

代码边界：

| 层 | 位置 | 职责 |
| --- | --- | --- |
| API | `api/bargain/v1/bargain.go` | 活动/实例/助力记录 DTO，实例状态对外为字符串枚举 |
| Controller | `internal/controller/bargain` | HTTP 适配；前台用户身份取自 `Principal.UserID`，后台身份取自 `AdminPrincipal` |
| Service | `internal/service/bargain.go` | `IBargain` 对内接口 |
| Logic | `internal/logic/bargain` | 活动校验、发起、助力事务、状态推进、懒过期、查询编排 |
| 错误码 | `internal/codes/codes.go` | 砍价域 `23001-23009`（域序 21） |
| 权限 | `internal/boot/seed.go` | `bargain:create`/`bargain:update`/`bargain:list`/`bargain:view` |

活动管理规则（后台）：

- 价格：`1 ≤ floor_price < start_price ≤ 99999999`，`help_amount ≥ 1`，`max_help_count ≥ 1`；
  且 `ceil((start_price - floor_price) / help_amount) ≤ max_help_count`（可达成性），
  否则活动永远无法砍到底价，一律 `23002`。
- 时间：`start_time < end_time`，且 `end_time` 必须晚于当前时间；缺时间或时间倒置 `23002`。
- 商品/SKU：商品必须存在，SKU 必须存在且 `skus.product_id = bargain_activities.product_id`，否则 `23002`。
- 编辑：`name`/价格/助力参数/时间窗可改；`product_id`/`sku_id` **不可改绑**（在途实例以活动为展示来源，
  改绑会让同一实例指向另一件商品），需要换商品时另建活动。
- 状态：`1=启用`、`0=停用`，启停幂等；**停用只阻止新发起**，已创建实例按自身快照继续推进（INV-BG08）。

发起规则（前台）：

- 仅 `status=1` 且 `NOW() ∈ [start_time, end_time)` 可发起，否则 `23003`；活动不存在 `23001`。
- 创建实例用单条 `INSERT ... SELECT` 从活动行取快照，把「活动可参与判定」与「写入实例」合并为一个原子语句，
  不存在先查后写的窗口：`start_price`/`floor_price`/`current_price=start_price`/`max_help_count`/`expire_at=end_time`。
- 同一用户同一活动至多一个进行中实例：`uk_active_instance` 撞键 → `23005`；失败/过期/成功释放 `active_key` 后可重新发起，
  历史实例保留。

### 3.7 助力事务与并发控制（实现细节）

`Help` 在**单个事务**内完成，顺序与 §3.5 一致：

1. `SELECT ... FOR UPDATE` 锁定实例行，并在同一语句内用 `(expire_at <= NOW())` 判定是否过期
   （时间比较只在 SQL 侧做，避免 Go↔MySQL 时区漂移）；未命中 → `23004`。
2. 资格校验：助力者是发起者本人 → `23006`；实例非进行中或已过期 → `23007`；`helped_count >= max_help_count` → `23007`。
3. 读取活动 `help_amount`（实例不快照该列），按 §3.8 计算 `cut`；`cut <= 0` → `23007`。
4. 条件更新实例（带 `status/次数/价格下限/未过期` 全量闸门），`RowsAffected=0` → `23007` 并回滚。
5. 插入 `bargain_help_records`；撞 `uk_instance_helper`（1062）→ `23008` 并整体回滚（价格与次数一并回滚）。
6. 若 `price_after <= floor_price`，同事务条件更新 `status=20, completed_at=NOW()`。

行锁让同一实例上的并发助力串行化，条件更新是无法绕过的第二道闸门，唯一索引是重复请求的最终防线。
`SELECT ... FOR UPDATE` 与唯一索引均为真实 MySQL 行为，由 `internal/logic/bargain/integration_test.go` 在隔离库上验证
（并发 20 人助力只成功 5 次、价格恰好停在底价）。

### 3.8 助力金额算法

```text
remaining  = current_price - floor_price        （实例快照，恒 ≥ 0）
cut_amount = min(help_amount, remaining)        （> 0 才允许助力）
price_after = current_price - cut_amount        （恒 ≥ floor_price）
成功条件    = price_after == floor_price
```

- 算法来源：Migration `20261110000012_bargain` 顶部注释与本文 §3.5 的 APPROVED 设计（`GREATEST(floor_price, current_price - help_amount)` 的等价表述），
  不是随机数、固定值或临时发明。Java 参考源码在本机不可得（见交付报告），因此以已批准设计为唯一依据。
- `help_amount` 取自活动当前配置（实例未快照该列，DDL 事实）；`floor_price`/`start_price`/`max_help_count`/`expire_at`
  是实例快照，因此**活动改价不会改变已创建实例的底价**，只会影响后续助力的单次砍减额。
- 示例：`start=10000, floor=9500, help=100` → 5 次助力后 `current=9500` 且状态置成功；
  若 `start=10000, floor=9900, help=500`，首次助力 `cut=100`（补足差额），`price_after=9900`。

### 3.9 成功、失败与过期语义

| 终态 | 触发 | 写入 |
| --- | --- | --- |
| 20 成功 | 助力后 `current_price == floor_price`（同事务条件更新） | `completed_at=NOW()` |
| 30 失败 | 发起者本人放弃（`POST /bargain-instances/:id/abandon`，仅进行中） | 不改 `completed_at` |
| 40 已过期 | 读取路径懒推进：`status=10 且 expire_at<=NOW()` | 不改 `completed_at` |

- 过期不引入扫描器/MQ/Redis：`InstanceDetail`、`MyInstances`、`Abandon` 在读/写前执行
  `UPDATE ... SET status=40 WHERE id=? AND status=10 AND expire_at<=NOW()`，条件更新保证**重复执行无副作用**。
  这只影响状态呈现时机（未被读取的过期实例在库中仍是 10），不影响任何可助力判定：助力的条件更新自带 `expire_at > NOW()`。
- 终态不可重复推进：成功/失败/过期后助力一律 `23007`；重复放弃 `23009`；越权或不存在 `23004`。
- 过期后 `active_key` 变 NULL，允许对同一活动重新发起。

### 3.10 API、权限与错误码

| 方法 | 路径 | 认证/权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/bargain-activities` | 公开 | 可参与活动分页列表（启用且在时间窗内） |
| GET | `/bargain-activities/:id` | 公开 | 活动详情 + `is_joinable` |
| POST | `/bargain-activities/:id/instances` | 用户 JWT | 发起砍价 |
| GET | `/my/bargain-instances` | 用户 JWT | 我的砍价记录（分页） |
| GET | `/bargain-instances/:id` | 用户 JWT | 实例详情（登录用户均可查看，便于好友打开分享） |
| GET | `/bargain-instances/:id/helps` | 用户 JWT | 助力记录（含助力者昵称/头像） |
| POST | `/bargain-instances/:id/help` | 用户 JWT | 好友助力 |
| POST | `/bargain-instances/:id/abandon` | 用户 JWT | 放弃砍价（仅发起者、仅进行中） |
| POST | `/admin/bargain-activities` | `bargain:create` | 创建活动 |
| GET | `/admin/bargain-activities` | `bargain:list` | 后台分页列表（状态/商品筛选） |
| GET | `/admin/bargain-activities/:id` | `bargain:view` | 后台详情 |
| PUT | `/admin/bargain-activities/:id` | `bargain:update` | 编辑活动 |
| POST | `/admin/bargain-activities/:id/status` | `bargain:update` | 启用/停用（幂等） |

错误码（域序 21，`21000-21999`；`20000-20999` 已被 B1-04-03 运费/计价占用）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 23001 | 砍价活动不存在 | 404 |
| 23002 | 活动参数非法（价格/时间/商品/SKU/助力参数/改绑商品） | 400 |
| 23003 | 活动不可参与（停用/未开始/已结束） | 409 |
| 23004 | 实例不存在或不属于当前用户 | 404 |
| 23005 | 已存在进行中的实例 | 409 |
| 23006 | 不能给自己的砍价助力 | 409 |
| 23007 | 实例不可助力（终态/过期/次数用尽/已到底价） | 409 |
| 23008 | 已助力过该实例 | 409 |
| 23009 | 实例状态迁移非法（终态重复推进） | 409 |

### 3.11 与订单模块的边界

- 本期**不创建订单**：下单需要「按砍价成交价计价」的能力，属于订单金额公式/计价范围（B1-04-03 与后续计价集成任务），
  本任务明确不修改 `internal/logic/pricing`、不改写 `orders` 金额语义。
- `bargain_instances.order_id` 保持 NULL；`uk_order_id` 已就位，后续集成任务在下单成功后回填即可（一个订单只能对应一个实例）。
- 砍价成功只表示「价格已砍到目标价」，不锁库存、不占库存、不产生支付或履约状态；库存扣减仍走普通下单链路。

## 4. 跨模块边界

| 对象 | 关系 | 说明 |
| --- | --- | --- |
| `products` / `skus` | 活动 `product_id`/`sku_id` FK RESTRICT | 被活动引用的商品/SKU 不可删除；活动不复制商品名/图片/库存 |
| `users` | `user_id`/`leader_user_id`/`helper_user_id` 软引用 | 沿用项目「用户表不做物理 FK」约定；用户删除不影响活动历史 |
| `orders` | 参与记录 `order_id`、砍价实例 `order_id` 软引用 + 唯一索引 | 订单删除后营销记录仍可读；一个订单只能参团一次/砍价一次 |
| `inventories` / `inventory_logs` | **无引用** | 拼团/砍价不建库存、不扣库存；成交仍走现有库存链路 |
| `flash_sale_*` | **无引用** | 秒杀库存与拼团/砍价完全隔离，不共享 `sold`/`total_stock` |
| `orders` 金额 | **不改写** | 拼团价/砍价成交价由 B1-04 在下单时写入现有订单金额快照；本任务不新增订单列 |

- 拼团/砍价的订单识别通过参与记录/实例上的 `order_id` 反查，不在 `orders` 加 `combination_id`/`bargain_id`；
  这样既不破坏既有订单语义，也避免历史 SQL 的冗余列。
- 同一订单不允许既参团又砍价：由 B1-04 在下单入口做互斥校验；本 Schema 只保证各自表内订单唯一。

## 5. 不变量

### 拼团

- INV-GB01（价格与人数快照）：`group_buy_teams.group_price`/`required_size` 在开团时固定，不随活动修改漂移。
- INV-GB02（容量）：`joined_count ≤ required_size`，由条件更新 + `INT UNSIGNED` 保证；不得用 `COUNT(*)` 事后补救并发。
- INV-GB03（一人一团一次）：`uk_team_user` 保证同一用户在同一团至多一条参与记录。
- INV-GB04（一单一团一次）：`uk_order_id` 保证一个订单只能参团一次（多个 NULL 允许）。
- INV-GB05（团长归属）：团长必须有参与记录且 `user_id = leader_user_id`（应用事务保证；DB 不设冗余列）。
- INV-GB06（状态机）：团队仅 `10→20/30/40` 合法；终态不可回退；`closed_at` 只写一次。
- INV-GB07（活动引用）：有团的活动不可删除（FK RESTRICT）；活动停用不影响已开的团。

### 砍价

- INV-BG01（实例快照）：`start_price`/`floor_price`/`max_help_count`/`expire_at` 在实例创建时固定，不随活动修改漂移。
- INV-BG02（进行中唯一）：`uk_active_instance` 保证同一用户同一活动至多一个进行中实例；失败/过期后可重开。
- INV-BG03（一单一实例）：`uk_order_id` 保证一个订单只能对应一个砍价实例（多个 NULL 允许）。
- INV-BG04（助力去重）：`uk_instance_helper` 保证同一用户对同一实例至多助力一次。
- INV-BG05（次数上限）：`helped_count ≤ max_help_count`，由条件更新 + `INT UNSIGNED` 保证。
- INV-BG06（价格边界）：`current_price ≥ floor_price` 且 `current_price ≤ start_price`；最后一次 `cut_amount`
  可小于 `help_amount`，但 `price_after = price_before - cut_amount` 且不低于 `floor_price`。
- INV-BG07（状态机）：实例仅 `10→20/30/40` 合法；`completed_at` 只在成功时写入。
- INV-BG08（活动引用）：有实例的活动不可删除（FK RESTRICT）；活动停用不改变已创建实例的历史。
- INV-BG09（禁止自助）：实例发起者不能给自己的实例助力（应用层校验 `user_id != helper_user_id`，返回 `23006`，
  且不产生助力记录、不改价格）。
- INV-BG10（活动可达成）：`ceil((start_price - floor_price) / help_amount) ≤ max_help_count`，否则活动创建/编辑被拒（`23002`），
  避免上架一个永远无法成功的活动。

## 6. Migration

| 版本 | 文件 | 内容 |
| --- | --- | --- |
| `20261110000011` | `group_buy` | `group_buy_activities` + `group_buy_teams` + `group_buy_participants`（含 down） |
| `20261110000012` | `bargain` | `bargain_activities` + `bargain_instances` + `bargain_help_records`（含 down） |

两组均只新增表，不改既有表；down 只删除本组表，破坏性（数据丢失）在各 down 文件顶部说明。
真实结构与约束由 `internal/migrations/marketing_schema_migration_test.go` 在真实 MySQL 上验证。

## 7. Deferred / 已知留白

- 拼团：开团/参团/成团/失败/过期与相关 API 本次未集成（B1-04-06 分支在仓库中不存在，只有 §2 的表结构）。
- 砍价：活动管理、用户发起、好友助力、并发控制、最低价保护与懒过期已实现（B1-04-07）；砍价成功后创建订单仍是待办，业务规则未冻结前不实现。
- 拼团/砍价的下单计价、Mock 支付联动、取消/退款后的团状态与价格恢复；砍价 `order_id` 回填与砍价成交价下单。
- 活动库存/配额、拼团失败自动退款、砍价成功后的下单有效期。
- 砍价过期**只做读取路径懒推进**，没有独立扫描器：无人读取的过期实例会在库中保持 `status=10`（状态呈现时机差异，
  不影响助力与成功判定）。若后续需要「到期即对外不可见/可统计」，再按现有 `internal/worker` ticker 模式补扫描器，
  不需要 MQ/Redis。
- 防刷规则（同一 IP/设备、助力者与发起人关系限制、黑名单）。
- 管理端/前台页面（本任务只交付后端 API 与 OpenAPI 契约）。
- 拼团/砍价订单的退款规则（是否退券、是否恢复活动名额）需 Owner 决策后写入 `refund.md` 与本文。
- `bargain_activities` 无「逻辑删除」：有实例的活动受 FK RESTRICT 保护，只能停用，不能删除。
