# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 / 路线 | `surgecart-baseline-audit-v1` / `NORMAL`（`contract.status=NOT_REQUIRED`，`resources.reservations={}`） |
| 仓库 / 分支 | `origin` = `https://cnb.cool/framework-station/my-shop`；`task/surgecart-baseline-audit-v1` |
| 任务基线 | `85402302622d059918e1a87b83bbb55b400d1a85`（`develop`） |
| **C1（审查对象）** | `1ca5445ef3d9cc04a7f11f78eb44638376d1eb78` — 仅修改 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`（5 insertions / 4 deletions，`git show --stat` 已核） |
| C2（metadata） | `f3da5f56895f9f0833e5a06ca81ddddb3e037104` — 仅 `state.yaml`（`review.status=PENDING`、`review.target=C1`，2+/2-） |
| **被审计代码版本** | `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`（报告 §0 锚点；`git merge-base --is-ancestor` 成立） |
| 上一轮结论与 Owner 决定 | 上一轮 CLEAN 的 target 为 `757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb`；Owner 于 `b013dc1` 记录 `REJECTED`（仅限报告事实准确性），要求修正 CLEAN-001/CLEAN-002 后重新形成 C1/C2 |
| 本轮性质 | 首次输入描述的是 `757ed22` 阶段；Git 事实已前移到新 Review Request（`review.target=1ca5445`，入口 Gate `cleaner-start` PASS），本轮按新靶标独立复审 |
| 审查开始时 Git 状态 | 工作区 clean；本地 HEAD == `origin/<branch>` == `f3da5f5…`（`git ls-remote` 已核） |
| 相关变更 | `7ba5a31..HEAD`：新增 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`；任务目录新增 `findings.md` / `core-logic.md` / `owner-decision.md` / `loop-ledger.md` 并修改 `state.yaml` |
| 任务前已有修改 | 无。受保护路径 `git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` 为空 |
| 关键配置 / 迁移版本 | `manifest/config/config.yaml:105,120,130,141` 四个 `enabled: false`；migration `20261001000011/15/18`（未变更） |
| 审查执行环境 | Go 1.24.1 linux/amd64；Docker 29.6.2-7；`mysql:8.0` + `redis:7-alpine`（本轮重新 `make deps-start` 拉取启动）；隔离验证使用 `/tmp/cleaner-cl001` worktree + 隔离库 `cleaner_verify_db`，均已删除 |

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | 报告 §0 记录仓库/分支/40 位 SHA/工作区状态，并显式声明锚点自引用与等价核验命令（报告 9–31 行）。`git rev-parse 7ba5a31…` 为真实提交且是 C1 祖先；`git diff --name-only 7ba5a31 HEAD` 仅出现报告与任务目录 artifact；`git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` 为空。`git diff --stat 8540230 7ba5a31` 复现「2 files, +124」与报告一致。字面 `git rev-parse HEAD` 逐字符比对因报告自身落盘必然前移，按报告 §0 与 Owner 指定口径用代码等价 diff 判定 |
| AC-002 | PASS | §2–§3 覆盖 J0–J4、S1–S15、C0–C9、Q1–Q2、RC1–RC2、A1–A2 逐跳文件:行与符号。§3.4 六条 grep 逐条复跑命中：`redis.go:207`、`consume.go:84`、`flashsale.go:532`、`request.go:62`、`routes_frontend.go:106` / `api/...:105`、`scanner.go:69`。另核对 `CreateOrder` `:266`、`insertOrderInTx` `:463`、`GetOrderResult` `request.go:117`、`RepairRequest` `repair.go:41`、`ReconcileCache` `redis.go:382`、`classifyConsumeError` `consume.go:226` 与报告一致 |
| AC-003 | PASS | §4 列出五表、状态取值、事务边界、唯一约束/索引、条件更新与不变量映射。逐项核对 migration：`15:18,26,27`（status / `uk_request_idempotency` / `idx_dequeue`）、`11:20-21,56-58`（`idx_status`/`idx_time`、三个订单唯一键）、`11:29-30`（`INT UNSIGNED`）、`18:18-19`（`idx_request_id`/`idx_operator`）均与报告一致；请求表确无 `(activity_id, sku_id, user_id)` 唯一键（支撑 R2） |
| AC-004 | PASS | §5 按「承担/不承担」判定请求表职责，领取 SQL、并发保护、业务/技术失败分类、退避、`retry_count=4` 落 `dead`、无 `processing`/无租约、事务回滚重领、人工修复均可在源码定位；`docs/design/flash-sale.md:88,131,160,167,207-208,224,242,339` 原文逐条对照 D1–D7 相符（本次抽查确认） |
| AC-005 | PASS | §6 以 E1–E14 记录命令、退出码、状态与输出，并单列 `NOT_EXECUTED`。独立复跑 `go build`、`go vet`、`make test-unit`、`make test-race`、`make test-prometheus`、`make test-container`、`make test-integration`、`make test` 全部 exit 0，与报告一致；`promtool`/`nginx -t` 跳过与 `NOT_EXECUTED` 标注一致。CLEAN-002 的计数修正经独立清点复核为 20 |
| AC-006 | PASS | §7 四分区完整：F1–F17（17）、I1–I7（7）、U1–U7（7）、R1–R11（11），与 §9 计数一致。抽查 F3/F6/F8/F9/F10/F11/F13/F16/F17 均有真实代码、SQL、配置或运行锚点；未执行项在 §6.2 与 §10.3 标 `NOT_EXECUTED`；§7.1 未见 README / 设计意图表述（设计文档仅在 §5.3 用于差异对照） |
| AC-007 | PASS | §8 给出 EV-01～EV-15 可复核锚点与 14 个待专项核查问题（新增第 14 项为 MySQL 库存兜底覆盖缺口）；明确 T02/T03 为 Owner 后续任务编号，未推断其范围或结论 |
| AC-008 | PASS | C1 只修改报告（`git diff --name-status 1ca5445^ 1ca5445` 仅 1 个报告文件）；`review.target..HEAD` 仅 `state.yaml`；受保护路径 diff 为空；无新增 Kafka 代码；本任务提交不含业务代码 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 入口 Gate `cleaner-start` | PASS | `.agent/bin/workflow-check gate cleaner-start .agent/tasks/surgecart-baseline-audit-v1`（exit 0；binary 由 `.agent/engine` 现场构建） |
| AC-008 范围 diff | PASS | `git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` → 空 |
| 报告 §3.4 抽查命令 ×6 | PASS | 命中行号 207 / 84 / 532 / 62 / 106、105 / 69，与报告一致 |
| `go build ./...` | PASS | exit 0，无输出 |
| `go vet ./...` | PASS | exit 0，无输出 |
| `make test-unit` | PASS | exit 0；复现 `internal/cmd 0.005s [no tests to run]`，证实 F16/R5 与 E3' |
| `make test-race` | PASS | exit 0；`internal/logic/flashsale 1.041s` |
| `make test-prometheus` | PASS（含跳过） | exit 0；`promtool`/`prometheus` 镜像缺失 WARN，与 E7 `NOT_EXECUTED` 一致 |
| `make test-container` | PASS | exit 0；Dockerfile / compose / 凭据分离 / 编排断言全过 |
| `make test-integration` | PASS | exit 0；`flashsale 3.702s`、`worker 0.006s`、`test/e2e 90.317s`、`migrations 27.615s` |
| `make test`（全量） | PASS | exit 0；`check-registry.sh` 输出「未发现 Reservation 重复或 Registry ↔ 实现明显不一致」；`nginx -t` 因镜像缺失跳过，与 E9/E10 一致 |
| e2e 用例清点（CLEAN-002 修复核验） | PASS | 物理分布 `flashsale_test.go 10`、`flashsale_v2_test.go 5`、`flashsale_v3_test.go 3`、`flashsale_v5_test.go 2`；`TestFlashSale*` 去重共 20；`TestFlashSaleV3FailClosed` 位于 `flashsale_v2_test.go:253`，与报告「10+4+4+2 = 20、v2 文件含 1 个 V3 用例」一致 |
| 测试资产计数（§6.4 其余行） | PASS | 白盒 `consume 4 + recovery 7 + capacity 5 + redis 2 + consume_metrics 1 = 19`；纯内存 `circuit_breaker 2 + order_outcome 1`；`order_outcome` 表驱动 14 个用例；`internal/worker/scanner_test.go` 1 个 |
| 隔离 Mutation M-MySQL（报告新表述核验） | PASS（测试仍通过） | worktree `7ba5a31`：`flashsale.go:499` `>=` → `>`、`:532` `sold < total_stock` → `1 = 1`；`TestFlashSaleConcurrentNoOversell` → `ok 0.906s`。证实报告 §6.4「同时移除预检与条件扣减后该用例仍会通过」属实 |
| 隔离 Mutation M-Redis（对照） | PASS（按预期失败） | 同一 worktree：`redis.go:122`/`:128` 判断改为 `if false` → `FAIL flashsale_test.go:517: successful enqueues=20 want 5`（0.803s），证明该用例对准入层敏感 |
| Mutation 清理 | PASS | `/tmp/cleaner-cl001` 已 `worktree remove --force` + `prune`；隔离库 `cleaner_verify_db` 已 DROP；主工作区 `git status --short` 为空 |
| 真实库 Schema 抽查 | PASS | 报告 §4.1/§4.4 的键名（`uk_request_idempotency`、`idx_dequeue`、三个订单唯一键）与 migration 原文及 e2e 迁移后的库一致 |
| Registry ↔ Contract ↔ 实现 | PASS | 本任务未新增错误码与 migration；`check-registry.sh` 通过；`state.resources.reservations={}` |

