# 一级分销设计（Distribution）

本文说明一级推荐关系与订单佣金记录的长期数据模型、绑定/计佣/冲回规则、状态机、并发与不变量、公开 API 与权限边界。
**B1-03-03 落了数据库结构，B1-04-05 落地了绑定、计佣、退款冲回与用户端/后台只读 API**。
本项目暂定**只做一级分销**，不实现多级分销、佣金账户、提现、真实资金结算与资金追偿。
「人工结算」只表示把佣金记录标记为已结算（`status 10→20` + `settled_at`），**不代表已完成打款**。

## 1. 职责与边界

- 推荐关系回答「用户是谁推荐来的、一级推荐人是谁、关系何时绑定、能否改绑」。
- 佣金记录回答「哪个订单、买家是谁、受益推荐人是谁、按什么基数/比例计了多少佣金、当前什么状态、退款后冲回多少」。
- 事实来源：单一 MySQL。`user_referrals`（关系）、`distribution_commissions`（佣金）。
- 不负责：订单金额与支付（`order.md`）、退款状态机（`refund.md`）、钱包余额与流水（`wallet.md`）、佣金提现。
- 不建佣金账户：佣金记录是**应收/已结算事实**，不是可消费余额；如需提现，必须另建账户与流水模型并由 Owner 决策。
- 不引入 MQ、Redis 缓存、分布式事务；计佣与冲回都在调用方已开启的 MySQL 事务内完成（`*InTx` 方法不自开事务）。
- 计佣对象只有普通订单（`orders`）。秒杀订单（`flash_sale_orders`）无支付语义，不进入计佣链路。

## 2. 推荐关系模型

### 2.1 `user_referrals`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，被推荐人（下级），软引用 `users.id`；`uk_user_id` 唯一 |
| `referrer_user_id` | BIGINT UNSIGNED | 非空，一级推荐人，软引用 `users.id` |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即绑定时间 |

索引：`PRIMARY(id)`、`uk_user_id(user_id)` UNIQUE、`idx_referrer_user(referrer_user_id, created_at)`。

- 一人一行、关系一次性绑定、不可改绑：`user_id` 唯一是机械保证；没有 UPDATE 业务路径。
- 只存一级：`referrer_user_id` 直接指向最终推荐人；不存 `parent_id` 链、不存层级路径。
- `created_at` 即绑定时间，不再冗余 `bound_at`。
- 用户软引用（无 FK）：沿用项目约定，用户删除不影响历史关系；推荐人是否存在由绑定事务校验。

### 2.2 绑定规则（已实现）

绑定入口 = 登录用户主动提交邀请人：`POST /distribution/referral`，请求体只含 `referrer_user_id`。

| 规则 | 校验方式 | 失败语义 |
| --- | --- | --- |
| 被推荐人只能是当前登录用户 | 取 `Principal.UserID`，请求体不承载被推荐人身份 | 未认证 401/1002 |
| 不能自荐 | `userID == referrerUserID` | 400/20001 |
| 推荐人必须存在且 `users.status=1` | 绑定前 `SELECT COUNT(*) FROM users WHERE id=? AND status=1` | 404/20002 |
| 一人最多一个上级 | 先读已有关系；没有则插入，`uk_user_id` 兜底并发 | 已绑定他人 → 409/20003 |
| 不允许改绑 | 已存在关系时只比较 `referrer_user_id`，不执行 UPDATE | 同上 |
| 重复绑定同一推荐人 | 已存在关系且推荐人相同 → 原样返回既有关系 | 200（幂等） |

并发语义（B1-04-FIX-01 起）：推荐关系图变更由 MySQL 全局锁串行化——绑定事务的**第一条语句**对
`referral_graph_guard`（单行 `id=1`）执行 `SELECT ... FOR UPDATE`；锁随事务提交/回滚释放，
跨 goroutine、跨连接、跨 Go 实例共用同一把 InnoDB 行锁。锁内一律使用**当前读**（`SELECT ... FOR UPDATE`）
重新读取本人关系与整条推荐链，再完成成环检查与写入，因此不存在「两个事务各自通过环检查后双双提交」的窗口。
`uk_user_id` 仍是「一人一个上级」的最终机械保证：若存在绕过锁协议的写入方，1062 会让事务整体回滚，
再由调用方在新快照中判定幂等/冲突（同一推荐人 200 / 不同推荐人 20003）。

