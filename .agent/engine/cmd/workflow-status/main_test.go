package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const wfStatusConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
`

// v4CliRepo 构造含 V4 state 的临时 git 仓库（feature 已同步远端、工作区 clean），返回仓库根。
func v4CliRepo(t *testing.T, blockedActive bool, contractStatus string) string {
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
	write(".agent/workflow.yaml", wfStatusConfig)
	write("README.md", "# base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "baseline")
	base := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/develop", base)

	git("checkout", "-q", "-b", "feature/agent-workflow-v2")
	blockedBy := ""
	blockedReason := ""
	if blockedActive {
		blockedBy = "Coder"
		blockedReason = "缺少批准的 Contract"
	}
	state := fmt.Sprintf(`schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: develop
  base_commit: %s
  task_branch: feature/agent-workflow-v2
contract:
  status: %s
  target: ""
resources:
  reservations: {}
review:
  status: NOT_REQUESTED
  target: ""
owner:
  status: PENDING
  review_target: ""
delivery:
  status: NOT_RUN
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: %t
  by: %q
  reason: %q
`, base, contractStatus, blockedActive, blockedBy, blockedReason)
	write(".agent/tasks/demo/state.yaml", state)
	git("add", "-A")
	git("commit", "-q", "-m", "v4 state")
	head := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/feature/agent-workflow-v2", head)

	return dir
}

func TestRunStatusUsageErrors(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("缺子命令期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"bogus", "demo"}); code != 2 {
		t.Fatalf("未知子命令期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"inspect"}); code != 2 {
		t.Fatalf("inspect 缺 task 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"recovery-check"}); code != 2 {
		t.Fatalf("recovery-check 缺 task 期望 exit 2，实际 %d", code)
	}
}

func TestRunInspect(t *testing.T) {
	dir := v4CliRepo(t, true, "NOT_REQUIRED")
	if code := run([]string{"inspect", "demo", "--root", dir}); code != 0 {
		t.Fatalf("inspect 合法 task 期望 exit 0，实际 %d", code)
	}
}

func TestRunInspectNotFound(t *testing.T) {
	dir := v4CliRepo(t, false, "NOT_REQUIRED")
	if code := run([]string{"inspect", "no-such-task", "--root", dir}); code != 2 {
		t.Fatalf("inspect 不存在的 task 期望 exit 2，实际 %d", code)
	}
}

func TestRunRecoveryCheckBlockedActive(t *testing.T) {
	dir := v4CliRepo(t, true, "NOT_REQUIRED")
	if code := run([]string{"recovery-check", "demo", "--root", dir}); code != 1 {
		t.Fatalf("ACTIVE BLOCKED 期望 exit 1，实际 %d", code)
	}
}

func TestRunRecoveryCheckGatePass(t *testing.T) {
	dir := v4CliRepo(t, false, "NOT_REQUIRED")
	if code := run([]string{"recovery-check", "demo", "--root", dir}); code != 0 {
		t.Fatalf("解除 BLOCKED 且 Gate PASS 期望 exit 0，实际 %d", code)
	}
}

func TestRunRecoveryCheckGateFail(t *testing.T) {
	dir := v4CliRepo(t, false, "PENDING")
	if code := run([]string{"recovery-check", "demo", "--root", dir}); code != 1 {
		t.Fatalf("解除 BLOCKED 但 Gate FAIL 期望 exit 1，实际 %d", code)
	}
}

func TestRunIndexFormatsAndErrors(t *testing.T) {
	root := v4CliRepo(t, false, "NOT_REQUIRED")
	for _, format := range []string{"tasks", "programs", "meta"} {
		if code := run([]string{"index", "--root", root, "--format", format}); code != 0 {
			t.Fatalf("%s: %d", format, code)
		}
	}
	for _, args := range [][]string{{"--format", "invalid"}, {"--format"}, {"demo"}} {
		full := append([]string{"index", "--root", root}, args...)
		if code := run(full); code != 2 {
			t.Fatalf("%v: %d", full, code)
		}
	}
	if code := run([]string{"index", "--root", t.TempDir()}); code != 2 {
		t.Fatalf("non-repo: %d", code)
	}
}