## Findings

### CLEAN-001：报告高估并发 e2e 测试对「MySQL 兜底」的覆盖（已修复）

- Severity：P3
- Status：**CLOSED**
- Location：`docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md:310`（§6.4 关键测试入口第 1 条）；修复登记于 `:434`（§8.2 第 14 项）
- AC / Invariant：AC-005 / AC-006；INV-001/002
- 原触发条件：在隔离环境同时削弱 MySQL 侧库存预检与条件扣减，再运行报告指定的 `TestFlashSaleConcurrentNoOversell`
- 修复后核验：C1 把表述改为「覆盖『Redis 准入 + 最终 MySQL 结果断言』」，明确「无法独立证明 MySQL 库存预检与条件扣减的正确性」，并说明原因（Redis 只放行 5 条，MySQL 侧无超额并发），登记为 §8.2 第 14 项待 T02 核查
- 本轮独立证据：隔离 worktree（`7ba5a31`）+ 隔离库上重复同一 Mutation → `ok 0.906s`（仍通过），报告新表述与实际行为一致；未变异基线 `ok 2.619s`；Redis 闸门 Mutation → `FAIL successful enqueues=20 want 5`
- 结论：原触发条件与事实陈述均已对齐，Finding 关闭

### CLEAN-002：e2e 秒杀用例数量与分组被少计（已修复）

