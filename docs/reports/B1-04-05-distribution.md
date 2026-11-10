# B1-04-05 一级分销与后台管理交付报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-05：一级分销与后台管理（独立开发，不依赖 B1-04-01） |
| 开发分支 | `feat/b104-05-distribution`（从 `feat/p2-backend` 创建） |
| 基线分支 | `feat/p2-backend`，基线 SHA `af512f9e8a64c133c705d3a0ed1dd106de3d5c60` |
| 生产代码/测试/设计文档提交 | `288ea77eb57394487e1425f872bab1988cbcb60a` |
| 本报告提交 | 本次提交（提交后为本分支最终 HEAD） |
| 验证环境 | MySQL `8.0.46`（容器 `mysql:8.0`）+ Redis `7-alpine` + Go `1.26.0 linux/amd64` |
| 执行日期 | 2026-11-10 |

## 1. 任务目标与范围

在现有 Go 单体后端落地**一级**分销闭环：推荐关系绑定、订单支付时计提佣金、退款成功时冲回佣金，
以及必要的用户端接口与后台只读管理接口。

**本次完成**

- 推荐关系：登录用户绑定唯一上级（禁止自荐、推荐人须启用、不可改绑、并发安全）。
- 佣金生成：接入真实订单支付事务（`IOrder.Pay`），按 `orders.total_amount × 配置比例` 计提，整数分运算。
- 退款冲回：接入真实退款执行事务（`IRefund.Execute` success 分支），条件更新只冲一次、不超额。
- 用户端 API：`/distribution/referral`、`/distribution/me`、`/distribution/subordinates`、`/distribution/commissions`。
- 后台 API：`/admin/distributions/referrals`、`/distributors`、`/commissions`、`/commissions/stats`（全部只读）。
- RBAC：新增 `distribution:referral_list`、`distribution:commission_list` 并接入 `RequirePermission`。
- 错误码：新增域 `20000-20999`（20001/20002/20003），Registry 登记 `RESERVED`。
- 配置：新增 `distribution.commission_rate_bp`（万分比，默认 `0` = 关闭计佣）。
- OpenAPI 契约按真实代码重新生成；新增 `openapi/paths/distribution.yaml`。
- 集成测试：`test/e2e/distribution_test.go`（11 个用例，真实 MySQL/Redis）。
- 文档：更新 `docs/design/distribution.md`，新增 `docs/dev/distribution.md`。

**本次不做（按任务书）**：二级及多级分销、提现、第三方支付分佣、真实结算、自动打款、多商户分销、
团队等级、分销商城、前端页面、前后端联调、重新实现钱包、修改秒杀订单与库存模型、导入历史 SQL。
**另外未做**：佣金结算（10→20）与后台写操作（详见 §10）。

### 1.1 Owner 决策事项（设计文档原为留白，本次按最小口径落地）

设计文档 `docs/design/distribution.md`（B1-03-03 版本）把「计佣基数与比例来源」「绑定入口」「结算触发条件」
列为 Deferred。本次实现必须先选定口径，选择结果与理由如下，**已同步写入设计文档，便于 Owner 复核/推翻**：

| # | 决策点 | 本次口径 | 依据 |
| --- | --- | --- | --- |
| D-1 | 佣金比例来源 | 系统配置 `distribution.commission_rate_bp`，**默认 0 = 关闭计佣** | 设计文档把「系统配置」列为候选来源；默认 0 避免凭空产生资金事实，不硬编码比例 |
| D-2 | 计佣基数 | `orders.total_amount`（实付金额快照，整数分） | 佣金对应实际到账资金；`total_amount` 是唯一服务端派生且下单后不可变的应付快照 |
| D-3 | 舍入规则 | `floor(base × rate_bp / 10000)`，整数运算 | 设计文档 §3.2 已固化该公式 |
| D-4 | 计佣时机 | 订单 `10→20` 支付事务内，与状态推进、券核销同事务 | 设计文档 §3.2「计佣发生在订单支付成功后的业务事务内」 |
| D-5 | 绑定入口与邀请标识 | 登录用户提交 `referrer_user_id`；不支持邀请码/邀请链接 | Java 参考实现的 `spread` 参数即上级 uid；`inviteCode` 依赖 `system_attachment` 附件码，Go 侧无对应基础设施（见 §1.2） |
| D-6 | 结算（10→20） | 不实现 | 触发条件（订单完成/售后期结束）与结算批次语义未定义；不写 `settled_at` |
| D-7 | 后台佣金状态管理 | 不实现（只读） | 无业务依据；Java 参考实现缺失对应后台能力；人工改资金事实属高危操作 |

