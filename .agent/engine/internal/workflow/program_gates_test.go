package workflow

import (
	"testing"
)

// --- Program 最终交付 Gate（Task 14）与 Assembler 前置 Gate ---

func newProgramGateRepo(t *testing.T) (*testRepo, string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.syncOrigin("develop")
	return r, base
}

// cleanerReportYAML 返回一份合法的 Program Cleaner 审查报告内容（绑定 target=C0）。
func cleanerReportYAML(programID, target string) string {
	return "program_id: " + programID + "\nkind: program-cleaner-review\ntarget: " + target + "\nstatus: CLEAN\n"
}

// finalizeProgramReviewed 在临时仓库产出真实证据时序（F5）：
//   - C0：最终集成 commit（含 final-integration.md，不含审查报告）；
//   - C1：C0 之后的审查证据 commit（新增 .agent/programs/prog-1/cleaner-review.yaml 报告）。
//
// 返回 (c0, c1)。C0 不预先包含审查报告，报告由 C1 新增，杜绝时序倒置。
func finalizeProgramReviewed(t *testing.T, r *testRepo) (string, string) {
	t.Helper()
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", c0))
	c1 := r.commit("program cleaner review evidence")
	return c0, c1
}

func gateValidator(r *testRepo) *ProgramValidator {
	return &ProgramValidator{
		Root:          r.dir,
		Git:           &ExecGit{Root: r.dir},
		Store:         &RegistryStore{Root: r.dir},
		LoadTaskState: func(id string) (State, error) { return LoadTaskStateFile(r.dir, id) },
	}
}

func writeProgramState(r *testRepo, base, body string) {
	full := "schema_version: 1\nprogram_id: prog-1\nparent_branch: develop\nbase_commit: " + base + "\n" + body
	r.write(".agent/programs/prog-1/program-state.yaml", full)
}

// registerTask 写子 Task state 并登记一条交付；deliverySHA 需为真实 commit 时传 base。
func registerTask(t *testing.T, r *testRepo, id, deliverySHA string) {
	t.Helper()
	r.write(".agent/tasks/"+id+"/state.yaml", taskStateYAML(id, "prog-1", "feature/"+id, deliverySHA))
	registerOnly(t, r, id, deliverySHA)
}

// registerOnly 仅登记交付记录，不写子 Task state（供已手工写 state 的测试使用）。
func registerOnly(t *testing.T, r *testRepo, id, deliverySHA string) {
	t.Helper()
	d := validDelivery(id)
	d.DeliverySHA = deliverySHA
	if err := (&RegistryStore{Root: r.dir}).Register("prog-1", d); err != nil {
		t.Fatal(err)
	}
}

// passingBody 返回一个「全部通过」的 Program State（单子 Task t1）。
// c0 = final_integration.sha == review.target；c1 = review.evidence_commit。
func passingBody(c0, c1 string) string {
	return "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n  status: CLEAN\n  target: " + c0 + "\n  evidence: " + ProgramCleanerEvidencePath("prog-1") + "\n  evidence_commit: " + c1 + "\n" +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
}

// twoTaskBody 返回「全部通过」的双子 Task Program State（t1 + t2）。
func twoTaskBody(c0, c1 string) string {
	return "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"  - task_id: t2\n    task_type: NORMAL\n    branch: feature/t2\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n  status: CLEAN\n  target: " + c0 + "\n  evidence: " + ProgramCleanerEvidencePath("prog-1") + "\n  evidence_commit: " + c1 + "\n" +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
}

// cleanerReviewOnly 返回「仅 review 段」的 YAML 片段，供负向测试自定义 review 字段。
func cleanerReviewOnly(status, target, evidence, commit string) string {
	b := "  status: " + status
	if target != "" {
		b += "\n  target: " + target
	}
	if evidence != "" {
		b += "\n  evidence: " + evidence
	}
	if commit != "" {
		b += "\n  evidence_commit: " + commit
	}
	return b + "\n"
}

func TestGateFinalDeliveryPass(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	registerTask(t, r, "t1", base)
	expectStatus(t, gateValidator(r).GateFinalDelivery("prog-1"), StatusPass)
}

