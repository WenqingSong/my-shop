# 钱包模块知识（接手必读）

资金业务语义、不变量与接入顺序见 `docs/design/wallet.md`；本文只记录代码入口、实现细节、
常见坑与测试方法。

## 1. 当前实现状态

- 已实现：余额查询、资金流水分页查询、Mock 充值、账户按需初始化，
  以及供订单/退款模块在**既有事务**内复用的扣款/入账原语。
- 未接入：`order.Pay`（余额支付渠道）与 `refund.Execute`（退款退回余额）仍只推进状态。
  基线未合入 B1-04-03，按任务约束不改这两个文件，接入方式见 `docs/design/wallet.md` §7。
- 未新增 Migration：`wallet_accounts` / `wallet_transactions` / `recharge_orders` 全部来自 B1-03-02。

## 2. 代码入口

| 关注点 | 位置 |
| --- | --- |
| 对外 DTO | `api/wallet/v1/wallet.go` |
| Controller（薄层，只取 Principal） | `internal/controller/wallet/wallet.go` |
| Service 接口（含 InTx 原语契约） | `internal/service/wallet.go` |
| 余额/流水/充值业务 | `internal/logic/wallet/wallet.go` |
| 事务内扣款/入账、账户锁、流水写入 | `internal/logic/wallet/wallet_fund.go` |
| 纯内存单元测试 | `internal/logic/wallet/wallet_unit_test.go` |
| 资金一致性集成测试（真实 MySQL） | `internal/logic/wallet/wallet_integration_test.go` |
| HTTP 契约测试（真实路由/中间件/登录） | `internal/controller/wallet/wallet_test.go` |
| 路由（前台 `middleware.Auth`） | `internal/cmd/routes_frontend.go` |
| 错误码 22001-22010 | `internal/codes/codes.go` |
| Mock 充值开关 | `internal/config/contract.go`（`wallet.mock_recharge.enabled`）、`manifest/config/config.yaml` |
| 生产禁止启用校验 | `internal/config/strict.go`（`ValidateMockRechargeDisabled`）、`internal/boot/boot.go` |

## 3. 账户初始化

- 入口：`ensureAccount`（只读余额查询）与 `lockAccountInTx`（资金操作）。
- 两者都使用：

  ```sql
  INSERT INTO wallet_accounts (user_id, balance) VALUES (?, 0)
  ON DUPLICATE KEY UPDATE user_id = user_id
  ```

  由 `uk_user_id` 兜底并发创建，**不要**改成「先 SELECT 再 INSERT」——那会引入并发重复插入与 1062 分支。
- 只读查询初始化账户不写流水（余额变化必须由流水解释，余额 0 时无变化）。
- 资金操作随后用 `SELECT ... FOR UPDATE` 锁读该行；`INSERT ... ON DUPLICATE KEY UPDATE` 本身已持有行锁，
  二者共同保证同用户资金操作串行。

## 4. 充值流程要点

1. 单号确定性派生：`"RC" + sha256("<user_id>:<idempotency_key>")[:30]`，长度固定 32。
   这让「同一用户同一幂等键」映射到同一 `recharge_orders.order_no`，重复请求命中 `uk_order_no`。
2. 事务内先锁账户，再按单号锁读充值单，然后依次：插入充值单（status=10）→ 条件 UPDATE 余额
   → 插入流水（`recharge:<recharge_id>`）→ 条件 UPDATE 充值单 `10→20`。
3. 幂等回放校验三要素：归属用户、金额、状态。金额不同 → `22008`；状态非 20 → `22005`；
   成功后回读 `recharge:<id>` 流水，**流水缺失视为数据损坏**（`22001`），不把冲突当成功。
4. 余额上限：`maxWalletBalance = 4294967295`（`INT UNSIGNED` 上限）。入账前先算 `before + amount`，
   再用 `WHERE balance <= max - amount` 条件更新兜底；失败返回 `22010` 且整个事务回滚。
