# Cleaner Findings

## Review Target

- Task：`project-baseline-cleanup-v1`
- Base commit（任务基线）：`359b8cc`（`chore/project-baseline-cleanup-v1` 起点，等于 `develop` 最新合并点）
- Implementation Evidence Commit C1：`6bbf38793df9a60da597e4d64f1f27b850db7cdb`
  - 完整实现 = `4a88ba2`（Registry 状态翻转 + migration 清单） + `6bbf387`（AC-007 修复：README 记录废弃目录/脚本引用核验结论）
- C2（metadata commit）：`f31849e`（`state.yaml` 写 `review.status=PENDING`、`review.target=6bbf387`，仅改 1 行）
- 审查对象：**C1 = 6bbf387**（其相对基线的完整实现变更），非 C2、非当前 working tree。
- 基线区分方式：`git diff 359b8cc 6bbf387` 为完整实现变更；任务开始前 working tree clean。
- 完整实现变更文件（5 个）：
  - `.agent/registry/migrations.md`（00009–00023 转 ACTIVE）
  - `.agent/registry/error-codes.md`（10000–19999 转 ACTIVE）
  - `docs/design/migration.md`（补齐 00009–00018、00020–00023 清单）
  - `docs/design/deployment.md`（`my-shop-app`→`my-shop-app:local`）
  - `README.md`（新增「历史遗留目录与脚本保留说明」，AC-007 证据）

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `migrations.md` 00009–00023 由 `RESERVED` 改 `ACTIVE`；23 个 `.up.sql` 全部存在且对应 feature 代码已合入 `develop` 基线 `359b8cc`（`git ls-tree` 确认 SQL 与代码均在基线）。无 version 删除/复用。 |
| AC-002 | PASS | `error-codes.md` 10000–19999（域序 10–19）改 `ACTIVE`；`internal/codes/codes.go` 实际使用各域错误码（10001、11001、12001、13001、14001、15001、16001、17001、18001、19001 等），区间无重叠（check-registry 退出码 0）。 |
| AC-003 | PASS | `docs/design/migration.md` §2.1 含 00001–00023 共 23 行，version/title 与 23 个 `.up.sql` 文件名、`migrations.md` 逐一比对一致，无遗漏/多余/title 漂移。 |
| AC-004 | PASS | 独立执行 `./scripts/check-registry.sh`，退出码 0。 |
| AC-005 | PASS | README「能力边界」明确记录 Mock 支付、秒杀独立下单不走普通支付/取消、MySQL 出队表无外部 MQ、无稳定性能报告、无 K8s 结论；Cleaner 对照 `internal/logic/order/order.go`（Pay Mock）、`internal/logic/flashsale/consume.go`（出队表 `FOR UPDATE SKIP LOCKED`）、`go.mod`（无外部 MQ）核实一致。 |
| AC-006 | PASS | `deployment.md` 服务清单/镜像名/健康检查与 `docker-compose.yml` 一致（C1 修正 `my-shop-app:local`）；Go 版本（builder 1.23）与 `Dockerfile`（`golang:1.23-alpine`）、`go.mod`（`go 1.23.0`）一致；`.cnb.yml` `golang:1.24` 与 README「开发使用 1.24」表述一致，非漂移；`/metrics` 开关语义与 `docker-compose.yml`、`prometheus/prometheus.yml` 一致。 |
| AC-007 | PASS（复审关闭 CLEAN-001） | README 新增「历史遗留目录与脚本保留说明」，逐项记录引用核验结论：`scripts/import-legacy.sh` 仍被 `docs/legacy-data.md` §6（第 60 行）引用 → 保留；`docs/学习/` 为 Project Mastery 学习产物 → 保留；`docs/tasks/` 为 V3 前旧任务产物、仅被自身引用且未在 `.agent/tasks/` 重复收录 → 保留。三项声明均经 Cleaner 独立核实属实；无其他明显废弃项遗漏。 |
| AC-008 | PASS | README「当前实现」表 +「能力边界」覆盖已实现模块、两条核心交易链路与未覆盖边界，Cleaner 对照代码与 `docs/design/*` 核实一致。 |
| AC-009 | PASS | 独立执行 `go build ./...`（0）、`go vet ./...`（0）、`./scripts/check-registry.sh`（0）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 完整实现 Diff 审阅（基线→C1） | 完成 | `git diff 359b8cc 6bbf387`（5 文件）逐行审阅；初审查 4a88ba2 四文件，复审核对 6bbf387 README 新增章节。 |
| Registry ↔ 实现三边一致性 | 通过 | 23 个 `.up.sql` 均登记且 ACTIVE；codes.go 各域实际使用、区间无重叠。 |
| `./scripts/check-registry.sh` | 退出码 0（初查 + 复审重跑） | 无重复/重叠/漂移/越域。 |
| `go build ./...` / `go vet ./...` | 退出码 0 | 无编译/静态检查回归（修复仅改 README，不影响构建）。 |
| AC-007 修复声明准确性 | 通过 | 三处保留结论逐一 grep/读文件核实属实；`import-legacy.sh` 引用确认 `docs/legacy-data.md:60`；`.agent/tasks/` 无 iam-v1/make-lifecycle/project-init；`docs/学习/README.md` 声明用途。 |
| 核心链路表述核实 | 通过 | Mock 支付 / 秒杀独立下单 / MySQL 出队表 / 无外部 MQ 均与实现一致。 |

## Findings

### CLEAN-001：废弃目录/脚本未按 AC-007 做引用核验且无证据留痕

- Severity：P2
- Status：CLOSED
- 复审结论：Coder 于 `6bbf387` 在 README 新增「历史遗留目录与脚本保留说明」，逐项给出 `scripts/import-legacy.sh`、`docs/学习/`、`docs/tasks/` 的引用核验结论与保留原因，补齐 AC-007 证据。Cleaner 独立核实三项声明均属实（引用与用途核对无误），无其他明显废弃项遗漏。原触发条件已消除。
