# B1-05 后端最终联合验收报告（B1-05-FINAL）

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-05-FINAL |
| 任务名称 | B1-05 四个子任务最终联合验收 |
| 验收对象 | B1-05-01 / B1-05-02 / B1-05-03 / B1-05-04 |
| 目标分支 | `feat/p2-backend` |
| 验收起点 SHA | `04ab269466ca35ca2feb2a26e62f4522c82390fb` |
| 验收期间合并提交 | `8a0030880d80bf1e035ab34cb8046993a1cde730`（B1-05-04） |
| 报告提交 SHA | 见本报告提交后 `git log -1` |
| 执行日期 | 2026-11-12 |
| 执行环境 | Go `1.26.0 linux/amd64`、MySQL `8.0.46`、Redis 7（Docker，独立端口 `13307` / `16379`） |
| 独立测试库 | `my_shop_b104_test`（集成/迁移）、`my_shop_bargain_test`、`my_shop_b10503_test`、`my_shop_b10503_flash_test`、`my_shop_outbox_race` |
| 未触碰数据库 | `my_shop`、`my_shop_staging`、`my_shop_b10304_verify`（实例内均不存在） |
| Push / Merge main | 未 Push；未合并 `main` / `develop` |
| 最终结论 | **PARTIAL** |

> 本报告以本次真实 Git 状态、真实源码、真实测试退出码和真实数据库结果为依据重写，
> 不复制任何历史报告中的 PASS。所有未执行项标记 NOT_RUN，所有无法验证项标记 BLOCKED。

---

## 1. 执行摘要

| 项 | 结果 |
|---|---|
| 最终结论 | **PARTIAL** |
| 目标分支 | `feat/p2-backend` |
| 验收开始 HEAD | `04ab269`（`origin/feat/p2-backend` 当时一致） |
| 验收结束 HEAD | `8a00308`（新增 B1-05-04 集成合并；脚本修复与报告另提交） |
| 工作区状态 | 验收开始与结束均干净（仅本次授权的修复文件与报告） |
| 执行时间 | 2026-11-12 13:10 ~ 14:35（CST） |
| 静态检查 | `gofmt` / `go build` / `go vet` / `git diff --check` 全部退出码 0 |
| 单元测试 | 28 组执行单元全部真实执行，退出码 0（33.1s） |
| 集成测试 | 退出码 0（11m56s）；首次因测试库名白名单失败，已定位非业务缺陷 |
| Migration | Up / Down / Up 全部 PASS，版本 `20261111000004`、`dirty=0`、64 表 |
| Race | 188 项全部真实 PASS，无 DATA RACE（3m51s） |
| OpenAPI | 210 常驻 + 4 planned、490 Schema、214 operationId、153 码表条目、73 权限码，门禁退出码 0 |
| Registry | 退出码 0，无 Reservation 重复或实现漂移 |
| 完整门禁 | `make test` 退出码 0（17m00s） |
| 历史真实数据迁移 | **BLOCKED**：`tmp/shopping.sql` 在本机不存在，无法执行真实导入核对 |

### 1.1 本次验收发现的关键事实

1. **B1-05-04 此前并未集成到 `feat/p2-backend`**。`6ffe875` / `5681ee7` 仅存在于
   `origin/feat/b1-05-04-openapi-report`，其 merge-base 为 `d1fa065`，从未合回目标分支。
   任务书"四个子任务均已合并"的前提与实际 Git 不符。本次已以无冲突合并提交 `8a00308` 补全集成。
2. **历史真实数据迁移仍不可验证**。`/root/projects/my-shop/tmp/shopping.sql` 不存在，
   `tmp/` 目录亦不存在；受控深度检索未在本机发现同名文件；该文件从未进入 Git。
   B1-05-02 引用 `.agent/tasks/legacy-data-migration/delivery.md` 的"Owner 取消真实导入"是
   **2026-10-12 的旧决策**，而 B1-04-FIX-02（2026-11）已记录 Owner 重新提供该文件，
   因此该 BLOCKED 是**当前环境源数据缺失**，不能以旧决策解释为"不要求"。
3. **B1-05-01 报告未入库**。`d1fa065` 仅含脚本与测试代码，`docs/` 下无 B1-05-01 交付报告，
   导致 313 顶层用例基线的统计口径不可复现（本次以 28 组执行单元与真实退出码为准）。
4. **测试基础设施存在真实安全缺口**：`test-integration.sh` / `test-migration.sh` 默认读取
   `config.yaml` 的 `my_shop`，而后者会清空全部业务表。本次已修复为 fail-closed 隔离库断言。

---

## 2. 子任务验收矩阵

### 2.1 B1-05-01 构建与单元测试完善

| 维度 | 结果 |
|---|---|
| 交付完整性 | `scripts/test-unit.sh`（28 组执行单元、`-count=1`、零匹配防护、失败传播）存在且可用 |
| Git 集成证据 | `d1fa065`，已验证为 HEAD 的祖先（`merge-base --is-ancestor` = YES） |
| 测试证据 | `make test-unit` 退出码 0，28 组全部真实执行；`run_unit_match` 对 `-run` 空跑显式失败 |
| 313 基线 | **不可精确复现**：原始报告未入库，统计口径未固化；本次脚本实际调度 28 组，无缩减证据 |
| 遗留问题 | P3：交付报告缺失；P3：313 统计口径不可复现 |
| 最终状态 | **PASS**（覆盖范围未缩减） |

### 2.2 B1-05-02 集成测试与历史数据迁移

| 维度 | 结果 |
|---|---|
| 交付完整性 | 数据库隔离守卫、Up/Down/Up、Schema 完整性、主链 E2E、历史迁移工具验证均存在 |
| Git 集成证据 | `f73a0a1` + merge `04ab269`，均为 HEAD 祖先 |
| 测试证据 | `TestB10502UpFromEmptySchemaInventory` PASS（8.40s）；`TestB10502DownUpRoundTrip` PASS（10.46s）；`make test` 集成阶段退出码 0 |
| 守卫有效性 | `requireIsolatedDestructiveDB` 以 `SELECT DATABASE()` 实际库名为准，命中 `my_shop` / `my_shop_staging` / `my_shop_b10304_verify` 即 `Fatal` |
| 历史数据迁移 | **BLOCKED**：源 SQL 缺失，未执行真实导入；合成夹具幂等不作为真实迁移 PASS |
| 遗留问题 | P2：测试库名白名单硬编码（`[my_shop_b104_test my_shop_b104_integration]`）；BLOCKED：真实历史数据 |
| 最终状态 | **PARTIAL** |

