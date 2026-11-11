# 拼团与砍价设计（Marketing）

本文说明拼团与砍价的长期数据模型、状态、跨模块边界与关键不变量。
**B1-03-03 只落数据库结构**；**B1-04-06 已实现拼团业务**（活动管理、开团/参团、成团、超时过期、
运营关闭，与订单/库存/退款协同），砍价业务仍未实现（留给 B1-04-07）。任何实现必须先遵守本文的不变量。

## 1. 职责与边界

- 拼团回答「哪个 SKU 以什么拼团价、几人成团、团的有效期、谁开团、谁参与、团成功还是失败/过期」。
- 砍价回答「哪个 SKU 的原始价/目标价是多少、用户实例当前价是多少、谁助力过、还能助力几次、实例成功/失败/过期」。
- 事实来源：单一 MySQL。Redis 仅会话；不引入 MQ、分布式锁或跨系统事务。
- 不拥有库存：拼团/砍价都**不建活动库存列**，也不读写 `inventories`/`inventory_logs`/`flash_sale_*`。
  成交时的库存扣减仍走现有普通库存链路（`IInventory.DeductInTx`），秒杀库存模型完全不受影响。
- 不拥有支付：Mock 支付沿用 `orders` 的现有支付状态；拼团/砍价只通过 `order_id` 关联订单。
- 不拥有订单金额：订单金额仍由 `orders`/`order_items` 快照承载；营销活动只在自身表保存价格快照，
  不新增 `orders.*_id` 列，也不改写既有订单金额语义。

### 1.1 B1-04-06 拼团实现范围（事实）

- 管理端：创建/修改/启停活动、活动列表与详情、团列表与详情、运营关闭团。
- 用户端：活动列表/详情、进行中团列表、开团、参团、团详情、我的拼团记录。
- 状态推进：开团（10）→ 满员成团（20）/ 运营关闭（30）/ 超时过期（40），全部为条件更新。
- 订单：每次开团/参团在同一事务内创建一条普通订单（`orders`/`order_items`），
  `group_buy_participants.order_id` 唯一关联；不新增订单表、不新增订单状态机。
- 失败补偿：团进入 30/40 时，同事务取消该团所有**待支付**订单并恢复库存；
  **已支付订单不自动退款、也不伪造退款**（见 §2.8）。
- 未实现：活动库存/配额、团长主动退出/解散（仅运营关闭）、拼团成功后自动支付、
  已支付订单的系统自动退款、退款后回补团名额、前端页面与联调。

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

### 2.6 拼团订单与价格快照（B1-04-06 落地）

拼团不新增订单表，也不新增 `orders` 列；每次开团/参团都在**同一事务**内创建一条普通订单：

| 订单字段 | 拼团写入值 | 说明 |
| --- | --- | --- |
| `orders.original_amount` | `skus.price × 1` | 服务端重读 SKU 现价，不信任客户端；保持既有「优惠前金额」语义 |
| `orders.discount_amount` | `(skus.price - group_price) × 1` | 拼团优惠额；不引入第二套金额体系 |
| `orders.total_amount` | `group_price × 1` | 应付金额 |
| `orders.shipping_fee` | 0（默认） | 当前阶段无运费 |
| `order_items.price` | `group_price` | 成交价快照；后续活动改价不影响已生成订单 |
| `orders.status` | 10（待支付） | 与普通订单一致，支付/取消/退款复用既有状态机 |

- 一人一份：`quantity` 固定为 1（拼团语义），客户端不能指定数量。
- 拼团价由服务端从**团快照**读取（`group_buy_teams.group_price`）或活动快照派生；
  下单时若 `group_price > skus.price`（活动创建后 SKU 降价），拒绝下单（21004），
  不产生「负优惠」或零优惠订单。
- 订单创建复用 `IOrder.CreateGroupBuyInTx`（事务内方法），内部仍走
  `resolveLine`（SKU enabled + 商品 on_shelf + 重读价格）与 `IInventory.DeductInTx`；
  不创建第二套订单/支付系统，不绕过商品价格校验与库存管理。
- `group_buy_participants.order_id` 唯一（`uk_order_id`），一个订单只能参团一次；
  `participants` 的 `order_id` 是订单与拼团关系的唯一事实，不在 `orders` 增加拼团列。

### 2.7 事务边界

