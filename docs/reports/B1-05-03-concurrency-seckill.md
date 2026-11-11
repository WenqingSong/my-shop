# B1-05-03 并发、事务与秒杀回归测试报告

> 本任务属于 B1-05 后端最终验收阶段：验证 B1-04 新增业务在真实并发下的正确性，
> 并确保既有秒杀系统没有回归。**未修改任何生产业务代码与秒杀内核。**

---

## 1. 任务基本信息

| 项目 | 内容 |
| --- | --- |
| 任务编号 | B1-05-03 |
| 开发分支 | `feat/b1-05-03-concurrency-seckill` |
| 基线分支 | `feat/p2-backend` |
| 基线 Commit | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 最终 Commit | 见紧随本报告之后的 docs 回填提交（本报告随代码提交入库） |
| Git 工作区 | 独立 Worktree `/root/projects/my-shop-b1-05-03`（主工作区由 `feat/p2-backend` 与 `feat/b1-05-01` 分支占用） |
| 测试环境 | linux/amd64 · Go 1.26.0 · MySQL 8.0（compose，127.0.0.1:3306）· Redis 7-alpine（测试专用 DB 15） |

### 基线处理过程

1. `git fetch origin` 后发现本地 `feat/p2-backend` 落后远端 35 个提交、领先 0 个提交，
   工作区干净（`git status --short` 无输出），满足 `git pull --ff-only` 的前置条件；
2. `git pull --ff-only` 快进到 `d1fa065`；
3. `git worktree add ../my-shop-b1-05-03 -b feat/b1-05-03-concurrency-seckill feat/p2-backend`
   创建独立工作区，避免与其它任务分支互相污染；
4. 开发期间分支始终为 `feat/b1-05-03-concurrency-seckill`，未切换、未合并、未推送。

### 实际修改文件

| 类型 | 文件 |
| --- | --- |
| 修改 | `scripts/test-race.sh` |
| 新增 | `internal/logic/concurrency/harness_test.go`、`inventory_concurrency_test.go`、`wallet_concurrency_test.go`、`groupbuy_concurrency_test.go`、`bargain_concurrency_test.go`、`coupon_concurrency_test.go` |
| 新增 | `internal/logic/flashsale/regression_test.go` |
| 新增 | `docs/reports/B1-05-03-concurrency-seckill.md` |

生产代码（含秒杀内核、订单/钱包/退款业务、Migration、OpenAPI 生成物、`routes_test.go`、
`tools/openapi-gen/gen.go`）**零修改**，由 `git diff --name-only` 可核验。

---

## 2. 原有测试体系调查

### 2.1 原有覆盖范围与数量

改造前 `scripts/test-race.sh` 共 8 次 `go test` 调用，按执行顺序：

| 阶段 | 包 | 用例数 | 说明 |
| --- | --- | --- | --- |
| 熔断器状态机 | `internal/logic/flashsale` | 2 | 纯内存 |
| Producer 逐条结果快照 | `internal/mq/kafka` | 6 | 纯内存（F-03） |
| B4 秒杀 Runtime 装配 | `internal/worker` | 14 | 纯内存 |
| Consumer Group 状态机 | `internal/mq/kafka` | 2 | 纯内存 |
| 指标并发读写 | `internal/metrics` | 9 | 纯内存 |
| HTTP drain/强制关闭 | `internal/cmd` | 7 | 纯内存 |
| Outbox 并发可靠性 | `internal/logic/flashsale` | 11 | 真实 MySQL（`my_shop_outbox_race`） |
| B3 Consumer 并发 | `internal/logic/flashsale` | 2 | 真实 MySQL（同上） |

合计 **53 项**（上述数字用改造后脚本同一正则经 `go test -list` 枚举得到；既有阶段的用例集合未被改动）。

仓库内相关测试资产（调查结论）：

- `internal/logic/flashsale/*_test.go`：约 98 个用例，覆盖 V1–V5 秒杀（闸门/预热/对账/消费/Outbox/修复），
  其中 8 个 `*RealKafka*` 用例需要真实 Broker，缺环境时 `t.Skip`；