### 2.3 B1-05-03 并发测试与秒杀回归

| 维度 | 结果 |
|---|---|
| 交付完整性 | `internal/logic/concurrency/`（5 文件）、`internal/logic/flashsale/regression_test.go`、`scripts/test-race.sh` 均存在 |
| Git 集成证据 | `9990c1f` + `591b201`，均为 HEAD 祖先 |
| 测试证据 | `make test-race` 退出码 0，**188 项全部真实 PASS**：17 项五类并发、8 项秒杀回归、90 项既有秒杀回归、20 项秒杀 HTTP E2E、20 项 Outbox/Consumer、33 项内存并发 |
| 零匹配 / SKIP 防护 | 每个阶段先 `go test -list` 枚举，0 项即失败；逐条核对 `--- PASS:`，PASS 行数必须等于枚举数，SKIP 记为未通过 |
| 遗留问题 | P2：`TestConsumeRequestRepairDeadToQueued` 不清理 `admins.username='repair-admin'`，同库重跑必 1062；P2：拼团并发返回 `9006`（订单幂等冲突）语义需产品确认 |
| 最终状态 | **PASS**（存在已记录 P2） |

### 2.4 B1-05-04 OpenAPI 契约审计与最终报告

| 维度 | 结果 |
|---|---|
| 交付完整性 | 生成器改进（`metadata.go`/`check.go`/`opmeta.go`）、七类契约修复、映射矩阵、最终报告均存在 |
| Git 集成证据 | **验收前未集成**：`6ffe875` / `5681ee7` 的 merge-base 为 `d1fa065`，`--is-ancestor` = NO；本次以 merge `8a00308` 补全（无冲突） |
| 测试证据 | `bash scripts/contract-check.sh` 退出码 0（21.8s），NOT RUN 0 项；`go run ./tools/openapi-gen -check` 退出码 0 |
| 遗留问题 | P1：D1 `500/51` 未修复；P2：时区偏移、枚举声明、`format: date-time` |
| 最终状态 | **PASS（集成缺口已在本次修复）** |

### 2.5 四子任务集成状态总览

| 子任务 | 验收前是否在目标分支 | 本次动作 | 现状 |
|---|---|---|---|
| B1-05-01 | 是（`d1fa065`） | 无 | 已集成 |
| B1-05-02 | 是（`04ab269` merge） | 无 | 已集成 |
| B1-05-03 | 是（`591b201`） | 无 | 已集成 |
| B1-05-04 | **否**（仅独立分支） | 合并为 `8a00308` | 已集成 |

---

## 3. 完整测试矩阵（真实退出码）

| # | 阶段 | 完整命令 | 退出码 | 结果 | 备注 |
|---:|---|---|---:|---|---|
| 1 | 格式 | `gofmt -l .` | 0 | PASS | 修复前命中 `internal/logic/concurrency/coupon_concurrency_test.go`，`gofmt -w` 后无输出 |
| 2 | 构建 | `go build ./...` | 0 | PASS | 无输出 |
| 3 | 静态分析 | `go vet ./...` | 0 | PASS | 无输出 |
| 4 | 空白符 | `git diff --check` | 0 | PASS | 无输出 |
| 5 | 单元 | `make test-unit` | 0 | PASS | 28 组，33.1s |
| 6 | Migration | `bash scripts/test-migration.sh` | 0 | PASS | 2m10s，7 组进程级用例 |
| 7 | 集成（首次） | `bash scripts/test-integration.sh` | 1 | **FAIL** | 25 个用例因库名白名单拒绝执行，见 §3.1 |
| 8 | 集成（隔离库修正后） | `bash scripts/test-integration.sh` | 0 | PASS | 11m56s，`DATABASE_DEFAULT_NAME=my_shop_b104_test` |
| 9 | Race | `make test-race` | 0 | PASS | 3m51s，188 项，0 SKIP，无 DATA RACE |
| 10 | 契约 | `bash scripts/contract-check.sh` | 0 | PASS | 21.8s，NOT RUN 0 |
| 11 | Registry | `bash scripts/check-registry.sh` | 0 | PASS | 无重复/漂移 |
| 12 | 完整门禁 | `make test` | 0 | PASS | 17m00s，含 vet/unit/integration/migration/registry/容器/K8s/Nginx |
| 13 | B1-05-02 定向 | `go test -count=1 -run 'TestB10502' ./internal/migrations/` | 0 | PASS | 18.9s，Up + Down/Up |

### 3.1 集成测试首次失败的根因（非业务缺陷）

```text
--- FAIL: TestAddressRegionCreateAndUpdate
    address_region_test.go:66: 拒绝执行破坏性测试：DATABASE_DEFAULT_NAME="my_shop_b105final_test"，
    必须显式设为隔离测试库 [my_shop_b104_test my_shop_b104_integration]
```

`internal/controller/address`、`region`、`profile` 的测试守卫把允许库名硬编码为
`my_shop_b104_test` / `my_shop_b104_integration`。这不是产品缺陷，而是测试资产可维护性问题（P2）。
改用白名单内隔离库后同一命令退出码 0。

### 3.2 SKIP 说明

- `make test-race`：0 项 SKIP（脚本对 SKIP 视为未通过并失败）。
- `make test` 的集成阶段包含真实 Kafka 用例，在未设置 `FLASH_SALE_KAFKA_TEST_BROKER` /
  `FLASH_SALE_KAFKA_E2E_BROKER` 时 `t.Skip`。这些用例**不计为通过**，其门禁入口
  `make test-kafka-e2e` 本次 **NOT_RUN**。

---

## 4. 数据库迁移验证

### 4.1 环境隔离

| 项 | 值 |
|---|---|
| MySQL 实例 | Docker `mysql:8.0`，`127.0.0.1:13307`（容器内 3306） |
| 实例内数据库 | `information_schema` / `mysql` / `performance_schema` / `sys` / 专用测试库 |
| `my_shop` 是否存在 | **不存在**（`SHOW DATABASES` 实测） |
| 目标库 | `my_shop_b104_test` |
| 连接证明 | 迁移日志输出 `migration 目标：127.0.0.1:13307/my_shop_b104_test` |

### 4.2 版本与 Schema

| 项 | 实测值 |
|---|---|
| 最新 migration 版本 | `20261111000004` |
| `schema_migrations.dirty` | `0`（`dirty=false`） |
| 目标库表数 | **64**（63 业务表 + `schema_migrations`） |
| 迁移文件 | 43 个 `up` + 20 个 `down` |
| 历史限制 | `20261001000001..20261001000023` 共 23 个版本只有 `up`，无 `down`（未伪造回滚文件） |