| 操作 | 单事务内容 | 失败语义 |
| --- | --- | --- |
| 开团 | `FOR UPDATE` 读活动并校验有效期 → 写团（价格/人数/有效期快照）→ 写订单 + 订单项 → 扣库存 → 写团长参与记录 | 任一失败整体回滚：不留下团、订单、库存扣减或参与记录 |
| 参团 | 条件更新 `joined_count + 1`（容量闸门）→ 重读团 → 写订单 + 订单项 → 扣库存 → 写参与记录 → 若满员条件更新 `status=20` | 唯一键冲突（`uk_team_user`）整体回滚，计数器一并回滚 |
| 超时过期 | 条件更新 `status=40, closed_at=NOW() WHERE status=10 AND expire_at<=NOW()` → 逐条取消未支付订单（条件 `status=10→60` + 恢复库存 + 释放券） | 单团失败只记录日志并跳过，其他团继续；已提交事务不回滚 |
| 运营关闭 | 条件更新 `status=30, closed_at=NOW() WHERE status=10` → 同上取消未支付订单 | 非 `pending` 团返回 21009，无副作用 |

**订单一致性**：订单创建与参与记录在同一事务，禁止「订单成功但参与记录缺失」；
也禁止先建订单后异步补参与记录。

### 2.8 超时、失败补偿与退款边界

- 超时推进：后台扫描器 `worker.StartGroupBuyExpireScanner`（周期 `group_buy.expire_scan_interval`，
  默认 60s）调用 `IGroupBuy.ExpireTeams`，逐团条件更新为 `40=expired`。
  多实例并发由条件更新 + `RowsAffected` 保证同一团只被推进一次。
- 失败补偿（30/40 共用 `terminateTeam`）：
  - **待支付订单（status=10）**：复用 `IOrder.CancelInTx` 条件取消（`10→60`）并
    `IInventory.IncreaseInTx` 恢复库存，同时释放已占用优惠券（与普通订单取消一致）；
  - **已支付订单（status=20）**：保持 `paid` 不动，**不自动退款、不改订单状态、不伪造退款成功**；
    用户通过现有 `POST /orders/:id/refund` 发起退款，由退款模块审核/执行，
    成功后由 `IOrder.CompleteRefundInTx` 推进 `20→70` 并恢复库存。
  - 已发货/已收货订单不在拼团失败补偿范围（拼团订单为单 SKU 单件，正常履约后不因团失败回滚）。
- 参与记录不回写：团 `status` 是团队结果的权威事实；`group_buy_participants.status`
  只表达有效/退出，终态不逐行改写（保留历史参与事实）。
- 超时与参团竞争：参团条件要求 `expire_at > NOW()`，过期推进条件要求 `expire_at <= NOW()`，
  两者在同一团队行上互斥，不会出现「已过期但仍参团成功」或「已成团又过期」。

### 2.9 并发控制要点

| 场景 | 控制手段 | 结果 |
| --- | --- | --- |
| 多人同时抢最后名额 | 条件更新 `joined_count < required_size` + 行锁串行化 | 成功数恰好等于剩余名额，`joined_count ≤ required_size` |
| 同一用户并发重复参团 | `uk_team_user` 唯一键 + 事务回滚 | 至多一条参与记录、一个订单、一次库存扣减 |
| 同一订单重复参团 | `uk_order_id` 唯一键 | 订单与参与一对一 |
| 重复成团 | `status=20 WHERE status=10 AND joined_count>=required_size` | 只有一个事务命中 |
| 成团与超时同时发生 | 同一行上的 `status=10` 条件更新 | 只有一个终态成立 |
| 开团与活动停用/改价 | 活动行 `FOR UPDATE` | 团快照稳定，不受并发编辑影响 |
| 多实例过期扫描 | 条件更新 + `RowsAffected` | 同一团只补偿一次订单/库存 |

容量判定禁止「先 `SELECT COUNT(*)` 再写入」：测试 `TestGroupBuyConcurrentJoinCapacity`
用 10 个真实用户竞争 2 个名额，错误实现会 `success=10`，正确实现必须 `success=2`。

### 2.10 API、权限与错误码

