package workflow

import (
	"fmt"
	"os"
	"path/filepath"
)

// 本文件实现 Workflow V4 DESIGN 工作流专属 Gate（Task 11）。
//
// DESIGN 固定流程（角色级，不引入自动调度）：
//
//	Discovery → Owner 决策 → TaskBuilder → Architect → Owner 架构评审（Checkpoint 1）
//	→ DocsSpecWriter → Cleaner（独立设计审查）→ OwnerGate → Owner 最终批准（Checkpoint 2）→ Merger。
//
// 本文件只提供 DESIGN 专属 Gate 与设计 Evidence 检查，不重复定义 State / Handoff /
// Git Workspace / Event / Merger，不修改全局 Gate CLI 注册机制。跨 Agent 尚未接入的
// 端到端验收（CLI 注册、recovery 候选 Gate 派生、Merger 接线）标记为 PENDING_INTEGRATION。
//
// Evidence 绑定事实（复用 Schema V4 的 design.* 与既有 review.* / owner.*，不新增字段）：
//   - approved_target：Owner 批准架构的 Evidence Commit SHA；
//   - review_target（design.review_target）：Cleaner 实际审查的设计交付 SHA；
//   - final_target：Owner 最终批准的设计交付 SHA。
//
// 每个 SHA 都经过 Git 真实性（CommitExists）、祖先关系（IsAncestor）与相应文件内容一致性检查。
//
// 能力边界：本引擎无法从机器层证明「真实 Owner 身份」。State 只记录 SHA 绑定事实；
// Owner 身份由 Git 侧（Evidence Commit 提交者 + shared branch protection）在集成侧提供，
// 本 Gate 不伪造身份认证能力，只机械校验 SHA 真实性、祖先关系与文件内容一致。

// architecturePath 返回 Architect 技术架构方案的固定路径（相对仓库根）。
// 该文件是「已批准架构」的 Evidence 对象，任何实质修改都会使旧批准失效。
func architecturePath(taskSlug string) string {
	return filepath.Join(".agent", "tasks", taskSlug, "architecture.md")
}

// designSpecPath 返回 DocsSpecWriter 正式工程设计与规范文档的固定路径（相对仓库根）。
// 该文件是「设计交付」的 Evidence 对象，任何实质修改都会使旧审查/批准失效。
func designSpecPath(taskSlug string) string {
	return filepath.Join(".agent", "tasks", taskSlug, "design.md")
}

// designReviewTarget 安全返回 design.review_target；design section 缺失时返回空串。
func designReviewTarget(s State) string {
	if s.Design == nil {
		return ""
	}
	return s.Design.ReviewTarget
}

// designTaskCheck 校验调用方确实是 DESIGN 工作流任务。非 DESIGN 任务调用 DESIGN Gate 属于业务 FAIL。
func (v *Validator) designTaskCheck(taskSlug string, s State) Result {
	if s.TaskType != TaskTypeDesign {
		return failResult(taskSlug, "Design Task Type", fmt.Sprintf("task_type=%s", s.TaskType), "DESIGN", "该 Gate 仅适用于 DESIGN 工作流任务")
	}
	if s.Git == nil || s.Git.BaseCommit == "" || s.Git.ParentBranch == "" || s.Git.TaskBranch == "" {
		return failResult(taskSlug, "Design Task Type", "git 基线缺失", "git.parent_branch/base_commit/task_branch", "DESIGN 任务必须绑定初始化 Git 基线")
	}
	return Result{Status: StatusPass}
}

// designEvidenceValid 校验一个设计 Evidence SHA 的真实性、祖先关系与绑定文件存在性。
// contentCheck=true 时额外校验该文件在 target 上的内容与当前工作区一致（检测批准/审查后漂移）。
func (v *Validator) designEvidenceValid(taskSlug, sha, path, check string, contentCheck bool) Result {
	if sha == "" {
		return failResult(taskSlug, check, "Evidence SHA 为空", "<evidence-commit-sha>", "必须绑定设计 Evidence Commit")
	}
	if ok, err := v.Git.CommitExists(sha); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check %s %s: %v", check, sha, err)}
	} else if !ok {
		return failResult(taskSlug, check, sha, "真实 commit", "Evidence SHA 不是真实 commit")
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if ok, err := v.Git.IsAncestor(sha, head); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", sha, err)}
	} else if !ok {
		return failResult(taskSlug, check, sha, "当前 feature HEAD 的祖先", "Evidence SHA 不在当前 feature 历史中")
	}
	if ok, err := v.Git.PathExistsAt(sha, path); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check %s@%s: %v", path, sha, err)}
	} else if !ok {
		return failResult(taskSlug, check, fmt.Sprintf("%s@%s 不存在", path, sha), path, "Evidence Commit 缺少绑定文件")
	}
	if !contentCheck {
		return Result{Status: StatusPass}
	}
	approved, err := v.Git.ShowFile(sha, path)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("show %s@%s: %v", path, sha, err)}
	}
	current, err := v.readWorkingTree(path)
	if err != nil {
		if os.IsNotExist(err) {
			return failResult(taskSlug, check, fmt.Sprintf("%s 当前不存在", path), "与 Evidence SHA 内容一致", "批准对象已被删除，旧批准失效")
		}
		return Result{Status: StatusError, Error: fmt.Sprintf("read working tree %s: %v", path, err)}
	}
	if approved != current {
		return failResult(taskSlug, check, fmt.Sprintf("%s 已偏离 %s", path, sha), "与 Evidence SHA 内容一致", "批准对象已发生实质修改，旧批准失效")
	}
	return Result{Status: StatusPass}
}