### 4.3 Up / Down / Up 实测

| 阶段 | 断言 | 结果 |
|---|---|---|
| 空白库 Up | 版本 `20261111000004`、`dirty=0`、全部业务表建立 | PASS |
| Schema 完整性 | 关键唯一约束 / 索引 / 外键列顺序一致 | PASS |
| Down 20 步 | 回退至 `20261001000023`、`dirty=0` | PASS |
| Down 边界 | 仅回滚 DDL，不伪造数据删除 | PASS |
| 二次 Up | 恢复 `20261111000004`、`dirty=0`、约束与索引恢复 | PASS |
| 幂等 Up | 第二次 `Up` 无待执行迁移，版本不变 | PASS |

命令与退出码：见 §3 第 6、13 项。B1-05-02 新增的 `b10502_db_guard_test.go` 在每次破坏性
DDL 前以实际连接库名做 fail-closed 校验。

### 4.4 历史真实数据迁移（重点复核）

#### 4.4.1 结论

**BLOCKED。** 本次无法执行任何真实历史数据导入，也未把合成夹具结果冒充为真实迁移。

#### 4.4.2 证据链

| 检查 | 实际结果 |
|---|---|
| `ls -l /root/projects/my-shop/tmp/shopping.sql` | 文件不存在；`tmp/` 目录不存在 |
| `sha256sum` 预期值 | `568da34e4cd084e497450e07e75709966b67153daa99e68ae14e291a01cc23c2`（历史记录，无法校验） |
| 历史来源记录 | B1-04-FIX-02 明确记录路径 `/root/projects/my-shop/tmp/shopping.sql`、5,671,481 字节、83 表、Navicat 导出 |
| 受控检索 | `/root`、`/data`、`/opt`、`/home`、`/mnt`、`/tmp`、`/var/tmp`、`/srv` 深度 ≤4 未发现 `shopping*.sql`；根目录受限检索亦未发现 |
| Git 跟踪 | `git log --all --diff-filter=A -- "*shopping*"` 无源 SQL 记录；`.gitignore` 排除 `/tmp/` 与 `/storage/private/legacy/` |
| B1-05-02 所称"Owner 取消" | 该记录位于 `.agent/tasks/legacy-data-migration/delivery.md`，日期 2026-10-12；晚于它的 B1-04-FIX-02 已记录 Owner 重新提供文件，故不能据此否定本次验证义务 |
| 已提取数据 | `internal/regiondata/regions_v1.tsv` 仅含 3938 条地区字典，不等于完整历史数据迁移 |

#### 4.4.3 未执行项（NOT_RUN / BLOCKED）

- 源库恢复（`system_city` 等 23 域源表）
- CLI `legacy-migrate dry-run` / `import` 的真实数据执行
- 行数核对（地区 3938、商品 55、SKU 110、订单 51、`legacy_id_map` 411 等历史参考值）
- 外键与映射完整性、错误数据与缺失映射检查
- 同一数据源二次 import 的真实幂等性

> 合成夹具幂等测试（`TestEngineImportIdempotentAndIDMap` 等）为 PASS，但**不能**替代真实迁移。

---

## 5. OpenAPI 最终统计

以合并 B1-05-04 后重新生成并校验的真实结果为准：

| 指标 | 本次实测 | 历史基线 | 一致性 |
|---|---:|---:|---|
| 常驻 API | **210** | 210 | 一致 |
| Planned API | **4** | 4 | 一致 |
| Schema 总数 | **490** | 490 | 一致 |
| 唯一 operationId | **214** | 214 | 一致 |
| 错误码码表条目 | **153**（含 `CodeOK`；152 个非零唯一码） | 153 | 一致，无重复值 |
| 权限码 | **73** | 73 | 一致 |
| 分域路径 | **167**（主文件 171 条含 planned 4） | — | 生成器实测 |
| 未引用组件 | 228（全部在白名单） | — | 生成器实测 |
| `x-known-issue` | **28** 条 operation | 28 | 一致 |
| 契约门禁 NOT RUN | **0** | 0 | 一致 |

### 5.1 逐项核对

| 检查项 | 结果 | 证据 |
|---|---|---|
| Go 路由 ↔ OpenAPI 双向一致 | PASS | `TestRoute|TestContractPlannedNotRouted` 退出码 0 |
| operationId 唯一 | PASS | 214 个，`-check` 步骤通过 |
| DTO ↔ Schema | PASS | 深比较通过 |
| 错误码 ↔ codes.go | PASS | 153 条逐项一致 |
| 权限码 ↔ 路由 ↔ seed | PASS | 73/73 集合相同 |
| 金额单位 | PASS | 19 处缺失已在 B1-05-04 修复 |
| 时间字段格式 | **P2 未修复** | `gtime.Time` 仍输出 `YYYY-MM-DD HH:MM:SS`，契约以 `x-format: gtime-local` 如实记录 |
| Planned 未误算为实际 API | PASS | 4 条 isolated，路由测试断言未实现 |
| 生成前后无漂移 | PASS | `contract-check.sh` 步骤 1 |

### 5.2 B1-05-04 遗留问题逐项复核

| 编号 | 历史描述 | 本次代码核实 | 判定 |
|---|---|---|---|
| O-1 | D1：DTO `v:"required"` 失败返回 HTTP 500 + code 51 | `response.go` 调 `codes.FromError` → `Code(51)`；`codes.HTTPStatus` 对未知码返回 500；`codes.go` 无 51 | **确认存在**（P1，需 Owner） |
| O-2 | 响应时间字段缺时区偏移 | `*gtime.Time` + 默认 `MarshalJSON`；契约以 `x-format: gtime-local` 记录 | **确认存在**（P2，破坏性变更需版本化） |
| O-3 | 三个 DTO 字段声明未实现枚举 | `member.go` 描述含 `admin_adjust`/`manual`，实现仅 `order_reward`/`refund_deduct`；`distribution.go` 描述含 `cancel`，实现仅 `refund` | **确认存在**（P2，需 Owner 定取值） |
| O-4 | 4 条 planned 未实现 | `x-planned-apis` 与路由断言一致 | 确认，属 Deferred |
| O-8 | RFC3339 查询参数缺 `format: date-time` | `grep -c 'format: date-time' openapi/` = 0 | **确认存在**（P2，不影响运行时校验） |

---

## 6. 遗留问题清单

