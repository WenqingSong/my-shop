Owner Verification Status: PENDING

# 核心逻辑验证（Owner）

本任务交付的是 Agent Workflow 状态机的只读 Validator（`cmd/workflow-check` + `internal/workflow`），不涉及生产/业务代码。真正决定流程判定正确性的机制有两个：CLEAN 的客观失效判定（STALE）与全局资源的机械 Gate（Resource Authority）。以下是 Owner 需要理解并亲自验证的核心机制。

## CL-001：Review Validity（CLEAN → STALE 的客观失效判定）

- Owner 需要理解：`review.status=CLEAN` 绑定唯一 `review.target`（`target_base` + `target_paths`）。`target_base` 之后，只要 `target_paths` 内（或命中「必须触发集合」：生产代码 `internal/`、`api/`、`main.go`、业务测试 `*_test.go`、`contract.md`、`task.md`、`docs/design/*`、migration SQL、runtime config、Registry）出现任何非白名单变化，旧 CLEAN 客观失效，机械降级为 `STALE`（同时 `phase → READY_FOR_REVIEW`、旧 `ACCEPTED`/`delivery` 失效），仅 Cleaner 复审可恢复。白名单（`findings.md`/`core-logic.md`/`delivery.md`/`state.yaml`）是唯一豁免。若白名单被错误放宽（例如把 `contract.md` 当 Review-neutral），已失效的 CLEAN 会被继续当作后续 Gate 的依据，导致未经复审的版本被误判为有效。
- 生产代码：`internal/workflow/paths.go`（`isSubstantialChange` / `isReviewNeutral` / `isMustTrigger`）、`internal/workflow/validator.go`（`checkReviewValidity`、`ValidateTask`）、`internal/workflow/git.go`（`changedFiles`：`git diff --name-only` + 未跟踪文件并集）
- 关键测试：`internal/workflow/git_integration_test.go` 的 `TestCleanStaleProductionCode`、`TestCleanReviewNeutralNoStale`、`TestCleanStaleBusinessTest`、`TestCleanStaleContractDesign`；`internal/workflow/state_test.go` 的 `TestPathClassification`
- 基线验证：`go test ./internal/workflow/...`，预期 `ok`
- 可选 Mutation：在 `internal/workflow/paths.go` 的 `reviewNeutralBasenames` 中临时加入 `"contract.md"`
- 预期失败：`TestPathClassification` 的「Contract」用例（期望 `isReviewNeutral=false`，改后为 `true`）必须失败；且 `TestCleanStaleContractDesign`（Contract/Design 实质变化应判 STALE）必须失败
- 恢复确认：移除该临时白名单条目，重新 `go test ./internal/workflow/...` 恢复 `ok`

## CL-002：Resource Authority（进入 IMPLEMENTING 前的全局资源机械 Gate）

- Owner 需要理解：`state.yaml.required_resources` 只「声明」任务需要哪些全局资源，绝不记录「已满足/SATISFIED」自证。任务进入/越过 `IMPLEMENTING` 时，Validator 以 shared `develop` 上的 Registry（`git show develop:.agent/registry/*`）为唯一权威，逐项验证每个 migration version / 错误码域的 `version/域区间`、`owner task`、`status` 是否与当前 Task 一致且为 `RESERVED`/`ACTIVE`。Feature Branch 内私写 `RESERVED`、或 `state.yaml` 自报 `SATISFIED`，均不构成有效 Reservation，会被拒绝。若该 Gate 被绕过（例如把 `RESERVED` 判为无效、或把 Feature 分支的 Registry 当权威），未预留资源的任务就能进入实现，破坏全局资源独占分配。
- 生产代码：`internal/workflow/registry.go`（`checkResourceAuthority`、`resourceActive`、`ParseMigrations`、`ParseErrorDomains`）、`internal/workflow/validator.go`（`resourceIssues`、`resourceGatePhases`、`loadDevelopRegistry`）
- 关键测试：`internal/workflow/git_integration_test.go` 的 `TestResourceAuthorityFeatureBranchReservedInvalid`、`TestResourceAuthorityDevelopReservedValid`、`TestMissingMigrationForbiddenImplementing`；`internal/workflow/registry_test.go` 的 `TestCheckResourceAuthority`
- 基线验证：`go test ./internal/workflow/...`，预期 `ok`
- 可选 Mutation：把 `internal/workflow/registry.go` 的 `resourceActive` 改为只认 `"ACTIVE"`（去掉 `"RESERVED"`）
- 预期失败：`TestResourceAuthorityDevelopReservedValid`（develop 上合法 `RESERVED` 本应 PASS）必须失败
- 恢复确认：恢复 `resourceActive` 原判定，重新 `go test ./internal/workflow/...` 恢复 `ok`
