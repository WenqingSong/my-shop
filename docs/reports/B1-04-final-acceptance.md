# B1-04 最终集成验收与质量收尾报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-FINAL-ACCEPTANCE：B1-04 最终集成验收与质量收尾 |
| 分支 | `feat/p2-backend` |
| 验收基线 SHA | `05a84c2`（工作区干净，`ahead 25`） |
| 本轮修复提交 | `63cd568` |
| 本报告提交 | 提交后回填（见 `git log -1`） |
| 执行日期 | 2026-11-11 |
| 隔离测试库 | `my_shop_b104_test`（本轮新建）、`my_shop_bargain_test`（既有，测试自清理） |
| 业务库 `my_shop` | 仅只读查询，未作为任何写操作目标 |
| 最终结论 | **PARTIAL**（地区字典数据源外部阻塞 + 1 项 P1 并发残留） |

## 1. 最终结论

**PARTIAL：主体完成，但存在未解决的验收事项。**

已核实成立的部分：

1. 八个子任务（B1-04-01 ~ B1-04-08）的合并提交全部是当前 HEAD 的祖先，实现文件、迁移、路由与测试均存在且可运行。
2. 基础质量门禁全绿：`gofmt`、`go build`、`go vet`、单元测试、完整 Race 脚本、Migration 门禁、OpenAPI 契约、Registry 校验退出码均为 0。
3. 210 条常驻 API + 4 条 planned 占位、490 个 Schema 与真实 GoFrame 路由表双向一致，无孤儿接口、无遗漏路由、无重复错误码/权限码。
4. 订单、计价、钱包、退款、会员、分销、拼团、砍价的核心链路均可在真实 MySQL 上运行并通过既有集成/E2E/Race 用例。

未解决的部分（详见第 5、7 节）：

1. **P0（外部阻塞）**：B1-03-04 的地区字典源数据（`system_city`，3938 条）在本环境不可获得，隔离测试库 `regions` 为空，导致 5 个 Address/Region 用例无法验证。**未伪造数据、未删除测试、未把失败标记为通过。**
2. **P1（真实缺陷，未在本轮修复）**：分销推荐关系的成环检测存在并发窗口，两个用户并发互绑可形成 A→B→A 双节点环；顺序场景受保护，且无资金影响（一级分销不遍历链路）。需要 Owner 决定是否在 B1-05 前修复。
3. **P2 / Deferred**：积分抵扣、砍价自动下单、佣金真实打款、拼团已支付订单自动退款、砍价过期扫描等已登记，均不属于 B1-04 强制交付范围（详见第 7 节）。

因此：**B1-04 暂不具备「正式关闭」条件**，但除地区字典数据外，不存在阻断 B1-05 的功能性缺陷。

## 2. Git 基线

### 2.1 开工前核实

| 检查 | 结果 |
| --- | --- |
| `git branch --show-current` | `feat/p2-backend` |
| `git log -1` | `05a84c2 docs(report): 回填 B1-04 拼团集成最终提交 SHA（仅元数据）` |
| `git status --short` | 空（无未提交修改、无未跟踪文件） |
| `git status -sb` | `## feat/p2-backend...origin/feat/p2-backend [ahead 25]` |
| `git remote -v` | `origin https://cnb.cool/framework-station/my-shop` |
| 破坏性操作 | 未执行（无 `reset --hard`、无 `clean -fd`、无 `force push`） |

基线 SH 与上一轮集成报告一致；本轮未假定 HEAD，已重新核实。

### 2.2 八个子任务集成提交（均为 HEAD 祖先，`git merge-base --is-ancestor` 逐项验证）

| 任务 | 合并提交 | 是否在 HEAD | 远端源分支 | 源分支 HEAD |
| --- | --- | --- | --- | --- |
| B1-04-01 用户资料/地区/地址 | `10f05fa` | YES | `origin/feat/b104-01-profile-region-address` | `5f233ee` |
| B1-04-02 规格/SKU/商品详情 | `60c0751` | YES | `origin/feat/b1-04-02-sku-spec-product-detail` | `ac09d69` |
| B1-04-03 计价/运费/订单 | `d1400cc` | YES | `origin/feat/b1-04-03-order-pricing-freight` | `9737072` |
| B1-04-04 钱包/充值/余额支付 | `2446b5d` | YES | `origin/feat/b1-04-04-wallet` | `37750fb` |
| B1-04-05 一级分销/后台管理 | `51d88ef` | YES | `origin/feat/b104-05-distribution` | `cae9638` |
| B1-04-06 拼团 | `5c2e3b3` | YES | `origin/feat/b1-04-06-group-buy` | `d93278e` |
| B1-04-07 砍价 | `0a67d5d` | YES | `origin/feat/b1-04-07-bargain` | `0865a9c` |
| B1-04-08 会员/分销扩展 | `7e7cede` | YES | `origin/feat/b1-04-08-member-distribution` | `deebff2` |

