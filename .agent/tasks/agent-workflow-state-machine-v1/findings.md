# Cleaner Findings

## Review Target

- 任务基线：Base commit `bba7578ea9219e43bb24efb2b48fa994974aa523`（分支 `develop`）。
- 当前对象版本：HEAD = `906637f966fb680eabced85db79c0c967343fd09`（`docs(workflow): 引入任务状态机与 Validator 契约`），叠加工作区未提交的复审修复（4 文件：`.agent/tasks/agent-workflow-state-machine-v1/contract.md`、`docs/design/agent-workflow.md`、`docs/agent/models/CoderAgent.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`）。
- 完整变更集（自 `bba7578` 起，含已提交 + 未提交）：

已修改：`.agent/registry/error-codes.md`、`.agent/registry/migrations.md`、`docs/agent/AgentCollaborationSpecification.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`、`docs/agent/models/{AnalystAgent,CleanerAgent,CoderAgent,DelivererAgent,TaskBuilderPrompt}.md`、`go.mod`、`scripts/check-registry.sh`、`.agent/tasks/agent-workflow-state-machine-v1/contract.md`、`docs/design/agent-workflow.md`

新增：`cmd/workflow-check/{main.go,main_test.go}`、`internal/workflow/{state,validator,errors,git,paths,registry}.go`、`internal/workflow/{state,validator,git_integration,registry}_test.go`、`docs/design/agent-workflow.md`、`.agent/tasks/agent-workflow-state-machine-v1/{task,contract,findings,core-logic,delivery}.md`

- 全局资源：本任务不占用错误码域 / migration version；仅修正 Registry `RESERVED` 语义文本，未新增分配条目。
- 备注：本任务为 State Machine V1 生效前任务（Legacy），无 `state.yaml`；按 Cutover Rule 不创建/不回填 `state.yaml`。`review.status` 的客观失效（STALE）与任务状态在此以文字记录，作为 Cleaner-owned evidence 的事实表达。

## Result

CHANGES_REQUIRED

## Current State

- Cleaner Result：`CHANGES_REQUIRED`（先前 `CLEAN` 已被 Owner Verification 核实的两条 P1 缺陷客观失效）。
- `review.status`：`STALE`（先前 CLEAN 绑定的 review target 经 Owner Verification 发现核心语义缺陷，客观失效）。
- `Owner Verification Status`：`PENDING`（见 `core-logic.md` 顶部，保持 PENDING；Owner 已核实 CL-001/CL-002 并发现缺陷，未 ACCEPT）。
- Task lifecycle route：`CONTRACT_REVISION_REQUIRED`（先走 CLEAN-003 的 Contract Revision；CLEAN-004 与 CLEAN-003 的 Coder 修复合并进行，不单独先行处理）。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `state.yaml` schema（`internal/workflow/state.go`、`docs/design/agent-workflow.md` §2）为唯一机器权威源；`Validator` 只读 `phase` 判定；无「以 Markdown 第一行 / Git HEAD 作为唯一状态来源」的判定路径。 |
| AC-002 | PASS | 13 值 phase 集（`allPhases`）+ 正交子事实区分明确；`TestPhaseSetHasExactly13Values` 断言恰 13 值。 |
| AC-003 | PASS | `transitionAuthority` 18 条、每条唯一 Decision Authority、文件写入者 ≠ Decision Authority；`TestTransitionAuthorityCompleteness`。 |
| AC-004 | PASS | Owner 唯一决策来源；`ACCEPTED` 仅 Owner 明确指令 → Agent 机械持久化；各角色 Prompt 均已固化。 |
| AC-005 | PASS | `review.target_base`/`target_paths` 绑定 CLEAN；白名单含 `findings.md`/`core-logic.md`/`delivery.md`/`state.yaml`；`TestCleanReviewNeutralNoStale` 证明 Cleaner 自写不失效。 |
| AC-006 | FAIL | Mechanical Invalidation 的 default-deny 语义未实现：非白名单文件仅在命中 must-trigger 或 `target_paths` 时判 STALE，其余默认放行（default-allow）。见 CLEAN-003。 |
| AC-007 | PASS | `docs/agent/models/CleanerAgent.md`「Mutation 边界」：隔离 worktree/disposable checkout、不污染、不 commit、不入交付。 |
| AC-008 | FAIL | Resource Authority 默认读取 local `develop` 而非 shared `develop`（`origin/develop`），未进入共享仓库的本地 Reservation 会被误判为有效授权。见 CLEAN-004。 |
| AC-009 | PASS | `RESERVED` 定义改为「Reservation 已通过只改 Registry 的 commit 落到共享 develop 生效、Feature 未合并」；落点 `.agent/registry/*`、协作规范 §11.4、`check-registry.sh` 注释。 |
| AC-010 | PASS | Deliverer Gate（`docs/design/agent-workflow.md` §7、`DelivererAgent.md`）；Validator 拒绝 `DELIVERING+owner_verification=PENDING` 与 `DELIVERING+review!=CLEAN`。 |
| AC-011 | PASS | 四个最小无效状态全部被拒绝且测试覆盖：`DELIVERING+PENDING`、`IMPLEMENTING+缺 Reservation`、`CLEAN+target 已改`、`DONE+delivery!=PASS`。 |
| AC-012 | FAIL | 四者一致被 CLEAN-003（default-deny 语义与实现漂移）与 CLEAN-004（shared develop 语义与实现漂移）破坏。 |
| AC-013 | PASS | Diff 未引入 DB/MQ/BPMN/Web UI/Orchestrator（仅 `cmd/workflow-check` + `internal/workflow` + `yaml.v3` 转直接依赖）；既有规则未被推翻。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go test ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 全 ok（但这些测试固化了 default-allow 与 local develop 的行为，见 CLEAN-003/004） |
| `go vet ./internal/workflow/... ./cmd/workflow-check/...` | PASS | 无告警 |
| `bash scripts/check-registry.sh` | PASS | 「校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致」 |
| `go run ./cmd/workflow-check` | PASS（预期） | 19 个存量任务全部 SKIPPED（无 `state.yaml`、未提供 cutover），exit 0，符合 Cutover Rule |
| Owner Verification CL-001（Review Validity） | FAIL | `isSubstantialChange` 为 default-allow；`TestPathClassification`「README 普通文档」断言 neutral=false、mustTrigger=false，`TestCheckReviewValidity`「target_paths 之外的无关文件变化」「无关 README 变化」断言 stale=false |
| Owner Verification CL-002（Resource Authority） | FAIL | `--develop-ref` 默认 `develop`；`loadDevelopRegistry` 经 `git show develop:path` 读本地；测试 `validator()` 固定 `DevelopRef:"develop"`，无 local/remote 分叉场景 |

