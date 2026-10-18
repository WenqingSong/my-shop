// Command workflow-git 是 Workflow V4 的独立受控 Git 写工具（Task 07）。
//
// 它与只读的 workflow-check 分离：workflow-check 只读，workflow-git 负责 Task 初始化所需的
// 最小受控 Git 写操作。它只创建独立 Task 分支与独立 Worktree，绝不直接更新父分支，
// 也绝不覆盖已存在的分支 / Worktree。
//
// 用法：
//
//	workflow-git verify-base     --parent <branch> --base <sha> [--root DIR]
//	workflow-git branch-exists   <branch> [--root DIR]
//	workflow-git worktree-exists <branch> [--root DIR]
//	workflow-git create-branch   <branch> --parent <branch> --base <sha> [--root DIR]
//	workflow-git create-worktree <path> <branch> [--root DIR]
//
// exit code：
//   - 0：成功（verify 通过 / 存在 / 已创建）；
//   - 1：安全拒绝（基线非法 / 分支或 Worktree 已存在 / 不满足前置条件）；
//   - 2：用法或运行级错误。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  workflow-git verify-base     --parent <branch> --base <sha> [--root DIR]
  workflow-git branch-exists   <branch> [--root DIR]
  workflow-git worktree-exists <branch> [--root DIR]
  workflow-git create-branch   <branch> --parent <branch> --base <sha> [--root DIR]
  workflow-git create-worktree <path> <branch> [--root DIR]