八个源任务分支在远端全部保留，HEAD 与上次交付报告一致；本轮未删除任何分支或 Worktree。

### 2.3 本轮新增提交

| 提交 | 内容 | 文件 |
| --- | --- | --- |
| `63cd568` | `fix(b1-04): 修正迁移版本断言漂移与计价/分销文档偏差` | `internal/logic/freight/integration_test.go`、`internal/migrations/migrations.go`、`internal/logic/pricing/pricing.go`、`internal/logic/distribution/distribution.go`、`docs/design/pricing.md` |
| 本报告提交 | `docs(report): B1-04 最终验收报告` | `docs/reports/B1-04-final-acceptance.md` |

### 2.4 Push 状态

未执行 Push、未合并 `main`/`develop`。基线时 `ahead 25`，本轮新增 2 个提交后为 `ahead 27`（提交后核实），等待 Owner 指示。

## 3. 八个任务验收表

| 任务 | 核心实现（事实） | 集成状态 | 测试结果 | 遗留问题 |
| --- | --- | --- | --- | --- |
| **B1-04-01** 用户资料、地区字典、地址结构化地区 | `users.birthday`、`addresses.region_id`（`20261110000015`）；`regions` 三级字典 API（`/regions`、`/regions/tree`、`/regions/:id`）；地址写入时以字典为准并校验省-市-区链路（`internal/logic/address/address.go:324` `resolveAddressRegion`） | 已合入 | `TestAddressRegionValidation`、`TestProfile*` PASS；`TestRegionListByParent`/`TestRegionTree`/`TestRegionDetail`、`TestAddressRegionCreateAndUpdate`、`TestAddressDefaultUniqueAcrossUsers` **FAIL（地区字典为空，环境夹具）** | 地区字典数据缺失（P0，见 §5） |
| **B1-04-02** 规格组/规格值/SKU 组合/商品详情/服务端定价 | `product_specs`/`product_spec_values`/`sku_spec_values`、`skus.sku_code`、`spec_signature`（`20261110000001`、`20261111000001`）；下单按 `sku_code` 定位、事务内重读 `skus.price` | 已合入 | `test/e2e`：`TestOrderPricingBySkuCode`、`TestOrderRejectsForgedPrice`、`TestOrderRejectsInvalidSkuCode`、`TestCartIgnoresClientPrice` PASS | 无（快照/定价口径见 §6.1） |
| **B1-04-03** 统一计价/运费模板/订单算价/事务复核/幂等/状态流水 | `pricing.Quote` 唯一入口；`freight_templates`/`freight_template_rules`（`20261111000002`）；`orders` 物流/支付字段、`order_status_logs`；`uk_user_idempotency` + `request_hash` | 已合入 | `internal/logic/pricing`、`internal/logic/freight`、`internal/logic/order`、`test/e2e` 全 PASS；本轮修复 1 处版本断言漂移 | 管理员 `ship`/`refund` 流水 `operator_id=0`（P2） |
| **B1-04-04** 钱包/Mock 充值/余额支付/退款入账 | `wallet_accounts`/`wallet_transactions`/`recharge_orders`（`20261110000006/7`）；扣款条件 `balance >= ?`；幂等键 `order_pay:<id>`、`refund:<id>`、`recharge:<id>`；Mock 充值默认关闭（`WALLET_MOCK_RECHARGE_ENABLED=false`） | 已合入 | `internal/logic/wallet` 全部 PASS；`TestOrderWalletPayAndRefundAtomicity` PASS | 真实渠道充值/提现未做（阶段外） |
| **B1-04-05** 一级分销/佣金/退款冲回/后台管理 | `user_referrals`（`uk_user_id`）、`distribution_commissions`（`uk_order_id`、`uk_refund_id`）；成环校验 `requireAcyclicInTx`；人工结算仅状态流转；佣金比例默认 0bp | 已合入 | `TestBindRules`、`TestBindRejectsLongerCycle`、`TestCommissionAccrualAndIdempotency`、`TestCommissionReversalIdempotency`、`TestCommissionSettleStateMachine`、`TestCommissionConcurrentAccrual`、`test/e2e` 分销 10 例 PASS | 并发互绑成环窗口（P1，见 §7 #11） |
| **B1-04-06** 拼团活动/开团/参团/名额并发/过期/订单集成 | `group_buy_activities`/`teams`/`participants`（`20261110000011`）；名额条件 UPDATE；`uk_team_user`、`uk_order_id`；`worker.StartGroupBuyExpireScanner`（60s）；团订单复用 `pricing.Quote` | 已合入 | `test/e2e`：`TestGroupBuyActivityManagement`、`TestGroupBuyCreateTeamOrdering`、`TestGroupBuyJoinSuccessAndIdempotency`、`TestGroupBuyConcurrentJoinCapacity`、`TestGroupBuyConcurrentDuplicateJoin`、`TestGroupBuyExpireAndRefundReuse`、`TestGroupBuyExpireRaceWithJoin` PASS | 已支付订单不自动退款（Deferred）；无活动级库存（设计） |
| **B1-04-07** 砍价活动/发起/助力/并发/状态推进 | `bargain_activities`/`instances`/`help_records`（`20261110000012`）；`active_key` + `uk_active_instance`、`uk_instance_helper`；行锁 + 条件 UPDATE；底价保护 | 已合入 | `internal/logic/bargain` 9 例 PASS（含并发助力不破底价） | 无过期扫描（懒过期）；助力额无实例快照；成功不下单（均 Deferred） |
| **B1-04-08** 会员等级/成长值/折扣/支付奖励/退款扣减/分销扩展 | `member_levels`/`user_memberships`/`member_growth_logs`（`20261110000013`）、`orders.member_discount_amount`（`20261111000003`）；成长值幂等键 `growth:order_reward:<id>`、`growth:refund_deduct:<id>` | 已合入 | `internal/logic/member` 全部 PASS；`TestOrderQuoteFreightAndMemberDiscount`、`member_distribution_chain_test` PASS | 订单不存折扣率快照（P2 报表口径） |

