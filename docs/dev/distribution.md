# 一级分销模块开发知识（接手必读）

业务语义、状态机与不变量见 `docs/design/distribution.md`；本文只记录真实代码入口、调用链、
事务/幂等实现、测试地图与排障要点。

## 1. 模块定位与职责边界

一级分销回答两件事：

1. **谁是谁的上级**：`user_referrals` 一人一行，一人最多一个直接上级，绑定后不可改绑。
2. **哪个订单该给谁多少钱**：`distribution_commissions` 一单一佣金，订单支付成功时计提，
   退款成功时冲回。

边界（不要越界实现）：

- 只做一级，没有层级路径、没有团队树、没有二级分佣。
- 佣金记录是「应收/已冲回事实」，**不写钱包、不写 `wallet_transactions`、不提供提现**。
- 计佣与冲回都在调用方已开启的事务内完成，本模块**不自开事务**。
- 秒杀订单（`flash_sale_orders`）不进入计佣链路。

## 2. 代码地图

| 关注点 | 位置 |
| --- | --- |
| 对外 DTO / 路由元信息 | `api/distribution/v1/distribution.go` |
| 前台/后台控制器 | `internal/controller/distribution/distribution.go`（`ControllerV1` / `AdminControllerV1`） |
| 服务接口与注册表 | `internal/service/distribution.go`（`IDistribution` / `Distribution()` / `RegisterDistribution`） |
| 业务规则 + 内联 SQL | `internal/logic/distribution/distribution.go` |
| 服务注册空导入 | `internal/logic/logic.go` |
| 前台路由（Auth 分组） | `internal/cmd/routes_frontend.go`（`/distribution/*`） |
| 后台路由（AdminAuth + RequirePermission） | `internal/cmd/routes_admin.go`（`/admin/distributions/*`） |
| 权限 seed | `internal/boot/seed.go`（`distribution:referral_list`、`distribution:commission_list`） |
| 错误码 | `internal/codes/codes.go`（20001-20004，域 20000-20999） |
| 计佣比例配置 | `internal/config/contract.go`、`manifest/config/config.yaml`（`distribution.commission_rate_bp`） |
| 计佣接入点 | `internal/logic/order/order.go` → `Pay` 事务内 |
| 冲回接入点 | `internal/logic/refund/refund.go` → `Execute`（success 分支）事务内 |
| 集成测试 | `test/e2e/distribution_test.go` |
| 建表迁移（B1-03-03 已建，本任务未改） | `internal/migrations/sql/20261110000014_distribution.{up,down}.sql` |

## 3. 核心调用链

### 3.1 绑定推荐人

```text
POST /distribution/referral
  → routes_frontend.go: user.POST("/distribution/referral", distributionCtrlV1.BindReferral)
  → controller/distribution.ControllerV1.BindReferral   // 取 Principal.UserID
  → service.Distribution().BindReferral(ctx, userID, referrerUserID)
  → logic/distribution.sDistribution.BindReferral
       a) 自荐校验（userID == referrerUserID → 20001）
       b) 推荐人可用性：SELECT COUNT(*) FROM users WHERE id=? AND status=1（0 → 20002）
       c) 事务开始（g.DB().Transaction）
          1) lockReferralGraphInTx：SELECT id FROM referral_graph_guard WHERE id=1 FOR UPDATE
             （第一条语句；记录缺失 → 内部错误，fail-closed）
          2) findReferralForUpdateInTx：当前读本人关系
             存在 → 同人幂等返回 / 异人 20003（事务结束，锁释放）
          3) requireAcyclicForUpdateInTx：从 referrer 沿 referrer_user_id 当前读向上追溯，
             到达本人（或再次遇到已访问节点/超过深度上限）→ 20004
          4) INSERT user_referrals（1062 → 回滚后新快照重判幂等/冲突）
          5) 当前读回填 id/created_at，返回
       d) 事务提交/回滚 → referral_graph_guard 行锁释放
```

关键点：锁必须在事务内、且是**第一条语句**，保证后续所有关系读取都是当前读，不会复用事务早期快照。

### 3.2 订单支付计佣

