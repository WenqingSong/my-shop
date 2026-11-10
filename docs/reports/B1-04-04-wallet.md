# B1-04-04 钱包、充值与余额支付 — 交付报告

## 1. 任务背景

SurgeCart 电商后端 B1-04 阶段中，B1-03 只建立了 `wallet_accounts` / `wallet_transactions` /
`recharge_orders` 的数据库结构，没有任何钱包业务代码；订单 Mock 支付与退款执行都不读写钱包。
B1-04-04 负责补齐资金侧能力，核心不是 CRUD，而是**资金事务一致性、幂等、并发安全与故障回滚**。

## 2. 开发范围

| 范围项 | 本次状态 |
| --- | --- |
| 钱包账户与余额查询 | 已完成 |
| 钱包资金流水查询 | 已完成 |
| Mock 充值 | 已完成 |
| 账户初始化 / 一人一账户 | 已完成 |
| 资金流水幂等（`recharge:` / `order_pay:` / `refund:`） | 已完成 |
| 订单余额支付 | **未完成（阻塞）**：钱包侧 `DebitForOrderInTx` 已就绪并有测试；`order.Pay` 未接入 |
| 退款退回钱包 | **未完成（阻塞）**：钱包侧 `CreditForRefundInTx` 已就绪并有测试；`refund.Execute` 未接入 |

未新增 Migration、未新增表、未改既有字段语义。

## 3. 基线与 Git 分支

```text
源分支            origin/feat/p2-backend
基线 SHA          af512f9e8a64c133c705d3a0ed1dd106de3d5c60
工作方式          git worktree add -b feat/b1-04-04-wallet ../my-shop-b1-04-04-wallet origin/feat/p2-backend
当前分支          feat/b1-04-04-wallet
实现提交          3954ef6034b737fc23f67fe77cd8c2907a9a65bb
最终 HEAD         本报告提交（docs(report): B1-04-04 交付报告）
基线工作区状态    clean（未执行 reset/clean/stash，未改动 feat/p2-backend）
```

基线核实结论：`origin/feat/p2-backend` 最新提交为 `af512f9`（B1-03-04 之后），本地无该分支的未推送提交，
源分支工作区无未提交修改；本任务从该 SHA 拉出独立分支与 Worktree。

### 3.1 订单/退款集成前置条件（任务书 §7 判定）

**判定为「情况 B：B1-04-03 尚未合入」**，证据：

- `git log --oneline` 上该基线最新提交全部属于 B1-03 系列，无任何 B1-04 提交；
- `internal/logic/order/` 只有 `order.go`，没有独立计价接口；`order.Pay` 无支付渠道参数，
  只做 `10→20` 状态推进，且**不写 `pay_type`**；
- `docs/design/order.md` / `wallet.md` / `refund.md` 均明确写着「订单支付/退款当前不读写钱包，
  余额支付、退款入账在 B1-04 实现」。

按任务书约束，**未修改** `internal/logic/order/order.go`、`internal/logic/refund/refund.go`、
`internal/service/order.go`，改为交付可被这两个模块直接调用的事务内原语（见 §7）。

## 4. 实际实现的业务能力

1. **余额查询** `GET /wallet`：身份取自 `Principal.UserID`；账户不存在时安全初始化（余额 0，不写流水）；
   出参只有 `balance` / `updated_at`，不含 `account_id` / `user_id`。
2. **流水分页查询** `GET /wallet/transactions`：`page`/`size`/`tx_type`，按 `id` 倒序；
   只查本人；返回交易类型、方向、金额、交易前后余额、业务类型/业务 id、备注与时间。
3. **Mock 充值** `POST /wallet/recharges`：充值单 + 余额 + 流水同事务；金额上限 1,000,000 分；
   同一 `idempotency_key` 只入账一次，同键不同金额拒绝。
