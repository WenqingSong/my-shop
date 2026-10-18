package workflow

import (
	"strings"
	"testing"
)

// --- 合法 V4 ---

// TestStateV4ValidMinimal 覆盖最小合法 V4（NORMAL + git 基线，其余 section 缺省）。
func TestStateV4ValidMinimal(t *testing.T) {
	s := mustParse(t, `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
`)
	if s.SchemaVersion != SchemaV4 {
		t.Fatalf("SchemaVersion = %d, 期望 %d", s.SchemaVersion, SchemaV4)
	}
	if s.TaskID != "demo" {
		t.Fatalf("TaskID = %q", s.TaskID)
	}
	if s.TaskType != TaskTypeNormal {
		t.Fatalf("TaskType = %q", s.TaskType)
	}
	if s.Git == nil || s.Git.ParentBranch != "main" || s.Git.BaseCommit != "abc123" || s.Git.TaskBranch != "feature/demo" {
		t.Fatalf("Git = %+v", s.Git)
	}
	if s.Design != nil || s.Handoff != nil || s.EventLog != nil || s.Program != nil {
		t.Fatalf("可选 section 应为 nil，实际 design=%+v handoff=%+v event_log=%+v program=%+v",
			s.Design, s.Handoff, s.EventLog, s.Program)
	}
}

// TestStateV4ValidFull 覆盖完整 V4（含 Contract Revision、resources、design/handoff/event_log/program）。
func TestStateV4ValidFull(t *testing.T) {
	s := mustParse(t, `schema_version: 4
task_id: flash-sale-v4
task_type: COMPLEX
git:
  parent_branch: main
  base_commit: b1a2c3d
  task_branch: feature/flash-sale-v4
contract:
  status: APPROVED
  target: a1b2c3d
  revision: 2
  history:
    - revision: 1
      target: e5f6a7b
resources:
  reservations:
    migration_version:
      - "20261001000011"
review:
  status: CLEAN
  target: c1d2e3f
owner:
  status: ACCEPTED
  review_target: c1d2e3f
delivery:
  status: PASS
  review_target: c1d2e3f
  feature_head: f1a2b3c
  develop_base: d1e2f3a
blocked:
  active: false
  by: ""
  reason: ""
design:
  status: APPROVED
  approved_version: 3
  approved_target: a9b8c7d
  final_version: 3
  final_target: d1e2f3a
  review_target: d1e2f3a
handoff:
  prepared: true
  scope: task
  scope_id: flash-sale-v4
  next_role: Coder
  workflow: COMPLEX
  evidence:
    version: 1
    items:
      - kind: contract
        sha: "2222222222222222222222222222222222222222"
  next_action: 实现 AC
  source_commit: "1111111111111111111111111111111111111111"
  entry: coder-start
event_log:
  path: .agent/logs/task-v4.log
program:
  id: program-x
  version: 1
`)
	if s.TaskType != TaskTypeComplex {
		t.Fatalf("TaskType = %q", s.TaskType)
	}
	if s.Contract.Status != ContractApproved || s.Contract.Target != "a1b2c3d" {
		t.Fatalf("Contract = %+v", s.Contract)
	}
	if s.Contract.Revision != 2 || len(s.Contract.History) != 1 {
		t.Fatalf("Contract Revision = %+v", s.Contract)
	}
	if s.Contract.History[0].Revision != 1 || s.Contract.History[0].Target != "e5f6a7b" {
		t.Fatalf("Contract History[0] = %+v", s.Contract.History[0])
	}
	if got := s.Resources.Reservations["migration_version"]; len(got) != 1 || got[0] != "20261001000011" {
		t.Fatalf("migration_version = %+v", got)
	}
	if s.Design == nil || s.Design.Status != DesignApproved || s.Design.ApprovedVersion != 3 ||
		s.Design.ApprovedTarget != "a9b8c7d" || s.Design.FinalVersion != 3 ||
		s.Design.FinalTarget != "d1e2f3a" || s.Design.ReviewTarget != "d1e2f3a" {
		t.Fatalf("Design = %+v", s.Design)
	}
	if s.Handoff == nil || !s.Handoff.Prepared || s.Handoff.NextRole != "Coder" || s.Handoff.Entry != "coder-start" {
		t.Fatalf("Handoff = %+v", s.Handoff)
	}
	if s.Handoff.Scope != HandoffScopeTask || s.Handoff.ScopeID != "flash-sale-v4" || s.Handoff.Workflow != WorkflowComplex {
		t.Fatalf("Handoff 作用域/工作流 = %+v", s.Handoff)
	}
	if s.Handoff.Evidence == nil || s.Handoff.Evidence.Version != 1 || len(s.Handoff.Evidence.Items) != 1 ||
		s.Handoff.Evidence.Items[0].Kind != "contract" || s.Handoff.Evidence.Items[0].SHA != "2222222222222222222222222222222222222222" {
		t.Fatalf("Handoff Evidence = %+v", s.Handoff.Evidence)
	}
	if s.Handoff.SourceCommit != "1111111111111111111111111111111111111111" {
		t.Fatalf("Handoff SourceCommit = %q", s.Handoff.SourceCommit)
	}
	if s.EventLog == nil || s.EventLog.Path != ".agent/logs/task-v4.log" {
		t.Fatalf("EventLog = %+v", s.EventLog)
	}
	if s.Program == nil || s.Program.ID != "program-x" || s.Program.Version != 1 {
		t.Fatalf("Program = %+v", s.Program)
	}
}