- `internal/logic/wallet/wallet_integration_test.go`：真实 MySQL 资金一致性（串行场景 + 幂等），
  自带 `DATABASE_DEFAULT_NAME` 必须以 `_test` 结尾的护栏；
- `test/e2e/flashsale*_test.go`：20 个 HTTP 端到端用例（含 `TestFlashSaleV3FailClosed`）、
  拼团/优惠券 e2e 并发用例；
- `internal/migrations/*_test.go`：含 `DROP TABLE` 的迁移测试，**无库名护栏**；
- `scripts/test-integration.sh`：未设置 `DATABASE_DEFAULT_NAME`，默认落到开发库 `my_shop`。

### 2.2 原有测试执行结果

既有 53 项在本次改造后的脚本中以**完全相同的包与正则**执行，**53/53 PASS**（见第 4、5 节的分阶段证据）。

### 2.3 本次发现的缺口

| 缺口 | 影响 |
| --- | --- |
| 缺「B1-04 五类业务 × 真实数据库 × 并发」的统一门禁（钱包只有串行集成测试，库存无并发测试） | 无法证明 B1-04 的条件更新/唯一约束在并发下成立 |
| 纯内存阶段的 `go test -run` 无存在性校验，测试改名后会「零匹配仍返回 0」 | 静默假 PASS 风险 |
| 秒杀 Lua 闸门没有直接回归（只有 e2e 间接覆盖） | 闸门返回码矩阵缺失 |
| fail-closed 只有 e2e 覆盖，未进入 `make test-race` | race 门禁无法发现降级回归 |
| `scripts/test-race.sh` 只 `CREATE DATABASE IF NOT EXISTS`，不清理历史残留 | 重复执行可能被脏数据干扰 |
| `coupon_batches`（发行批次）只有 schema，没有批次领取实现 | 批次级限领无法验证（见遗留问题） |

---

## 3. 五类并发测试

实现位置：`internal/logic/concurrency/`（新增测试包，仅测试文件）。所有写路径均调用真实业务服务与
真实 MySQL 事务，不使用 Mock、不做纯内存模拟；数据库为脚本自建的 `my_shop_b10503_test`。

### 3.1 库存扣减并发（3 项）

- **测试目标**：验证 `inventories` 条件扣减 `quantity >= ?` + `inventory_logs` 流水在并发下不超卖、不产生幽灵流水。
- **并发场景**：① 初始 25 件，3 轮 × 40 并发 = 120 次扣 1 件；② 初始 10 件，30 并发；③ 初始 100 件，2 轮 × 40 并发混合「+3 / -2」。
- **测试方法**：并发屏障（`close(start)`）后同时发起，逐条统计成功/`6001`，再按 `id ASC` 校验流水链。
- **核心断言**：成功数严格等于可扣数量；最终库存为 0 且不为负；`before_qty == 上一条 after_qty`、
  `after == before ± change_qty`；流水条数 = 1 条初始化 + 成功数（失败请求零流水）；混合增减满足账实相等。
- **实际执行数量**：3 项，全部 PASS。
- **测试结果**：PASS。

### 3.2 钱包余额扣减并发（4 项）

- **测试目标**：验证 `DebitForOrderInTx` 的「行锁 + 幂等键 + 条件更新 + 同事务流水」。
- **并发场景**：① 余额 1000，40 并发各扣 30（不同订单）；② 同一订单（同一幂等键）20 并发扣 100；
  ③ 余额 3000，30 并发混合 100/200/300；④ 扣款成功后由调用方回滚。
- **测试方法**：`g.DB().Transaction` 内调用真实服务，与订单支付同一事务边界；逐条比对账户余额与流水。
- **核心断言**：无负余额、无超额扣款；同订单并发只扣 1 次（1 条流水、余额只减 1 次）；
  流水 `balance_before/after` 严格串行自洽且末值等于账户余额；
  回滚后余额/流水/幂等键全部回滚，同一订单可重新扣款。
- **实际执行数量**：4 项，全部 PASS。
- **测试结果**：PASS。

### 3.3 拼团参团并发（3 项）

- **测试目标**：验证 `joined_count < required_size` 条件更新、`uk_team_user`/`uk_order_id`/`uk_user_idempotency`
  与「成团只推进一次」。
