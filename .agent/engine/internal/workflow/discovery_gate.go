package workflow

import (
	"fmt"
	"path/filepath"
)

// 本文件实现 Discovery 两阶段的入口/出口 Gate。
//
//   - discovery-start：第一阶段（调查）入口，校验 Intake 记录与 Git 基线有效；
//   - discovery-decision：第二阶段入口，无 Owner Decision 时 FAIL；
//   - discovery-handoff：第二阶段出口，校验 taskbuilder-handoff.md 机器可用且绑定
//     Owner 已确认的范围与 Git 基线。
//
// Discovery 发生在正式 Task 分支创建之前：这些 Gate 只依赖 Intake 记录 + 受控 Git 只读检查，
// 不依赖尚不存在的 Task 分支，不修改父分支，不启动任何 Agent。

// intakeIDFromDir 从 taskDir（Intake 目录路径）推导 intake_id。
func intakeIDFromDir(taskDir string) string {
	return filepath.Base(filepath.Clean(taskDir))
}

// intakeBaselineValid 校验 Intake 的 Git 基线：父分支存在、base_commit 真实且是父分支 tip 的祖先。
// 这保证「初始父分支和 Commit 不会丢失」且与真实 Git 事实可核对。
func (v *Validator) intakeBaselineValid(intakeID string, rec IntakeRecord) Result {
	check := "Intake Baseline"
	if rec.IntakeID != intakeID {
		return failResult(intakeID, check, rec.IntakeID, intakeID, "Intake 内容与实际目录标识不一致")
	}
	if ok, err := v.Git.BranchExists(rec.ParentBranch); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check parent branch %s: %v", rec.ParentBranch, err)}
	} else if !ok {
		return failResult(intakeID, check, fmt.Sprintf("parent_branch=%s", rec.ParentBranch), "真实存在的分支", "Intake 父分支不存在")
	}
	if ok, err := v.Git.CommitExists(rec.BaseCommit); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check base_commit %s: %v", rec.BaseCommit, err)}
	} else if !ok {
		return failResult(intakeID, check, rec.BaseCommit, "真实 commit", "Intake base_commit 不是真实 commit（非法 SHA）")
	}
	tip, err := v.Git.RevParse(rec.ParentBranch)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s: %v", rec.ParentBranch, err)}
	}
	if ok, err := v.Git.IsAncestor(rec.BaseCommit, tip); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s %s: %v", rec.BaseCommit, rec.ParentBranch, err)}
	} else if !ok {
		return failResult(intakeID, check, rec.BaseCommit, fmt.Sprintf("%s 的祖先", rec.ParentBranch), "Intake base_commit 不是父分支的祖先")
	}
	return Result{Status: StatusPass}
}

// GateDiscoveryStart 是 Discovery 第一阶段（调查）入口 Gate。
func (v *Validator) GateDiscoveryStart(taskDir string) Result {
	intakeID := intakeIDFromDir(taskDir)
	rec, res := v.loadIntakeRecord(intakeID)
	if res.Status != StatusPass {
		return res
	}
	return v.intakeBaselineValid(intakeID, rec)
}

// GateDiscoveryDecision 是 Discovery 第二阶段入口 Gate：无 Owner Decision 时 FAIL。
func (v *Validator) GateDiscoveryDecision(taskDir string) Result {
	intakeID := intakeIDFromDir(taskDir)
	rec, res := v.loadIntakeRecord(intakeID)
	if res.Status != StatusPass {
		return res
	}
	if res := v.intakeBaselineValid(intakeID, rec); res.Status != StatusPass {
		return res
	}
	fm, ok, err := v.loadMarkdownFrontMatter(ownerDecisionPath(intakeID))
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: %v", ownerDecisionPath(intakeID), err)}
	}
	if !ok {
		return failResult(intakeID, "Discovery Decision", "owner-decision.md 无 front-matter", "decision: APPROVED", "无 Owner Decision 时第二阶段入口不得通过")
	}
	d, err := parseOwnerDecision(fm)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if d.Decision != OwnerDecisionApproved {
		return failResult(intakeID, "Discovery Decision", fmt.Sprintf("decision=%s", d.Decision), "APPROVED", "Owner 未批准，第二阶段入口不得通过")
	}
	return Result{Status: StatusPass}
}