func TestGateFinalDeliveryMissingTask(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	// 规划了 t1、t2，但只登记 t1。
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"  - task_id: t2\n    task_type: NORMAL\n    branch: feature/t2\n    dependencies: [t1]\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	registerTask(t, r, "t1", base)
	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "缺必需子 Task")
}

func TestGateFinalDeliveryMissingEvidence(t *testing.T) {
	r, base := newProgramGateRepo(t)
	sha := shaLike("final")
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: NOT_RUN\n" +
		"review:\n  status: CLEAN\n  target: " + sha + "\n  evidence: " + ProgramCleanerEvidencePath("prog-1") + "\n  evidence_commit: " + sha + "\n" +
		"owner:\n  status: ACCEPTED\n  target: " + sha + "\n"
	writeProgramState(r, base, body)
	registerTask(t, r, "t1", base)
	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "最终集成")
}

func TestGateFinalDeliveryCleanerNotClean(t *testing.T) {
	r, base := newProgramGateRepo(t)
	sha := shaLike("final")
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + sha + "\n  evidence: final-integration.md\n" +
		"review:\n  status: PENDING\n" +
		"owner:\n  status: ACCEPTED\n  target: " + sha + "\n"
	writeProgramState(r, base, body)
	registerTask(t, r, "t1", base)
	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "Cleaner")
}

func TestGateFinalDeliveryOwnerNotAccepted(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: PENDING\n"
	writeProgramState(r, base, body)
	registerTask(t, r, "t1", base)
	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "ACCEPTED")
}

// 父分支漂移（base_commit 不再是父分支祖先）→ 最终 Gate FAIL。
func TestGateFinalDeliveryParentDrift(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	registerTask(t, r, "t1", base)

	// 重写 origin/develop 为与 base 无关的新根。
	r.git("checkout", "--orphan", "rewritten")
	newHead := r.commit("rewritten root")
	r.git("update-ref", "refs/remotes/origin/develop", newHead)

	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "漂移")
}

// 父分支仅 fast-forward（base_commit 仍为祖先）→ 漂移检查通过（Merger 负责最终重新验证）。
func TestGateFinalDeliveryParentFastForwardOK(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	registerTask(t, r, "t1", base)

	// 切回 develop 前进一个 commit（fast-forward），base 仍是祖先。
	r.git("checkout", "develop")
	r.write("README.md", "# base\nmore\n")
	r.commit("advance")
	r.syncOrigin("develop")

	expectStatus(t, gateValidator(r).GateFinalDelivery("prog-1"), StatusPass)
}

// Assembler 前置 Gate：缺必需子 Task → FAIL；齐备 → PASS。
func TestGateAssemblerStartMissingTask(t *testing.T) {
	r, base := newProgramGateRepo(t)
	writeProgramState(r, base, passingBody(base, base))
	// 未登记任何 task。
	expectIssueContains(t, gateValidator(r).GateAssemblerStart("prog-1"), "缺必需子 Task")
}

func TestGateAssemblerStartPass(t *testing.T) {
	r, base := newProgramGateRepo(t)
	writeProgramState(r, base, passingBody(base, base))
	registerTask(t, r, "t1", base)
	expectStatus(t, gateValidator(r).GateAssemblerStart("prog-1"), StatusPass)
}

// --- Program Cleaner 独立 Gate（F5）---

func TestGateProgramCleanerPass(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	expectStatus(t, gateValidator(r).GateProgramCleaner("prog-1"), StatusPass)
}

// 真实时序：C0（最终集成，不含报告）→ Cleaner 产生报告 → C1（证据 commit，新增报告）
// → 登记状态 → Cleaner Gate PASS → Final Gate PASS。
func TestProgramCleanerEvidenceTemporalSequence(t *testing.T) {
	r, base := newProgramGateRepo(t)

	// 1. 最终集成 C0（不含 Cleaner 报告）。
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("final integration C0")

	// 2. 确认 C0 不包含 Cleaner 报告（先集成后审查，不得预含报告）。
	git := &ExecGit{Root: r.dir}
	if ok, _ := git.PathExistsAt(c0, ProgramCleanerEvidencePath("prog-1")); ok {
		t.Fatalf("C0 不应包含 Cleaner 报告（时序倒置）")
	}

	// 3. Cleaner 在 C0 之后产生审查报告。
	// 4. 报告提交为 C1。
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", c0))
	c1 := r.commit("cleaner evidence C1")

	// 5. 登记 C1 形成后续状态。
	writeProgramState(r, base, passingBody(c0, c1))
	registerTask(t, r, "t1", base)

	// 6. Program Cleaner Gate PASS。
	expectStatus(t, gateValidator(r).GateProgramCleaner("prog-1"), StatusPass)
	// 7. Program Final Gate PASS。
	expectStatus(t, gateValidator(r).GateFinalDelivery("prog-1"), StatusPass)
}

