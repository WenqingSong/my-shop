# B1-03-03 营销与关系模型 Schema 交付报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-03-03：营销与关系模型 Schema |
| 分支 | `feat/p2-backend` |
| 基线 SHA | `6ea2e17e9afb63820143c5428566ab2734d3c3e4`（B1-03-02 最终 HEAD，与任务书一致） |
| 实现/测试/设计文档提交 | `85dea0fe753d90c72885427530ec8318cded4fa8` |
| 本报告提交 | 本次提交（推送后为最终 HEAD） |
| 验证库 | 真实 MySQL `8.0.46`（隔离容器，库 `my_shop`，`utf8mb4_unicode_ci`）+ Redis 7 |
| 执行日期 | 2026-11-10 |

## 1. 任务目标与范围

在现有 Go 单体后端补齐营销与用户关系相关**数据库模型**：优惠券批次/范围/折扣类型/每人限领、拼团、砍价、
会员等级/成长值、一级分销推荐关系与订单佣金；同步长期设计文档，为 B1-04 业务实现提供可靠结构。

**本次只做**：表/字段、索引、唯一约束、外键、Migration Up/Down、真实 MySQL 约束测试、设计文档、交付报告。

**本次不做**：Controller/Logic/Service、业务 API、DTO/路由/OpenAPI、历史 SQL 导入、秒杀业务与库存扣减改造、
真实支付、前端、拼团/砍价/会员/分销的实际业务流程。

### 1.1 开工前核对

| 项 | 结果 |
| --- | --- |
| 当前分支 | `feat/p2-backend` |
| 基线 HEAD | `6ea2e17e9afb63820143c5428566ab2734d3c3e4`（`docs(report): B1-03-02 交付报告`） |
| 与远端同步 | `origin/feat/p2-backend` 与本地 `0 ahead / 0 behind` |
| 工作区 | 仅 3 个未跟踪的上一轮分析文档（`docs/phase2/B1-03_*.md`），本次未修改、未提交 |
| 当前 DB 版本 | `20261110000009 (dirty=false)`，新迁移从 `20261110000010` 接续 |

### 1.2 参考项目审查结论

| 参考 | 结论 |
| --- | --- |
| Java 后端 `tmp/eshop-backend-java` | **仅部分可用**：存在 147 个 `.java`，主要是 Controller/Param/少量 Domain/Service 接口；拼团、砍价、优惠券、会员等级、分销的 Service 实现缺失。可用事实仅限 REST 路径、请求参数与少量校验，不能据此虚构业务实现。 |
| `tmp/shopping.sql` | 可用：提取了 `store_coupon*`、`store_combination`、`store_pink`、`store_bargain*`、`system_user_level`、`user_level`、`system_user_task`、`user_task_finish`、`eshop_user`、`user_bill`、`user_extract`、`store_order` 的 DDL 与少量配置数据；未导入任何历史数据。 |

从参考中提取并重新设计的点：

- 金额一律从历史 `decimal(8,2)` 元改为**整数分**；不复制 `decimal`、`is_del`、`mer_id`、运费模板、装修字段等与本项目无关列。
- 历史 `store_coupon_issue` 是「领取活动」而非严格批次；本次改为 `coupon_batches`，保留模板/批次双层配额，
  不复制 `remain_count` 冗余列。
- 历史 `store_coupon.product_id` 是逗号分隔字符串；本次改为 `coupon_template_scopes` 行式模型 + 物理 FK。
- 历史 `store_pink.k_id` 是「团长 0/非 0」的旧表达；本次用 `group_buy_teams.leader_user_id` +
  `group_buy_participants` 表达团长/成员关系。
- 历史 `store_bargain_user` 无并发唯一约束；本次增加 `active_key` 唯一键，限制同一用户同一活动仅一个进行中实例。
- 历史 `eshop_user.spread_uid` 把推荐关系塞在用户表；本次独立 `user_referrals`，不改 `users`。
- 历史 `eshop_user.integral/level` 把积分与等级塞在用户表；本次成长值与积分分账本，等级独立配置。

## 2. 新增与修改的表结构

### 2.1 优惠券

**修改 `coupon_templates`（3 个 additive 列）**