### 1.2 Java 参考项目核查结论（`tmp/eshop-backend-java`）

| 关注点 | 结论 |
| --- | --- |
| 分销关系绑定 | **部分可用**：`AuthService.register/wechatLogin` 调用 `userService.setSpread(spread, uid)`；`RegParam.inviteCode` 经 `system_attachmentService.getByCode` 映射到上级 uid。`UserService#setSpread` 实现文件不在快照中 → **NOT AVAILABLE** |
| 分销用户查询 | **API 契约可用**：`UserBillController#spreadPeople`（`POST /spread/people`）返回下级列表与统计；`getSpreadCount/getUserSpreadGrade` 实现 **NOT AVAILABLE** |
| 佣金生成/计算 | **NOT AVAILABLE**：无计佣 Service/Mapper/SQL 实现；`ShopUser.brokeragePrice` 仅表示佣金余额字段 |
| 订单与佣金关联 | **NOT AVAILABLE**（无计佣调用点）；仅 `UserBillController#spreadOrder` 声明了「推广订单」入口 |
| 退款与佣金处理 | **NOT AVAILABLE** |
| 后台分销管理 | **NOT AVAILABLE**：无任何分销后台 Controller |
| 用户端佣金明细 | **API 契约可用**：`GET /spread/commission/{type}` → `userBillService.getUserBillList`；`GET /commission` 推广数据（昨日佣金/累计提现/当前佣金） |

因此本任务只在「绑定入口（上级 uid）」与「用户端能力集合」上参考 Java 契约；
计佣公式、冲回语义、事务与幂等全部按本项目设计文档与 MySQL 约束实现，不照搬 Java 架构。

## 2. Git 基线及最终 SHA

### 2.1 开工前基线检查

| 检查 | 结果 |
| --- | --- |
| 起始分支 | `feat/b104-01-profile-region-address`（工作区干净） |
| `git fetch origin` | 成功，无冲突 |
| `feat/p2-backend` vs `origin/feat/p2-backend` | `0 ahead / 0 behind`（完全同步，无分叉） |
| 基线 HEAD | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60` |
| 新建分支 | `git switch -c feat/b104-05-distribution`（此前不存在同名分支） |
| B1-04-01 依赖 | **无**：未合并、未 cherry-pick `feat/b104-01-profile-region-address`（`5f233ee`）的任何提交 |

### 2.2 最终状态

| 项 | 值 |
| --- | --- |
| 开发分支 | `feat/b104-05-distribution` |
| 基线 SHA | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60` |
| 代码/测试/文档提交 | `288ea77eb57394487e1425f872bab1988cbcb60a` |
| 报告提交 | 本次提交（最终 HEAD，见交付消息） |
| 推送 | **未推送**（按要求不 push / 不 merge / 不改 `feat/p2-backend`） |

### 2.3 发现并处置的基线缺陷（需 Owner 知悉）

基线 `af512f9` 的提交说明为「新增地区字典接口、用户资料更新与地址结构化地区字段」，但它**只改了 OpenAPI 与
盘点文档，没有提交任何 Go 代码**（`git show --stat af512f9` 仅 7 个 md/yaml 文件，`api/`、`internal/` 无变更）。
在该提交上执行 `go run ./tools/openapi-gen -repo . -check`（独立 worktree 验证）直接失败 9 项：

```text
OpenAPI paths 中的 GET /regions 不是实际注册路由（可能是虚构接口）
OpenAPI paths 中的 GET /regions/:id 不是实际注册路由
OpenAPI paths 中的 PUT /me 不是实际注册路由
openapi/components/error-codes.yaml 与 internal/codes/codes.go 不一致
openapi/components/schemas.yaml 与 Go DTO 不一致
实际 API 数量为 145，基线为 142
组件 schemas/RegionDetailRequest、RegionListRequest、UserUpdateMeRequest 未被引用且不在白名单
```

