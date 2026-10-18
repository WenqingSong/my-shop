package workflow

import (
	"strings"
	"testing"
)

// 本文件覆盖 I4/I5 跨模块接线：Assembler（工作包 D）通过 MergerMergeOps 适配层
// 实际调用 Merger（工作包 B）的隔离集成原语，真实完成阶段合并，而非仅 Plan 后伪造 PASS。
//
// 关键断言：
//   - 阶段集成产生真实合并 commit（非空、真实 commit、内容包含 feature）；
//   - 阶段集成绝不更新正式父分支（origin/develop 保持不变）；
//   - 缺分支绑定 / 缺测试命令时拒绝，不削弱 Merger 的来源分支绑定与测试约束。

// setupProgramDeliverables 构建「develop 基线 + feature/t1 + feature/t2（并行）」场景，
// 并登记两条交付记录，返回 base、t1SHA、t2SHA。
func setupProgramDeliverables(t *testing.T) (*mRepo, *RegistryStore, string, string, string) {
	t.Helper()
	r := newMRepo(t)
	r.checkout("develop")
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.push("develop")

	r.checkout("feature/t1", base)
	r.write("internal/t1.go", "package t1\n")
	t1SHA := r.commit("t1 implementation")
	r.push("feature/t1")

	r.checkout("feature/t2", base)
	r.write("internal/t2.go", "package t2\n")
	t2SHA := r.commit("t2 implementation")
	r.push("feature/t2")
	r.switchTo("develop")

	store := &RegistryStore{Root: r.host}
	if err := store.Register("prog-1", RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: t1SHA,
		ReviewStatus: ReviewClean, ReviewTarget: t1SHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: t1SHA,
		RegisteredBy: "CollectorAgent", RegisteredAt: "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatalf("登记 t1: %v", err)
	}
	if err := store.Register("prog-1", RegisteredDelivery{
		TaskID: "t2", Branch: "feature/t2", DeliverySHA: t2SHA,
		ReviewStatus: ReviewClean, ReviewTarget: t2SHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: t2SHA,
		RegisteredBy: "CollectorAgent", RegisteredAt: "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatalf("登记 t2: %v", err)
	}

	// 写子 Task state（供跨 Task 契约校验加载准确 Evidence）。
	r.write(".agent/tasks/t1/state.yaml", taskStateYAML("t1", "prog-1", "feature/t1", t1SHA))
	r.write(".agent/tasks/t2/state.yaml", taskStateYAML("t2", "prog-1", "feature/t2", t2SHA))

	return r, store, base, t1SHA, t2SHA
}

func TestAssemblerIntegrateRealMerge(t *testing.T) {
	r, store, base, t1SHA, t2SHA := setupProgramDeliverables(t)

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2"},
	})

	mergeOps := &MergerMergeOps{
		Merger:       r.merger(),
		ParentBranch: "develop",
		BranchOf:     map[string]string{t1SHA: "feature/t1", t2SHA: "feature/t2"},
		TestCommand:  []string{"sh", "-c", "test -f internal/t1.go && test -f internal/t2.go"},
	}
	asm := &Assembler{Store: store, Git: &ExecGit{Root: r.host}, Merge: mergeOps}

	plan, res := asm.Plan(program)
	if res.Status != StatusPass {
		t.Fatalf("Plan 应 PASS，实际 %s %v %q", res.Status, res.Issues, res.Error)
	}
	if len(plan.Batches) != 1 {
		t.Fatalf("并行任务应单批，实际 %d 批", len(plan.Batches))
	}

	commits, res := asm.Integrate(plan)
	if res.Status != StatusPass {
		t.Fatalf("Integrate 应 PASS，实际 %s %v %q", res.Status, res.Issues, res.Error)
	}
	if len(commits) != 1 || commits[0] == "" {
		t.Fatalf("阶段集成应返回一个非空合并 SHA，实际 %v", commits)
	}

	// 合并结果必须是真实 commit，且包含两个 feature 的内容（不是伪造 PASS）。
	content := gitAt(t, r.host, "show", commits[0]+":internal/t1.go")
	if !strings.Contains(content, "package t1") {
		t.Fatalf("阶段合并结果缺少 t1 内容: %q", content)
	}
	content2 := gitAt(t, r.host, "show", commits[0]+":internal/t2.go")
	if !strings.Contains(content2, "package t2") {
		t.Fatalf("阶段合并结果缺少 t2 内容: %q", content2)
	}

	// 阶段集成绝不更新正式父分支（OwnerAuthorized=false）。
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("阶段集成不得更新 origin/develop，期望 %s 实际 %s", base, got)
	}
}