4. **事务内资金原语**：`DebitForOrderInTx` / `CreditForRefundInTx`，接收调用方 `gdb.TX`，自身不开事务。
5. **安全开关**：`wallet.mock_recharge.enabled` 默认 `false`；严格部署模式开启即启动失败。

## 5. 数据库 Schema 使用情况

| 表 | 用途 | 本任务操作 |
| --- | --- | --- |
| `wallet_accounts` | 余额与账户唯一性 | 读、初始化（余额 0）、条件更新余额 |
| `wallet_transactions` | append-only 资金流水 | 只插入 + 只读查询 |
| `recharge_orders` | Mock 充值单 | 插入、状态 `10→20`、只读 |

- 未创建 `wallets`、`wallet_ledger` 或任何重复表；未新增 Migration（迁移清单与
  `internal/migrations/migrations_test.go` 均未改动）。
- `recharge_plans` 未创建（无 Owner 批准的充值套餐决策）。
- 与设计文档一致性：`docs/design/wallet.md` 的字段描述与 `20261110000006_wallet.up.sql` /
  `20261110000007_recharge_orders.up.sql` 完全一致，本任务只把「已建表」更新为「已实现业务」，
  未改写字段语义。未发现 Schema 与文档冲突。

## 6. API 清单

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/wallet` | 用户 `middleware.Auth` | 查询本人余额 |
| GET | `/wallet/transactions` | 用户 `middleware.Auth` | 本人资金流水分页 |
| POST | `/wallet/recharges` | 用户 `middleware.Auth` | Mock 充值（受开关控制） |

- 均不接受 `user_id`，不使用管理员身份；响应沿用统一 `{code,message,data}` 包装。
- 订单支付接口未新增：余额渠道应在现有 `POST /orders/:id/pay` 上扩展（见 §11）。

### 6.1 错误码（域 21000-21999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 21001 | 钱包账户不存在或初始化失败 | 500 |
| 21002 | 余额不足 | 409 |
| 21003 | 充值金额非法 | 400 |
| 21004 | 充值订单不存在 | 404 |
| 21005 | 充值订单状态不允许操作 | 409 |
| 21006 | 重复支付（订单支付幂等键冲突） | 409 |
| 21007 | 退款入账失败（退款幂等键冲突） | 409 |
| 21008 | 资金流水幂等冲突 | 409 |
| 21009 | Mock 充值未启用 | 403 |
| 21010 | 金额超出可表示范围（余额溢出） | 409 |

未重复定义通用码（1001/1002 等直接复用）。

## 7. 事务设计

### 7.1 Mock 充值（自开事务）

```text
g.DB().Transaction
  ├─ lockAccountInTx：INSERT ... ON DUPLICATE KEY UPDATE + SELECT ... FOR UPDATE
  ├─ findRechargeOrderInTx：按确定性单号 FOR UPDATE
  ├─ INSERT recharge_orders(status=10)
  ├─ UPDATE wallet_accounts SET balance = balance + ? WHERE id = ? AND balance <= ?
  ├─ INSERT wallet_transactions（recharge:<id>）
  └─ UPDATE recharge_orders SET status=20, pay_type='mock', paid_at=NOW() WHERE status=10
```

### 7.2 余额支付（由调用方事务包裹）

```text
调用方 tx
  ├─ DebitForOrderInTx：锁账户 → 幂等键检查 → 条件扣减 → 写流水（order_pay:<order_id>）
  └─ 订单 10→20（条件更新 + paid_at + pay_type）
```

### 7.3 退款入账（由调用方事务包裹）

```text
调用方 tx
  ├─ 订单 20→70 + 恢复库存（CompleteRefundInTx）
  ├─ CreditForRefundInTx：锁账户 → 幂等键检查 → 条件入账 → 写流水（refund:<refund_id>）
  └─ refund_requests → 60