即基线本身**无法通过契约门禁**。本次按任务要求运行生成器（契约以 Go 源码为唯一事实源）后，这些无实现支撑的
路径/字段/Schema 被移除，`openapi/paths/region.yaml` 成为孤儿分域视图（其 `/regions` 无对应 DTO/controller/路由），
故删除该文件。**这是生成物一致性修复，不涉及任何 B1-04-01 源码语义**；B1-04-01 分支后续带代码合并时会重新生成
`region.yaml` 与相关字段，不会与本分支冲突（无同文件语义改写）。

## 3. 生产代码变更

### 3.1 新增模块文件

| 文件 | 内容 |
| --- | --- |
| `api/distribution/v1/distribution.go` | 8 个接口的 DTO + `Referral`/`Commission`/`CommissionSummary`/`Subordinate`/`Distributor`/`CommissionStatsItem` 实体；状态字符串枚举 |
| `internal/controller/distribution/distribution.go` | `ControllerV1`（4 个用户端方法，取 `Principal`）+ `AdminControllerV1`（4 个后台只读方法，取 `AdminPrincipal`） |
| `internal/service/distribution.go` | `IDistribution` 接口 + `Distribution()` / `RegisterDistribution()` |
| `internal/logic/distribution/distribution.go` | 全部业务规则与内联 SQL：绑定、查询、计佣 `AccrueOrderCommissionInTx`、冲回 `ReverseOrderCommissionInTx` |

### 3.2 接入真实调用链（核心）

| 文件 | 变更 |
| --- | --- |
| `internal/logic/order/order.go` | `Pay` 的 `g.DB().Transaction` 内、券核销之后调用 `service.Distribution().AccrueOrderCommissionInTx(ctx, tx, id)`；返回错误即整体回滚 |
| `internal/logic/refund/refund.go` | `Execute`（success 分支）事务内、`refund_requests → 60` 成功后、审计之前调用 `service.Distribution().ReverseOrderCommissionInTx(ctx, tx, req.Id, r.OrderId)` |

订单状态机与退款状态机**未改动**：未新增订单状态、未改退款状态、未改库存恢复逻辑；
计佣/冲回是既有事务内的附加步骤。

### 3.3 横切注册

| 文件 | 变更 |
| --- | --- |
| `internal/logic/logic.go` | 空导入 `internal/logic/distribution`（触发 `init()` 注册服务） |
| `internal/cmd/routes_frontend.go` | 4 条 Auth 分组路由：`POST /distribution/referral`、`GET /distribution/{me,subordinates,commissions}` |
| `internal/cmd/routes_admin.go` | 4 条 `RequirePermission` 路由：`/admin/distributions/{referrals,distributors}`（referral_list）、`/admin/distributions/commissions`、`/admin/distributions/commissions/stats`（commission_list） |
| `internal/boot/seed.go` | 新增 2 个权限 seed |
| `internal/codes/codes.go` | 域 `20000-20999`：`CodeDistributionSelfReferral(20001)`、`CodeDistributionReferrerUnavailable(20002)`、`CodeDistributionAlreadyBound(20003)` + 码表映射 |
| `internal/config/contract.go`、`manifest/config/config.yaml`、`docs/development/configuration.md` | 新增配置 `distribution.commission_rate_bp`（`DISTRIBUTION_COMMISSION_RATE_BP`，int，默认 0） |
| `.agent/registry/error-codes.md` | 登记 `20000-20999 / distribution-v1 / RESERVED` |

### 3.4 契约与生成器

