package workflow

import "testing"

// setupHandoffState 构建一个已 Prepared 且已 commit/push 的 V4 Handoff feature 历史。
// 返回 evidence（E1）与 handoff（HEAD）SHA。
func setupHandoffState(t *testing.T) (*testRepo, map[string]string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/contract.md", "# contract\n")
	ev := r.commit("evidence")

	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	s.Handoff = &Handoff{
		Prepared:     true,
		Scope:        HandoffScopeTask,
		ScopeID:      "demo",
		NextRole:     RoleCoder,
		Workflow:     WorkflowComplex,
		Evidence:     &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "contract", SHA: ev, Path: ".agent/tasks/demo/contract.md"}}},
		NextAction:   "实现 AC",
		SourceCommit: ev,
		Entry:        "coder-start",
	}
	r.writeState(s)
	h := r.commit("handoff")
	r.syncOrigin("feature/agent-workflow-v2")
	return r, map[string]string{"base": base, "evidence": ev, "handoff": h}
}

// --- 结构校验（parse 阶段，exit 2 语义）---

func TestHandoffV4ValidFull(t *testing.T) {
	mustParse(t, `schema_version: 4
task_id: demo
task_type: COMPLEX
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
handoff:
  prepared: true
  scope: task
  scope_id: demo
  next_role: Coder
  workflow: COMPLEX
  evidence:
    version: 1
    items:
      - kind: contract
        sha: "2222222222222222222222222222222222222222"
  next_action: 实现 AC
  source_commit: "1111111111111111111111111111111111111111"
  ack:
    version: 1
    acked_by: Coder
    target: "1111111111111111111111111111111111111111"
  entry: coder-start
`)
}

func TestHandoffV4Invalid(t *testing.T) {
	base := "schema_version: 4\ntask_id: demo\ntask_type: COMPLEX\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\n"
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{"非法 scope", base + "handoff:\n  scope: bogus\n", "handoff.scope"},
		{"缺 scope_id", base + "handoff:\n  scope: task\n  next_role: Coder\n", "scope_id"},
		{"非法 next_role", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Orcchestrator\n", "next_role"},
		{"非法 workflow", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: PROGRAM\n", "workflow"},
		{"evidence.version 为 0", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: COMPLEX\n  evidence:\n    version: 0\n    items:\n      - kind: contract\n        sha: \"2222222222222222222222222222222222222222\"\n", "evidence.version"},
		{"evidence.items 空", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: COMPLEX\n  evidence:\n    version: 1\n    items: []\n", "items 不得为空"},
		{"evidence.sha 非法", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: COMPLEX\n  evidence:\n    version: 1\n    items:\n      - kind: contract\n        sha: short\n", "sha"},
		{"source_commit 非法", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: COMPLEX\n  evidence:\n    version: 1\n    items:\n      - kind: contract\n        sha: \"2222222222222222222222222222222222222222\"\n  source_commit: short\n", "source_commit"},
		{"ack.target 与 source_commit 不一致", base + "handoff:\n  scope: task\n  scope_id: demo\n  next_role: Coder\n  workflow: COMPLEX\n  evidence:\n    version: 1\n    items:\n      - kind: contract\n        sha: \"2222222222222222222222222222222222222222\"\n  source_commit: \"1111111111111111111111111111111111111111\"\n  ack:\n    version: 1\n    acked_by: Coder\n    target: \"3333333333333333333333333333333333333333\"\n", "ACK 必须与交接目标一致"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustParseError(t, tt.yml, tt.want)
		})
	}
}

// --- 出口/入口 Gate（运行时，exit 0/1 语义）---

func TestGateHandoffPreparedPass(t *testing.T) {
	r, _ := setupHandoffState(t)
	expectStatus(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), StatusPass)
}

func TestGateHandoffEntryPass(t *testing.T) {
	r, _ := setupHandoffState(t)
	expectStatus(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), StatusPass)
}

// 出口/入口不要求下一角色尚未产生的 ACK（未收到 ACK 不阻断上一角色出口）。
func TestGateHandoffNoAckStillPasses(t *testing.T) {
	r, _ := setupHandoffState(t)
	// 读回 state，确认无 ack（fixture 本身未写 ack）。
	expectStatus(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), StatusPass)
	expectStatus(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), StatusPass)
}

// 缺 Handoff 不得通过入口。
func TestGateHandoffEntryMissing(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	r.writeState(s)
	r.commit("state without handoff")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), "Handoff")
}

// 非法 SHA 不得通过（格式合法但非真实 commit 的 source_commit）。
func TestGateHandoffEntryInvalidSHA(t *testing.T) {
	r, _ := setupHandoffState(t)
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: r.git("rev-parse", "develop"), TaskBranch: "feature/agent-workflow-v2"}
	s.Handoff = &Handoff{
		Prepared:     true,
		Scope:        HandoffScopeTask,
		ScopeID:      "demo",
		NextRole:     RoleCoder,
		Workflow:     WorkflowComplex,
		Evidence:     &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "contract", SHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}}},
		NextAction:   "实现 AC",
		SourceCommit: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Entry:        "coder-start",
	}
	r.writeState(s)
	r.commit("handoff with bogus sha")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), "Handoff Evidence")
}

// 已失效 Evidence 不得通过（evidence SHA 不在当前 feature 历史中）。
func TestGateHandoffEntryStaleEvidence(t *testing.T) {
	r, shas := setupHandoffState(t)
	// 用 develop 基线 commit 作为 evidence（在 feature 历史之外，或虽为祖先但 path 缺失）。
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: shas["base"], TaskBranch: "feature/agent-workflow-v2"}
	s.Handoff = &Handoff{
		Prepared:     true,
		Scope:        HandoffScopeTask,
		ScopeID:      "demo",
		NextRole:     RoleCoder,
		Workflow:     WorkflowComplex,
		Evidence:     &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "contract", SHA: shas["base"], Path: ".agent/tasks/demo/contract.md"}}},
		NextAction:   "实现 AC",
		SourceCommit: shas["evidence"],
		Entry:        "coder-start",
	}
	r.writeState(s)
	r.commit("handoff with stale evidence path")
	r.syncOrigin("feature/agent-workflow-v2")
	// base 是 develop 基线上真实 commit，但其中不含 contract.md → 证据文件缺失 → FAIL。
	expectCheck(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), "Handoff Evidence")
}

// 出口 Gate：工作区脏 → FAIL（不能 commit 后修改 Handoff 产生脏工作区却误报 Ready）。
func TestGateHandoffPreparedDirty(t *testing.T) {
	r, _ := setupHandoffState(t)
	// 新增未提交文件使工作区变脏（不破坏 state.yaml，保证 Gate 先走到 handoffReady 判定）。
	r.write(".agent/tasks/demo/findings.md", "# dirty edit\n")
	expectCheck(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), "Handoff Ready")
}

// 出口 Gate：未 push → FAIL。
func TestGateHandoffPreparedUnpushed(t *testing.T) {
	r, _ := setupHandoffState(t)
	r.write(".agent/tasks/demo/findings.md", "# local\n")
	r.commit("local not pushed")
	expectCheck(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), "Handoff Ready")
}

// Handoff 协议仅适用于 V4；对 V2/V3 任务返回 ERROR（exit 2），不影响 V2/V3 既有流程。
func TestGateHandoffNonV4Error(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	r.writeState(s)
	r.commit("v3 state")
	r.syncOrigin("feature/agent-workflow-v2")
	res := r.validator().GateHandoffEntry(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("V2/V3 任务期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}