| 列 | 类型 | 默认 | 语义 |
| --- | --- | --- | --- |
| `discount_type` | TINYINT | 1 | 1=固定金额满减；2=折扣率（结构预留） |
| `discount_rate_bp` | INT UNSIGNED | NULL | 折扣率万分比（9000=9 折），仅 type=2 使用 |
| `per_user_limit` | INT UNSIGNED | 1 | 每人限领数量；0=不限；默认 1 与既有语义一致 |

既有 `total_quantity`/`issued_quantity`/领取与使用时间窗/`status` 语义不变；模板仍是券权益的唯一事实来源。

**新增 `coupon_batches`（发行批次）**

`id`、`template_id`（FK → `coupon_templates` RESTRICT）、`total_quantity`、`issued_quantity`（默认 0）、
`claim_start_time`、`claim_end_time`、`status`（默认 1）、`created_at`/`updated_at`。

**新增 `coupon_template_scopes`（适用范围）**

`id`、`template_id`（FK CASCADE）、`product_id`（可空，FK CASCADE）、`category_id`（可空，FK CASCADE）、`created_at`。
无范围行 = 平台通用券；有行 = 仅限所列商品/分类；`product_id`/`category_id` 恰有一个非空（应用层保证）。

**修改 `user_coupons`（2 个 additive 列 + 唯一键替换）**

| 列 | 类型 | 默认 | 语义 |
| --- | --- | --- | --- |
| `batch_id` | BIGINT UNSIGNED | NULL | 领取批次（软引用，无 FK）；NULL=历史券/直接发放 |
| `claim_no` | INT UNSIGNED | 1 | 该用户在该模板下的第几次领取（从 1 开始） |

`uk_user_template(user_id, template_id)` 替换为 `uk_user_template_claim(user_id, template_id, claim_no)`；
新增 `idx_batch_id`。既有四列权益快照（`coupon_title`/`coupon_price`/`use_min_price`/`end_time`）未改动。

### 2.2 拼团（3 张新表）

- `group_buy_activities`：`product_id`/`sku_id`（FK RESTRICT）、`name`、`group_price`、`group_size`、
  `team_ttl_minutes`、`start_time`/`end_time`、`status`（默认 1）。
- `group_buy_teams`：`activity_id`（FK RESTRICT）、`leader_user_id`、`group_price`（开团快照）、
  `required_size`（开团快照）、`joined_count`（默认 1）、`status`（默认 10）、`expire_at`、`closed_at`。
- `group_buy_participants`：`team_id`（FK RESTRICT）、`user_id`、`order_id`（可空软引用）、
  `status`（默认 10）、`created_at`（参团时间）。

### 2.3 砍价（3 张新表）

- `bargain_activities`：`product_id`/`sku_id`（FK RESTRICT）、`name`、`start_price`、`floor_price`、
  `help_amount`、`max_help_count`、`start_time`/`end_time`、`status`（默认 1）。
- `bargain_instances`：`activity_id`（FK RESTRICT）、`user_id`、`start_price`/`floor_price`/`current_price`、
  `helped_count`（默认 0）、`max_help_count`、`status`（默认 10）、`expire_at`、`completed_at`、
  `order_id`（可空软引用）、`active_key`（生成列，见 §3）。
- `bargain_help_records`：`instance_id`（FK RESTRICT）、`helper_user_id`、`cut_amount`、
  `price_before`/`price_after`、`created_at`。

### 2.4 会员（3 张新表，不动积分账本）

- `member_levels`：`level_no`、`name`、`growth_threshold`（默认 0）、`discount_rate_bp`（默认 10000）、
  `status`（默认 1）。
- `user_memberships`：`user_id`、`level_id`（FK RESTRICT）、`growth_value`（默认 0）、`upgraded_at`。
- `member_growth_logs`：`user_id`、`change_amount`（带符号）、`growth_before/after`、`change_type`、
  `biz_type`/`biz_id`、`idempotency_key`、`remark`、`created_at`。

`user_points_accounts`/`user_points_logs` 未修改、未复制；积分与成长值是独立账本。

### 2.5 一级分销（2 张新表）