成环校验（B1-04-FIX-01）：**写入前**在同一事务内从 `referrer` 沿 `referrer_user_id` 向上追溯（深度上限 200），
若能到达 `child`（即本次绑定的被推荐人）则返回 409/20004 并回滚，不写入任何关系行。
链深度超限、或既有关系图本身已含环时同样拒绝（fail-closed，不在脏数据上继续扩展）。

锁竞争与失败语义：并发绑定在全局锁上排队（InnoDB 行锁，等待上限由 `innodb_lock_wait_timeout` 决定）。
锁等待超时 / 死锁由 MySQL 报错，绑定返回内部错误并整体回滚（不留关系、不降级为无锁）；
客户端可安全重试，重复提交按幂等语义返回既有关系。锁记录被误删或迁移未执行时同样 fail-closed（500），
绝不会在无串行化保障下写入关系。

邀请码/邀请链接：当前 Go 侧没有邀请码生成、附件码（Java 的 `system_attachment.code`）或短链基础设施，
因此本任务只支持「提交推荐人 user id」这一种邀请标识，见 §8 Deferred。

### 2.3 `referral_graph_guard`（关系图变更锁，B1-04-FIX-01）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | TINYINT UNSIGNED | 主键，固定为 `1`（全表唯一锁记录） |
| `created_at` | DATETIME | 记录初始化时间，仅运维参考 |

- 迁移：`20261111000004_referral_graph_guard`（up 建表并初始化 `id=1`；down 删除该表）。
- 只用于 `SELECT ... FOR UPDATE`，不承载业务数据、不建外键、不接受业务写入。
- 生命周期：随 migration 创建/删除；无定期清理、无轮转、无租约（锁随事务结束释放，不需要过期时间）。
- 缺失语义：锁记录不存在时绑定 fail-closed（返回内部错误，拒绝写入），不做「无锁降级」。

## 3. 佣金模型

