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
	if reason := initialTaskPolicy(s); reason != "" {
		return failResult(taskSlug, "TaskBuilder Init", reason, "已批准类型的未执行初始 State/首个角色", "初始化不允许继承批准、审查或错派角色；已有活动任务应按恢复协议继续")
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
	branch, err := v.Git.CurrentBranch()
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check current task worktree: %v", err)}
	}
	if branch != s.Git.TaskBranch || branch == s.Git.ParentBranch {
		return failResult(taskSlug, "TaskBuilder Init", branch, s.Git.TaskBranch, "必须在该 Task 的独立 Worktree 核验，不能借用另一分支/父分支的文件")
	}
	head, err := v.Git.RevParse("HEAD")
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if ok, err := v.Git.IsAncestor(s.Git.BaseCommit, head); err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	} else if !ok {
		return failResult(taskSlug, "TaskBuilder Init", head, s.Git.BaseCommit+" 的后代", "当前 Task 历史未承继 Owner 批准的 Base")
	}
	return Result{Status: StatusPass}
}

// initialTaskPolicy validates NEW schema-4 task initialization only. It does
// not classify a request, initialize State, schedule Agents, or reopen an old
// task. The approved type still comes from the original Intake decision.
func initialTaskPolicy(s State) string {
	contract, firstRole := ContractNotRequired, RoleCoder
	switch s.TaskType {
	case TaskTypeNormal:
	case TaskTypeComplex:
		contract, firstRole = ContractPending, RoleAnalyst
	case TaskTypeDesign:
		firstRole = RoleArchitect
	default:
		return "Program/未知类型不适用 Task 初始化"
	}
	if s.Contract.Status != contract || s.Contract.Target != "" || s.Contract.Revision != 0 || len(s.Contract.History) != 0 {
		return "contract 初值必须为 " + contract + " 且不含旧 target/revision/history"
	}
	if s.Review.Status != ReviewNotRequested || s.Review.Target != "" || s.Owner.Status != OwnerPending || s.Owner.ReviewTarget != "" ||
		s.Delivery.Status != DeliveryNotRun || s.Delivery.ReviewTarget != "" || s.Delivery.FeatureHead != "" || s.Delivery.DevelopBase != "" {
		return "review/owner/delivery 必须为未执行初值，不继承 CLEAN、接受或交付证据"
	}
	if s.Blocked.Active || len(s.Resources.Reservations) != 0 {
		return "初始化必须未阻塞，资源预留留待已授权 Analyst 流程"
	}
	if s.TaskType == TaskTypeDesign && (s.Design == nil || s.Design.Status != DesignPending) {
		return "DESIGN 必须有 design.status=PENDING"
	}
	if d := s.Design; d != nil {
		if s.TaskType != TaskTypeDesign && d.Status != DesignNotRequired {
			return "非 DESIGN 初始化不能携带活动架构审批"
		}
		if d.ApprovedVersion != 0 || d.ApprovedTarget != "" || d.FinalVersion != 0 || d.FinalTarget != "" || d.ReviewTarget != "" {
			return "初始化不能继承旧 architecture/review/final 批准或 SHA"
		}
	}
	// The initial TaskBuilder Gate precedes Handoff creation. Rechecking after
	// that metadata commit is supported, but any supplied route must be exact.
	if h := s.Handoff; h != nil && (h.Scope != HandoffScopeTask || h.ScopeID != s.TaskID || h.Workflow != s.TaskType || h.NextRole != firstRole) {
		return "初始 Handoff 必须绑定本 Task/类型且 next_role=" + firstRole
	}
	return ""
}
