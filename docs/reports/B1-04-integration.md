# B1-04 八分支统一集成 · 交付报告（最终）

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-INTEGRATION：八个独立开发分支统一集成 |
| 目标分支 | `feat/p2-backend` |
| 集成开始时 HEAD | `637ae2d35d98892094b82184bc8045e6fda3b42c`（上一轮只合入 01/05 的集成结果，0 ahead / 0 behind `origin/feat/p2-backend`） |
| 最终 HEAD | 本报告提交（`git log -1`，见 §12） |
| 执行日期 | 2026-11-11 |
| 执行环境 | Go `1.26.0 linux/amd64`、MySQL `8.0`（容器 `b10402-mysql`，`127.0.0.1:3306`）、Redis `7-alpine` |
| 隔离测试库 | `my_shop_b104_integration`、`my_shop_bargain_test`、`my_shop_boot_test`、`my_shop_mig_check` |
| 未触碰的库 | `my_shop`（生产用途，仅只读查询 `schema_migrations`/表结构） |
| Push | **未执行**（任务禁止） |
| 合并 main / develop | **未执行** |
| 删除分支 / Worktree | **未执行** |

## 1. 结论摘要

| # | 任务 | 结果 |
| --- | --- | --- |
| 01 | 用户资料与地区地址 | **已完整集成**（上一轮合并 `10f05fa`，本轮复核未回退） |
| 02 | SKU 规格与商品详情 | **已完整集成**（`60c0751`） |
| 03 | 订单金额与运费 | **已完整集成并补齐 A6–A9**（`d1400cc` + `7458f85`） |
| 04 | 钱包与 Mock 充值 | **已完整集成，并接入订单支付/退款事务**（`2446b5d` + `7458f85`） |
| 05 | 一级分销与后台管理 | **已完整集成**（上一轮 `51d88ef`），本轮吸收 08 的成环校验与人工结算 |
| 06 | 拼团业务 | **BLOCKED：分支与提交在仓库中不存在**（证据见 §2.2） |
| 07 | 砍价业务 | **已完整集成**（`0a67d5d`） |
| 08 | 会员等级与一级分销 | **已集成会员；分销按唯一实现合并**（`7e7cede` + `7458f85`） |

关键结论：

1. 以 B1-04-05 的分销实现为唯一实现，B1-04-08 的重复分销代码未进入基线，仅吸收「成环校验」与「人工结算」两项兼容能力。
2. Migration 版本 15 的冲突已解决：`20261110000015` 唯一归属 B1-04-01；B1-04-08 的同名版本上移为 `20261111000003`。
3. 订单计价统一到 `pricing.Quote` 六槽位，`/orders/quote`、事务内复核、幂等指纹扩展与状态流水全部落地。
4. 钱包扣款、退款入账、分销计佣/冲回、会员成长值发放/扣减全部纳入订单/退款事务边界。
5. 违反纪律的删除测试、放宽断言、伪造 PASS 均未发生；未实现的能力（拼团、积分、砍价下单）明确列出。

## 2. 基线与分支核验

### 2.1 开工前状态

| 检查 | 结果 |
| --- | --- |
| 仓库 | `/root/projects/my-shop`（唯一克隆） |
| Worktree | 仅 1 个：`/root/projects/my-shop`；核验时检出 `feat/b1-04-02-...`（工作区干净） |
| `feat/p2-backend` | `637ae2d`，与 `origin/feat/p2-backend` 一致 |
| 既有集成 | 已包含 01（`10f05fa`）与 05（`51d88ef`）的合并、OpenAPI 与文档提交，**未回退** |
| `git stash` / `git fsck --lost-found` | 空 / 无输出 |
| 基线构建 | `go build ./...` 成功 |

### 2.2 八个分支核验

