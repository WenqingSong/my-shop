package workflow

import "testing"

// 本文件覆盖 DESIGN 工作流专属 Gate（Task 11）的 AC，使用与现有 Gate 相同的临时 git 仓库 fixture。
//
// DESIGN 是纯设计工作流：下面所有 happy-path fixture 都不包含任何 Coder 业务实现（无 internal/*.go），
// 以此锁定「DESIGN 无需生产业务代码，不得因为没有 Coder 实现直接判定 FAIL」。

// setupDesignRepo 构建 DESIGN 任务的最小仓库：develop 基线 + feature 分支。
func setupDesignRepo(t *testing.T) (*testRepo, string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v4-design")
	return r, base
}

// designState 返回最小合法 V4 DESIGN State（架构 PENDING，尚未批准）。
func designState(base string) State {
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeDesign
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v4-design"}
	s.Design = &Design{Status: DesignPending}
	return s
}

// setupDesignPending 构建 Architect 入口前置：DESIGN 任务，架构 PENDING，未批准。
func setupDesignPending(t *testing.T) (*testRepo, map[string]string) {
	r, base := setupDesignRepo(t)
	s := designState(base)
	r.writeState(s)
	shas := map[string]string{"base": base, "S1": r.commit("design pending state")}
	r.syncOrigin("feature/agent-workflow-v4-design")
	return r, shas
}

// setupDesignArchitectureApproved 构建架构批准（Checkpoint 1）前置：A1 architecture Evidence + A2 批准 metadata。
func setupDesignArchitectureApproved(t *testing.T) (*testRepo, map[string]string) {
	r, base := setupDesignRepo(t)
	r.write(".agent/tasks/demo/architecture.md", "# architecture v1\n")
	shas := map[string]string{"base": base, "A1": r.commit("architecture")}

	s := designState(base)
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"]}
	r.writeState(s)
	shas["A2"] = r.commit("architecture approval metadata")
	r.syncOrigin("feature/agent-workflow-v4-design")
	return r, shas
}

// finalDesignState 返回最终批准后的完整 DESIGN State（Owner ACCEPTED + final 绑定 review.target）。
func finalDesignState(shas map[string]string) State {
	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"], FinalVersion: 1, FinalTarget: shas["D1"], ReviewTarget: shas["D1"]}
	s.Review = Review{Status: ReviewClean, Target: shas["D1"]}
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: shas["D1"]}
	return s
}

// setupDesignFinalApproved 构建最终批准（Checkpoint 2）前置的完整历史：
// D1 design 交付 → D2 PENDING → C1 CLEAN（findings/core-logic）→ F1 最终批准。
func setupDesignFinalApproved(t *testing.T) (*testRepo, map[string]string) {
	r, shas := setupDesignArchitectureApproved(t)

	r.write(".agent/tasks/demo/design.md", "# design v1\n")
	shas["D1"] = r.commit("design delivery")

	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"]}
	s.Review = Review{Status: ReviewPending, Target: shas["D1"]}
	r.writeState(s)
	shas["D2"] = r.commit("design review request")

	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s.Review = Review{Status: ReviewClean, Target: shas["D1"]}
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"], ReviewTarget: shas["D1"]}
	r.writeState(s)
	shas["C1"] = r.commit("design clean review")

	r.write(".agent/tasks/demo/owner-decision.md", "# decision\n")
	r.writeState(finalDesignState(shas))
	shas["F1"] = r.commit("final approval")
	r.syncOrigin("feature/agent-workflow-v4-design")
	return r, shas
}

// --- Architect Start ---

