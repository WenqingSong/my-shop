# Cleaner Findings

## Review Target

- 任务基线：Base commit `bba7578ea9219e43bb24efb2b48fa994974aa523`（分支 `develop`）。
- 当前对象版本：HEAD = `bd63282bad15e045fbab8c81953721ec4503c279`（`docs(workflow): 收敛 V1 Validator 能力边界并统一 phase 术语`），叠加工作区未提交的第四轮修复（8 文件：`.agent/tasks/agent-workflow-state-machine-v1/contract.md`、`cmd/workflow-check/main.go`、`docs/design/agent-workflow.md`、`internal/workflow/{git_integration_test,paths,state_test,validator,validator_test}.go`）。
- 第四轮 Contract Revision（响应 CLEAN-003/004，Owner APPROVED）+ Design Sync + Coder 修复：Review Validity 改为真 default-deny；`review.target_paths` 降级为 Evidence/Audit Record；Resource Authority 默认 `origin/develop`。
- 完整变更集（自 `bba7578` 起，含已提交 + 未提交）：`cmd/workflow-check/*`、`internal/workflow/*`、`docs/design/agent-workflow.md`、`docs/agent/*`、`.agent/registry/{error-codes,migrations}.md`、`scripts/check-registry.sh`、`go.mod`、`.agent/tasks/agent-workflow-state-machine-v1/*`。
- 全局资源：本任务不占用错误码域 / migration version。
- 备注：本任务为 State Machine V1 生效前任务（Legacy），无 `state.yaml`；按 Cutover Rule 不创建/不回填 `state.yaml`，CLEAN 绑定的 review target 以本文件记录。

## Result

CLEAN

## Current State

