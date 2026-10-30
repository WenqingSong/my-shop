# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 / 路线 | `surgecart-baseline-audit-v1` / `NORMAL`（`contract.status=NOT_REQUIRED`，`resources.reservations={}`） |
| 仓库 / 分支 | `origin` = `https://cnb.cool/framework-station/my-shop`；`task/surgecart-baseline-audit-v1` |
| 任务基线 | `85402302622d059918e1a87b83bbb55b400d1a85`（`develop`） |
| **C1（审查对象）** | `757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb` — 仅新增 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`（468 行，`git show --stat` 已核） |
| C2（metadata） | `10019625a40be783163fdc80a7dd9e14ee8378b9` — 仅 `state.yaml`（`review.status=PENDING`、`review.target=C1`） |
| **被审计代码版本** | `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`（C1 的父提交；`git log -1 --format=%P 757ed22` 已核） |
| 审查开始时 Git 状态 | 工作区 clean；本地 HEAD == `origin/<branch>` == `10019625…`（`git ls-remote` 已核） |
| 相关已跟踪 / 新增文件 | 新增 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`；修改 `.agent/tasks/surgecart-baseline-audit-v1/state.yaml` |
| 任务前已有修改 | 无。`7ba5a31..C1` 只含报告；`C1..C2` 只含 `state.yaml` |
| 关键配置 / 迁移版本 | `manifest/config/config.yaml`（四个 `enabled: false`）；migration `20261001000011/15/18`（未变更） |
| 审查执行环境 | Go 1.24.1 linux/amd64；Docker 29.6.2-7；`mysql:8.0` + `redis:7-alpine`（本机无镜像时由 `make deps-start` 拉取并启动）；隔离验证使用 `/tmp` 临时 worktree，已删除 |

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | 报告 §0 记录仓库/分支/40 位 SHA/工作区状态，并显式声明锚点自引用（报告 20–31 行）。`git rev-parse 7ba5a31…` 为真实提交且是 C1 父提交；`git diff --name-only 7ba5a31 HEAD` → 仅 `M .agent/tasks/surgecart-baseline-audit-v1/state.yaml`、`A docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`；`git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` → 空。字面 `git rev-parse HEAD` 逐字符比对因报告自身落盘必然前移，按 Owner 指定口径用代码等价 diff 判定，结论成立。 |
| AC-002 | PASS | §2–§3 给出 J0–J4、S1–S15、C0–C9、Q1–Q2、RC1–RC2、A1–A2 逐跳文件:行与符号，覆盖入口路由、参数校验、限流/容量、Redis Lua 准入、入队、消费者领取、建单、扣减、结果查询、对账/修复。§3.4 六条 grep 全部逐字复跑命中：`redis.go:207`、`consume.go:84`、`flashsale.go:532`、`request.go:62`、`routes_frontend.go:106` 与 `api/...:105`、`scanner.go:69`。另抽查 `cmd.go:135-139`、`repair.go:78-107`、`consume.go:79-85/195-222` 均一致。 |
| AC-003 | PASS | §4 列出五表、状态取值、事务边界、唯一约束/索引与条件更新，并逐项映射不变量。migration 行号逐项核对一致（`11:11-22/24-37/39-61/56-58/29-30/20-21/35/59-60`；`15:11-28/18-22/26-27`；`18:7-20/18-19`）；真实库 `SHOW CREATE TABLE` 与 §4.1/F9 完全一致（`uk_request_idempotency`、`idx_dequeue`、`uk_flash_one_per_user`、`uk_flash_idempotency`、`uk_flash_order_no`）。 |
| AC-004 | PASS | §5 按「承担/不承担」判定请求表职责，并给出领取 SQL（`consume.go:79-85`）、并发保护、业务/技术失败分类（`:226-254`）、退避 5/10/20s（`:257-266`）、`retry_count=4` 落 `dead`（`:197-209`）、无 `processing`/无超时回收、事务回滚重领、人工修复（`repair.go`）。与 `docs/design/flash-sale.md` 的差异 D1–D7 逐条可在 `:88/:131/:160/:167/:207-208/:224/:242/:339` 找到对应原文，描述相符。 |
| AC-005 | PASS | §6 以 E1–E14 记录命令、退出码、状态与关键输出，并单列 `NOT_EXECUTED`。独立复跑 `go build`、`go vet`、`make test-unit`、`make test-integration`、`make test-race`、`make test-prometheus`、`make test-container`、`make test` 全部 exit 0，与报告一致；`promtool`/`nginx -t` 跳过与 `NOT_EXECUTED` 标注一致；E12 复现 exit 1 且失败点/原因与报告一致。测试数量表述有误，见 CLEAN-002（不影响命令、退出码与状态结论）。 |
| AC-006 | PASS | §7 四分区完整：已验证事实 F1–F17（17）、推断 I1–I7（7）、未知 U1–U7（7）、风险 R1–R11（11），与 §9 计数一致。抽查 F3（Lua 顺序）、F6（SKIP LOCKED）、F8（条件扣减）、F9（唯一键）、F10（退避/死信）、F11（权威值收敛）、F16（单元门禁空转）均有真实代码或运行锚点。§7.1 未混入 README/设计意图；未执行项在 §6.2/§10 标 `NOT_EXECUTED`。 |
| AC-007 | PASS | §8 给出 EV-01～EV-15 可复核锚点与 13 个待专项核查问题，覆盖一致性、恢复、容量、文档与测试门禁；明确 T02/T03 为 Owner 后续任务编号，未推断其范围、路线或结论。 |
| AC-008 | PASS | C1 只新增报告 468 行；C2 只改 `state.yaml` 2 行；`git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` 为空；无新增 Kafka 代码；`git log` 中本任务提交不含业务代码。 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 入口 Gate `cleaner-start` | PASS | `.agent/bin/workflow-check gate cleaner-start .agent/tasks/surgecart-baseline-audit-v1`（exit 0） |
| AC-008 范围 diff | PASS | 受保护路径 `git diff --stat 7ba5a31 HEAD -- …` 输出为空；`git diff --name-status` 仅报告 + `state.yaml` |
| 报告 §3.4 抽查命令 ×6 | PASS | 逐条执行，命中行号 207 / 84 / 532 / 62 / 106、105 / 69，与报告一致 |
| `go build ./...` | PASS | exit 0，无输出 |
| `go vet ./...` | PASS | exit 0，无输出 |
| `make test-unit` | PASS | exit 0；`internal/cmd 0.006s [no tests to run]` 复现，证实 F16/R5 |
| `make test-integration` | PASS | exit 0；`internal/logic/flashsale 3.947s`、`internal/worker 0.010s`、`test/e2e 93.904s`、`internal/migrations 28.396s` |
| `make test-race` | PASS | exit 0；`ok … 1.045s` |
| `make test-prometheus` | PASS（含跳过） | exit 0；`promtool` 缺失 WARN，与 E7 `NOT_EXECUTED` 一致 |
| `make test-container` | PASS | exit 0；静态断言全过 |
| `make test`（全量） | PASS | exit 0；registry 校验「未发现 Reservation 重复或 Registry ↔ 实现明显不一致」；`nginx -t` 因镜像缺失跳过，与 E9/E10 一致 |
| E12 单包复跑 | PASS（按预期失败） | `go test -count=1 -run 'FlashSale' ./test/e2e/...` exit 1；唯一失败 `TestDashboardFlashSales` @ `dashboard_test.go:587`，`DELETE FROM products` 被 `fk_skus_product` 阻止；全部 `TestFlashSale*` 通过，与报告 E12/R11 归因一致 |
| 真实库 Schema | PASS | `SHOW CREATE TABLE flash_sale_order_requests` 与 §4.1 一致；索引/唯一键查询命中 F9 所列键名 |
| 隔离 Mutation M1（破坏 Redis 闸门） | 预期失败 | 临时把 `redis.go:122/128` 判断改为 `if false`，`TestFlashSaleConcurrentNoOversell` FAIL（`successful enqueues=20 want 5`），证明测试对准入层敏感 |
| 隔离 Mutation M2（破坏 MySQL 兜底） | **仍通过（缺口）** | 临时把 `flashsale.go:499` 的 `>=` 改为 `>`、`:532` 的 `sold < total_stock` 改为 `1 = 1`，同测试仍 PASS（0.795s），说明测试不能单独检测 MySQL 兜底被移除 → CLEAN-001 |
| Mutation 清理 | PASS | 两个 `/tmp/cleaner-mut*` worktree 已 `worktree remove`；主工作区 `git status --short` 为空，无遗留 |

