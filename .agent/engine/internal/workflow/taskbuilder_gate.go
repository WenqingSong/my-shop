package workflow

import (
	"fmt"
	"path/filepath"
)

// 本文件实现 TaskBuilder 出口 Gate（taskbuilder-init）：校验任务已完整初始化——
// 未经 Discovery 与 Owner 决策不得初始化；创建时绑定父分支及 Base Commit；分支已存在、
// Worktree 已存在、任务状态与真实 Git 证据可校验。
//
// 它只读校验，不创建/覆盖分支或 Worktree（实际创建由 workflow-git 写工具承担，且同样拒绝覆盖），
// 不启动任何 Agent。

// GateTaskBuilderInit 是 TaskBuilder 出口 Gate。
func (v *Validator) GateTaskBuilderInit(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if s.SchemaVersion != SchemaV4 {
		return Result{Status: StatusError, Error: fmt.Sprintf("taskbuilder-init 仅适用于 schema_version=4 任务（当前 %d）", s.SchemaVersion)}
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if s.TaskID == "" || s.TaskID != taskSlug {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("task_id=%q", s.TaskID), taskSlug, "state.task_id 与任务目录不一致")
	}
	if !ValidTaskType(s.TaskType) {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("task_type=%s", s.TaskType), "NORMAL | COMPLEX | DESIGN", "NORMAL/COMPLEX/DESIGN 路由错误")
	}
	if s.Git == nil || s.Git.ParentBranch == "" || s.Git.BaseCommit == "" || s.Git.TaskBranch == "" {
		return failResult(taskSlug, "TaskBuilder Init", "git 基线不完整", "parent_branch/base_commit/task_branch 齐备", "创建时必须绑定父分支及 Base Commit")
	}
	// 未经 Discovery 与 Owner 决策不得初始化：必须绑定 Intake 引用。
	if s.Intake == nil || s.Intake.ID == "" {
		return failResult(taskSlug, "TaskBuilder Init", "intake 引用缺失", "intake.id", "未经 Discovery 和 Owner 决策不得初始化")
	}

	// task.md 已产出（任务定义是「任务已完整初始化」的必备产物）。
	if !v.fileNonEmpty(filepath.Join(taskDir, "task.md")) {
		return failResult(taskSlug, "TaskBuilder Init", "task.md 缺失或为空", "非空 task.md", "任务未完整初始化（缺少 task.md）")
	}

	// 载入并核对 Discovery 产物（Owner 已批准的范围与 Git 基线）。
	if res := v.GateDiscoveryHandoff(intakeDir(s.Intake.ID)); res.Status != StatusPass {
		for i := range res.Issues {
			res.Issues[i].Reason = res.Issues[i].Check + ": " + res.Issues[i].Reason
			res.Issues[i].Check = "TaskBuilder Init"
		}
		return res
	}
	rec, res := v.loadIntakeRecord(s.Intake.ID)
	if res.Status != StatusPass {
		return Result{Status: StatusError, Error: fmt.Sprintf("载入 intake %s: %s", s.Intake.ID, res.Error)}
	}
	hfm, ok, err := v.loadMarkdownFrontMatter(taskBuilderHandoffPath(s.Intake.ID))
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: %v", taskBuilderHandoffPath(s.Intake.ID), err)}
	}
	if !ok {
		return failResult(taskSlug, "TaskBuilder Init", "taskbuilder-handoff.md 无 front-matter", "有效的 TaskBuilder Handoff", "缺少 taskbuilder-handoff.md（未经 Discovery 不得初始化）")
	}
	h, err := parseTaskBuilderHandoff(hfm)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := v.intakeDecisionBinding(s.Intake.ID, h); res.Status != StatusPass {
		return res
	}
	if h.OwnerDecision != OwnerDecisionApproved {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("owner_decision=%s", h.OwnerDecision), "APPROVED", "Owner 决策未批准，不得初始化")
	}
	if h.TaskID != s.TaskID {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("taskbuilder-handoff.task_id=%s", h.TaskID), s.TaskID, "Task 标识与 Owner 确认的范围不一致")
	}
	if h.TaskType != s.TaskType {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("taskbuilder-handoff.task_type=%s", h.TaskType), s.TaskType, "工作流模式与 Owner 确认的范围不一致")
	}
	if h.ParentBranch != s.Git.ParentBranch || h.BaseCommit != s.Git.BaseCommit {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("state.git=(%s@%s) handoff=(%s@%s)", s.Git.ParentBranch, s.Git.BaseCommit, h.ParentBranch, h.BaseCommit), "一致", "任务 Git 基线未绑定 Owner 确认的父分支与 Base Commit")
	}
	if h.ParentBranch != rec.ParentBranch || h.BaseCommit != rec.BaseCommit {
		return failResult(taskSlug, "TaskBuilder Init", "taskbuilder-handoff 与 Intake 基线不一致", "一致", "TaskBuilder Handoff 未绑定 Intake 的 Git 基线")
	}

	// 真实 Git 证据：父分支存在、base_commit 真实且为父分支祖先、task_branch 存在、worktree 存在。
	if res := v.intakeBaselineValid(s.Intake.ID, rec); res.Status != StatusPass {
		return res
	}
	if ok, err := v.Git.BranchExists(s.Git.TaskBranch); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check task branch %s: %v", s.Git.TaskBranch, err)}
	} else if !ok {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("task_branch=%s", s.Git.TaskBranch), "真实存在的分支", "任务分支不存在（任务未完整初始化）")
	}
	if ok, err := v.Git.WorktreeExists(s.Git.TaskBranch); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check worktree %s: %v", s.Git.TaskBranch, err)}
	} else if !ok {
		return failResult(taskSlug, "TaskBuilder Init", fmt.Sprintf("worktree(%s)", s.Git.TaskBranch), "真实存在的 worktree", "任务 Worktree 不存在（任务未完整初始化）")
	}
	return Result{Status: StatusPass}
}
