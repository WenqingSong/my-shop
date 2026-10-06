// Package workflow 实现 Agent Workflow V2 的只读校验逻辑。
// 它不修改 state.yaml、Registry 或任何 Workflow Artifact，不承担 Orchestrator 职责，
// 不做角色派发，不代理任何 git 写操作。Validator 只对显式 Gate 给出 PASS / FAIL / ERROR。
package workflow

import "fmt"

// Issue 描述一处校验失败，能明确定位 task、检查项（invariant/gate/schema）、
// 实际值、期望值与原因，满足对失败输出的可定位要求。
type Issue struct {
	Task     string // 任务 slug
	Check    string // 不变量 / Gate / schema 标识，如 INV-4、Review Tail
	Actual   string // 实际值
	Expected string // 期望值
	Reason   string // 原因说明
}

func (i Issue) String() string {
	return fmt.Sprintf("task=%s check=%s actual=%s expected=%s reason=%s",
		i.Task, i.Check, i.Actual, i.Expected, i.Reason)
}

// StatusKind 是单个 Gate 的校验结果状态。
type StatusKind = string

const (
	StatusPass  StatusKind = "PASS"
	StatusFail  StatusKind = "FAIL"
	StatusError StatusKind = "ERROR"
)

// Result 是单个 Gate 的统一结果载体。
// PASS：全部满足；FAIL：不变量/Gate 不满足（Issues 填充）；ERROR：validator/输入/运行级错误（Error 填充）。
type Result struct {
	Status StatusKind
	Issues []Issue
	Error  string
}
