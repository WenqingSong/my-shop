package workflow

import "testing"

// setupContractRevisionRepo 构建 V4 Contract Revision 测试所需的最小真实 Git fixture：
//   - develop 基线（仅 README，无 contract.md）→ base；
//   - feature 上依次提交 contract v1（A1，含 contract.md）与 contract v2（A2，含 contract.md）。
//
// 该 fixture 用真实 ExecGit 验证 Evidence SHA 的真实性、祖先关系与 contract.md 存在性，
// 不以 Mock PASS 代替 Git 证据验证。
func setupContractRevisionRepo(t *testing.T) (*testRepo, map[string]string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/contract.md", "# contract v1\n")
	a1 := r.commit("contract v1")
	r.write(".agent/tasks/demo/contract.md", "# contract v2\n")
	a2 := r.commit("contract v2")
	r.syncOrigin("feature/agent-workflow-v2")

	return r, map[string]string{"base": base, "A1": a1, "A2": a2}
}

func v4RevisionState(c Contract) State {
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Contract = c
	return s
}

// --- 合法版本化 Contract ---

// TestContractRevisionValidApproved 覆盖：revision=2、history=[{1,A1}]、target=A2 → PASS。
func TestContractRevisionValidApproved(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 2,
		History:  []ContractRevision{{Revision: 1, Target: shas["A1"]}},
	})
	expectStatus(t, v.contractRevisionValid("demo", s), StatusPass)
}

// TestContractRevisionValidUnversioned 覆盖：未版本化（revision==0 且 history 空）→ PASS（V2/V3 兼容）。
func TestContractRevisionValidUnversioned(t *testing.T) {
	r, _ := setupContractRevisionRepo(t)
	v := r.validator()
	for _, c := range []Contract{
		{Status: ContractNotRequired},
		{Status: ContractApproved, Target: "0000000000000000000000000000000000000000"}, // APPROVED 未版本化由 contractValid 内容校验负责，revision 校验不介入
	} {
		expectStatus(t, v.contractRevisionValid("demo", v4RevisionState(c)), StatusPass)
	}
}

// --- 非法版本关系 → FAIL（AC-04）---

func TestContractRevisionDuplicate(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 3,
		History: []ContractRevision{
			{Revision: 1, Target: shas["A1"]},
			{Revision: 1, Target: shas["A2"]},
		},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

func TestContractRevisionDescending(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 3,
		History: []ContractRevision{
			{Revision: 2, Target: shas["A2"]},
			{Revision: 1, Target: shas["A1"]},
		},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

func TestContractRevisionHistoryExceedsCurrent(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 1,
		History:  []ContractRevision{{Revision: 2, Target: shas["A2"]}},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

// TestContractRevisionReuseTarget 覆盖「伪造递增后沿用旧批准对象」：当前 target 复用历史 Evidence SHA。
func TestContractRevisionReuseTarget(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A1"], // 与 history[0].target 相同
		Revision: 2,
		History:  []ContractRevision{{Revision: 1, Target: shas["A1"]}},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

func TestContractRevisionHistoryTargetNotCommit(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 2,
		History:  []ContractRevision{{Revision: 1, Target: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

func TestContractRevisionHistoryTargetNoContract(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 2,
		// base 是 develop 基线上的真实 commit，但其中不含 contract.md。
		History: []ContractRevision{{Revision: 1, Target: shas["base"]}},
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

func TestContractRevisionNonApprovedVersioned(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	v := r.validator()
	s := v4RevisionState(Contract{
		Status:   ContractNotRequired,
		Target:   shas["A1"],
		Revision: 1,
	})
	expectCheck(t, v.contractRevisionValid("demo", s), "Contract Revision")
}

// TestContractRevisionBumpWithoutNewEvidence 覆盖 AC-05：修改 revision 字段、沿用旧 target，
// 未获得新的有效证据时旧审批不得恢复有效。经 coder-start 端到端验证（重新读盘）。
func TestContractRevisionBumpWithoutNewEvidence(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	// working tree 当前 contract.md 为 "# contract v2"（A2 提交），而 target 仍伪造指向 A1（v1）。
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: shas["base"], TaskBranch: "feature/agent-workflow-v2"}
	s.Contract = Contract{
		Status:   ContractApproved,
		Target:   shas["A1"],
		Revision: 2, // 只把 revision 改成 2，未绑定新 Evidence Commit
	}
	r.writeState(s)
	r.commit("bump revision without new evidence")
	r.syncOrigin("feature/agent-workflow-v2")

	// A1 内容仍是 v1，与工作区 v2 不一致：只改 revision 不能使旧批准恢复有效。
	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Contract")
}

// --- 端到端：合法 V4 版本化 Contract 经 Gate 通过 ---

// TestGateCoderStartV4ContractRevisionValid 覆盖合法版本化 Contract 经 coder-start 完整链路 PASS。
func TestGateCoderStartV4ContractRevisionValid(t *testing.T) {
	r, shas := setupContractRevisionRepo(t)
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: shas["base"], TaskBranch: "feature/agent-workflow-v2"}
	s.Contract = Contract{
		Status:   ContractApproved,
		Target:   shas["A2"],
		Revision: 2,
		History:  []ContractRevision{{Revision: 1, Target: shas["A1"]}},
	}
	r.writeState(s)
	r.commit("v4 contract metadata")
	r.syncOrigin("feature/agent-workflow-v2")

	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}
