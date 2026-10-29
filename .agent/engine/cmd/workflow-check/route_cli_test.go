package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖 `workflow-check route` 的 CLI 语义：
// 它是路线事实自检（非 Gate），默认要求 route 存在，历史任务需显式 --allow-legacy。

// routeCLIState 生成带/不带 route 的 v3 state.yaml。
func routeCLIState(route, contract string) string {
	routeLine := ""
	if route != "" {
		routeLine = "route: " + route + "\n"
	}
	return "schema_version: 3\n" + routeLine + "task_id: demo\n" +
		"contract:\n  status: " + contract + "\n  target: \"\"\n" +
		"resources:\n  reservations: {}\n" +
		"review:\n  status: NOT_REQUESTED\n  target: \"\"\n" +
		"owner:\n  status: PENDING\n  review_target: \"\"\n" +
		"delivery:\n  status: NOT_RUN\n  review_target: \"\"\n  feature_head: \"\"\n  develop_base: \"\"\n" +
		"blocked:\n  active: false\n  by: \"\"\n  reason: \"\"\n"
}

// routeCLIRepo 构造 init 现场：develop 基线 → feature 分支上的 task.md + state.yaml → 已同步远端。
func routeCLIRepo(t *testing.T, route, contract, complexity string) string {
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
	write(".agent/workflow.yaml", "schema_version: 1\ngit:\n  integration_branch: develop\nresources: {}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "baseline")
	git("update-ref", "refs/remotes/origin/develop", git("rev-parse", "HEAD"))

	git("checkout", "-q", "-b", "feature/agent-workflow-v2")
	write(".agent/tasks/demo/task.md", "# Task: demo\n\n## Goal\n演示。\n\n## Complexity\n"+complexity+"\n\n原因：夹具。\n")
	write(".agent/tasks/demo/state.yaml", routeCLIState(route, contract))
	git("add", "-A")
	git("commit", "-q", "-m", "task init")
	git("update-ref", "refs/remotes/origin/feature/agent-workflow-v2", git("rev-parse", "HEAD"))
	return dir
}

func TestRunRoutePass(t *testing.T) {
	dir := routeCLIRepo(t, "NORMAL", "NOT_REQUIRED", "NORMAL")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 0 {
		t.Fatalf("合法 route 期望 exit 0，实际 %d", code)
	}
}

func TestRunRouteContractMismatch(t *testing.T) {
	dir := routeCLIRepo(t, "ARCHITECT", "NOT_REQUIRED", "ARCHITECT")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("ARCHITECT + NOT_REQUIRED 期望 exit 1，实际 %d", code)
	}
}

func TestRunRouteDocumentMismatch(t *testing.T) {
	dir := routeCLIRepo(t, "COMPLEX", "PENDING", "NORMAL")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("route 与 task.md 不一致期望 exit 1，实际 %d", code)
	}
}

func TestRunRouteIllegalValue(t *testing.T) {
	dir := routeCLIRepo(t, "ARCH", "PENDING", "ARCHITECT")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("非法 route 期望 exit 1，实际 %d", code)
	}
}

func TestRunRouteLegacy(t *testing.T) {
	dir := routeCLIRepo(t, "", "NOT_REQUIRED", "NORMAL")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("无 route 时默认应 fail-closed（exit 1），实际 %d", code)
	}
	if code := run([]string{"route", ".agent/tasks/demo", "--allow-legacy", "--root", dir}); code != 0 {
		t.Fatalf("显式 --allow-legacy 期望 exit 0，实际 %d", code)
	}
}

func TestRunRouteUsageErrors(t *testing.T) {
	dir := routeCLIRepo(t, "NORMAL", "NOT_REQUIRED", "NORMAL")
	if code := run([]string{"route", "--root", dir}); code != 2 {
		t.Fatalf("缺 task 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"route", ".agent/tasks/demo", "--bogus", "--root", dir}); code != 2 {
		t.Fatalf("未知参数期望 exit 2，实际 %d", code)
	}
}

func TestRunRouteNonV2State(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.c",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.c")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e.c")
	git("config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(dir, ".agent/tasks/demo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent/tasks/demo/state.yaml"), []byte("task_id: demo\nphase: NEW\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "legacy")
	if code := run([]string{"route", ".agent/tasks/demo", "--root", dir}); code != 2 {
		t.Fatalf("非 V2 task 期望 exit 2，实际 %d", code)
	}
}

// --root 允许相对路径：必须按当前工作目录解析，不能因相对/绝对混用而误判越界。
func TestRunRouteRelativeRoot(t *testing.T) {
	dir := routeCLIRepo(t, "NORMAL", "NOT_REQUIRED", "NORMAL")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if code := run([]string{"route", ".agent/tasks/demo", "--root", rel}); code != 0 {
		t.Fatalf("相对 --root 期望 exit 0，实际 %d", code)
	}
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", rel}); code != 0 {
		t.Fatalf("相对 --root 的 gate 期望 exit 0，实际 %d", code)
	}
}