- `user_referrals`：`user_id`（唯一）、`referrer_user_id`、`created_at`（绑定时间）。
- `distribution_commissions`：`order_id`（FK → `orders` RESTRICT）、`buyer_user_id`、`beneficiary_user_id`、
  `base_amount`、`rate_bp`、`commission_amount`、`status`（默认 10）、`settled_at`、`reversed_amount`（默认 0）、
  `reversed_at`、`refund_id`、`reversal_reason`、`created_at`/`updated_at`。

## 3. 关键索引及约束

| 表 | 唯一约束 | 索引 | 外键/删除策略 |
| --- | --- | --- | --- |
| `coupon_templates` | `PRIMARY` | — | — |
| `coupon_batches` | `PRIMARY` | `idx_template_status(template_id,status)` | `template_id` → `coupon_templates` RESTRICT |
| `coupon_template_scopes` | `uk_template_product(template_id,product_id)`、`uk_template_category(template_id,category_id)` | `idx_product_id`、`idx_category_id` | `template_id`/`product_id`/`category_id` → 各自父表 CASCADE |
| `user_coupons` | `uk_user_template_claim(user_id,template_id,claim_no)` | `idx_user_id`、`idx_order_id`、`idx_batch_id` | 无 FK（既有约定） |
| `group_buy_activities` | `PRIMARY` | `idx_product_id`、`idx_sku_id`、`idx_status_time(status,start_time,end_time)` | `product_id`/`sku_id` RESTRICT |
| `group_buy_teams` | `PRIMARY` | `idx_activity_status(activity_id,status)`、`idx_leader_user`、`idx_status_expire(status,expire_at)` | `activity_id` RESTRICT |
| `group_buy_participants` | `uk_team_user(team_id,user_id)`、`uk_order_id(order_id)` | `idx_user_time(user_id,created_at)` | `team_id` RESTRICT |
| `bargain_activities` | `PRIMARY` | `idx_product_id`、`idx_sku_id`、`idx_status_time` | `product_id`/`sku_id` RESTRICT |
| `bargain_instances` | `uk_active_instance(active_key)`、`uk_order_id(order_id)` | `idx_activity_id`、`idx_user_status`、`idx_status_expire` | `activity_id` RESTRICT |
| `bargain_help_records` | `uk_instance_helper(instance_id,helper_user_id)` | `idx_helper_time(helper_user_id,created_at)` | `instance_id` RESTRICT |
| `member_levels` | `uk_level_no(level_no)`、`uk_growth_threshold(growth_threshold)` | — | — |
| `user_memberships` | `uk_user_id(user_id)` | `idx_level_id` | `level_id` → `member_levels` RESTRICT |
| `member_growth_logs` | `uk_idempotency(idempotency_key)` | `idx_user_time`、`idx_biz` | 无 FK |
| `user_referrals` | `uk_user_id(user_id)` | `idx_referrer_user(referrer_user_id,created_at)` | 无 FK |
| `distribution_commissions` | `uk_order_id(order_id)`、`uk_refund_id(refund_id)` | `idx_beneficiary_status`、`idx_status_created` | `order_id` → `orders` RESTRICT |

`bargain_instances.active_key` 定义：
`VARCHAR(64) GENERATED ALWAYS AS (CASE WHEN status = 10 THEN CONCAT(activity_id, ':', user_id) ELSE NULL END) STORED`。
它保证「同一用户同一活动至多一个进行中实例」；失败/过期后变为 NULL，允许重新发起（与 `refund_requests.active_key` 同一模式）。

## 4. 关键业务不变量