### 3.1 `distribution_commissions`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，FK → `orders.id` ON DELETE RESTRICT；`uk_order_id` 唯一 |
| `buyer_user_id` | BIGINT UNSIGNED | 非空，买家 user id 快照（软引用） |
| `beneficiary_user_id` | BIGINT UNSIGNED | 非空，受益推荐人 user id 快照（软引用） |
| `base_amount` | INT UNSIGNED | 非空，计佣基数快照（整数分） |
| `rate_bp` | INT UNSIGNED | 非空，佣金比例快照（万分比，500=5%） |
| `commission_amount` | INT UNSIGNED | 非空，佣金金额快照（整数分） |
| `status` | TINYINT | 非空默认 10，`10=待结算`、`20=已结算`、`30=已冲回` |
| `settled_at` | DATETIME | 可空，人工结算时间（`POST /admin/distributions/commissions/:id/settle` 写入；不代表真实资金转账） |
| `reversed_amount` | INT UNSIGNED | 非空默认 0，已冲回金额（整单退款时等于 `commission_amount`） |
| `reversed_at` | DATETIME | 可空，冲回时间 |
| `refund_id` | BIGINT UNSIGNED | 可空，触发冲回的退款单 id（软引用 `refund_requests.id`）；`uk_refund_id` 唯一 |
| `reversal_reason` | VARCHAR(32) | 非空默认 ''，冲回原因：`refund`（当前唯一实现值） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_order_id(order_id)` UNIQUE、`uk_refund_id(refund_id)` UNIQUE、
`idx_beneficiary_status(beneficiary_user_id, status, created_at)`、`idx_status_created(status, created_at)`。

- `uk_order_id` 是「防止重复计提」的最终幂等约束：一个订单至多一条佣金记录，重复计提返回 1062。
  不额外引入 `idempotency_key`，因为它与订单唯一键表达同一规则。
- `uk_refund_id` 保证同一退款单至多冲回一条佣金；MySQL 唯一索引允许多个 NULL（未冲回记录）。
- `base_amount`/`rate_bp`/`commission_amount` 是计算快照；后续改配置、改订单金额不得回写历史佣金。
- `buyer_user_id`/`beneficiary_user_id` 是身份快照；用户改名/删除不影响历史记录。
- `order_id` 物理 FK RESTRICT：佣金是资金事实，订单在佣金存在时不可删除；退款记录用软引用，
  避免退款表删除级联影响佣金历史。

### 3.2 计佣口径（已实现）

计佣发生在**订单支付成功事务内**：`internal/logic/order/order.go` 的 `Pay` 在「订单 10→20」条件更新命中后、
同事务内调用 `service.Distribution().AccrueOrderCommissionInTx(ctx, tx, orderID)`。

```text
base_amount       = orders.total_amount（实付金额快照，整数分，服务端派生）
rate_bp           = distribution.commission_rate_bp（配置，万分比）
commission_amount = floor(base_amount × rate_bp / 10000)   // 整数运算，向下取整到分
```

实现约束：

- **比例来源是配置**：`distribution.commission_rate_bp`（环境变量 `DISTRIBUTION_COMMISSION_RATE_BP`，
  默认 `0` = 关闭计佣）。代码里不硬编码任何比例；未配置比例时不产生任何佣金事实。
- **比例合法区间** `[0, 10000]`；越界视为配置非法，计佣返回技术错误并回滚整笔支付事务（fail-closed），
  不静默跳过以免留下「订单已支付但佣金口径不明」的状态。
- **基数固定为订单实付金额** `orders.total_amount`（已含运费、已扣优惠），不是商品原价、不是 `original_amount`。
  理由：佣金对应实际到账资金，`total_amount` 是唯一服务端派生的应付快照，且在下单后不可变。
- **禁止浮点**：全程 `int64` 整数运算，先乘后除，避免精度误差；`base ≤ 4.29e9`、`rate ≤ 10000`，
  `base × rate` 不会溢出 `int64`。
- **不计佣的情形**（均为 no-op，不写记录、不报错）：买家没有一级推荐人；比例为 0；基数 ≤ 0；
  计算结果不足 1 分。
- 金额快照在插入时固化；订单后续退款/改价不回写 `base_amount`/`rate_bp`/`commission_amount`。
- 同事务写入保证「订单状态推进」与「佣金记录」原子：计佣失败 → 整个支付事务回滚，
  不会出现「订单已支付但佣金缺失或错账」。

### 3.3 佣金状态机

| from | to | 触发 | 说明 |
| --- | --- | --- | --- |
| （无） | 10 待结算 | 订单支付成功计佣 | `settled_at` 为空 |
| 10 待结算 | 20 已结算 | 后台人工结算 `POST /admin/distributions/commissions/:id/settle`（持 `distribution:settle`） | 写 `settled_at=NOW()`；只做状态流转，**不代表真实资金转账** |
| 10 待结算 | 30 已冲回 | 退款成功（`refund.md` 执行成功事务） | 写 `reversed_amount`/`reversed_at`/`refund_id`/`reversal_reason` |
| 20 已结算 | 30 已冲回 | 退款成功 | 同上；已结算佣金也必须可冲回 |

终态：30。`20→30` 允许，`30→20/10` 不允许（冲回后不会被任何代码路径改回）。
B1-04 集成为 10→20 提供**后台人工结算**入口（`distribution:settle`，幂等）；
自动结算任务、触发条件（订单完成/售后期结束）与结算批次语义仍未定义，属 Deferred（§8）。

## 4. 退款冲回机制

退款执行成功时，在**同一个事务**内冲回佣金：`internal/logic/refund/refund.go` 的 `Execute`（success 分支）
在「订单 20→70 + 恢复库存」与「`refund_requests` → 60」都成功后、写审计之前调用
`service.Distribution().ReverseOrderCommissionInTx(ctx, tx, refundID, orderID)`。

```sql
UPDATE distribution_commissions
   SET status = 30,
       reversed_amount = commission_amount,
       reversed_at = NOW(),
       refund_id = ?,
       reversal_reason = 'refund'
 WHERE order_id = ?
   AND status IN (10, 20);