func TestDesignGateArchitectStartHappy(t *testing.T) {
	r, _ := setupDesignPending(t)
	expectStatus(t, r.validator().GateDesignArchitectStart(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateArchitectStartNonDesign(t *testing.T) {
	r, base := setupDesignRepo(t)
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v4-design"}
	r.writeState(s)
	r.commit("normal v4 state")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignArchitectStart(".agent/tasks/demo"), "Design Task Type")
}

func TestDesignGateArchitectStartAlreadyApproved(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	expectCheck(t, r.validator().GateDesignArchitectStart(".agent/tasks/demo"), "Architect Start")
}

func TestDesignGateArchitectStartBlocked(t *testing.T) {
	r, _ := setupDesignPending(t)
	s := designState(r.git("rev-parse", "HEAD~0"))
	s.Blocked = Blocked{Active: true, By: "TaskBuilder", Reason: "blocked"}
	r.writeState(s)
	r.commit("blocked state")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignArchitectStart(".agent/tasks/demo"), "Architect Start")
}

// --- 架构批准（Checkpoint 1）---

func TestDesignGateArchitectureReviewHappy(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	expectStatus(t, r.validator().GateDesignArchitectureReview(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateArchitectureReviewNotApproved(t *testing.T) {
	r, _ := setupDesignPending(t)
	expectCheck(t, r.validator().GateDesignArchitectureReview(".agent/tasks/demo"), "Architecture Approval")
}

func TestDesignGateArchitectureReviewFakeTarget(t *testing.T) {
	r, shas := setupDesignArchitectureApproved(t)
	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}
	r.writeState(s)
	r.commit("fake approved target")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignArchitectureReview(".agent/tasks/demo"), "Architecture Approval")
}

func TestDesignGateArchitectureReviewMissingFile(t *testing.T) {
	r, base := setupDesignRepo(t)
	// approved_target 指向真实但缺少 architecture.md 的基线 commit。
	s := designState(base)
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: base}
	r.writeState(s)
	r.commit("approval metadata pointing at baseline")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignArchitectureReview(".agent/tasks/demo"), "Architecture Approval")
}

func TestDesignGateArchitectureReviewDrift(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	r.write(".agent/tasks/demo/architecture.md", "# architecture v2\n")
	r.commit("architecture drift")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignArchitectureReview(".agent/tasks/demo"), "Architecture Approval")
}

// --- DocsSpecWriter Start ---