| 编号 | 不变量 | DB 保证 |
| --- | --- | --- |
| INV-C01 | 同一用户同一模板默认至多一张券 | `uk_user_template_claim` + `claim_no DEFAULT 1`（既有语义不变） |
| INV-C02 | 模板发放总量不超发 | 既有 `issued_quantity < total_quantity` 条件更新（B1-04 不变） |
| INV-C12 | 批次配额不超发 | `coupon_batches.issued_quantity < total_quantity` 条件更新 + RowsAffected |
| INV-C13 | 批次与模板时间窗同时生效 | 应用层双闸门；批次不延长模板窗口 |
| INV-C14 | 每人限领 | `claim_no` 从 1 递增 + `per_user_limit` 校验 + 唯一键兜底 |
| INV-C15 | 适用范围 | `coupon_template_scopes` 行 + 两个唯一键；无行=通用 |
| INV-GB01 | 团价格/人数不随活动漂移 | `group_buy_teams.group_price`/`required_size` 快照 |
| INV-GB02 | 参团人数 ≤ 成团人数 | `joined_count < required_size` 条件更新 + `INT UNSIGNED` |
| INV-GB03 | 一人在一团一次 | `uk_team_user` |
| INV-GB04 | 一单一团一次 | `uk_order_id` |
| INV-GB05 | 团长必须有参与记录 | 应用事务（DB 不设冗余 `is_leader`） |
| INV-GB06 | 团状态机 | 仅 10→20/30/40 合法；`closed_at` 只写一次 |
| INV-BG01 | 实例价格/次数/截止时间快照 | 实例列快照 |
| INV-BG02 | 同一用户同一活动仅一个进行中实例 | `uk_active_instance` |
| INV-BG03 | 一单一实例 | `uk_order_id` |
| INV-BG04 | 同一用户对同一实例只助力一次 | `uk_instance_helper` |
| INV-BG05 | 助力次数上限 | `helped_count < max_help_count` 条件更新 |
| INV-BG06 | 价格不穿透底价 | `GREATEST(floor_price, ...)` 条件更新 + `INT UNSIGNED` |
| INV-M06~M09 | 等级序号/门槛唯一、一人一条会员、等级引用有效 | `uk_level_no`、`uk_growth_threshold`、`uk_user_id`、FK RESTRICT |
| INV-M10~M12 | 成长值非负/可追溯/幂等/与等级同事务 | `BIGINT UNSIGNED`、`member_growth_logs`、`uk_idempotency` |
| INV-D01~D04 | 一人一个推荐人、不可自荐、只做一级 | `user_referrals.uk_user_id` + 应用校验 |
| INV-D05 | 一单一佣金、防止重复计提 | `distribution_commissions.uk_order_id` |
| INV-D06~D07 | 佣金金额快照、非负 | `base_amount`/`rate_bp`/`commission_amount` + `INT UNSIGNED` |
| INV-D09 | 退款冲回一次 | `status IN (10,20)` 条件更新 + `uk_refund_id` |

完整语义与状态机见 `docs/design/coupon.md`、`marketing.md`、`member.md`、`distribution.md`。

## 5. Migration 版本

```text
20261110000010_coupon_batch
20261110000011_group_buy
20261110000012_bargain
20261110000013_member
20261110000014_distribution
```

- 版本号接续既有最大 `20261110000009`，14 位时间戳、严格递增、无重复。
- 每组均提供 Up/Down；Down 只删除本组新增表/列/索引，破坏性（数据丢失）在文件顶部说明。
- `coupon_batch` 的 Down 为恢复 `uk_user_template`，会先删除 `claim_no > 1` 的多领记录（已注释说明，仅测试库/确认可丢弃数据时使用）。
- 未修改任何既有 Migration；`internal/migrations/migrations.go` 未改。

## 6. 历史兼容与设计决策

| 对象 | 决策 |
| --- | --- |
| 优惠券历史券 | `batch_id` 保持 NULL、`claim_no` 取默认 1；四个权益快照列仍为 NULL，读取方回退模板；不伪造批次/快照 |
| 既有领取去重 | `uk_user_template` → `uk_user_template_claim`，默认 `claim_no=1`，同用户同模板第二次领取仍 1062；既有测试不变 |
| 模板/批次配额 | 模板计数语义不变；批次是第二层闸门，从批次领取时模板计数仍 +1 |
| 拼团/砍价与库存 | 不建活动库存列、不读写 `inventories`/`inventory_logs`/`flash_sale_*`；成交仍走现有库存链路 |
| 拼团/砍价与订单 | 不新增 `orders.*_id` 列；通过参与记录/实例的 `order_id` 反查，避免破坏既有订单金额与状态语义 |
| 会员与积分 | 成长值独立于 `user_points_*`；不建立未论证的积分→成长值换算 |
| 分销与用户表 | 推荐关系独立 `user_referrals`，不把 `spread_uid` 塞回 `users` |
| 佣金与钱包 | 佣金记录是应收/已结算事实，不直接写钱包；已入钱包后的追偿属 Deferred |

## 7. 测试命令、退出码与真实结果