| # | 分支 | 报告预期 Commit | 实际引用 / HEAD | 结论 |
| --- | --- | --- | --- | --- |
| 01 | `feat/b104-01-profile-region-address` | `5f233ee` | `origin/feat/b104-01-...` = `5f233ee` | 一致，已合并 |
| 02 | `feat/b1-04-02-sku-spec-product-detail` | `cae0adb` | 本地/远端 = `ac09d69`（其父为 `b97357f` 实现提交） | **报告 SHA 在仓库中不存在**；以分支真实 HEAD 合并 |
| 03 | `feat/b1-04-03-order-pricing-freight` | `9737072` | `origin/...` = `9737072` | 一致 |
| 04 | `feat/b1-04-04-wallet` | `37750fb` | `origin/...` = `37750fb` | 一致 |
| 05 | `feat/b104-05-distribution` | `cae9638` | `origin/...` = `cae9638` | 一致，已合并 |
| 06 | `feat/b1-04-06-group-buy` | `50ae62d` | **无引用、无对象** | **缺失，BLOCKED** |
| 07 | `feat/b1-04-07-bargain` | `0865a9c` | `origin/...` = `0865a9c` | 一致 |
| 08 | `feat/b1-04-08-member-distribution` | `deebff2` | `origin/...` = `deebff2` | 一致 |

06 缺失证据（按任务要求逐项排查后仍无结果）：

- `git cat-file -t 50ae62d` → `fatal: Not a valid object name 50ae62d`；
- `git for-each-ref | grep -iE 'group-buy|b1-04-06'` → 0 条（含 `refs/remotes/*`）；
- `git reflog --all | grep -i group` → 0 条；`git stash list` → 空；
- `git fsck --unreachable --dangling` → 无输出；磁盘仅有本仓库一个 worktree；
- 全仓 `internal/logic` 无 `group_buy`/`groupbuy` 包，只有 B1-03-03 的 `group_buy_*` 表结构。

### 2.3 分支提交范围（是否混入其他任务）

| 分支 | 相对共同基线的提交 | 判定 |
| --- | --- | --- |
| 02 | `b97357f`（实现）、`ac09d69`（报告） | 仅 spec/sku/product/order 定价与迁移，无混入 |
| 03 | `e7c8978`（Registry 预留）、`9737072`（运费+计价） | 仅运费/计价，无混入 |
| 04 | `3954ef6`、`37750fb`（报告） | 仅钱包/配置/OpenAPI，无混入 |
| 07 | `1574e05`、`0865a9c`（报告） | 仅砍价，无混入 |
| 08 | `771fcd3`、`deebff2`（报告） | 会员 + 分销（与 05 重复），无其他任务混入 |

## 3. Merge 记录

全部使用 `git merge --no-ff` 保留独立开发历史，未 Cherry-pick、未删除源分支。

| 顺序 | 源分支（HEAD） | Merge Commit | 结果 |
| --- | --- | --- | --- |
| 1 | `feat/b1-04-02...`（`ac09d69`） | `60c0751` | 干净合并（基底为 `637ae2d`） |
| 2 | `origin/feat/b1-04-03...`（`9737072`） | `d1400cc` | 8 文件冲突：错误码域、路由、seed、Registry/设计文档 |
| 3 | `origin/feat/b1-04-04-wallet`（`37750fb`） | `2446b5d` | 9 文件冲突：错误码、路由、配置契约、生成器 |
| 4 | `origin/feat/b1-04-07-bargain`（`0865a9c`） | `0a67d5d` | 19 文件冲突：错误码、路由、测试基线、OpenAPI 生成物 |
| 5 | `origin/feat/b1-04-08-member-distribution`（`deebff2`） | `7e7cede` | 最大冲突：重复分销实现（add/add）、订单/退款、会员错误码、迁移 15 |

后续集成提交：

| Commit | 内容 |
| --- | --- |
| `7458f85` | 统一计价、订单算价与复核、状态流水、钱包支付、退款事务、分销结算/成环、商品运费模板绑定、新增测试 |
| 本报告提交 | 设计/开发文档同步 + 交付报告 |

## 4. 解决的冲突

### 4.1 文本冲突（按「保留双方有效语义」处理，无整文件覆盖）