```

关键约束：钱包方法只使用传入的 `gdb.TX`，**不自开事务、不跨事务**；余额变更与流水写入永远在
同一事务提交或回滚。

## 8. 幂等与并发设计

### 8.1 幂等键

| 业务 | 幂等键 | 唯一约束 |
| --- | --- | --- |
| 充值 | `recharge:<recharge_order_id>` | `wallet_transactions.uk_idempotency` |
| 支付 | `order_pay:<order_id>` | 同上 |
| 退款 | `refund:<refund_id>` | 同上 |
| 充值单本身 | `order_no = "RC" + sha256(user_id:idempotency_key)[:30]` | `recharge_orders.uk_order_no` |

充值幂等不依赖「先查后插」：直接用确定性单号插入，命中 `uk_order_no` 后回读既有单并核对
（用户、金额、状态），金额不一致返回 21008，状态非成功返回 21005，流水缺失返回 21001
（不把任何冲突都当成功）。

### 8.2 并发安全

- 所有资金操作先对 `wallet_accounts` 行加锁（`INSERT ... ON DUPLICATE KEY UPDATE` 已持行锁，
  再 `SELECT ... FOR UPDATE`），把同一用户的资金操作串行化，避免「先 SELECT 余额→应用层判断→UPDATE」竞态。
- 扣减/入账都使用带条件的原子 UPDATE，以 `RowsAffected` 判定成功：
  `balance >= ?`（扣减）、`balance <= max - ?`（入账）。
- 锁顺序统一：`wallet_accounts` → `wallet_transactions` / `recharge_orders`，不与订单行锁形成反向环。

## 9. 测试用例和结果

### 9.1 结果汇总

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | `go build ./...` | PASS |
| 静态检查 | `go vet ./...` | PASS |
| 钱包单元 + 集成 | `DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go test -count=1 ./internal/logic/wallet/` | PASS（25 项） |
| 钱包并发/Race | 同上 + `-race` | PASS |
| 钱包 HTTP 契约 | `DATABASE_DEFAULT_NAME=... REDIS_DEFAULT_ADDRESS=127.0.0.1:6379 go test -count=1 ./internal/controller/wallet/` | PASS（2 项） |
| 配置契约与安全闸门 | `go test -count=1 ./internal/config/` | PASS（含新增 4 项） |
| boot 集成 | `go test -count=1 ./internal/boot/` | PASS |
| 仓库单元门禁 | `bash scripts/test-unit.sh` | **部分 FAIL**（见 §12，均为全局 API 计数类） |

### 9.2 测试环境隔离

- 使用独立数据库 `my_shop_b104_test`（新建空库，`migrations.Up` 建表）；
  未连接、未操作 `my_shop`、`my_shop_b10304_verify`。
- 测试内 `requireTestDB` 强制要求 `DATABASE_DEFAULT_NAME` 以 `_test` 结尾，否则 `t.Skip`，
  防止误在默认库上跑资金用例。
- 清理只按本用例创建的 `user_id` 删除三张钱包表数据，**不做** `DELETE FROM users` 全表清空、
  **不执行** `DROP TABLE`。
- 本地依赖：隔离容器 `my-shop-b104-mysql`（127.0.0.1:3306）、`my-shop-b104-redis`（127.0.0.1:6379，
  测试用 DB 5）。

### 9.3 用例清单（logic 包，25 项）

| 用例 | 覆盖 |
| --- | --- |
| `TestValidateRechargeAmount` | 金额 0/负数/1 分/上限/上限+1/int64 max |
| `TestRechargeOrderNoDeterministic` | 单号确定性、长度 32、跨用户/跨键不同 |
| `TestIdempotencyKeys` | 三类幂等键格式 |
| `TestNormalizePaging` | 分页默认/边界/越界 |
| `TestReplayFundResult` | 幂等回放一致/金额/用户/方向/类型不一致 |
| `TestDirectionAndStatusText` | 方向、充值状态枚举映射 |
| `TestValidTxType` | 流水类型白名单 |
| `TestMaxWalletBalanceMatchesUnsignedInt` | 上限 = `INT UNSIGNED` 最大值 |
| `TestRechargeNormalAndIdempotent` | 场景一/二：正常充值、重复充值、同键不同金额 |
| `TestRechargeDisabledByDefault` | 开关关闭 → 21009，无充值单/流水 |
| `TestRechargeAmountValidation` | 非法金额 → 21003，无副作用 |
| `TestRechargeOverflowRollback` | 充值单创建后余额溢出 → 整体回滚 |
| `TestConcurrentRechargeSameKey` | 8 并发同键，只入账 5000 一次、1 单 1 流水 |
| `TestConcurrentAccountInit` | 8 并发初始化，只 1 个账户 |
| `TestDebitForOrderSuccessAndIdempotent` | 场景三：10000 扣 6000 → 4000；重复支付幂等；同单不同金额 → 21006 |
| `TestDebitInsufficientBalance` | 场景四：3000 付 6000 → 21002，余额 3000，无流水 |
| `TestDebitExactBalanceBoundary` | 恰好足额扣到 0；再扣 1 分失败 |
| `TestCreditRejectsInvalidAmount` | 退款金额 0/-1 → 1001，无副作用 |
| `TestRollbackWhenTransactionInsertFails` | 余额已扣但流水插入失败 → 回滚，余额恢复 |
| `TestDebitRollbackWhenOrderAdvanceFails` | 扣款后订单推进失败 → 回滚 |
| `TestCreditForRefundAndIdempotent` | 场景五/六：4000 退款 6000 → 10000；重复退款不重复入账 |
| `TestCreditRollbackWhenRefundAdvanceFails` | 入账后退款状态推进失败 → 回滚 |
| `TestConcurrentDebitNeverNegative` | 场景七：10 并发各扣 3000，仅 3 成功、余额 1000、3 条流水 |
| `TestTransactionsPaginationAndIsolation` | 分页/倒序/类型过滤/用户隔离/非法参数 |
| `TestRechargeFlowIsRepeatableAcrossTransactions` | 三次充值逐笔前后余额闭合 |

### 9.4 HTTP 契约用例（2 项）

- `TestWalletHTTPFlow`：匿名 401（三个接口）、余额 0、充值成功、重复充值幂等、流水字段、
  他人隔离、同键不同金额 409/21008。
- `TestWalletHTTPValidation`：金额非法 400/21003、分页与类型非法 400/1001、关闭开关后 403/21009。

## 10. 安全性与故障回滚

### 10.1 Mock 充值安全

- `wallet.mock_recharge.enabled` 默认 `false`（代码默认值、YAML 均为 false）；
- 关闭时接口返回 21009，不创建充值单、不产生流水；
- 部署严格模式（`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true`）下开启该开关 → `boot.ValidateConfig`
  返回错误，进程启动失败；
- 单笔上限 1,000,000 分，余额上限显式校验，不做无限造钱；
- 不接入真实支付渠道（`pay_type` 固定 `mock`）。

### 10.2 故障回滚验证

| 故障场景 | 用例 | 结果 |
| --- | --- | --- |
| 充值单创建成功、余额更新失败（溢出） | `TestRechargeOverflowRollback` | 余额不变、充值单 0 条、流水 0 条 |
| 余额扣减成功、流水插入失败（唯一键冲突） | `TestRollbackWhenTransactionInsertFails` | 余额恢复、仅保留既有流水 |
| 钱包扣款成功、订单状态更新失败 | `TestDebitRollbackWhenOrderAdvanceFails` | 余额恢复、无扣款流水 |
| 退款入账成功、退款状态更新失败 | `TestCreditRollbackWhenRefundAdvanceFails` | 余额恢复、无退款流水 |

四项均由真实 MySQL 事务验证，不是 mock 断言。

## 11. 未完成事项

1. **订单余额支付入口未接入**（阻塞）：`order.Pay` 未增加余额渠道、未写 `pay_type`。
   原因：基线未合入 B1-04-03，任务书要求不得抢改 `internal/logic/order/order.go`。
   接入方式已固化为 `IWallet.DebitForOrderInTx`（同事务调用，幂等键 `order_pay:<order_id>`）。
2. **退款退回钱包未接入**（阻塞）：`refund.Execute` 成功分支未调用 `CreditForRefundInTx`。
   原因同上（`internal/logic/refund/refund.go`）。接入时必须判断原支付渠道为余额。
3. **无「订单已支付但钱包未扣款」的对账任务**：待订单接入后需要一次全量对账与告警。
4. 充值单查询接口、充值取消/过期、充值套餐（`recharge_plans`）未实现（超出本任务范围或未获决策）。
5. 钱包域错误码未在 `.agent/registry/error-codes.md` 登记（见 §12 阻塞事项）。

## 12. 已知问题与阻塞事项

### 12.1 全局门禁差异（任务书允许，未伪造通过）

| 门禁 | 基线状态 | 当前分支状态 | 说明 |
| --- | --- | --- | --- |
| `internal/cmd` `TestRouteTable` | PASS（142） | FAIL | 出现 3 条未登记路由（`GET /wallet`、`GET /wallet/transactions`、`POST /wallet/recharges`），实际 145 |
| `internal/cmd` `TestRouteCountByScope` | PASS（67/73/2） | FAIL | frontend 70 / admin 73 / health 2 = 145 |
| `internal/cmd` `TestRouteTableMatchesContract` | **FAIL（基线已失败）** | FAIL | 基线 openapi 已含 `GET /regions`、`GET /regions/{id}`、`PUT /me` 三条虚构接口且 openapi 计数 145；本任务新增 3 条钱包路由未登记 |
| `tools/openapi-gen` `TestUnusedComponentsOnRepo` | **FAIL（基线已失败）** | FAIL | `RegionDetailRequest`/`RegionListRequest`/`UserUpdateMeRequest` 未被引用（基线遗留） |
| `tools/openapi-gen` `TestParseRouteFilesAndControllers` | PASS | FAIL | routes 注册数 143 vs 期望 140（142 - 2 health） |
| `tools/openapi-gen` `TestD1KnownIssuesCoverAllRequiredFields` | PASS | FAIL | `POST /wallet/recharges` 的 required 字段未标注 B1-02-D1；受影响接口 14→15 |

未修改 `internal/cmd/routes_test.go`、`tools/openapi-gen/gen.go` 的 `expectedTotal`、
`openapi/`、`scripts/test-unit.sh`、`scripts/test-race.sh`、`internal/migrations/migrations_test.go`；
上述差异留给 B1-05 集成任务统一处理。

> `tools/openapi-gen` 仅新增钱包域元数据（`domains`、`entitySchemaNames`、`pathFileOrder`、
> `tagDescriptions`、`CodeWallet` 前缀映射），未改任何数量常量。

### 12.2 错误码 Registry 阻塞

- 任务书指定钱包域为 **21000**，代码已使用 21001-21010；
- 但基线 `.agent/registry/error-codes.md` 只登记到域序 19（19000-19999），**没有域序 20/21**；
- 按 Registry 规则，域必须在 Registry 中有独立 RESERVED 记录才算有效预留；
- 本任务**未修改** `.agent/registry/error-codes.md`（不属于可写范围，且不应在 Feature Branch 内私留预留），
  该缺口需 Owner 通过独立 Registry 变更补登记。

### 12.3 其它已知限制

- 充值幂等依赖客户端提供稳定的 `idempotency_key`；未提供时 DTO 校验返回 1001（不会静默生成随机键）。
- 余额支付/退款入账的「原支付渠道」判定在调用方（订单/退款模块），钱包只提供资金原语。

## 13. 交付结论

```text
任务：B1-04-04
分支：feat/b1-04-04-wallet
基线 SHA：af512f9e8a64c133c705d3a0ed1dd106de3d5c60
最终 HEAD：本报告提交（实现提交 3954ef6034b737fc23f67fe77cd8c2907a9a65bb）
工作区状态：干净（提交后）