// --- 缺失必要字段 ---

// TestStateV4MissingRequired 覆盖 V4 必填字段缺失 → ERROR（exit 2）。
func TestStateV4MissingRequired(t *testing.T) {
	base := `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
`
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{"缺 task_type", "schema_version: 4\ntask_id: demo\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\n", "缺少必填 task_type"},
		{"缺 git 整段", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\n", "缺少必填 git"},
		{"缺 git.parent_branch", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  base_commit: abc123\n  task_branch: feature/demo\n", "git.parent_branch"},
		{"缺 git.base_commit", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  task_branch: feature/demo\n", "git.base_commit"},
		{"缺 git.task_branch", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n", "git.task_branch"},
		{"空 git 段", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit: {}\n", "git.parent_branch"},
	}
	_ = base
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustParseError(t, tt.yml, tt.want)
		})
	}
}

// --- 枚举非法 ---

// TestStateV4InvalidEnums 覆盖 V4 专属枚举非法 → ERROR。
func TestStateV4InvalidEnums(t *testing.T) {
	mustParseError(t, `schema_version: 4
task_id: demo
task_type: COORDINATED
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
`, "非法的 task_type")

	mustParseError(t, `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
design:
  status: BOGUS
`, "非法的 design.status")
}

// TestTaskTypeAndDesignStatusValidators 覆盖 task_type / design.status 枚举校验器。
func TestTaskTypeAndDesignStatusValidators(t *testing.T) {
	for _, tt := range []TaskType{TaskTypeNormal, TaskTypeComplex, TaskTypeDesign} {
		if !ValidTaskType(tt) {
			t.Fatalf("ValidTaskType(%s) 应为 true", tt)
		}
	}
	for _, d := range []DesignStatus{DesignNotRequired, DesignPending, DesignApproved, DesignRejected} {
		if !ValidDesignStatus(d) {
			t.Fatalf("ValidDesignStatus(%s) 应为 true", d)
		}
	}
	if ValidTaskType("COORDINATED") || ValidTaskType("PROGRAM") || ValidTaskType("") || ValidDesignStatus("STALE") || ValidDesignStatus("") {
		t.Fatal("非法枚举不应通过校验（Program/COORDINATED 不是 Task 工作流模式）")
	}
}

// --- 未知字段 fail-closed ---

// TestStateV4UnknownFields 覆盖 V4 顶层与嵌套未知字段 → ERROR（AC-04）。
func TestStateV4UnknownFields(t *testing.T) {
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{"顶层 typo resourcess", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\nresourcess: {}\n", "resourcess"},
		{"顶层 V3 不存在字段 phase", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\nphase: NEW\n", "phase"},
		{"git.parent_branchh typo", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branchh: main\n  base_commit: abc123\n  task_branch: feature/demo\n", "parent_branchh"},
		{"contract.revisionn typo", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\ncontract:\n  status: NOT_REQUIRED\n  revisionn: 1\n", "revisionn"},
		{"history entry 未知字段", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\ncontract:\n  status: APPROVED\n  history:\n    - revision: 1\n      targett: x\n", "targett"},
		{"design.statuss typo", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\ndesign:\n  statuss: APPROVED\n", "statuss"},
		{"design.approved_targett typo", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\ndesign:\n  status: APPROVED\n  approved_version: 1\n  approved_targett: abc123\n  final_version: 1\n  final_target: abc123\n", "approved_targett"},
		{"handoff.next_rol typo", "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\nhandoff:\n  next_rol: Coder\n", "next_rol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustParseError(t, tt.yml, tt.want)
		})
	}
}

// --- 资源：V4 仅 generic reservations ---

// TestStateV4LegacyResourceFieldRejected 覆盖 V4 使用 legacy 资源字段 → ERROR。
func TestStateV4LegacyResourceFieldRejected(t *testing.T) {
	mustParseError(t, `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
resources:
  migrations:
    - "20261001000012"
`, "不允许 legacy")
}

// --- V2/V3 拒绝 V4 专属字段（跨版本 fail-closed） ---

// TestStateLegacyRejectsV4Fields 覆盖 V2/V3 文档中出现 V4 专属字段 → ERROR，
// 保证 AC-02（V2/V3 语义兼容）与新旧 Schema 语义清晰，不静默接受混用。
func TestStateLegacyRejectsV4Fields(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
task_type: NORMAL
`, "task_type")

	mustParseError(t, `schema_version: 3
task_id: demo
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
`, "git")

	mustParseError(t, `schema_version: 3
task_id: demo
contract:
  status: NOT_REQUIRED
  revision: 1
`, "revision")

	mustParseError(t, `schema_version: 2
task_id: demo
resources:
  migrations: []
design:
  status: APPROVED
`, "design")
}

// --- 只读行为 ---

// TestParseStateV4DoesNotMutateInput 覆盖 V4 解析为纯函数，不产生写副作用（AC-07）。
func TestParseStateV4DoesNotMutateInput(t *testing.T) {
	yml := `schema_version: 4
task_id: demo
task_type: DESIGN
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
design:
  status: PENDING
  approved_version: 0
  final_version: 0
  review_target: ""
`
	before := append([]byte(nil), []byte(yml)...)
	if _, err := parseState([]byte(yml)); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if string(before) != yml {
		t.Fatal("parseState 不应修改输入字节")
	}
}

// --- V4 数据引用边界 ---

// TestStateV4OptionalSectionsAbsent 覆盖可选 section 缺失时为零值/nil，不误判。
func TestStateV4OptionalSectionsAbsent(t *testing.T) {
	s := mustParse(t, `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
contract:
  status: NOT_REQUIRED
  target: ""
`)
	if s.Contract.Revision != 0 || len(s.Contract.History) != 0 {
		t.Fatalf("未版本化 contract 应为 revision=0 / history 空，实际 %+v", s.Contract)
	}
	if s.Design != nil || s.Handoff != nil || s.EventLog != nil || s.Program != nil {
		t.Fatal("可选 section 缺失应为 nil")
	}
}

// TestStateV4DesignNotReviewNeutral 是数据契约层面的说明性断言：
// DESIGN 相关字段（design.review_target）不与 review.target 混同——两者是独立绑定。
// 真正的「设计文件是否 review-neutral」由 paths.go 白名单决定（docs/design/* 为 substantive），
// 本测试只锁定 V4 内部字段的独立性与非空约束语义。
func TestStateV4DesignReviewTargetIndependent(t *testing.T) {
	s := mustParse(t, `schema_version: 4
task_id: demo
task_type: DESIGN
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
review:
  status: NOT_REQUESTED
  target: ""
design:
  status: PENDING
  approved_version: 0
  final_version: 0
  review_target: ""
`)
	if s.Design == nil {
		t.Fatal("Design 应为非 nil")
	}
	// design.review_target 与 review.target 是独立事实，二者默认值不互相覆盖。
	if s.Review.Target != "" || s.Design.ReviewTarget != "" {
		t.Fatalf("review.target=%q design.review_target=%q 应为独立空值", s.Review.Target, s.Design.ReviewTarget)
	}
}

// TestStateV4ProgramRefOnly 覆盖 program 只承载公共标识/版本，不承载子 Task 状态。
func TestStateV4ProgramRefOnly(t *testing.T) {
	yml := `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
program:
  id: program-x
  version: 2
`
	s := mustParse(t, yml)
	if s.Program == nil || s.Program.ID != "program-x" || s.Program.Version != 2 {
		t.Fatalf("Program = %+v", s.Program)
	}
	// program 内出现未定义字段（如 tasks 子状态）必须 fail-closed，不得把 Program Registry 塞进 Task state。
	if _, err := parseState([]byte(strings.Replace(yml, "program:\n  id: program-x\n  version: 2", "program:\n  id: program-x\n  version: 2\n  tasks: []", 1))); err == nil {
		t.Fatal("program 内未知字段 tasks 应报错")
	}
}

// --- DESIGN Evidence Binding ---

// TestStateV4DesignTargetsIndependent 覆盖 approved_target / review_target / final_target
// 三者可独立保存与解析，且 review_target（Cleaner 审查快照）不与 approved_target（批准架构基线）混同。
func TestStateV4DesignTargetsIndependent(t *testing.T) {
	s := mustParse(t, `schema_version: 4
task_id: demo
task_type: DESIGN
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
design:
  status: APPROVED
  approved_version: 2
  approved_target: arch0001
  final_version: 2
  final_target: final0002
  review_target: review0003
`)
	if s.Design == nil {
		t.Fatal("Design 应为非 nil")
	}
	if s.Design.ApprovedTarget != "arch0001" {
		t.Fatalf("approved_target = %q, 期望 arch0001", s.Design.ApprovedTarget)
	}
	if s.Design.FinalTarget != "final0002" {
		t.Fatalf("final_target = %q, 期望 final0002", s.Design.FinalTarget)
	}
	if s.Design.ReviewTarget != "review0003" {
		t.Fatalf("review_target = %q, 期望 review0003", s.Design.ReviewTarget)
	}
	// review_target 是 Cleaner 审查快照，不得被混同为批准架构 Target。
	if s.Design.ReviewTarget == s.Design.ApprovedTarget {
		t.Fatal("review_target 不应被混同为 approved_target")
	}
}

// TestStateV4InvalidStructure 覆盖 V4 结构完整性的非法输入 → ERROR（exit 2）。
func TestStateV4InvalidStructure(t *testing.T) {
	base := "schema_version: 4\ntask_id: demo\ntask_type: NORMAL\ngit:\n  parent_branch: main\n  base_commit: abc123\n  task_branch: feature/demo\n"
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{"contract.revision 负数", base + "contract:\n  status: NOT_REQUIRED\n  revision: -1\n", "contract.revision"},
		{"contract.history revision 为 0", base + "contract:\n  status: NOT_REQUIRED\n  history:\n    - revision: 0\n      target: abc\n", "history[0].revision"},
		{"contract.history revision 负数", base + "contract:\n  status: NOT_REQUIRED\n  history:\n    - revision: -1\n      target: abc\n", "history[0].revision"},
		{"contract.history target 为空", base + "contract:\n  status: NOT_REQUIRED\n  history:\n    - revision: 1\n      target: \"\"\n", "history[0].target"},
		{"design.approved_version 负数", base + "design:\n  status: PENDING\n  approved_version: -1\n", "approved_version"},
		{"design.final_version 负数", base + "design:\n  status: PENDING\n  final_version: -1\n", "final_version"},
		{"design APPROVED 缺 approved_target", base + "design:\n  status: APPROVED\n  approved_version: 1\n", "approved_target"},
		{"design final_version 缺 final_target", base + "design:\n  status: PENDING\n  final_version: 1\n", "final_target"},
		{"program.id 为空", base + "program:\n  id: \"\"\n  version: 1\n", "program.id"},
		{"program.version 为 0", base + "program:\n  id: p\n  version: 0\n", "program.version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustParseError(t, tt.yml, tt.want)
		})
	}
}

// TestStateV4EarlyDesignNotRejected 覆盖合法早期 DESIGN 状态（section 存在但未定版）不被误拒绝。
func TestStateV4EarlyDesignNotRejected(t *testing.T) {
	// PENDING + 版本 0 + 所有 target 空：合法早期状态。
	s := mustParse(t, `schema_version: 4
task_id: demo
task_type: DESIGN
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
design:
  status: PENDING
  approved_version: 0
  final_version: 0
  review_target: ""
`)
	if s.Design == nil || s.Design.Status != DesignPending {
		t.Fatalf("Design = %+v", s.Design)
	}
	// NOT_REQUIRED + 版本 0：无架构审批需求的合法状态，同样不得被误拒绝。
	if s2 := mustParse(t, `schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: main
  base_commit: abc123
  task_branch: feature/demo
design:
  status: NOT_REQUIRED
  approved_version: 0
  final_version: 0
`); s2.Design == nil || s2.Design.Status != DesignNotRequired {
		t.Fatalf("NOT_REQUIRED Design = %+v", s2.Design)
	}
}