| 文件 | 冲突 | 处理 |
| --- | --- | --- |
| `internal/codes/codes.go` | 03/04/07/08 分别声明 20001-20012、21001-21010、21001-21009、20001-20007 | 按域序整体重排（见 §6），代码与映射表同时修改 |
| `internal/boot/seed.go` | 分销/运费/砍价/会员权限条目位置冲突 | 并集保留，去掉未被路由引用的 `distribution:list/view` |
| `internal/cmd/routes_frontend.go` / `routes_admin.go` | 同一 `Register*` 函数内新增路由 | 并集保留：钱包、运费模板、会员、砍价、分销结算、订单算价 |
| `internal/cmd/routes_test.go` / `tools/openapi-gen/gen.go` / `main_test.go` / `scripts/contract-check.sh` | 路由总数互不相同（155/158/162/175） | 以真实路由表为准统一为 **195**（前台 91 / 后台 102 / health 2） |
| `api/distribution/v1`、`internal/{controller,logic,service}/distribution` | 05 与 08 各有一套完整实现（add/add） | **保留 05 版本**，08 版本不进入基线；吸收成环校验与人工结算 |
| `internal/logic/order/order.go` | 02/03/04/05/08 均修改创建/支付 | 以最终语义重写（计价、复核、钱包、成长值、佣金、状态流水） |
| `internal/logic/refund/refund.go` | 05 佣金冲回、08 成长值扣减、04 钱包入账 | 三者在同一事务内串联 |
| `openapi/**`、`docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md` | 各分支各自重新生成，互相删除 `region.yaml` | 不采用任何分支生成物，按最终代码 `go run ./tools/openapi-gen -repo .` 重新生成 |

### 4.2 语义冲突（必须人工决策的技术项）

| # | 问题 | 证据 | 处理 |
| --- | --- | --- | --- |
| 1 | 迁移版本 15 被 01 与 08 同时使用 | 两个不同 SQL 文件同名；`my_shop` 已应用 01 的 15 | 08 上移为 `20261111000003`（未被任何环境应用过） |
| 2 | 03 的 `20261110000016` 在已应用 `20261111000001` 的库上会被跳过 | `my_shop.schema_migrations = 20261111000001` | 上移为 `20261111000002`，保证升序可执行 |
| 3 | 运费/计价错误码与分销域冲突 | 03 使用 20001-20012，05 已占用 20001-20003 | 运费迁移到域序 21（21001-21012） |
| 4 | 钱包/砍价错误码同为 21001-… | 04 与 07 各自占用域 21 | 钱包 → 22，砍价 → 23 |
| 5 | 会员错误码 20001-20005 与分销冲突 | 08 报告与代码 | 会员 → 24（24001-24005） |
| 6 | 两套分销 API/路由/表语义 | 05 与 08 的 `api/distribution/v1`、路由、service 完全不同 | 只保留 05 一套；08 的 `/referrals/bind`、`/my/referral`、`/admin/commissions` 等路由不注册 |
| 7 | 08 的佣金率硬编码 500bp | `commissionRateBp = 500` | 采用 05 的可配置 `distribution.commission_rate_bp`，**生产默认 0**；500bp 仅出现在测试夹具 |
| 8 | 生成器未登记 `freight`/`member`/`pricing` 域 | `go run ./tools/openapi-gen` 直接失败 | 在 `model.go`/`gen.go`/`parse_codes.go` 登记业务域、实体名与错误码前缀 |
| 9 | 迁移测试/`boot` 测试的 DROP 清单未含新表 | 迁移重跑时 `freight_templates` 已存在 → dirty | 把 `freight_templates`/`freight_template_rules` 补入两处清单 |
| 10 | 并发绑定推荐人时事务死锁 / RR 快照读不到已提交行 | `Error 1213`、`Error 1062` 后回读为空 | 去掉 `FOR UPDATE`，改由 `uk_user_id` 仲裁；1062 时回滚并在新快照判定幂等/改绑 |
| 11 | `internal/logic/logic.go` 未导入 `freight` | 订单创建 500（`implement not found for IFreight`） | 补入聚合注册包 |

## 5. Migration 最终编号

### 5.1 最终清单（B1-04 相关）

| Migration | 来源 | 修改内容 | 冲突 | 处理 |
| --- | --- | --- | --- | --- |
| `20261110000015_profile_region_address` | 01 | `users.birthday`、`addresses.region_id` + 索引 | 与 08 同名 | 保留（已应用，不改） |
| `20261111000001_sku_code_pricing` | 02 | `skus.sku_code/original_price`、`order_items` 快照列 | 无 | 保留（`my_shop` 已应用） |
| `20261111000002_freight` | 03 | `freight_templates`、`freight_template_rules`、`products.freight_template_id` | 原拟 `20261110000016` 会被跳过 | 上移版本号 |
| `20261111000003_order_member_discount` | 08 | `orders.member_discount_amount` | 原为 `20261110000015`（已被占用） | 上移版本号 |
| 无新增 | 04 / 05 / 07 | 复用 B1-03 的钱包/分销/砍价表 | - | 不新增 |