```text
POST /orders/:id/pay
  → controller/order.ControllerV1.Pay
  → service.Order().Pay
  → logic/order.sOrder.Pay
       - 已是 paid(20)：幂等快路径返回，不进事务、不计佣
       - 事务：UPDATE orders SET status=20 WHERE id=? AND user_id=? AND status=10
               → 0 行 = 9002（并发/非法迁移）
               → 券核销 MarkUsedInTx
               → service.Distribution().AccrueOrderCommissionInTx(ctx, tx, orderID)
                    · 读 orders(user_id,status,total_amount)（同事务可见 status=20）
                    · status != 20 → no-op
                    · 读 user_referrals(user_id=buyer) → 无上级 no-op
                    · 读配置 distribution.commission_rate_bp（0 → no-op；>10000 → 错误）
                    · amount = base * rate / 10000（整数）；<=0 → no-op
                    · INSERT distribution_commissions（1062 → 视为已计提）
```

### 3.3 退款冲回

```text
POST /admin/refunds/:id/execute (result=success)
  → controller/refund.AdminControllerV1.Execute
  → service.Refund().Execute
  → logic/refund.sRefund.Execute（success 分支事务）
       1) service.Order().CompleteRefundInTx  // 订单 20→70 + 恢复库存，0 行 → 9002 整体回滚
       2) SELECT ... FOR UPDATE 退款行
       3) UPDATE refund_requests SET status=60 WHERE id=? AND status=?
       4) service.Distribution().ReverseOrderCommissionInTx(ctx, tx, refundID, orderID)
            UPDATE distribution_commissions
               SET status=30, reversed_amount=commission_amount, reversed_at=NOW(),
                   refund_id=?, reversal_reason='refund'
             WHERE order_id=? AND status IN (10,20)
            （1062 于 uk_refund_id → 视为已冲回）
       5) INSERT refund_audits（execute_success）
```

顺序不可随意调整：冲回必须在退款状态更新成功之后、审计写入之前，且必须在同一个 `tx` 内。

## 4. 关键数据结构

- `user_referrals(id, user_id, referrer_user_id, created_at)`，`uk_user_id` 唯一。
- `distribution_commissions(...)`：见设计文档 §3.1。要点：
  - `uk_order_id` = 一单一佣金；`uk_refund_id` = 一退款单一次冲回（允许多个 NULL）。
  - 金额三列 `INT UNSIGNED`：`base_amount` / `commission_amount` / `reversed_amount`，单位整数分。
  - `rate_bp` 万分比；`status` 10/20/30。
- 代码内枚举（`internal/logic/distribution/distribution.go`）：
  `commissionStatusPending=10`、`commissionStatusSettled=20`、`commissionStatusReversed=30`、
  `reversibleStatuses={10,20}`、`reversalReasonRefund="refund"`、`commissionScale=10000`、`maxRateBp=10000`。
- 跨模块常量：`orderStatusPaid=20`、`userStatusEnabled=1`（订单/用户模块未导出，按设计文档固化，
  避免引入包依赖）。

## 5. 业务不变量（实现视角）

| 不变量 | 实现手段 |
| --- | --- |
| 一人一个上级 | `uk_user_id` 唯一 + 已绑定时不执行 UPDATE |
| 禁止自荐 | `userID == referrerUserID` → 20001 |
| 推荐人必须有效 | 绑定前 `users.status=1` 计数校验 |
| 不可改绑 | 无 UPDATE 路径；换人 20003 |
| 一单一佣金 | `uk_order_id`，1062 幂等忽略 |
| 计佣时机正确 | 只在 `Pay` 事务内、订单已置 20 之后调用 |
| 整数金额 | 全链路 `int64`，先乘后除，无 `float64` |
| 冲回只一次 | `WHERE status IN (10,20)` 条件更新；终态 30 不再命中 |
| 冲回不超额 | `reversed_amount` 由 SQL 引用 `commission_amount` 列写入 |
| 事务一致 | 计佣/冲回与订单、退款状态同事务，失败整体回滚 |
| 数据归属 | 用户端身份只来自 `Principal.UserID` |

## 6. 事务、锁与幂等

