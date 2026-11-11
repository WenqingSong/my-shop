# B1-05-02 集成测试与 Migration 验证报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-05-02：集成测试与 Migration 验证 |
| 开发分支 | `feat/b1-05-02-integration-migration` |
| Worktree | `/root/projects/my-shop-b1-05-02` |
| 上游基线 | `origin/feat/p2-backend` = `d1fa065a5620f7430596de236a60c0613aaac557` |
| 本报告提交 | 见本分支 `git log -1` |
| 执行日期 | 2026-11-11 ~ 2026-11-12 |
| 执行环境 | Go `1.26.0 linux/amd64`、MySQL `8.0.46`、Redis `7.4.12` |
| 独立测试库 | `my_shop_b104_test`、`my_shop_b104_migration_test`、`my_shop_b104_legacy_test`、`my_shop_bargain_test`、`my_shop_b104_legacy_source` |
| 未触碰的库 | `my_shop`、`my_shop_staging`、`my_shop_b10304_verify`（本实例上均不存在） |
| Push / Merge | 未执行 |
| 最终状态 | **PARTIAL**（可执行项全部 PASS；真实历史数据行数核对因源数据被 Owner 删除而 BLOCKED） |

## 1. 任务背景与验证范围

本任务不新增业务功能，目标是用真实 MySQL/Redis + 真实 HTTP 路由证明三件事：

1. B1-04 新增业务能力与既有电商主链能正确协同（注册 → 浏览 → 加购 → 算价 → 下单 → 钱包支付 → 发货 → 收货 → 评价）；
2. 已交付 Migration 能从空白库完整建立 Schema，可回滚部分能真正回滚并再次重建；
3. Migration 与历史数据迁移具备可重复性、数据安全性，且不会误伤历史库。

范围冻结：未修改任何生产 API/Controller/Service/Logic、迁移 SQL、Migration Registry、`openapi/`、`tools/openapi-gen/`、`scripts/test-unit.sh`、`scripts/test-race.sh`、CI 配置。

## 2. Git 基线与开发分支

| 检查 | 结果 |
| --- | --- |
| 仓库根 | `/root/projects/my-shop` |
| 开工时所在分支 | `feat/b1-04-06-group-buy`（工作区干净） |
| 本地 `feat/p2-backend` | `637ae2d`，落后远端 35 个提交、0 个未推送提交 |
| `origin/feat/p2-backend` | `d1fa065a5620f7430596de236a60c0613aaac557` |
| Worktree 创建 | `git worktree add -b feat/b1-05-02-integration-migration ../my-shop-b1-05-02 origin/feat/p2-backend` |
| 新 Worktree HEAD | `d1fa065`，与上游基线一致，工作区干净 |
| 原 `feat/p2-backend` 工作区 | 未修改（仅 `git fetch`，无 reset/stash/覆盖） |

未执行：Push、Merge、删除分支、修改他人分支。

## 3. 测试环境与数据库隔离证明

### 3.1 物理隔离的依赖实例

为避免与任何既有开发数据共享实例，本任务启动**专用容器**（独立端口 + 独立数据卷）：

| 组件 | 容器 | 端口映射 | 凭据来源 |
| --- | --- | --- | --- |
| MySQL | `b10502-mysql`（`mysql:8.0`，数据卷 `b10502_mysql_data`） | `127.0.0.1:13306 → 3306` | `DATABASE_DEFAULT_PASS=b10502pass`（仅本任务测试实例） |
| Redis | `b10502-redis`（`redis:7-alpine`） | `127.0.0.1:16379 → 6379` | 无密码，仅本任务测试实例 |

实例内初始库只有 `information_schema / mysql / performance_schema / sys`——**不存在 `my_shop`**，因此本任务的任何 DROP/DELETE 在物理上不可能落到历史库。

### 3.2 实际连接证明（不是按名称推断）

`migrate up` 由应用自身输出目标库：

```text
[INFO] migration 目标：127.0.0.1:13306/my_shop_b104_migration_test
[INFO] migration 执行完成，dirty=false
```

从宿主机连接同一实例：