### 5.2 只读核查结论

| 数据库 | `schema_migrations` | 说明 |
| --- | --- | --- |
| `my_shop`（生产用途） | `20261111000001`，`dirty=0` | 已应用 01 的 15 与 02 的 `20261111000001`；未应用 08 的 15 |
| 其他库 | 本轮开工时容器内只有 `my_shop`（上一轮的 `b10401_*`/`b10304_*` 等已不存在） | 未发现「同一版本号对应两套 Schema」 |

因此：`20261110000015 = profile_region_address` 唯一；08 的同名迁移从未进入任何环境，可安全上移。
对 `my_shop` 只做了 `SELECT`，未执行 Up/Down/DROP。

### 5.3 Up/Down 实测（隔离库）

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| 空库完整迁移 | `DATABASE_DEFAULT_NAME=my_shop_b104_integration go run . migrate up` | exit 0，63 张表，`dirty=false` |
| 版本查询 | `go run . migrate version` | `20261111000003 (dirty=false)` |
| Up → Down → Up + 历史数据 | `go test -run TestB104IntegrationMigrationsUpDownUp ./internal/migrations/` | exit 0：回滚到 `20261111000001` 后运费表/会员折扣列消失、SKU 列保留；再次 Up 完整恢复；迁移前插入的商品/订单保留，历史订单 `member_discount_amount=0` |

新增测试文件：`internal/migrations/b104_integration_migration_test.go`。

## 6. 错误码分配结果

| 域序 | 区间 | 归属 | 最终编号 | 原编号（集成前） |
| --- | --- | --- | --- | --- |
| 20 | 20000-20999 | distribution-v1 | 20001-20003（既有一级分销）+ 20004 成环 + 20005 佣金不存在 + 20006 佣金状态非法 | 05: 20001-20003；08: 21001-21007（废弃） |
| 21 | 21000-21999 | freight-pricing | 21001-21009 模板/规则，21010-21012 计价 | 03: 20001-20012 |
| 22 | 22000-22999 | wallet | 22001-22010 | 04: 21001-21010 |
| 23 | 23000-23999 | bargain | 23001-23009 | 07: 21001-21009 |
| 24 | 24000-24999 | member | 24001-24005 | 08: 20001-20005 |

同步范围：`internal/codes/codes.go`、各模块逻辑与测试断言、`openapi/components/error-codes.yaml`（生成）、
`docs/design/error-codes.md`、`.agent/registry/error-codes.md`。下一空闲域序为 25。
仅 IAM 的 `2016 CodePhoneExists` 与既有一级分销 20001-20003 在集成前已落地，本轮未 renumber 已发布码。

## 7. API 与权限变化

| 项 | 变化 |
| --- | --- |
| 常驻 API 总数 | 155（B1-04 前）→ **195**（前台 91 / 后台 102 / health 2，以路由表实测） |
| 订单 | 新增 `POST /orders/quote`（算价预览）；`POST /orders/:id/pay` 新增 `pay_type`（mock/balance） |
| 钱包 | `GET /wallet`、`GET /wallet/transactions`、`POST /wallet/recharges` |
| 运费 | 后台模板/规则 7 条：`/admin/freight-templates`（增删改查、状态、规则整体替换） |
| 会员 | 前台 `GET /members/levels`、`GET /my/membership`、`GET /my/membership/growth-logs`；后台 5 条 `/admin/member-levels*`、`/admin/members/:user_id` |
| 砍价 | 公开 2 条 + 前台 6 条 + 后台 5 条 |
| 分销 | 保留 05 的 8 条（`/distribution/*` + `/admin/distributions/*`），新增 `POST /admin/distributions/commissions/:id/settle` |
| 商品 | `freight_template_id` 进入创建/更新请求与商品实体（绑定入口与有效性校验） |
| 新增权限码 | `distribution:settle`、`freight:create/list/view/update`、`member:list/view/create/update/delete` |
| 契约删除 | 未恢复任何无实现支撑的虚构接口；地区 `region.yaml` 由真实路由重新生成 |

