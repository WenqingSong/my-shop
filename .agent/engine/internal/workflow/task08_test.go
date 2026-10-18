package workflow

import "testing"

// 本文件覆盖 Task 08 NORMAL/COMPLEX 正式执行与审查链路的机器 Gate 行为。
//
// 重点验证：
//   - NORMAL 完整合法流程（TaskBuilder → Coder → Cleaner → OwnerGate → Deliverer）五个 Gate 顺序通过；
//   - COMPLEX 未获得批准 Contract 时不能进入 Coder；
//   - 旧版本（V2/V3）任务行为不回归（既有测试已覆盖，此处补充 V4 专属断言）。

// v4BaseState 返回一个最小 V4 NORMAL 任务 state（contract NOT_REQUIRED）。
func v4NormalState(baseSHA string) State {
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: baseSHA, TaskBranch: "feature/agent-workflow-v2"}
	return s
}

// v4ComplexState 返回一个 V4 COMPLEX 任务 state（给定 contract status/target）。
func v4ComplexState(baseSHA string, status ContractStatus, target string) State {
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: baseSHA, TaskBranch: "feature/agent-workflow-v2"}
	s.Contract = Contract{Status: status, Target: target}
	return s
}

// TestNormalFullFlow 覆盖 AC：NORMAL 完整合法流程通过。
func TestNormalFullFlow(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")

	// Step 1: TaskBuilder 初始化 NORMAL 任务（contract NOT_REQUIRED）。
	s := v4NormalState(base)
	r.writeState(s)
	r.commit("taskbuilder init")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)

	// Step 2: Coder 实现 C1 + PENDING metadata C2。
	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation")
	s.Review = Review{Status: ReviewPending, Target: c1}
	r.writeState(s)
	r.commit("review request")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateCleanerStart(".agent/tasks/demo"), StatusPass)

	// Step 3: Cleaner CLEAN。
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s.Review = Review{Status: ReviewClean, Target: c1}
	r.writeState(s)
	r.commit("clean review")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateOwnerGateStart(".agent/tasks/demo"), StatusPass)

	// Step 4: OwnerGate ACCEPT。
	r.write(".agent/tasks/demo/owner-decision.md", "# decision\n")
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: c1}
	r.writeState(s)
	r.commit("owner acceptance")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateDeliveryStart(".agent/tasks/demo"), StatusPass)

	// Step 5: Deliverer PASS。
	r.write(".agent/tasks/demo/delivery.md", "# delivery\n")
	fh := r.git("rev-parse", "HEAD")
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: c1, FeatureHead: fh, DevelopBase: base}
	r.writeState(s)
	r.commit("delivery pass")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateMergeReady(".agent/tasks/demo"), StatusPass)
}

// TestComplexNotApprovedCannotEnterCoder 覆盖 AC：COMPLEX 未获得批准 Contract 时不能进入 Coder。
func TestComplexNotApprovedCannotEnterCoder(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")

	for _, status := range []ContractStatus{ContractPending, ContractRejected, ContractNotRequired} {
		s := v4ComplexState(base, status, "")
		r.writeState(s)
		r.commit("complex state " + status)
		r.syncOrigin("feature/agent-workflow-v2")
		expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Coder Entry")
	}
}

// TestComplexApprovedCanEnterCoder 覆盖 AC：COMPLEX 获得批准 Contract 后可进入 Coder。
func TestComplexApprovedCanEnterCoder(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/contract.md", "# contract v1\n")
	a1 := r.commit("contract evidence")

	s := v4ComplexState(base, ContractApproved, a1)
	r.writeState(s)
	r.commit("contract metadata")
	r.syncOrigin("feature/agent-workflow-v2")

	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

// TestNormalWithApprovedContractRejected 覆盖 NORMAL 任务带 APPROVED Contract 的入口不一致被拒绝。
func TestNormalWithApprovedContractRejected(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/contract.md", "# contract v1\n")
	a1 := r.commit("contract evidence")

	s := v4NormalState(base)
	s.Contract = Contract{Status: ContractApproved, Target: a1}
	r.writeState(s)
	r.commit("normal with approved contract")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Coder Entry")
}

// setupCleanClean 构建 review CLEAN（无 Owner 决策）的 feature，用于验证 Owner 接受流程的前置。
func setupCleanClean(t *testing.T) *testRepo {
	r, shas := setupCleanerStart(t)
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	r.writeState(s)
	r.commit("clean review")
	r.syncOrigin("feature/agent-workflow-v2")
	return r
}

// TestCleanerNotCleanCannotEnterOwnerGate 覆盖 AC：Cleaner 未 CLEAN 不能进入 Owner 接受流程。
func TestCleanerNotCleanCannotEnterOwnerGate(t *testing.T) {
	// setupCleanerStart 的 review.status == PENDING，owner-gate-start 必须 FAIL。
	r, _ := setupCleanerStart(t)
	expectCheck(t, r.validator().GateOwnerGateStart(".agent/tasks/demo"), "CLEAN Validity")
}

// TestOwnerNotAcceptedCannotDeliver 覆盖 AC：Owner 未 ACCEPT 不允许最终交付。
func TestOwnerNotAcceptedCannotDeliver(t *testing.T) {
	r := setupCleanClean(t) // review CLEAN，但 owner 仍 PENDING
	expectCheck(t, r.validator().GateDeliveryStart(".agent/tasks/demo"), "Delivery Start")
}

// TestComplexContractRevisionInvalidated 覆盖 AC：Contract 修改导致旧批准失效（COMPLEX 语境端到端）。
func TestComplexContractRevisionInvalidated(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	// working tree 当前 contract.md 为 "# contract v2"（A2），但伪造 target 仍指向 A1。
	s := v4ComplexState(shas["base"], ContractApproved, shas["A1"])
	s.Contract.Revision = 2 // 只改 revision，未绑定新 Evidence Commit
	r.writeState(s)
	r.commit("bump revision without new evidence")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Contract")
}