- **并发场景**：① 团上限 3 人（团长占 1），10 个不同用户并发抢 2 个名额；② 同一用户并发参团 10 次；
  ③ 同一用户用同一幂等键参团两个不同的团（第二个订单必冲突）。
- **测试方法**：真实 `CreateTeam`/`JoinTeam`（含真实下单、扣库存、状态流水）。
- **核心断言**：不超员（`joined_count == required_size`、参与记录 == 人数）；成团后 `status=20` 且 `closed_at` 非空；
  参与记录与订单一一对应且订单真实存在；同一用户只 1 条参与 + 1 个订单；
  失败事务不虚增人数、不留参与记录、不改团状态、不多扣库存。
- **实际执行数量**：3 项，全部 PASS。
- **测试结果**：PASS。并发重复参团在竞态窗口可能返回 `9006`（订单幂等键冲突）而非幂等成功，
  但副作用为零、不变量成立（详见遗留问题）。

### 3.4 砍价助力并发（4 项）

- **测试目标**：验证 `active_key` STORED 生成列 + `uk_instance_helper` + 底价条件更新。
- **并发场景**：① 同一用户并发发起 8 次砍价；② 同一助手并发助力 10 次；
  ③ 20 个不同助手并发助力（2000→1000 底价，每次名义 300）；④ 放弃后重新发起。
- **测试方法**：真实 `Start`/`Help`/`Abandon`；测试代码从不写 `active_key`，只读校验。
- **核心断言**：同一活动同一用户仅 1 个进行中实例（`active_key = "activity:user"`）；
  同一助手只 1 条助力记录；价格永不低于底价、不为负；助力记录 `price_after = price_before - cut` 且首尾相接，
  `Σcut == start_price - current_price`；到底价后 `status=20`、`active_key` 变 NULL、只推进一次；
  `information_schema` 确认 `active_key` 为 `STORED GENERATED`。
- **实际执行数量**：4 项，全部 PASS。
- **测试结果**：PASS。

### 3.5 优惠券领取并发（3 项）

- **测试目标**：验证 `issued_quantity < total_quantity` 原子闸门 + `uk_user_template_claim` 防重复。
- **并发场景**：① 发行 5 张、30 个用户并发抢；② 同一用户并发领 10 次（总量 100）；③ 串行重复领取 + 绕过服务直插。
- **测试方法**：真实 `Claim`（真实事务）；失败场景额外验证数据库唯一键兜底。
- **核心断言**：成功数 == 发行量，`issued_quantity ==` 实际用户券数；同一用户只 1 张，
  且失败领取的 `+1` 必须随事务回滚（`issued_quantity` 不虚增）；重复领取返回 `19004`，
  直插 `claim_no=1` 被唯一键拒绝（1062）。
- **实际执行数量**：3 项，全部 PASS。
- **测试结果**：PASS。

> 说明：`coupon_batches` 只有建表，没有批次领取入口；本次覆盖的是实现中真实存在的
> 「模板总量闸门 + 唯一约束」路径，批次级限制列入遗留问题。

---

## 4. 秒杀回归

新增：`internal/logic/flashsale/regression_test.go`（8 项）；同时执行既有 90 项与 e2e 20 项。
秒杀生产内核（`redis.go` Lua、`flashsale.go`、`consume.go`、`outbox.go`、`kafka_consumer.go`、
`circuit_breaker.go`）**未做任何修改**。

### 4.1 Redis Lua 准入闸门（`TestFlashSaleRegressionLuaGateMatrix`）

真实 Redis 执行真实 Lua，覆盖：正常准入（`GATE_PASSED` + 预扣 + 一人一单/幂等标记原子写入）、
幂等重试（`IDEMPOTENT_HIT`，不重复预扣）、同用户换键（`ALREADY_PURCHASED`）、另一用户准入、
SKU 未绑定（`SKU_NOT_BOUND`）、活动不存在（`NOT_FOUND`）、未开始（`NOT_IN_WINDOW`）、已停用（`NOT_FOUND`）。PASS。

### 4.2 库存售罄（`TestFlashSaleRegressionLuaGateSoldOut`、`...GateConcurrentNoOversell`、
`...SoldOutConvergesWithDB`）