## 8. 文档变化

### 8.1 `docs/design/`（系统应该怎样运行）

| 文档 | 变化 |
| --- | --- |
| `architecture.md` | 模块职责补钱包/运费计价/砍价/会员；退款职责补钱包入账与成长值扣减 |
| `order.md` | 订单接入统一计价与运费；支付渠道（mock/balance）；状态流水已写入；金额守恒与历史兼容 |
| `pricing.md` | 六槽位生效范围、订单落库公式、会员折扣来源、积分槽位仍 Deferred |
| `refund.md` | 退款成功事务包含钱包入账（仅余额渠道）、佣金冲回、成长值扣减、订单状态流水 |
| `wallet.md` | 订单支付/退款已接入；错误码域改为 22 |
| `distribution.md` | 人工结算状态机、成环校验、并发绑定语义、结算≠资金转账；`settled_at` 语义 |
| `marketing.md` | 砍价已实现范围；拼团未集成；砍价下单未实现 |
| `migration.md` | 最终版本清单（15 / `20261111000001-3`）与上移原因、下一可用版本 |
| `error-codes.md` | 最终域分配表（20-24）、重排依据、下一空闲域序 25 |
| `rbac.md` | 新增 `distribution:settle`、`freight:*`、`member:*` 权限清单 |

### 8.2 `docs/dev/`（代码现在怎样实现）

| 文档 | 变化 |
| --- | --- |
| `order-pricing.md` | 真实调用链（Quote/Create/事务复核）、事务边界、幂等指纹、测试地图 |
| `wallet.md` | 余额支付与退款入账的真实调用链、幂等键、非余额渠道不退回余额 |
| `distribution.md` | 统一实现说明、成环校验、人工结算、并发绑定（1062 回滚后判定）、0bp 默认值 |
| `freight.md` | 商品绑定入口、订单接入、迁移版本 |
| `member.md` | 会员折扣进入 `pricing.Quote`、成长值幂等、错误码域 24 |
| `marketing.md` | 砍价已集成 / 拼团未集成 / 砍价下单未实现 |
| `product-sku.md`、`legacy-migration.md`、`iam.md`、`address.md` | 未受本轮语义影响，保持原样（01/02 已在其分支更新） |

### 8.3 `docs/reports/`

- 保留七个独立任务报告：`B1-04-01_*`、`B1-04-02-*`、`B1-04-03-*`、`B1-04-04-*`、`B1-04-05-*`、`B1-04-07-*`、`B1-04-08-*`；
  **06 的报告随分支缺失，仓库中不存在**。
- 本文件为重写后的统一集成报告（上一版「6 个分支缺失」的结论已不适用，历史见 Git）。

## 9. 测试与验证

### 9.1 环境隔离

- 集成测试只写 `my_shop_b104_integration`；砍价测试按其自身夹具使用 `my_shop_bargain_test`（通过
  `BARGAIN_TEST_MYSQL_*` 覆盖到本机 3306，避免依赖不存在的 13307 实例）；`my_shop_boot_test`、`my_shop_mig_check` 为独立库。
- `my_shop` 只做只读查询，未作为任何写操作目标。
- Redis 使用 `REDIS_DEFAULT_DB=9`。

### 9.2 验证矩阵（真实命令与退出码）