5. 单笔充值上限 `v1.MaxRechargeAmount = 1_000_000` 分（¥10,000）。

## 5. 余额支付事务（给订单模块）

```go
err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
    res, err := service.Wallet().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{
        UserID: userID, OrderID: orderID, Amount: order.TotalAmount,
    })
    if err != nil {
        return err
    }
    if res.AlreadyApplied {
        // 该订单已扣过款：把业务当作已处理，不要重复推进订单状态
    }
    // 同一事务内推进 orders 10→20（条件更新 + paid_at + pay_type='balance'）
    return nil
})
```

顺序约束：

- **先锁订单行再调用钱包**（与 `refund.Execute` 的 `orders → refund` 锁顺序保持一致），
  钱包内部只锁 `wallet_accounts`，不会反向获取订单行锁，避免跨表死锁。
- 不要在钱包方法内自开事务：`DebitForOrderInTx` / `CreditForRefundInTx` 接收 `gdb.TX`，
  自身只使用传入事务。
- 余额不足（`22002`）时不要「部分扣款」或「降级为其它渠道」，直接返回错误让事务回滚。

## 6. 退款回滚（给退款模块）

```go
err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
    if err := service.Order().CompleteRefundInTx(ctx, tx, orderID); err != nil { // 订单 20→70 + 恢复库存
        return err
    }
    if order.PayType == "balance" { // 只有余额支付的订单才退回钱包
        if _, err := service.Wallet().CreditForRefundInTx(ctx, tx, service.WalletCreditInput{
            UserID: userID, RefundID: refundID, OrderID: orderID, Amount: refund.Amount,
        }); err != nil {
            return err
        }
    }
    // 同一事务内推进 refund_requests → 60
    return nil
})
```

- 退款申请/审核通过**不得**提前增加余额；只有执行成功分支才调用入账。
- 重复执行退款：`refund:<refund_id>` 已存在且要素一致时返回 `AlreadyApplied=true`，
  不会重复入账，也不会让退款状态回退。
- 非余额渠道（`mock` 等）绝不能调用入账方法，否则等于凭空造钱。

## 7. 幂等键一览

| 业务 | `idempotency_key` | `tx_type` | `biz_type` / `biz_id` |
| --- | --- | --- | --- |
| Mock 充值 | `recharge:<recharge_order_id>` | `recharge` | `recharge` / 充值单 id |
| 订单余额支付 | `order_pay:<order_id>` | `order_pay` | `order` / 订单 id |
| 退款退回余额 | `refund:<refund_id>` | `order_refund` | `refund` / 退款申请 id |

`wallet_transactions.uk_idempotency` 全局唯一。**同键不同金额/方向/类型必须拒绝**：

- 订单路径 → `22006`
- 退款路径 → `22007`
- 充值/兜底 → `22008`

## 8. 并发安全

- 统一锁顺序：`wallet_accounts`（按 `user_id`）→ `wallet_transactions` / `recharge_orders`。
- 扣减与入账都用带条件的原子 UPDATE 并以 `RowsAffected` 判定，不以「先读后写」做资金判断。
- 余额字段是 `INT UNSIGNED`，但**不要依赖** MySQL 严格模式拦截下溢：非严格模式下无符号下溢会截断为 0
  并只产生 warning。条件更新才是权威保护。
- 已验证：10 个 goroutine 并发用 10000 分余额各扣 3000 分，只有 3 个成功、余额 1000、流水 3 条、
  账实一致（`TestConcurrentDebitNeverNegative`）。

## 9. 常见问题

**Q：为什么充值必须传 `idempotency_key`？**
A：`recharge_orders` 没有幂等列（Schema 冻结，本任务不新增迁移），只能用确定性单号承载幂等。
客户端每次充值请求生成一个稳定键（例如 UUID 或业务请求号）即可；重试必须复用同一个键。

