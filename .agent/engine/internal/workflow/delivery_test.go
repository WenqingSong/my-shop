package workflow

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本文件覆盖 Finding Fix B：最终交付受控入口（DeliveryController）的正负向行为。
// 全部使用真实临时仓库 + 真实 bare origin（mRepo，见 merger_test.go），
// 验证隔离合并、真实测试、fast-forward-only 更新、父分支漂移拒绝、授权边界与只读 plan。

// deliveryController 构造一个指向 mRepo 宿主仓库的交付控制器。
func (r *mRepo) deliveryController(testCmd ...string) *DeliveryController {
	tc := []string{"true"}
	if len(testCmd) > 0 {
		tc = testCmd
	}
	cfg, _ := LoadConfig(r.host) // Program 交付不使用 Config；DESIGN fixture 会写 workflow.yaml。
	return &DeliveryController{
		Root:        r.host,
		Git:         &ExecGit{Root: r.host},
		Merger:      r.merger(),
		Config:      cfg,
		TestCommand: tc,
	}
}

func writeMState(t *testing.T, r *mRepo, s State) {
	t.Helper()
	b, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	r.write(".agent/tasks/demo/state.yaml", string(b))
}

// --- Program 交付 fixture ---

func programStateFor(base, final, evidenceCommit string) ProgramState {
	return ProgramState{
		SchemaVersion:    ProgramSchemaV1,
		ProgramID:        "prog-1",
		ParentBranch:     "develop",
		BaseCommit:       base,
		Tasks:            []ProgramTask{{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"}},
		FinalIntegration: ProgramFinalIntegration{Status: "PASS", SHA: final, Evidence: "final-integration.md"},
		Review:           ProgramReview{Status: ReviewClean, Target: final, Evidence: ProgramCleanerEvidencePath("prog-1"), EvidenceCommit: evidenceCommit},
		Owner:            ProgramOwner{Status: OwnerAccepted, Target: final},
	}
}

func writeMProgramState(t *testing.T, r *mRepo, ps ProgramState) {
	t.Helper()
	b, err := yaml.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal program state: %v", err)
	}
	r.write(".agent/programs/prog-1/program-state.yaml", string(b))
}

func registerProgramDelivery(t *testing.T, r *mRepo, final string) {
	t.Helper()
	d := RegisteredDelivery{
		TaskID:       "t1",
		Branch:       "feature/t1",
		DeliverySHA:  final,
		ReviewStatus: ReviewClean,
		ReviewTarget: final,
		OwnerStatus:  OwnerAccepted,
		OwnerTarget:  final,
		RegisteredBy: "CollectorAgent",
		RegisteredAt: "2026-10-16T00:00:00Z",
	}
	if err := (&RegistryStore{Root: r.host}).Register("prog-1", d); err != nil {
		t.Fatalf("register: %v", err)
	}
}

// writeMProgramTaskState 写入子 Task t1 的独立 state.yaml，供 F-A 强化后的 Final Gate 跨 Task
// 契约校验（checkCrossTaskContracts → validateCrossTaskContracts）加载已登记 delivery_sha 的准确 Evidence。
func writeMProgramTaskState(t *testing.T, r *mRepo, taskID, programID, branch, base, final string) {
	t.Helper()
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
  target: ` + final + `
owner:
  status: ACCEPTED
  review_target: ` + final + `
delivery:
  status: PASS
  review_target: ` + final + `
  feature_head: ` + final + `
  develop_base: ` + base + `
blocked:
  active: false
  by: ""
  reason: ""
`
	r.write(".agent/tasks/"+taskID+"/state.yaml", yml)
}

// setupProgramDelivery 构建可交付 Program：develop 基线 + feature/t1 实现（C0）+ 独立证据 commit（C1）。
// C0（final）不含审查报告；报告由独立证据分支上的 C1 新增，最终集成源 SHA 保持 C0 不变。
func setupProgramDelivery(t *testing.T) (*mRepo, string, string, string) {
	r := newMRepo(t)
	r.checkout("develop")
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.push("develop")

	r.checkout("feature/t1")
	r.write("internal/t1.go", "package t1\n")
	final := r.commit("t1 implementation") // C0：最终集成，不含审查报告
	r.push("feature/t1")

	// 最终集成分支：Assembler 发布最终集成结果的分支，tip == final == C0。
	r.checkout("integration/prog-1")
	r.push("integration/prog-1")

	// 独立证据分支：审查报告提交为 C1，保持 origin/integration/prog-1 == C0 不变。
	r.checkout("review/prog-1")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", final))
	evidenceCommit := r.commit("program cleaner review evidence")
	r.push("review/prog-1")

	writeMProgramState(t, r, programStateFor(base, final, evidenceCommit))
	registerProgramDelivery(t, r, final)
	writeMProgramTaskState(t, r, "t1", "prog-1", "feature/t1", base, final)
	return r, base, final, evidenceCommit
}

// --- DESIGN 交付 fixture ---

func designDeliveryState(base, branch, a1, d1 string) State {
	return State{
		SchemaVersion: SchemaV4,
		TaskID:        "demo",
		TaskType:      TaskTypeDesign,
		Git:           &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: branch},
		Contract:      Contract{Status: ContractNotRequired},
		Resources:     Resources{Reservations: map[string][]string{}},
		Review:        Review{Status: ReviewClean, Target: d1},
		Owner:         Owner{Status: OwnerAccepted, ReviewTarget: d1},
		Delivery:      Delivery{Status: DeliveryNotRun},
		Blocked:       Blocked{},
		Design: &Design{
			Status:          DesignApproved,
			ApprovedVersion: 1,
			ApprovedTarget:  a1,
			FinalVersion:    1,
			FinalTarget:     d1,
			ReviewTarget:    d1,
		},
	}
}

// setupDesignDelivery 构建可交付 DESIGN 任务（纯设计，无 Coder 业务实现）。
func setupDesignDelivery(t *testing.T) (*mRepo, string, string) {
	r := newMRepo(t)
	r.checkout("develop")
	r.write("README.md", "# base\n")
	r.write(".agent/workflow.yaml", defaultWorkflowConfig)
	base := r.commit("baseline")
	r.push("develop")

	r.checkout("feature/design-v1")
	r.write(".agent/tasks/demo/architecture.md", "# architecture v1\n")
	a1 := r.commit("architecture")
	r.write(".agent/tasks/demo/design.md", "# design v1\n")
	d1 := r.commit("design delivery")

	// 最终批准后仅允许 review-neutral 文件；state.yaml 记录最终批准。
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	r.write(".agent/tasks/demo/owner-decision.md", "# decision\n")
	writeMState(t, r, designDeliveryState(base, "feature/design-v1", a1, d1))
	r.commit("final approval")
	r.push("feature/design-v1")
	return r, base, d1
}

// --- Program 正向 ---

func TestDeliveryProgramHappy(t *testing.T) {
	r, base, final, _ := setupProgramDelivery(t)
	dc := r.deliveryController("sh", "-c", "test -f internal/t1.go")

	plan, res := dc.PlanProgram("prog-1", "integration/prog-1")
	if res.Status != StatusPass {
		t.Fatalf("plan 应 PASS，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
	if plan.ParentBranch != "develop" || plan.ParentBase != base || plan.Source.SHA != final || plan.FinalEvidenceSHA != final {
		t.Fatalf("计划字段错误: %+v", plan)
	}
	// plan 严格只读：父分支不变。
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("plan 不应推送父分支，origin/develop=%s", got)
	}

	rep, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: final, ParentBranch: "develop"})
	if res.Status != StatusPass {
		t.Fatalf("execute 应 PASS，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
	if !rep.Merged || !rep.TestPassed || !rep.ParentUpdated {
		t.Fatalf("期望 Merged/TestPassed/ParentUpdated 全为 true，实际 %+v", rep)
	}
	if got := r.remoteSHA("develop"); got != rep.MergeCommit {
		t.Fatalf("复核远端父分支 HEAD：期望 %s，实际 %s", rep.MergeCommit, got)
	}
	if !strings.Contains(gitAt(t, r.host, "show", r.remoteSHA("develop")+":internal/t1.go"), "package t1") {
		t.Fatal("父分支未包含 feature 内容")
	}
}

// --- DESIGN 正向（无 Coder 业务实现） ---

func TestDeliveryDesignHappy(t *testing.T) {
	r, base, d1 := setupDesignDelivery(t)
	dc := r.deliveryController("sh", "-c", "test -f .agent/tasks/demo/design.md")

	plan, res := dc.PlanDesign("demo")
	if res.Status != StatusPass {
		t.Fatalf("plan 应 PASS，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
	if plan.ParentBranch != "develop" || plan.ParentBase != base || plan.FinalEvidenceSHA != d1 {
		t.Fatalf("计划字段错误: %+v", plan)
	}
	if plan.Source.Branch != "feature/design-v1" {
		t.Fatalf("来源分支错误: %+v", plan.Source)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("plan 不应推送父分支，origin/develop=%s", got)
	}

	rep, res := dc.ExecuteDesign("demo", DeliveryAuthorization{SourceSHA: d1, ParentBranch: "develop"})
	if res.Status != StatusPass {
		t.Fatalf("execute 应 PASS，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
	if !rep.ParentUpdated {
		t.Fatalf("期望 ParentUpdated，实际 %+v", rep)
	}
	if got := r.remoteSHA("develop"); got != rep.MergeCommit {
		t.Fatalf("复核远端父分支 HEAD：期望 %s，实际 %s", rep.MergeCommit, got)
	}
}

// --- 未经明确 Owner 授权不能更新父分支 ---

func TestDeliveryProgramUnauthorizedRefused(t *testing.T) {
	r, base, _, _ := setupProgramDelivery(t)
	dc := r.deliveryController("true")

	// 授权 SHA 与最终 Evidence SHA 不一致 → 拒绝。
	rep, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: shaLike("wrong"), ParentBranch: "develop"})
	if res.Status != StatusFail || !strings.Contains(res.Error+issueText(res), "Authorization") {
		t.Fatalf("期望 FAIL 且含 Authorization，实际 %s error=%q issues=%v", res.Status, res.Error, res.Issues)
	}
	if rep.ParentUpdated {
		t.Fatal("授权不一致时不得更新父分支")
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("未授权时 origin/develop 不应变化，实际 %s", got)
	}
}

func TestDeliveryProgramWrongParentBranchRefused(t *testing.T) {
	r, base, final, _ := setupProgramDelivery(t)
	dc := r.deliveryController("true")

	rep, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: final, ParentBranch: "release"})
	if res.Status != StatusFail || !strings.Contains(issueText(res), "Authorization") {
		t.Fatalf("期望 FAIL 且含 Authorization，实际 %s error=%q issues=%v", res.Status, res.Error, res.Issues)
	}
	if rep.ParentUpdated {
		t.Fatal("父分支授权不一致时不得更新父分支")
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("origin/develop 不应变化，实际 %s", got)
	}
}

// --- Final Gate FAIL 不能更新父分支 ---

func TestDeliveryProgramFinalGateFail(t *testing.T) {
	r, base, final, ev := setupProgramDelivery(t)
	ps := programStateFor(base, final, ev)
	ps.FinalIntegration.Status = "FAIL"
	writeMProgramState(t, r, ps)

	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "integration/prog-1"); res.Status != StatusFail {
		t.Fatalf("期望 plan FAIL，实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("Final Gate FAIL 时父分支不应变化，实际 %s", got)
	}
}

func TestDeliveryDesignFinalGateFail(t *testing.T) {
	r, base, d1 := setupDesignDelivery(t)
	// 撤销 Owner ACCEPT → GateDesignFinalApproval FAIL。
	s := designDeliveryState(base, "feature/design-v1", gitAt(t, r.host, "rev-parse", "HEAD~2"), d1)
	s.Owner.Status = OwnerPending
	writeMState(t, r, s)
	r.commit("owner not accepted")
	r.push("feature/design-v1")

	dc := r.deliveryController("true")
	if _, res := dc.PlanDesign("demo"); res.Status != StatusFail {
		t.Fatalf("期望 plan FAIL，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("Final Gate FAIL 时父分支不应变化，实际 %s", got)
	}
}

// --- 缺 Cleaner 证据不能更新父分支 ---

func TestDeliveryProgramMissingCleanerEvidence(t *testing.T) {
	r, base, final, ev := setupProgramDelivery(t)
	ps := programStateFor(base, final, ev)
	ps.Review.Target = shaLike("other") // Cleaner 证据未绑定最终 SHA
	writeMProgramState(t, r, ps)

	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "integration/prog-1"); res.Status != StatusFail {
		t.Fatalf("期望 plan FAIL，实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("缺 Cleaner 证据时父分支不应变化，实际 %s", got)
	}
}

// --- Owner ACCEPT 对象与最终 SHA 不一致拒绝 ---

func TestDeliveryProgramOwnerTargetMismatch(t *testing.T) {
	r, base, final, ev := setupProgramDelivery(t)
	ps := programStateFor(base, final, ev)
	ps.Owner.Target = shaLike("other") // Owner ACCEPT 未绑定最终 SHA
	writeMProgramState(t, r, ps)

	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "integration/prog-1"); res.Status != StatusFail {
		t.Fatalf("期望 plan FAIL，实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("Owner ACCEPT 不一致时父分支不应变化，实际 %s", got)
	}
}

// --- 父分支漂移拒绝 ---

func TestDeliveryParentDriftRefused(t *testing.T) {
	r, _, final, _ := setupProgramDelivery(t)
	// 父分支 fast-forward 前进：base 仍是祖先，但 Merger 要求精确基线 → 拒绝。
	r.switchTo("develop")
	r.write("README.md", "# base\n// advanced\n")
	advanced := r.commit("develop advance")
	r.push("develop")

	dc := r.deliveryController("true")
	rep, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: final, ParentBranch: "develop"})
	if res.Status != StatusFail {
		t.Fatalf("父分支漂移应 FAIL，实际 %s error=%q", res.Status, res.Error)
	}
	if rep.ParentUpdated {
		t.Fatal("父分支漂移时不得更新父分支")
	}
	if got := r.remoteSHA("develop"); got != advanced {
		t.Fatalf("漂移拒绝后 origin/develop 应保持 %s，实际 %s", advanced, got)
	}
}

// --- 测试失败父分支不变 ---

func TestDeliveryTestFailureParentUnchanged(t *testing.T) {
	r, base, final, _ := setupProgramDelivery(t)
	dc := r.deliveryController("sh", "-c", "exit 1")

	rep, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: final, ParentBranch: "develop"})
	if res.Status != StatusFail {
		t.Fatalf("测试失败应 FAIL，实际 %s error=%q", res.Status, res.Error)
	}
	if rep.ParentUpdated {
		t.Fatal("测试失败不得更新父分支")
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("测试失败时 origin/develop 不应变化，实际 %s", got)
	}
}

// --- 非法来源分支或 SHA 拒绝 ---

func TestDeliveryProgramInvalidSourceBranch(t *testing.T) {
	r, base, _, _ := setupProgramDelivery(t)
	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "nonexistent-branch"); res.Status != StatusFail {
		t.Fatalf("非法来源分支应 FAIL，实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("非法来源分支时父分支不应变化，实际 %s", got)
	}
}

func TestDeliveryProgramInvalidSourceSHA(t *testing.T) {
	r, base, final, ev := setupProgramDelivery(t)
	ps := programStateFor(base, final, ev)
	fake := shaLike("nonexistent")
	ps.FinalIntegration.SHA = fake
	ps.Review.Target = fake
	ps.Owner.Target = fake
	writeMProgramState(t, r, ps)

	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "integration/prog-1"); res.Status != StatusFail {
		t.Fatalf("非法来源 SHA 应 FAIL，实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("非法来源 SHA 时父分支不应变化，实际 %s", got)
	}
}

// --- 不使用 force push（经 Merger 原语，非快进推送必须被拒绝） ---

func TestDeliveryNoForcePush(t *testing.T) {
	r, base, final, _ := setupProgramDelivery(t)
	dc := r.deliveryController("true")
	if _, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{SourceSHA: final, ParentBranch: "develop"}); res.Status != StatusPass {
		t.Fatalf("首次交付应 PASS，实际 %s", res.Status)
	}
	// 尝试把旧基线 base 推回 develop（非快进）→ 必须拒绝，禁止 force push。
	if err := dc.Merger.Git.PushFastForward("origin", base, "develop"); err == nil {
		t.Fatal("非快进推送应被拒绝（禁止 force push）")
	}
	if got := r.remoteSHA("develop"); got != final {
		t.Fatalf("非快进推送失败后 origin/develop 应保持 %s，实际 %s", final, got)
	}
}

// --- plan 严格只读 / 无自动调度、无自动审批 ---

func TestDeliveryPlanReadOnlyNoAutoApproval(t *testing.T) {
	r, base, _, _ := setupProgramDelivery(t)
	dc := r.deliveryController("true")
	if _, res := dc.PlanProgram("prog-1", "integration/prog-1"); res.Status != StatusPass {
		t.Fatalf("plan 应 PASS，实际 %s", res.Status)
	}
	// plan 绝不合并/推送；execute 需要显式授权，不接受空授权。
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("plan 只读，origin/develop 应保持 %s，实际 %s", base, got)
	}
	if _, res := dc.ExecuteProgram("prog-1", "integration/prog-1", DeliveryAuthorization{}); res.Status != StatusFail {
		t.Fatalf("空授权应 FAIL（无自动审批），实际 %s", res.Status)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("空授权时 origin/develop 不应变化，实际 %s", got)
	}
}

func issueText(res Result) string {
	var b strings.Builder
	for _, iss := range res.Issues {
		b.WriteString(" ")
		b.WriteString(iss.String())
	}
	return b.String()
}