## 4. 完整测试矩阵（真实命令与退出码）

环境：MySQL `127.0.0.1:3306`（既有独立容器 `b10402-mysql`，未停止/未删除）、Redis `127.0.0.1:6379`；测试库 `my_shop_b104_test`（本轮创建）、`my_shop_bargain_test`（测试自清理）。

| # | 命令 | 退出码 | 状态 | 说明 / 对验收的影响 |
| --- | --- | --- | --- | --- |
| 1 | `gofmt -l .` | 0 | **PASS** | 无输出 |
| 2 | `go build ./...` | 0 | **PASS** | — |
| 3 | `go vet ./...` | 0 | **PASS** | — |
| 4 | `bash scripts/test-unit.sh` | 0 | **PASS** | 纯内存包 + 生成器单测 |
| 5 | `bash scripts/test-integration.sh`（`DATABASE_DEFAULT_NAME=my_shop_b104_test`、`BARGAIN_TEST_MYSQL_PORT=3306`） | 1 | **FAIL** | 45 个包 `ok`；仅 5 个地区用例失败（见下） |
| 6 | `go test -p 1 ./...`（同上环境） | 1 | **FAIL** | 与 #5 同一 5 个用例，其余全部 PASS |
| 7 | `bash scripts/test-race.sh` | 0 | **PASS** | 需 compose 管理的 MySQL；本轮用独立 compose 项目 `my-shop` + 独立卷在 `127.0.0.1:3307` 提供该实例后执行成功（5 段全绿，含 Outbox 11 例、Consumer 2 例） |
| 8 | `go test -race -p 1 ./internal/logic/{order,wallet,distribution,member,pricing,freight,bargain}/...` | 0 | **PASS** | B1-04 核心模块定向 Race |
| 9 | `go test -race -p 1 ./test/e2e/...` | 0 | **PASS** | 586s，含拼团/分销/订单计价 E2E（本轮修复后重跑） |
| 10 | `bash scripts/test-migration.sh`（`DATABASE_DEFAULT_NAME=my_shop_b104_test`） | 0 | **PASS** | 14 个用例：空库迁移、幂等、并发迁移、锁等待/超时、进程被杀恢复、严格模式等 |
| 11 | `go run . migrate up` / `migrate version`（隔离库） | 0 / 0 | **PASS** | `version: 20261111000003 (dirty=false)`，已是最新 |
| 12 | `go test -run TestB104IntegrationMigrationsUpDownUp ./internal/migrations/` | 0 | **PASS** | B1-04 新增迁移 up → down → up，回滚不影响 `20261111000001` |
| 13 | `bash scripts/contract-check.sh` | 0 | **PASS** | 210 常驻 API + 4 planned、490 Schema、228 未引用组件（白名单）；路由双向一致；Spectral 无 error |
| 14 | `bash scripts/check-registry.sh` | 0 | **PASS** | 无错误码域重叠、无 Migration 版本重复/漂移；仅 RESERVED 域 INFO |

### 4.1 失败用例明细（全部为地区字典夹具缺失）