完成的业务能力：
- 钱包余额查询（按需初始化、一人一账户）
- 钱包资金流水分页查询（类型筛选、本人隔离）
- Mock 充值（同事务、确定性单号幂等、金额上限、安全开关）
- 事务内资金原语（DebitForOrderInTx / CreditForRefundInTx，不自开事务）
- 资金流水幂等（recharge:/order_pay:/refund: 三类键）
- 账户行锁 + 条件 UPDATE 的并发安全
- 钱包错误码域 21001-21010

未完成的业务能力：
- 订单余额支付入口（order.Pay）未接入
- 退款退回钱包入口（refund.Execute）未接入
- 充值单查询/取消、充值套餐、资金对账任务

新增 API：
- GET  /wallet
- GET  /wallet/transactions
- POST /wallet/recharges

修改的核心文件：
- api/wallet/v1/wallet.go（新增）
- internal/controller/wallet/wallet.go（新增）
- internal/service/wallet.go（新增）
- internal/logic/wallet/wallet.go、wallet_fund.go（新增）
- internal/codes/codes.go（追加 21001-21010）
- internal/config/contract.go、strict.go（新增 wallet.mock_recharge.enabled + 安全闸门）
- internal/boot/boot.go（调用安全闸门）
- internal/cmd/routes_frontend.go（仅追加 3 条钱包路由）
- internal/logic/logic.go（注册 wallet 包）
- manifest/config/config.yaml、docs/development/configuration.md
- tools/openapi-gen/{gen,model,parse_codes}.go（仅域元数据）
- docs/design/{wallet,order,refund,error-codes}.md、docs/dev/wallet.md

