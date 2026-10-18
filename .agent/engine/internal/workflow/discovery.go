package workflow

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// 本文件实现 Discovery 两阶段产物的机器可读 front-matter 解析与结构校验。
//
// Discovery 第一阶段（调查）产出 discovery-report.md（人读）并进入 WAITING_FOR_OWNER_DECISION；
// Owner 决策后写入 owner-decision.md（机器 front-matter 记录 APPROVED/REJECTED）；
// 第二阶段产出 taskbuilder-handoff.md（机器 front-matter），供 TaskBuilder 机器校验。
//
// 这些产物都属于 Intake 记录，不构成 Task State Schema，不引入自动任务编排。

// OwnerDecision 是 owner-decision.md 的机器可读 front-matter。
type OwnerDecision struct {
	Decision string `yaml:"decision"` // APPROVED | REJECTED
	// Existing Intake decision, now binding the actual approved handoff facts.
	// Legacy records remain parseable; new task initialization requires binding.
	IntakeID     string `yaml:"intake_id,omitempty"`
	TaskID       string `yaml:"task_id,omitempty"`
	TaskType     string `yaml:"task_type,omitempty"`
	ParentBranch string `yaml:"parent_branch,omitempty"`
	BaseCommit   string `yaml:"base_commit,omitempty"`
}

// Owner 决策枚举。
const (
	OwnerDecisionApproved = "APPROVED"
	OwnerDecisionRejected = "REJECTED"
)

// validOwnerDecision 报告 s 是否为合法 Owner 决策。
func validOwnerDecision(s string) bool {
	return s == OwnerDecisionApproved || s == OwnerDecisionRejected
}

// TaskBuilderHandoff 是 taskbuilder-handoff.md 的机器可读 front-matter。
// 它必须绑定 Owner 已确认的范围及 Git 基线：parent_branch / base_commit 与 Intake 记录一致，
// task_id / task_type 是 Owner 确认后的 Task 标识与工作流模式。
type TaskBuilderHandoff struct {
	IntakeID      string `yaml:"intake_id"`
	OwnerDecision string `yaml:"owner_decision"`
	ParentBranch  string `yaml:"parent_branch"`
	BaseCommit    string `yaml:"base_commit"`
	TaskType      string `yaml:"task_type"`
	TaskID        string `yaml:"task_id"`
}

// validateTaskBuilderHandoff 校验 taskbuilder-handoff.md front-matter 的结构完整性。
// owner_decision 的 APPROVED / REJECTED 都是合法枚举；「未批准不得初始化」属于 Gate 的业务 FAIL，
// 由 discovery-handoff / taskbuilder-init 在结构校验通过后判定，避免把业务拒绝误报为输入 ERROR。
func validateTaskBuilderHandoff(h TaskBuilderHandoff) error {
	if !validIntakeID(h.IntakeID) {
		return fmt.Errorf("非法的 taskbuilder-handoff.intake_id=%q", h.IntakeID)
	}
	if !validOwnerDecision(h.OwnerDecision) {
		return fmt.Errorf("非法的 taskbuilder-handoff.owner_decision=%q（期望 APPROVED | REJECTED）", h.OwnerDecision)
	}
	if h.ParentBranch == "" {
		return fmt.Errorf("taskbuilder-handoff.parent_branch 不得为空")
	}
	if h.BaseCommit == "" || !validSHA(h.BaseCommit) {
		return fmt.Errorf("非法的 taskbuilder-handoff.base_commit=%q（期望完整 Commit SHA）", h.BaseCommit)
	}
	if !ValidTaskType(h.TaskType) {
		return fmt.Errorf("非法的 taskbuilder-handoff.task_type=%q（期望 NORMAL | COMPLEX | DESIGN）", h.TaskType)
	}
	if !ValidScopeID(h.TaskID) {
		return fmt.Errorf("非法的 taskbuilder-handoff.task_id=%q（必须为不穿越路径的单一标识）", h.TaskID)
	}
	return nil
}

// parseTaskBuilderHandoff 解析并校验 taskbuilder-handoff.md front-matter。
func parseTaskBuilderHandoff(fm []byte) (TaskBuilderHandoff, error) {
	var h TaskBuilderHandoff
	dec := yaml.NewDecoder(bytes.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&h); err != nil {
		return TaskBuilderHandoff{}, fmt.Errorf("解析 taskbuilder-handoff.md front-matter: %w", err)
	}
	if err := validateTaskBuilderHandoff(h); err != nil {
		return TaskBuilderHandoff{}, err
	}
	return h, nil
}

// parseOwnerDecision 解析并校验 owner-decision.md front-matter。
func parseOwnerDecision(fm []byte) (OwnerDecision, error) {
	var d OwnerDecision
	dec := yaml.NewDecoder(bytes.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return OwnerDecision{}, fmt.Errorf("解析 owner-decision.md front-matter: %w", err)
	}
	if !validOwnerDecision(d.Decision) {
		return OwnerDecision{}, fmt.Errorf("非法的 owner-decision.decision=%q（期望 APPROVED | REJECTED）", d.Decision)
	}
	return d, nil
}