| 包 | 用例 | 失败断言 | 根因分类 |
| --- | --- | --- | --- |
| `internal/controller/region` | `TestRegionListByParent` | `expected 34 provinces, got 0` | 环境夹具（`regions` 空） |
| `internal/controller/region` | `TestRegionTree` | `expected total=3938, got 0` | 环境夹具 |
| `internal/controller/region` | `TestRegionDetail` | `/regions/3` 返回 400/1001 | 环境夹具 |
| `internal/controller/address` | `TestAddressRegionCreateAndUpdate` | `create address with region: 400/1001` | 环境夹具（`ResolveDistrict` 查不到 id=3） |
| `internal/controller/address` | `TestAddressDefaultUniqueAcrossUsers` | `create address with region: 400/1001` | 环境夹具（同上） |

日志证据：`地区树构建完成: nodes=0 roots=0`、`SELECT COUNT(*) FROM regions` 在所有库均为 0。**不是业务代码缺陷**，未通过跳过/删除测试规避。

### 4.2 修复前的基线失败（对照）

首轮在隔离库执行 `bash scripts/test-integration.sh`（仅注入 `DATABASE_DEFAULT_NAME`，未覆盖砍价实例）时退出码为 1，共 15 个失败：上述 5 个地区用例 + `internal/logic/bargain` 9 例（默认连接 `127.0.0.1:13307` 的独立实例不存在，`connection refused`）+ `internal/logic/freight` 1 例（硬编码期望迁移版本 `20261111000002`，实际已推进到 `20261111000003`）。

- 砍价 9 例：属**测试实例约定**（包注释明确要求独立 MySQL，可用 `BARGAIN_TEST_MYSQL_*` 覆盖），按文档覆盖到本机 3306 后全部 PASS，未改测试默认值。
- freight 1 例：属**真实测试缺陷**（版本断言未随 B1-04-08 新增迁移更新），本轮已最小修复。

## 5. 地区字典数据

### 5.1 数据源调查结论

| 调查项 | 结果 | 证据 |
| --- | --- | --- |
| B1-03-04 历史迁移脚本 | 存在 | `internal/logic/legacymigrate/domain_user.go:32`（`SELECT city_id,parent_id,level,name FROM system_city`） |
| 仓库是否保留地区数据文件 | **否** | `git rev-list --all --objects` 中无 `tmp/*.sql`、无地区种子文件；`git log --all --diff-filter=A -- "*regions*"` 仅命中迁移 DDL；无 Git LFS |
| 可复用的历史导入数据 | **无** | `.gitignore:39-40` 忽略 `/shopping.sql`、`/storage/private/legacy/`；`scripts/import-legacy.sh` 要求的 `storage/private/legacy/shopping.sql` 不存在；全盘无 >100KB 的 `.sql` dump |
| 可访问的原始 `system_city` 数据库 | **无** | 实例内库：`my_shop`、`my_shop_b104_integration`、`my_shop_bargain_test`、`my_shop_boot_test`、`my_shop_mig_check`；无 `my_shop_staging`、无 `system_city` 表 |
| 现有测试环境是否具备地区数据初始化方式 | **否** | 测试仅执行 `migrations.Up` + `bootstrap`（`internal/controller/region/region_test.go:117-132`），迁移注释明确「只建结构，不导入任何地区数据」（`20261110000003_regions.up.sql:1-2`） |

补充：Docker 仅有 2 个卷（MySQL 469MB、Redis 15KB），不存在遗留 staging 卷；`git stash` 为空。

### 5.2 实际导入结果

| 项 | 值 |
| --- | --- |
| 数据来源 | 不可获得（应为 B1-03-04 `system_city`，3938 条） |
| 实际导入数量 | **0**（未导入） |
| 层级数量 | 省 0 / 市 0 / 区县 0（期望 34 / 369 / 3535） |
| 完整性验证 | **NOT_RUN**（无数据可验） |
| 测试库初始化方式 | `my_shop_b104_test` 由本轮创建，`migrations.Up` 建 63 张表；`regions` 为空，无地区数据初始化脚本 |
| 结论 | **BLOCKED** |

按任务要求：**未伪造 3938 条地区数据、未编造省市区名称与父子关系、未删除或跳过相关测试、未把失败标记为 PASS。**

### 5.3 恢复路径（供 Owner 决策）

任一方式即可解除阻塞，且无需改动业务代码：

1. 提供归档文件 `storage/private/legacy/shopping.sql`（或 `/shopping.sql`）→ `bash scripts/import-legacy.sh` 导入 `my_shop_staging` → `legacy-migrate import` 写入 `regions`；或
2. 直接提供可访问的 `system_city` 源库（只读），指向同一 MySQL 实例；或
3. 提供一份经 Owner 确认来源的 `regions` 数据 dump（3938 行，保持 `id=city_id`、`level=源level+1`、`parent_id` 原样），导入隔离测试库。

恢复后需执行：`DATABASE_DEFAULT_NAME=my_shop_b104_test go test -p 1 ./internal/controller/region/ ./internal/controller/address/`，并核对 34/369/3535 与父子关系。

## 6. 核心链路验收