### 6.1 P0（阻断验收）

无。

### 6.2 P1（阻止完整 PASS）

| 编号 | 问题 | 类型 | 证据 | 处理 |
|---|---|---|---|---|
| P1-1 | D1：请求 DTO 校验失败返回 `HTTP 500 + code 51`，与目标 `400 + 1001` 不一致 | 历史遗留 / 运行时缺陷 | `internal/middleware/response.go`、`codes.go` 无 51；契约 28 条 operation 以 `x-known-issue` 双状态记录 | **需 Owner 决策**：建议在 `codes.FromError` 归一 `gvalid`，涉及全局错误语义 |
| P1-2 | 历史真实数据迁移无法验证（源 SQL 缺失） | 关键验证缺失 / 环境阻塞 | §4.4 证据链 | 需数据持有方重新提供源文件；不得伪造 |

### 6.3 P2（局部缺陷 / 测试质量）

| 编号 | 问题 | 证据 |
|---|---|---|
| P2-1 | `TestConsumeRequestRepairDeadToQueued` 不清理 `admins.username='repair-admin'`，同库重复执行必 1062 | `kafka_consumer_test.go:924`；`setupConsumeTest` 清理列表不含 `admins` |
| P2-2 | `bargain` 集成测试硬编码 `127.0.0.1:13307` 与 `bargain-test-pass` | `internal/logic/bargain/integration_test.go:34-38` |
| P2-3 | 测试库名白名单硬编码，新隔离库无法复用 | `address_test.go:114`、`region_test.go:29`、`profile_test.go:34` |
| P2-4 | 响应时间字段无时区偏移（O-2） | §5.2 |
| P2-5 | 三个 DTO 枚举声明与实现不一致（O-3） | §5.2 |
| P2-6 | RFC3339 查询参数无 `format: date-time`（O-8） | §5.2 |
| P2-7 | 拼团并发重复参团返回 `9006` 而非幂等成功 | `concurrency/groupbuy_concurrency_test.go:265-271`；副作用为零，语义待产品确认 |

### 6.4 P3（文档 / 可维护性）

| 编号 | 问题 |
|---|---|
| P3-1 | B1-05-01 交付报告未入库（仅代码），313 顶层用例统计口径不可复现 |
| P3-2 | `test/e2e` 无砍价独立 E2E 文件（HTTP 层覆盖在 `internal/controller/bargain`） |
| P3-3 | `x-error-codes` 为静态调用链上界，非运行时穷举（O-5） |
| P3-4 | `RefundAudit.action` 有 `enum` 无 `x-enum-labels`（O-7） |

### 6.5 BLOCKED / NOT_RUN / Deferred / Owner 待决策

| 分类 | 项目 | 说明 |
|---|---|---|
| BLOCKED | 真实历史数据导入与行数核对 | `shopping.sql` 不存在 |
| NOT_RUN | `make test-kafka-e2e`（真实 Kafka Broker） | 本次环境无 Broker |
| NOT_RUN | `make test-storage`（真实七牛） | 需真实凭据 |
| NOT_RUN | `make test-serve-lifecycle` | 未在本次门禁内 |
| NOT_RUN | Kubernetes 真实集群行为 | `make test` 仅做离线 Kustomize 校验 |
| NOT_RUN | 真实第三方支付 / 自动打款 | 明确不在范围 |
| Deferred | 砍价成功后下单 | 代码仅有 `order_id` 字段，无下单实现（业务边界） |
| Deferred | `coupon_batches` 批次领取 | 表存在、迁移工具写入存在，`coupon` 逻辑无批次入口 |
| Deferred | 4 条 Planned API | 秒杀公开列表/详情、可领取券列表、订单可用券 |
| Deferred | 积分抵扣 | `points_discount` 恒 0 |
| Owner | D1 500/51 归一方案 | 影响全局错误语义 |
| Owner | 时区输出改 RFC3339 | 破坏性变更，需 API 版本化 |
| Owner | O-3 枚举取值（保留未实现 vs 删除文档） | 产品语义 |
| Owner | 拼团重复参团 `9006` 是否改为幂等成功 | 产品语义 |

---

## 7. Git 最终状态

| 项 | 值 |
|---|---|
| 分支 | `feat/p2-backend`（本地跟踪 `origin/feat/p2-backend`） |
| 验收起点 HEAD | `04ab269466ca35ca2feb2a26e62f4522c82390fb` |
| 验收期间合并提交 | `8a0030880d80bf1e035ab34cb8046993a1cde730` |
| 与远端关系 | 验收起点与远端一致；本次新增 3 个本地提交（合并 + 脚本修复 + 报告） |
| Push 状态 | **未 Push** |
| Merge main/develop | **未执行** |
| 分支/Worktree 删除 | **未执行** |
| 工作区 | 提交后干净 |

### 7.1 本次变更

| 文件 | 性质 | 说明 |
|---|---|---|
| `.cnb.yml` | 测试基础设施修复 | CI 使用 `my_shop_ci_test`，显式注入 `DATABASE_DEFAULT_NAME` |
| `scripts/lib.sh` | 测试基础设施修复 | 新增 `assert_isolated_test_db`（fail-closed 隔离库断言） |
| `scripts/test-integration.sh` | 测试基础设施修复 | 调用断言；`go test -p 1 -count=1 ./...` |
| `scripts/test-migration.sh` | 测试基础设施修复 | 调用断言 |
| `internal/logic/concurrency/coupon_concurrency_test.go` | 格式修复 | `gofmt -w`，无语义变更 |
| `docs/phase2/B1-05_FINAL_REPORT.md` | 文档 | 本报告 |

### 7.2 历史提交保留

`d1fa065`（01）、`f73a0a1` + `04ab269`（02）、`9990c1f` + `591b201`（03）、
`6ffe875` + `5681ee7`（04）全部保留，未改写、未 rebase、未 force push。

---

## 8. 最终结论

### **PARTIAL**

判定依据：

1. **主体工程门禁全部真实通过**：静态检查、28 组单元测试、Migration Up/Down/Up、
   全量集成（11m56s）、188 项 Race、OpenAPI 契约、Registry、`make test`（17m00s）
   退出码均为 0，无 DATA RACE，无 SKIP 冒充 PASS。
2. **存在未解决的 P1**：D1 `HTTP 500 + code 51` 是产品可见的运行时缺陷，需 Owner 决策修复方案。
3. **存在关键验证缺失**：历史真实数据迁移因源 SQL 缺失而 BLOCKED，无法完成行数、外键、
   映射与真实二次幂等核对。合成夹具结果不作为真实迁移 PASS。
