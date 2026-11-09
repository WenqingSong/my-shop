Owner Verification Status: PENDING

# 核心逻辑验证（Owner）

本任务交付 Agent Workflow 状态机的只读 Validator（`cmd/workflow-check` + `internal/workflow`），不涉及生产/业务代码。决定流程判定正确性的核心机制有两个：CLEAN 的客观失效判定（default-deny STALE）与全局资源的机械 Gate（shared develop Resource Authority）。以下是 Owner 需要理解并亲自验证的核心机制（已按第四轮 Contract Revision 修正后的语义更新）。

## CL-001：Review Validity（CLEAN → STALE 的真 default-deny）

- Owner 需要理解：`review.status=CLEAN` 绑定唯一 `review.target`（`target_base` + `target_paths`）。自 `target_base` 起，**任何非 Review-neutral 白名单的变化**（无论是否在 `target_paths` 内）都默认使旧 CLEAN 失效，机械降级为 `STALE`（同时 `phase → READY_FOR_REVIEW`、旧 `ACCEPTED`/`delivery` 失效），仅 Cleaner 复审可恢复。`review.target_paths` 只是 Cleaner 本轮审查范围的 Evidence/Audit Record，**不是** STALE 边界、不是允许列表、不是隐式白名单。唯一豁免是白名单（`findings.md`/`core-logic.md`/`delivery.md`/合法 `state.yaml` 机械持久化）。若 default-deny 被反向（改回「只有命中清单/target_paths 才失效」），target_paths 之外的实质变化会被漏判，旧 CLEAN 被继续当作后续 Gate 依据。
- 生产代码：`internal/workflow/paths.go`（`isSubstantialChange` = `!isReviewNeutral`、`isReviewNeutral`）、`internal/workflow/validator.go`（`checkReviewValidity`、`ValidateTask`）、`internal/workflow/git.go`（`changedFiles`：`git diff --name-only` + 未跟踪文件并集）
- 关键测试：`internal/workflow/git_integration_test.go` 的 `TestCleanStaleProductionCode`、`TestCleanReviewNeutralNoStale`、`TestCleanStaleBusinessTest`、`TestCleanStaleContractDesign`；`internal/workflow/state_test.go` 的 `TestPathClassification`；`internal/workflow/validator_test.go` 的 `TestCheckReviewValidity`
- 基线验证：`go test -count=1 ./internal/workflow/...`，预期 `ok`
- 可选 Mutation：把 `internal/workflow/paths.go` 的 `isSubstantialChange` 临时改为 `return false`（破坏 default-deny）
- 预期失败：`TestPathClassification`、`TestCheckReviewValidity`、`TestCleanStaleProductionCode` 等必须失败（README/docs/agent 等非白名单变化不再判 STALE）
- 恢复确认：恢复 `return !isReviewNeutral(p)`，重新 `go test -count=1 ./internal/workflow/...` 恢复 `ok`

## CL-002：Resource Authority（shared develop 全局资源机械 Gate）

- Owner 需要理解：`state.yaml.required_resources` 只「声明」需要哪些全局资源，绝不记录「已满足/SATISFIED」自证。任务进入/越过 `IMPLEMENTING` 时，Validator 以 shared `origin/develop`（remote-tracking ref，非本地 `develop` 分支）为唯一权威，逐项验证每个 migration version / 错误码域的 `version/域区间`、`owner task`、`status` 是否与当前 Task 一致且为 `RESERVED`/`ACTIVE`。Feature Branch 私写 `RESERVED`、或只落到本地 `develop` 未推送、或 `state.yaml` 自报 `SATISFIED`，均不构成有效 Reservation。若默认权威回退到本地 `develop`，未共享的本地 Reservation 会被误判为有效授权，破坏全局资源独占分配。
- 生产代码：`cmd/workflow-check/main.go`（`--develop-ref` 默认 `origin/develop`）、`internal/workflow/validator.go`（`loadDevelopRegistry`、`resourceIssues`、`resourceGatePhases`）、`internal/workflow/git.go`（`ShowFile`，只读 `git show`，无 `git fetch`、无 fallback）、`internal/workflow/registry.go`（`checkResourceAuthority`、`resourceActive`）
- 关键测试：`internal/workflow/git_integration_test.go` 的 `TestResourceAuthorityLocalDevelopReservedInvalid`、`TestResourceAuthorityDevelopReservedValid`、`TestResourceAuthorityFeatureBranchReservedInvalid`、`TestMissingMigrationForbiddenImplementing`
- 基线验证：`go test -count=1 ./internal/workflow/...`，预期 `ok`
- 可选 Mutation：把 `loadDevelopRegistry` 读取的 ref 临时改为硬编码 `"develop"`（模拟 fallback 到本地 develop）
- 预期失败：`TestResourceAuthorityLocalDevelopReservedInvalid` 必须失败（local develop 私留 Reservation 被误判为 PASS）
- 恢复确认：恢复读取 `v.DevelopRef`，重新 `go test -count=1 ./internal/workflow/...` 恢复 `ok`