| 检查 | 状态 | 命令与退出码 | 说明 |
| --- | --- | --- | --- |
| gofmt | PASS | `gofmt -l .` → **0**，无输出 | — |
| Build | PASS | `go build ./...` → **0** | — |
| Vet | PASS | `go vet ./...` → **0** | — |
| Unit | PASS | `bash scripts/test-unit.sh` → **0** | 纯内存包 + 生成器单测 |
| Integration | FAIL | `bash scripts/test-integration.sh` → **1** | 45 个包 `ok`；仅 `internal/controller/address`（2 例）与 `internal/controller/region`（3 例）失败，原因见 §11.1：隔离库无地区字典数据（外部 `system_city` 源库不存在） |
| 完整测试 | FAIL | `go test -p 1 ./...` → **1** | 与上表同一 5 个用例，其余全部 PASS（含 boot、migrations、e2e） |
| Race（脚本） | FAIL | `bash scripts/test-race.sh` → **1** | 前 4 段 PASS；第 5 段要求 compose 管理的 MySQL（`make deps-start`），当前为独立容器，脚本按设计拒绝静默跳过 |
| Race（等价手工） | PASS | `go test -race -p 1 ./internal/logic/{order,distribution,wallet}` → **0**；砍价 `-race` → **0** | 覆盖并发支付、并发绑定、并发计佣、助力并发 |
| Migration | PASS | `migrate up` → **0**；`migrate version` → **0**；`go test -run TestB104IntegrationMigrationsUpDownUp ./internal/migrations/` → **0** | 空库 63 表、`dirty=false`；含历史数据兼容 |
| OpenAPI | PASS | `go run ./tools/openapi-gen -repo .` → **0**；`-check` → **0**；`bash scripts/contract-check.sh` → **0** | 195 常驻 API + 4 planned、455 Schema、213 未引用组件（白名单） |
| Registry | PASS | `bash scripts/check-registry.sh` → **0** | 仅 RESERVED 域 INFO，无漂移 |

### 9.3 业务不变量覆盖

| 模块 | 验证用例 | 结果 |
| --- | --- | --- |
| 用户资料 | `TestProfileIsolation`、`TestProfilePhoneUnique` | PASS |
| 地址 | 默认地址唯一/并发/隔离（`TestAddressDefault*`、`TestAddressIsolation`） | PASS |
| 地区 | `TestRegionListByParent`/`Tree`/`Detail`、`TestAddressRegion*` | **FAIL（缺地区字典夹具）** |
| SKU / 商品 | 规格组合唯一、SKU 编码、下单价格来自数据库（`test/e2e/order_sku_pricing_test.go`、`order_pricing_wallet_test.go`） | PASS |
| 计价 | `pricing_test.go` + `TestOrderQuoteFreightAndMemberDiscount`（守恒/溢出/槽位顺序） | PASS |
| 运费 | 运费单测/集成 + 订单运费 900 的按件续件计算 | PASS |
| 订单 | 事务内改价复核、幂等指纹、状态流水（`TestOrderCreateRepricesAuthoritativePriceInTx`、`TestOrderWalletPayAndRefundAtomicity`） | PASS |
| 钱包 | 并发扣款/余额不足回滚/重复支付（`internal/logic/wallet`、订单测试） | PASS |
| 退款 | 钱包+佣金+成长值+订单状态原子一致、重复退款不重复入账（`TestOrderWalletPayAndRefundAtomicity`） | PASS |
| 会员 | 成长值幂等、等级升级、折扣进入计价（`internal/logic/member`、链式测试） | PASS |
| 分销 | 佣金唯一、退款只冲回一次、并发绑定、成环、人工结算（`internal/logic/distribution`、`test/e2e/distribution_test.go`） | PASS |
| 拼团 | — | **BLOCKED（无实现）** |
| 砍价 | 并发助力不破底价、状态推进（`internal/logic/bargain`、controller 测试） | PASS |

## 10. 关键业务链路（最终实现）

1. **商品 → SKU → 订单创建**：`sku_code` 定位（兼容 `sku_id`）→ 事务内 `FOR UPDATE` 重读 SKU/商品 →
   快照编码/规格/原价/名称/主图 → 扣库存 → 预占券 → 清购物车 → 状态流水。
2. **算价 → 运费 → 会员折扣 → 实付**：`pricing.Quote` 六槽位；商品金额取 `skus.price×qty`；
   券由 `ICoupon.GetDiscount`；会员折扣 `floor(original×(10000-rate_bp)/10000)` 并夹紧；
   运费由 `IFreight.Quote` 按 `region_id` + `freight_template_id` 计算；`total = original - coupon - member + shipping`。
3. **充值 → 钱包支付 → 退款入账**：Mock 充值（默认关闭）→ 支付 `DebitForOrderInTx`（`order_pay:<id>`）→
   退款仅余额渠道 `CreditForRefundInTx`（`refund:<id>`）。