func TestDesignGateDocsSpecWriterStartHappy(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	expectStatus(t, r.validator().GateDesignDocsSpecWriterStart(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateDocsSpecWriterStartNoApproval(t *testing.T) {
	r, _ := setupDesignPending(t)
	// 缺架构批准不能进入 DocsSpecWriter。
	expectCheck(t, r.validator().GateDesignDocsSpecWriterStart(".agent/tasks/demo"), "Architecture Approval")
}

func TestDesignGateDocsSpecWriterStartDrift(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	r.write(".agent/tasks/demo/architecture.md", "# architecture v2\n")
	r.commit("architecture drift")
	r.syncOrigin("feature/agent-workflow-v4-design")
	// 架构漂移导致 FAIL，不允许 DocsSpecWriter 静默接受新架构。
	expectCheck(t, r.validator().GateDesignDocsSpecWriterStart(".agent/tasks/demo"), "Architecture Approval")
}

// --- DESIGN Cleaner Start ---

func TestDesignGateCleanerStartHappy(t *testing.T) {
	r, shas := setupDesignArchitectureApproved(t)
	r.write(".agent/tasks/demo/design.md", "# design v1\n")
	d1 := r.commit("design delivery")
	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"]}
	s.Review = Review{Status: ReviewPending, Target: d1}
	r.writeState(s)
	r.commit("design review request")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectStatus(t, r.validator().GateDesignCleanerStart(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateCleanerStartNotPending(t *testing.T) {
	r, _ := setupDesignArchitectureApproved(t)
	expectCheck(t, r.validator().GateDesignCleanerStart(".agent/tasks/demo"), "Design Cleaner Start")
}

func TestDesignGateCleanerStartFakeTarget(t *testing.T) {
	r, shas := setupDesignArchitectureApproved(t)
	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"]}
	s.Review = Review{Status: ReviewPending, Target: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}
	r.writeState(s)
	r.commit("fake review target")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignCleanerStart(".agent/tasks/demo"), "Design Review Target")
}

// --- DESIGN OwnerGate Start ---

func TestDesignGateOwnerGateStartHappy(t *testing.T) {
	r, shas := setupDesignFinalApproved(t)
	// 最终批准前的 OwnerGate 入口：CLEAN 且 design.review_target 绑定 review.target。
	// 注：此处 state 已含 final 字段，但 OwnerGate 入口 Gate 不校验 final，仍应 PASS。
	_ = shas
	expectStatus(t, r.validator().GateDesignOwnerGateStart(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateOwnerGateStartNotClean(t *testing.T) {
	r, shas := setupDesignArchitectureApproved(t)
	r.write(".agent/tasks/demo/design.md", "# design v1\n")
	d1 := r.commit("design delivery")
	s := designState(shas["base"])
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: shas["A1"]}
	s.Review = Review{Status: ReviewPending, Target: d1}
	r.writeState(s)
	r.commit("design review request")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignOwnerGateStart(".agent/tasks/demo"), "CLEAN Validity")
}

func TestDesignGateOwnerGateStartReviewTargetMismatch(t *testing.T) {
	r, shas := setupDesignFinalApproved(t)
	s := finalDesignState(shas)
	s.Design.ReviewTarget = shas["A1"] // 错误绑定：design.review_target 指向架构而非设计交付
	r.writeState(s)
	r.commit("review target mismatch")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignOwnerGateStart(".agent/tasks/demo"), "Design OwnerGate Start")
}

// --- 最终设计批准（Checkpoint 2）---

func TestDesignGateFinalApprovalHappy(t *testing.T) {
	r, _ := setupDesignFinalApproved(t)
	expectStatus(t, r.validator().GateDesignFinalApproval(".agent/tasks/demo"), StatusPass)
}

func TestDesignGateFinalApprovalNoOwnerAccept(t *testing.T) {
	r, shas := setupDesignFinalApproved(t)
	s := finalDesignState(shas)
	s.Owner.Status = OwnerPending
	r.writeState(s)
	r.commit("owner not accepted")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignFinalApproval(".agent/tasks/demo"), "Design Owner Acceptance")
}

func TestDesignGateFinalApprovalFinalTargetMismatch(t *testing.T) {
	r, shas := setupDesignFinalApproved(t)
	s := finalDesignState(shas)
	s.Design.FinalTarget = shas["A1"] // 最终批准绑定架构而非已审查设计交付
	r.writeState(s)
	r.commit("final target mismatch")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignFinalApproval(".agent/tasks/demo"), "Final Design Approval")
}

func TestDesignGateFinalApprovalBaselineDrift(t *testing.T) {
	r, shas := setupDesignFinalApproved(t)
	s := finalDesignState(shas)
	// base_commit 被指向 final_target 的后代（非祖先），最终交付不再绑定初始化父分支。
	s.Git.BaseCommit = shas["F1"]
	r.writeState(s)
	r.commit("baseline drift")
	r.syncOrigin("feature/agent-workflow-v4-design")
	expectCheck(t, r.validator().GateDesignFinalApproval(".agent/tasks/demo"), "Design Baseline")
}

func TestDesignGateFinalApprovalSpecDrift(t *testing.T) {
	r, _ := setupDesignFinalApproved(t)
	r.write(".agent/tasks/demo/design.md", "# design v2\n")
	r.commit("design spec drift")
	r.syncOrigin("feature/agent-workflow-v4-design")
	// 设计规范实质修改后，旧审查/批准失效。
	expectCheck(t, r.validator().GateDesignFinalApproval(".agent/tasks/demo"), "Review Tail")
}

// --- 设计文件属 substantive（不进入 neutral 白名单）---

func TestDesignFilesSubstantive(t *testing.T) {
	for _, p := range []string{
		".agent/tasks/demo/architecture.md",
		".agent/tasks/demo/design.md",
		"docs/design/foo.md",
		".agent/tasks/demo/contract.md",
	} {
		if isReviewNeutral("demo", p) {
			t.Fatalf("%s 不应是 review-neutral（设计文档默认 substantive）", p)
		}
		if !isReviewSubstantial("demo", p) {
			t.Fatalf("%s 应判定为 substantive", p)
		}
	}
	// 中性 metadata 仍应是 neutral（对照），确保只锁定设计文档的 default-deny。
	if !isReviewNeutral("demo", ".agent/tasks/demo/state.yaml") {
		t.Fatal("state.yaml 应是 review-neutral")
	}
}
