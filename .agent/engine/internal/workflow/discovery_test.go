package workflow

import (
	"fmt"
	"testing"
)

// setupIntakeRepo 构建一个含 develop 基线 + 未提交 Intake 记录的临时仓库。
// Intake 记录以工作区文件形式存在（模拟受控 Intake 工作区），不污染父分支。
func setupIntakeRepo(t *testing.T) (*testRepo, string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")
	r.write(".agent/intake/my-intake/intake.yaml", fmt.Sprintf("intake_id: my-intake\nparent_branch: develop\nbase_commit: %s\n", base))
	return r, base
}

func writeOwnerDecision(t *testing.T, r *testRepo, decision string) {
	r.write(".agent/intake/my-intake/owner-decision.md", fmt.Sprintf("---\ndecision: %s\nintake_id: my-intake\ntask_id: demo\ntask_type: NORMAL\nparent_branch: develop\nbase_commit: %s\n---\n# Owner Decision\n\n%s\n", decision, r.git("rev-parse", "develop"), decision))
}

func writeTaskBuilderHandoff(t *testing.T, r *testRepo, base string) {
	r.write(".agent/intake/my-intake/taskbuilder-handoff.md", fmt.Sprintf(`---
intake_id: my-intake
owner_decision: APPROVED
parent_branch: develop
base_commit: %s
task_type: NORMAL
task_id: demo
---
# TaskBuilder Handoff
`, base))
}

// --- discovery-start（第一阶段入口）---

func TestGateDiscoveryStartPass(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	expectStatus(t, r.validator().GateDiscoveryStart(".agent/intake/my-intake"), StatusPass)
}

func TestGateDiscoveryStartMissingIntake(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	res := r.validator().GateDiscoveryStart(".agent/intake/nope")
	if res.Status != StatusError {
		t.Fatalf("缺 intake.yaml 期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

func TestGateDiscoveryStartBadBaseCommit(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.write(".agent/intake/my-intake/intake.yaml", "intake_id: my-intake\nparent_branch: develop\nbase_commit: deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n")
	expectCheck(t, r.validator().GateDiscoveryStart(".agent/intake/my-intake"), "Intake Baseline")
}

func TestGateDiscoveryStartBadParentBranch(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	r.write(".agent/intake/my-intake/intake.yaml", "intake_id: my-intake\nparent_branch: nonexistent\nbase_commit: 1111111111111111111111111111111111111111\n")
	expectCheck(t, r.validator().GateDiscoveryStart(".agent/intake/my-intake"), "Intake Baseline")
}

// --- discovery-decision（第二阶段入口，无 Owner Decision 时 FAIL）---

func TestGateDiscoveryDecisionNoOwnerDecision(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	expectCheck(t, r.validator().GateDiscoveryDecision(".agent/intake/my-intake"), "Discovery Decision")
}

func TestGateDiscoveryDecisionRejected(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionRejected)
	expectCheck(t, r.validator().GateDiscoveryDecision(".agent/intake/my-intake"), "Discovery Decision")
}

func TestGateDiscoveryDecisionApproved(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	expectStatus(t, r.validator().GateDiscoveryDecision(".agent/intake/my-intake"), StatusPass)
}

// --- discovery-handoff（第二阶段出口）---

func TestGateDiscoveryHandoffMissingHandoff(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	expectCheck(t, r.validator().GateDiscoveryHandoff(".agent/intake/my-intake"), "Discovery Handoff")
}

func TestGateDiscoveryHandoffBaselineMismatch(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	r.write(".agent/intake/my-intake/taskbuilder-handoff.md", `---
intake_id: my-intake
owner_decision: APPROVED
parent_branch: develop
base_commit: 1111111111111111111111111111111111111111
task_type: NORMAL
task_id: demo
---
`)
	expectCheck(t, r.validator().GateDiscoveryHandoff(".agent/intake/my-intake"), "Discovery Handoff")
}

func TestGateDiscoveryHandoffPass(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	expectStatus(t, r.validator().GateDiscoveryHandoff(".agent/intake/my-intake"), StatusPass)
}
