package workflow

import (
	"testing"
)

// 本文件用临时 Git 仓库测试 Program 多 Task 的历史关系、版本漂移与冲突处理。

func writeE2ETaskState(r *testRepo, taskID, programID, branch, reviewSHA, featureSHA, base string) {
	yml := `schema_version: 4
task_id: ` + taskID + `
task_type: NORMAL
git:
  parent_branch: develop
  base_commit: ` + base + `
  task_branch: ` + branch + `
program:
  id: ` + programID + `
  version: 1
contract:
  status: NOT_REQUIRED
  target: ""
review:
  status: CLEAN
  target: ` + reviewSHA + `
owner:
  status: ACCEPTED
  review_target: ` + reviewSHA + `
delivery:
  status: PASS
  review_target: ` + reviewSHA + `
  feature_head: ` + featureSHA + `
  develop_base: ` + base + `
blocked:
  active: false
  by: ""
  reason: ""
`
	r.write(".agent/tasks/"+taskID+"/state.yaml", yml)
}

func deliveryFor(taskID, branch, deliverySHA string) RegisteredDelivery {
	return RegisteredDelivery{
		TaskID:       taskID,
		Branch:       branch,
		DeliverySHA:  deliverySHA,
		ReviewStatus: ReviewClean,
		ReviewTarget: deliverySHA,
		OwnerStatus:  OwnerAccepted,
		OwnerTarget:  deliverySHA,
		RegisteredBy: "CollectorAgent",
		RegisteredAt: "2026-10-16T00:00:00Z",
	}
}

// 端到端：多 Task 历史关系 + Collector 登记 + Assembler 计划 + 版本钉死 + 最终 Gate。
func TestProgramMultiTaskEndToEnd(t *testing.T) {
	r, base := newProgramGateRepo(t)

	// t1 从 develop 基线派生。
	r.checkout("feature/t1")
	r.write("internal/t1.go", "package t1\n")
	t1SHA := r.commit("t1 implementation")

	// t2 从 t1 的交付点派生（历史依赖关系：t2 历史包含 t1）。
	r.checkout("feature/t2")
	r.write("internal/t2.go", "package t2\n")
	t2SHA := r.commit("t2 implementation")

	writeE2ETaskState(r, "t1", "prog-1", "feature/t1", t1SHA, t1SHA, base)
	writeE2ETaskState(r, "t2", "prog-1", "feature/t2", t2SHA, t2SHA, base)

	git := &ExecGit{Root: r.dir}
	load := func(id string) (State, error) { return LoadTaskStateFile(r.dir, id) }
	store := &RegistryStore{Root: r.dir}
	col := &Collector{Store: store, Git: git, LoadTaskState: load}

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2", Dependencies: []string{"t1"}},
	})

	if res := col.Collect(program, deliveryFor("t1", "feature/t1", t1SHA)); res.Status != StatusPass {
		t.Fatalf("登记 t1 应 PASS，实际 %s %v %q", res.Status, res.Issues, res.Error)
	}
	if res := col.Collect(program, deliveryFor("t2", "feature/t2", t2SHA)); res.Status != StatusPass {
		t.Fatalf("登记 t2 应 PASS，实际 %s %v %q", res.Status, res.Issues, res.Error)
	}

	asm := &Assembler{Store: store, Git: git}
	plan, res := asm.Plan(program)
	if res.Status != StatusPass {
		t.Fatalf("Plan 应 PASS，实际 %s %v", res.Status, res.Issues)
	}
	if len(plan.Batches) != 2 || plan.Batches[0].Tasks[0] != "t1" || plan.Batches[1].Tasks[0] != "t2" {
		t.Fatalf("批次错误: %+v", plan.Batches)
	}
	if plan.Batches[0].SHAs["t1"] != t1SHA || plan.Batches[1].SHAs["t2"] != t2SHA {
		t.Fatalf("SHA 绑定错误: %+v", plan.Batches)
	}

	// 版本漂移：t1 分支前进新 commit，但已登记 SHA 必须保持钉死（集成用已登记版本）。
	r.git("checkout", "feature/t1")
	r.write("internal/t1.go", "package t1\n// advance\n")
	r.commit("t1 advance")
	plan2, res2 := asm.Plan(program)
	if res2.Status != StatusPass {
		t.Fatalf("分支前进后 Plan 仍应 PASS（已登记 SHA 未失效），实际 %s", res2.Status)
	}
	if plan2.Batches[0].SHAs["t1"] != t1SHA {
		t.Fatalf("集成应使用已登记版本 %s，而非当前 HEAD", t1SHA)
	}

	// 最终 Gate：先集成后审查（C0 不含报告，报告由 C1 新增），写 program-state 并核验。
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	expectStatus(t, gateValidator(r).GateFinalDelivery("prog-1"), StatusPass)
}

// 错误 delivery SHA（非真实 commit）被 Collector 拒绝（真实 Git）。
func TestCollectorRejectFakeSHARealGit(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.checkout("feature/t1")
	r.write("internal/t1.go", "package t1\n")
	t1SHA := r.commit("t1 implementation")
	writeE2ETaskState(r, "t1", "prog-1", "feature/t1", t1SHA, t1SHA, base)

	git := &ExecGit{Root: r.dir}
	load := func(id string) (State, error) { return LoadTaskStateFile(r.dir, id) }
	col := &Collector{Store: &RegistryStore{Root: r.dir}, Git: git, LoadTaskState: load}

	program := programWithTasks("prog-1", "develop", base, []ProgramTask{
		{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
	})

	// delivery_sha 用一个格式合法但不存在的 SHA。
	fake := shaLike("nonexistent")
	d := deliveryFor("t1", "feature/t1", fake)
	d.ReviewTarget = t1SHA
	d.OwnerTarget = t1SHA
	if res := col.Collect(program, d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（delivery_sha 非真实 commit），实际 %s", res.Status)
	}
}