- **事务对象**：GoFrame `gdb.TX`，由调用方 `g.DB().Transaction(ctx, func(ctx, tx gdb.TX) error {...})` 注入。
  `*InTx` 方法只使用传入的 `tx`，**绝不在内部再开事务**。
- **锁顺序**：退款链路沿用 `orders → refund_requests → distribution_commissions` 的既有顺序
  （`orders` 行由 `UPDATE ... WHERE status=20` 隐式加锁），本模块不对 `orders` 加显式锁。
- **绑定锁协议（B1-04-FIX-01）**：`BindReferral` 的自建事务固定顺序为
  `referral_graph_guard(id=1, FOR UPDATE) → user_referrals(本人, FOR UPDATE) → 推荐链逐跳(FOR UPDATE) → INSERT`
  - 全局锁是单行排他锁，跨 goroutine/连接/实例串行化，不存在锁释放先于事务提交的窗口；
  - 锁内只用当前读（`LockUpdate()`），不读一致性快照；
  - 锁记录缺失 → 返回内部错误并拒绝写入（fail-closed），不降级为无锁绑定；
  - 其他写入口：`user_referrals` 只有 `BindReferral` 一处 INSERT，无 UPDATE/DELETE 业务路径；
    新增写入口必须复用同一锁协议。
- **幂等三件套**：
  1. 订单支付快路径 `if status == 20 { return }`（不进事务）；
  2. 计佣 `uk_order_id` 1062 视为已计提；
  3. 冲回 `WHERE status IN (10,20)` + `uk_refund_id` 1062 视为已冲回。
- **失败的语义**：计佣返回错误 → 支付事务回滚（订单保持 10，可重试）；冲回返回错误 → 退款事务回滚
  （退款保持 20/30，可重试）。不存在「先写一半」的中间态。
- **配置非法**（`rate_bp` 越界）按技术错误处理并回滚，属于 fail-closed；`rate_bp=0` 是合法配置（关闭计佣）。

## 7. API、权限与错误码

- 用户端：`POST /distribution/referral`、`GET /distribution/me`、`GET /distribution/subordinates`、
  `GET /distribution/commissions`；均挂在 `middleware.Auth` 分组。
- 后台：`GET /admin/distributions/referrals`、`/distributors`（`distribution:referral_list`）、
  `/commissions`、`/commissions/stats`（`distribution:commission_list`）。
- 权限码 seed 在 `internal/boot/seed.go`；`tools/openapi-gen/check.go` 强制「路由权限码 ↔ seed ↔ OpenAPI」三向相等，
  新增/改名权限码必须同步 seed、路由、OpenAPI 三处。
- 错误码：20001 自荐（400）、20002 推荐人不可用（404）、20003 已绑定他人（409）；
  参数/分页错误复用 1001，未认证 1002，无权限 1003，内部错误 1000。
- 新增/修改接口后必须重跑 `go run ./tools/openapi-gen -repo .` 与 `bash scripts/contract-check.sh`。

## 8. 测试地图

| 测试 | 覆盖不变量 |
| --- | --- |
| `TestDistributionBindReferral` | 正常绑定落库、同人幂等、换人 20003、自荐 20001、推荐人不存在/禁用 20002、未登录 401 |
| `TestDistributionBindReferralConcurrent` | 8 个并发绑定不同推荐人 → 只有 1 个成功、落库 1 行且与成功响应一致 |
| `TestDistributionBindReferralConcurrentMutualCycle` | HTTP 层双向互绑并发 → 1 成功 + 1 成环拒绝（409/20004），落库 1 行且无环 |
| `TestDistributionCommissionAccrual` | base/rate/amount 快照正确（5000×2 → 10000 基数、500 佣金）、status=10；重复支付不重复计提 |
| `TestDistributionCommissionPaidConcurrent` | 8 个并发支付 → 订单只到 20、佣金只 1 条 |
| `TestDistributionCommissionNoReferral` | 无上级订单不产生佣金 |
| `TestDistributionCommissionRateZero` | 比例未配置（0）不产生佣金 |
| `TestDistributionCommissionUnpaidOrder` | 待支付 / 取消订单不产生佣金 |
| `TestDistributionReversalOnRefund` | 退款成功 → status=30、reversed=400、refund_id/reason 可审计；重复执行不再冲回 |
| `TestDistributionReversalConcurrent` | 8 个并发执行退款 → 只冲回一次、金额不超额、退款成功记录 1 条 |
| `TestDistributionUserIsolation` | 身份取自 Principal、伪造 `user_id` 不越权、未登录 401 |
| `TestDistributionAdminAccess` | 无权限 403、按权限码分级放行、统计与记录一致、普通用户 token 403 |