| 文件 | 变更 |
| --- | --- |
| `tools/openapi-gen/model.go` | `domains` 新增 `distribution`；`entitySchemaNames` 新增 6 个实体 |
| `tools/openapi-gen/gen.go` | 期望路由数 142→150（前台 67→71、后台 73→77）；`pathFileOrder` 新增 `distribution`；`tagDescriptions` 新增「分销」 |
| `tools/openapi-gen/parse_codes.go` | `CodeDistribution*` → domain `distribution` |
| `tools/openapi-gen/opmeta.go` | clamp 分页 2 条、strict 分页 3 条、`POST /distribution/referral` 的 D1 已知问题 |
| `tools/openapi-gen/metadata.go` | 佣金状态枚举（实体/统计/两个请求 DTO） |
| `tools/openapi-gen/unused-components-allowlist.yaml` | 8 个完整请求 DTO 保留（请求体按字段内联生成） |
| `tools/openapi-gen/main_test.go`、`internal/cmd/routes_test.go`、`scripts/contract-check.sh`、`scripts/test-unit.sh` | 路由总数与分布同步为 150 / 71 / 77 / 2 |
| `openapi/**` | 由 `go run ./tools/openapi-gen -repo .` 重新生成（未手工编辑） |

## 4. Migration 变更

| 项 | 结论 |
| --- | --- |
| 是否新增 Migration | **否** |
| 是否修改现有 Schema | **否**（`20261110000014_distribution.{up,down}.sql` 未改） |
| 使用的现有表 | `user_referrals`、`distribution_commissions`（B1-03-03 建表） |
| 版本冲突 | 未占用 `20261110000015`（B1-04-01 使用的版本），本任务无新版本需求 |

复用依据：现有 Schema 已完整支撑本次实现——`uk_user_id`（一人一上级）、`uk_order_id`（一单一佣金）、
`uk_refund_id`（一退款单一次冲回）、状态列、冲回四列、金额三列（整数分）、`idx_referrer_user`
（下级列表/推荐人归集）与 `idx_beneficiary_status`（受益人佣金查询）均够用。

## 5. 设计文档与开发文档变更

| 文档 | 变更 |
| --- | --- |
| `docs/design/distribution.md` | 重写为「已实现」状态：职责边界、绑定规则表、计佣口径（基数/比例来源/公式/不计佣情形）、状态机、冲回 SQL 与时序、14 条不变量（INV-D01~D14）、跨模块关系、公开 API/权限/错误码、Deferred 清单 |
| `docs/design/order.md` | §5 成功语义补充「支付成功 = 状态推进 + 券核销 + 计佣」；§8 跨模块关系新增一级分销段落 |
| `docs/design/refund.md` | §5 执行成功事务补充「佣金冲回」；§8 新增冲回调用点与幂等说明 |
| `docs/design/rbac.md` | §2.3 权限清单新增 2 个 `distribution:*` |
| `docs/design/error-codes.md` | §4 域分配表新增 `20000-20999 / distribution-v1 / RESERVED` |
| `docs/design/architecture.md` | §2 模块表新增「一级分销」；文末索引新增 `docs/design/distribution.md` |
| `docs/development/configuration.md` | 配置矩阵新增 `distribution.commission_rate_bp` |
| `docs/dev/distribution.md` | **新增**开发知识文档（11 节：定位/代码地图/调用链/数据结构/不变量/事务锁幂等/API 权限错误码/测试地图/排障/维护注意/相关文档） |

## 6. 测试命令、退出码与实际结果

| # | 命令 | 退出码 | 结果 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `gofmt -l api internal tools test main.go` | 0 | PASS | 无输出（全部已格式化） |
| 2 | `go build ./...` | 0 | PASS | — |
| 3 | `go vet ./...` | 0 | PASS | — |
| 4 | `bash scripts/test-unit.sh` | 0 | PASS | 全部包 `ok`，含 `internal/cmd`（路由 150 条锁定）与 `tools/openapi-gen` |
| 5 | `DATABASE_DEFAULT_PASS=root bash scripts/test-integration.sh`（`go test -p 1 ./...`） | 0 | PASS | 全部包 `ok`；`test/e2e 102.195s`、`internal/migrations 280.047s`、`internal/logic/flashsale 8.814s`、`internal/logic/legacymigrate 21.685s` |
| 6 | `DATABASE_DEFAULT_PASS=root go test -count=1 -run 'TestDistribution' ./test/e2e/` | 0 | PASS | 11 个用例全部 PASS（9.55s） |
| 7 | `DATABASE_DEFAULT_PASS=root go test -race -count=1 -run 'TestDistribution' ./test/e2e/` | 0 | PASS | 无 `DATA RACE`（61.65s） |
| 8 | `bash scripts/contract-check.sh` | 0 | PASS | 生成无漂移；150 条常驻 API + 4 条 planned、345 Schema、168 未引用组件（均已登记）；Spectral 无 error |
| 9 | `bash scripts/check-registry.sh` | 0 | PASS | 错误码 20001-20003 落在 RESERVED 域；migration Registry 一致 |

