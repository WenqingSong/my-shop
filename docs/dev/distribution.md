# 一级分销与佣金模块知识（接手必读）

本文只记录「代码怎么组织、约束在哪里、改哪里、怎么验证」。长期设计语义见 `docs/design/distribution.md`，
不在此重复。

## 1. 模块定位

- 负责：一级推荐关系绑定与查询（`user_referrals`）、支付成功后计提订单佣金（`distribution_commissions`）、
  后台人工结算、退款成功冲回佣金、前台/后台佣金查询。
- 不负责：订单与支付（`order.md`）、退款状态机（`refund.md`）、钱包与提现、多级分销。
- 关键事实：**佣金记录是应收/已结算事实，不是可消费余额**——不写钱包、无提现、不产生资金流水。
  金额整数分，比例万分比，`commission = floor(base × rate_bp / 10000)`。

## 2. 代码地图

| 关注点 | 位置 |
| --- | --- |
| 前台/后台 DTO | `api/distribution/v1/distribution.go` |
| HTTP 适配 | `internal/controller/distribution/distribution.go`（`NewV1()` / `NewAdminV1()`） |
| 服务接口 | `internal/service/distribution.go`（`service.Distribution()`） |
| 业务逻辑 | `internal/logic/distribution/distribution.go` |
| 服务注册 | `internal/logic/logic.go`（空白导入 `logic/distribution`） |
| 路由 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 权限码 seed | `internal/boot/seed.go`（`distribution:list/view/settle`） |
| 错误码 | `internal/codes/codes.go`（`21001-21007`） |
| 支付侧接入 | `internal/logic/order/order.go`（`Pay` 事务内调用 `AccrueForOrderInTx`） |
| 退款侧接入 | `internal/logic/refund/refund.go`（`Execute` 成功分支调用 `ReverseForRefundInTx`） |
| 表结构 | `internal/migrations/sql/20261110000014_distribution.up.sql` |
| 测试 | `internal/logic/distribution/distribution_test.go`、`internal/logic/order/member_distribution_chain_test.go`、`internal/controller/member/member_test.go` |

## 3. 核心调用链

### 3.1 绑定推荐人

```text
POST /referrals/bind → controller/distribution.Bind（userID 取 Principal.UserID）
  → logic/distribution.Bind（单事务）
      → 参数校验：referrer≤0 → 1001；referrer==self → 21003
      → SELECT users WHERE id IN (self, referrer) ORDER BY id FOR UPDATE（消除并发窗口）
          ├─ 推荐人不存在 / status≠1 → 21005
      → SELECT user_referrals WHERE user_id=self FOR UPDATE
          ├─ 已绑定同一推荐人 → 幂等返回既有关系
          └─ 已绑定其他推荐人 → 21002
      → INSERT user_referrals（1062 → 重新读取并按同上规则判定）
      → requireAcyclic：从 self 沿 referrer 链向上（每行 FOR UPDATE），回到 self → 21004 回滚
  → 查询推荐人用户名 → 返回 Referral
```

### 3.2 支付成功计佣

```text
POST /orders/:id/pay → logic/order.Pay（单事务）
  → orders 条件更新 10→20
  → Member.AccrueForOrderInTx
  → Distribution.AccrueForOrderInTx
      → 读 orders(id,user_id,status,original_amount,discount_amount,member_discount_amount)
      → status≠20 → no-op
      → base = original - discount - memberDiscount；base≤0 → no-op
      → 读 user_referrals（买家的一级推荐人）；无 → no-op；受益人==买家 → no-op（防御）
      → commission = base*500/10000；≤0 → no-op
      → INSERT distribution_commissions(status=10)；1062（uk_order_id）→ 已计提，返回 nil
```

### 3.3 退款成功冲回

