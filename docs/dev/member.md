# 会员等级与成长值模块知识（接手必读）

本文只记录「代码怎么组织、约束在哪里、改哪里、怎么验证」。长期设计语义见 `docs/design/member.md`、
金额规则见 `docs/design/pricing.md`，不在此重复。

## 1. 模块定位

- 负责：会员等级配置（`member_levels`）的读写、用户当前等级与累计成长值（`user_memberships`）、
  成长值流水（`member_growth_logs`）、会员折扣率的下单取值，以及支付/退款时对成长值的联动写入。
- 不负责：积分账本（`user_points_*`，属 B1-04-05）、优惠券、钱包、分销佣金、运费。
- 关键事实：**成长值 = 已支付订单实付金额（整数分）**，`1 分消费 = 1 成长值`；
  `discount_rate_bp` 是万分比（`10000` = 原价）。

## 2. 代码地图

| 关注点 | 位置 |
| --- | --- |
| 前台/后台 DTO | `api/member/v1/member.go` |
| HTTP 适配 | `internal/controller/member/member.go`（`NewV1()` 前台 / `NewAdminV1()` 后台） |
| 服务接口 | `internal/service/member.go`（`service.Member()`） |
| 业务逻辑 | `internal/logic/member/member.go` |
| 服务注册 | `internal/logic/logic.go`（空白导入 `logic/member`） |
| 路由 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 权限码 seed | `internal/boot/seed.go`（`member:list/view/create/update/delete`） |
| 错误码 | `internal/codes/codes.go`（`24001-24005`） |
| 订单侧接入 | `internal/logic/order/order.go`（`resolveMemberDiscount`、`Pay`） |
| 退款侧接入 | `internal/logic/refund/refund.go`（`Execute` 成功分支） |
| 表结构 | `internal/migrations/sql/20261110000013_member.up.sql`、`20261110000015_order_member_discount.up.sql` |
| 测试 | `internal/logic/member/member_test.go`、`internal/logic/order/member_distribution_chain_test.go`、`internal/controller/member/member_test.go`、`internal/migrations/order_member_discount_migration_test.go` |

## 3. 核心调用链

### 3.1 下单算会员折扣

```text
POST /orders → controller/order.Create
  → logic/order.Create
      → buildLines（重读 skus.price）
      → Coupon.GetDiscount（券优惠，可为 0）
      → resolveMemberDiscount(ctx, userID, originalAmount, couponDiscount)
          → service.Member().DiscountRateBp(userID)
              ├─ 有 user_memberships 档案 → 取 level.discount_rate_bp（不看 level.status）
              └─ 无档案 → findLevelForGrowth(0)（启用 + 门槛 ≤ 0）
      → 夹紧：min(floor(original×(10000-rate)/10000), original-coupon)
      → insertOrder（写 orders.member_discount_amount + total_amount）
```

### 3.2 支付成功发放成长值

```text
POST /orders/:id/pay → logic/order.Pay（单事务）
  → orders 条件更新 10→20
  → Coupon.MarkUsedInTx
  → Member.AccrueForOrderInTx
      → 读 orders(id,user_id,status,total_amount)；status≠20 或金额≤0 → no-op
      → findLevelForGrowth(0)；无可用等级 → warn + no-op（不阻塞支付）
      → INSERT user_memberships ... ON DUPLICATE KEY UPDATE id=id（惰性建档）
      → SELECT user_memberships WHERE user_id=? FOR UPDATE（行锁）
      → INSERT member_growth_logs（幂等键 growth:order_reward:<order_id>；1062 → 视为已处理返回 nil）
      → findLevelForGrowth(newGrowth) → 更新 growth_value（+ 必要时 level_id/upgraded_at）
  → Distribution.AccrueForOrderInTx（一级佣金）
```

### 3.3 退款成功扣减成长值

```text
POST /admin/refunds/:id/execute（success）→ logic/refund.Execute（单事务）
  → Order.CompleteRefundInTx（订单 20→70 + 恢复库存）
  → refund_requests 20/30→60
  → Distribution.ReverseForRefundInTx（佣金 10/20→30）
  → Member.DeductForRefundInTx
      → 读 growth:order_reward:<order_id> 流水；不存在 → no-op
      → SELECT user_memberships FOR UPDATE
      → deduct = min(原发放额, growth_value)；为 0 → no-op
      → INSERT member_growth_logs（幂等键 growth:refund_deduct:<refund_id>；1062 → no-op）
      → 更新 growth_value（不回退 level_id）
  → insertAudit
```