发布前基线缺陷验证（不作为交付门禁，仅作为 §2.3 证据）：

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| 在 `af512f9` 独立 worktree 执行 `go run ./tools/openapi-gen -repo . -check` | 1 | FAIL（9 项，见 §2.3） |

`TestDistribution*` 明细：

```text
--- PASS: TestDistributionBindReferral (0.98s)
--- PASS: TestDistributionBindReferralConcurrent (1.51s)
--- PASS: TestDistributionCommissionAccrual (0.73s)
--- PASS: TestDistributionCommissionPaidConcurrent (0.71s)
--- PASS: TestDistributionCommissionNoReferral (0.62s)
--- PASS: TestDistributionCommissionRateZero (0.74s)
--- PASS: TestDistributionCommissionUnpaidOrder (0.72s)
--- PASS: TestDistributionReversalOnRefund (0.82s)
--- PASS: TestDistributionReversalConcurrent (0.82s)
--- PASS: TestDistributionUserIsolation (0.86s)
--- PASS: TestDistributionAdminAccess (0.91s)
```

## 7. 关键业务不变量与对应测试

| 不变量 | 实现手段 | 测试 |
| --- | --- | --- |
| 一人一个上级（INV-D01） | `uk_user_id` 唯一 + 已绑定时不 UPDATE | `TestDistributionBindReferral`、`...Concurrent` |
| 禁止自荐（INV-D02） | `userID == referrerUserID → 20001` | `TestDistributionBindReferral` |
| 推荐人必须启用（INV-D03） | 绑定前 `users.status=1` 计数 | `TestDistributionBindReferral`（不存在 / 被禁用 → 20002） |
| 不可改绑（INV-D04） | 换人 → 20003；同人幂等 200 | `TestDistributionBindReferral` |
| 一单一佣金（INV-D06） | `uk_order_id` + 1062 视为已计提 | `TestDistributionCommissionAccrual`（重复支付）、`...PaidConcurrent`（并发支付） |
| 计佣口径正确（INV-D07/D08） | `base=total_amount`、`rate_bp` 快照、`floor(base×rate/10000)`、整数运算 | `TestDistributionCommissionAccrual`（5000×2 → base 10000、rate 500、amount 500） |
| 计佣时机（INV-D09） | 仅在 `Pay` 事务内且订单 `status=20` | `TestDistributionCommissionUnpaidOrder`（待支付/取消） |
| 无推荐人不计佣 | 读 `user_referrals` 为空即 no-op | `TestDistributionCommissionNoReferral` |
| 未配置比例不计佣 | `rate_bp=0` → no-op（不硬编码比例） | `TestDistributionCommissionRateZero` |
| 冲回只一次（INV-D11） | `WHERE status IN (10,20)` + `RowsAffected`；`uk_refund_id` | `TestDistributionReversalOnRefund`（重复执行）、`...Concurrent` |
| 冲回不超额（INV-D12） | `reversed_amount = commission_amount`（SQL 列引用） | 同上（断言 `reversed_amount == commission_amount == 原始佣金`） |
| 冲回可审计 | `refund_id`、`reversal_reason='refund'`、`reversed_at` | `TestDistributionReversalOnRefund` |
| 事务一致性 | 计佣/冲回与订单、退款同事务；失败整体回滚 | `...PaidConcurrent`（订单只到 20、佣金只 1 条）、`...ReversalConcurrent`（退款成功记录 1 条） |
| 数据归属（INV-D14） | 身份只取 `Principal.UserID`，无 `user_id` 归属入参 | `TestDistributionUserIsolation`（伪造 `user_id` 参数不越权、未登录 401） |
| 后台权限 | `RequirePermission` + seed 权限码 | `TestDistributionAdminAccess`（无权限 403、分权限放行、普通用户 token 403） |