```text
POST /admin/refunds/:id/execute（success）→ logic/refund.Execute（单事务）
  → Order.CompleteRefundInTx
  → refund_requests → 60
  → Distribution.ReverseForRefundInTx
      → UPDATE ... SET status=30, reversed_amount=commission_amount, reversed_at=NOW(),
                       refund_id=?, reversal_reason='refund'
        WHERE order_id=? AND status IN (10,20)
      → RowsAffected=0 → no-op（无佣金或已冲回）；1062（uk_refund_id）→ 视为已冲回
  → Member.DeductForRefundInTx
  → insertAudit
```

### 3.4 后台结算

```text
POST /admin/commissions/:id/settle → logic.AdminSettleCommission（单事务）
  → SELECT commission FOR UPDATE
      ├─ 不存在 → 21006
      ├─ 20 已结算 → 幂等返回当前记录
      ├─ 30 已冲回 → 21007
      └─ 10 待结算 → UPDATE status=20, settled_at=NOW()（条件更新 status=10）
```

## 4. 关键数据结构

| 结构 | 对应表 | 用途 |
| --- | --- | --- |
| `referralRow` | `user_referrals` | 关系读写与成环遍历 |
| `commissionRow` | `distribution_commissions` | 结算用的行锁读与状态判定 |
| `commissionOrderSnapshot` | `orders` | 计提所需的金额/身份快照 |
| `commissionListRow` | `distribution_commissions LEFT JOIN orders` | 列表/详情联查（带 `order_no`） |
| `userStateRow` | `users` | 绑定时的推荐人状态与用户名 |

对外 DTO：`Referral`、`Commission`、`AdminReferralItem`，以及各自的 Req/Res（`api/distribution/v1`）。
`Commission.Status` 是字符串枚举（`pending`/`settled`/`reversed`），DB 是 TINYINT 10/20/30。

## 5. 业务不变量（代码强制的部分）

- 一人一个推荐人：`user_referrals.uk_user_id`；无 UPDATE 路径（不改绑）。
- 禁止自荐：`Bind` 显式判断（21003），计佣侧还有一层「受益人==买家则跳过」的防御。
- 推荐人必须启用：锁内读 `users.status`。
- 禁止环：插入后沿链向上遍历检测（21004 回滚）。
- 一单一佣金：`distribution_commissions.uk_order_id`；1062 视为已计提。
- 冲回不超原额：`reversed_amount` 直接取同一行 `commission_amount`（`gdb.Raw`）。
- 同一退款只冲一次：`uk_refund_id` + `status IN (10,20)` 条件更新。
- 状态机：只允许 `10→20`、`10→30`、`20→30`；`30` 终态。

## 6. 事务、锁与幂等

| 场景 | 事务 | 锁 | 幂等 |
| --- | --- | --- | --- |
| 绑定 | 独立事务 | `users`（双方，按 id 升序）+ `user_referrals`（本人及链上行）`FOR UPDATE` | `uk_user_id` + 锁内判重 |
| 计佣 | 订单支付事务 | 无额外行锁（插入靠唯一键） | `uk_order_id` |
| 结算 | 独立事务 | 佣金行 `FOR UPDATE` | 已结算幂等返回 |
| 冲回 | 退款成功事务 | 行锁由 UPDATE 条件获取 | `status IN (10,20)` + `uk_refund_id` |

要点：

- 绑定按 `id ASC` 锁双方 `users` 行，避免两个方向的并发绑定互相死锁；同一对用户的并发绑定因此串行化。
- 成环遍历使用 `FOR UPDATE` 读，而不是普通 `SELECT`：普通读在 REPEATABLE READ 下可能读到旧快照，
  看不到并发已提交的新边。
- 计佣故意**不加**「先查后插」：直接插并处理 1062，比「查了再插」在并发下更可靠。

## 7. API 与错误码

| 方法 | 路径 | 鉴权 |
| --- | --- | --- |
| POST | `/referrals/bind` | 用户 JWT |
| GET | `/my/referral` | 用户 JWT |
| GET | `/my/commissions` | 用户 JWT（clamp 分页 + 状态过滤） |
| GET | `/my/commissions/summary` | 用户 JWT |
| GET | `/admin/referrals` | `distribution:list`（strict 分页） |
| GET | `/admin/commissions` | `distribution:list`（strict 分页 + 白名单排序 + 时间范围） |
| GET | `/admin/commissions/:id` | `distribution:view` |
| POST | `/admin/commissions/:id/settle` | `distribution:settle` |

