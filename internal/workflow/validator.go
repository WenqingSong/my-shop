package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// StatusKind 是单个 Task 的校验结果状态。
type StatusKind = string

const (
	StatusPass    StatusKind = "PASS"
	StatusFail    StatusKind = "FAIL"
	StatusSkipped StatusKind = "SKIPPED"
)

// TaskResult 是单个 Task 的校验结果。
type TaskResult struct {
	Task       string
	Status     StatusKind
	SkipReason string // 仅 SKIPPED 时填充，如 "LEGACY（生效前任务，无 state.yaml）"
	Issues     []Issue
}

// Validator 编排对 state.yaml 的只读校验。它不修改任何 Artifact。
type Validator struct {
	Root       string // 仓库根目录，用于文件系统访问（state.yaml 读取）
	Git        Git    // 只读 git 操作（应与 Root 指向同一仓库）
	DevelopRef string // shared develop 引用（如 develop / origin/develop），资源权威来源
	Cutover    string // State Machine V1 生效 commit，用于 Cutover Rule
}

// resourceGatePhases 是「已进入或越过 IMPLEMENTING」的 phase 集合，
// 这些 phase 下 required_resources 必须经 shared develop Registry 权威验证。
var resourceGatePhases = map[Phase]struct{}{
	PhaseImplementing:          {},
	PhaseReadyForReview:        {},
	PhaseInReview:              {},
	PhaseChangesRequired:       {},
	PhaseWaitingForOwnerAccept: {},
	PhaseDelivering:            {},
	PhaseDone:                  {},
}

func phaseRequiresResources(p Phase) bool {
	_, ok := resourceGatePhases[p]
	return ok
}

// ValidateStateSchema 校验 state 的 schema、枚举与非法状态组合。
// 它是纯函数，不依赖 git 或 Registry，便于表驱动测试。
func ValidateStateSchema(state State) []Issue {
	task := state.Task
	if task == "" {
		task = "<unknown>"
	}

	var issues []Issue

	if state.Task == "" {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "schema",
			Actual:   "task 字段为空",
			Expected: "<task-slug>",
			Reason:   "state.yaml 必须包含 task 字段",
		})
	}

	if !ValidPhase(state.Phase) {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "phase enum (INV-001)",
			Actual:   state.Phase,
			Expected: strings.Join(allPhases, " | "),
			Reason:   "phase 必须是 13 值之一",
		})
	}

	if _, ok := reviewStatusSet[state.Review.Status]; !ok {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "review.status enum",
			Actual:   state.Review.Status,
			Expected: "NONE | CLEAN | STALE",
			Reason:   "review.status 取值非法",
		})
	}

	if _, ok := ownerVerificationStatusSet[state.OwnerVerification.Status]; !ok {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "owner_verification.status enum",
			Actual:   state.OwnerVerification.Status,
			Expected: "NOT_REQUIRED | PENDING | ACCEPTED",
			Reason:   "owner_verification.status 取值非法",
		})
	}

	if _, ok := deliveryStatusSet[state.Delivery.Status]; !ok {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "delivery.status enum",
			Actual:   state.Delivery.Status,
			Expected: "NONE | PASS | CONDITIONAL_PASS | FAIL",
			Reason:   "delivery.status 取值非法",
		})
	}

	issues = append(issues, checkPhaseCombinations(task, state)...)
	return issues
}