4. **已修复的集成缺口**：B1-05-04 此前未合并，本次以 `8a00308` 补全；该事实说明
   "四个子任务均已进入 `feat/p2-backend`"的既有认知与 Git 实际不符。

因此不满足 PASS 的"所有强制验收项目通过、无未处理 P1"条件；也不构成 FAIL
（未发现数据安全、资金错误或交易一致性缺陷）。

---

## 9. B1-05-FINAL-FIX 证据补齐

> 本章为 B1-05-FINAL 的增补，不推翻 §1~§8 的既有结论。最终结论仍为 **PARTIAL**。

### 9.1 CI 数据库安全一致性审计

#### 9.1.1 实际配置

| 层 | 位置 | 实际值 |
|---|---|---|
| CI 依赖启动 | `.cnb.yml`（pr-check / main-ci 的 `start-dependencies`） | `MYSQL_DATABASE=my_shop_ci_test` |
| CI 测试执行 | `.cnb.yml`（`test` stage） | `DATABASE_DEFAULT_NAME=my_shop_ci_test` |
| Shell 守卫 | `scripts/lib.sh` `assert_isolated_test_db` | 要求显式设置且以 `_test` / `_race` 结尾 |
| Go 守卫 | `internal/testdb`（本次新增，统一来源） | 逐库显式白名单 |

#### 9.1.2 修复前的不一致（已确认）

- CI 使用 `my_shop_ci_test`，Shell 层允许（`_test` 后缀）。
- Go 层有 4 处硬编码白名单，仅允许 `my_shop_b104_test` / `my_shop_b104_integration`：
  `internal/controller/address/address_test.go`、`internal/controller/region/region_test.go`、
  `internal/controller/profile/profile_test.go`、`internal/regiondata/regiondata_integration_test.go`。
- 后果：`make test` 在 CI 中会在 `address` / `region` / `profile` 三个包直接 `Fatal`；
  `regiondata` 与 `freight` 则静默 `Skip`（更隐蔽）。
- 结论：**修复前 CI 的 `make test` 无法通过，且 freight/regiondata 集成用例不会真实执行。**

#### 9.1.3 修复方式

新增 `internal/testdb` 作为唯一白名单来源（逐库显式列名，**不是**任意 `_test` 前缀）：

```go
var allowed = []string{
    "my_shop_b104_test",
    "my_shop_b104_integration",
    "my_shop_ci_test",
}
```

5 个测试文件改为调用 `testdb.IsAllowed` / `testdb.Allowed()`；安全守卫本身未取消、未放宽。
`internal/testdb/testdb_test.go` 增加两类回归：

1. `TestIsAllowed`：CI 库放行；`my_shop`、`my_shop_staging`、空值、`my_shop_random_test`、
   `my_shop_ci_test_evil` 全部拒绝。
2. `TestCIConfiguredDatabasesAreAllowed`：解析 `.cnb.yml` 中所有
   `MYSQL_DATABASE` / `DATABASE_DEFAULT_NAME`，要求均在白名单内，防止再次漂移。

#### 9.1.4 验证矩阵（真实执行）

| 场景 | 预期 | Shell 层实测 | Go 层实测 |
|---|---|---|---|
| `my_shop` | 拒绝 | 退出码 1：`拒绝在共享/历史数据库 my_shop` | `IsAllowed=false`（单测） |
| 未配置 | 拒绝 | 退出码 1：`必须显式设置 DATABASE_DEFAULT_NAME` | `IsAllowed("")=false` |
| `my_shop_ci_test` | 允许 | 通过守卫 | region/address/profile/regiondata/freight 定向用例全部退出码 0 |
| 不符合命名规则（`my_shop_foo`） | 拒绝 | 退出码 1：`必须以 _test 或 _race 结尾` | `IsAllowed("my_shop_random_test")=false` |

Migration 是否使用同一隔离库：**是**。真实执行日志：

```text
$ DATABASE_DEFAULT_NAME=my_shop_ci_test bash scripts/test-migration.sh
[INFO] migration 目标：127.0.0.1:13307/my_shop_ci_test
[INFO] migration 执行完成，dirty=false
ok  cnb.cool/go-cloud-devops/my-shop/internal/migrations  119.559s
exit=0
```

> 说明：本次未在真实 CI runner 上执行流水线，而是以 `.cnb.yml` 的同等环境变量在本地复现。
> CI 端到端结果仍属 NOT RUN，不能标记为 PASS。

### 9.2 `make test` 实际执行组成

`make -n test` 仅输出 `bash scripts/test.sh`，真实组成需读取 `scripts/test.sh`。

| 门禁 | 是否包含 | 实际命令 | 条件跳过 |
|---|---|---|---|
| Build | **否** | — | `make test` 不调用 `go build`；构建由 `make build` 独立承担 |
| Vet | 是 | `go vet ./...` | 无 |
| Unit | 是 | `scripts/test-unit.sh`（29 组） | 无 |
| Integration | 是 | `go test -p 1 -count=1 ./...` | 需 MySQL/Redis，缺失即失败 |
| Migration | 是 | `scripts/test-migration.sh` | 需 MySQL |
| Race | **否** | — | 独立 CI stage `make test-race` |
| E2E | 是（间接） | 包含在 `./test/e2e` 的 `go test ./...` 中 | 无独立入口 |
| OpenAPI 契约 | **否** | — | 独立 CI stage `bash scripts/contract-check.sh` |
| Registry | 是 | `scripts/check-registry.sh` | 无 |
| lib 语义 | 是 | `scripts/test-lib.sh` | 无 |
| Prometheus | 是 | `scripts/test-prometheus.sh` | 缺 `promtool` 时仅跳过 recording rules 语法，静态断言仍执行 |
| 容器化 | 是 | `scripts/test-container.sh` | 无 |
| K8s 清单 | 是 | `scripts/test-k8s-manifest.sh` | 需 `kubectl kustomize`；无集群时跳过 server-side dry-run |
| 发布脚本 | 是 | `scripts/test-deploy-k8s.sh` | 使用 fake kubectl，无真实集群 |
| K8s 基础设施 | 是 | `scripts/test-k8s-infra.sh` | 无集群时跳过 server-side dry-run |
| 集群脚本 | 是 | `scripts/test-k8s-cluster.sh` | 使用 fake kind/kubectl |
| Nginx | 是 | `scripts/test-nginx.sh` | 无 |

真实执行日志（`make test`，退出码 0，17m00s）中的条件分支：