库存 0 拒绝并写售罄标记、标记后快速失败、Redis remaining 不为负；40 并发抢 5 件恰好 5 次准入；
经 `CreateOrder` 受理 2 件、第 3 个请求 `12003`，消费后 MySQL `sold=2`、订单 2、Redis remaining=0，
缓存与数据库收敛一致。PASS。

### 4.3 重复购买拦截（`...GateDuplicateUser`、`...DuplicatePurchaseSingleOrder`）

同一用户同一幂等键并发 20 次 → 1 次准入 + 19 次幂等命中，只预扣 1 件；
不同幂等键并发 20 次 → 1 次准入 + 19 次一人一单拦截，只预扣 1 件；
同一用户换键再抢返回 `12004`，消费后仅 1 个有效订单；原幂等键重复提交返回既有请求状态。
Kafka 重复消费场景见 4.5。PASS。

### 4.4 Redis 故障时 fail-closed（`TestFlashSaleRegressionFailClosedOnRedisFault`）

把活动 Hash 改成 string 使 Lua `HGETALL` 报 `WRONGTYPE`，连续两次 `CreateOrder` 均返回
`CodeServiceUnavailable(1005)`，且：

- `flash_sale_order_requests` = 0（不落请求，不绕过闸门）；
- `flash_sale_orders` = 0（不创建有效秒杀订单）；
- MySQL `sold` = 0（不降级为直接 MySQL 抢购、不扣库存）；
- Redis remaining 不变（不预扣）。

清除故障并重新预热后同一活动可正常受理，证明 fail-closed 不是永久降级。
注：`redis.go` 中 `runGate` 的注释仍写着「调用方应降级走纯 MySQL 路径」，与实现不符；
本次断言以 `flashsale.go` 的实际行为（fail-closed）为准，未修改该注释。PASS。

### 4.5 Kafka 消费幂等（`TestFlashSaleRegressionKafkaRedeliveryConsumesOnce`）

直接驱动 Kafka 消费入口 `ConsumeRequest`：首次 `success` 落单，重复投递返回 `duplicate` 且不重复落单；
订单数 1、`sold` 1、请求终态 `success`。既有 `TestConsumeCrashBeforeCommitRedeliversOnce`、
`TestConsumeInterruptedByCtxCancel`、`TestConsumerRaceKafkaAndScannerSameRequest` 等共同覆盖
「崩溃重投只产生一次业务效果」。PASS。

### 4.6 Outbox 可靠性

保留原有 11 项并发用例（领取不重叠、多 Worker 不死锁、租约接管、旧 Token 失效、人工修复竞争、
ACK 成功但标记失败可恢复、终态不被回写、结果未知重发、部分 ACK 不放大重复、标记不等待业务行锁、
标记超时不写 `published_at`），全部 PASS；不新增重复用例。

---

## 5. Race 检查

### 5.1 执行命令

```bash
go build ./...
go vet ./...
bash -n scripts/test-race.sh
make test-race          # 全部门禁，含 -race
```

### 5.2 实际执行的测试数量与结果

| 阶段 | 用例数 | 结果 |
| --- | --- | --- |
| 纯内存共享状态（6 个 go test 调用） | 40 | PASS |
| Outbox 并发可靠性 | 11 | PASS |
| Consumer 并发落单 | 2 | PASS |
| B1-05-03 五类并发 | 17 | PASS |
| B1-05-03 秒杀回归 | 8 | PASS |
| 秒杀既有回归全集（排除 RealKafka） | 90 | PASS |
| 秒杀 HTTP 端到端回归（`test/e2e`） | 20 | PASS |
| **合计** | **188** | **全 PASS** |

所有阶段均以 `-race -count=1 -v` 执行；`go build ./...`、`go vet ./...` 均无输出（成功）。
脚本整体耗时约 2 分 05 秒，退出码 0。

### 5.3 竞态问题

未发现数据竞争（`WARNING: DATA RACE` 零出现）。另外对新增测试做了 `-race -count=3` 的三轮重复执行，
结果稳定（`internal/logic/concurrency` 16.5s、`TestFlashSaleRegression` 10.8s，均 PASS），无 flaky。