`internal/logic/distribution/distribution_concurrency_test.go`（真实 MySQL，多连接 + 同步屏障）：

| 测试 | 覆盖不变量 |
| --- | --- |
| `TestBindConcurrentMutualCycleRejected` | 双向并发互绑 → 1 成功 + 1 成环拒绝，1 行关系、无环 |
| `TestBindConcurrentThreeNodeCycleRejected` | A→B / B→C / C→A 并发 → 2 成功 + 1 成环拒绝，无环 |
| `TestBindRejectsCycleOnLongChain` | 既有 A→B→C→D，D→A（及 D→C）被拒，既有关系不变 |
| `TestBindConcurrentSameUserDifferentReferrers` | A→B 与 A→C 并发 → 1 成功 + 1 冲突(20003)，仅 1 行 |
| `TestBindConcurrentSameRelationIdempotent` | A→B ×3 并发 → 全部幂等成功，仅 1 行 |
| `TestBindReferralWaitsForGraphLock` | 手工持有 `referral_graph_guard` 行锁时绑定被阻塞，释放后成功（证明锁真实生效） |
| `TestBindReferralTxRollbackReleasesLockAndLeavesNoRow` | 锁 + 写入后事务失败 → 无残留、锁已释放、后续绑定立即成功 |
| `TestBindReferralFailsClosedWhenGuardRowMissing` | 锁记录缺失 → 拒绝写入（fail-closed），无关系落库 |
| `TestBindConcurrentMutualCycleStress` | 双向互绑 100 轮，每轮核对数据库关系图（100 成功 / 100 成环拒绝 / 无环） |
| `TestBindConcurrentThreeNodeCycleStress` | 三节点 100 轮（200 成功 / 100 成环拒绝 / 无环） |

运行方式：

```bash
# 集成 / 并发用例（必须指向隔离测试库；这些用例会清理用户与关系表）
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 ./internal/logic/distribution/ -v
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -race -count=1 ./internal/logic/distribution/

DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 -run 'TestDistribution' ./test/e2e/
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -race -count=1 -run 'TestDistribution' ./test/e2e/
```

前置条件：MySQL/Redis 就绪（`docker compose up -d mysql redis`）。测试直接读库断言唯一约束与金额，
不做任何 DB mock。

## 9. 常见排障

| 现象 | 排查方向 |
| --- | --- |
| 支付成功但没有佣金 | 买家是否绑定上级（`SELECT * FROM user_referrals WHERE user_id=<buyer>`）；`DISTRIBUTION_COMMISSION_RATE_BP` 是否为 0；订单 `total_amount` 是否为 0；`base × rate` 是否不足 1 分 |
| 支付直接失败且日志含「计佣」 | 配置 `distribution.commission_rate_bp` 越界（>10000）或为负数；这是 fail-closed 设计 |
| 退款成功但佣金没冲回 | 订单本来就没有佣金（正常 no-op）；或佣金已被更早的退款单冲回（`status=30`，同订单不可再冲） |
| 绑定返回 20003 | 该用户已有上级；分销关系不可改绑，属于预期行为 |
| 绑定返回 20002 | 推荐人 id 不存在或被禁用（`users.status=0`） |
| 绑定偶发等待/超时 | 有长事务持有 `referral_graph_guard` 行锁；用 `SELECT * FROM performance_schema.data_locks WHERE OBJECT_NAME='referral_graph_guard'` 与 `information_schema.innodb_trx` 定位持锁事务，等待其提交/回滚，不要手工删除锁记录 |
| 绑定返回 500 且日志含「全局锁记录缺失」 | `20261111000004_referral_graph_guard` 未执行或记录被误删；执行 `migrate up`，并确认 `SELECT COUNT(*) FROM referral_graph_guard` = 1。缺失时应用 fail-closed，属预期 |
| 后台 403 | 管理员缺少 `distribution:referral_list` / `distribution:commission_list`；检查 `role_permissions` |
| 清理 `orders` 时报 1451 | 先 `DELETE FROM distribution_commissions`，佣金对订单是 FK RESTRICT |
| 契约检查失败 | 忘了跑 `go run ./tools/openapi-gen -repo .`，或权限码三向不一致 |