```text
$ mysql -h127.0.0.1 -P13306 -uroot -p -N -e "SELECT CONCAT('port=',@@port), CONCAT('hostname=',@@hostname);"
port=3306   hostname=6b4e9e99ac30    # 容器内端口/hostname，宿主机映射为 13306
```

测试库迁移状态（真实 SQL 查询）：

| 数据库 | `schema_migrations` | 基础表数 |
| --- | --- | ---: |
| `my_shop_b104_test` | `20261111000004 dirty=0` | 64 |
| `my_shop_b104_migration_test` | `20261111000004 dirty=0` | 64 |
| `my_shop_b104_legacy_test` | `20261111000004 dirty=0` | 64 |

### 3.3 新增隔离防护（唯一的基础设施修复）

既有 `setupCleanDB`（迁移测试）会 `DROP TABLE`，`setupIsolationServer`（E2E）会 `DELETE users/admins/roles/permissions`；此前只靠调用方自觉设置环境变量。本任务增加 4 行调用 + 2 个守卫文件：

- `internal/migrations/b10502_db_guard_test.go`：以 `SELECT DATABASE()` 的实际结果为准，并与 `database.default.name` 交叉核对；命中 `my_shop` / `my_shop_staging` / `my_shop_b10304_verify` 或无法确认时立即 `Fatal`，不执行任何 DDL。
- `test/e2e/b10502_db_guard_test.go`：先 `boot.ApplyDatabaseConfig` 把统一配置层（环境变量优先）注入 gdb，再读实际库名；避免把 config.yaml 的默认端口误判为实际连接。
- 调用点：`setupCleanDB`、`setupSyntheticMigrationDB`、`setupIsolationServer` 各 1 行。

**防护有效性验证**（在专用实例上临时建空库 `my_shop` 后执行，验证完立即删除）：

```text
--- FAIL: TestB10502UpFromEmptySchemaInventory
    拒绝在 "my_shop"（开发/生产用途库）执行破坏性迁移测试
--- FAIL: TestB10502MainChainE2E
    拒绝在 "my_shop"（开发/生产用途库）执行破坏性 E2E 清理
my_shop 中表数量 = 0
```

即：守卫触发后没有产生任何写入。

## 4. Migration Up / Down / Up 验证结果

### 4.1 实际 CLI 语义（先核实再执行）

- `my-shop migrate` 仅提供 `up | force <version> | version`，**没有 down 子命令**；
- 迁移文件 63 个：43 个 `up` + 20 个 `down`；`20261001000001..20261001000023` 这 23 个历史基线**只有 up 文件**；
- 因此「Down 回退到空库」在现有实现中不受支持，可验证下界为 `20261001000023`。

本任务未修改迁移 SQL，也未为了让测试通过而伪造回滚：Down 通过真实 golang-migrate 实例 `Steps(-20)` 执行仓库中真实的 20 个 down 文件。

### 4.2 结果

| 阶段 | 断言 | 结果 |
| --- | --- | --- |
| 空白库 Up | `version=20261111000004`、`dirty=0`、64 张基础表（63 业务表 + `schema_migrations`） | PASS |
| Schema 完整性 | 13 个关键唯一约束（含 `orders.uk_order_no`、`skus.uk_sku_code`、`wallet_transactions.uk_idempotency`、`legacy_id_map.uk_source_entity_old`、`user_coupons.uk_user_template_claim`）列顺序一致 | PASS |
| 索引 | 6 个关键索引列顺序一致（含 `flash_sale_order_requests.idx_outbox_claim`、`products.idx_freight_template_id`、`addresses.idx_region_id`） | PASS |
| 外键 | 13 组关键外键存在（`order_items→orders`、`wallet_transactions→wallet_accounts`、`distribution_commissions→orders` 等） | PASS |
| 幂等 Up | 第二次 `Up` 返回「没有待执行的 migration」，版本/dirty 不变 | PASS |
| Down 20 步 | `version=20261001000023`、`dirty=0` | PASS |
| Down 生效 | 24 张本批次新增表消失（钱包/积分/订单流水/地区/规格三表/拼团/砍价/会员/分销/运费/推荐锁）；13 个新增列消失 | PASS |
| Down 边界 | 回滚只做 DDL：商品、订单、库存行仍存在，库存数量不变；更早的列（`orders.total_amount`/`status`、`skus.price`）保留 | PASS |
| 二次 Up | `version=20261111000004`、`dirty=0`、全部表/约束/索引/外键恢复 | PASS |
| 历史数据兼容 | 往返后订单 `total_amount=2000`、`member_discount_amount=0`（不重算）；商品与库存保留 | PASS |
| 唯一约束有效 | 重建后重复 `order_no` 插入被拒绝 | PASS |

