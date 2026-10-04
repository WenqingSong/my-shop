# Cleaner Findings

## Review Target

- Base commit：`bba7578ea9219e43bb24efb2b48fa994974aa523`（分支 `develop`，`git rev-parse HEAD` 一致）。
- 任务开始时工作区干净（`git status --short` 为空），基线可可靠区分：本次变更 = 全部当前未提交修改 + 新增文件，无任务前既有修改需要剥离。
- 本次审查对象（已跟踪修改 + 新增文件）：

已修改：
- `.agent/registry/error-codes.md`
- `.agent/registry/migrations.md`
- `docs/agent/AgentCollaborationSpecification.md`
- `docs/agent/Five-AgentResponsibilityBoundary.md`
- `docs/agent/models/AnalystAgent.md`
- `docs/agent/models/CleanerAgent.md`
- `docs/agent/models/CoderAgent.md`
- `docs/agent/models/DelivererAgent.md`
- `docs/agent/models/TaskBuilderPrompt.md`
- `go.mod`
- `scripts/check-registry.sh`

新增：
- `cmd/workflow-check/main.go`、`cmd/workflow-check/main_test.go`
- `internal/workflow/{state,validator,errors,git,paths,registry}.go`
- `internal/workflow/{state,validator,git_integration,registry}_test.go`
- `docs/design/agent-workflow.md`
- `.agent/tasks/agent-workflow-state-machine-v1/{task,contract,findings,core-logic,delivery}.md`