## 10. 维护注意事项

- 修改计佣口径（基数/比例/舍入）必须同时更新 `docs/design/distribution.md` §3.2 与百分比配置说明；
  比例默认值必须保持「0 = 关闭」，不得改成硬编码比例。
- 不要给佣金表加 `idempotency_key`：`uk_order_id` 已表达同一规则。
- 不要在 `logic/distribution` 里 `import` `logic/order` 或 `logic/refund`（会造成包循环）；跨模块协作只能经 `internal/service`。
- 不要给后台加分销写接口（改佣金状态、改推荐关系）而不先取得 Owner 决策与设计文档更新。
- 冲回金额一律用 SQL 列引用（`reversed_amount = commission_amount`），不要改成应用层减法，避免并发/部分退款时超额。
- 新增用户端分销接口时必须从 `Principal.UserID` 取身份，禁止接受 `user_id` 入参作为归属依据。
- 表结构变更需要新的 migration version 并在 `.agent/registry/migrations.md` 预占；本任务复用了 B1-03-03 的 `20261110000014`，未新增版本。

## 11. 相关文档

- 设计（状态机/不变量/API/错误码）：`docs/design/distribution.md`
- 订单支付链路：`docs/design/order.md`
- 退款链路：`docs/design/refund.md`
- 钱包边界：`docs/design/wallet.md`
- 权限清单：`docs/design/rbac.md`
- 错误码域：`docs/design/error-codes.md`、`.agent/registry/error-codes.md`
- 交付报告：`docs/reports/B1-04-05-distribution.md`

## 12. B1-04 集成变更（统一实现）

1. **唯一实现**：保留本模块（B1-04-05）的 `api/controller/logic/service` 与路由；
   B1-04-08 的重复分销实现已按集成决策废弃，仅吸收其两项能力：
   - 推荐关系**成环校验**（`CodeDistributionCycle=20004`）：B1-04-FIX-01 起改为「取全局锁 → 写入前从 referrer 向上当前读追溯 → 命中即拒绝」；
   - **人工结算** `POST /admin/distributions/commissions/:id/settle`（`distribution:settle`）：
     `pending(10) → settled(20)` + `settled_at`，幂等；已冲回(30) 返回 20006，不存在返回 20005。
     该操作**只做状态流转，不做真实资金转账**（无钱包流水、无打款）。
2. **并发绑定**（B1-04-FIX-01 修正）：原先「不显式加锁、靠 `uk_user_id` 仲裁」只能保证「一人一个上级」，
   无法阻止双向并发互绑形成的 A→B→A 环（两个事务的一致性读互相看不到对方未提交插入）。
   现改为 MySQL 级串行化：绑定事务第一条语句对 `referral_graph_guard.id=1` 取排他行锁，
   锁内当前读完成后成环检查与写入；`uk_user_id` 退化为绕过锁协议时的最终兜底（1062 → 回滚重判）。
   详细协议与测试见 `docs/design/distribution.md` §2.2/§2.3 与 B1-04-FIX-01 报告。
3. **佣金比例**：仍由 `distribution.commission_rate_bp` 控制，默认 0（关闭计佣）；
   测试中通过环境变量注入 500 仅用于验证公式，生产默认值不变。
4. **计佣基数**：`orders.total_amount`（已含会员折扣与运费），比例万分比、向下取整；一单一条佣金（`uk_order_id`）。
5. **退款冲回**：`status IN (10,20) → 30` + `uk_refund_id`，与退款成功同事务。
