# B1-04 八分支统一集成 · 交付报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-INTEGRATION：八个独立开发分支统一集成 |
| 目标分支 | `feat/p2-backend` |
| 集成前基线 SHA | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60`（`af512f9`，与 `origin/feat/p2-backend` 一致，0 ahead / 0 behind） |
| 执行日期 | 2026-11-11 |
| 执行环境 | Go `1.26.0 linux/amd64`、MySQL `8.0`（容器 `b103-mysql`）、Redis `7-alpine`（容器 `b103-redis`） |
| 隔离测试库 | `my_shop_b104_integration`（本次创建）、`my_shop_b104_race`（创建后删除）、`my_shop_b104_downcheck`（创建后删除） |
| Push | **未执行**（本任务禁止 Push） |
| 合并 main/develop | **未执行** |

## 1. 结论摘要

本次集成只完成了 **8 个分支中的 2 个**，原因不是冲突或工作量，而是**其余 6 个分支在仓库中根本不存在**：

- `feat/b104-01-profile-region-address`：**已完整集成**（用户资料、地区字典、地址 `region_id`、Migration 15）。
- `feat/b104-05-distribution`：**已完整集成**（一级分销绑定/计佣/冲回、用户端 4 条与后台 4 条只读接口、2 个权限码）。
- `feat/b1-04-02-sku-spec-product-detail`、`feat/b1-04-03-order-pricing-freight`、
  `feat/b1-04-04-wallet`、`feat/b1-04-06-group-buy`、`feat/b1-04-07-bargain`、
  `feat/b1-04-08-member-distribution`：**BLOCKED（证据见 §2.2）**——本地无分支、远端无引用、
  Git 对象库无对应 commit、reflog / stash / 其他 worktree 与克隆中均无痕迹。

直接后果：任务书第六阶段的跨模块业务链（统一计价 `pricing.Quote`、订单算价与创建链路、
钱包支付与退款入账、会员等级与成长值、拼团、砍价、运费模板）**没有可集成的实现**。
仓库当前不存在 `internal/logic/pricing`、`wallet`、`member`、`group_buy`、`bargain`、`points`，
也不存在 `POST /orders/quote`。这些内容一律未凭空创建，也未用 Mock 伪装为已实现。

在可核实范围内已完成：两个分支的合入、Migration 版本统一、错误码唯一性核实与登记、
OpenAPI 全量重新生成并过门禁、路由与权限基线更新、设计/开发文档同步、隔离 MySQL 环境下的
Migration Up/Down 与完整测试执行。

## 2. 集成基线与 Git 事实

### 2.1 开工前基线核验

| 检查 | 结果 |
| --- | --- |
| 仓库 | `/root/projects/my-shop`（唯一克隆；`/workspace` 为无关仓库 `fangfangtext/et.git`） |
| Worktree | 仅 1 个：`/root/projects/my-shop`；核验时检出 `feat/b104-05-distribution`（工作区干净） |
| `feat/p2-backend` | `af512f9`，与 `origin/feat/p2-backend` 一致；**未回退基线** |
| `stash` | 空 |
| `git fsck --unreachable --dangling` | 无输出（无游离 commit 可恢复） |
| 磁盘其他 `.git` | 仅本仓库与无关的 `/workspace/.git` |
| 开工前构建 | `go build ./...` 成功（exit 0） |

### 2.2 八个分支核验

| # | 任务 | 分支 | 报告预期 Commit | 本地/远端引用 | 对象存在 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| 01 | 用户资料与地区地址 | `feat/b104-01-profile-region-address` | `5f233eef74501cefc84d135dacd4daf4421fd0fd` | 本地存在 / 远端无 | 存在 | **一致**，无后续新提交 |
| 02 | SKU 规格与商品详情 | `feat/b1-04-02-sku-spec-product-detail` | `cae0adb` | 无 | `fatal: Not a valid object name` | **缺失** |
| 03 | 订单金额与运费 | `feat/b1-04-03-order-pricing-freight` | `9737072` | 无 | `fatal: Not a valid object name` | **缺失** |
| 04 | 钱包与 Mock 充值 | `feat/b1-04-04-wallet` | `37750fb` | 无 | `fatal: Not a valid object name` | **缺失** |
| 05 | 一级分销与后台管理 | `feat/b104-05-distribution` | `cae96384be44a67c2e9095510bbfe4d8bbbc054d` | 本地存在 / 远端无 | 存在 | **一致**，无后续新提交 |
| 06 | 拼团业务 | `feat/b1-04-06-group-buy` | `50ae62d` | 无 | `fatal: Not a valid object name` | **缺失** |
| 07 | 砍价业务 | `feat/b1-04-07-bargain` | `0865a9cc535aefe206c73a691abad3fcf6db357b` | 无 | `fatal: git cat-file: could not get object info` | **缺失** |
| 08 | 会员等级与一级分销 | `feat/b1-04-08-member-distribution` | `deebff2ac800f5ef32a2d3248dd0b7d3aa4ea5e0` | 无 | `fatal: git cat-file: could not get object info` | **缺失** |

补充证据：

- `git for-each-ref` 全量引用中不存在上述 6 个分支名（含 `refs/remotes/*`）。
- `git reflog --all` 仅有 01 与 05 的 `branch: Created from` 记录，从未创建过其余 6 个分支。
- `git stash list` 为空；`git fsck --unreachable --dangling` 无输出。
- `git worktree list` 仅主工作区；磁盘扫描未发现其他克隆或 worktree。
- 按任务书要求「不能因缺少远端引用就认定成果不存在」逐项排查后，仍无任何可恢复的开发成果。

### 2.3 分支提交范围检查（是否混入其他任务）

| 分支 | 相对 `af512f9` 的提交 | 判定 |
| --- | --- | --- |
| 01 | `5f233ee feat(b104-01): implement user profile and region address` | 单一提交，改动仅 profile/region/address + Migration 15 + 文档，**无混入** |
| 05 | `288ea77 feat(b104-05): …一级分销关系、订单佣金与退款冲回`、`cae9638 docs(report): B1-04-05 …交付报告` | 两个提交，改动仅分销 + 订单/退款钩子 + 契约 + 文档，**无混入** |

## 3. Merge 提交记录

全部使用 `git merge --no-ff` 保留独立开发历史，未 Cherry-pick、未删除源分支。

| 顺序 | 源分支 | Merge Commit | 父提交 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `feat/b104-01-profile-region-address`（`5f233ee`） | `10f05fa8e00f43ab736e5f43859de46ce17315ba` | `af512f9` + `5f233ee` | 干净合并，0 冲突 |
| 2 | `feat/b104-05-distribution`（`cae9638`） | `51d88ef12695616b8dd4e18e305e899156767e1c` | `10f05fa` + `cae9638` | 自动合并，0 文本冲突；语义冲突见 §4 |

后续集成提交：

| Commit | 内容 |
| --- | --- |
| `dfd722f` | 按集成后真实实现重新生成 OpenAPI；路由/生成器/迁移测试基线统一（155 条路由） |
| `2da0c5a` | 统一设计文档与全局资源 Registry（Migration 15、错误码域状态） |
| 本报告提交 | `docs/reports/B1-04-integration.md`（最终 HEAD，SHA 见交付消息） |

## 4. 解决的问题

### 4.1 文本冲突

**0 处**。两个分支都使用 `// B1-04-01 BEGIN/END`、`// B1-04-05 BEGIN/END` 锚点，
`codes.go`、`routes_frontend.go`、`logic.go`、`order.go`、`refund.go` 均为不同位置的增量改动，
Git 自动合并成功。合并后逐项复核了最终文件内容（而非依赖「自动合并即正确」）。

### 4.2 语义冲突（必须人工处理）

| # | 问题 | 证据 | 处理 |
| --- | --- | --- | --- |
| 1 | 生成器未登记 `profile` / `region` 业务域，`go run ./tools/openapi-gen` 直接失败 | `api/profile/v1/profile.go: 未登记的 API 业务域 "profile"` | 在 `tools/openapi-gen/model.go` 登记两个域与 `UserProfile`/`Region`/`RegionTreeNode` 实体名 |
| 2 | 05 为通过基线门禁重新生成契约，删除了 `openapi/paths/region.yaml` 并移除了 `iam.yaml` 中无实现支撑的 `/regions`、`PUT /me` | 05 报告 §2.3；`git show 51d88ef -- openapi/paths/iam.yaml` | 不做人工回填：合并 01 的真实实现后**重新生成**，`region.yaml`、`profile.yaml` 由代码再生，`PUT /me` 仍不存在（未虚构接口） |
| 3 | 新增错误码 `2016 CodePhoneExists` 在生成物中落到 `domain: unknown` | `openapi/components/error-codes.yaml` 首次生成结果 | `tools/openapi-gen/parse_codes.go` 前缀表登记 `CodePhoneExists → iam` |
| 4 | 路由基线写死 150 条 | `internal/cmd/routes_test.go`、`tools/openapi-gen/gen.go`、`check.go` | 统一为 155/76/77/2，并补齐 5 条新路由到清单 |
| 5 | 生成器测试写死「routes 文件 148 条」 | `tools/openapi-gen/main_test.go` | 更新为 153（155 − 2 health） |
| 6 | 新增 5 个 GET 请求 DTO 被判定为「未引用组件」 | `-check` 报错 5 项 | 在 `unused-components-allowlist.yaml` 的 `dto-request` 分组逐项登记（GET 无 body，按既有口径保留） |
| 7 | 迁移测试写死最新版本 14 与 `users` 结构快照 | `TestSchemaStructureMatchesBaseline`：`users: 列数量 mismatch: want 10, got 11` | `latestMigrationVersion` → `20261110000015`；结构快照补 `birthday DATE NULL` |
| 8 | 01 的三个测试文件把测试库名写死为 `my_shop_b104_test`，在本次隔离库直接拒绝执行 | `拒绝执行破坏性测试：DATABASE_DEFAULT_NAME="my_shop_b104_integration"` | 改为**隔离测试库白名单**（`my_shop_b104_test` + `my_shop_b104_integration`），仍拒绝 `my_shop`；断言强度不变 |
| 9 | `scripts/contract-check.sh` 成功文案写死 150 条 | 脚本第 97 行 | 同步为 155 条（仅文案，不放宽任何判定） |

**未采用**「用某个分支的整份文件覆盖另一个分支」的做法，所有共享文件均按实际语义逐段保留。

## 5. Migration 最终编号

### 5.1 最终清单（本次新增部分）

| Migration | 来源分支 | 修改内容 | 是否冲突 | 处理方式 |
| --- | --- | --- | --- | --- |
| `20261110000015_profile_region_address` | B1-04-01 | `users` 增 `birthday DATE NULL`；`addresses` 增 `region_id`（可空、软引用 `regions.level=3`）+ `idx_region_id`；成对 down | **否** | 保留为唯一版本 15 |
| 无新增 Migration | B1-04-05 | 复用 B1-03-03 的 `20261110000011~14` 既有 Schema | 否 | 不新增迁移 |

### 5.2 版本 15 风险核实（任务书最高优先级）

| 检查 | 结果 |
| --- | --- |
| 目标分支现有最高版本 | `20261110000014_distribution`（B1-03 的 00001~00014 全部存在） |
| 分支 01 实际文件 | 仅 `20261110000015_profile_region_address.{up,down}.sql` |
| 分支 05 实际文件 | 无 SQL 变更 |
| 02 / 08 的「Migration 15」 | 对应分支不存在，仓库中无任何 `order_member_discount` 或 SKU 相关 15 号迁移文件 |
| 03 的「20261110000016」 | 对应分支不存在，无 16 号文件；当前 16 号**空闲** |
| `my_shop` | `schema_migrations = 20261110000014`，未应用 15 |
| `my_shop_b104_test` | `20261110000015`，Schema 为 `users.birthday` + `addresses.region_id`，**与 01 一致** |
| `my_shop_b10401_migrate` | `20261110000015`，同上，**同一 Schema** |
| `my_shop_b10401_test` | `version=20261001000001, dirty=1`（历史 dirty 库，未使用） |
| `my_shop_b10304_verify` | `20261110000014` |
| `my_shop_staging` | 无 `schema_migrations` 表（仅承载历史导入源数据） |

**结论**：未发现「同一版本号对应不同 Schema」的数据库，版本 15 未被其它分支占用，
因此按项目规则确定为 `20261110000015 = profile_region_address`，下一可用版本为 `20261110000016`。
只做了只读查询，未对任何既有数据库执行 Up / Down / DROP / 重建。

### 5.3 Up/Down 实测（隔离库）

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| 空库完整迁移 | `DATABASE_DEFAULT_NAME=my_shop_b104_integration go run . migrate up` | exit 0，`dirty=false`，`version=20261110000015`，共 61 张表 |
| 版本查询 | `go run . migrate version` | `version: 20261110000015 (dirty=false)` |
| Down 脚本 | 在临时库 `my_shop_b104_downcheck` 执行 `20261110000015…down.sql` | exit 0，`users.birthday` 与 `addresses.region_id` 均被移除 |
| 重新 Up | 再次执行 `…up.sql` | exit 0，两列恢复 |
| 迁移包回归 | `go test -p 1 ./internal/migrations/` | exit 0（含多版本 down/re-up 语义与结构快照） |

临时库 `my_shop_b104_downcheck` 验证后已删除。

## 6. 错误码分配结果

### 6.1 实际进入集成分支的错误码

| 域序 | 区间 | 归属 | 新增编号 | 说明 |
| --- | --- | --- | --- | --- |
| 2 | 2000-2999 | IAM（ACTIVE） | `2016 CodePhoneExists` | 手机号唯一冲突，409；位于 IAM 段内，未新占域 |
| 20 | 20000-20999 | distribution-v1（RESERVED） | `20001` 自荐、`20002` 推荐人不可用、`20003` 已绑定不可改绑 | 新域，Registry 已登记 |

### 6.2 任务书列出的冲突编号核实

| 报告来源 | 报告编号 | 仓库实际情况 |
| --- | --- | --- |
| 运费 | 20000–20999 | 无实现、无分支，`codes.go` 无对应常量 |
| 会员 | 20001–20005 | 无实现、无分支 |
| 钱包 | 21001–21010 | 无实现、无分支 |
| 砍价 | 21001–21009 | 无实现、无分支 |
| 分销 | 21001–21007 | 实际实现为 `20001–20003`（B1-04-05） |

因此**不存在需要 renumber 的已实现冲突**；`20000-20999` 已被分销占用，不可复用。

### 6.3 后续规则（已写入文档）

- `.agent/registry/error-codes.md`：分销域保持 `RESERVED`（已并入 `feat/p2-backend`、未进入 `develop`，
  不虚报 ACTIVE），并标注「下一个空闲域序 21（`21000-21999`）」。
- `docs/design/error-codes.md`：记录本次核实结论与下一空闲域。
- 未发布分支的错误码仍可在其恢复后申请 21+ 域，但**不得**占用 `20000-20999` 或已落地的 `2016`。
- 代码、测试、OpenAPI（`components/error-codes.yaml`）与 Registry 已同步，未只改 Registry。

## 7. API 与权限变化

| 项 | 变化 |
| --- | --- |
| 常驻 API 总数 | 142（B1-01 冻结）→ 150（B1-04-05）→ **155**（本次 + B1-04-01 的 5 条） |
| 前台 | 76 条（含新增 `/profile` 2 条、`/regions` 3 条） |
| 后台 | 77 条（分销后台 4 条已含在 05 的 150 内） |
| Health | 2 条 |
| 新增公开接口 | `GET /regions`、`GET /regions/tree`、`GET /regions/:id` |
| 新增用户接口 | `GET /profile`、`PUT /profile`、`POST /distribution/referral`、`GET /distribution/me`、`GET /distribution/subordinates`、`GET /distribution/commissions` |
| 新增后台接口 | `GET /admin/distributions/referrals`、`/distributors`、`/commissions`、`/commissions/stats`（全部只读） |
| 新增权限码 | `distribution:referral_list`、`distribution:commission_list`（已 seed，路由 `RequirePermission` 与审计一致） |
| 契约删除 | 基线中无实现支撑的虚构 `PUT /me` 未被恢复（不虚构接口） |
| 地址契约 | `addresses.region_id` 进入创建/更新/查询 Schema，语义为「软引用 `regions.level=3`」 |

## 8. 文档变化

### 8.1 `docs/design/`（系统应该怎样运行）

| 文档 | 变化 |
| --- | --- |
| `architecture.md` | 新增「用户资料与地区」模块职责；前台路由组织补 `/profile`、地区字典 |
| `migration.md` | 固化版本 15 唯一性与下一可用版本 16（本次核实结论） |
| `error-codes.md` | 记录实际错误码冲突核实结果、分销域状态与下一空闲域序 21 |
| `address.md`、`iam.md` | 由 01 更新（`region_id` 语义、资料字段与手机号唯一） |
| `order.md`、`refund.md`、`rbac.md`、`distribution.md` | 由 05 更新（计佣/冲回事务边界、权限码、佣金比例与结算边界） |

### 8.2 `docs/dev/`（代码现在怎样实现）

| 文档 | 变化 |
| --- | --- |
| `address.md`、`iam.md` | 由 01 更新（profile/region 代码入口、测试地图） |
| `distribution.md` | 由 05 新增（绑定/计佣/冲回调用链、事务边界、测试地图） |
| `order.md`、`wallet.md`、`product-sku.md`、`freight.md`、`member.md`、`marketing.md` | **未创建**：对应实现分支缺失，无代码可写，拒绝写「设想中的实现」 |

### 8.3 `docs/reports/`

- 保留的独立任务报告：`docs/reports/B1-04-01_USER_PROFILE_REGION_ADDRESS.md`、
  `docs/reports/B1-04-05-distribution.md`（其余 6 份报告随分支缺失，仓库中不存在）。
- 新增本次统一集成报告：`docs/reports/B1-04-integration.md`（本文件）。

## 9. 测试与验证

### 9.1 隔离环境

- 全部写操作只指向 `my_shop_b104_integration`（新建）；`my_shop_b104_race`、`my_shop_b104_downcheck` 为一次性库，用完删除。
- 未使用 `my_shop`、`my_shop_b10304_verify`、`my_shop_b10401_*`、`my_shop_b104_test`、`my_shop_staging` 作为测试目标（仅只读查询）。
- 地区字典测试数据（3938 行）从 `my_shop_b104_test` **只读复制**到隔离库，未修改来源库。
- Redis 通过 `REDIS_DEFAULT_DB=9` 隔离，避免 `FlushDB` 影响其他任务使用的 db 0。

### 9.2 验证矩阵

| 检查 | 状态 | 命令与退出码 | 说明 |
| --- | --- | --- | --- |
| Build | PASS | `go build ./...` → **0** | 合并前后均通过 |
| Vet | PASS | `go vet ./...` → **0** | — |
| gofmt | PASS | `gofmt -l .` → **0**，无输出 | — |
| Unit | PASS | `bash scripts/test-unit.sh` → **0** | 纯内存包 + 精确匹配用例 |
| Integration | PASS | `DATABASE_DEFAULT_NAME=my_shop_b104_integration … go test -p 1 -count=1 <除 internal/boot 外全部包>` → **0**；随后 `go test -p 1 ./internal/boot/` → **0** | 见 §9.3：脚本原样执行 exit 1，原因是 boot 用例会 DROP 全部表导致地区字典 fixture 丢失 |
| Race | PASS | `bash scripts/test-race.sh` → **1**（第 5 段因无 compose MySQL 退出）；等价手工执行：Outbox 11 个用例 `go test -race …` → **0**，Consumer 2 个用例 → **0**，进程内 race 用例 → **0** | 见 §9.3 |
| Migration | PASS | `migrate up` → **0**；`migrate version` → **0**；隔离库 Down/Up → **0**；`go test ./internal/migrations/` → **0** | 空库 61 张表、`dirty=false` |
| OpenAPI | PASS | `go run ./tools/openapi-gen -repo .` → **0**；`-check` → **0**；`bash scripts/contract-check.sh` → **0** | 155 常驻 API + 4 planned、358 Schema，生成无漂移 |
| Registry | PASS | `bash scripts/check-registry.sh` → **0** | 仅对 RESERVED 域输出 INFO，非漂移 |
| 完整测试 | PASS（拆分执行） | `go test -p 1 -count=1` 全包 → 各包 **0**（boot 单独最后执行 → **0**） | 35 个包全部 `ok` |

### 9.3 需要说明的两处「脚本原样 exit 1」

1. **`scripts/test-integration.sh` 原样执行 exit 1（两次）**，被以下两类测试阻断：
   - 01 的三个测试文件当时把 `DATABASE_DEFAULT_NAME` 写死为 `my_shop_b104_test` → 已改为隔离库白名单；
   - `internal/boot` 的迁移用例按设计 `DROP TABLE` 全部表并重建，导致预先导入的 `regions` 测试数据丢失，
     之后按字母序执行的 `address`/`region` 用例失败（`expected 34 provinces, got 0`）。
   处理方式：**不放宽任何断言**，改为把 `internal/boot` 放到最后单独执行；其余包一次性执行通过。
   这是测试夹具顺序问题，不是业务缺陷。
2. **`scripts/test-race.sh` exit 1**：脚本第 5 段要求 docker compose 管理的 MySQL（`docker_compose_placeholder ps -q mysql`），
   当前实例为独立容器 `b103-mysql`，脚本按设计**拒绝静默跳过**。改用同一批用例、同一 `-race` 参数、
   在专用隔离库 `my_shop_b104_race` 手工执行，11 个 Outbox 用例与 2 个 Consumer 用例全部 `--- PASS`，
   逐名校验通过后删除该库。未修改门禁脚本以绕过检查。

### 9.4 业务不变量覆盖情况

| 模块 | 是否可验证 | 说明 |
| --- | --- | --- |
| 用户资料 | 是 | `TestProfileIsolation`（只能改自己）、`TestProfilePhoneUnique`（并发唯一）通过 |
| 地址 | 是 | `TestAddressDefaultUniqueAcrossUsers` 等通过（同用户仅一个默认地址） |
| 地区 | 是 | `TestRegionListByParent`/`Tree`/`Detail` 通过（层级、排序、链路） |
| SKU / 商品 | 部分 | 既有 SKU 规格唯一与商品价格用例通过；B1-04-02 的快照增强未集成 |
| 计价 | **否** | 无 `pricing` 实现（B1-04-03 缺失） |
| 运费 | **否** | 无运费模板实现（B1-04-03 缺失） |
| 订单事务幂等 | 部分 | 既有订单事务与幂等用例通过；A6–A9（quote、事务内复核、requestHash、状态流水原子写）未实现 |
| 钱包 | **否** | 无实现（B1-04-04 缺失），仅 Schema 存在 |
| 退款 | 部分 | 退款状态机 + 佣金冲回原子性用例通过；钱包入账/成长值扣减无实现 |
| 会员 | **否** | 无实现（B1-04-08 缺失），仅 Schema 存在 |
| 分销 | 是 | `test/e2e/distribution_test.go` 11 个用例通过（佣金唯一、退款只冲一次、绑定并发） |
| 拼团 | **否** | 无实现（B1-04-06 缺失），仅 Schema 存在 |
| 砍价 | **否** | 无实现（B1-04-07 缺失），仅 Schema 存在 |

## 10. 未执行 / 未验证事项

| 项 | 状态 | 原因 |
| --- | --- | --- |
| 6 个缺失分支的合并 | **BLOCKED** | 分支与 commit 在本地、远端、对象库、reflog、stash、其他 worktree 中均不存在 |
| 统一计价 `pricing.Quote` 接入 | **NOT_RUN** | 无实现可接入；未创建第二套计价公式 |
| `POST /orders/quote`、事务内金额复核、`requestHash` 扩展、状态流水原子写 | **NOT_RUN** | B1-04-03 未交付 |
| 钱包支付/退款入账、会员折扣与成长值、拼团、砍价 | **NOT_RUN** | B1-04-04/06/07/08 未交付 |
| 部分退款、真实支付、自动提现、多级分销、前端联调 | **NOT_RUN** | 任务书明确禁止或未到实现阶段 |
| Spectral lint | PASS | 环境可获取 `@stoplight/spectral-cli`，无 error |
| Docker/K8s 部署验证 | **NOT_RUN** | 本任务只负责 Go 后端集成，未变更部署编排 |

## 11. 未完成的业务决策（需 Owner）

| # | 决策点 | 当前证据 | 影响 | 是否阻塞上线 | 需要 Owner 决定 |
| --- | --- | --- | --- | --- | --- |
| 1 | 6 个分支的开发成果如何恢复 | 仓库无任何痕迹（§2.2） | 计价/钱包/会员/拼团/砍价整块能力缺失，B1-04 无法按原范围交付 | **是** | 请提供分支所在远端/机器或确认需重新排期开发 |
| 2 | 正式佣金比例 | B1-04-05 默认 `distribution.commission_rate_bp=0`（关闭计佣）；B1-04-08 报告的 500bp 无代码可核 | 直接决定资金支出 | 否（默认 0 安全） | 是否启用 5%（500bp），何时启用 |
| 3 | 佣金结算（10→20）与人工结算 | 05 明确不实现，无 `settled_at` 写入 | 佣金只能计提，不能标记/打款 | 否 | 结算触发条件（订单完成/售后期结束）与是否人工结算 |
| 4 | 砍价成功后是否下单、成交价规则 | 设计文档无成交价与下单时机定义，08/07 分支缺失 | 砍价无法闭环 | 是（若要求砍价闭环） | 成交价公式、下单时机、与统一计价的对接方式 |
| 5 | 会员折扣与积分抵扣规则 | `pricing.md`/`member.md` 仅有 Schema，无实现 | 无法计算会员价/积分抵扣 | 是（若要求会员能力） | 折扣与抵扣的优先级、上限与是否允许叠加 |

## 12. 最终 Git 状态

| 项 | 值 |
| --- | --- |
| 目标分支 | `feat/p2-backend` |
| 集成前 SHA | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60` |
| 合并提交 | `10f05fa`（01）、`51d88ef`（05） |
| 集成提交 | `dfd722f`、`2da0c5a`、本报告提交 |
| 最终 HEAD | 本报告提交（含随后一次仅回填 SHA 的元数据提交） |
| 源分支 | 8 个分支名全部保留；实际存在的 2 个分支 HEAD 未改动（`5f233ee`、`cae9638`） |
| 工作区 | 干净 |
| Push | 未执行 |
| 合并 main / develop | 未执行 |
| 删除分支 / Worktree | 未执行 |
| 破坏性命令 | 未执行（无 `reset --hard`、无 `clean -fd`、无 `push --force`） |
