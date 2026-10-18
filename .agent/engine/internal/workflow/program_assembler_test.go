package workflow

import (
	"testing"
)

// --- Assembler 集成计划与跨模块契约（Task 13）---

func assemblerFor(t *testing.T, git Git) *Assembler {
	return &Assembler{
		Store: &RegistryStore{Root: t.TempDir()},
		Git:   git,
		LoadTaskState: func(id string) (State, error) {
			// 返回与 validDelivery(id).DeliverySHA 绑定的准确 Evidence。
			return State{
				SchemaVersion: SchemaV4,
				TaskID:        id,
				TaskType:      TaskTypeNormal,
				Contract:      Contract{Status: ContractNotRequired},
				Resources:     Resources{Reservations: map[string][]string{}},
				Delivery:      Delivery{Status: DeliveryPass, FeatureHead: shaLike("delivery:" + id)},
			}, nil
		},
	}
}

func programTwoTasks() ProgramState {
	return ProgramState{
		SchemaVersion: ProgramSchemaV1,
		ProgramID:     "prog-1",
		ParentBranch:  "develop",
		BaseCommit:    testSHA(1),
		Tasks: []ProgramTask{
			{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
			{TaskID: "t2", TaskType: TaskTypeNormal, Branch: "feature/t2", Dependencies: []string{"t1"}},
		},
	}
}

func registerBoth(t *testing.T, a *Assembler) {
	t.Helper()
	if err := a.Store.Register("prog-1", validDelivery("t1")); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Register("prog-1", validDelivery("t2")); err != nil {
		t.Fatal(err)
	}
}

func TestAssemblerPlanMissingTask(t *testing.T) {
	a := assemblerFor(t, nil)
	if err := a.Store.Register("prog-1", validDelivery("t1")); err != nil {
		t.Fatal(err)
	}
	_, res := a.Plan(programTwoTasks())
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL（缺 t2），实际 %s", res.Status)
	}
}

func TestAssemblerPlanValidBatches(t *testing.T) {
	a := assemblerFor(t, nil)
	registerBoth(t, a)
	plan, res := a.Plan(programTwoTasks())
	if res.Status != StatusPass {
		t.Fatalf("期望 PASS，实际 %s issues=%v", res.Status, res.Issues)
	}
	if len(plan.Batches) != 2 {
		t.Fatalf("batches=%d 期望 2", len(plan.Batches))
	}
	// 第二批绑定已登记 delivery_sha。
	if plan.Batches[0].SHAs["t1"] != validDelivery("t1").DeliverySHA {
		t.Fatalf("t1 SHA 未绑定已登记版本: %v", plan.Batches[0].SHAs)
	}
	if plan.Batches[1].SHAs["t2"] != validDelivery("t2").DeliverySHA {
		t.Fatalf("t2 SHA 未绑定已登记版本: %v", plan.Batches[1].SHAs)
	}
}

func TestAssemblerPlanCycle(t *testing.T) {
	a := assemblerFor(t, nil)
	registerBoth(t, a)
	program := programTwoTasks()
	program.Tasks[0].Dependencies = []string{"t2"} // t1→t2, t2→t1 成环
	_, res := a.Plan(program)
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL（环），实际 %s", res.Status)
	}
}

func TestAssemblerPlanStaleSHA(t *testing.T) {
	g := newFakeGit()
	g.commits[validDelivery("t1").DeliverySHA] = false
	a := assemblerFor(t, g)
	registerBoth(t, a)
	_, res := a.Plan(programTwoTasks())
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL（SHA 失效），实际 %s", res.Status)
	}
}

// 跨模块契约：共享资源冲突阻塞整体集成。
func TestCheckCrossTaskContractsResourceCollision(t *testing.T) {
	tasks := []State{
		{TaskID: "t1", TaskType: TaskTypeNormal, Resources: Resources{Reservations: map[string][]string{
			"migration_version": {"20261001000011"},
		}}},
		{TaskID: "t2", TaskType: TaskTypeNormal, Resources: Resources{Reservations: map[string][]string{
			"migration_version": {"20261001000011"},
		}}},
	}
	issues := CheckCrossTaskContracts(tasks)
	if len(issues) == 0 {
		t.Fatal("期望检测到共享资源冲突")
	}
}

// 跨模块契约：COMPLEX 缺少 APPROVED Contract 阻塞集成。
func TestCheckCrossTaskContractsComplexNoContract(t *testing.T) {
	tasks := []State{
		{TaskID: "t1", TaskType: TaskTypeComplex, Contract: Contract{Status: ContractPending}},
	}
	issues := CheckCrossTaskContracts(tasks)
	if len(issues) == 0 {
		t.Fatal("期望检测到 COMPLEX 缺 Contract")
	}
}

// 跨模块契约：无冲突时通过。
func TestCheckCrossTaskContractsClean(t *testing.T) {
	tasks := []State{
		{TaskID: "t1", TaskType: TaskTypeNormal, Resources: Resources{Reservations: map[string][]string{
			"migration_version": {"20261001000011"},
		}}},
		{TaskID: "t2", TaskType: TaskTypeNormal, Resources: Resources{Reservations: map[string][]string{
			"migration_version": {"20261001000012"},
		}}},
	}
	if issues := CheckCrossTaskContracts(tasks); len(issues) != 0 {
		t.Fatalf("期望无冲突，实际 %v", issues)
	}
}