前台（公开只读 + 登录写）：

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/group-buys` | 公开 | 进行中活动分页列表 |
| GET | `/group-buys/:id` | 公开 | 活动详情（含已结束/停用，便于历史追溯） |
| GET | `/group-buys/:id/teams` | 公开 | 该活动进行中的团 |
| POST | `/group-buys/:id/teams` | 用户 JWT | 开团 + 下单 |
| POST | `/group-buy-teams/:id/join` | 用户 JWT | 参团 + 下单 |
| GET | `/group-buy-teams/:id` | 用户 JWT | 团详情（含成员用户名与本人参与） |
| GET | `/my/group-buys` | 用户 JWT | 我的拼团记录 |

后台（`AdminAuth + RequirePermission`）：

| 方法 | 路径 | 权限 code |
| --- | --- | --- |
| POST | `/admin/group-buys` | `group_buy:create` |
| PUT | `/admin/group-buys/:id` | `group_buy:update` |
| POST | `/admin/group-buys/:id/status` | `group_buy:update` |
| GET | `/admin/group-buys` | `group_buy:list` |
| GET | `/admin/group-buys/:id` | `group_buy:view` |
| GET | `/admin/group-buy-teams` | `group_buy:list` |
| GET | `/admin/group-buy-teams/:id` | `group_buy:view` |
| POST | `/admin/group-buy-teams/:id/close` | `group_buy:close` |

错误码域 `21000-21999`（域序 21，见 `.agent/registry/error-codes.md`）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 21001 | 拼团活动不存在 | 404 |
| 21002 | 活动/参团参数非法（价格、人数、有效期、时间窗、SKU 归属） | 400 |
| 21003 | 活动未开始/已结束/已停用，不可开团 | 409 |
| 21004 | 拼团价非法（≤0 或高于 SKU 现价） | 400 |
| 21005 | 团不存在 | 404 |
| 21006 | 团已满/已成团/已失败/已过期，不可参与 | 409 |
| 21007 | 该用户已在本团中（唯一键兜底） | 409 |
| 21008 | 拼团商品不可购买（SKU 禁用/商品下架） | 409 |
| 21009 | 团不在拼团中，不可关闭 | 409 |

复用：`9006`（订单幂等键冲突）、`9003`/`5001`（转换为 21008）、`6001`（库存不足）、
`7001`（地址不存在/非本人）、`18002`（退款状态迁移非法）、`1001`/`1002`/`1003`。

### 2.11 配置

| 配置键 | 环境变量 | 默认 | 说明 |
| --- | --- | --- | --- |
| `group_buy.expire_scan_interval` | `GROUP_BUY_EXPIRE_SCAN_INTERVAL` | 60 | 过期扫描周期（秒），由 `worker.StartGroupBuyExpireScanner` 读取 |

### 2.12 已知留白（B1-04-06 范围内）

- 活动不设库存/配额：热门活动可能产生大量拼团订单，库存仍由 SKU 库存兜底（不足返回 6001）。
- 无团长退出/解散接口：仅运营关闭（`group_buy:close`）；成员不能主动退出已参与的团。
- 已支付订单的自动退款未实现：需要退款模块提供「系统发起 + 自动执行」能力（当前仅用户发起 + 人工审核/执行）。
- 拼团成功后不自动支付：订单仍需用户按普通订单流程支付。
- 退款成功后不回补团名额：`joined_count` 不回退，团终态与参与历史不变。

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

## 4. 跨模块边界

| 对象 | 关系 | 说明 |
| --- | --- | --- |
| `products` / `skus` | 活动 `product_id`/`sku_id` FK RESTRICT | 被活动引用的商品/SKU 不可删除；活动不复制商品名/图片/库存 |
| `users` | `user_id`/`leader_user_id`/`helper_user_id` 软引用 | 沿用项目「用户表不做物理 FK」约定；用户删除不影响活动历史 |
| `orders` | 参与记录 `order_id`、砍价实例 `order_id` 软引用 + 唯一索引 | 订单删除后营销记录仍可读；一个订单只能参团一次/砍价一次。拼团已由 B1-04-06 在开团/参团同事务写入订单并回填 `order_id` |
| `inventories` / `inventory_logs` | **无引用** | 拼团/砍价不建库存、不扣库存；成交仍走现有库存链路（拼团下单经 `IInventory.DeductInTx`，失败补偿经 `IncreaseInTx`） |
| `flash_sale_*` | **无引用** | 秒杀库存与拼团/砍价完全隔离，不共享 `sold`/`total_stock` |
| `orders` 金额 | **不改写语义** | 拼团价以 `discount_amount`（原价与拼团价差额）+ `total_amount`（拼团价）表达，仍满足 `total = original - discount`；不新增订单列 |

- 拼团/砍价的订单识别通过参与记录/实例上的 `order_id` 反查，不在 `orders` 加 `combination_id`/`bargain_id`；
  这样既不破坏既有订单语义，也避免历史 SQL 的冗余列。
- 同一订单不允许既参团又砍价：由 B1-04 在下单入口做互斥校验；本 Schema 只保证各自表内订单唯一。

## 5. 不变量

### 拼团

- INV-GB01（价格与人数快照）：`group_buy_teams.group_price`/`required_size` 在开团时固定，不随活动修改漂移。
  *实现*：开团事务内 `FOR UPDATE` 读活动并写入快照；参团只读团快照。
- INV-GB02（容量）：`joined_count ≤ required_size`，由条件更新 + `INT UNSIGNED` 保证；不得用 `COUNT(*)` 事后补救并发。
  *实现*：`UPDATE ... SET joined_count=joined_count+1 WHERE status=10 AND joined_count<required_size AND expire_at>NOW()`；
  `TestGroupBuyConcurrentJoinCapacity`（10 用户抢 2 名额）为回归证据。
- INV-GB03（一人一团一次）：`uk_team_user` 保证同一用户在同一团至多一条参与记录。
  *实现*：冲突 1062 回滚并返回 21007；`TestGroupBuyConcurrentDuplicateJoin` 为回归证据。
- INV-GB04（一单一团一次）：`uk_order_id` 保证一个订单只能参团一次（多个 NULL 允许）。
  *实现*：下单与参与同事务，参与失败即回滚订单。
- INV-GB05（团长归属）：团长必须有参与记录且 `user_id = leader_user_id`（应用事务保证；DB 不设冗余列）。
  *实现*：开团事务内写团后立即写团长参与记录。
- INV-GB06（状态机）：团队仅 `10→20/30/40` 合法；终态不可回退；`closed_at` 只写一次。
  *实现*：三种终态均为条件更新（`WHERE status=10` + 各自前置条件），命中者才写 `closed_at`。
- INV-GB07（活动引用）：有团的活动不可删除（FK RESTRICT）；活动停用不影响已开的团。
  *实现*：参团不校验活动启停/时间窗，只校验团状态、团截止时间与 SKU 可售性。
- INV-GB08（订单一致性，B1-04-06 新增）：`group_buy_participants.order_id` 必须指向同事务创建的订单；
  禁止存在「有订单无参与」或「有参与无订单」的团记录。
- INV-GB09（失败补偿只一次）：团终态推进与未支付订单取消均为条件更新，
  多实例/重复扫描不会重复取消订单或重复恢复库存。

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

## 6. Migration

| 版本 | 文件 | 内容 |
| --- | --- | --- |
| `20261110000011` | `group_buy` | `group_buy_activities` + `group_buy_teams` + `group_buy_participants`（含 down） |
| `20261110000012` | `bargain` | `bargain_activities` + `bargain_instances` + `bargain_help_records`（含 down） |

两组均只新增表，不改既有表；down 只删除本组表，破坏性（数据丢失）在各 down 文件顶部说明。
真实结构与约束由 `internal/migrations/marketing_schema_migration_test.go` 在真实 MySQL 上验证。

**B1-04-06 未新增或修改任何 migration**：直接复用 `20261110000011_group_buy` 的三张表；
现有字段（`group_price`/`required_size`/`joined_count`/`expire_at`/`closed_at`/`order_id`）
足以承载已批准业务，无需扩大数据库变更范围。

## 7. Deferred / 已知留白

已由 B1-04-06 完成：拼团下单计价与订单关联、开团/参团/成团/过期/运营关闭、超时扫描器、
管理端 API/权限码/错误码/OpenAPI、拼团订单与库存/取消/退款的协同。

仍未实现：

- 砍价全部业务（B1-04-07）：实例/助力/成功判定/超时与订单关联。
- 活动库存/配额；拼团失败时已支付订单的**自动**退款（依赖退款模块的系统发起能力）。
- 团长主动退出/解散、成员退出、退款后回补团名额。
- 防刷规则（同一 IP/设备、黑名单、频率限制）。
- 前端页面与前后端联调。
- 拼团专属退款规则 Owner 决策（当前完全复用普通订单退款：整单全额、退券规则不变、不回补名额）。