// checkPhaseCombinations 校验 phase 与子事实之间的非法状态组合（Gate 与生命周期一致性）。
func checkPhaseCombinations(task string, s State) []Issue {
	var issues []Issue
	phase := s.Phase
	rs := s.Review.Status
	ov := s.OwnerVerification.Status
	dv := s.Delivery.Status

	// INV-007：DELIVERING 时 owner_verification ∈ {ACCEPTED, NOT_REQUIRED}。
	if phase == PhaseDelivering && ov == OwnerVerificationPending {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "Owner Verification Gate (INV-007)",
			Actual:   "DELIVERING + owner_verification=PENDING",
			Expected: "owner_verification ∈ {ACCEPTED, NOT_REQUIRED}",
			Reason:   "Owner 尚未完成核心逻辑确认，不得进入里程碑验收",
		})
	}

	// Deliverer Gate：DELIVERING 要求 review.status=CLEAN（S2 #13）。
	if phase == PhaseDelivering && rs != ReviewClean {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "Delivery Gate (INV-007)",
			Actual:   fmt.Sprintf("DELIVERING + review.status=%s", rs),
			Expected: "review.status=CLEAN",
			Reason:   "里程碑验收只消费 Cleaner CLEAN 后的状态",
		})
	}

	// INV-009：DONE 时 delivery.status=PASS（有 Deliverer 路径）。
	if phase == PhaseDone && dv != DeliveryPass {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "Delivery Gate (INV-009)",
			Actual:   fmt.Sprintf("DONE + delivery.status=%s", dv),
			Expected: "delivery.status=PASS",
			Reason:   "DONE 是终态，必须已通过里程碑验收",
		})
	}

	// Mechanical Invalidation 后的生命周期一致性（STALE 降级包）。
	if rs == ReviewStale {
		if phase == PhaseDelivering || phase == PhaseWaitingForOwnerAccept || phase == PhaseDone {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Lifecycle Consistency (INV-005)",
				Actual:   fmt.Sprintf("review.status=STALE + phase=%s", phase),
				Expected: "phase ∈ {READY_FOR_REVIEW, IN_REVIEW}",
				Reason:   "旧 CLEAN 已失效，不得仍处于后续 Delivery 阶段",
			})
		}
		if ov == OwnerVerificationAccepted {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Lifecycle Consistency (INV-005)",
				Actual:   "review.status=STALE + owner_verification=ACCEPTED",
				Expected: "owner_verification=PENDING",
				Reason:   "旧 ACCEPTED 针对旧 review.target，不得继续作为新版本 Gate 依据",
			})
		}
		if dv != DeliveryNone {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Lifecycle Consistency (INV-005)",
				Actual:   fmt.Sprintf("review.status=STALE + delivery.status=%s", dv),
				Expected: "delivery.status=NONE",
				Reason:   "依赖旧 CLEAN 的交付结论应同被失效",
			})
		}
	}

	// CLEAN 必须绑定明确 review.target（target_base + target_paths）。
	if rs == ReviewClean {
		if s.Review.TargetBase == "" {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Review Target (INV-004)",
				Actual:   "review.status=CLEAN 但 target_base 为空",
				Expected: "target_base=<commit-hash>",
				Reason:   "CLEAN 必须绑定唯一可复核版本",
			})
		}
		if len(s.Review.TargetPaths) == 0 {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Review Target (INV-004)",
				Actual:   "review.status=CLEAN 但 target_paths 为空",
				Expected: "target_paths=[<被审查相对路径>...]",
				Reason:   "Cleaner 必须记录完整被审查文件集",
			})
		}
	}

	return issues
}

// checkReviewValidity 校验 CLEAN 是否因 review.target 之后的实质变化而客观 STALE。
// changed 是 target_base 之后的变更路径集合（相对仓库根，已去重）。
// 该函数只读、只检测，不修改 state.yaml（INV-011）。
func checkReviewValidity(task string, s State, changed []string) []Issue {
	if s.Review.Status != ReviewClean || s.Review.TargetBase == "" {
		return nil
	}
	var issues []Issue
	for _, p := range changed {
		if isSubstantialChange(p, s.Review.TargetPaths) {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Review Validity (INV-005/INV-011)",
				Actual:   fmt.Sprintf("review.status=CLEAN 但 %s 已发生实质变化", p),
				Expected: "review.status=STALE",
				Reason:   "review.target 之后出现非 Review-neutral 实质变化，旧 CLEAN 客观失效",
			})
			break
		}
	}
	return issues
}