```

不变量：

- 只冲回一次：`WHERE status IN (10,20)` 条件更新 + `RowsAffected`；重复执行 0 行（终态 30 不可再次命中）。
- 同一退款单只冲回一条：`uk_refund_id` 兜底（1062 视为已冲回，幂等返回）。
- 冲回金额可审计：`reversed_amount` 由 SQL 引用 `commission_amount` 列写入，不经过应用层差值计算，
  因此恒等于原始佣金、不会超额；`commission_amount` 同步保留。
- 没有佣金记录（订单无推荐人/未计佣/比例关闭）时，冲回是 no-op，不伪造记录。
- 冲回不回写 `orders`/`refund_requests`/钱包；佣金状态是独立事实。
- 退款事务失败（例如订单已发货导致 `CompleteRefundInTx` 0 行）→ 整体回滚，佣金保持冲回前状态，
  不会出现「佣金已冲回但退款没成功」的部分终态。
- 订单取消（未支付）不产生佣金，也不触发冲回；未来若支持「已支付订单取消退佣金」，复用同一条件更新，
  `reversal_reason='cancel'`。

退款流程本身见 `refund.md`：V1 仅整单全额退款，退款成功推进订单状态并恢复库存；
佣金冲回是该事务的附加步骤，不改变退款状态机，也不扩展为部分退款/部分冲回。

## 5. 业务不变量

- INV-D01（一人一个推荐人）：`user_referrals.user_id` 唯一；不存在两条推荐关系。
- INV-D02（禁止自荐）：`user_id <> referrer_user_id`（应用校验 20001；DB 不引入 CHECK）。
- INV-D03（推荐人有效）：绑定前校验推荐人存在且 `users.status=1`（否则 20002）。
- INV-D04（不可改绑）：已存在关系时不执行 UPDATE；同一推荐人幂等返回，不同推荐人 20003。
- INV-D05（只做一级）：`referrer_user_id` 是最终推荐人；不存在多级链/层级路径字段。
- INV-D06（一单一佣金）：`distribution_commissions.uk_order_id` 保证订单至多一条佣金，重复计提 1062 视为已计提。
- INV-D07（金额快照）：`base_amount`/`rate_bp`/`commission_amount` 写入后不随配置或订单变化回写。
- INV-D08（整数金额）：金额列为 `INT UNSIGNED`，应用层用 `int64` 整数运算，禁止浮点；
  `commission_amount ≤ base_amount` 由 `rate_bp ≤ 10000` 保证。
- INV-D09（计佣时机）：只有在订单 `status=20` 时计佣，且与状态推进同事务；未支付/取消/退款订单不产生佣金。
- INV-D10（状态机）：仅 `10→20`、`10→30`、`20→30` 合法；`30` 终态，不允许回退。
- INV-D11（冲回一次）：`status IN (10,20)` 条件更新 + `RowsAffected`；`uk_refund_id` 兜底同一退款单重复冲回。
- INV-D12（冲回不超额）：`reversed_amount` 恒等于 `commission_amount`（SQL 列引用写入），不累加。
- INV-D13（订单保护）：有佣金记录的订单不可删除（FK RESTRICT）。
- INV-D14（数据归属）：用户端接口的用户身份只来自 `Principal.UserID`，客户端传参不改变归属。
- INV-D15（关系图无环，并发安全）：绑定事务先对 `referral_graph_guard.id=1` 取得全局排他行锁，
  锁内以当前读完成成环检查与写入；跨连接/跨实例并发互绑时，最终关系图仍必须无环，且最多一条成环边被拒绝（20004）。

## 6. 跨模块关系

- `user_referrals.user_id`/`referrer_user_id` → `users.id`（软引用，无 FK）。
- `distribution_commissions.order_id` → `orders.id`（FK RESTRICT）；
  `refund_id` → `refund_requests.id`（软引用，无 FK）；
  `buyer_user_id`/`beneficiary_user_id` → `users.id`（软引用，无 FK）。
- 计佣入口：订单支付成功事务内（`IOrder.Pay` → `IDistribution.AccrueOrderCommissionInTx`），
  先读 `user_referrals`（买家的一级推荐人），再按当时配置计算并插入佣金；`uk_order_id` 兜底重复回调。
- 冲回入口：退款成功事务内（`IRefund.Execute` success 分支 → `IDistribution.ReverseOrderCommissionInTx`），
  与订单状态推进、库存恢复同事务。
- 依赖方向：`logic/order`、`logic/refund` 只依赖 `internal/service`；`logic/distribution` 不反向依赖二者，无包循环。
- 钱包：佣金不直接入钱包，也不写 `wallet_transactions`；如后续需要「佣金余额」，必须新建佣金账户/流水
  或明确复用钱包的语义，并先更新 `wallet.md` 与本文。
- 建表经 golang-migrate `20261110000014_distribution.up.sql`（含 down）；B1-04-FIX-01 新增
  `20261111000004_referral_graph_guard.up.sql`（并发控制锁表，含 down），未修改任何历史 migration。

## 7. 公开 API、权限与错误码

### 7.1 用户端（Auth 分组，仅本人数据）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/distribution/referral` | 绑定我的上级推荐人（幂等；不可改绑） |
| GET | `/distribution/me` | 我的分销信息：上级关系、直接下级人数、佣金汇总 |
| GET | `/distribution/subordinates` | 我的直接下级列表（分页，clamp 归一） |
| GET | `/distribution/commissions` | 我的佣金记录（分页 + 可选 `status` 过滤） |