环境：真实 MySQL 8.0.46 + Redis 7（Docker 隔离容器），`DATABASE_DEFAULT_PASS=root` 显式注入；未用 Mock 冒充数据库约束验证。

| # | 命令 | 退出码/结果 | 状态 |
| --- | --- | --- | --- |
| 1 | `gofmt -l internal/ tools/` | 无输出 | PASS |
| 2 | `go build ./...` | exit 0 | PASS |
| 3 | `go vet ./...` | exit 0 | PASS |
| 4 | `bash scripts/test-unit.sh` | exit 0，全部通过 | PASS |
| 5 | `DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 -run '<8 个新用例>' ./internal/migrations/ -v` | exit 0；8/8 PASS；`ok ... 50.077s` | PASS |
| 6 | `DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./internal/migrations/` | exit 0；`ok ... 279.933s` | PASS |
| 7 | `DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./internal/...` | exit 0；27 个包 `ok`，32 个包无测试文件；migrations `299.450s` | PASS |
| 8 | `DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 -run 'TestOrder\|TestRefund\|TestCoupon\|TestFlashSale' ./test/e2e/ -v` | exit 0；62 PASS / 0 FAIL（Order 13 / Refund 13 / Coupon 16 / FlashSale 20）；`ok ... 44.430s` | PASS |
| 9 | `DATABASE_DEFAULT_PASS=root go run . migrate version` | `version: 20261110000014 (dirty=false)` | PASS |
| 10 | `DATABASE_DEFAULT_PASS=root go run . migrate up` | `没有待执行的 migration` | PASS |

新增迁移测试（`internal/migrations/marketing_schema_migration_test.go`，全部 PASS）：

| 用例 | 验证内容 |
| --- | --- |
| `TestMarketingSchemaMigrationUpDownUp` | Up → Down 到 `20261110000009` → Up；13 张新表/新列/唯一键消失后恢复；多领券按声明裁剪；既有订单/钱包/积分/秒杀数据不变 |
| `TestCouponBatchAndClaimConstraints` | 模板默认折扣类型/限领；批次 FK；批次配额条件更新售罄；`claim_no` 1/2 与重复 1062；历史默认 1 的重复领取语义；范围唯一与 FK |
| `TestGroupBuyTeamParticipantConstraints` | 团队容量条件更新、满员 0 行；`uk_team_user`/`uk_order_id`；NULL 订单可多条；无效团 1452；活动 RESTRICT 1451 |
| `TestBargainHelpDuplicateConstraints` | 进行中实例唯一；失败后重开；助力重复 1062；无效实例 1452；价格不穿透底价、无符号下溢 1690；实例 RESTRICT 1451 |
| `TestMemberLevelAndGrowthConstraints` | 等级序号/门槛唯一；会员唯一；等级 FK/RESTRICT；成长值负向 1690；流水幂等 1062；与积分账本独立 |
| `TestReferralUniqueness` | 一个用户一个推荐人；同一推荐人可绑定多个下级 |
| `TestDistributionCommissionIdempotency` | 一单一佣金 1062；退款单唯一 1062；NULL 退款单可多条；冲回只生效一次；订单 FK/RESTRICT |
| `TestMarketingMigrationFilesArePaired` | 5 个版本 Up/Down 成对、严格递增、均大于上一批次 |

`TestSchemaStructureMatchesBaseline` 对 `expectedSchema` 登记的 45 张关键业务表（含本批次 13 张新表与 2 张修改表）
逐列/逐索引校验列顺序、类型、默认值、索引、引擎与字符集，已随第 6 项通过；`businessTables` 的 60 张表存在性由
`TestUpCreatesSchemaAndIsIdempotent` 等用例覆盖。

## 8. 未验证事项