- Severity：P3
- Status：**CLOSED**
- Location：`docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md:264`（E12）、`:305`（§6.4）、`:448`（§9 AC-007 行）
- AC / Invariant：AC-005 测试基线记录准确性
- 修复后核验：E12 改为「20 个 `TestFlashSale*` 全部 PASS」；§6.4 改为「10+4+4+2 = 20」，并标注 `flashsale_v2_test.go` 含 1 个 V3 用例、按用例语义分组；§9 同步为 14 个待核查问题
- 本轮独立证据：`grep -c '^func TestFlashSale'` 逐文件 = 10/5/3/2；`grep -rho` 去重 20；`TestFlashSaleV3FailClosed` 物理位于 `flashsale_v2_test.go:253`；全量 `make test-integration` 中 `test/e2e 90.317s` 全绿，20 个 `TestFlashSale*` 均通过
- 结论：计数与分组已与实际一致，Finding 关闭

### 非阻塞备注（非 Finding，供 Owner / 后续任务参考）

- AC-001 的「SHA 与 `git rev-parse HEAD` 逐字符一致」在报告落盘后必然不再成立；报告 §0 已显式说明自引用并给出等价命令，受保护路径 diff 为空，故按 Owner 指定口径判 PASS。报告 §0 中「`git diff --name-only` 期望只出现报告与 `state.yaml`」这一注释，在 Cleaner artifacts 落盘后还会额外出现 `findings.md` / `core-logic.md` / `owner-decision.md` / `loop-ledger.md`；这些都在任务目录内，属 review-neutral，不影响结论。
- F4 的「不预扣」应读作「EVAL 未执行」；若脚本已在服务端执行而客户端未收到响应，可能残留预扣/标记。该残留类别已由 R3（孤儿标记不重跑闸门）与 U4（崩溃窗口频率）覆盖，并有 60s 对账收敛；不影响 F4 的错误码与请求不落库结论，建议 T02 按此口径复核。
- 报告已登记 11 条风险（R1–R11），按 Task 约定本轮只记录不修复；「未修复」本身不构成 Finding。

## Conclusion

- 全部 AC 有可复核证据，PASS 8/8。
- 两条历史 P3（CLEAN-001、CLEAN-002）经本轮独立证据确认已修复并关闭；无开放 P0/P1/P2。
- `review.target` 保持 C1 `1ca5445ef3d9cc04a7f11f78eb44638376d1eb78`；CLEAN 只对该快照成立。