可复核命令（退出码 0）：

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_migration_test ... \
  go test -count=1 -run 'TestB10502' -v ./internal/migrations/
--- PASS: TestB10502UpFromEmptySchemaInventory (8.22s)
--- PASS: TestB10502DownUpRoundTrip (10.27s)
ok cnb.cool/go-cloud-devops/my-shop/internal/migrations 18.501s
```

既有 B1-04 往返测试同时通过（`internal/migrations` 整包 `ok 355.278s`）。

## 5. 历史数据迁移行数核对

**结论：BLOCKED（不可伪造）**。

事实依据：

1. 仓库内没有历史源数据：`storage/private/legacy/shopping.sql` 不存在（该路径被 `.gitignore` 排除），`find . -name '*.sql'` 也不存在其它源文件；
2. Owner 已正式取消真实数据导入：`.agent/tasks/legacy-data-migration/delivery.md` 明确记载「原始历史 `shopping.sql` 已被 Owner 删除、不再提供、不要求恢复……不再要求历史真实数据导入，改为验证迁移工具的合成数据正确性」；
3. 因此 B1-03-04 报告中的行数（`products=55`、`skus=110`、`orders=51`、`legacy_id_map=411` 等）**无法在本环境复核**，本报告不将其写成已验证事实。

真实 CLI 行为（在 `my_shop_b104_legacy_test` + 空 `my_shop_b104_legacy_source` 上执行）：

```text
$ ./bin/my-shop legacy-migrate dry-run
迁移域 regions 失败: 读取 system_city: Error 1146 (42S02):
Table 'my_shop_b104_legacy_source.system_city' doesn't exist
exit=1

$ ./bin/my-shop legacy-migrate import
迁移域 regions 失败: 读取 system_city: Error 1146 (42S02):
Table 'my_shop_b104_legacy_source.system_city' doesn't exist
exit=1
```

两次失败后目标库行数不变（证明失败是 fail-fast 且无部分写入）：

```text
categories=0 products=0 users=0 legacy_id_map=0 regions=0
```

## 6. 二次导入幂等性结果

| 层次 | 内容 | 状态 |
| --- | --- | --- |
| 引擎级（合成夹具） | `TestEngineImportIdempotentAndIDMap`：同一 staging 合成数据集连续 import 两次，第二次零新增、`legacy_id_map` 不重复 | **PASS** |
| 引擎级（B1-03-04 域） | `TestMigrateOrdersCouponsReviewsFavorites`（含二次执行幂等断言） | **PASS** |
| 引擎级（其它域） | `TestMigrateProductSpecsAndReferencedDeleted`、`TestMigrateUsersAddressesWalletPoints` | **PASS** |
| 真实历史数据 | 需要 `shopping.sql`，源文件不存在 | **BLOCKED** |

命令与结果（退出码 0）：

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_legacy_test \
  go test -count=1 -v -run 'TestEngineImportIdempotentAndIDMap|TestMigrateOrdersCouponsReviewsFavorites' \
  ./internal/logic/legacymigrate/
--- PASS: TestMigrateOrdersCouponsReviewsFavorites (3.35s)
--- PASS: TestEngineImportIdempotentAndIDMap (3.24s)
```

说明：上述幂等验证基于合成夹具，只能证明「引擎在重复导入时不产生重复数据」，**不能**替代真实历史数据的行数与映射覆盖核对。

## 7. E2E 主链验证结果