## 4. 关键数据结构（logic 内部行结构）

| 结构 | 对应表 | 用途 |
| --- | --- | --- |
| `levelRow` | `member_levels` | 等级配置读写 |
| `membershipRow` | `user_memberships` | 行锁读当前余额/等级 |
| `growthLogRow` | `member_growth_logs` | 流水读取（幂等键回查原发放额） |
| `orderGrowthSnapshot` | `orders` | 发放成长值所需的订单事实（只读） |

对外 DTO：`MemberLevel`、`Membership`、`GrowthLog`（`api/member/v1`）。
`Membership.LevelId/LevelNo` 为 `*int64`/`*int`：**未建档时为 null**，用于区分「未建档」与「已建档在最低等级」。

## 5. 业务不变量（代码强制的部分）

- 一人一条会员档案：`user_memberships.uk_user_id`；建档走 `INSERT ... ON DUPLICATE KEY UPDATE id=id`。
- 成长值与流水同事务：`AccrueForOrderInTx`/`DeductForRefundInTx` 都使用调用方 `gdb.TX`，不自开事务。
- 流水自洽：`growth_after = growth_before + change_amount`，且余额更新用 `WHERE growth_value = <before>` 条件更新 + `RowsAffected` 校验。
- 事件幂等：`member_growth_logs.uk_idempotency`，键格式固定为 `growth:order_reward:<id>` / `growth:refund_deduct:<id>`。
- 只升不降：扣减/退款路径**不写** `level_id`。
- 等级单调：`level_no` 升序时 `growth_threshold` 严格递增，写事务内 `requireMonotonicLevels` 校验。
- 折扣不越界：`resolveMemberDiscount` 的夹紧逻辑保证 `discount + member_discount ≤ original`。

## 6. 事务、锁与幂等

| 场景 | 事务 | 锁 | 幂等 |
| --- | --- | --- | --- |
| 下单算折扣 | 无（只读） | 无 | 依赖订单幂等键（既有） |
| 支付发成长值 | 订单支付事务 | `user_memberships` 行 `FOR UPDATE` | `growth:order_reward:<order_id>` |
| 退款扣成长值 | 退款成功事务 | `user_memberships` 行 `FOR UPDATE` | `growth:refund_deduct:<refund_id>` |
| 等级配置写 | 独立事务 | 全表 `member_levels` 行 `FOR UPDATE` | 唯一键 1062 → 24004 |

要点：

- 先 `INSERT ... ON DUPLICATE KEY UPDATE id=id` 再 `FOR UPDATE`：既保证首次建档，也保证并发下同一用户的
  `growth_before` 读取是串行化的（否则两台并发支付会读到相同 before）。
- 不要在事务外先查余额再更新：会丢失并发更新。
- `AccrueForOrderInTx` 的「无可用等级 → no-op」是**有意设计**：会员功能不能阻塞订单支付。

## 7. API 与错误码

| 方法 | 路径 | 鉴权 |
| --- | --- | --- |
| GET | `/members/levels` | 用户 JWT |
| GET | `/my/membership` | 用户 JWT |
| GET | `/my/membership/growth-logs` | 用户 JWT（clamp 分页） |
| GET | `/admin/member-levels` | `member:list` |
| POST | `/admin/member-levels` | `member:create` |
| PUT | `/admin/member-levels/:id` | `member:update` |
| DELETE | `/admin/member-levels/:id` | `member:delete` |
| GET | `/admin/members/:user_id` | `member:view` |

错误码：`24001` 等级不存在、`24002` 配置非法（含破坏单调性）、`24003` 等级被引用不可删、
`24004` 序号/门槛重复、`24005` 成长值不足扣减。详见 `docs/design/member.md` §10。

## 8. 测试地图