---

## 6. 测试脚本改造

`scripts/test-race.sh` 从「只依赖 `go test` 退出码」升级为「**没有真实执行就绝不允许 PASS**」：

### 6.1 保留项

- 原有 6 次纯内存 `go test` 调用、包路径、`-run` 正则全部保留；
- 原有 11 项 Outbox 用例清单与 2 项 Consumer 用例清单原样保留（含逐条 `--- PASS:` 校验）；
- 原有隔离库 `my_shop_outbox_race` 及其「MySQL 不可用即失败、不跳过」语义保留；
- 未删除任何阶段，未使用 `|| true`。

### 6.2 新增项

| 变化 | 说明 |
| --- | --- |
| 通用阶段执行器 `run_stage` | 同一正则先 `go test -list` 枚举，再 `go test -race -count=1 -v -run` 执行 |
| 零匹配拦截 | 枚举数为 0 → 立即 `fail`，绝不打印 PASS |
| 逐条 PASS 校验 | 每个枚举到的测试名必须出现 `^--- PASS: <name> `，并校验 PASS 行数 == 枚举数 |
| SKIP 不算通过 | 被跳过的用例不会有 PASS 行，直接判失败 |
| 退出码检查 | `if ! go test ... | tee`，管道 `pipefail`，非零立即失败 |
| 库名白名单 | `assert_owned_db` 只允许 `my_shop_*_test` / `my_shop_*_race`，显式拒绝 `my_shop`、`my_shop_b10304_verify` 等 |
| 每轮重建库 | `create_fresh_db` 先 `DROP` 再 `CREATE`，消除历史残留导致的假失败/假通过 |
| Redis 隔离 | 所有数据库阶段使用 `REDIS_DEFAULT_DB=15`，不再 Flush 开发用的 DB 0 |
| 新增 3 个阶段 | 五类并发（17 项）、秒杀回归（8 项）、秒杀既有全集（90 项）、HTTP 端到端（20 项） |

### 6.3 反作弊机制有效性（负向验证）

为避免「机制只是写在注释里」，用临时副本做了两次负向验证（验证后已删除临时文件）：

1. 把首个阶段正则改为不存在的 `TestB10503PatternThatDoesNotExist`
   → 退出码 `1`，输出：`阶段「秒杀熔断器状态机」零匹配：...（脚本拒绝报告 PASS）`；
2. 把首个阶段正则改为缺 Broker 会 `t.Skip` 的 `TestConsumerRealKafkaEndToEnd`
   → 退出码 `1`，输出：`实际 PASS 0/1 项（PASS 行 0 行）；未通过: TestConsumerRealKafkaEndToEnd`。

---

## 7. 数据安全

| 项目 | 做法 |
| --- | --- |
| 测试数据库 | `my_shop_outbox_race`（沿用）、`my_shop_b10503_test`（五类并发）、`my_shop_b10503_flash_test`（秒杀回归） |
| 创建/销毁 | 脚本 `create_fresh_db` 前先 `DROP`，`trap cleanup EXIT` 统一销毁，运行结束不留库 |
| 库名校验 | `assert_owned_db` 白名单式校验；另在测试代码内再次校验 `DATABASE_DEFAULT_NAME == my_shop_b10503_test` 且 `SELECT DATABASE()` 一致 |
| 触碰开发库 | 无。运行后 `SHOW DATABASES` 仅剩 `my_shop`（`information_schema/mysql/performance_schema/sys`），`my_shop` 表数为 0 |
| 历史迁移验证库 | 本次运行环境不存在 `my_shop_b10304_verify`；脚本对该名称显式拒绝，任何情况下不会执行清理 |
| Redis | 专用 DB 15，不 Flush DB 0 |
| 真实用户/订单/资金 | 不使用：所有用户、地址、商品、订单、钱包账户均为测试内自建，并在每个用例开始时清空本包相关表 |

补充：测试代码中的清理只作用于「已通过库名/连接库双重校验」的独立库；钱包相关用例仍沿用
`WHERE user_id = ?` 的定点清理风格。

### 未修复但已记录的既有风险（超出本任务范围）

