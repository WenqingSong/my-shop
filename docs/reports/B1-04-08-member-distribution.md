# B1-04-08 会员等级与一级分销佣金 交付报告

结论：**PARTIAL**。
会员与分销两个后端模块的功能、事务、幂等、并发与文档已完整交付并通过真实 MySQL 验证；
但**基线提交 `af512f9` 本身就带着未实现的 OpenAPI 契约**（`/regions`、`PUT /me`、地址结构化地区字段），
本任务为让契约门禁可用而按真实 DTO/路由重新生成，因而移除了该批不存在于代码的契约内容（见 §7.1）。
该动作需要 Owner 知悉并确认，故整体结论标注 PARTIAL 而非 PASS。

## 1. Git 基线与分支

| 项 | 值 |
| --- | --- |
| 仓库根 | `/root/projects/my-shop` |
| 起点分支 | `origin/feat/p2-backend`（`git fetch` 后与 `git ls-remote` 一致，无本地同名分支） |
| 基线 SHA | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60`（"feat(openapi): 新增地区字典接口、用户资料更新与地址结构化地区字段"） |
| 开发分支 | `feat/b1-04-08-member-distribution` |
| Worktree | `/root/projects/my-shop-b1-04-08-member-distribution` |
| 实现提交 | `771fcd36e0c45d6952802dadfe6340969e0283af` |
| 报告提交 | 见本文件所在提交（`docs(report): B1-04-08 …`） |

基线检查结果：工作区干净（`git status --porcelain` 为空）、HEAD 与 `origin/feat/p2-backend` 同步、
目标分支与目标目录均不存在（无覆盖风险）。未从 `main` 创建，未在 `feat/p2-backend` 上开发，
未修改 `my-shop-b1-04-04-wallet` Worktree。**未执行任何 push / merge**。

## 2. 实际实现的功能

### 2.1 会员等级与会员权益（`internal/logic/member`）

- 等级配置：前台只读启用等级（`GET /members/levels`）、后台增删改查（含停用等级）。
- 等级配置校验：`level_no` 正整数、`name` 1~64 字符、`growth_threshold ≥ 0`、`discount_rate_bp ∈ [1,10000]`、
  `status ∈ {0,1}`；`level_no`/`growth_threshold` 唯一（DB 唯一键 + 1062 → 20004）；
  「`level_no` 升序时门槛严格递增」在写事务内锁全表校验（违反 → 20002 回滚）；
  删除被 `user_memberships` 引用的等级由 FK RESTRICT 拒绝 → 20003。
- 用户会员：当前等级、累计成长值、`upgraded_at`、下一等级进度（`GET /my/membership`）；一人一行。
- 成长值口径：**已支付订单实付金额（整数分）**，1 分消费 = 1 成长值；等级门槛同为整数分口径。
- 成长值流水：append-only，`growth_after = growth_before + change_amount`，`GET /my/membership/growth-logs` 分页（clamp）。
- 等级判定与升级：`levelForGrowth(g)` = 启用且门槛 ≤ g 的最高门槛等级；只升不降；等级停用不影响已入会用户权益。
- 会员折扣权益：`IMember.DiscountRateBp` 供下单算价；无档案时按成长值 0 解析。
- 订单计价联动：`orders.member_discount_amount` 独立快照列；
  `member_discount = min(floor(original × (10000-rate)/10000), original - coupon_discount)`，
  保证 `total = original - coupon - member ≥ 0`（不突破订单金额边界）。
- 支付/退款联动：支付成功同事务发放成长值；退款成功同事务按原发放额扣减（幂等、不降级、不为负）。

### 2.2 一级分销与佣金（`internal/logic/distribution`）

- 推荐关系绑定：`POST /referrals/bind`，被推荐人恒取 `Principal.UserID`；禁自荐（21003）、
  推荐人须存在且启用（21005）、已绑定不可改绑（同推荐人幂等成功 / 不同推荐人 21002）、
  插入后沿推荐人链遍历检测成环（21004 回滚）。
- 佣金计提：支付成功事务内 `AccrueForOrderInTx`，基数 = `original - coupon_discount - member_discount`（商品实收，不含运费），
  比例 `500bp`（V1 固定，写库快照），`floor(base × rate / 10000)`；一单一佣金（`uk_order_id`）；
  未支付 / 无推荐人 / 受益人等于买家 / 基数或佣金为 0 一律 no-op。
- 佣金结算：`POST /admin/commissions/:id/settle`，`10→20`；已结算重复调用幂等；已冲回返回 21007。
- 退款冲回：退款成功事务内 `ReverseForRefundInTx`，`status IN (10,20) → 30`，
  `reversed_amount` 直接取同额 `commission_amount`（不可能超额），同一退款单由 `uk_refund_id` 兜底。
- 查询：`GET /my/referral`、`GET /my/commissions`（clamp 分页 + 状态过滤）、`GET /my/commissions/summary`、
  `GET /admin/referrals`、`GET /admin/commissions`（strict 分页 + 白名单排序 + 时间范围）、`GET /admin/commissions/:id`、
  以及 `GET /admin/members/:user_id`。
- 边界：**不涉及钱包**（未调用 `DebitForOrderInTx`/`CreditForRefundInTx`，未改 `internal/logic/wallet`、
  未新增提现），佣金只是应收/已结算事实，不产生可消费余额。

### 2.3 跨模块接入点（明确标注集成冲突面）

| 文件 | 改动 | 冲突风险 |
| --- | --- | --- |
| `internal/logic/order/order.go` | `resolveMemberDiscount`；`Create` 计算并写 `member_discount_amount`；`Pay` 事务内调用成长值发放与佣金计提 | **高**：与 B1-04-03（订单金额明细/运费）同改 `orders` 金额计算与 `insertOrder` |
| `internal/logic/refund/refund.go` | `Execute` 成功事务内调用佣金冲回与成长值扣减 | **中**：与 B1-04-04（退款入账钱包）同改该事务 |
| `api/order/v1/order.go` | `Order` 增加 `member_discount_amount` | 中：与 B1-04-03 的订单 DTO 扩展同文件 |

## 3. 数据库变更

### 3.1 新增 Migration（1 个，additive）

| 版本 | 文件 | 内容 |
| --- | --- | --- |
| `20261110000015` | `order_member_discount.up.sql` / `.down.sql` | `orders` 增 `member_discount_amount INT UNSIGNED NOT NULL DEFAULT 0`（位于 `discount_amount` 之后），无回填 |

- up/down 成对；down 只 `DROP COLUMN`，不改动既有金额列与历史数据。
- Registry 已登记：`.agent/registry/migrations.md`（`RESERVED`，owner `B1-04-08`）。
- 复用既有 Schema：`member_levels`、`user_memberships`、`member_growth_logs`、`user_referrals`、
  `distribution_commissions` 全部沿用 B1-03-03 已交付结构，**未重复建表、未修改历史迁移**。

### 3.2 未新增的表

无。会员/分销 5 张表全部复用 B1-03-03。

## 4. API 与错误码

### 4.1 新增路由（16 条，常驻路由 142 → 158）

前台（`Auth`）：

| 方法 | 路径 |
| --- | --- |
| GET | `/members/levels` |
| GET | `/my/membership` |
| GET | `/my/membership/growth-logs` |
| POST | `/referrals/bind` |
| GET | `/my/referral` |
| GET | `/my/commissions` |
| GET | `/my/commissions/summary` |

后台（`AdminAuth` + `RequirePermission`）：`GET /admin/member-levels`（`member:list`）、`POST /admin/member-levels`（`member:create`）、
`PUT /admin/member-levels/:id`（`member:update`）、`DELETE /admin/member-levels/:id`（`member:delete`）、
`GET /admin/members/:user_id`（`member:view`）、`GET /admin/referrals`（`distribution:list`）、
`GET /admin/commissions`（`distribution:list`）、`GET /admin/commissions/:id`（`distribution:view`）、
`POST /admin/commissions/:id/settle`（`distribution:settle`）。

### 4.2 新增权限码（8 个，seed 49 → 57）

`member:list`、`member:view`、`member:create`、`member:update`、`member:delete`、
`distribution:list`、`distribution:view`、`distribution:settle`（`internal/boot/seed.go`）。

### 4.3 新增错误码（12 个）

域 `20000-20999`（会员，`RESERVED`）：`20001` 等级不存在、`20002` 配置非法/破坏单调性、`20003` 等级被引用、
`20004` 序号或门槛重复、`20005` 成长值不足。

域 `21000-21999`（分销，`RESERVED`）：`21001` 关系不存在、`21002` 已绑定不可改绑、`21003` 自荐、
`21004` 成环、`21005` 推荐人无效、`21006` 佣金不存在、`21007` 佣金状态迁移非法。

编号来源：`.agent/registry/error-codes.md` 当前最高域序为 19（`19000-19999` coupon-v1），
按 `domain_seq_next = max+1` 取 20 / 21；已在本分支 Registry 登记为 `RESERVED`。
**该两条 Reservation 仍需 Owner 通过独立 Registry 变更合入共享分支后才算生效**。

### 4.4 OpenAPI

- 生成方式：`go run ./tools/openapi-gen -repo .`（未手工编辑生成物）。
- 新增分域视图：`openapi/paths/member.yaml`、`openapi/paths/distribution.yaml`。
- 生成器元数据同步：业务域与 Schema 名（`tools/openapi-gen/model.go`）、错误码域前缀（`parse_codes.go`）、
  分页模式（`opmeta.go`，前台 clamp / 后台 strict）、API 数量基线（`gen.go` 158/74/82）、
  生成器单测基线（`main_test.go`）、未引用请求 DTO 白名单（`unused-components-allowlist.yaml`）。

## 5. 文档变更

设计文档（均按真实实现重写/补写，不是只加一句「已实现」）：

| 文件 | 变化 |
| --- | --- |
| `docs/design/member.md` | 重写：职责边界、数据模型（含新列）、成长值口径、等级判定与升级、折扣公式与叠加顺序、事务边界与幂等键、INV-M01~M16、API 与权限、错误码域、Migration、Deferred |
| `docs/design/distribution.md` | 重写：绑定语义与并发边界、计佣公式与基数冻结、状态机、退款冲回、INV-D01~D12、API 与权限、错误码域、Deferred |
| `docs/design/pricing.md` | §5 从「Deferred」改为「已实现」（公式、取整、夹紧、叠加顺序）；§8 现行应付公式；§10 历史兼容行；INV-P04 扩展；§12 Deferred 收敛 |
| `docs/design/order.md` | 新增列与四列金额快照语义、状态机备注、INV-003b 支付事务边界、INV-004 扩展、§8 跨模块（会员/分销）、Migration 行 |
| `docs/design/refund.md` | 状态机备注、INV-009 扩展、成功语义、§8 跨模块（会员/分销） |
| `docs/design/error-codes.md` | 域分配表 +2 行（20/21，RESERVED）、兼容性范围 1000-21999、§8 索引补 member/distribution |
| `docs/design/architecture.md` | 模块表 +2 行（会员/一级分销）、订单流程付款/退款侧联动、计价与一致性说明、文档索引 |
| `docs/design/migration.md` | 迁移表 +1 行（`20261110000015`） |

开发知识文档（新增）：

- `docs/dev/member.md`：模块定位、代码地图、3 条核心调用链、关键数据结构、不变量、事务/锁/幂等表、
  API 与错误码、测试地图、常见问题、维护注意事项。
- `docs/dev/distribution.md`：同上结构（4 条调用链，含绑定/计佣/冲回/结算）。

Registry：

- `.agent/registry/error-codes.md`：`20000-20999`、`21000-21999` 两条 `RESERVED`。
- `.agent/registry/migrations.md`：`20261110000015` `RESERVED`。

## 6. 测试命令、退出码与真实结果

测试环境：真实 MySQL 8.0.46（`127.0.0.1:3306`）+ 真实 Redis（`127.0.0.1:6379`），
使用本任务专用库 `my_shop_b10408_test`（与其它 Worktree 的 `my_shop_b104_test` 隔离）与 Redis DB 9。

| # | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| 1 | `gofmt -l .` | 0 | PASS（输出为空） |
| 2 | `go build ./...` | 0 | PASS |
| 3 | `go vet ./...` | 0 | PASS |
| 4 | `go test -p 1 ./...`（`DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop_b10408_test REDIS_DEFAULT_DB=9`） | 0 | PASS（全量包通过，无 FAIL） |
| 5 | `go test -race -p 1 ./internal/logic/member/ ./internal/logic/distribution/ ./internal/logic/order/ ./internal/controller/member/` | 0 | PASS（4 包全绿，无 DATA RACE） |
| 6 | `bash scripts/contract-check.sh` | 0 | PASS（158 条路由双向一致；生成无漂移；Spectral 无 error；生成器单测通过；NOT RUN 0 项） |
| 7 | `bash scripts/check-registry.sh` | 0 | PASS（无区间重叠/版本重复；新码落在 RESERVED 域内，仅 INFO 提示） |
| 8 | `bash scripts/test-race.sh` | 1 | **PARTIAL**：前 4 段（熔断器、F-03 结果快照、B4 装配/生命周期、T2-02 Consumer Group、HTTP drain）全部 `ok`（6 个 ok 行）；最后一段 Outbox Race 因「需要 compose 管理的 MySQL」直接失败 → 该段 **NOT_RUN** |
| 9 | `go test -race -p 1 ./...` | 124（`timeout 900`） | **NOT_RUN**：900s 预算内跑完 33 个包且 0 失败，但未覆盖全部包即超时；不作为 PASS |
| 10 | `bash scripts/test-integration.sh` | 0 | PASS（内部即第 4 条命令） |

新增测试（22 个用例，全部真实 MySQL，无 Mock 替换事务/约束）：
会员 8（`internal/logic/member/member_test.go`）、
分销 7（`internal/logic/distribution/distribution_test.go`）、
订单链路 3（`internal/logic/order/member_distribution_chain_test.go`）、
HTTP 层 3（`internal/controller/member/member_test.go`）、
迁移 2（`internal/migrations/order_member_discount_migration_test.go`）。

覆盖对照任务要求：

- 会员：等级查询 ✅、成长值增加 ✅、成长值扣减 ✅、流水一致性 ✅、重复事件幂等 ✅、
  升级边界（跨级、已达顶、未达门槛）✅、折扣计算 ✅、并发成长值变更（8 并发）✅。
- 分销：正常绑定 ✅、自我推荐拒绝 ✅、循环推荐拒绝（2 环 + 3 环）✅、重复绑定（幂等/拒绝改绑）✅、
  正常计佣 ✅、同订单重复佣金拒绝 ✅、退款冲回 ✅、同退款重复冲回 ✅、
  部分退款边界 → **N/A**（V1 仅整单全额退款，设计文档已声明）、并发佣金生成（6 订单 + 6 并发同订单）✅。

## 7. 未验证项与剩余问题

### 7.1 基线契约漂移（需 Owner 决策）

基线提交 `af512f9` 只改了 `openapi/` 与 inventory 文档，**没有对应 Go 代码**：

- `openapi/paths/region.yaml`（`GET /regions`、`GET /regions/{id}`）：仓库中不存在 `api/region`，
  任何分支都没有该包（已用 `git cat-file` 遍历本地/远程分支确认）；
- `openapi/paths/iam.yaml` 的 `PUT /me`：`routes_frontend.go` 无该路由，`internal/logic/iam` 无 `UpdateMe`；
- `openapi/paths/address.yaml` 的 `province_id`/`city_id`/`district_id`：`api/address/v1` 无这些字段，
  且 `docs/design/address.md` 明确写「`addresses` 不新增 `region_id`、结构化地区接入属于后续任务」。

在上述基线上执行 `bash scripts/contract-check.sh` 会失败（已在 `af512f9` 的临时 Worktree 中复现：
「生成器重写了已提交的契约文件」+「分域文件 region.yaml 存在路径 /regions，但主文件缺少该路径」+ 生成器单测失败）。

本任务的处理：**按真实 DTO/路由重新生成契约**（生成器是 openapi 的唯一写入方，禁止手工编辑），
因此上述三处虚构契约被移除，`openapi/paths/region.yaml` 被删除。契约门禁随之转绿。
影响与建议：

- 若这批契约是 B1-04-01（用户资料与地区地址）的「契约先行」产物，则该任务实现时应通过生成器重新产出
  （`api/region` 包 + DTO + 路由 + seed 权限码），而不是回退本次生成结果。
- 恢复方式：`git checkout af512f9 -- openapi/`（但会同时把契约门禁重新变红）。
- **本任务未实现** `/regions`、`PUT /me`、地址结构化地区字段——这三项不属 B1-04-08 范围。

### 7.2 其他未验证 / 未完成

| 项 | 状态 | 说明 |
| --- | --- | --- |
| 佣金比例可配置 | NOT_IMPLEMENTED | V1 固定常量 `500bp`，配置来源仍 Deferred（`distribution.md` §9） |
| 自动结算（订单完成触发） | NOT_IMPLEMENTED | 仅后台人工结算触发 `10→20`（状态机注释已声明） |
| 会员等级 seed | NOT_IMPLEMENTED | 平台未配置等级时成长值发放 no-op、折扣为原价；`docs/design/member.md` §12 已记录 |
| 退款入账钱包 / 余额支付 | NOT_IMPLEMENTED | 属 B1-04-04；本任务未调用任何钱包接口 |
| 积分抵扣/返积分 | NOT_IMPLEMENTED | 属 B1-04-05；本任务未读写积分账本 |
| `order_status_logs` 写入 | NOT_IMPLEMENTED | 既存 gap，本任务未改动（未扩大） |
| 长链并发成环（≥4 边同时提交） | NOT_VERIFIED | 见 `distribution.md` §2.2「并发边界」；已实现两用户锁 + 插入后环检测 |
| 全量 `go test -race ./...` | NOT_RUN | 900s 超时（33 包 0 失败）；已对改动包单独跑 race |
| `scripts/test-race.sh` 的 Outbox 段 | NOT_RUN | 需要 compose 管理的 MySQL，本环境 MySQL 是独立容器 |
| `make test`（完整门禁） | NOT_RUN | 内含容器/K8s/Nginx/Kustomize 等与本次变更无关的门禁；已逐项替代执行第 1-7 项 |
| 前端/联调 | OUT_OF_SCOPE | 本任务只做后端 |

## 8. 后续集成要求

1. **错误码与迁移 Reservation**：`20000-20999`、`21000-21999`、`20261110000015` 目前仅在本分支 Registry 登记为
   `RESERVED`。按项目规则需通过独立 Registry 变更合入共享分支后才算有效预留；合并时应把状态改为 `ACTIVE`。
2. **与 B1-04-03（订单金额明细与运费）**：双方都改 `orders` 金额计算与 `api/order/v1`。本任务已占用
   `orders.member_discount_amount`；B1-04-03 若另行新增 `member_discount`（不同列名）会产生**双列/命名冲突**，
   合并时需统一为一列，并把 `resolveMemberDiscount` 与运费公式组合进同一处算价，同时更新 `pricing.md` 与金额守恒测试。
3. **与 B1-04-04（钱包/余额支付/退款入账）**：本任务在 `refund.Execute` 成功事务内新增两步（佣金冲回、成长值扣减），
   钱包分支若在同一事务内加入退款入账，需保证步骤顺序与失败整体回滚；本任务**未调用**任何钱包 API。
4. **与 B1-04-05（积分）**：会员折扣与积分抵扣会在同一个 `total_amount` 上叠加；届时必须重新确认三项优惠的叠加顺序、
   上限与舍入，并更新 `pricing.md`、`order.md` INV-004 与金额守恒测试。
5. **与 B1-04-01（用户资料与地区地址）**：需重新生成被移除的 region/updateMe/地址结构化地区契约（见 §7.1）。
6. **分销比率**：若后续改为可配置，必须保留 `rate_bp` 快照语义，不得回写历史佣金。

## 9. 最终 Git 状态

- 分支 `feat/b1-04-08-member-distribution`，基于 `af512f9`，实现提交 `771fcd3`，报告单独提交。
- 工作区在提交后为空（`git status --porcelain` 无输出）。
- **未 push、未 merge、未合并 main、未部署**。
- 未触碰 `my-shop-b1-04-04-wallet` Worktree；未 cherry-pick 任何分支。

最终状态：**PARTIAL**（功能与测试完整；整体受 §7.1 基线契约修正需 Owner 确认所限）。