新增 `test/e2e/b10502_main_chain_test.go`，全部步骤走真实 HTTP（`RegisterFrontendRoutes` + `RegisterAdminRoutes`）与真实 MySQL/Redis。

### 7.1 主链（`TestB10502MainChainE2E`，PASS）

| 步骤 | 关键断言（真实数据逐级传递） |
| --- | --- |
| 注册 | `POST /register` 200；`users` 行存在、`password_hash` 非空、`status=1` |
| 登录 | `POST /login` 200；`token_type=Bearer`；JWT subject 与注册用户 id 一致；`GET /me` 通过 |
| 商品浏览 | `GET /products/:id` 200；`status=on_shelf`；SKU `sku_code/price=2000/original_price=2500/status=enabled` |
| 地址 | `POST /addresses` 200；`region_id` 为区县级且省/市/区文本由字典填充；数据库归属为当前用户 |
| 优惠券 | `POST /coupons/:id/claim` 200，返回券 id |
| 加入购物车 | `POST /cart/items` 200；`GET /cart` 仅 1 条、数量 3、`price_snapshot=2000`、`available=true`；DB 归属为当前用户 |
| 订单算价 | `POST /orders/quote`：`original=6000`、`coupon=300`、`member=600`、`points=0`、`shipping=600`、`payable=5700`，且满足守恒等式 |
| 创建订单 | `total=5700/original=6000/discount=300/member=600/shipping=600`，状态 `pending_payment`；明细快照 `sku_code/spec/price/original_price/quantity/product_name` 正确；库存 10→7；购物车清空；券 `available→locked(20)`；写 1 条 `create` 流水 |
| 钱包充值 | `POST /wallet/recharges`（Mock 开关经测试进程环境变量打开）成功；`GET /wallet` 余额 10000 |
| 余额支付 | `POST /orders/:id/pay {pay_type:balance}`：状态 `paid`、`pay_type=balance`、`paid_at` 非空；余额 10000→4300；券 `locked→used(30)`；`wallet_transactions` 恰好 1 条 `order_pay`（direction=2、amount=5700、before=10000、after=4300） |
| 重复支付 | 再次支付返回 200/`paid`，余额不变、流水仍为 1 条（无重复扣款） |
| 商家发货 | `POST /admin/orders/:id/ship`（超管带 `order:ship`）：状态 `shipped`、`shipped_at` 非空 |
| 越权收货 | 其他用户 `POST /orders/:id/receive` → 404，订单状态保持 30 |
| 用户收货 | 本人收货：状态 `completed`（30→40→50 原子），`received_at/completed_at` 非空；重复收货 → `409/9002` |
| 商品评价 | `POST /reviews` 成功，`user_id/order_item_id/product_id/sku_id` 与订单一致；公开列表 `total=1`、`avg_rating=5`；重复评价 → `409/10003` |
| 状态流水 | 全程恰好 5 条，目标状态序列 `10 → 20 → 30 → 40 → 50` |

算价公式与契约一致（`docs/design/pricing.md`、`docs/design/freight.md`、会员折扣实现）：

```text
原价 = 2000 × 3 = 6000
券   = 300（满 3000 减 300）
会员 = floor(6000 × (10000-9000) / 10000) = 600
积分 = 0（未实现的冻结槽位，不发明比例）
运费 = 500（首件 1 件）+ ceil((3-1)/2) × 100 = 600
应付 = 6000 - 300 - 600 - 0 + 600 = 5700
```

### 7.2 余额不足（`TestB10502WalletBalanceInsufficient`，PASS）

- 新用户余额 0 时 `pay_type=balance` → `409/22002`；
- 订单状态保持 10、无 `order_pay` 流水、余额仍为 0（拒绝无副作用）；
- 充值 2000 后同一订单支付成功，余额归零——证明失败后可恢复，不存在脏状态。

## 8. 数据完整性验证结果