- Cleaner Result：`CLEAN`（CLEAN-003/004 均已 CLOSED，无开放 P0/P1/P2）。
- `review.status`：`CLEAN`（绑定本 Review Target）。
- `Owner Verification Status`：`PENDING`（见 `core-logic.md` 顶部；Owner 需对修复后版本重新执行 CL-001/CL-002 核心验证并决定是否 ACCEPT）。
- Task lifecycle route：`WAITING_FOR_OWNER_ACCEPTANCE`。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `state.yaml` schema 为唯一机器权威源；`Validator` 只读 `phase` 判定；无「Markdown 第一行 / Git HEAD 唯一来源」路径。 |
| AC-002 | PASS | 13 值 phase 集 + 正交子事实区分明确；`TestPhaseSetHasExactly13Values` 断言恰 13 值。 |
| AC-003 | PASS | `transitionAuthority` 18 条、每条唯一 Decision Authority、文件写入者 ≠ Decision Authority；`TestTransitionAuthorityCompleteness`。 |
| AC-004 | PASS | Owner 唯一决策来源；`ACCEPTED` 仅 Owner 明确指令 → Agent 机械持久化；各角色 Prompt 固化。 |
| AC-005 | PASS | `review.target_base`/`target_paths` 绑定 CLEAN；白名单含 `findings.md`/`core-logic.md`/`delivery.md`/`state.yaml`；`TestCleanReviewNeutralNoStale`。 |
| AC-006 | PASS | 真 default-deny 实现：`isSubstantialChange = !isReviewNeutral`；`TestCleanStaleProductionCode/BusinessTest/ContractDesign` + `TestCheckReviewValidity`（target_paths 外非白名单亦 STALE）。 |
| AC-007 | PASS | `docs/agent/models/CleanerAgent.md`「Mutation 边界」：隔离环境、不污染、不 commit、不入交付。 |
| AC-008 | PASS | Resource Authority 默认 `origin/develop`（shared）；`TestResourceAuthorityLocalDevelopReservedInvalid` 证明 local develop 独有 Reservation → FAIL。 |
| AC-009 | PASS | `RESERVED` 定义修正；落点 `.agent/registry/*`、协作规范 §11.4、`check-registry.sh`。 |
| AC-010 | PASS | Deliverer Gate；Validator 拒绝 `DELIVERING+owner_verification=PENDING` 与 `DELIVERING+review!=CLEAN`。 |
| AC-011 | PASS | 四个最小无效状态全部被拒绝且测试覆盖。 |
| AC-012 | PASS | 四者一致：Contract S4/S6/S7 ↔ Design §5/§6.2/§8 ↔ Implementation ↔ Tests 对 default-deny 与 shared develop Authority 完全一致；无旧规则残留。 |
| AC-013 | PASS | Diff 未引入 DB/MQ/BPMN/Web UI/Orchestrator；既有规则未被推翻。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 无告警 |
| `go test -count=1 ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 全 ok |
| Mutation A（default-deny 反向：`isSubstantialChange` 恒 false） | FAIL（预期） | `TestPathClassification`/`TestCheckReviewValidity`/`TestCleanStaleProductionCode`/`TestCleanStaleBusinessTest`/`TestCleanStaleContractDesign` 均失败，证明测试能捕获 default-deny 回退 |
| Mutation B（Authority fallback：`loadDevelopRegistry` 改读本地 `develop`） | FAIL（预期） | `TestResourceAuthorityLocalDevelopReservedInvalid` 失败，证明测试能捕获 shared→local 回退 |
| 隔离环境 | PASS | Mutation 在 `/tmp/wfmutate`（复制副本）执行，未触碰 Review Target working tree，事后已清理 |
| `grep` must-trigger / 旧语义残留 | PASS | `isMustTrigger`/`mustTrigger*` 已从代码移除；S4/§5 已改「不依赖 must-trigger 黑名单」 |

## Findings

### CLEAN-001：Contract/Design 声称「phase 转换是否在允许表内」「乱序写入被拒」，但 Validator 未校验转换顺序

- Severity：P2
- Status：CLOSED
- Resolution：第三轮 Contract Revision（Owner APPROVED）移除过度声明，明确 V1 只校验当前状态与 Gate。

### CLEAN-002：Coder/Deliverer 交接词 `READY_FOR_CLEANER` 与状态机 phase `READY_FOR_REVIEW` 未完全收敛

- Severity：P3
- Status：CLOSED
- Resolution：`docs/agent/*` 已统一为 `READY_FOR_REVIEW`。

### CLEAN-003：Review Validity 实际为 default-allow，违反批准的 default-deny 语义

- Severity：P1
- Status：CLOSED
- Resolution：第四轮 Contract Revision（Owner APPROVED）+ Design Sync + Coder 修复。`internal/workflow/paths.go` 现为 `isSubstantialChange(p) = !isReviewNeutral(p)`（真 default-deny）；`checkReviewValidity` 不再接收 `target_paths`；`review.target_paths` 仅在 `State` schema 中保留为 Evidence/Audit Record，不再参与 STALE 判定。测试更新：`TestPathClassification`（README/docs/agent → substantial=true）、`TestCheckReviewValidity`（target_paths 外非白名单 → stale=true）。Mutation A 验证测试能捕获 default-deny 回退。

### CLEAN-004：Resource Authority 默认读取 local develop，而非 shared develop

- Severity：P1
- Status：CLOSED
- Resolution：`cmd/workflow-check/main.go` 的 `--develop-ref` 默认值改为 `origin/develop`（remote-tracking ref）；`loadDevelopRegistry` 经 `git show <DevelopRef>:path` 读取，无 fallback 到本地 `develop`、无 `git fetch`（`git.go` 仅 `diff --name-only`/`ls-files --others`/`show`/`cat-file -e`，全部只读）；remote ref 不可读时 `ShowFile` 返回错误 → `ValidateTask` 返回 exit 2（明确失败）。新增集成测试 `TestResourceAuthorityLocalDevelopReservedInvalid`（local develop 私留、origin/develop 无 → FAIL）与 `syncOriginDevelop` helper。Mutation B 验证测试能捕获 shared→local fallback。

### CLEAN-005：Contract Open Risks 残留旧语义表述

- Severity：P3
- Status：OPEN
- Location：`.agent/tasks/agent-workflow-state-machine-v1/contract.md`「Open Risks」节
- AC / Invariant：AC-012（四者一致，文档层面）
- Trigger：读者阅读 Open Risks 时看到「`review.target_paths` 的精确性影响 STALE 判定：过宽误报、过窄漏报」，与第四轮修订后的 S4（target_paths 不参与 STALE 判定）相矛盾。
- Actual：该句仍描述旧 default-allow 语义；新语义下 `target_paths` 宽度不影响 STALE（default-deny 已覆盖一切非白名单变化）。
- Expected：更新为与 S4 一致（如「`review.target_paths` 仅作审计记录；Cleaner 须如实完整记录被审查文件集，但 STALE 判定不依赖其范围」）。
- Impact：低；仅文档残留，无行为影响。
- Required Fix Boundary：删除或改写该句，使 Open Risks 与 S4 一致。由 Owner 决定是否处理。

### CLEAN-006：`--develop-ref` 默认值缺少直接单元测试

- Severity：P3
- Status：OPEN
- Location：`cmd/workflow-check/main.go`（默认 `origin/develop`）、`cmd/workflow-check/main_test.go`
- AC / Invariant：AC-008（Resource Authority 默认 shared develop）
- Trigger：若有人把 `--develop-ref` 默认值改回 `develop`，现有测试不报错（`main_test.go` 未覆盖 resource authority 路径；集成测试直接构造 `Validator{DevelopRef:"origin/develop"}`，不经过 flag 默认值）。
- Actual：默认值正确（`origin/develop`），但该一行默认值无断言保护。
- Expected：可选地增加一个断言默认 ref 的测试，或一个经 `run()` 走 flag 默认值的资源场景测试。
- Impact：低；行为已在集成层覆盖，仅 flag 默认值这一薄层未被直接保护。
- Required Fix Boundary：补默认值断言或经 `run()` 的资源场景测试。由 Owner 决定是否处理。

## 开放 Finding 汇总

- CLEAN-005（P3，OPEN）、CLEAN-006（P3，OPEN）——低风险，由 Owner 决定，不阻塞 CLEAN。
- CLEAN-001/002/003/004 —— CLOSED。

无开放 P0/P1/P2。