// GateDiscoveryHandoff 是 Discovery 第二阶段出口 Gate：校验 taskbuilder-handoff.md 已形成、
// 机器可用，且绑定 Owner 已确认的范围与 Git 基线（与 Intake 记录一致）。
func (v *Validator) GateDiscoveryHandoff(taskDir string) Result {
	intakeID := intakeIDFromDir(taskDir)
	rec, res := v.loadIntakeRecord(intakeID)
	if res.Status != StatusPass {
		return res
	}
	if res := v.intakeBaselineValid(intakeID, rec); res.Status != StatusPass {
		return res
	}
	// Owner 决策必须已批准。
	fm, ok, err := v.loadMarkdownFrontMatter(ownerDecisionPath(intakeID))
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: %v", ownerDecisionPath(intakeID), err)}
	}
	if !ok {
		return failResult(intakeID, "Discovery Handoff", "owner-decision.md 无 front-matter", "decision: APPROVED", "无 Owner Decision 不得形成 TaskBuilder Handoff")
	}
	d, err := parseOwnerDecision(fm)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if d.Decision != OwnerDecisionApproved {
		return failResult(intakeID, "Discovery Handoff", fmt.Sprintf("decision=%s", d.Decision), "APPROVED", "Owner 未批准，不得形成 TaskBuilder Handoff")
	}

	// taskbuilder-handoff.md 机器可用且与 Intake 基线一致。
	hfm, ok, err := v.loadMarkdownFrontMatter(taskBuilderHandoffPath(intakeID))
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: %v", taskBuilderHandoffPath(intakeID), err)}
	}
	if !ok {
		return failResult(intakeID, "Discovery Handoff", "taskbuilder-handoff.md 无 front-matter", "有效的 TaskBuilder Handoff front-matter", "缺少 taskbuilder-handoff.md")
	}
	h, err := parseTaskBuilderHandoff(hfm)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if h.IntakeID != intakeID {
		return failResult(intakeID, "Discovery Handoff", fmt.Sprintf("taskbuilder-handoff.intake_id=%s", h.IntakeID), intakeID, "TaskBuilder Handoff 的 intake_id 与 Intake 目录不一致")
	}
	if h.ParentBranch != rec.ParentBranch {
		return failResult(intakeID, "Discovery Handoff", fmt.Sprintf("taskbuilder-handoff.parent_branch=%s", h.ParentBranch), rec.ParentBranch, "TaskBuilder Handoff 的父分支与 Intake 基线不一致")
	}
	if h.BaseCommit != rec.BaseCommit {
		return failResult(intakeID, "Discovery Handoff", fmt.Sprintf("taskbuilder-handoff.base_commit=%s", h.BaseCommit), rec.BaseCommit, "TaskBuilder Handoff 的 base_commit 与 Intake 基线不一致")
	}
	if d.IntakeID != "" || d.TaskID != "" || d.TaskType != "" || d.ParentBranch != "" || d.BaseCommit != "" {
		return v.intakeDecisionBinding(intakeID, h)
	}
	return Result{Status: StatusPass}
}

// Reuse the original Owner decision, not the Handoff's APPROVED mirror. This
// binds structured scope facts; it cannot authenticate the human or compare
// free-form business intent. No new Task State or approval mechanism is added.
func (v *Validator) intakeDecisionBinding(id string, h TaskBuilderHandoff) Result {
	fm, ok, err := v.loadMarkdownFrontMatter(ownerDecisionPath(id))
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if !ok {
		return failResult(id, "Intake Owner Binding", "missing decision", "original bound approval", "缺原始批准")
	}
	d, err := parseOwnerDecision(fm)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if d.Decision != OwnerDecisionApproved || d.IntakeID != id || d.TaskID != h.TaskID || d.TaskType != h.TaskType || d.ParentBranch != h.ParentBranch || d.BaseCommit != h.BaseCommit {
		return failResult(id, "Intake Owner Binding", "missing/mismatched original approval facts", "Owner-approved Intake/Task/type/parent/base equals Handoff", "旧版未绑定批准仅可历史读取；新初始化需Owner明确确认的新绑定决策，不回填历史批准")
	}
	return Result{Status: StatusPass}
}
