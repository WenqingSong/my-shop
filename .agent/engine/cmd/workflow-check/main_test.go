package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const defaultWorkflowConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
`

// cliRepo 构造一个含 feature 分支且已同步远端的临时 git 仓库，返回仓库根目录。
func cliRepo(t *testing.T, stateYAML string) string {
	return cliRepoWithConfig(t, stateYAML, defaultWorkflowConfig)
}

// cliRepoWithConfig 与 cliRepo 相同，但允许指定（或不指定）机器配置内容。
func cliRepoWithConfig(t *testing.T, stateYAML, configYAML string) string {
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
	if configYAML != "" {
		write(".agent/workflow.yaml", configYAML)
	}
	write("README.md", "# base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "baseline")
	git("update-ref", "refs/remotes/origin/develop", git("rev-parse", "HEAD"))

	git("checkout", "-q", "-b", "feature/agent-workflow-v2")
	write(".agent/tasks/demo/state.yaml", stateYAML)
	git("add", "-A")
	git("commit", "-q", "-m", "state")
	git("update-ref", "refs/remotes/origin/feature/agent-workflow-v2", git("rev-parse", "HEAD"))

	return dir
}

const v2PassState = `schema_version: 2
task_id: demo
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  migrations: []
  error_code_domains: []
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

const v2ContractPendingState = `schema_version: 2
task_id: demo
contract:
  status: PENDING
  target: ""
resources:
  migrations: []
  error_code_domains: []
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

func TestRunUsageErrors(t *testing.T) {
	if code := run([]string{"gate"}); code != 2 {
		t.Fatalf("缺 gate/task 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"gate", "bogus", "x"}); code != 2 {
		t.Fatalf("未知 gate 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"validate", "x"}); code != 2 {
		t.Fatalf("非 gate 子命令期望 exit 2，实际 %d", code)
	}
}

func TestRunGatePass(t *testing.T) {
	dir := cliRepo(t, v2PassState)
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 0 {
		t.Fatalf("合法 coder-start 期望 exit 0，实际 %d", code)
	}
}

func TestRunGateFail(t *testing.T) {
	dir := cliRepo(t, v2ContractPendingState)
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("Contract PENDING 期望 exit 1，实际 %d", code)
	}
}

func TestRunGateErrorNonV2(t *testing.T) {
	dir := cliRepo(t, "task_id: demo\nphase: NEW\n")
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 2 {
		t.Fatalf("非 V2 task 期望 exit 2，实际 %d", code)
	}
}

func TestRunGateStrictYamlError(t *testing.T) {
	dir := cliRepo(t, `schema_version: 3
task_id: demo
resources:
  reservationss: {}
`)
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 2 {
		t.Fatalf("strict YAML 未知字段 期望 exit 2，实际 %d", code)
	}
}

func TestRunGateConfigMissing(t *testing.T) {
	dir := cliRepoWithConfig(t, v2PassState, "")
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 2 {
		t.Fatalf("机器配置缺失 期望 exit 2，实际 %d", code)
	}
}

// chdirEngine 在临时仓库内创建 .agent/engine 子目录并切换过去，返回恢复函数。
// Engine 是 nested module，其 cwd 可能位于 repo/.agent/engine，Repository Root 必须
// 仍通过 git 解析为 repo 根（而非 .agent/engine），否则会错误地拼接出
// .agent/engine/.agent/workflow.yaml 之类的路径。
func chdirEngine(t *testing.T, dir string) func() {
	t.Helper()
	engineDir := filepath.Join(dir, ".agent", "engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir engine: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(engineDir); err != nil {
		t.Fatalf("chdir engine: %v", err)
	}
	return func() { _ = os.Chdir(prev) }
}

// TestFindRootFromEngineSubdir 覆盖：cwd 位于 repo/.agent/engine 时，
// findRoot 仍通过 git rev-parse --show-toplevel 解析到 Repository Root。
func TestFindRootFromEngineSubdir(t *testing.T) {
	dir := cliRepo(t, v2PassState)
	restore := chdirEngine(t, dir)
	defer restore()

	got, err := filepath.EvalSymlinks(findRoot())
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval dir: %v", err)
	}
	if got != want {
		t.Fatalf("findRoot 期望 %q，实际 %q", want, got)
	}
}

// TestRunGateFromEngineSubdirNoRoot 覆盖：不传 --root、从 .agent/engine cwd 运行 gate，
// 仍能解析 Repository Root、读取 repo/.agent/workflow.yaml 并正确执行 Gate（exit 0）。
func TestRunGateFromEngineSubdirNoRoot(t *testing.T) {
	dir := cliRepo(t, v2PassState)
	restore := chdirEngine(t, dir)
	defer restore()

	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo"}); code != 0 {
		t.Fatalf("从 engine 子目录运行 gate 期望 exit 0，实际 %d", code)
	}
}

// TestRunGateExplicitRootStillWorks 覆盖：显式 --root 仍可工作（不依赖 cwd）。
func TestRunGateExplicitRootStillWorks(t *testing.T) {
	dir := cliRepo(t, v2PassState)
	restore := chdirEngine(t, dir)
	defer restore()

	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 0 {
		t.Fatalf("显式 --root 期望 exit 0，实际 %d", code)
	}
}
