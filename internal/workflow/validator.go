package workflow

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Validator 编排对 state.yaml 的只读 Gate 校验。它不修改任何 Artifact，不代理任何 git 写操作。
type Validator struct {
	Root       string // 仓库根目录，用于文件系统访问（state.yaml / contract.md 读取）
	Git        Git    // 只读 git 操作（应与 Root 指向同一仓库）
	DevelopRef string // shared develop 远端引用（如 origin/develop），资源权威来源；本地 develop 分支不是权威
}

// loadV2State 读取并解析 taskDir/state.yaml，校验 schema_version==2。
// 非 V2 任务（含 Legacy V1）返回 ERROR。
func (v *Validator) loadV2State(taskDir string) (State, Result) {
	statePath := filepath.Join(v.Root, taskDir, "state.yaml")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("read %s: %v", statePath, err)}
	}
	var s State
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("parse %s: %v", statePath, err)}
	}
	if s.SchemaVersion != SchemaV2 {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("task %q 不是 V2 任务（schema_version=%d，期望 %d）", s.TaskID, s.SchemaVersion, SchemaV2)}
	}
	return s, Result{Status: StatusPass}
}

// schemaValidation 校验 state 的 schema 与枚举合法性。纯函数，便于表驱动测试。
func schemaValidation(s State) []Issue {
	task := s.TaskID
	if task == "" {
		task = "<unknown>"
	}

	var issues []Issue
	if s.TaskID == "" {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "schema",
			Actual:   "task_id 字段为空",
			Expected: "<task-slug>",
			Reason:   "state.yaml 必须包含 task_id",
		})
	}
	if !ValidContractStatus(s.Contract.Status) {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "contract.status enum",
			Actual:   s.Contract.Status,
			Expected: "PENDING | APPROVED | REJECTED | NOT_REQUIRED",
			Reason:   "contract.status 取值非法",
		})
	}
	if !ValidReviewStatus(s.Review.Status) {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "review.status enum",
			Actual:   s.Review.Status,
			Expected: "NOT_REQUESTED | PENDING | CLEAN | CHANGES_REQUIRED",
			Reason:   "review.status 取值非法",
		})
	}
	if !ValidOwnerStatus(s.Owner.Status) {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "owner.status enum",
			Actual:   s.Owner.Status,
			Expected: "PENDING | ACCEPTED | REJECTED | NOT_REQUIRED",
			Reason:   "owner.status 取值非法",
		})
	}
	if !ValidDeliveryStatus(s.Delivery.Status) {
		issues = append(issues, Issue{
			Task:     task,
			Check:    "delivery.status enum",
			Actual:   s.Delivery.Status,
			Expected: "NOT_RUN | PASS | FAIL | BLOCKED",
			Reason:   "delivery.status 取值非法",
		})
	}
	return issues
}

// readWorkingTree 读取仓库工作区内相对路径文件的内容。
func (v *Validator) readWorkingTree(rel string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(v.Root, rel))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// featureHead 返回当前 HEAD 的完整 SHA。
func (v *Validator) featureHead() (string, error) {
	return v.Git.RevParse("HEAD")
}

// loadDevelopRegistry 读取 shared develop 上的 Registry 权威事实。
func (v *Validator) loadDevelopRegistry() (Registry, error) {
	if v.DevelopRef == "" {
		return Registry{}, fmt.Errorf("缺少 develop-ref，无法机械验证 shared develop Registry 权威")
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

// contractPath 返回当前 task 的 contract.md 相对路径。
func contractPath(taskSlug string) string {
	return filepath.Join(".agent", "tasks", taskSlug, "contract.md")
}