## 8. 未验证事项

| 项 | 状态 | 原因/影响 |
| --- | --- | --- |
| 佣金结算 `10→20`（写 `settled_at`） | NOT_VERIFIED / 未实现 | 触发条件与结算语义未定义（§1.1 D-6），代码中无该路径，测试无法覆盖 |
| 后台佣金状态写操作 | NOT_VERIFIED / 未实现 | 无业务依据，按设计只提供只读能力（§1.1 D-7） |
| 邀请码/邀请链接绑定路径 | NOT_VERIFIED | Go 侧无邀请码/附件码基础设施，路径不存在（§1.1 D-5） |
| 部分退款/部分冲回 | NOT_VERIFIED | 现有退款只支持整单全额，未扩展（任务明确禁止自行扩展） |
| 真实支付网关回调触发计佣 | NOT_VERIFIED | 系统本身只有 Mock 支付（`POST /orders/:id/pay`），不存在支付回调链路 |
| 前端页面与前后端联调 | NOT_RUN | 任务范围外 |
| 多实例部署下的跨进程并发 | NOT_VERIFIED | 未起多实例；并发正确性依赖数据库唯一约束与条件更新（已单进程多 goroutine + 真实 MySQL 验证） |

## 9. 已知限制

1. **计佣比例是单一全局配置**，不支持按商品/活动/等级差异化费率；比例改动只影响之后支付的订单（历史佣金是快照）。
2. **默认关闭计佣**（`distribution.commission_rate_bp=0`）：未配置时上线不会产生任何佣金，需要在部署时显式配置。
3. **比例配置非法时支付会失败**（fail-closed）：`rate_bp>10000` 或负数会让计佣返回技术错误并回滚支付事务，
   这是刻意取舍（不接受口径不明的资金事实）。
4. **佣金与钱包完全隔离**：佣金状态推进到「已冲回/已结算」都不产生钱包流水，运营无法在钱包侧看到佣金余额。
5. **绑定不可逆**：一旦绑定，用户无法自助解绑或改绑；需要人工干预只能改库（本次未提供任何后台写接口）。
6. **下级列表展示用户名**：`GET /distribution/subordinates` 返回下级 `username`（登录名）与绑定时间，
   用于让推荐人识别自己邀请的用户；未返回昵称/手机号等更敏感字段。
7. **后台无可写能力**：无法人工补发、撤销或修正佣金，出错时只能通过数据库订正（需另立需求）。
8. **`/distribution/*` 路由使用既有 IAM 会话机制**：用户被禁用后即时失效（复用中间件），本模块未额外加频控。

## 10. 遗留问题

1. **B1-04-01 契约污染**：基线 `af512f9` 把地区字典/资料更新的 OpenAPI 提前提交但无 Go 实现，导致基线契约门禁失败。
   本任务已按生成器恢复一致性并删除孤儿 `openapi/paths/region.yaml`；**B1-04-01 合并时必须重新生成契约**，
   不要手工回填这些路径，否则会再次出现「虚构接口」。
2. **结算能力缺失**：`status=20` 目前没有任何代码路径可达，`settled_at` 恒为空。需要 Owner 定义结算触发条件
   （订单完成/售后期结束/人工确认）后再实现，并同步 `distribution.md` §3.3。
3. **佣金账户/提现**：若业务需要「可消费/可提现的佣金余额」，必须先设计佣金账户与流水（或明确复用钱包语义），
   并更新 `wallet.md`，不得直接把 `distribution_commissions` 当余额使用。
4. **邀请码体系**：若产品需要分享链接/邀请码，需要新增邀请码生成与解析能力（新表或新列 + migration version 预占），
   当前只有「提交上级 user id」。
5. **风控**：目前没有防刷（批量注册下级、自买自返）规则；如后续需要，应在绑定或计佣环节增加策略，
   并评估是否影响已生成佣金。
6. **后台报表**：暂未提供佣金趋势/导出，`idx_beneficiary_status`、`idx_status_created` 已预留支撑。
