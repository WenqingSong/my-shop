package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workflow-v2-engine/internal/workflow"
)

// programCLIRepo 构造一个含 Program State + Registry + origin/develop + 子 Task state 的临时仓库。
func programCLIRepo(t *testing.T, programBody string, register []string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.c",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.c",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	git("init", "-q")
	git("checkout", "-q", "-b", "develop")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e.c")
	git("config", "commit.gpgsign", "false")
	write("README.md", "# base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "baseline")
	base := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/develop", base)

	// 产出真实证据时序：C0（最终集成，不含报告）+ C1（审查报告）。
	// 替换 programBody 中的 __FINAL_SHA__（=C0）与 __EVIDENCE_SHA__（=C1）占位符。
	write("final-integration.md", "final integration evidence\n")
	git("add", "-A")
	git("commit", "-q", "-m", "final integration")
	finalSHA := git("rev-parse", "HEAD")

	write(workflow.ProgramCleanerEvidencePath("prog-1"),
		"program_id: prog-1\nkind: program-cleaner-review\ntarget: "+finalSHA+"\nstatus: CLEAN\n")
	git("add", "-A")
	git("commit", "-q", "-m", "cleaner review evidence")
	evidenceSHA := git("rev-parse", "HEAD")

	programBody = strings.ReplaceAll(programBody, "__FINAL_SHA__", finalSHA)
	programBody = strings.ReplaceAll(programBody, "__EVIDENCE_SHA__", evidenceSHA)

	write(".agent/programs/prog-1/program-state.yaml",
		"schema_version: 1\nprogram_id: prog-1\nparent_branch: develop\nbase_commit: "+base+"\n"+programBody)

	store := &workflow.RegistryStore{Root: dir}
	for _, id := range register {
		write(".agent/tasks/"+id+"/state.yaml", cliTaskStateYAML(id, base))
		d := workflow.RegisteredDelivery{
			TaskID: id, Branch: "feature/" + id, DeliverySHA: base,
			ReviewStatus: workflow.ReviewClean, ReviewTarget: base,
			OwnerStatus: workflow.OwnerAccepted, OwnerTarget: base,
			RegisteredBy: "CollectorAgent", RegisteredAt: "2026-10-16T00:00:00Z",
		}
		if err := store.Register("prog-1", d); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	return dir
}

// cliTaskStateYAML 生成一条子 Task state（供跨 Task 契约校验加载准确 Evidence）。
func cliTaskStateYAML(taskID, deliverySHA string) string {
	return `schema_version: 4
task_id: ` + taskID + `
task_type: NORMAL
git:
  parent_branch: develop
  base_commit: ` + deliverySHA + `
  task_branch: feature/` + taskID + `
program:
  id: prog-1
  version: 1
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  reservations: {}
review:
  status: CLEAN
  target: ` + deliverySHA + `
owner:
  status: ACCEPTED
  review_target: ` + deliverySHA + `
delivery:
  status: PASS
  review_target: ` + deliverySHA + `
  feature_head: ` + deliverySHA + `
  develop_base: ` + deliverySHA + `
`
}

func cliPassingBody() string {
	return "tasks:\n" +
		"  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: __FINAL_SHA__\n  evidence: final-integration.md\n" +
		"review:\n  status: CLEAN\n  target: __FINAL_SHA__\n  evidence: " + workflow.ProgramCleanerEvidencePath("prog-1") + "\n  evidence_commit: __EVIDENCE_SHA__\n" +
		"owner:\n  status: ACCEPTED\n  target: __FINAL_SHA__\n"
}

func TestRunUsageErrors(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("缺子命令期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"gate", "bogus", "prog-1"}); code != 2 {
		t.Fatalf("未知 gate 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"bogus"}); code != 2 {
		t.Fatalf("未知子命令期望 exit 2，实际 %d", code)
	}
}

func TestRunGateFinalDeliveryPass(t *testing.T) {
	dir := programCLIRepo(t, cliPassingBody(), []string{"t1"})
	if code := run([]string{"gate", "final-delivery", "prog-1", "--root", dir}); code != 0 {
		t.Fatalf("合法 final-delivery 期望 exit 0，实际 %d", code)
	}
}

func TestRunGateFinalDeliveryFail(t *testing.T) {
	dir := programCLIRepo(t,
		"tasks:\n  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n"+
			"final_integration:\n  status: NOT_RUN\n",
		[]string{"t1"})
	if code := run([]string{"gate", "final-delivery", "prog-1", "--root", dir}); code != 1 {
		t.Fatalf("缺最终集成 Evidence 期望 exit 1，实际 %d", code)
	}
}

func TestRunGateProgramCleanerPass(t *testing.T) {
	dir := programCLIRepo(t, cliPassingBody(), []string{"t1"})
	if code := run([]string{"gate", "program-cleaner", "prog-1", "--root", dir}); code != 0 {
		t.Fatalf("合法 program-cleaner 期望 exit 0，实际 %d", code)
	}
}

func TestRunGateProgramCleanerFail(t *testing.T) {
	dir := programCLIRepo(t,
		"tasks:\n  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n"+
			"final_integration:\n  status: PASS\n  sha: __FINAL_SHA__\n  evidence: final-integration.md\n"+
			"review:\n  status: PENDING\n",
		[]string{"t1"})
	if code := run([]string{"gate", "program-cleaner", "prog-1", "--root", dir}); code != 1 {
		t.Fatalf("program-cleaner 未 CLEAN 期望 exit 1，实际 %d", code)
	}
}

func TestRunPlanPass(t *testing.T) {
	dir := programCLIRepo(t, cliPassingBody(), []string{"t1"})
	if code := run([]string{"plan", "prog-1", "--root", dir}); code != 0 {
		t.Fatalf("plan 期望 exit 0，实际 %d", code)
	}
}
