package workflow

import (
	"fmt"
	"strings"
	"testing"
)

// setupTaskBuilderRepo 构建 taskbuilder-init PASS 的完整前置：
//   - develop 基线（base）；
//   - Intake 记录 + owner-decision（APPROVED）+ taskbuilder-handoff（与 Intake 基线一致）；
//   - feature 分支上的 V4 state.yaml（绑定 intake.id + git 基线）+ task.md。
//
// Intake 文件在工作区未提交时先落盘，checkout feature 后随 task.md / state.yaml 一起提交到
// feature 分支，develop 始终保持 base 不动（父分支不被污染）。
func setupTaskBuilderRepo(t *testing.T) (*testRepo, string) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init")
	r.syncOrigin("feature/agent-workflow-v2")
	return r, base
}

// --- 出口 Gate：任务已完整初始化 ---

func TestGateTaskBuilderInitPass(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusPass)
}

// 未经 Discovery 与 Owner 决策不得初始化：缺失 intake 引用 → FAIL。
func TestGateTaskBuilderInitNoIntake(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	// 故意不写 s.Intake
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init without intake")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// 缺失 task.md → FAIL（任务未完整初始化）。
func TestGateTaskBuilderInitMissingTaskMD(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	// 故意不写 task.md
	r.commit("task init without task.md")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// Owner 未批准（taskbuilder-handoff.owner_decision = REJECTED）→ FAIL。
func TestGateTaskBuilderInitOwnerRejected(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionRejected)
	r.write(".agent/intake/my-intake/taskbuilder-handoff.md", fmt.Sprintf(`---
intake_id: my-intake
owner_decision: REJECTED
parent_branch: develop
base_commit: %s
task_type: NORMAL
task_id: demo
---
`, base))
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// NORMAL/COMPLEX/DESIGN 路由错误（state.task_type 与 handoff 不一致）→ FAIL。
func TestGateTaskBuilderInitRouteMismatch(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeComplex // handoff 声明 NORMAL，state 声明 COMPLEX → 不一致
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// Git 基线未绑定 Owner 确认的 Base Commit → FAIL。
func TestGateTaskBuilderInitBaselineMismatch(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: "1111111111111111111111111111111111111111", TaskBranch: "feature/agent-workflow-v2"} // 与 handoff 不一致
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// 任务分支不存在 → FAIL（分支未创建，任务未完整初始化）。
func TestGateTaskBuilderInitBranchMissing(t *testing.T) {
	r, base := setupIntakeRepo(t)
	writeOwnerDecision(t, r, OwnerDecisionApproved)
	writeTaskBuilderHandoff(t, r, base)
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.SchemaVersion = SchemaV4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/nonexistent"}
	s.Intake = &IntakeRef{ID: "my-intake"}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task: demo\n")
	r.commit("task init with wrong branch ref")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), "TaskBuilder Init")
}

// taskbuilder-init 仅适用于 V4 → 非 V4 返回 ERROR（exit 2）。
func TestGateTaskBuilderInitNonV4(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# Task\n")
	r.commit("v3 state")
	r.syncOrigin("feature/agent-workflow-v2")
	res := r.validator().GateTaskBuilderInit(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("V2/V3 任务期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

// TestTaskBuilderHandoffInvalid 覆盖 taskbuilder-handoff.md front-matter 结构校验（非法输入 → 解析错误）。
func TestTaskBuilderHandoffInvalid(t *testing.T) {
	for _, tt := range []struct {
		name string
		fm   string
		want string
	}{
		{"非法 owner_decision", "intake_id: x\nowner_decision: MAYBE\nparent_branch: develop\nbase_commit: 1111111111111111111111111111111111111111\ntask_type: NORMAL\ntask_id: demo\n", "owner_decision"},
		{"非法 task_type", "intake_id: x\nowner_decision: APPROVED\nparent_branch: develop\nbase_commit: 1111111111111111111111111111111111111111\ntask_type: PROGRAM\ntask_id: demo\n", "task_type"},
		{"非法 base_commit", "intake_id: x\nowner_decision: APPROVED\nparent_branch: develop\nbase_commit: short\ntask_type: NORMAL\ntask_id: demo\n", "base_commit"},
		{"非法 task_id", "intake_id: x\nowner_decision: APPROVED\nparent_branch: develop\nbase_commit: 1111111111111111111111111111111111111111\ntask_type: NORMAL\ntask_id: ../demo\n", "task_id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseTaskBuilderHandoff([]byte(tt.fm)); err == nil {
				t.Fatalf("期望错误含 %q，实际 nil", tt.want)
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.want, err)
			}
		})
	}
}