### 6.1 商品 → SKU → 购物车 → 订单 → 库存

| 核实点 | 结论 | 证据 |
| --- | --- | --- |
| `sku_code` 定位 SKU（兼容 `sku_id`） | 成立 | `internal/logic/order/order.go:433`（`buildLines`：`sku_code` 优先，同时提交时须指向同一 SKU，否则 9003） |
| 服务端读取权威价格 | 成立 | `order.go:707-763` `reloadLinesInTx`，`skus` 行 `LockUpdate()`（`:716-718`），价格覆盖 `:754` |
| 事务内复核 SKU 状态与价格 | 成立 | `order.go:577` 开启事务 → `:582` 事务内重算 `computeAmounts`；客户端金额仅校验不参与计算（`:534-543`） |
| 订单商品快照 | 成立 | `order.go:619-635`（sku_id/sku_code/product_id/名称/规格/主图/price/original_price/quantity） |
| 库存扣减与订单创建原子 | 成立 | 同事务 `order.go:637-641` → `internal/logic/inventory/inventory.go:143-152` 条件 `quantity >= ?`，0 行 → 6001 |
| 重复请求幂等 | 成立 | `orders.uk_user_idempotency`（`20261001000007_orders.up.sql:38-39`）+ `request_hash`（`order.go:1542-1585`）；同键同指纹返回原订单，异指纹 9006/409（`:785-801`） |
| 失败回滚 | 成立 | 任一步 `return err` → 整个事务回滚（`order.go:671-673`）；取消/退款恢复库存 `order.go:1336-1378` 条件更新保证只恢复一次 |

### 6.2 统一计价

- 唯一入口 `pricing.Quote`（`internal/logic/pricing/pricing.go:60-100`），六槽位顺序固定：`OriginalAmount → CouponDiscount → MemberDiscount → PointsDiscount + ShippingFee = PayableAmount`。
- 不变量与保护：输入非负（21010）、逐项扣减不得超剩余（21011）、加运费前 `MaxInt64` 溢出检查、应付上限 `MaxTotalAmount = MaxUint32`（与 `orders` 的 `INT UNSIGNED` 对齐）（21012）；全 int64 整数分，无 float64。
- 公式唯一性：全仓 `pricing.Quote` 生产调用点仅 2 处（普通下单 `order.go:341-347`、拼团下单 `order.go:1188-1194`）。订单落库 `total_amount=PayableAmount`、`discount_amount=CouponDiscount`、`member_discount_amount`、`shipping_fee`（`order.go:591-595`）。
- 需登记的口径（非缺陷，但影响报表）：
  1. 拼团差价复用 `CouponDiscount` 槽位并落 `discount_amount`，与真实券折扣混用同一列；
  2. 会员折扣金额由订单模块 `resolveMemberDiscount`（`order.go:404-431`）按 `floor(original×(10000-rate_bp)/10000)` 计算后传入槽位，折扣规则不在 pricing 内；
  3. 秒杀订单（`flash_sale_orders`）为独立订单实体，不进入统一计价（无运费/券/积分）；
  4. `PointsDiscount` 生产调用点恒传 0，`orders` 表无对应列（积分抵扣未实现）。

### 6.3 钱包资金闭环

| 核实点 | 结论 | 证据 |
| --- | --- | --- |
| Mock 充值默认关闭 | 成立 | `internal/config/contract.go:274`（默认 `false`）；严格部署模式禁止启用（`internal/config/strict.go:77-89`） |
| 充值幂等 | 成立 | 客户端键 + 确定性单号 `RC+sha256(user_id:key)[:30]`（`internal/logic/wallet/wallet.go:467-472`）+ `recharge_orders.uk_order_no` |
| 余额不允许为负 | 成立 | 列 `INT UNSIGNED`（`20261110000006_wallet.up.sql:20/33-35`）+ 条件 UPDATE `balance >= ?`（`wallet_fund.go:77`） |
| 扣款与订单状态同事务 | 成立 | `order.go:886-942`：订单 10→20、`DebitForOrderInTx`、券核销、成长值、佣金、流水同一事务 |
| 退款入账与退款状态同事务 | 成立 | `internal/logic/refund/refund.go:497-560`：订单 20→70 + 库存恢复 + 退款单→60 + 钱包入账 + 佣金冲回 + 成长值扣减 + 审计 |
| 资金流水完整 | 成立 | `wallet_transactions` 含 `tx_type/direction/amount/balance_before/balance_after/biz_type/biz_id/idempotency_key`，`uk_idempotency` |
| 账户行锁/条件 UPDATE | 成立 | `wallet_fund.go:163-188`（UPSERT + `LockUpdate`）、`:192`（扣减 `balance >= ?` / 入账 `balance <= ?`） |
| 重复支付/退款不重复记账 | 成立 | 幂等键 `order_pay:<order_id>`、`refund:<refund_id>`；支付条件更新 `WHERE status=10`（`order.go:892-903`），退款订单闸门 `WHERE status=20`（`order.go:1113-1116`） |