| 项 | 状态 | 原因 |
| --- | --- | --- |
| MySQL 5.7 兼容性 | NOT_VERIFIED | 验证库为 8.0.46；生成列/唯一索引语法 5.7 支持，但未实测 |
| 生产/预发库实际 schema 差异 | NOT_VERIFIED | 无生产库访问权限，仅隔离测试库 |
| 优惠券批次领取的业务集成 | NOT_VERIFIED | `Claim` 未修改，批次/限领逻辑属 B1-04 |
| 拼团/砍价状态扫描与并发业务链路 | NOT_VERIFIED | 业务代码未实现；仅验证 DB 条件更新/唯一键语义 |
| 佣金计算、结算与退款冲回集成 | NOT_VERIFIED | 业务代码未实现；仅验证表/唯一键/状态字段 |
| 历史 `shopping.sql` 导入与金额换算 | NOT_RUN | 本任务明确不做历史数据导入 |
| `make test` / `scripts/test.sh` 全量门禁 | NOT_RUN | 含 Registry/容器/K8s 断言，本任务不执行旧 Workflow Gate |
| 容器镜像构建、K8s 部署、压测 | NOT_RUN | 超出 Schema 任务范围 |
| 前端/OpenAPI/DTO 联调 | NOT_RUN | 本任务不新增 API |

## 9. Git 基线与最终提交

| 项 | 值 |
| --- | --- |
| 基线 | `6ea2e17e9afb63820143c5428566ab2734d3c3e4` |
| 实现/测试/设计文档提交 | `85dea0fe753d90c72885427530ec8318cded4fa8`（19 files, +2305/-57） |
| 本报告提交 | 本次提交；推送后最终 HEAD 以 `git rev-parse HEAD` 与 `git ls-remote origin feat/p2-backend` 一致为准 |
| 推送 | `origin/feat/p2-backend`，fast-forward，无 force push，未合 main |
| 未跟踪分析文档 | `docs/phase2/B1-03_SCHEMA_DISCOVERY.md`、`B1-03_REFERENCE_REAUDIT.md`、`B1-03_FINAL_MIGRATION_AUDIT.md` 未修改、未提交 |

## 10. 文档变更清单

| 文档 | 变更 |
| --- | --- |
| `docs/design/coupon.md` | 模板三列、批次、范围、`claim_no` 唯一键替换、每人限领、批次/范围不变量（INV-C12~C15）、B1-04 未实现状态 |
| `docs/design/marketing.md`（新增） | 拼团/砍价模型、状态机、容量与助力并发、库存/订单/秒杀边界、INV-GB/BG |
| `docs/design/member.md` | 会员等级/用户会员/成长值流水；积分与成长值分离论证；升级数据关系；INV-M06~M12 |
| `docs/design/distribution.md`（新增） | 一级推荐关系、绑定规则、佣金快照/状态、退款冲回、INV-D01~D10 |
| `docs/design/migration.md` | 新增 5 个迁移条目与跨模块表清单 |
| `docs/design/pricing.md` | 会员等级/折扣配置已建模、计价仍 Deferred（修正“尚未建模”表述） |
| `docs/design/error-codes.md` | 未修改：本次不新增错误码，不预分配错误码域 |
| `docs/dev/*` | 未修改：对应业务尚未实现，不编造 Controller/Logic/调用链/测试入口 |

## 11. 未修改文件范围核对

基于 `git diff --name-only 6ea2e17e..85dea0f` 的核对结果：

- 未修改 `api/`、`internal/controller/`、`internal/logic/`、`internal/service/`、`openapi/`、`manifest/`、`scripts/`、
  `Dockerfile`、`docker-compose.yml`（命令过滤结果：`NONE`）。
- 未修改任何既有 Migration：`internal/migrations/sql/` 下仅新增 `20261110000010`~`20261110000014`，
  其余文件（含 `20261001000021_coupon`、`20261110000009_coupon_snapshot` 等）无 diff（命令过滤结果：`NONE`）。
- 未修改 `inventories`/`inventory_logs`/`flash_sale_*` 相关表与秒杀业务代码。
- 未修改 `user_points_accounts`/`user_points_logs`、`wallet_accounts`/`wallet_transactions`、`orders`/`order_items` 的既有结构。
- 三个 `docs/phase2/B1-03_*.md` 保持未跟踪、未覆盖。
- 提交内容不含原始 SQL、PII、密钥或 Token；测试手机号/地址为构造值。

## 12. 交付结论

B1-03-03 的营销与关系模型已具备数据库基础：优惠券批次/范围/限领、拼团、砍价、会员等级/成长值、一级分销关系与佣金
均已在真实 MySQL 上完成 Up/Down/Up 与约束验证；既有订单、优惠券、积分、钱包、秒杀模型保持兼容。
本次只交付 Schema，不包含任何业务流程；B1-04 可直接在这些表上实现 Mock 业务。