```text
[WARN] 未找到 promtool 或 prometheus 镜像，跳过 recording rules 语法校验（静态断言仍执行）
[WARN] 未安装 kubeconform，跳过 API Schema 校验（NOT_VERIFIED）
[WARN] 无可用集群：跳过 server-side dry-run 与真实部署验证（NOT_VERIFIED）
```

未包含且本次未执行：`test-race`、`contract-check`、`test-kafka-e2e`、`test-storage`、
`test-serve-lifecycle`、真实 Kubernetes 集群验证、真实第三方支付。

### 9.3 单元测试数量同口径对比

统计方法（两侧完全相同）：

1. 从 `scripts/test-unit.sh` 解析全部 `run_unit` / `run_unit_match` 执行组（含
   `FLASH_SALE_UNIT_TESTS` 变量展开，分支自带前导 `|`，直接拼接而非再次用 `|` 连接）。
2. 对每组执行 `go test -count=1 -v [-run <同一正则>] <同一包>`。
3. 顶层用例只统计 `=== RUN   TestXxx`（名称不含 `/`）；子测试统计含 `/` 的 RUN 行；两者分开。
4. 基线源码通过 `git archive d1fa065` 导出到临时目录，未切换分支、未改动工作区。

| 指标 | B1-05-01 基线 `d1fa065` | 当前 HEAD | 差异 |
|---|---:|---:|---|
| 执行组 / 测试包 | 28 | 29 | +1（新增 `internal/testdb`） |
| 顶层 Test 函数 | **313** | **315** | +2（白名单与 CI 一致性回归） |
| 子测试 | 254 | 254 | 0 |
| PASS（顶层） | 313 | 315 | +2 |
| SKIP | 0 | 0 | 0 |
| FAIL | 0 | 0 | 0 |

结论：

- 历史基线 313 **已精确复现**，此前"统计口径不可复现"的 P3 结论由本条更正。
- 当前 HEAD 未发生任何用例缩减；增加 2 项为本任务新增的安全回归。
- 统计不包含集成测试包（`internal/migrations` 等仅统计脚本登记的纯内存 `-run` 子集），
  也不包含 `t.Run` 子测试。

### 9.4 D1 运行时缺陷复现

使用现有 E2E 基础设施（真实路由 + 真实 MySQL/Redis + 进程内 HTTP server）构造最小非法请求。
临时取证用例运行后已删除，未提交（避免把缺陷行为固化为断言）。

| 项目 | 实际结果 |
|---|---|
| API 路径 | `POST /categories`（后台创建分类） |
| 请求参数 | `{}`（缺少必填 `name`）；补充验证 `{"name": ""}` |
| 预期行为 | `HTTP 400 + code 1001`（参数校验失败） |
| 实际 HTTP 状态 | **500** |
| 实际错误码 | **51** |
| 错误信息 | `内部错误` |
| 错误产生位置 | GoFrame `ghttp` 请求参数校验（`gvalid`）返回 `gcode.CodeValidationFailed`，其值为 51（`github.com/gogf/gf/v2@v2.10.3/errors/gcode/gcode.go:32`） |
| 最终映射位置 | `internal/middleware/response.go:31-37` → `internal/codes/codes.go:444-453`（`FromError` 原样返回 51）→ `codes.go:417-422`（未知码 → 500）→ `codes.go:425-430`（未知码 → "内部错误"） |

原始输出：

```text
=== RUN   TestD1ReproMissingRequiredField
    d1_repro_tmp_test.go:13: D1_REPRO status=500 code=51 message="内部错误"
    d1_repro_tmp_test.go:16: D1_REPRO_EMPTY status=500 code=51 message="内部错误"
--- PASS: TestD1ReproMissingRequiredField (0.76s)
```

调用链定位：

| 环节 | 位置 |
|---|---|
| 路由注册 | `internal/cmd/routes_admin.go:113`（`require("category:create").POST("/categories", ...)`） |
| DTO 必填标签 | `api/categories/v1/categories.go:45`（`Name string \`v:"required"\``） |
| Controller | `internal/controller/categories/categories.go:29` |
| 影响范围 | OpenAPI 中 28 条 operation 以 `x-known-issue: B1-02-D1` 如实记录 |

结论：**DEFECT CONFIRMED（可稳定复现）**。本次仅复现，不修复。归一为 `400 + 1001`
属全局错误语义变更，仍待 Owner 决策。

### 9.5 历史数据迁移状态归档

| 项 | 结论 |
|---|---|
| B1-05-02 报告 | 明确记录真实历史数据核对 BLOCKED，未伪造行数 |
| B1-04-FIX-02 报告 | 记录 Owner 曾提供 `/root/projects/my-shop/tmp/shopping.sql`，并仅提取 `system_city` |
| `internal/regiondata/regions_v1.tsv` | **存在**（105,346 字节，`go:embed`，含来源与 SHA-256 注释） |
| 地区字典恢复 | **已真实验证**：3938 行（34 省 / 369 市 / 3535 区县），`TestDatasetIntegrity` 与导入幂等测试通过 |
| 完整历史业务数据迁移 | **BLOCKED**：商品/分类/SKU/库存/用户/地址/订单/优惠券/钱包/积分/评价/收藏/`legacy_id_map` 均未在本次真实导入 |

原始文件 SHA-256 `568da34e4cd084e497450e07e75709966b67153daa99e68ae14e291a01cc23c2`
仅为历史记录（来自 B1-04-FIX-02 与 `regions_v1.tsv` 头注释），**本次未重新校验原始文件**，
因为文件不存在。本次未再进行全盘扫描。

归档结论：真实历史业务数据迁移为**外部 BLOCKED**，阻塞原因是可信数据源缺失，
不是迁移工具实现缺陷。等待 Owner 重新提供数据源后方可恢复验证。

### 9.6 本次修订与 Git

| 项 | 值 |
|---|---|
| 分支 | `feat/p2-backend` |
| 修订前 HEAD | `2ec9d8f6614c4fb8f341c0fe4b2dc9188d33e4e7` |
| 本次修订提交 | 见 `git log -1`（`docs(b1-05): supplement final acceptance evidence`） |
| Push | 未执行 |
| Merge main/develop | 未执行 |

修改文件：