| 对象 | 断言 | 结果 |
| --- | --- | --- |
| 用户 | 注册行、密码哈希、状态、登录身份一致 | PASS |
| 地址 | `region_id` 与省/市/区文本一致，归属为当前用户 | PASS |
| 商品/SKU | 详情返回上架商品与 enabled SKU，价格与原价正确 | PASS |
| 购物车 | 条目归属、数量、价格快照、可购状态正确；下单后清空 | PASS |
| 订单金额 | 六槽位与算价一致且守恒；明细为服务端快照 | PASS |
| 库存 | 下单扣减 10→7；取消/回滚语义由既有测试覆盖（整包 PASS） | PASS |
| 优惠券 | 领取→locked→used 与订单绑定；重复评价/重复收货被唯一性拒绝 | PASS |
| 钱包 | 账户余额与流水 `balance_before/after` 自洽；重复支付不重复入账 | PASS |
| 订单状态流水 | 5 条、序列 10/20/30/40/50，与状态机一致 | PASS |
| Migration Schema | 表/唯一约束/索引/外键完整（§4） | PASS |
| 历史数据 | 行数、`legacy_id_map` 覆盖、孤儿记录、`data_issues` | **BLOCKED**（无源数据） |

## 9. 测试命令及执行结果

环境前缀（所有命令一致）：

```bash
export DATABASE_DEFAULT_HOST=127.0.0.1 DATABASE_DEFAULT_PORT=13306 \
       DATABASE_DEFAULT_USER=root DATABASE_DEFAULT_PASS=b10502pass \
       DATABASE_DEFAULT_NAME=my_shop_b104_test \
       REDIS_DEFAULT_ADDRESS=127.0.0.1:16379 REDIS_DEFAULT_DB=0
```

| # | 命令 | 退出码 | 结果 |
| ---: | --- | ---: | --- |
| 1 | `go build ./...` | 0 | PASS |
| 2 | `go vet ./...` | 0 | PASS |
| 3 | `go test -count=1 ./internal/migrations/...`（库 `my_shop_b104_migration_test`） | 0 | PASS（`355.278s`，含 B1-04 往返测试） |
| 4 | `go test -count=1 ./test/e2e/...`（库 `my_shop_b104_test`） | 0 | PASS（`132.162s`，180 个既有用例 + 2 个新增） |
| 5 | `go test -count=1 ./internal/logic/legacymigrate/...`（库 `my_shop_b104_legacy_test`） | 0 | PASS（`24.460s`） |
| 6 | `./bin/my-shop migrate up` / `migrate version` | 0 | PASS（`20261111000004`，`dirty=false`） |
| 7 | `./bin/my-shop legacy-migrate dry-run` / `import` | 1 | BLOCKED（缺 `system_city` 源表，零写入，见 §5） |
| 8 | `bash scripts/check-registry.sh` | 0 | PASS（仅 RESERVED 域 INFO，无漂移） |
| 9 | `bash scripts/test-integration.sh`（默认环境） | 1 | FAIL：仅 `internal/logic/bargain` 9 例，原因是其测试固定要求 `127.0.0.1:13307` |
| 10 | `bash scripts/test-integration.sh`（补 `BARGAIN_TEST_MYSQL_*` 指向本任务实例） | 0 | PASS（「集成测试全部通过」） |
| 11 | `go test -p 1 -count=1 ./...`（禁用缓存，同上环境） | 0 | PASS：48 个包 `ok`，0 个 `FAIL` |

第 9 项说明：`internal/logic/bargain/integration_test.go` 默认连接 `127.0.0.1:13307/my_shop_bargain_test`，本环境无该实例；按该测试自身支持的环境变量 `BARGAIN_TEST_MYSQL_*` 指向本任务专用实例后全部通过。该失败与 B1-05-02 改动无关，属既有测试的环境假设，未修改其代码。

`scripts/test-integration.sh` 的既有行为：仅导出 `DATABASE_DEFAULT_PASS`（缺省 `root`），随后执行 `go test -p 1 ./...`；脚本本身不传 `-count=1`，重复运行会复用 go test 缓存。因此本报告以第 11 项（显式禁用缓存）作为门禁证据，未修改该脚本（避免与 B1-05-01 的治理范围冲突）。

## 10. 失败项、阻塞项和未执行项