// architectureApprovalValid 校验「已批准架构」的绑定事实完整有效（供多个 DESIGN Gate 复用）。
// 覆盖 AC：已批准架构不能被静默替换（漂移即 FAIL）；缺批准即 FAIL。
func (v *Validator) architectureApprovalValid(taskSlug string, s State) Result {
	if s.Design == nil {
		return failResult(taskSlug, "Architecture Approval", "design section 缺失", "design section 存在", "DESIGN 任务必须声明 design section")
	}
	d := s.Design
	if d.Status != DesignApproved {
		return failResult(taskSlug, "Architecture Approval", fmt.Sprintf("design.status=%s", d.Status), "APPROVED", "架构必须经 Owner 批准（APPROVED）")
	}
	if d.ApprovedVersion <= 0 {
		return failResult(taskSlug, "Architecture Approval", fmt.Sprintf("approved_version=%d", d.ApprovedVersion), ">=1", "已批准架构必须绑定版本号")
	}
	if d.ApprovedTarget == "" {
		return failResult(taskSlug, "Architecture Approval", "approved_target 为空", "<evidence-commit-sha>", "已批准架构必须绑定 Evidence Commit")
	}
	return v.designEvidenceValid(taskSlug, d.ApprovedTarget, architecturePath(taskSlug), "Architecture Approval", true)
}

// designOwnerAcceptanceValid 校验 Owner 已对当前设计审查快照明确 ACCEPT（最终批准的前置，非 Cleaner PASS 可替代）。
func (v *Validator) designOwnerAcceptanceValid(taskSlug string, s State) Result {
	if s.Owner.Status != OwnerAccepted {
		return failResult(taskSlug, "Design Owner Acceptance", fmt.Sprintf("owner.status=%s", s.Owner.Status), "ACCEPTED", "缺最终 Owner ACCEPT 不得交付")
	}
	if s.Owner.ReviewTarget != s.Review.Target {
		return failResult(taskSlug, "Design Owner Acceptance", fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget), s.Review.Target, "Owner Acceptance 必须绑定当前 review.target")
	}
	return Result{Status: StatusPass}
}

// designBaselineValid 校验最终交付仍绑定初始化父分支：base_commit 是 target 的祖先。
func (v *Validator) designBaselineValid(taskSlug string, s State, target string) Result {
	if s.Git == nil || s.Git.BaseCommit == "" {
		return failResult(taskSlug, "Design Baseline", "git.base_commit 为空", "<base-commit-sha>", "DESIGN 任务必须绑定初始化父分支基线")
	}
	if ok, err := v.Git.CommitExists(s.Git.BaseCommit); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check git.base_commit %s: %v", s.Git.BaseCommit, err)}
	} else if !ok {
		return failResult(taskSlug, "Design Baseline", s.Git.BaseCommit, "真实 commit", "git.base_commit 不是真实 commit")
	}
	if ok, err := v.Git.IsAncestor(s.Git.BaseCommit, target); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s %s: %v", s.Git.BaseCommit, target, err)}
	} else if !ok {
		return failResult(taskSlug, "Design Baseline", target, "git.base_commit 的后代", "最终交付必须仍绑定初始化父分支")
	}
	return Result{Status: StatusPass}
}

// GateDesignArchitectStart 是 Architect 的入口 Gate。
// 前提：DESIGN 任务、未阻塞、Handoff Ready、架构尚未批准（design.status=PENDING 且无 approved_target）。
func (v *Validator) GateDesignArchitectStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Architect Start", "blocked.active=true", "false", "任务被阻塞，不得开始架构设计")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	if s.Design == nil {
		return failResult(taskSlug, "Architect Start", "design section 缺失", "design section 存在", "DESIGN 任务必须声明 design section")
	}
	if s.Design.Status != DesignPending {
		return failResult(taskSlug, "Architect Start", fmt.Sprintf("design.status=%s", s.Design.Status), "PENDING", "Architect 只能在架构待定（PENDING）时开始")
	}
	if s.Design.ApprovedTarget != "" || s.Design.ApprovedVersion != 0 {
		return failResult(taskSlug, "Architect Start", fmt.Sprintf("approved_version=%d approved_target=%s", s.Design.ApprovedVersion, s.Design.ApprovedTarget), "未批准", "已存在已批准架构，不得重新开始 Architect")
	}
	return Result{Status: StatusPass}
}

// GateDesignArchitectureReview 是第一个 Owner Checkpoint（架构批准）。
// 校验 Owner 已批准架构的绑定事实完整有效：design.status=APPROVED、approved_version>=1、
// approved_target 为真实 commit、位于当前 feature 历史、且 architecture.md 内容与工作区一致（未漂移）。
func (v *Validator) GateDesignArchitectureReview(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Architecture Approval", "blocked.active=true", "false", "任务被阻塞，不得记录架构批准")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	return v.architectureApprovalValid(taskSlug, s)
}