| 文件 | 原因 |
|---|---|
| `internal/testdb/testdb.go` | 新增统一测试库白名单（消除 4 处硬编码漂移） |
| `internal/testdb/testdb_test.go` | 新增安全边界与 CI 配置一致性回归 |
| `internal/controller/address/address_test.go` | 改用统一白名单 |
| `internal/controller/region/region_test.go` | 改用统一白名单 |
| `internal/controller/profile/profile_test.go` | 改用统一白名单 |
| `internal/regiondata/regiondata_integration_test.go` | 改用统一白名单（避免 CI 静默 Skip） |
| `internal/logic/freight/integration_test.go` | 改用统一白名单（避免 CI 静默 Skip） |
| `scripts/test-unit.sh` | 登记 `internal/testdb` 纯内存测试（28 → 29 组） |
| `tools/openapi-gen/gen.go`、`metadata.go`、`opmeta.go` | `gofmt` 格式修复（B1-05-04 合并后遗留，无逻辑变更） |
| `docs/phase2/B1-05_FINAL_REPORT.md` | 本章证据补齐 |

### 9.7 问题清单变更

| 编号 | 变更 | 级别 | 状态 |
|---|---|---|---|
| FIX-1 | CI `my_shop_ci_test` 与 Go 白名单不一致，CI `make test` 必然失败，freight/regiondata 静默 Skip | P1（工程安全） | **已修复**（统一白名单 + 回归测试） |
| FIX-2 | B1-05-04 合并后 3 个 `tools/openapi-gen` 文件未通过 `gofmt` | P3 | **已修复** |
| FIX-3 | 此前"313 统计口径不可复现" | P3 | **已更正**：同口径下基线精确复现 313 |
| P1-1 | D1 `500 + 51` | P1 | **仍存在**（已稳定复现，待 Owner 决策） |
| P1-2 | 历史真实业务数据迁移 | BLOCKED | **仍阻塞**（数据源缺失） |

最终结论维持 **PARTIAL**：工程门禁与 CI 安全配置已修复，但 D1 运行时缺陷与真实历史数据迁移
仍未解决，不满足 PASS 条件。

## 10. B1-05-FINAL-FIX-02：最终工程决策执行

> 本章为追加修订，不覆盖 §1~§9。§9.7 中"D1 仍存在"是当时的真实结论；
> 本章记录 D1 修复后的新状态。

### 10.1 D1：GoFrame 参数校验统一映射（已修复）

#### 10.1.1 实现

在统一错误映射层 `internal/codes/codes.go` 的 `FromError` 中识别 GoFrame 校验失败：

```go
if c.Code() == gcode.CodeValidationFailed.Code() { // 框架码 51
    return CodeInvalidArgument // 业务码 1001 → HTTP 400
}
```

约束遵守情况：

- 只归一 `gvalid` 的确定语义（51），未把所有未知错误映射为 1001。
- `gcode.CodeNil` 仍为 1000；普通错误仍为 1000；未知码仍由 `HTTPStatus` 返回 500。
- 未修改第三方依赖，未逐个 Controller 打补丁。
- 冲突检查：`codes.go` 码表中不存在 51（也不存在 50/53），无业务码冲突。

#### 10.1.2 真实 HTTP 证据

| 请求 | 修复前 | 修复后 |
|---|---|---|
| `POST /categories` `{}` | 500 + 51 + "内部错误" | **400 + 1001 + "参数错误"** |
| `POST /categories` `{"name":""}` | 500 + 51 | **400 + 1001** |
| `POST /admin/login` `{}`（第二个代表性 DTO） | 500 + 51 | **400 + 1001** |
| `POST /categories` 同级重名（正常业务错误） | 409 + 3002 | **409 + 3002（不变）** |
| 未知内部错误（单元测试） | 500 | **500（不变）** |

回归测试：

- `internal/codes/fromerror_test.go`：51 → 1001；普通错误 → 1000；未知码 HTTP 500。
- `test/e2e/validation_error_mapping_test.go`：3 个 DTO 校验入口 + 1 个业务错误入口。

契约同步：删除 28 条 operation 上已失效的 `x-known-issue: B1-02-D1`（及全局条目），
生成器新增回归测试禁止该标记再次出现。

### 10.2 O2：响应时间字段兼容性（文档化，不改 API）

- 现有响应保持 `YYYY-MM-DD HH:MM:SS`，不含时区偏移，不改为 RFC3339。
- 新增 `docs/design/api-time-semantics.md`，记录：时区来源（`dashboard.timezone`，
  默认 `Asia/Shanghai`，MySQL 会话固定偏移，DST 时区启动即拒绝）、序列化格式、
  客户端不得假定 UTC、API 不携带时区标识的已知限制、新 API 版本必须使用 RFC3339。
- `docs/design/architecture.md` 增加索引；OpenAPI `info.description` 增加时间语义指引。
- 未修改任何生产序列化代码，无破坏性变更。

### 10.3 O3：清理无运行时依据的枚举声明

三个字段的核实结果：

| 字段 | 修复前契约 | 运行时实现 | 处理 |
|---|---|---|---|
| `MemberGrowthLog.change_type` | 无 `enum` 键，但描述列出 `admin_adjust` | 仅 `order_reward` / `refund_deduct` | 描述删除 `admin_adjust` |
| `MemberGrowthLog.biz_type` | 无 `enum` 键，但描述列出 `manual` | 仅 `order` / `refund` | 描述删除 `manual` |
| `DistributionCommission.reversal_reason` | 无 `enum` 键，但描述列出 `cancel` | 仅 `refund` | 描述删除 `cancel` |

结论：生成物中原本不存在 `enum` 键（B1-05-04 已避免虚构 enum），实际不一致是 DTO 描述
列举了未实现取值。本次从 DTO 事实源删除这些取值并重新生成，三个字段不再出现无运行时
依据的枚举描述；`TestDeferredEnumValuesNotDocumented` 防止回归。
字段类型、必填约束、可空性均未改变。

### 10.4 O4：拼团重复参团语义（维持 9006）

生产代码未修改，维持既有语义。真实 MySQL 并发测试
`TestGroupBuyConcurrentDuplicateJoinSingleParticipant`（10 并发，同一用户、同一团、同一幂等键）：

```text
并发重复参团结果：成功回读=2，9006(订单幂等冲突)=8，25007(已参与)=0
joined_count=2，participants=2，orders=2，库存仅扣减 2
```

- 未获得资格的请求全部为 **9006**（业务冲突，HTTP 409），不是幂等成功。
- 无第二条参与记录/订单，未重复扣库存、未重复增加人数。
- 既有"已参与者顺序重试回读团详情"的幂等快路径保持不变。
- `docs/design/marketing.md` §2.9.1 固化上述语义；未引入第二套订单幂等机制。

