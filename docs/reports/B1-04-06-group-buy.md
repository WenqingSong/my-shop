# B1-04-06 拼团业务开发报告

## 1. 最终结论

**PASS**（本任务范围内的功能与验收项全部通过；环境导致的非本任务用例失败与已知限制见 §8）。

本任务只实现 Go 后端拼团业务，未进行前后端联调，未依赖、未合并、未 cherry-pick
`feat/b1-04-02-sku-spec-product-detail` 的任何代码。

## 2. Git

| 项 | 值 |
| --- | --- |
| 基线 SHA | `637ae2d35d98892094b82184bc8045e6fda3b42c`（`origin/feat/p2-backend`，本地新建跟踪分支 `feat/p2-backend`） |
| 开发分支 | `feat/b1-04-06-group-buy`（从本地 `feat/p2-backend` 切出） |
| 代码与文档提交 | `ee3e482cd2d5d326a5a952b52013a9f7701deec8` |
| 交付报告提交 | 本文件单独提交（HEAD），报告对应代码 SHA 为上行的 `ee3e482` |
| 工作区状态 | 提交前干净（无未跟踪/未提交修改）；未推送远端、未合并、未部署、未修改 `main` |

创建分支前确认：工作区干净、当前分支为 `main`、本地 `feat/p2-backend` 不存在而
`origin/feat/p2-backend` 存在（`git fetch --prune` 后 SHA 未变）。按要求先从远端跟踪分支
建立本地 `feat/p2-backend`，再切出任务分支。

## 3. 实现功能

### 3.1 活动管理（后台）

- 创建活动：名称（≤64）、商品、SKU、拼团价、成团人数、团有效期、活动起止时间。
- 修改活动：名称/价格/人数/有效期/时间窗可改；商品与 SKU 不可变（避免已开团引用漂移）。
- 启用/停用：`POST /admin/group-buys/:id/status`；停用不影响已开的团。
- 活动列表（分页、按状态/商品/SKU 过滤）与详情；返回 SKU/商品实时摘要与进行中团数量。
- 服务端校验：`group_size ≥ 2`、`team_ttl_minutes ∈ [1,10080]`、`group_price > 0` 且
  `≤ skus.price`、`end_time > start_time`、SKU 属于该商品、SKU enabled、商品 on_shelf。

### 3.2 用户参与（前台）

- 活动列表（仅启用且在 `[start_time, end_time)` 内）、活动详情（含已结束/停用，便于追溯）。
- 某活动进行中团列表（`status=10 AND expire_at>NOW()`）。
- 发起拼团：创建团 + 同事务创建拼团订单 + 团长参与记录。
- 参与拼团：容量条件更新 + 同事务创建订单与参与记录；满员自动成团。
- 团详情：团、活动/SKU/商品摘要、成员用户名、当前用户参与记录与订单 id（不暴露他人订单）。
- 我的拼团：按团 id 倒序分页，可按团状态过滤。

### 3.3 状态管理

- 待成团（10）→ 已成团（20，满员）/ 已失败（30，运营关闭）/ 已过期（40，超时）。
- 终态全部由 `WHERE status=10` 条件更新推进，只发生一次；`closed_at` 只写一次。
- 已成团/已失败/已过期的团不可再加入；重复参与由 `uk_team_user` 兜底。

### 3.4 订单集成

- `group_buy_participants.order_id` 关联 `orders.id`（`uk_order_id` 唯一）。
- 复用 `IOrder.CreateGroupBuyInTx`：订单 `original_amount = skus.price`、
  `discount_amount = skus.price - group_price`、`total_amount = group_price`、
  `order_items.price = group_price`；不新增订单列、不改订单状态机。
- 库存仍由 `IInventory.DeductInTx` 扣减；失败补偿由 `IInventory.IncreaseInTx` 恢复。
- 团容量更新、订单、订单项、库存、参与记录在同一事务提交。

### 3.5 超时与失败补偿