func TestGateProgramCleanerNotClean(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n  status: PENDING\n" +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "尚未 CLEAN")
}

func TestGateProgramCleanerTargetMismatch(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	otherSHA := shaLike("other-final")
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, otherSHA, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "不一致")
}

func TestGateProgramCleanerMissingEvidence(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, "", c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "审查报告")
}

func TestGateProgramCleanerMissingEvidenceCommit(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), "") +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "证据 commit")
}

func TestGateProgramCleanerFakeSHA(t *testing.T) {
	r, base := newProgramGateRepo(t)
	finalizeProgramReviewed(t, r)
	fakeSHA := shaLike("not-a-real-commit")
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + fakeSHA + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, fakeSHA, ProgramCleanerEvidencePath("prog-1"), fakeSHA) +
		"owner:\n  status: ACCEPTED\n  target: " + fakeSHA + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "真实 commit")
}

// 复现用例：将已有 README.md 冒充 Cleaner Evidence，Gate 必须 FAIL（路径受限 + 非报告）。
func TestGateProgramCleanerEvidenceREADME(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, "README.md", c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "受限")
}

// 证据 commit 不是真实 commit → FAIL。
func TestGateProgramCleanerEvidenceCommitNotReal(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	fake := shaLike("evidence-not-real")
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), fake) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "真实 commit")
}

// 证据 commit 不是 C0 的后代（与最终集成脱节）→ FAIL。
// 用基线 base 作为 evidence_commit：base 是 C0 的祖先而非后代。
func TestGateProgramCleanerEvidenceCommitNotDescendant(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), base) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "后代")
}

// 证据 commit 与 C0 相同（未产生后续提交）→ FAIL（C1 必须严格晚于 C0）。
func TestGateProgramCleanerEvidenceCommitSameAsTarget(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c0) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "晚于")
}

// C1 存在但不包含正式审查报告 → FAIL。
func TestGateProgramCleanerReportMissingInC1(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	// C1 只新增一个无关文件（不含报告）。
	r.write("unrelated-note.md", "not a report\n")
	c1 := r.commit("unrelated evidence")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "不包含")
}

// 报告 Program ID 不匹配 → FAIL。
func TestGateProgramCleanerReportProgramMismatch(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("other-prog", c0))
	c1 := r.commit("review evidence")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "Program ID")
}

// 报告目标 SHA 不匹配 → FAIL。
func TestGateProgramCleanerReportTargetMismatch(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", shaLike("other-target")))
	c1 := r.commit("review evidence")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "不一致")
}

// 报告不是 CLEAN → FAIL。
func TestGateProgramCleanerReportNotClean(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), "program_id: prog-1\nkind: program-cleaner-review\ntarget: "+c0+"\nstatus: CHANGES_REQUIRED\n")
	c1 := r.commit("review evidence")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "格式非法")
}

// 报告格式错误（非 program-cleaner-review 类型 / 任意普通文件）→ FAIL。
func TestGateProgramCleanerReportBadFormat(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), "# 这只是一段普通文本\nnot a machine report\n")
	c1 := r.commit("review evidence")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "格式非法")
}

// 审查后 C0..C1 混入实质修改（生产代码）→ 旧 CLEAN 失效 → FAIL。
func TestGateProgramCleanerSubstantiveAfterC0(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	c0 := r.commit("program final integration")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", c0))
	r.write("internal/t1.go", "package t1\n// substantive after review\n")
	c1 := r.commit("review evidence + substantive")

	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n" + cleanerReviewOnly(ReviewClean, c0, ProgramCleanerEvidencePath("prog-1"), c1) +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "实质修改")
}