exit code: 0 成功；1 安全拒绝；2 用法或运行级错误
`)
}

// args 是解析后的命令行参数：--key value / --key=value 归入 flags，其余归入 positional。
type args struct {
	flags      map[string]string
	positional []string
}

// parseArgs 手工解析参数，允许 flag 与位置参数任意顺序出现（避免 Go flag 包在首个位置参数后停解析）。
func parseArgs(list []string) args {
	a := args{flags: map[string]string{}}
	for i := 0; i < len(list); i++ {
		s := list[i]
		if strings.HasPrefix(s, "--") {
			key := strings.TrimPrefix(s, "--")
			if eq := strings.Index(key, "="); eq >= 0 {
				a.flags[key[:eq]] = key[eq+1:]
				continue
			}
			if i+1 < len(list) {
				a.flags[key] = list[i+1]
				i++
			} else {
				a.flags[key] = ""
			}
			continue
		}
		a.positional = append(a.positional, s)
	}
	return a
}

// gitRunner 在指定仓库根执行 git 命令。
type gitRunner struct {
	root string
}

// run 执行 git 命令，返回 stdout；命令失败时返回 error（含 stderr）。
func (g *gitRunner) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if g.root != "" {
		cmd.Dir = g.root
	}
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// exists 执行 git 命令，返回命令是否成功退出；exit 1 视为「不存在/否」，其它非 0 视为 error。
// 适用于 show-ref --verify、merge-base --is-ancestor 等「0=是 / 1=否」语义的命令。
func (g *gitRunner) exists(args ...string) (bool, error) {
	cmd := exec.Command("git", args...)
	if g.root != "" {
		cmd.Dir = g.root
	}
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	_, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ee.ExitCode() == 1 {
				return false, nil
			}
			return false, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return false, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return true, nil
}

// commitExists 报告 sha 是否为真实 commit（含 --quiet，非法/缺失对象都归为 false）。
func (g *gitRunner) commitExists(sha string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if g.root != "" {
		cmd.Dir = g.root
	}
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	_, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ee.ExitCode() == 1 {
				return false, nil
			}
			return false, fmt.Errorf("git rev-parse --verify %s^{commit}: %s", sha, strings.TrimSpace(string(ee.Stderr)))
		}
		return false, err
	}
	return true, nil
}

func run(argsList []string) int {
	if len(argsList) == 0 {
		usage()
		return 2
	}
	switch argsList[0] {
	case "help", "-h", "--help":
		usage()
		return 0
	case "verify-base":
		return runVerifyBase(argsList[1:])
	case "branch-exists":
		return runBranchExists(argsList[1:])
	case "worktree-exists":
		return runWorktreeExists(argsList[1:])
	case "create-branch":
		return runCreateBranch(argsList[1:])
	case "create-worktree":
		return runCreateWorktree(argsList[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: 未知子命令 %q\n", argsList[0])
		usage()
		return 2
	}
}

// findRoot 通过 git 定位仓库根，失败则回退到当前目录。
func findRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return wd
}

// resolveRoot 解析 --root，缺省通过 git 定位。
func resolveRoot(flags map[string]string) string {
	if r := flags["root"]; r != "" {
		return r
	}
	return findRoot()
}

// verifyParentAndBase 是基线核验的公共实现：父分支存在、base 真实、base 是父分支祖先。
func verifyParentAndBase(g *gitRunner, parent, base string) (int, error) {
	if ok, err := branchExists(g, parent); err != nil {
		return 0, err
	} else if !ok {
		return 1, fmt.Errorf("refuse: 父分支 %q 不存在", parent)
	}
	if ok, err := g.commitExists(base); err != nil {
		return 0, err
	} else if !ok {
		return 1, fmt.Errorf("refuse: base %q 不是真实 commit", base)
	}
	if ok, err := g.exists("merge-base", "--is-ancestor", base, parent); err != nil {
		return 0, err
	} else if !ok {
		return 1, fmt.Errorf("refuse: base %q 不是父分支 %q 的祖先", base, parent)
	}
	return 0, nil
}

// runVerifyBase 核验父分支存在、base 是真实 commit，且 base 是父分支的祖先（基线有效）。
func runVerifyBase(list []string) int {
	a := parseArgs(list)
	parent, base := a.flags["parent"], a.flags["base"]
	if parent == "" || base == "" {
		fmt.Fprintln(os.Stderr, "error: verify-base 需要 --parent 与 --base")
		return 2
	}
	g := &gitRunner{root: resolveRoot(a.flags)}
	if code, err := verifyParentAndBase(g, parent, base); err != nil {
		if code == 1 {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	fmt.Printf("ok parent=%s base=%s\n", parent, base)
	return 0
}

// runBranchExists 报告分支是否存在（本地或 origin 远端）。
func runBranchExists(list []string) int {
	a := parseArgs(list)
	if len(a.positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-git branch-exists <branch> [--root DIR]")
		return 2
	}
	g := &gitRunner{root: resolveRoot(a.flags)}
	ok, err := branchExists(g, a.positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if ok {
		fmt.Printf("exists branch=%s\n", a.positional[0])
		return 0
	}
	fmt.Printf("missing branch=%s\n", a.positional[0])
	return 1
}

// runWorktreeExists 报告分支是否已 checkout 到某个 worktree（含主 worktree）。
func runWorktreeExists(list []string) int {
	a := parseArgs(list)
	if len(a.positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-git worktree-exists <branch> [--root DIR]")
		return 2
	}
	g := &gitRunner{root: resolveRoot(a.flags)}
	ok, err := worktreeExists(g, a.positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if ok {
		fmt.Printf("exists worktree branch=%s\n", a.positional[0])
		return 0
	}
	fmt.Printf("missing worktree branch=%s\n", a.positional[0])
	return 1
}

// runCreateBranch 从 base 创建独立 Task 分支；已存在或基线非法则安全拒绝。
func runCreateBranch(list []string) int {
	a := parseArgs(list)
	if len(a.positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-git create-branch <branch> --parent <branch> --base <sha> [--root DIR]")
		return 2
	}
	branch := a.positional[0]
	parent, base := a.flags["parent"], a.flags["base"]
	if parent == "" || base == "" {
		fmt.Fprintln(os.Stderr, "error: create-branch 需要 --parent 与 --base")
		return 2
	}
	g := &gitRunner{root: resolveRoot(a.flags)}

	// 1. 分支已存在不得覆盖。
	if ok, err := branchExists(g, branch); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	} else if ok {
		fmt.Fprintf(os.Stderr, "refuse: 分支 %q 已存在，不得覆盖\n", branch)
		return 1
	}
	// 2. 父分支 + base 基线有效。
	if code, err := verifyParentAndBase(g, parent, base); err != nil {
		if code == 1 {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	// 3. 创建分支（绑定父分支与 Base SHA）。
	if _, err := g.run("branch", branch, base); err != nil {
		fmt.Fprintf(os.Stderr, "error: 创建分支 %q: %v\n", branch, err)
		return 2
	}
	fmt.Printf("created branch=%s parent=%s base=%s\n", branch, parent, base)
	return 0
}

// runCreateWorktree 为已存在的分支创建独立 Worktree；路径已存在或分支已 checkout 则安全拒绝。
func runCreateWorktree(list []string) int {
	a := parseArgs(list)
	if len(a.positional) != 2 {
		fmt.Fprintln(os.Stderr, "usage: workflow-git create-worktree <path> <branch> [--root DIR]")
		return 2
	}
	worktreePath, branch := a.positional[0], a.positional[1]
	g := &gitRunner{root: resolveRoot(a.flags)}

	// 1. 分支必须已存在（不得隐式创建）。
	if ok, err := branchExists(g, branch); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	} else if !ok {
		fmt.Fprintf(os.Stderr, "refuse: 分支 %q 不存在，create-worktree 不会隐式创建分支\n", branch)
		return 1
	}
	// 2. 分支尚未 checkout 到任何 worktree（一个分支同一时刻只能一个 worktree）。
	if ok, err := worktreeExists(g, branch); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	} else if ok {
		fmt.Fprintf(os.Stderr, "refuse: 分支 %q 已 checkout 到某 worktree，不得重复创建\n", branch)
		return 1
	}
	// 3. Worktree 路径已存在不得覆盖。
	abs, err := filepath.Abs(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if _, err := os.Stat(abs); err == nil {
		fmt.Fprintf(os.Stderr, "refuse: worktree 路径 %q 已存在，不得覆盖\n", worktreePath)
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	// 4. 创建 worktree。
	if _, err := g.run("worktree", "add", abs, branch); err != nil {
		fmt.Fprintf(os.Stderr, "error: 创建 worktree %q: %v\n", worktreePath, err)
		return 2
	}
	fmt.Printf("created worktree path=%s branch=%s\n", abs, branch)
	return 0
}

// branchExists 报告 name 分支是否存在（本地 refs/heads 或远端 refs/remotes/origin）。
func branchExists(g *gitRunner, name string) (bool, error) {
	out, err := g.run("for-each-ref", "--format=%(refname)", "refs/heads/"+name, "refs/remotes/origin/"+name)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// worktreeExists 报告 branch 是否已 checkout 到某个 worktree（含主 worktree）。
func worktreeExists(g *gitRunner, branch string) (bool, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	want := "branch refs/heads/" + branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return true, nil
		}
	}
	return false, nil
}