### 10.5 M1：真实历史业务数据迁移（继续 BLOCKED）

- 地区字典 3938 条为真实数据验证，不等于完整历史业务数据迁移。
- 商品/订单/用户/钱包/积分等真实导入仍缺少可信源文件，本次未执行、未伪造。
- 恢复条件：Owner 提供可信 `shopping.sql` 或其他正式数据源，并在隔离源库/目标库重新执行
  dry-run、import、行数/外键/映射核对与二次幂等。
- 迁移功能与既有报告均保留，未为消除 BLOCKED 修改任何迁移结果。

### 10.6 测试验证矩阵（真实退出码）

| # | 命令 | 退出码 | 结果 | 备注 |
|---:|---|---:|---|---|
| 1 | `gofmt -l .` | 0 | PASS | 无输出 |
| 2 | `go build ./...` | 0 | PASS | — |
| 3 | `go vet ./...` | 0 | PASS | — |
| 4 | `git diff --check` | 0 | PASS | — |
| 5 | `make test-unit` | 0 | PASS | 30 组，27.7s |
| 6 | `bash scripts/test-race.sh` | 0 | PASS | **188 项**，2m25.8s，0 SKIP，无 DATA RACE |
| 7 | `bash scripts/test-migration.sh` | 0 | PASS | 119.0s |
| 8 | `bash scripts/check-registry.sh` | 0 | PASS | — |
| 9 | `bash scripts/contract-check.sh` | 0 | PASS | NOT RUN 0 |
| 10 | `make test`（含 `go test -p 1 -count=1 ./...`） | 0 | PASS | 17m05.5s |
| 11 | 定向：`go test ./internal/codes/` | 0 | PASS | 2 项 |
| 12 | 定向：D1 E2E 4 个场景 | 0 | PASS | 400/1001 与 409/3002 |
| 13 | 定向：拼团并发重复参团 | 0 | PASS | 8×9006，无重复副作用 |

单元用例同口径统计（`scripts/test-unit.sh` 每组 `-count=1 -v`，顶层 `=== RUN` 不含 `/`）：

| 指标 | B1-05-FINAL-FIX-01 基线 | 本次 HEAD |
|---|---:|---:|
| 执行组 / 包 | 29 | 30 |
| 顶层 Test 函数 | 315 | **317** |
| 子测试 | 254 | 254 |
| SKIP | 0 | 0 |
| FAIL | 0 | 0 |

增加 2 项为 D1 错误映射单测；未删除、未缩小正则、未增加 SKIP。

### 10.7 OpenAPI 统计变化

| 指标 | 修改前 | 修改后 | 说明 |
|---|---:|---:|---|
| 常驻 API | 210 | **210** | 不变 |
| Planned API | 4 | **4** | 不变 |
| Schema | 490 | **490** | 不变 |
| 唯一 operationId | 214 | **214** | 不变 |
| 错误码码表 | 153 | **153** | 不变 |
| 权限码 | 73 | **73** | 不变 |
| `x-known-issue: B1-02-D1` operation | 28 | **0** | D1 已修复，契约删除失效声明 |
| 未引用组件 | 228 | **228** | 不变 |

生成物净变化：`-333` 行 D1 known-issue 块、3 处字段描述修正、1 处 `info.description` 增补。

### 10.8 最终结论

| 维度 | 结论 |
|---|---|
| 本次定向修复（D1/O2/O3/O4） | **PASS** |
| B1-05 工程验收 | **PARTIAL**（M1 外部阻塞） |
| 真实历史业务数据迁移 | **BLOCKED** |

不将 B1-05 整体改为 PASS：真实历史业务数据迁移仍未验证，属外部数据源阻塞。

### 10.9 本次提交

| 项 | 值 |
|---|---|
| 分支 | `feat/p2-backend` |
| 修订前基线 | `f0b5ccbbfde8013f3b4e25e201f412902f9a1325` |
| 代码 / 契约 / 测试提交 | `353abc2`（`fix(api): normalize GoFrame validation errors to 400/1001`） |
| 文档 / 报告提交 | 本提交，见 `git log -1` |
| Push | **未执行** |
| Merge main/develop | **未执行** |
| 工作区 | 提交后干净 |

## 附录 A：本次修复记录（修改前 → 修改后）

| 编号 | 原始问题 | 修改 | 修改前验证 | 修改后验证 |
|---|---|---|---|---|
| F-1 | `gofmt -l .` 命中 `coupon_concurrency_test.go` | `gofmt -w` | 输出 1 个文件 | 无输出，退出码 0 |
| F-2 | `test-integration.sh` 无 `-count=1`，可能复用缓存 | 加 `-count=1` | 代码审查确认缺失 | 集成测试真实执行 11m56s，退出码 0 |
| F-3 | `test-integration.sh` / `test-migration.sh` 默认落到 `my_shop`（会清空业务表） | `lib.sh` 新增 `assert_isolated_test_db`，两脚本调用 | 未设置时按 `config.yaml` 使用 `my_shop` | 未设置 / `my_shop` / 无 `_test` 后缀三种情况均退出码 1 且不执行 SQL |
| F-4 | CI `make test` 无隔离库名 | `.cnb.yml` 设置 `MYSQL_DATABASE=my_shop_ci_test` 与 `DATABASE_DEFAULT_NAME` | `make test` 使用 `my_shop` | 配置层显式隔离（本次未在 CI 实跑） |
| F-5 | B1-05-04 未合并 | `git merge --no-ff`，merge-tree 预检无冲突 | `--is-ancestor` = NO | `8a00308` 合并成功，契约门禁退出码 0 |

## 附录 B：可复核命令

```bash
git branch --show-current                     # feat/p2-backend
git log --oneline -5
git merge-base --is-ancestor 6ffe875 HEAD     # 验收前返回 1，合并后返回 0

gofmt -l .
go build ./...
go vet ./...
git diff --check

make test-unit
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PORT=13307 \
DATABASE_DEFAULT_PASS=*** REDIS_DEFAULT_ADDRESS=127.0.0.1:16379 \
  bash scripts/test-migration.sh
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PORT=13307 \
DATABASE_DEFAULT_PASS=*** REDIS_DEFAULT_ADDRESS=127.0.0.1:16379 \
  bash scripts/test-integration.sh
DATABASE_DEFAULT_PORT=13307 DATABASE_DEFAULT_PASS=*** \
REDIS_DEFAULT_ADDRESS=127.0.0.1:16379 bash scripts/test-race.sh

bash scripts/contract-check.sh
bash scripts/check-registry.sh
make test
```