func TestAssemblerIntegrateMissingBranchBinding(t *testing.T) {
	r, store, base, t1SHA, _ := setupProgramDeliverables(t)

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
	})

	// 缺 t1 的分支绑定：MergerMergeOps 必须拒绝，绝不静默跳过来源分支核验。
	mergeOps := &MergerMergeOps{
		Merger:       r.merger(),
		ParentBranch: "develop",
		BranchOf:     map[string]string{}, // 缺 t1SHA -> branch
		TestCommand:  []string{"true"},
	}
	asm := &Assembler{Store: store, Git: &ExecGit{Root: r.host}, Merge: mergeOps}

	plan, res := asm.Plan(program)
	if res.Status != StatusPass {
		t.Fatalf("Plan 应 PASS，实际 %s %v", res.Status, res.Issues)
	}
	if _, res := asm.Integrate(plan); res.Status != StatusError {
		t.Fatalf("缺分支绑定时 Integrate 应 ERROR，实际 %s", res.Status)
	}
	// 直接调用 MergeSources 也应拒绝（缺分支绑定）。
	if _, err := mergeOps.MergeSources(base, []string{t1SHA}); err == nil {
		t.Fatal("缺分支绑定时 MergeSources 应返回 error")
	}
}

// 跨 Task 契约冲突（共享资源）→ Plan FAIL，且不产生可集成计划（F1）。
func TestAssemblerPlanCrossTaskConflict(t *testing.T) {
	r, store, base, t1SHA, t2SHA := setupProgramDeliverables(t)
	conflict := "resources:\n  reservations:\n    migration_version:\n      - \"20261001000011\"\n"
	r.write(".agent/tasks/t1/state.yaml", taskStateYAMLRes("t1", "prog-1", "feature/t1", t1SHA, conflict))
	r.write(".agent/tasks/t2/state.yaml", taskStateYAMLRes("t2", "prog-1", "feature/t2", t2SHA, conflict))

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2"},
	})
	asm := &Assembler{Store: store, Git: &ExecGit{Root: r.host}}
	_, res := asm.Plan(program)
	if res.Status != StatusFail {
		t.Fatalf("跨 Task 资源冲突应 FAIL，实际 %s %v", res.Status, res.Issues)
	}
}

// 跨 Task 契约冲突的任务不能实际 Integrate，且绕过 Plan 直接调用 Integrate 也会被拦截（F1）。
func TestAssemblerIntegrateRejectsCrossTaskConflict(t *testing.T) {
	r, store, base, t1SHA, t2SHA := setupProgramDeliverables(t)
	conflict := "resources:\n  reservations:\n    migration_version:\n      - \"20261001000011\"\n"
	r.write(".agent/tasks/t1/state.yaml", taskStateYAMLRes("t1", "prog-1", "feature/t1", t1SHA, conflict))
	r.write(".agent/tasks/t2/state.yaml", taskStateYAMLRes("t2", "prog-1", "feature/t2", t2SHA, conflict))

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2"},
	})
	mergeOps := &MergerMergeOps{
		Merger:       r.merger(),
		ParentBranch: "develop",
		BranchOf:     map[string]string{t1SHA: "feature/t1", t2SHA: "feature/t2"},
		TestCommand:  []string{"true"},
	}
	asm := &Assembler{Store: store, Git: &ExecGit{Root: r.host}, Merge: mergeOps}

	if _, res := asm.Plan(program); res.Status != StatusFail {
		t.Fatalf("Plan 应 FAIL（资源冲突），实际 %s", res.Status)
	}

	// 绕过 Plan，直接调用 Integrate（手工构造计划）也必须被契约校验拦截。
	handcrafted := IntegrationPlan{
		ProgramID:    "prog-1",
		ParentBranch: "develop",
		BaseCommit:   base,
		Batches: []IntegrationBatch{{
			Tasks: []string{"t1", "t2"},
			SHAs:  map[string]string{"t1": t1SHA, "t2": t2SHA},
		}},
	}
	if _, res := asm.Integrate(handcrafted); res.Status != StatusFail {
		t.Fatalf("直接调用 Integrate 应被契约校验 FAIL，实际 %s %v", res.Status, res.Issues)
	}

	// 且 origin/develop 未发生任何更新（阶段集成绝不更新父分支）。
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("冲突场景不得更新 origin/develop，期望 %s 实际 %s", base, got)
	}
}

func TestAssemblerIntegrateMissingTestCommand(t *testing.T) {
	r, store, base, t1SHA, t2SHA := setupProgramDeliverables(t)

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2"},
	})

	// 缺测试命令：阶段集成必须对合并结果运行真实测试，不得跳过。
	mergeOps := &MergerMergeOps{
		Merger:       r.merger(),
		ParentBranch: "develop",
		BranchOf:     map[string]string{t1SHA: "feature/t1", t2SHA: "feature/t2"},
		TestCommand:  nil,
	}
	asm := &Assembler{Store: store, Git: &ExecGit{Root: r.host}, Merge: mergeOps}

	plan, res := asm.Plan(program)
	if res.Status != StatusPass {
		t.Fatalf("Plan 应 PASS，实际 %s %v", res.Status, res.Issues)
	}
	if _, res := asm.Integrate(plan); res.Status != StatusError {
		t.Fatalf("缺测试命令时 Integrate 应 ERROR，实际 %s", res.Status)
	}
}
