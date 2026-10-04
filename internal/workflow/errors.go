// Package workflow 实现 Agent Workflow 状态机的只读校验逻辑。
// 它不修改 state.yaml、Registry 或任何 Workflow Artifact，也不承担 Orchestrator 职责。
package workflow

import "fmt"

// Issue 描述一处校验失败，能明确定位 task、检查项（invariant/gate/schema）、
// 实际值、期望值与原因，满足 Contract 对失败输出的可定位要求。
type Issue struct {
	Task     string // 任务 slug
	Check    string // 不变量 / Gate / schema 标识，如 INV-007、Delivery Gate
	Actual   string // 实际值
	Expected string // 期望值
	Reason   string // 原因说明
}

func (i Issue) String() string {
	return fmt.Sprintf("task=%s check=%s actual=%s expected=%s reason=%s",
		i.Task, i.Check, i.Actual, i.Expected, i.Reason)
}