## Findings

### CLEAN-001：报告高估并发 e2e 测试对「MySQL 兜底」的覆盖

- Severity：P3
- Status：OPEN
- Location：`docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md:310`（§6.4 关键测试入口第 1 条）
- AC / Invariant：AC-005 / AC-006 的测试基线准确性；INV-001/002（库存不为负、成功订单数 ≤ 初始库存）
- Trigger：在隔离 worktree 同时移除 MySQL 侧两道防超卖判断（`insertOrderInTx` 的 `sold >= total_stock` 预检与 `UPDATE … WHERE sold < total_stock` 条件），再运行报告指定的 `TestFlashSaleConcurrentNoOversell`
- Actual：测试仍 PASS（0.795s）。该用例的 20 个并发请求中只有 5 个通过 Redis Lua 闸门进入队列，MySQL 侧从未出现超过 `total_stock` 的并发写入；移除 MySQL 守卫不影响「受理数=5、sold=5、订单数=5」断言
- Expected：报告应把该用例的覆盖范围表述为「Redis 准入 + 最终 MySQL 结果断言」，并登记「没有测试能在 MySQL 侧独立检测条件扣减/预检被移除」这一覆盖缺口（风险或 T02/T03 待核查项），而不是不加限定地写「同时覆盖…MySQL 兜底」
- Impact：下游 T02/T03 可能据此认为 MySQL 最终防线已有回归测试保护；实际上该防线的正确性目前只能靠代码阅读与 F8 锚点确认。生产代码守卫本身完整，风险在于证据强度被高估
- Evidence：隔离 worktree `757ed22` 上两处改动 → `go test -count=1 -run TestFlashSaleConcurrentNoOversell ./test/e2e/...` → `ok … 0.795s`；对照 M1（仅破坏 Redis 闸门）→ `FAIL: successful enqueues=20 want 5`。主工作区未改动，worktree 已删除
- Required Fix Boundary：修正 §6.4 该句的覆盖表述，并把「MySQL 条件扣减/库存预检缺少可区分的回归测试」登记为风险或待核查问题；不得修改生产代码或测试（Task Out of Scope），不得改动既有锚点行号