- 身份只来自 `Principal.UserID`；不存在任何接受 `user_id` 作为数据归属的用户端接口。
- 分页为 clamp 形态：`page<1 → 1`、`size<1 → 20`、`size>100 → 100`。
- 金额字段统一整数分（`base_amount`/`commission_amount`/`reversed_amount` 等）。

### 7.2 后台（AdminAuth + RequirePermission，全部只读）

| 方法 | 路径 | 权限码 | 说明 |
| --- | --- | --- | --- |
| GET | `/admin/distributions/referrals` | `distribution:referral_list` | 分销关系分页查询（`user_id`/`referrer_user_id` 过滤） |
| GET | `/admin/distributions/distributors` | `distribution:referral_list` | 推荐人维度汇总（下级人数 + 佣金笔数/金额） |
| GET | `/admin/distributions/commissions` | `distribution:commission_list` | 佣金记录分页查询（`order_id`/`buyer_user_id`/`beneficiary_user_id`/`status` 过滤） |
| GET | `/admin/distributions/commissions/stats` | `distribution:commission_list` | 按状态统计（固定 `pending`/`settled`/`reversed` 三项） |

- 后台分页为 strict 形态：`page<1`、`size<1`、`size>100`、非法 `status`、非正 id 过滤值 → 400/1001。
- **不提供**人工修改佣金状态、修改用户推荐关系、补发/撤销佣金等写接口：无业务依据（Java 参考实现缺失，
  见 `docs/reports/B1-04-05-distribution.md`）。后台仅有查询能力。

### 7.3 错误码（域 20000-20999，Registry 已登记 RESERVED）

| code | 常量 | 语义 | HTTP |
| --- | --- | --- | --- |
| 20001 | `CodeDistributionSelfReferral` | 不能绑定自己为推荐人 | 400 |
| 20002 | `CodeDistributionReferrerUnavailable` | 推荐人不存在或已禁用 | 404 |
| 20003 | `CodeDistributionAlreadyBound` | 已绑定其他推荐人，不可改绑 | 409 |

复用：`1001`（参数错误）、`1002`（未认证）、`1003`（无权限）、`1000`（内部错误）、`9002`（订单非法状态迁移）。

## 8. Deferred / 已知留白

- 多级分销、团队关系、层级佣金、平级返佣。
- 佣金账户、提现、结算批次、结算周期、税/手续费、发票。
- **自动佣金结算（10→20）**：触发条件（订单完成/售后期结束）、结算任务形态与批次语义未定义；当前只有后台人工触发，且不产生真实转账。
- 计佣比例的配置来源扩展（按商品/活动/等级差异化费率）：当前只有一个全局比例配置。
- 邀请码/邀请链接/分享海报/短链：缺少邀请码生成与解析基础设施（Java 参考实现用 `system_attachment.code`，
  Go 侧无对应表），本任务只支持提交推荐人 user id。
- 注册时绑定（`POST /register` 携带邀请人）：需要改 IAM 注册契约，本任务未改，绑定发生在登录后的显式接口。
- 退款失败/关闭时是否恢复佣金（V1 只有退款成功才冲回；失败/关闭不冲回、不恢复）。
- 已结算佣金已入钱包/已提现后的追偿与负向流水（V1 只记录冲回状态，不处理资金追回）。
- 部分退款与部分冲回（V1 仅整单全额退款，`reversed_amount = commission_amount`）。
- 防刷/风控（同一设备批量注册绑定下级等）、分销业务报表导出。
- 前端页面与前后端联调。