// GateDesignDocsSpecWriterStart 是 DocsSpecWriter 的入口 Gate。
// 前提：架构已批准且未漂移。缺架构批准不能进入 DocsSpecWriter；架构漂移导致 FAIL（不允许静默接受新架构）。
func (v *Validator) GateDesignDocsSpecWriterStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "DocsSpecWriter Start", "blocked.active=true", "false", "任务被阻塞，不得开始设计规范编写")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	// 架构批准绑定是 DocsSpecWriter 的硬前提：缺批准或漂移均 FAIL（复用架构批准校验结果）。
	return v.architectureApprovalValid(taskSlug, s)
}

// GateDesignCleanerStart 是 DESIGN Cleaner 的入口 Gate。
// 前提：架构仍有效、设计交付已提交且 review.status=PENDING、review.target 绑定真实设计交付 Evidence。
// DESIGN 是纯设计工作流：不得因为没有 Coder 业务实现就直接判定 FAIL。
func (v *Validator) GateDesignCleanerStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Design Cleaner Start", "blocked.active=true", "false", "任务被阻塞，不得开始设计审查")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	if res := v.architectureApprovalValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Review.Status != ReviewPending {
		return failResult(taskSlug, "Design Cleaner Start", fmt.Sprintf("review.status=%s", s.Review.Status), "PENDING", "Cleaner 只能审查 DocsSpecWriter 已发起的 PENDING 设计审查")
	}
	if s.Review.Target == "" {
		return failResult(taskSlug, "Design Cleaner Start", "review.target 为空", "<design-delivery-sha>", "review.target 必须绑定设计交付 Evidence Commit")
	}
	return v.designEvidenceValid(taskSlug, s.Review.Target, designSpecPath(taskSlug), "Design Review Target", false)
}

// GateDesignOwnerGateStart 是 DESIGN OwnerGate 的入口 Gate。
// 前提：Cleaner 设计审查 CLEAN 且仍有效（review.target 后无实质变化，design.md 属 substantive）、
// 架构仍有效、design.review_target 绑定当前 review.target。
func (v *Validator) GateDesignOwnerGateStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Design OwnerGate Start", "blocked.active=true", "false", "任务被阻塞，不得请求 Owner 最终决策")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	if res := v.architectureApprovalValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	// CLEAN 有效性：review.status==CLEAN 且 review.target 后无实质变化（default-deny，design 文件 substantive）。
	if res := v.cleanValidity(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	if designReviewTarget(s) != s.Review.Target {
		return failResult(taskSlug, "Design OwnerGate Start", fmt.Sprintf("design.review_target=%s", designReviewTarget(s)), s.Review.Target, "design.review_target 必须绑定当前 review.target")
	}
	return Result{Status: StatusPass}
}

// GateDesignFinalApproval 是第二个 Owner Checkpoint（最终设计批准）。
// 校验：Owner 已 ACCEPT（owner.status=ACCEPTED 且绑定 review.target）、final_version>=1、
// final_target 绑定当前 review.target（最终批准绑定已审查快照，不等于 Cleaner PASS）、
// final_target 真实有效、且仍绑定初始化父分支（base_commit 为其祖先）。
func (v *Validator) GateDesignFinalApproval(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := v.designTaskCheck(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Final Design Approval", "blocked.active=true", "false", "任务被阻塞，不得记录最终设计批准")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	if res := v.architectureApprovalValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := v.cleanValidity(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	if res := v.designOwnerAcceptanceValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Design == nil {
		return failResult(taskSlug, "Final Design Approval", "design section 缺失", "design section 存在", "DESIGN 任务必须声明 design section")
	}
	d := s.Design
	if d.FinalVersion <= 0 {
		return failResult(taskSlug, "Final Design Approval", fmt.Sprintf("final_version=%d", d.FinalVersion), ">=1", "最终设计批准必须绑定版本号")
	}
	if d.FinalTarget == "" {
		return failResult(taskSlug, "Final Design Approval", "final_target 为空", "<final-design-sha>", "最终设计批准必须绑定设计交付 Evidence Commit")
	}
	if d.FinalTarget != s.Review.Target {
		return failResult(taskSlug, "Final Design Approval", fmt.Sprintf("final_target=%s", d.FinalTarget), s.Review.Target, "最终批准必须绑定 Cleaner 已审查的设计交付快照（最终批准不等于 Cleaner PASS）")
	}
	if d.ReviewTarget != s.Review.Target {
		return failResult(taskSlug, "Final Design Approval", fmt.Sprintf("design.review_target=%s", d.ReviewTarget), s.Review.Target, "design.review_target 必须绑定当前 review.target")
	}
	if res := v.designEvidenceValid(taskSlug, d.FinalTarget, designSpecPath(taskSlug), "Final Design Approval", true); res.Status != StatusPass {
		return res
	}
	return v.designBaselineValid(taskSlug, s, d.FinalTarget)
}