- 后台扫描器 `worker.StartGroupBuyExpireScanner`（`group_buy.expire_scan_interval`，默认 60s），
  在 `serve` 内独立运行，HTTP drain 结束后停止。
- 过期/运营关闭时：同事务取消该团**待支付**订单（`status=10→60` + 恢复库存 + 释放券）。
- 已支付订单保持 `paid`，不自动退款、不伪造退款；用户走现有 `/orders/:id/refund`
  由退款模块审核/执行，成功后订单 `20→70` 并恢复库存。

## 4. 关键业务机制

| 机制 | 实现 |
| --- | --- |
| 成团 | 最后一次参团同事务条件更新 `status=20, closed_at=NOW() WHERE status=10 AND joined_count>=required_size` |
| 容量 | `UPDATE joined_count=joined_count+1 WHERE status=10 AND joined_count<required_size AND expire_at>NOW()` + `RowsAffected` |
| 并发 | InnoDB 行锁 + 条件更新 + `uk_team_user`/`uk_order_id`；无 Redis 锁、无 MQ |
| 幂等 | 常规重复参团先读参与记录直接返回团详情；并发重复由唯一键回滚（21007）；订单幂等键冲突返回 9006 |
| 订单关联 | 同事务写 `orders`/`order_items` + `participants.order_id`；禁止「有订单无参与」 |
| 超时 | 条件更新 `status=40 WHERE status=10 AND expire_at<=NOW()`；与参团的 `expire_at>NOW()` 闸门互斥 |
| 失败补偿 | 团终态条件更新后逐单 `CancelInTx`（订单 `status=10` 条件更新），只补偿一次 |
| 开团快照 | 活动行 `FOR UPDATE` 读，价格/人数/有效期写入团快照，后续改活动不影响已开团 |
| 时区 | 活动时间写入转 `dashboard.timezone` 字面量，避免 gdb UTC 格式化与连接层 `time_zone` 漂移 |

## 5. 数据库变更

**未新增、未修改任何 Migration。**

直接复用 B1-03-03 的 `20261110000011_group_buy`：
`group_buy_activities`、`group_buy_teams`、`group_buy_participants`。
现有字段、索引、唯一约束足以实现已批准业务，未扩大数据库变更范围，未触碰
`flash_sale_*`、`inventories`、`orders` 既有表结构。

## 6. 文档变更

| 文件 | 变更 |
| --- | --- |
| `docs/design/marketing.md` | 新增拼团实现范围、订单价格快照、事务边界、超时/失败补偿、并发控制、API/权限/错误码/配置、已知留白；补充不变量实现映射（INV-GB01..GB09） |
| `docs/design/order.md` | 新增拼团订单集成说明（复用 `CreateGroupBuyInTx`/`CancelInTx`，不改订单状态机） |
| `docs/design/inventory.md` | 新增拼团复用扣减/恢复库存说明 |
| `docs/design/refund.md` | 新增拼团订单复用退款、已支付订单不自动退款、退款不回补名额 |
| `docs/design/migration.md` | 标注 B1-04-06 复用 `20261110000011`，未新增 DDL |
| `docs/design/error-codes.md` | 域分配表新增 21（21000-21999，group-buy） |
| `.agent/registry/error-codes.md` | 登记 21000-21999 为 group-buy RESERVED（需按流程以独立 Registry 变更进入 develop） |
| `docs/dev/marketing.md` | **新增**模块开发知识：代码地图、调用链、数据结构、不变量映射、事务/锁/幂等、错误码与契约、测试地图、排障、维护注意事项 |
| `docs/development/configuration.md` | 新增 `group_buy.expire_scan_interval` 映射行 |
| `docs/reports/B1-04-06-group-buy.md` | 本报告 |

OpenAPI 由生成器重新生成（未手工修改）：`openapi/surgecart.openapi.yaml`、
`openapi/paths/groupbuy.yaml`、`openapi/components/{schemas,error-codes}.yaml`、
`docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`。