### 6.4 会员与分销

| 核实点 | 结论 | 证据 |
| --- | --- | --- |
| 支付后成长值增加 | 成立 | `order.go:929` → `member.go:389`，键 `growth:order_reward:<order_id>`，`member_growth_logs.uk_idempotency` |
| 退款后成长值扣减 | 成立 | `refund.go:556` → `member.go:511`，键 `growth:refund_deduct:<refund_id>`；只扣成长值不回退等级 |
| 会员折扣进入订单金额 | 成立 | `member.go:352-379`（`DiscountRateBp`，夹紧 `[1,10000]`）→ `order.go:404-431` → `pricing.Quote`；落库 `member_discount_amount` |
| 佣金只生成一次 | 成立 | `distribution.go:582` + `distribution_commissions.uk_order_id`；比例取 `distribution.commission_rate_bp`（默认 0，`:617`） |
| 退款佣金只冲回一次 | 成立 | `distribution.go:666`，状态 `10/20→30` 条件更新 + `uk_refund_id` |
| 推荐关系不能形成环 | **顺序场景成立，并发存在窗口** | `requireAcyclicInTx`（`distribution.go:199-217`）在事务内写后检测；但并发互绑时双方各自看不到对方未提交插入，可能形成 A→B→A（`distribution.go:136-165`）。无资金影响（一级不遍历链路） |
| 分销数据访问权限 | 成立 | 用户端身份取 Principal（`internal/controller/distribution/distribution.go:108-114`），查询固定 `beneficiary_user_id`/`referrer_user_id`；后台路由 `distribution:referral_list`/`commission_list`/`settle`（`internal/cmd/routes_admin.go:216-231`） |
| 后台人工结算不重复执行 | 成立 | `distribution.go:463-514`：行锁 + `status=10→20` 条件更新，已结算幂等返回，冲回终态报错 |

### 6.5 拼团

| 核实点 | 结论 | 证据 |
| --- | --- | --- |
| 活动状态与可开团条件 | 成立 | `groupbuy.go:1272-1285`：`status=1 AND start_time<=NOW() AND end_time>NOW()` |
| 开团/参团事务边界 | 成立 | `groupbuy.go:287`/`:400`：团、订单、参与记录、库存同一事务 |
| 名额条件更新 | 成立 | `groupbuy.go:411-423`：`UPDATE ... WHERE status=10 AND joined_count<required_size AND expire_at>NOW()`，`RowsAffected==0` → 25006 |
| 重复参团拒绝 | 成立 | `uk_team_user`（`20261110000011_group_buy.up.sql:76-77`）+ 1062 → 25007（`groupbuy.go:455-465`） |
| 满员成团 | 成立 | `groupbuy.go:467-485` 条件更新 `joined_count>=required_size`，并发下仅一次生效 |
| 超时失败与库存恢复 | 成立 | `worker/scanner.go:80-98` 60s 扫描 → `groupbuy.go:913-953` 条件置 40 → 逐笔 `CancelInTx`（仅 `status=10`）→ `IncreaseInTx` 恢复库存 |
| 已支付订单失败处理 | 不自动退款 | `groupbuy.go:833-836` 等注释明确保留 `paid`，走既有退款流程（Deferred） |
| 拼团订单使用统一计价 | 成立 | `order.go:1140-1268`：事务内重读权威价 → `pricing.Quote` → 六槽位落库；拼团价高于现价 → 25004 |
| 独立活动库存 | 不建（设计） | `20261110000011_group_buy.up.sql:5-7`：复用 SKU 库存 |

### 6.6 砍价

| 核实点 | 结论 | 证据 |
| --- | --- | --- |
| 活动有效性 | 成立 | `bargain.go:301-321` 列表过滤；`:354-369` `INSERT ... SELECT` 在 SQL 内校验 `status=1 AND start_time<=NOW() AND end_time>NOW()` |
| 同用户同活动进行中实例唯一 | 成立 | `active_key` 生成列 + `uk_active_instance`（`20261110000012_bargain.up.sql:67/71`），1062 → 23005 |
| 助力资格 | 成立 | `bargain.go:487-495`：不能帮自己（23006）、实例须进行中且未过期、次数未满 |
| 助力金额计算 | 成立（固定值，非随机） | `bargain.go:497-505`：读活动 `help_amount`，`cut = min(help_amount, current - floor)` |
| 并发锁与条件更新 | 成立 | `lockInstanceInTx`（`bargain.go:743-757` `FOR UPDATE`）+ 条件 UPDATE（`:507-518`） |
| 终态保护 | 成立 | 成功/放弃/过期后助力被 Go 侧与 SQL 条件双重拒绝（`:490-495`、`:509-510`） |
| 助力记录与实例价格一致 | 成立 | 同一行锁内读→算→写；落库 `cut_amount/price_before/price_after`（`:520-526`） |
| 过期处理 | 仅懒过期 | `bargain.go:702-725`，读取/放弃路径推进；`internal/worker/scanner.go` 无砍价扫描器 |
| 成功后自动下单 | 未实现 | `bargain.go:535-542` 只推进状态；`order_id` 只读不写；订单侧无砍价入口 |