## Findings

### CLEAN-001：Contract/Design 声称「phase 转换是否在允许表内」「乱序写入被拒」，但 Validator 未校验转换顺序

- Severity：P2
- Status：CLOSED
- Resolution：第三轮 Contract Revision（Owner APPROVED，见 `contract.md` 第三轮决策）。采用修复路径 (b)：Contract S7 与 Failure Semantics、Design §8/§11 移除「phase 转换是否在允许表内」「乱序写入被拒」的声明，明确 V1 Validator 能力边界为「仅校验当前 state.yaml schema / 当前 phase 与正交子状态组合 / 当前 Gate 与 Invariant / Review Validity / Resource Authority」，并显式声明「不证明历史 transition sequence / actor authenticity」；不新增 `previous_phase`；Future Extension（`agentctl / controlled transition writer`）仅记录不实现。`transitionAuthority` 表作为 `Normative Transition Rule` 规范事实保留，其「存在 + 每条唯一 Decision Authority」由 `TestTransitionAuthorityCompleteness` 覆盖。

### CLEAN-002：Coder/Deliverer 交接词 `READY_FOR_CLEANER` 与状态机 phase `READY_FOR_REVIEW` 未完全收敛

- Severity：P3
- Status：CLOSED
- Resolution：`docs/agent/models/CoderAgent.md`（3 处）与 `docs/agent/Five-AgentResponsibilityBoundary.md`（1 处）已将 `READY_FOR_CLEANER` 统一为 `READY_FOR_REVIEW`；`grep READY_FOR_CLEANER docs/agent/` 无命中。

### CLEAN-003：Review Validity 实际为 default-allow，违反批准的 default-deny 语义