4. **支付 → 分销佣金 → 退款冲回**：`uk_order_id` 一单一条；比例取配置（默认 0）；退款 `status IN (10,20)→30` + `uk_refund_id`。
5. **支付 → 会员成长值 → 退款扣减**：`growth:order_reward:<order_id>` / `growth:refund_deduct:<refund_id>` 幂等。
6. **拼团 → 订单 → 库存 → 失败补偿**：**未集成（BLOCKED）**。
7. **砍价 → 助力 → 成交状态**：活动/实例/助力/并发/最低价/懒过期已实现；**砍价成功后的下单未实现**（业务规则未冻结）。

## 11. 未执行 / 未验证事项

| 项 | 状态 | 原因 |
| --- | --- | --- |
| 06 拼团集成 | **BLOCKED** | 分支/提交在本地、远端、对象库、reflog、stash 中均不存在 |
| 地区相关 5 个用例 | **NOT_RUN / FAIL（环境夹具）** | 隔离库 `regions` 为空；地区字典来自外部 `system_city` 源库（B1-03-04 导入），本轮环境无该数据源。未用伪造数据让测试通过 |
| `scripts/test-race.sh` 全量 | **NOT_RUN（第 5 段）** | 要求 compose 管理的 MySQL，当前为独立容器；已用等价 `-race` 手工执行核心包 |
| `scripts/test-integration.sh` 全绿 | **FAIL** | 同上，仅地区夹具 5 例 |
| 积分抵扣 | **NOT_RUN** | 无积分账本实现，`PointsDiscount` 恒 0（未伪造实现） |
| 砍价成功后下单 | **NOT_RUN** | 成交价/下单时机/订单关联方式无冻结契约，属 Owner 决策 |
| 真实支付渠道 / 提现 / 多级分销 / 部分退款 | **NOT_RUN** | 任务明确禁止或未到阶段 |
| 前端联调 | **NOT_RUN** | 本任务只负责 Go 后端 |
| Docker/K8s 部署验证 | **NOT_RUN** | 未变更部署编排 |

## 12. 未完成的业务决策（需 Owner）

| # | 决策点 | 当前证据 | 影响 | 是否阻塞上线 | 需要 Owner 决定 |
| --- | --- | --- | --- | --- | --- |
| 1 | 正式佣金比例 | `distribution.commission_rate_bp` 默认 0（关闭）；08 报告中的 500bp 仅存在于测试夹具 | 直接决定资金支出 | 否（0 安全） | 是否启用 5%，何时启用 |
| 2 | 佣金人工结算是否对接真实打款 | 当前 `settle` 只把 10→20 并写 `settled_at`，无钱包流水/无转账 | 结算≠到账，运营可能误解 | 否 | 是否引入打款/提现/对账 |
| 3 | 砍价成功后的下单规则 | 无成交价公式、下单时机、订单关联定义 | 砍价无法闭环 | 是（若要求闭环） | 成交价、下单时机、与 `pricing.Quote` 的对接方式 |
| 4 | 拼团分支如何恢复 | 仓库无任何痕迹 | 拼团整块能力缺失 | 是（若要求拼团） | 提供分支所在远端/机器，或重新排期 |
| 5 | 积分抵扣规则 | 只有 B1-03 的账户/流水表，无实现 | 无法计算积分抵扣 | 否 | 换算比例、上限、叠加顺序 |

## 13. 最终 Git 状态

| 项 | 值 |
| --- | --- |
| 目标分支 | `feat/p2-backend` |
| 集成开始时 SHA | `637ae2d35d98892094b82184bc8045e6fda3b42c` |
| Merge 提交 | `60c0751`（02）、`d1400cc`（03）、`2446b5d`（04）、`0a67d5d`（07）、`7e7cede`（08）；01/05 为上一轮 `10f05fa`/`51d88ef` |
| 集成提交 | `7458f85`（核心实现与测试）、本报告提交（文档） |
| 最终 HEAD | 本报告提交（`git log -1`） |
| 源分支 | 全部保留，HEAD 未改动：01 `5f233ee`、02 `ac09d69`、03 `9737072`、04 `37750fb`、05 `cae9638`、07 `0865a9c`、08 `deebff2`；06 不存在 |
| 工作区 | 干净 |
| Push | 未执行 |
| 合并 main / develop | 未执行 |
| 破坏性命令 | 未执行（无 `reset --hard`、无 `clean -fd`、无 `push --force`） |
| 测试数据库 | 仅创建/使用 4 个隔离库；未写 `my_shop` |