## 7. 遗留事项

分类：**IMPLEMENTED / DEFERRED / OWNER_DECISION / BLOCKED**。

| # | 事项 | 分类 | 证据 | 说明 / 归属 |
| --- | --- | --- | --- | --- |
| 1 | 生产佣金比例 | **IMPLEMENTED（0bp 默认关闭）** + **OWNER_DECISION** | `internal/config/contract.go:268`（默认 0，>10000 非法） | 是否启用 5%、何时启用需 Owner 决定，未擅自调整 |
| 2 | 佣金真实打款 | **DEFERRED** | `distribution.go:463-514` 仅状态 10→20 + `settled_at` | 资金/对账能力，后续独立任务 |
| 3 | 佣金结算语义 | **IMPLEMENTED（状态流转）** + **OWNER_DECISION** | 同 #2 | 需 Owner 确认「结算≠到账」的运营口径 |
| 4 | 砍价成功后自动下单 | **DEFERRED** + **OWNER_DECISION** | `bargain.go:535-542`；`api/bargain/v1/bargain.go:61` | 成交价/下单时机/订单关联未冻结，未自行设计 |
| 5 | 积分抵扣 | **DEFERRED** + **OWNER_DECISION** | `pricing.QuoteRequest.PointsDiscount` 恒 0；`orders` 无对应列 | 需要换算比例、上限、叠加顺序规则 |
| 6 | 拼团已支付订单失败自动退款 | **DEFERRED** | `groupbuy.go:833-836` | 当前仅取消待支付订单，已支付走既有退款流程 |
| 7 | 拼团独立活动库存 | **DEFERRED（设计不建）** | `20261110000011_group_buy.up.sql:5-7` | 复用 SKU 库存，无活动配额 |
| 8 | 砍价助力金额快照 | **缺失（DEFERRED，真实缺口）** | `bargain.go:497` 实时读活动 `help_amount` | 运营中途改配置会影响在途实例每次砍减额（底价仍受实例快照保护）；建议会员/营销配置加版本或补实例快照 |
| 9 | 砍价过期扫描 | **未实现（懒过期）** | `internal/worker/scanner.go` 仅 order/groupbuy/flashsale | 从不被读取的实例长期停留 `status=10`，`active_key` 持续占位（重新发起会 23005）；报表「进行中」口径偏差 |
| 10 | 真实支付回调 | **DEFERRED / BLOCKED（阶段外）** | 支付入口仅 balance/mock，无渠道回调表 | Mock 支付体系不含真实渠道，未擅自引入 |
| 11 | 分销并发互绑成环 | **必须修复（P1，未修复）** | `distribution.go:136-165`（无 `FOR UPDATE`）+ `:199-217`（普通读） | 建议最小方案：事务内按 `user_id` 升序 `SELECT ... FOR UPDATE` 锁双方用户行，链遍历改锁定读，并补「双向并发绑定」回归用例；需 Owner 决定是否 B1-05 前修复 |
| 12 | 管理员 ship/refund 流水 `operator_id=0` | **建议修复（P2）** | `order.go:1090`、`:1134`（`operatorTypeAdmin, 0`） | 与 `order_status_logs` 注释「记录管理员 id」不符，审计无法定位操作人；属历史遗留，本轮未改（避免扩大范围） |
| 13 | 拼团失败补偿复用 `timeout_cancel` | **建议修复（P2）** | `order.go:1275`（`CancelInTx` 固定 `cancelReasonTimeout`） | 订单详情会显示「超时取消」，与「成团失败」语义不符；仅展示层影响 |
| 14 | 订单无会员折扣率/拼团优惠独立快照 | **已知留白（P2）** | `20261111000003` 只存金额；拼团差价复用 `discount_amount` | 事后无法从订单反查「当时几折」「拼团优惠多少」，影响报表拆分 |
| 15 | 订单幂等键无长度校验 | **建议修复（P3）** | `api/order/v1/order.go:90` 仅 `required` | 超 64 字符会由 DB 报错变 500 而非 400 |

## 8. 本轮最小修复