## 7. 测试结果

环境：Linux，真实 MySQL 8.0（docker `gb-mysql`，127.0.0.1:3306）+ Redis 7（`gb-redis`），
Go 1.26.0。

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `gofmt -l api internal test tools` | 0 | PASS（0 个未格式化文件） |
| `go build ./...` | 0 | PASS |
| `go vet ./...` | 0 | PASS |
| `bash scripts/test-unit.sh` | 0 | PASS（含 170 条路由锁定、契约生成器、配置文档一致性） |
| `bash scripts/contract-check.sh` | 0 | PASS（170 条常驻路由与契约双向一致；Spectral 无 error；生成器无漂移；NOT RUN 0 项） |
| `DATABASE_DEFAULT_PASS=root go test -count=1 -run 'TestGroupBuy' ./test/e2e/ -v` | 0 | PASS（7/7） |
| `DATABASE_DEFAULT_PASS=root go test -race -count=1 -run 'TestGroupBuyConcurrent' ./test/e2e/` | 0 | PASS（无数据竞争） |
| `DATABASE_DEFAULT_PASS=root go test -count=1 -run 'TestOrder\|TestRefund\|TestGroupBuy' ./test/e2e/` | 0 | PASS（订单/退款/拼团相邻链路无回归） |
| `DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go test -p 1 ./internal/...` | 1 | **部分失败（环境）**：`controller/address` 5 例、`controller/region` 3 例因隔离库 `regions` 表为空失败（测试期望 34 个省级行政区，实际 0）；其余 29 个包 `ok`，含 `internal/cmd`、`internal/config`、`internal/worker`、`internal/migrations` |

补充说明：

- `internal/...` 的 address/region 失败与本次改动无关：本任务未修改 address/region/profile
  代码，失败原因是测试所需的地区字典数据未导入（仓库只有 `regions` 建表 migration，不导数据）。
  未通过 `Skip`/放宽断言掩盖，如实记录为环境失败。
- 首次在默认库运行 `./internal/...` 时，address/region/profile 用例因「必须显式指定隔离测试库」
  保护而拒绝执行；改用 `my_shop_b104_test` 后 profile 全部通过，address/region 仅剩数据缺失失败。
- 并发测试有效性对照实验（临时修改，未提交）：把参团容量闸门改为「先读 `joined_count` 再无条件 +1」后，
  `TestGroupBuyConcurrentJoinCapacity` 实测 `success=10`（超员）并失败；恢复条件更新后 PASS。
  证明该测试能够区分正确实现与「先查后写」的错误实现，而非普通串行测试。

## 8. 关键业务不变量与测试对应关系

| 不变量 | 测试 | 结果 |
| --- | --- | --- |
| INV-GB01 价格/人数快照 | `TestGroupBuyCreateTeamOrdering`（团快照 3000/2；改活动不影响已开团） | PASS |
| INV-GB02 容量不超卖 | `TestGroupBuyConcurrentJoinCapacity`（10 抢 2，`joined=3/required=3`） | PASS |
| INV-GB03 一人一团一次 | `TestGroupBuyConcurrentDuplicateJoin`（8 并发 → 1 参与/1 订单） | PASS |
| INV-GB04 一单一团一次 | 参与插入失败即事务回滚（`uk_order_id`） | PASS（结构约束 + 事务原子性） |
| INV-GB05 团长归属 | `TestGroupBuyCreateTeamOrdering`（团长参与记录 = 订单） | PASS |
| INV-GB06 状态机只进一次 | `TestGroupBuyJoinSuccessAndIdempotency`、`TestGroupBuyExpireRaceWithJoin` | PASS |
| INV-GB07 停用不影响已开团 | `TestGroupBuyActivityManagement`（停用禁止新开团；已开团不受影响见 INV-GB07 实现） | PASS |
| INV-GB08 订单/参与一致 | `TestGroupBuyCreateTeamOrdering`（`order_id` 对应、订单金额/项价正确） | PASS |
| INV-GB09 补偿只一次 | `TestGroupBuyExpireAndRefundReuse`（未支付取消一次、退款恢复一次） | PASS |
| 失败补偿：已支付不伪造退款 | `TestGroupBuyExpireAndRefundReuse`（团过期后订单仍 20） | PASS |
| 退款补偿复用 | `TestGroupBuyExpireAndRefundReuse`（申请→审核→执行成功→订单 70、库存 100） | PASS |
| 活动参数校验 | `TestGroupBuyActivityManagement`（人数/时间窗/价格/商品/SKU/禁用 SKU） | PASS |

