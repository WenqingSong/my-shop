package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// 本文件实现 Merger 隔离集成所需的 git 读写原语（Task 09）。
//
// 与只读的 Git 接口（git.go）刻意分离：Merger 是唯一承担「受控隔离集成」写操作的组件，
// 但它绝不直接改写共享父分支工作区——所有 merge 都发生在新建的 scratch worktree 内，
// 最终只在 Owner 明确授权、父分支未漂移、合并测试通过的前提下，以 fast-forward-only
// 方式推送精确目标 SHA。禁止任何 force push / force-with-lease。

// GitOps 是 Merger 隔离集成所需的 git 读写接口。
// 所有写操作要么发生在隔离 scratch worktree，要么是 fast-forward-only 的精确 SHA 推送。
type GitOps interface {
	// Fetch 更新远端引用。
	Fetch() error
	// RevParse 返回 ref 解析为 commit 后的完整 SHA。
	RevParse(ref string) (string, error)
	// CommitExists 报告 sha 是否为仓库中真实存在的 commit 对象。
	CommitExists(sha string) (bool, error)
	// IsAncestor 报告 ancestor 是否为 desc 的祖先 commit（正常 false 与 runtime error 区分）。
	IsAncestor(ancestor, desc string) (bool, error)
	// WorktreeAdd 新建隔离 worktree：创建 scratch 分支并 checkout 到 path，起点为 base。
	WorktreeAdd(path, branch, base string) error
	// WorktreeRemove 移除隔离 worktree（scratch worktree 可被 dispose，允许 --force 清理）。
	WorktreeRemove(path string) error
	// 只删除本次创建且仍指向 expected 的 scratch ref；已变化时保留。
	DeleteScratchBranch(branch, expected string) error
	// Merge 在 worktreeDir 内把 target 合并进当前分支，返回机器合并结果。
	// 冲突（unmerged files）不视为 error，而是通过 MergeResult.Conflict 表达。
	Merge(worktreeDir, target string) (MergeResult, error)
	// MergeAbort 中止 worktreeDir 内未完成的合并。
	MergeAbort(worktreeDir string) error
	// StatusPorcelain 返回指定目录工作区的 git status --porcelain 输出。
	StatusPorcelain(dir string) ([]string, error)
	// RunTest 在 dir 内运行测试命令，返回标准输出（合并 stdout/stderr）与 error。
	// 测试失败（非零退出码）返回 error，不伪造 PASS。
	RunTest(dir string, args []string) (string, error)
	// PushFastForward 以 fast-forward-only 方式推送精确 SHA 到目标分支（禁止 force）。
	// 若目标分支已移动导致非快进，git 将拒绝，函数返回 error。
	PushFastForward(remote, sha, branch string) error
}

// ExecGitOps 是基于 os/exec 调用 git 的 GitOps 实现，工作在指定仓库根目录。
type ExecGitOps struct {
	Root string
}

// gitRun 在 dir 内运行 git 命令，返回标准输出（错误时含 stderr）。
// 不注入 GIT_OPTIONAL_LOCKS，因为 Merger 需要执行真实写操作。
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (g *ExecGitOps) Fetch() error {
	_, err := gitRun(g.Root, "fetch", "origin")
	return err
}

func (g *ExecGitOps) RevParse(ref string) (string, error) {
	out, err := gitRun(g.Root, "rev-parse", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g *ExecGitOps) CommitExists(sha string) (bool, error) {
	cmd := exec.Command("git", "cat-file", "-e", sha+"^{commit}")
	cmd.Dir = g.Root
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 128 {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file -e %s^{commit}: %w", sha, err)
	}
	return true, nil
}

func (g *ExecGitOps) IsAncestor(ancestor, desc string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, desc)
	cmd.Dir = g.Root
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ee.ExitCode() == 1 {
				return false, nil
			}
			return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, desc, err)
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, desc, err)
	}
	return true, nil
}

func (g *ExecGitOps) WorktreeAdd(path, branch, base string) error {
	_, err := gitRun(g.Root, "worktree", "add", "-b", branch, path, base)
	return err
}

func (g *ExecGitOps) WorktreeRemove(path string) error {
	// scratch worktree 是 Merger 自行创建的一次性隔离环境，允许 --force 清理
	// （这不是 force push，不涉及共享历史重写）。
	if _, err := gitRun(g.Root, "worktree", "remove", path); err != nil {
		if _, ferr := gitRun(g.Root, "worktree", "remove", "--force", path); ferr != nil {
			return fmt.Errorf("移除 scratch worktree %s: %v", path, ferr)
		}
	}
	return nil
}

func (g *ExecGitOps) DeleteScratchBranch(branch, expected string) error {
	if !strings.HasPrefix(branch, "scratch/merger-") || !validSHA(expected) || expected == "" {
		return fmt.Errorf("拒绝清理非 scratch 分支或非法 SHA")
	}
	_, err := gitRun(g.Root, "update-ref", "-d", "refs/heads/"+branch, expected)
	return err
}

// Merge 在 worktreeDir 内把 target 合并进当前分支。
// 冲突通过 unmerged files（git diff --diff-filter=U）判定，不与其它 merge 错误混淆。
func (g *ExecGitOps) Merge(worktreeDir, target string) (MergeResult, error) {
	if _, err := gitRun(worktreeDir, "merge", "--no-edit", target); err != nil {
		conflicted, cerr := gitRun(worktreeDir, "diff", "--name-only", "--diff-filter=U")
		if cerr == nil && strings.TrimSpace(conflicted) != "" {
			return MergeResult{Conflict: true, Conflicted: splitLines(conflicted)}, nil
		}
		return MergeResult{}, fmt.Errorf("git merge %s: %w", target, err)
	}
	out, err := gitRun(worktreeDir, "rev-parse", "HEAD")
	if err != nil {
		return MergeResult{}, fmt.Errorf("rev-parse HEAD after merge: %w", err)
	}
	return MergeResult{Conflict: false, MergeCommit: strings.TrimSpace(out)}, nil
}

func (g *ExecGitOps) MergeAbort(worktreeDir string) error {
	_, err := gitRun(worktreeDir, "merge", "--abort")
	return err
}

func (g *ExecGitOps) StatusPorcelain(dir string) ([]string, error) {
	out, err := gitRun(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (g *ExecGitOps) RunTest(dir string, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("测试命令为空")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	outputDir, err := os.MkdirTemp("", "workflow-test-output-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(outputDir) // only this invocation's disposable output
	cmd.Env = append(os.Environ(), "WORKFLOW_TEST_OUTPUT_DIR="+outputDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("测试命令 %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (g *ExecGitOps) PushFastForward(remote, sha, branch string) error {
	// 推送精确 SHA 到目标分支：git 默认拒绝非快进（无任何 force 选项）。
	_, err := gitRun(g.Root, "push", remote, sha+":refs/heads/"+branch)
	return err
}