**Q：重复充值返回的响应是「成功」还是「已处理」？**
A：接口语义是幂等成功：返回同一充值单（相同 `id`），余额不变、流水不新增。同键不同金额会明确返回 `22008`。

**Q：`AlreadyApplied=true` 时订单还要不要改状态？**
A：不要。它表示资金已由前一次请求扣过；订单状态应由订单模块自己的条件更新判定，
若订单已是 `paid` 就按幂等成功返回。

**Q：为什么账户余额和流水对不上？**
A：业务路径不存在「只改余额不写流水」；先检查是否有测试/脚本直接 `UPDATE wallet_accounts`（本模块测试的
`setWalletBalance` 就是这样构造期初余额的），再检查是否有外部工具写入。生产禁止直接改余额。

**Q：Mock 充值在生产被打开会怎样？**
A：严格部署模式（`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true`）下进程直接启动失败；
非严格模式（本地/联调）才会生效。

## 10. 测试方法

纯内存单元测试（无需数据库）：

```bash
go test -count=1 -run 'TestValidateRechargeAmount|TestNormalizePaging|TestReplayFundResult' ./internal/logic/wallet/
```

资金一致性集成测试（**必须使用专用测试库**，文件名以 `_test` 结尾）：

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_test \
DATABASE_DEFAULT_PASS=root \
  go test -race -count=1 ./internal/logic/wallet/

DATABASE_DEFAULT_NAME=my_shop_b104_test \
DATABASE_DEFAULT_PASS=root \
REDIS_DEFAULT_ADDRESS=127.0.0.1:6379 \
  go test -count=1 ./internal/controller/wallet/
```

测试库护栏：

- `requireTestDB` 在 `DATABASE_DEFAULT_NAME` 不以 `_test` 结尾时 **skip**，不会在 `my_shop` 上跑资金用例。
- 清理只按本用例创建的 `user_id` 删除三张表数据，不做全表 `DELETE` / `DROP TABLE`。
- 集成测试会执行 `migrations.Up`（内嵌迁移），测试库需为空库或已是同版本。

覆盖清单（对应 `docs/reports/B1-04-04-wallet.md` §9）：

| 用例 | 覆盖点 |
| --- | --- |
| `TestRechargeNormalAndIdempotent` | 正常充值、重复充值、同键不同金额 |
| `TestRechargeDisabledByDefault` | 安全开关默认关闭、无副作用 |
| `TestRechargeAmountValidation` | 金额 0/负数/超上限 |
| `TestRechargeOverflowRollback` | 充值单创建后余额失败 → 整体回滚 |
| `TestConcurrentRechargeSameKey` | 并发同键只入账一次 |
| `TestConcurrentAccountInit` | 并发初始化只产生一个账户 |
| `TestDebitForOrderSuccessAndIdempotent` | 余额支付、重复支付、同订单不同金额 |
| `TestDebitInsufficientBalance` | 余额不足不改余额/不写流水 |
| `TestDebitExactBalanceBoundary` | 余额恰好足额扣到 0，再扣 1 分失败 |
| `TestCreditRejectsInvalidAmount` | 退款金额 0/负数拒绝且无副作用 |
| `TestRollbackWhenTransactionInsertFails` | 余额扣减成功后流水插入失败 → 整体回滚 |
| `TestDebitRollbackWhenOrderAdvanceFails` | 扣款后订单推进失败 → 回滚 |
| `TestCreditForRefundAndIdempotent` | 支付后退款、重复退款 |
| `TestCreditRollbackWhenRefundAdvanceFails` | 入账后退款推进失败 → 回滚 |
| `TestConcurrentDebitNeverNegative` | 并发扣款不透支、账实一致 |
| `TestTransactionsPaginationAndIsolation` | 分页、类型过滤、用户隔离 |
| `TestRechargeFlowIsRepeatableAcrossTransactions` | 余额与流水逐笔闭合 |
| `TestWalletHTTPFlow` / `TestWalletHTTPValidation` | HTTP 契约、匿名拒绝、错误码、开关 |