全局资源：本任务不占用错误码域 / migration version；仅修正 Registry `RESERVED` 语义文本，未新增分配条目。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `state.yaml` schema（`internal/workflow/state.go`、`docs/design/agent-workflow.md` §2）为唯一机器权威源；`Validator` 只读 `phase` 判定；`grep` 确认无「以 Markdown 第一行 / Git HEAD 作为唯一状态来源」的判定路径。 |
| AC-002 | PASS | 13 值 phase 集（`allPhases`）+ 正交子事实（`review/owner_verification/delivery/blocked`）区分明确；`TestPhaseSetHasExactly13Values` 断言恰 13 值。 |
| AC-003 | PASS（定义层面） | `transitionAuthority` 18 条、每条唯一 Decision Authority、文件写入者 ≠ Decision Authority；`TestTransitionAuthorityCompleteness`。但运行时未校验转换 → 见 CLEAN-001。 |
| AC-004 | PASS | Owner 唯一决策来源；`ACCEPTED` 仅 Owner 明确指令 → Agent 机械持久化；Analyst/Cleaner/Coder/Deliverer Prompt 均已固化。 |
| AC-005 | PASS | `review.target_base`/`target_paths` 绑定 CLEAN；白名单含 `findings.md`/`core-logic.md`/`delivery.md`/`state.yaml`；`TestCleanReviewNeutralNoStale` 证明 Cleaner 自写不失效。 |
| AC-006 | PASS | Mechanical Invalidation 实现（`checkReviewValidity` + `isSubstantialChange`）；实质变化 → STALE；仅 Cleaner `STALE→CLEAN`；白名单避免死循环；`TestCleanStaleProductionCode/BusinessTest/ContractDesign` 覆盖。 |
| AC-007 | PASS（文档层面） | `docs/agent/models/CleanerAgent.md` 新增「Mutation 边界」：隔离 worktree/disposable checkout、不污染、不 commit、不入交付。 |
| AC-008 | PASS | `resourceGatePhases` + `checkResourceAuthority`（读 shared develop Registry）；`TestResourceAuthorityFeatureBranchReservedInvalid`、`TestMissingMigrationForbiddenImplementing` 证明私留 `RESERVED` / 缺 Reservation 均 FAIL。 |
| AC-009 | PASS | `RESERVED` 定义改为「Reservation 已通过只改 Registry 的 commit 落到共享 develop 生效、Feature 未合并」；落点 `.agent/registry/*`、协作规范 §11.4、`check-registry.sh` 注释。 |
| AC-010 | PASS | Deliverer Gate（`docs/design/agent-workflow.md` §7、`DelivererAgent.md`）；Validator 拒绝 `DELIVERING+owner_verification=PENDING` 与 `DELIVERING+review!=CLEAN`。 |
| AC-011 | PASS | 四个最小无效状态全部被拒绝且测试覆盖：`DELIVERING+PENDING`、`IMPLEMENTING+缺 Reservation`、`CLEAN+target 已改`、`DONE+delivery!=PASS`。 |
| AC-012 | FAIL | 旧规则残留 grep 通过（无「Owner 必须亲自落笔」/「额外交付授权」正面残留）；但「四者一致」不成立：Contract/Design 声称 Validator 校验「phase 转换是否在允许表内」「乱序写入被拒」，实现未做（CLEAN-001）；`READY_FOR_CLEANER` 与 `READY_FOR_REVIEW` 用词未完全收敛（CLEAN-002）。 |
| AC-013 | PASS | Diff 未引入 DB/MQ/BPMN/Web UI/Orchestrator（仅 `cmd/workflow-check` + `internal/workflow` + `yaml.v3` 转直接依赖）；Owner Verification 三态、`milestone` 唯一来源、Deliverer 不额外授权、Design Impact、Registry tombstone 等既有规则未被推翻。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go test ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 全 ok |
| `go vet ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 无告警 |
| `bash scripts/check-registry.sh` | PASS | 「校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致」 |
| `go run ./cmd/workflow-check` | PASS（预期） | 19 个存量任务全部 SKIPPED（无 `state.yaml`、未提供 cutover），exit 0，符合 Cutover Rule |
| `go test ./...`（全量） | 部分 FAIL（与本任务无关） | `internal/middleware` 若干测试因 MySQL 缺 `permissions`/`admins` 表失败；属环境问题，本任务未触碰生产代码，非本任务引入 |
| `grep transitionAuthority` | 定位 CLEAN-001 | 常量仅被 `state.go`（定义）与 `state_test.go`（完整性断言）引用，运行时校验未使用 |
| 全仓 grep 旧规则残留 | PASS | 无「Owner 必须/亲自编辑」「Delivery Authorization」正面残留；命中均为历史任务 contract 记录或否定语境 |

## Findings

### CLEAN-001：Contract/Design 声称「phase 转换是否在允许表内」「乱序写入被拒」，但 Validator 未校验转换顺序

- Severity：P2
- Status：OPEN
- Location：`internal/workflow/state.go`（`transitionAuthority` 常量，仅定义）vs `internal/workflow/validator.go`（无转换校验）；`docs/design/agent-workflow.md` §8 与 `contract.md` S7、Failure and Consistency Semantics
- AC / Invariant：AC-003（任何角色只能触发被授权的转换）、AC-012（四者一致）；INV-002；Contract S7「校验内容：… phase 转换是否在允许表内」、Failure「乱序写入被拒」
- Trigger：任意角色/脚本把 `state.yaml.phase` 写成非法的乱序值（例如从 `APPROVED` 直接跳到 `DONE`，并伪造 `delivery.status=PASS`、`review.status=CLEAN`、`owner_verification=ACCEPTED`）
- Actual：`transitionAuthority`（18 条转换表）只在 `state_test.go` 被用于完整性断言，运行时 `ValidateTask`/`checkPhaseCombinations` 从不读取它；`State` schema 也没有记录 previous-phase 的字段，因此「当前 phase 是否由合法转换到达」完全无法校验。上述乱序状态只要各子字段组合合法，Validator 会判 `PASS`。
- Expected：按 Contract/Design，Validator 应校验「转换在允许表内」并「拒绝乱序写入」；或（若 schema 不支持）应从 Contract S7/Failure 与 Design §8 移除该过度声明，恢复四者一致。
- Impact：状态机核心目标之一是「使无效状态不可存在」，但「乱序/跳步状态」这一类无效状态未被机器拒绝；`transitionAuthority` 表实际为未参与校验的死数据，与 Contract/Design 的公开声明漂移。
- Evidence：`grep transitionAuthority` 仅命中 `internal/workflow/state.go`（定义）与 `state_test.go`（`TestTransitionAuthorityCompleteness`）；`State` 结构体无 `from`/`previous_phase` 字段；`validator.go` 的 `ValidateStateSchema`/`checkPhaseCombinations` 只校验枚举与固定组合，不校验转换。
- Required Fix Boundary：恢复 Task ↔ Contract ↔ Design ↔ Implementation 四者一致。两种可接受路径：(a) 在 `state.yaml` schema 增加记录前一 phase 的字段并实现转换合法性校验（会改变 S3 schema，需 Contract Revision）；(b) 从 Contract S7、Failure and Consistency Semantics 与 Design §8 删除「phase 转换是否在允许表内」「乱序写入被拒」的声明，明确 V1 Validator 只校验「13 值 phase + 子字段枚举 + 无效状态组合 + 资源 Gate + Deliverer Gate + review validity」。因涉及 APPROVED Contract/Design 语义，预计需路由 `CONTRACT_REVISION_REQUIRED`（Analyst 提案、Owner 确认）后再交 Coder。

### CLEAN-002：Coder/Deliverer 交接词 `READY_FOR_CLEANER` 与状态机 phase `READY_FOR_REVIEW` 未完全收敛

- Severity：P3
- Status：OPEN
- Location：`docs/agent/models/CoderAgent.md`（状态节）、`docs/agent/Five-AgentResponsibilityBoundary.md` §5，对比 `docs/agent/AgentCollaborationSpecification.md` 阶段关口表与 `docs/design/agent-workflow.md` §3.1
- AC / Invariant：AC-012（Prompt ↔ Spec ↔ State Model 一致）
- Trigger：交接双方看到两个阶段词（`READY_FOR_CLEANER` vs `READY_FOR_REVIEW`），需靠括号注释映射才对齐
- Actual：`CoderAgent.md` 与 `Five-AgentResponsibilityBoundary.md` 保留旧词 `READY_FOR_CLEANER` 并用「（对应状态机 phase `READY_FOR_REVIEW`）」标注；`AgentCollaborationSpecification.md` 与 Design 已直接用 `READY_FOR_REVIEW`
- Expected：统一为状态机权威 phase `READY_FOR_REVIEW`（保留一处映射说明即可），避免双词并存
- Impact：低；不阻塞 CLEAN，属术语收敛问题
- Evidence：`grep -rn "READY_FOR_CLEANER" docs/agent/` 命中 `CoderAgent.md`、`Five-AgentResponsibilityBoundary.md`
- Required Fix Boundary：将旧词统一为 `READY_FOR_REVIEW`，不影响状态语义与转换表。