| 验证目标 | 测试 |
| --- | --- |
| 等级配置 CRUD/校验/删除保护 | `internal/logic/member/member_test.go::TestAdminLevelCRUDValidation` |
| 未建档等级解析与折扣、下一等级进度 | `::TestMembershipDefaultsAndDiscount` |
| 成长值发放/跨级升级/重复事件幂等/未支付不发放 | `::TestGrowthAccrualUpgradeAndIdempotency` |
| 退款扣减/幂等/不降级 | `::TestGrowthRefundDeductAndNoDowngrade` |
| 并发成长值变更（8 并发，余额=流水合计） | `::TestGrowthConcurrentAccrual` |
| 未配置等级 no-op | `::TestAccrualSkipsWhenLevelNotConfigured` |
| 流水分页与越界归一 | `::TestGrowthLogsPagination` |
| 等级停用后已入会用户权益保留 | `::TestLevelSnapshotSurvivesDisable` |
| 下单折扣快照 → 支付发放 → 退款扣减全链路 | `internal/logic/order/member_distribution_chain_test.go` |
| 折扣夹紧边界（券抵满/取整/0 元） | 同上 `::TestResolveMemberDiscountBoundaries` |
| HTTP 认证与 RBAC | `internal/controller/member/member_test.go::TestMemberAndDistributionAuth` |
| 迁移 up/down/up 与历史数据不变 | `internal/migrations/order_member_discount_migration_test.go` |

运行（需真实 MySQL/Redis，使用隔离库避免与其它任务互相污染）：

```bash
DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop_b10408_test REDIS_DEFAULT_DB=9 \
  go test -p 1 ./internal/logic/member/ ./internal/logic/order/ ./internal/controller/member/
```

## 9. 常见问题

- **支付后成长值没变**：先确认平台是否配置了 `status=1` 且 `growth_threshold ≤ 成长值` 的等级；
  没有可用等级时发放是 no-op（日志事件 `member_level_not_configured`）。再确认订单 `status=20`。
- **同一订单又加了一次成长值**：不可能——`growth:order_reward:<order_id>` 上有唯一键。若怀疑，用
  `SELECT * FROM member_growth_logs WHERE idempotency_key='growth:order_reward:<id>'` 核对。
- **退款后等级没降**：符合设计（只升不降）。若业务需要降级，必须先改 `docs/design/member.md` 并补级联测试。
- **下单会员折扣为 0**：依次检查 ① 用户有档案时看 `level.discount_rate_bp` 是否为 10000；
  ② 无档案时看是否存在 `status=1 AND growth_threshold=0` 的等级；③ 券是否已把商品金额抵满（夹紧为 0）。
- **管理员创建的等级报 24002**：多半是破坏了「`level_no` 升序时门槛严格递增」。该约束是刻意引入的
  （避免升级歧义），插入中间等级时需同时调整相邻等级的 `level_no`/门槛。
- **删除等级报 24003**：该等级被 `user_memberships.level_id` 引用。需要先把用户迁到其他等级（当前无换级入口，
  只能通过成长值自然升级；迁移需人工 SQL，属运维操作）。

## 10. 维护注意事项

- 新增成长值事件类型时，必须同时定义确定性幂等键并写入 `docs/design/member.md` §2.5 的类型表，否则无法防重。
- 不要把会员折扣写入 `orders.discount_amount`：该列语义是券优惠，`distribution` 的计佣基数也依赖这一区分。
- 不要在没有证据的情况下把「无可用等级 → no-op」改成报错：会让订单支付依赖会员配置。
- 修改折扣公式时必须同步 `docs/design/pricing.md`（含舍入与夹紧规则）并补金额守恒测试。
- 修改 `AccrueForOrderInTx` 的读订单字段时，注意它读的是**下单快照**（`original_amount` 等），不要改成重算。
- 积分账本（`user_points_*`）与本模块隔离：不要为了省事共用流水表或幂等键前缀。

## B1-04 集成补充：折扣进入统一计价

- `IMember.DiscountRateBp(userID)` 只读、不落库；无档案按成长值解析等级，无可用等级返回 10000。
- 订单侧把折扣换算为**金额**后作为 `pricing.Quote` 的 `MemberDiscount` 槽位，写入 `orders.member_discount_amount`
  （不占用 `discount_amount` 的券语义），公式：`floor(original × (10000-rate_bp) / 10000)`，并夹紧到 `original - coupon`。
- 支付成功 `AccrueForOrderInTx`（幂等键 `growth:order_reward:<order_id>`）与退款成功 `DeductForRefundInTx`
  （幂等键 `growth:refund_deduct:<refund_id>`）都在订单/退款事务内，等级只升不降。
- 错误码域：会员 `24001-24005`（域序 24）。