- Severity：P1
- Status：OPEN
- 来源：CL-001 Owner Verification
- Location：`internal/workflow/paths.go`（`isSubstantialChange`）、`internal/workflow/validator.go`（`checkReviewValidity`）；相关测试 `internal/workflow/state_test.go`（`TestPathClassification`）、`internal/workflow/validator_test.go`（`TestCheckReviewValidity`）
- AC / Invariant：AC-006；INV-005；Contract S4（Mechanical Invalidation default-deny + 白名单）
- Trigger：`review.target_base` 之后出现一个文件变化，该文件不在 `review.target_paths`、不属于 must-trigger 集合、也不在 Review-neutral 白名单（例如 `docs/agent/*`、`README.md`、`go.mod`、前端文件等）
- Actual：`isSubstantialChange` 返回 `false`，`checkReviewValidity` 不产生 issue，旧 `CLEAN` 保持有效。实现为「trigger-list + 默认放行」，与「default-deny + 白名单」相反；`target_paths` 被事实上用作 STALE 判定边界（隐式白名单）。
- Expected：任何非 Review-neutral 实质变化默认使旧 CLEAN 失效（STALE）；`review.target_paths` 仅作为 Review Evidence / 审查范围记录，不是未来 STALE 判定的允许列表；只有明确 Review-neutral 白名单（`findings.md`/`core-logic.md`/`delivery.md`/`state.yaml` 合法机械持久化等）可豁免。
- Impact：状态机核心机制「CLEAN 客观失效」被削弱；目标语义「使无效状态不可存在」无法对 target_paths 之外的实质变化兜底，已失效 CLEAN 会被继续当作后续 Gate 依据。
- Evidence：`internal/workflow/paths.go:77-92`；`TestPathClassification`「README 普通文档」→ `neutral=false, mustTrigger=false`；`TestCheckReviewValidity`「target_paths 之外的无关文件变化」（`docs/agent/coder.md`）→ `stale=false`、「无关 README 变化」（`README.md`）→ `stale=false`。现有测试显式固化了 default-allow 行为。
- Required Fix Boundary（先 Contract Revision，后实现）：
  1. Contract Revision 必须明确：`review.target_paths` 是 Review Evidence / 审查范围记录，不是 STALE 判定允许列表；Review Target 后任何非 Review-neutral 实质变化默认使旧 CLEAN 失效；仅 Review-neutral 白名单可豁免。
  2. Contract/Design 修订经 Owner APPROVED 后，Coder 才修改实现（`isSubstantialChange` 应为「非白名单即实质变化」）与对应测试。
- Routing：`CONTRACT_REVISION_REQUIRED`（Analyst → Contract Revision → Owner Approval → Design Sync → Coder）。

### CLEAN-004：Resource Authority 默认读取 local develop，而非 shared develop

- Severity：P1
- Status：OPEN
- 来源：CL-002 Owner Verification
- Location：`cmd/workflow-check/main.go`（`--develop-ref` 默认值 `develop`）、`internal/workflow/validator.go`（`loadDevelopRegistry`）、`internal/workflow/git.go`（`ShowFile`）
- AC / Invariant：AC-008；INV-006；Contract S6/S7（shared develop Registry 权威）
- Trigger：`origin/develop` 无某 Reservation，local `develop` 有该 Reservation（未推送），Feature Task 声明需要该资源 → 当前读 local `develop`，会命中该 Reservation 且 owner/status 一致 → PASS（错误）。
- Actual：`--develop-ref` 默认 `develop`，`loadDevelopRegistry` 经 `git show develop:path` 读取本地 `refs/heads/develop`，未推送的本地 Reservation 可被误判为有效全局授权。
- Expected：默认 Authority 为 shared `origin/develop`（或等价 `refs/remotes/origin/develop`）；不得 fallback 到 local `develop`；Validator 保持 read-only、不执行 `git fetch`；shared develop ref 不存在/不可读时明确失败；Reservation 真正进入 `origin/develop` 后才允许 Gate 通过。
- Impact：破坏 Global Resource Reservation 的独占性——Feature Branch / 本地未共享的 `RESERVED` 可绕过「落到共享 develop」的硬 Gate，进入 IMPLEMENTING。
- Evidence：`cmd/workflow-check/main.go:31`；`internal/workflow/validator.go:358-382`；`git_integration_test.go` 的 `validator()` helper 固定 `DevelopRef:"develop"`，无 local/remote 分叉场景测试（该场景未被覆盖）。
- Required Fix Boundary（Implementation Drift，无需单独 Contract Revision，但与 CLEAN-003 合并修复）：
  1. 默认 Authority 改为 `origin/develop`（或 `refs/remotes/origin/develop`），不得 fallback 到 local `develop`；
  2. Validator 保持 read-only，不执行 `git fetch`；
  3. shared develop ref 不存在/不可读时明确失败；
  4. 增加集成测试：local `develop` 有 Reservation、`origin/develop` 无 → FAIL；Reservation 进入 `origin/develop` 后 → PASS。
- Routing：`CHANGES_REQUIRED`（实现漂移）。按 Owner 指示，不在 Contract Revision 前单独处理，待 CLEAN-003 的 Contract Revision 完成 Design Sync 后，与 CLEAN-003 的 default-deny 实现/测试一并交 Coder 修复。

## 开放 Finding 汇总

- CLEAN-003（P1，OPEN）：default-allow，需 Contract Revision（`CONTRACT_REVISION_REQUIRED`）。
- CLEAN-004（P1，OPEN）：local develop Authority，实现漂移（`CHANGES_REQUIRED`，随 CLEAN-003 合并修复）。
- CLEAN-001 / CLEAN-002：CLOSED。

存在开放 P1（CLEAN-003、CLEAN-004），阻塞 `CLEAN`。
