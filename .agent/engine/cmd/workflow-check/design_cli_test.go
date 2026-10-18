package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖 I3 接线：DESIGN 六个 Gate 通过 workflow-check 的正式 CLI 分发机制可达，
// 而非只能在 Go 单元测试中直接调用。覆盖真实 CLI 负向（exit 1）与正向（exit 0）。

// designCLIRepo 构造一个 DESIGN 任务的临时仓库：develop 基线 + feature 分支（已同步远端），
// 返回仓库根与 develop 基线 SHA。state.yaml 的 git.base_commit 绑定真实基线 SHA。
func designCLIRepo(t *testing.T) (string, string) {
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
	write(".agent/workflow.yaml", defaultWorkflowConfig)
	write("README.md", "# base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "baseline")
	base := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/develop", base)

	git("checkout", "-q", "-b", "feature/agent-workflow-v2")
	write(".agent/tasks/demo/state.yaml", designStateYAML(base))
	git("add", "-A")
	git("commit", "-q", "-m", "design state")
	git("update-ref", "refs/remotes/origin/feature/agent-workflow-v2", git("rev-parse", "HEAD"))

	return dir, base
}

// designStateYAML 返回 DESIGN 任务 state（架构 PENDING，Architect 入口前置满足）。
func designStateYAML(base string) string {
	return `schema_version: 4
task_id: demo
task_type: DESIGN
git:
  parent_branch: develop
  base_commit: ` + base + `
  task_branch: feature/agent-workflow-v2
design:
  status: PENDING
contract:
  status: NOT_REQUIRED
  target: ""
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
  active: false
  by: ""
  reason: ""
`
}

// TestDesignGateCLINegative 覆盖：非 DESIGN 任务调用 DESIGN Gate 返回 FAIL（exit 1），
// 而非「未知 gate」（exit 2），证明 CLI 已正确分发到 DESIGN Gate。
func TestDesignGateCLINegative(t *testing.T) {
	dir := cliRepo(t, v2PassState) // V2 任务，非 DESIGN
	if code := run([]string{"gate", "design-architect-start", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("非 DESIGN 任务调用 design-architect-start 期望 exit 1，实际 %d", code)
	}
}

// TestDesignGateCLIPositive 覆盖：DESIGN 任务通过 CLI 运行 design-architect-start 返回 PASS（exit 0）。
func TestDesignGateCLIPositive(t *testing.T) {
	dir, _ := designCLIRepo(t)
	if code := run([]string{"gate", "design-architect-start", ".agent/tasks/demo", "--root", dir}); code != 0 {
		t.Fatalf("DESIGN 任务 design-architect-start 期望 exit 0，实际 %d", code)
	}
}

// TestDesignGateCLIAllRegistered 覆盖：六个 DESIGN Gate 名都在 CLI 合法 Gate 表中（不报未知 gate）。
func TestDesignGateCLIAllRegistered(t *testing.T) {
	dir, _ := designCLIRepo(t)
	for _, g := range []string{
		"design-architect-start",
		"design-architecture-review",
		"design-docsspecwriter-start",
		"design-cleaner-start",
		"design-owner-gate-start",
		"design-final-approval",
	} {
		if code := run([]string{"gate", g, ".agent/tasks/demo", "--root", dir}); code == 2 {
			t.Fatalf("gate %q 应已注册（不应报未知 gate exit 2）", g)
		}
	}
}