Migration：无新增（复用 20261110000006_wallet / 20261110000007_recharge_orders）
错误码：21001-21010（钱包域）

go build：PASS
go vet：PASS
单元测试：PASS（钱包 logic 25 项含集成；config 全包通过，含新增 4 项安全闸门用例）
集成测试：PASS（真实 MySQL my_shop_b104_test；HTTP 契约 2 项通过）
并发测试：PASS（8 并发充值同键、10 并发扣款、8 并发账户初始化）
Race：PASS（go test -race ./internal/logic/wallet/）

资金事务验证：PASS（充值/扣款/退款入账均与流水同事务；4 类故障场景全部回滚成功）
充值幂等验证：PASS（同键重复 8 并发只入账一次；同键不同金额 21008）
支付幂等验证：PASS（同一订单重复扣款 AlreadyApplied，余额与流水不变；不同金额 21006）
退款幂等验证：PASS（同一退款重复入账只加一次；不同要素 21007）

已知问题：
- 全局路由/OpenAPI 计数门禁失败（145 vs 142、143 vs 140、D1 计数 14→15），属并行新增 API 的预期差异
- 基线已有失败：TestRouteTableMatchesContract、TestUnusedComponentsOnRepo（Region* 组件）

阻塞事项：
1. 订单支付与退款执行集成阻塞于 B1-04-03 未合入（未修改 order.go / refund.go）
2. 错误码域 21000 未在 .agent/registry/error-codes.md 登记，需 Owner 补 Registry

交付结论：PARTIAL
```

**结论说明**：独立钱包内核（账户、流水、Mock 充值、幂等、并发安全、事务内扣款/入账原语）已完整交付
并通过真实数据库的一致性、并发与故障回滚验证；订单余额支付与退款退回钱包因基线未合入 B1-04-03、
按任务约束不得抢改 `order.go` / `refund.go`，故本任务不宣称 PASS。
