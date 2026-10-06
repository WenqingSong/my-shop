package workflow

import (
	"fmt"
	"os"
	"path/filepath"
)

// Validator 编排对 state.yaml 的只读 Gate 校验。它不修改任何 Artifact，不代理任何 git 写操作。
type Validator struct {
	Root   string  // 仓库根目录，用于文件系统访问（state.yaml / contract.md 读取）
	Git    Git     // 只读 git 操作（应与 Root 指向同一仓库）
	Config *Config // 机器配置（.agent/workflow.yaml），提供 integration branch 与 registry 映射
}

// developRef 返回 shared integration branch 的远端引用（origin/<integration_branch>）。
func (v *Validator) developRef() string {
	return "origin/" + v.Config.Git.IntegrationBranch
}

// loadStateFile 读取并解析 taskDir/state.yaml，支持 schema v3（generic）与 v2（legacy 兼容）。
// 非 V2/V3 任务（含 Legacy V1）与 schema 错误返回 ERROR（exit 2）。
func (v *Validator) loadStateFile(taskDir string) (State, Result) {
	statePath := filepath.Join(v.Root, taskDir, "state.yaml")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("read %s: %v", statePath, err)}
	}
	s, err := parseState(raw)
	if err != nil {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("parse %s: %v", statePath, err)}
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

// loadDevelopRegistries 按 state 实际声明的 resource kind 读取 shared integration branch 上的 Registry 权威事实。
// 路径来自机器配置 .agent/workflow.yaml（config.resources.<kind>.registry）。
// 未在配置中声明的 kind 属于 Workflow Configuration ERROR（exit 2），不是「resource not reserved」。
func (v *Validator) loadDevelopRegistries(s State) (map[string][]RegistryEntry, error) {
	ref := v.developRef()
	regs := map[string][]RegistryEntry{}
	for _, kind := range sortedKeys(s.Resources.Reservations) {
		rc, ok := v.Config.Resources[kind]
		if !ok {
			return nil, fmt.Errorf("机器配置未声明 resource kind %q（缺少 resources.%s.registry）", kind, kind)
		}
		content, err := v.Git.ShowFile(ref, rc.Registry)
		if err != nil {
			return nil, fmt.Errorf("读取 %s:%s: %w", ref, rc.Registry, err)
		}
		regs[kind] = ParseRegistry(content)
	}
	return regs, nil
}

// contractPath 返回当前 task 的 contract.md 相对路径。
func contractPath(taskSlug string) string {
	return filepath.Join(".agent", "tasks", taskSlug, "contract.md")
}