// ValidateTask 校验单个任务目录。返回 TaskResult 与可能的运行错误（exit 2 级）。
func (v *Validator) ValidateTask(taskDir string) (TaskResult, error) {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	statePath := filepath.Join(v.Root, taskDir, "state.yaml")

	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return v.legacyOrMissing(taskDir, taskSlug)
	} else if err != nil {
		return TaskResult{}, fmt.Errorf("stat %s: %w", statePath, err)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		return TaskResult{}, fmt.Errorf("读取 %s: %w", statePath, err)
	}

	var state State
	if err := yaml.Unmarshal(raw, &state); err != nil {
		return TaskResult{}, fmt.Errorf("解析 %s: %w", statePath, err)
	}

	issues := ValidateStateSchema(state)

	// Review Validity：仅 CLEAN 且 target_base 非空时做实质变化检测。
	if state.Review.Status == ReviewClean && state.Review.TargetBase != "" {
		changed, err := v.changedFiles(state.Review.TargetBase)
		if err != nil {
			return TaskResult{}, fmt.Errorf("检测 %s 的 review.target 变化: %w", taskSlug, err)
		}
		issues = append(issues, checkReviewValidity(taskSlug, state, changed)...)
	}

	// Resource Authority：已进入/越过 IMPLEMENTING 且声明了全局资源时，以 develop Registry 为权威。
	if state.HasRequiredResources() && phaseRequiresResources(state.Phase) {
		resIssues, err := v.resourceIssues(taskSlug, state)
		if err != nil {
			return TaskResult{}, err
		}
		issues = append(issues, resIssues...)
	}

	status := StatusPass
	if len(issues) > 0 {
		status = StatusFail
	}
	return TaskResult{Task: taskSlug, Status: status, Issues: issues}, nil
}

// legacyOrMissing 处理缺失 state.yaml 的任务（Cutover Rule，INV-010）。
func (v *Validator) legacyOrMissing(taskDir, taskSlug string) (TaskResult, error) {
	if v.Cutover == "" {
		return TaskResult{
			Task:       taskSlug,
			Status:     StatusSkipped,
			SkipReason: "LEGACY（未提供 cutover，无法判定，跳过）",
		}, nil
	}

	exists, err := v.Git.PathExistsAt(v.Cutover, filepath.Join(taskDir, "task.md"))
	if err != nil {
		return TaskResult{}, fmt.Errorf("判定 %s 是否 Legacy: %w", taskSlug, err)
	}
	if exists {
		return TaskResult{
			Task:       taskSlug,
			Status:     StatusSkipped,
			SkipReason: "LEGACY（State Machine V1 生效前任务，无 state.yaml，跳过）",
		}, nil
	}

	return TaskResult{
		Task:   taskSlug,
		Status: StatusFail,
		Issues: []Issue{{
			Task:     taskSlug,
			Check:    "Cutover Rule (INV-010)",
			Actual:   "缺 state.yaml",
			Expected: "V1 生效后新建 Task 必须存在合法 state.yaml",
			Reason:   "该任务在 cutover commit 之后创建，缺少机器事实源",
		}},
	}, nil
}

// changedFiles 返回 target_base 之后的所有变更路径（已提交 + 未提交 + 未跟踪，去重）。
func (v *Validator) changedFiles(base string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(ps []string) {
		for _, p := range ps {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}

	diff, err := v.Git.DiffNameOnly(base)
	if err != nil {
		return nil, err
	}
	add(diff)

	untracked, err := v.Git.UntrackedFiles()
	if err != nil {
		return nil, err
	}
	add(untracked)

	return out, nil
}

// resourceIssues 以 shared develop Registry 为权威校验资源。
func (v *Validator) resourceIssues(taskSlug string, state State) ([]Issue, error) {
	reg, err := v.loadDevelopRegistry()
	if err != nil {
		return nil, err
	}
	return checkResourceAuthority(taskSlug, state, reg), nil
}

func (v *Validator) loadDevelopRegistry() (Registry, error) {
	if v.DevelopRef == "" {
		return Registry{}, fmt.Errorf("缺少 --develop-ref，无法机械验证 shared develop Registry 权威")
	}

	migContent, err := v.Git.ShowFile(v.DevelopRef, ".agent/registry/migrations.md")
	if err != nil {
		return Registry{}, fmt.Errorf("读取 develop Registry migrations.md（%s）: %w", v.DevelopRef, err)
	}
	ecContent, err := v.Git.ShowFile(v.DevelopRef, ".agent/registry/error-codes.md")
	if err != nil {
		return Registry{}, fmt.Errorf("读取 develop Registry error-codes.md（%s）: %w", v.DevelopRef, err)
	}

	migrations, err := ParseMigrations(migContent)
	if err != nil {
		return Registry{}, fmt.Errorf("解析 develop migrations.md: %w", err)
	}
	domains, err := ParseErrorDomains(ecContent)
	if err != nil {
		return Registry{}, fmt.Errorf("解析 develop error-codes.md: %w", err)
	}

	return Registry{Migrations: migrations, ErrorDomains: domains}, nil
}