- `scripts/test-integration.sh` 未设置 `DATABASE_DEFAULT_NAME`，默认落到 `my_shop`，而同脚本会跑
  `internal/migrations` 的 `DROP TABLE` 用例与多个 controller 的整表 `DELETE`。本任务未运行、
  也未修改该脚本，建议 Owner 单独排期修复。

---

## 8. 修改文件清单

**修改**

- `scripts/test-race.sh`

**新增**

- `internal/logic/concurrency/harness_test.go`
- `internal/logic/concurrency/inventory_concurrency_test.go`
- `internal/logic/concurrency/wallet_concurrency_test.go`
- `internal/logic/concurrency/groupbuy_concurrency_test.go`
- `internal/logic/concurrency/bargain_concurrency_test.go`
- `internal/logic/concurrency/coupon_concurrency_test.go`
- `internal/logic/flashsale/regression_test.go`
- `docs/reports/B1-05-03-concurrency-seckill.md`

**未修改（核验项）**：`internal/logic/flashsale/` 下所有非 `_test.go` 生产文件、`internal/logic/order|wallet|refund|inventory|groupbuy|bargain|coupon/*.go` 生产实现、
`internal/migrations/sql/*`、`openapi/*`、`api/*`、`tools/openapi-gen/gen.go`、`*routes_test.go`、其它任务脚本。

---

## 9. 遗留问题

1. **优惠券发行批次无领取实现**：`coupon_batches` 在 B1-03-03 建表，但 `coupon.Claim` 只走模板级
   总量闸门，没有批次级入口/每人限领校验。本次测试覆盖的是真实实现路径；批次级限制需业务补齐后另行验证。
2. **拼团并发重复参团返回 `9006`**：同一用户在同一竞态窗口内重复提交时，第二个请求可能撞
   `orders.uk_user_idempotency` 返回 9006（而非幂等返回既有结果）。副作用为零（整体回滚），
   业务不变量成立；若希望返回幂等成功，需 Order 侧在冲突时回读既有订单，属产品语义决策。
3. **既有用例 `TestConsumeRequestRepairDeadToQueued` 不可在脏库重复执行**：该用例插入
   `admins.username='repair-admin'` 但从不清理，同一库第二次执行必然 1062 失败。
   本次通过「每轮重建独立库」规避，未修改该用例（属其它任务的测试资产）。
4. **`scripts/test-integration.sh` 默认连接 `my_shop`**：与「禁止对生产/开发库执行清理」冲突，
   建议改为显式 `DATABASE_DEFAULT_NAME` 并在脚本内建库/销毁。
5. **`internal/migrations/*_test.go` 的 `DROP TABLE` 无库名护栏**、`bargain`/`freight` 集成测试硬编码库名：
   同样是测试隔离缺陷，建议统一到 `_test` 后缀 + `SELECT DATABASE()` 双检。
6. **e2e 全量套件未纳入 `make test-race`**：本任务只纳入 `test/e2e` 中 `TestFlashSale*` 的 20 项，
   以避免门禁时长进一步膨胀；其余 e2e 仍由既有 `make test` 路径覆盖。

---

## 10. 最终验收结论

| 验收项 | 结论 | 依据 |
| --- | --- | --- |
| 并发测试（五类） | **PASS** | 17 项真实数据库并发用例全绿（含 `-race`、`-count=3`） |
| 秒杀回归 | **PASS** | 新增 8 项 + 既有 90 项 + e2e 20 项全绿；fail-closed 断言 1005/503 |
| Race 检查 | **PASS** | 188 项均以 `-race` 执行，无 `DATA RACE`，无 flaky |
| 测试脚本 | **PASS** | `bash -n` 通过；零匹配/SKIP 负向验证均返回非零；真实执行 188 项 |
| 数据库隔离 | **PASS** | 3 个脚本自建库运行后全部销毁；`my_shop` 表数 0；未触碰 `my_shop_b10304_verify` |
| 生产代码零修改 | **PASS** | `git diff --name-only` 仅 `scripts/test-race.sh`，其余为新增测试与报告 |

`go build ./...`、`go vet ./...`、`make test-race` 均成功（退出码 0）。