错误码：`21001` 关系不存在、`21002` 已绑定其他推荐人、`21003` 自荐、`21004` 成环、`21005` 推荐人无效、
`21006` 佣金不存在、`21007` 佣金状态迁移非法。详见 `docs/design/distribution.md` §8。

## 8. 测试地图

| 验证目标 | 测试 |
| --- | --- |
| 绑定全拒绝路径 + 幂等 + 改绑拒绝 + 2 环 | `internal/logic/distribution/distribution_test.go::TestBindRules` |
| 三级链成环 | `::TestBindRejectsLongerCycle` |
| 计佣基数/取整/一单一佣金/未支付/无推荐人/0 佣金 | `::TestCommissionAccrualAndIdempotency` |
| 退款冲回同额/状态 30/重复冲回 no-op | `::TestCommissionReversalIdempotency` |
| 结算状态机（10→20、重复幂等、30→20 拒绝） | `::TestCommissionSettleStateMachine` |
| 并发计佣（多订单 + 同订单并发） | `::TestCommissionConcurrentAccrual` |
| 前台/后台查询、过滤、汇总、严格分页与排序校验 | `::TestAdminAndUserQueries` |
| 支付计佣 → 退款冲回端到端 | `internal/logic/order/member_distribution_chain_test.go` |
| HTTP 认证/RBAC/绑定拒绝/查询契约 | `internal/controller/member/member_test.go::TestDistributionAPIContract` |

```bash
DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop_b10408_test REDIS_DEFAULT_DB=9 \
  go test -p 1 ./internal/logic/distribution/ ./internal/logic/order/ ./internal/controller/member/
```

## 9. 常见问题

- **支付成功但没有佣金**：依次检查 ① 买家是否有 `user_referrals` 行；② 订单是否 `status=20`；
  ③ `original - discount - member_discount` 是否 ≤ 0（0 元订单不计佣）；④ `commission` 是否因向下取整为 0。
- **同一订单出现两条佣金**：不可能——`uk_order_id`。若命中，检查是否手工改过唯一键或直接写了库。
- **退款后佣金仍是 30 但金额没变**：`reversed_amount` 只由第一次冲回写入，重复冲回是 no-op，属预期。
- **结算报 21007**：该佣金已被退款冲回（终态 30），不可再结算。
- **绑定报 21004 但看不出环**：用
  `WITH RECURSIVE ...` 或循环查询 `user_referrals` 沿 `referrer_user_id` 打印链路；
  深度超过 200 也会按成环处理（保守拒绝）。
- **绑定报 21005**：推荐人被禁用（`users.status=0`）与不存在返回同一个码，避免暴露账号存在性。

## 10. 维护注意事项

- 不要给 `user_referrals` 增加 UPDATE 路径（改绑）：会破坏「一次性绑定」不变量与历史佣金的归属可解释性。
- 不要引入多级：`referrer_user_id` 是最终受益人；多级需要新表与新设计文档，不是加一列能解决的。
- 不要复用 `wallet_accounts.balance` 表示佣金余额：佣金与钱包资金是不同账本。
- 修改计佣基数口径（当前 = `original - discount - member_discount`）必须同步 `docs/design/distribution.md` §3.2
  与 `pricing.md`，并补金额守恒/与订单金额一致的测试。
- 修改佣金比例（当前是代码常量 `commissionRateBp = 500`）时注意：历史记录已快照 `rate_bp`，改常量只影响新订单。
- 绑定入口目前只接受 `referrer_user_id`，没有邀请码/风控/频率限制；引入时必须先设计防刷与可追溯性，
  并注意并发成环校验（见 `docs/design/distribution.md` §2.2 的并发边界）。
