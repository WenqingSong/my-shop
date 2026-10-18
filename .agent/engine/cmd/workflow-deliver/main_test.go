package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖 workflow-deliver CLI 的授权强制与 plan/execute 行为。
// 核心：execute 必须显式提供 --source-sha 与 --parent-branch（无 --yes、无自动审批）。

type cliRepo struct {
	t    *testing.T
	host string
}

func newCLIRepo(t *testing.T) *cliRepo {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	host := filepath.Join(base, "host")
	cliGit(t, base, "init", "--bare", "-q", "origin.git")
	if err := os.MkdirAll(host, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cliGit(t, host, "init", "-q")
	cliGit(t, host, "config", "user.name", "test")
	cliGit(t, host, "config", "user.email", "test@example.com")
	cliGit(t, host, "config", "commit.gpgsign", "false")
	cliGit(t, host, "remote", "add", "origin", origin)
	return &cliRepo{t: t, host: host}
}

func cliGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *cliRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.host, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write: %v", err)
	}
}

func (r *cliRepo) commit(msg string) string {
	r.t.Helper()
	cliGit(r.t, r.host, "add", "-A")
	cliGit(r.t, r.host, "commit", "-q", "-m", msg)
	return cliGit(r.t, r.host, "rev-parse", "HEAD")
}

func (r *cliRepo) push(branch string) {
	r.t.Helper()
	cliGit(r.t, r.host, "push", "-q", "origin", branch+":refs/heads/"+branch)
}

func (r *cliRepo) remoteSHA(branch string) string {
	r.t.Helper()
	out := cliGit(r.t, r.host, "ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(out)
	if len(fields) == 0 {
		r.t.Fatalf("ls-remote %s 无输出", branch)
	}
	return fields[0]
}

func setupCLIProgram(t *testing.T) (*cliRepo, string, string) {
	r := newCLIRepo(t)
	cliGit(t, r.host, "checkout", "-q", "-b", "develop")
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.push("develop")

	cliGit(t, r.host, "checkout", "-q", "-b", "feature/t1")
	r.write("internal/t1.go", "package t1\n")
	final := r.commit("implementation") // C0：最终集成，不含审查报告
	r.push("feature/t1")

	cliGit(t, r.host, "checkout", "-q", "-b", "integration/prog-1")
	r.push("integration/prog-1")

	// 独立证据分支：审查报告提交为 C1，保持 origin/integration/prog-1 == C0 不变。
	cliGit(t, r.host, "checkout", "-q", "-b", "review/prog-1")
	r.write(".agent/programs/prog-1/cleaner-review.yaml",
		"program_id: prog-1\nkind: program-cleaner-review\ntarget: "+final+"\nstatus: CLEAN\n")
	evidenceCommit := r.commit("cleaner review evidence")
	r.push("review/prog-1")

	state := "schema_version: 1\nprogram_id: prog-1\nparent_branch: develop\nbase_commit: " + base + "\n" +
		"tasks:\n  - task_id: t1\n    task_type: NORMAL\n    branch: feature/t1\n" +
		"final_integration:\n  status: PASS\n  sha: " + final + "\n  evidence: final-integration.md\n" +
		"review:\n  status: CLEAN\n  target: " + final + "\n  evidence: .agent/programs/prog-1/cleaner-review.yaml\n  evidence_commit: " + evidenceCommit + "\n" +
		"owner:\n  status: ACCEPTED\n  target: " + final + "\n"
	r.write(".agent/programs/prog-1/program-state.yaml", state)

	reg := "schema_version: 1\nprogram_id: prog-1\nrevision: 1\ndeliveries:\n" +
		"  - task_id: t1\n    branch: feature/t1\n    delivery_sha: " + final + "\n" +
		"    review_status: CLEAN\n    review_target: " + final + "\n" +
		"    owner_status: ACCEPTED\n    owner_target: " + final + "\n" +
		"    registered_by: CollectorAgent\n    registered_at: 2026-10-16T00:00:00Z\n"
	r.write(".agent/programs/prog-1/registry.yaml", reg)

	taskState := "schema_version: 4\ntask_id: t1\ntask_type: NORMAL\n" +
		"git:\n  parent_branch: develop\n  base_commit: " + base + "\n  task_branch: feature/t1\n" +
		"program:\n  id: prog-1\n  version: 1\n" +
		"contract:\n  status: NOT_REQUIRED\n  target: \"\"\n" +
		"review:\n  status: CLEAN\n  target: " + final + "\n" +
		"owner:\n  status: ACCEPTED\n  review_target: " + final + "\n" +
		"delivery:\n  status: PASS\n  review_target: " + final + "\n  feature_head: " + final + "\n  develop_base: " + base + "\n" +
		"blocked:\n  active: false\n  by: \"\"\n  reason: \"\"\n"
	r.write(".agent/tasks/t1/state.yaml", taskState)
	return r, base, final
}

func TestExecuteRequiresExplicitAuthorization(t *testing.T) {
	// 缺 --source-sha / --parent-branch 必须 exit 2，绝不自动审批。
	if code := run([]string{"execute", "program", "prog-1", "--source-branch", "integration/prog-1"}); code != 2 {
		t.Fatalf("缺授权应 exit 2，实际 %d", code)
	}
	if code := run([]string{"execute", "program", "prog-1", "--source-branch", "integration/prog-1", "--parent-branch", "develop"}); code != 2 {
		t.Fatalf("缺 --source-sha 应 exit 2，实际 %d", code)
	}
}

func TestPlanProgramCLI(t *testing.T) {
	r, base, _ := setupCLIProgram(t)
	code := run([]string{"plan", "program", "prog-1", "--root", r.host, "--source-branch", "integration/prog-1", "--test", "true"})
	if code != 0 {
		t.Fatalf("plan 应 exit 0，实际 %d", code)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("plan 只读，origin/develop 应保持 %s，实际 %s", base, got)
	}
}

func TestExecuteProgramCLI(t *testing.T) {
	r, _, final := setupCLIProgram(t)
	// 错误授权 SHA → exit 1，父分支不变。
	code := run([]string{"execute", "program", "prog-1", "--root", r.host, "--source-branch", "integration/prog-1",
		"--source-sha", "0000000000000000000000000000000000000000", "--parent-branch", "develop", "--test", "true"})
	if code != 1 {
		t.Fatalf("错误授权应 exit 1，实际 %d", code)
	}
	if got := r.remoteSHA("develop"); got == final {
		t.Fatal("错误授权时父分支不应更新")
	}

	// 正确授权 → exit 0，父分支更新。
	code = run([]string{"execute", "program", "prog-1", "--root", r.host, "--source-branch", "integration/prog-1",
		"--source-sha", final, "--parent-branch", "develop", "--test", "test -f internal/t1.go"})
	if code != 0 {
		t.Fatalf("正确授权应 exit 0，实际 %d", code)
	}
	if got := r.remoteSHA("develop"); got != final {
		t.Fatalf("交付后 origin/develop 应等于 %s，实际 %s", final, got)
	}
}