### CLEAN-002：e2e 秒杀用例数量被少计 1（19 vs 20）

- Severity：P3
- Status：OPEN
- Location：`docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md:264`（E12）、`:305`（§6.4 测试资产分布）
- AC / Invariant：AC-005 测试基线记录准确性
- Trigger：`grep -rho '^func TestFlashSale[A-Za-z0-9_]*' test/e2e/*.go | sort -u` 统计实际用例
- Actual：实际 20 个 `TestFlashSale*`（V1 10、V2 4、V3 4、V5 2）；`test/e2e/flashsale_v2_test.go` 含 5 个用例（其中 `TestFlashSaleV3FailClosed` 属 V3）。报告写「10+4+3+2 = 19」，E12 亦写「19 个 `TestFlashSale*` 全部 PASS」
- Expected：计数与分组准确，或按实际文件归属列出（v2 文件 5 个）
- Impact：仅影响测试资产计数的可复核性；E12「全部通过」的实质结论成立（独立复跑确认所有 `TestFlashSale*` 通过，唯一失败为 `TestDashboardFlashSales` 的夹具 FK 清理）
- Evidence：`grep -c '^func Test' test/e2e/flashsale_v2_test.go` → 5；全部 `TestFlashSale*` 名称去重共 20 个；E12 复跑日志仅 `TestDashboardFlashSales` 失败
- Required Fix Boundary：更正两处计数/分组，不改动其它结论与锚点

### 非阻塞备注

- AC-001 的「SHA 与 `git rev-parse HEAD` 逐字符一致」在报告落盘后必然不再成立（自引用）；报告已在 §0 显式说明并给出等价核验命令，本次按 Owner 指定口径判定为 PASS。
- F4 的「不预扣」应读作「`EVAL` 未执行」；若脚本已在服务端执行而客户端未收到响应，可能残留预扣/标记，该残留类别已由 R3 与 U4 覆盖并有对账收敛，不影响 F4 的错误码与请求不落库结论，建议 T02 按此口径复核。

## Conclusion

- 全部 AC 有可复核证据，PASS 8/8。
- 无开放 P0/P1/P2；两项 P3 为报告文本/计数精度问题，是否修正由 Owner 决定。
- `review.target` 保持 C1 `757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb`；CLEAN 只对该实现快照成立。
