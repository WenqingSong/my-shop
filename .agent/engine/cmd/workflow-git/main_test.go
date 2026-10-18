package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo 在临时目录初始化一个真实 Git 仓库（默认分支 develop，含一个初始提交），
// 返回 (repoDir, baseSHA)。测试只针对临时仓库，不对正式 main 做任何创建/合并/删除。
func setupGitRepo(t *testing.T) (string, string) {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitRun(t, repoDir, "init", "-b", "develop")
	gitRun(t, repoDir, "config", "user.email", "test@example.com")
	gitRun(t, repoDir, "config", "user.name", "workflow-git-test")
	gitRun(t, repoDir, "config", "commit.gpgsign", "false")
	gitRun(t, repoDir, "config", "tag.gpgsign", "false")
	writeFile(t, repoDir, "README.md", "hello\n")
	gitRun(t, repoDir, "add", "README.md")
	gitRun(t, repoDir, "commit", "-m", "init")
	base := strings.TrimSpace(gitOutput(t, repoDir, "rev-parse", "HEAD"))
	if base == "" {
		t.Fatal("空 base SHA")
	}
	return repoDir, base
}

// writeFile 在 dir 下写文件。
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// gitRun 执行 git 命令并在失败时终止测试。
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

// gitOutput 执行 git 命令并返回 stdout。
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

// TestVerifyBase 覆盖基线核验：父分支存在、base 真实且是父分支祖先。
func TestVerifyBase(t *testing.T) {
	repoDir, base := setupGitRepo(t)

	if code := run([]string{"verify-base", "--parent", "develop", "--base", base, "--root", repoDir}); code != 0 {
		t.Fatalf("verify-base 期望 0，实际 %d", code)
	}
	// 非法 SHA → 拒绝。
	if code := run([]string{"verify-base", "--parent", "develop", "--base", "deadbeef", "--root", repoDir}); code != 1 {
		t.Fatalf("verify-base 非法 base 期望 1，实际 %d", code)
	}
	// 父分支不存在 → 拒绝。
	if code := run([]string{"verify-base", "--parent", "nope", "--base", base, "--root", repoDir}); code != 1 {
		t.Fatalf("verify-base 缺父分支期望 1，实际 %d", code)
	}
}

// TestCreateBranch 覆盖独立 Task 分支创建：成功创建、重复创建安全拒绝。
func TestCreateBranch(t *testing.T) {
	repoDir, base := setupGitRepo(t)

	if code := run([]string{"create-branch", "feature/x", "--parent", "develop", "--base", base, "--root", repoDir}); code != 0 {
		t.Fatalf("create-branch 期望 0，实际 %d", code)
	}
	// 分支已存在 → 拒绝，不得覆盖。
	if code := run([]string{"create-branch", "feature/x", "--parent", "develop", "--base", base, "--root", repoDir}); code != 1 {
		t.Fatalf("重复 create-branch 期望 1，实际 %d", code)
	}
	// 分支确实落在 base。
	got := strings.TrimSpace(gitOutput(t, repoDir, "rev-parse", "feature/x"))
	if got != base {
		t.Fatalf("feature/x = %s，期望 %s", got, base)
	}
}

// TestCreateWorktree 覆盖真实 Worktree 创建与覆盖拒绝。
func TestCreateWorktree(t *testing.T) {
	repoDir, base := setupGitRepo(t)
	if code := run([]string{"create-branch", "feature/y", "--parent", "develop", "--base", base, "--root", repoDir}); code != 0 {
		t.Fatalf("create-branch 期望 0，实际 %d", code)
	}

	wtPath := filepath.Join(filepath.Dir(repoDir), "worktree-y")
	if code := run([]string{"create-worktree", wtPath, "feature/y", "--root", repoDir}); code != 0 {
		t.Fatalf("create-worktree 期望 0，实际 %d", code)
	}
	if code := run([]string{"worktree-exists", "feature/y", "--root", repoDir}); code != 0 {
		t.Fatalf("worktree-exists 期望 0，实际 %d", code)
	}
	// 重复创建同一路径 → 拒绝。
	if code := run([]string{"create-worktree", wtPath, "feature/y", "--root", repoDir}); code != 1 {
		t.Fatalf("重复 create-worktree 期望 1，实际 %d", code)
	}
	// 分支不存在时 create-worktree 不隐式创建分支 → 拒绝。
	if code := run([]string{"create-worktree", filepath.Join(filepath.Dir(repoDir), "wt-nope"), "feature/nope", "--root", repoDir}); code != 1 {
		t.Fatalf("create-worktree 缺分支期望 1，实际 %d", code)
	}
}

// TestBranchExistsWorktreeExists 覆盖存在性查询的退出码语义。
func TestBranchExistsWorktreeExists(t *testing.T) {
	repoDir, base := setupGitRepo(t)

	if code := run([]string{"branch-exists", "develop", "--root", repoDir}); code != 0 {
		t.Fatalf("branch-exists develop 期望 0，实际 %d", code)
	}
	if code := run([]string{"branch-exists", "missing", "--root", repoDir}); code != 1 {
		t.Fatalf("branch-exists missing 期望 1，实际 %d", code)
	}

	if code := run([]string{"create-branch", "feature/z", "--parent", "develop", "--base", base, "--root", repoDir}); code != 0 {
		t.Fatalf("create-branch 期望 0，实际 %d", code)
	}
	if code := run([]string{"worktree-exists", "feature/z", "--root", repoDir}); code != 1 {
		t.Fatalf("worktree-exists（未创建）期望 1，实际 %d", code)
	}
}