| 文件 | 修改 | 原因 | 回归验证 |
| --- | --- | --- | --- |
| `internal/logic/freight/integration_test.go` | 版本断言改为 `migrations.LatestVersion()`，`dirty` 查询参数化 | B1-04-08 新增 `20261111000003` 后该用例仍断言 `20261111000002`，导致全量测试必然失败（真实测试缺陷，非环境问题） | `TestIntegrationSchemaVersion` PASS；`internal/logic/freight` 包 PASS |
| `internal/migrations/migrations.go` | 新增只读导出 `LatestVersion()` | 供跨包测试断言「已迁移到最新」，避免再次硬编码漂移；不连接数据库、无副作用 | `go build`/`go vet`/迁移测试 PASS |
| `internal/logic/pricing/pricing.go` | 包注释、`QuoteRequest` 注释与槽位说明同步事实 | 原文写「会员折扣本批次恒 0 / 预留给 B1-04-08」，与已实现状态不符 | `internal/logic/pricing` PASS；契约检查无漂移 |
| `internal/logic/distribution/distribution.go` | `BindReferral` 注释改为与实现一致，并登记并发成环残留 | 原文声称「按 user_id 升序锁定双方关系行」，实现明确不使用 `FOR UPDATE`，会误导验收 | `internal/logic/distribution` PASS |
| `docs/design/pricing.md` | 修正会员折扣「当前恒 0」的过期表述 | 文档与实现不一致 | 无代码影响 |

未修改：任何历史 Migration、八个子任务的业务实现、已冻结的业务规则、路由/OpenAPI 生成物；未新增业务功能。

## 9. 最终验收意见

1. **B1-04 是否可以关闭？**
   **否（PARTIAL）。** 八个子任务已全部集成，功能链路、契约、Race、Migration、Registry 均可复核；但地区字典数据源缺失导致 5 个地区/地址用例无法验证（外部 BLOCKED），且分销并发互绑成环为未修复的 P1 数据完整性风险。补齐地区数据并处置 #11 后可复验关闭。

2. **是否允许进入 B1-05？**
   **有条件允许。** 除地区字典数据与已登记 Deferred 项外，不存在阻断 B1-05 的功能性缺陷；建议在 B1-05 开工前完成地区数据恢复（或由 Owner 书面接受该缺口），并就 #11 给出修复或风险接受结论。

3. **是否还有 P0 阻塞？**
   有且仅有 1 项：地区字典数据源不可获得（`system_city` / `shopping.sql` / staging 库均不存在），5 个用例无法执行。未伪造数据。

4. **是否还有 P1 未完成事项？**
   有 1 项：分销推荐关系并发互绑的成环窗口（一级分销无资金影响，但数据完整性缺陷成立）。其余为 P2/P3（`ship`/`refund` 流水操作者 id、拼团失败取消原因语义、订单优惠快照拆分、幂等键长度校验）。

5. **需要 Owner 决策什么？**
   - 提供地区字典数据源（3 条可选路径见 §5.3），或书面接受 B1-04 以「5 个地区用例未验证」状态关闭；
   - 分销并发成环：B1-05 前修复，还是接受残留风险并登记到后续任务；
   - 生产佣金比例（当前 0bp 关闭）何时启用；
   - 佣金「结算」是否对接真实打款/对账；
   - 砍价成交规则（成交价、下单时机、与统一计价的对接方式）；
   - 积分抵扣规则（换算比例、上限、叠加顺序）；
   - 拼团已支付订单是否需要系统自动发起退款。

## 10. 环境与清理说明

- 测试只写隔离库：`my_shop_b104_test`（本轮创建）、`my_shop_bargain_test`（既有，用例自清理）；`my_shop` 仅只读查询，未执行任何清库/重建。
- 既有容器 `b10402-mysql`、`b10402-redis`（其他任务在用）**未停止、未删除、未删除其数据卷**。
- 为满足 `scripts/test-race.sh` 对「compose 管理的 MySQL」的硬性要求，临时以 compose 项目 `my-shop`、独立数据卷在 `127.0.0.1:3307` 启动了一个独立 MySQL 实例（容器 `my-shop-mysql`，仅用于本轮 Race 门禁），未复用/未修改既有容器数据；验证结束后已 `docker compose -p my-shop down -v` 清理（容器/网络/卷均已删除，既有 `b10402-*` 容器与其数据卷未受影响）。
- 复现 Race 门禁的命令：
  `COMPOSE_PROJECT_NAME=my-shop MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=<32B> MYSQL_PORT=3307 REDIS_PORT=6380 docker compose up -d mysql`，待 healthy 后执行
  `COMPOSE_PROJECT_NAME=my-shop DATABASE_DEFAULT_PORT=3307 DATABASE_DEFAULT_PASS=root bash scripts/test-race.sh`。
- 未对历史业务数据库执行任何 `down` 迁移；`migration down` 仅在隔离库 `my_shop_b104_test` 上通过测试用例验证。