// 状态写 CLEAN、但没有合法证据（evidence/evidence_commit 均缺失）→ FAIL。
func TestGateProgramCleanerCleanWithoutEvidence(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, _ := finalizeProgramReviewed(t, r)
	body := "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + c0 + "\n  evidence: final-integration.md\n" +
		"review:\n  status: CLEAN\n  target: " + c0 + "\n" +
		"owner:\n  status: ACCEPTED\n  target: " + c0 + "\n"
	writeProgramState(r, base, body)
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "审查报告")
}

// --- F1：跨 Task 契约接入最终 Gate / Assembler 前置 Gate ---

func TestGateFinalDeliveryCrossTaskConflict(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, twoTaskBody(c0, c1))
	conflict := "resources:\n  reservations:\n    migration_version:\n      - \"20261001000011\"\n"
	r.write(".agent/tasks/t1/state.yaml", taskStateYAMLRes("t1", "prog-1", "feature/t1", base, conflict))
	r.write(".agent/tasks/t2/state.yaml", taskStateYAMLRes("t2", "prog-1", "feature/t2", base, conflict))
	registerOnly(t, r, "t1", base)
	registerOnly(t, r, "t2", base)
	expectIssueContains(t, gateValidator(r).GateFinalDelivery("prog-1"), "冲突")
}

func TestGateAssemblerStartCrossTaskConflict(t *testing.T) {
	r, base := newProgramGateRepo(t)
	writeProgramState(r, base, twoTaskBody(shaLike("final"), shaLike("evidence")))
	conflict := "resources:\n  reservations:\n    migration_version:\n      - \"20261001000011\"\n"
	r.write(".agent/tasks/t1/state.yaml", taskStateYAMLRes("t1", "prog-1", "feature/t1", base, conflict))
	r.write(".agent/tasks/t2/state.yaml", taskStateYAMLRes("t2", "prog-1", "feature/t2", base, conflict))
	registerOnly(t, r, "t1", base)
	registerOnly(t, r, "t2", base)
	expectIssueContains(t, gateValidator(r).GateAssemblerStart("prog-1"), "冲突")
}

// --- S7：C0 不得预含审查报告（时序倒置 fail-closed）---

// C0 预存报告、C1 修改报告为合法 → FAIL（不得放行时序倒置）。
func TestGateProgramCleanerReportPreExistingInC0Modified(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	// C0 预含一份审查报告（占位 target）。
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", shaLike("pre-existing")))
	c0 := r.commit("final integration C0（预含报告）")
	// C1 将报告修改为合法内容（target 绑定 C0）。
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", c0))
	c1 := r.commit("review evidence C1（修改报告为合法）")

	writeProgramState(r, base, passingBody(c0, c1))
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "C0 不得预含")
}

// C0 预存报告、C1 不修改报告（仅新增无关文件）→ FAIL。
func TestGateProgramCleanerReportPreExistingInC0Unmodified(t *testing.T) {
	r, base := newProgramGateRepo(t)
	r.write("final-integration.md", "final integration evidence\n")
	r.write(ProgramCleanerEvidencePath("prog-1"), cleanerReportYAML("prog-1", shaLike("pre-existing")))
	c0 := r.commit("final integration C0（预含报告）")
	// C1 不触碰审查报告，仅新增无关文件。
	r.write("unrelated-note.md", "not a report\n")
	c1 := r.commit("unrelated evidence C1（不修改报告）")

	writeProgramState(r, base, passingBody(c0, c1))
	expectIssueContains(t, gateValidator(r).GateProgramCleaner("prog-1"), "C0 不得预含")
}

// C0 不含报告、C1 新增有效报告 → PASS（回归保护）。
func TestGateProgramCleanerReportAddedInC1Pass(t *testing.T) {
	r, base := newProgramGateRepo(t)
	c0, c1 := finalizeProgramReviewed(t, r)
	writeProgramState(r, base, passingBody(c0, c1))
	expectStatus(t, gateValidator(r).GateProgramCleaner("prog-1"), StatusPass)
}