| 项 | 状态 | 说明 |
| --- | --- | --- |
| 真实历史数据行数核对（§5） | **BLOCKED** | `shopping.sql` 已被 Owner 删除；不伪造行数 |
| 真实历史数据二次导入幂等 | **BLOCKED** | 同上；仅完成引擎级合成夹具幂等 |
| Down 回退到空库 | **NOT SUPPORTED（既有设计）** | 23 个历史基线迁移没有 down 文件；已改为验证 20 个可回滚版本并记录该事实 |
| 积分抵扣计价 | **NOT RUN（未实现）** | `points_discount` 恒 0，无冻结比例，不发明规则 |
| 地区字典全量数据（3938 条） | **NOT RUN** | 历史导入 BLOCKED；E2E 使用自建地区夹具 |
| `scripts/test-integration.sh` 默认环境全绿 | **FAIL（既有环境假设）** | 仅 bargain 包硬编码 13307；补环境变量后全绿 |
| 竞态/Push/合并/部署 | **NOT RUN** | 不属于本任务范围 |

本轮未发现生产代码缺陷（第 7 节全部断言通过，无 Finding 需要上报业务实现）。

## 11. 已知风险及后续建议

1. **共享 Redis 的 FlushDB 风险**：`setupIsolationServer` 会 `FlushDB`。本任务通过专用 Redis（16379）规避，但测试代码本身仍不校验 Redis 目标。建议后续增加 Redis 实例/DB 号断言（本次未越权修改）。
2. **集成门禁未禁用缓存**：`scripts/test-integration.sh` 不带 `-count=1`，重复执行可能复用缓存；建议由脚本 Owner（B1-05-01/03 范围）统一加上 `-count=1`。
3. **bargain 测试硬编码 13307**：建议改为「缺失依赖即 `t.Skip` 并打印原因」，避免在无该实例的环境产生 9 个 FAIL 噪音。
4. **历史基线不可回滚**：`20261001000001..000023` 无 down 文件。若未来要求「Down 到空库」，需要补齐 23 个 down 迁移并评审数据丢失影响，不能在测试层伪造。
5. **历史数据验收能力缺失**：`shopping.sql` 已不存在，B1-03-04 的行数/映射结论只能作为历史报告引用；如需再次验证，必须由数据持有方提供源文件并重新导入隔离 staging 库。
6. **测试数据累积**：E2E 在专用库内持续新增用户/订单/地区等夹具（未逐条清理）。当前用「专属测试库整体可丢弃」策略控制，符合任务允许的清理方式。

## 12. 最终自检结论

| 自检项 | 结论 |
| --- | --- |
| 是否在独立分支/Worktree 开发 | 是（`feat/b1-05-02-integration-migration`，HEAD 基于 `d1fa065`） |
| 是否触碰 `feat/p2-backend` / 他人分支 | 否 |
| 是否修改生产代码/迁移 SQL/Registry/OpenAPI/CI | 否 |
| 是否所有破坏性操作都在独立测试库 | 是（专用实例 + 专用库 + 双守卫验证） |
| 是否伪造 PASS / 跳过真实流程 | 否（历史数据明确 BLOCKED；Down 语义按实际实现记录） |
| 是否保留真实命令、退出码与日志 | 是（§9；详细日志见执行记录） |
| 交付文件 | 5 个新增/修改代码文件 + 本报告 |
| 总体状态 | **PARTIAL**：可执行验证全部 PASS；真实历史数据相关验证 BLOCKED（源数据缺失，非本次实现缺陷） |

### 交付文件清单

```text
internal/migrations/b10502_db_guard_test.go            （新增，隔离守卫）
internal/migrations/b10502_migration_roundtrip_test.go （新增，Up/Down/Up + Schema 完整性）
internal/migrations/migrations_test.go                 （2 行：破坏性清理前调用守卫）
test/e2e/b10502_db_guard_test.go                       （新增，隔离守卫）
test/e2e/b10502_main_chain_test.go                     （新增，主链 + 余额不足 E2E）
test/e2e/identity_isolation_test.go                     （2 行：清理前调用守卫）
docs/reports/B1-05-02-INTEGRATION-MIGRATION.md          （本报告）
```