## 9. 并发测试结果

| 场景 | 结果 |
| --- | --- |
| 10 个真实用户并发抢 2 个剩余名额 | 成功 2、拒绝 8（409/21006 或 21007）；`joined_count=3`、参与记录 3、订单 3、库存 97 |
| 同一用户 8 次并发重复参团（不同幂等键） | 参与记录 1、订单 1、`joined_count=2`、库存 98（只扣一次） |
| 已过期团 6 个并发参团 + 过期推进同时执行 | 全部 409/21006；团 `expired`、`joined_count=1`、库存 100（未成功参团不计入、未支付订单取消恢复） |
| Race 检测（上述并发用例） | PASS，无数据竞争 |
| 错误实现对照 | 去掉容量条件（先查后写）→ `success=10` 超员，测试 FAIL，证明测试有效 |

## 10. 未验证项及已知限制

| 项 | 状态 | 说明 |
| --- | --- | --- |
| `./internal/...` 全量测试 | **NOT_VERIFIED（环境）** | address/region 共 8 例因隔离库 `regions` 无数据失败；其余包 PASS。非本任务改动引入 |
| 真实支付/资金链路 | **NOT_VERIFIED（能力缺失）** | 项目仅有 Mock 支付；退款也是人工 Mock 确认，不涉及真实资金到账/对账 |
| 已支付订单自动退款 | **NOT_IMPLEMENTED** | 依赖退款模块「系统发起 + 自动执行」能力，当前不存在；本任务只保留 `paid` 并复用用户退款 |
| 多实例部署下的扫描器 | **NOT_VERIFIED** | 条件更新保证幂等，但未在真实多副本环境验证 |
| 前端页面/联调 | **NOT_RUN** | 任务明确只做 Go 后端 |
| 真实高并发压测（QPS/延迟） | **NOT_RUN** | 仅做正确性并发测试，不声明容量 |
| 活动库存/配额 | **NOT_IMPLEMENTED** | Schema 与任务均未授权新增活动库存；库存仍由 SKU 库存兜底 |

## 11. 遗留问题及后续任务依赖

1. **砍价（B1-04-07）**：`bargain_*` 表仍未实现业务；本任务未触碰。
2. **退款模块系统发起能力**：若需要拼团失败自动退款，需退款模块新增「系统发起 + 自动执行」
   的内部接口与幂等键；当前不得用改订单状态/伪造退款代替。
3. **错误码域进入 develop**：`21000-21999` 已按 Registry 规则登记为 RESERVED，但按项目流程
   需以「只改 Registry 文件的独立 commit」进入 `develop` 后才算正式预留；本任务未推送远端。
4. **活动库存/配额**：如需限制热门活动规模，需 Owner 批准新的数据模型（当前 Schema 无活动库存列）。
5. **团长退出/成员退出/退款回补名额**：需要新的状态与计数语义，当前未实现。
6. **B1-04-03（订单计价/运费）**：本任务未依赖其未合并代码；若其后续改变订单金额公式，
   需同步复核 `CreateGroupBuyInTx` 的 `original/discount/total` 计算。

## 12. 最终状态

**开发完成**（本任务范围内 PASS）。代码、测试、设计与开发文档已提交到
`feat/b1-04-06-group-buy`；未合并 `feat/p2-backend`，未合并其他 B1-04 分支，未推送远端。
