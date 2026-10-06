package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Git 抽象 Workflow V2 Gate 所需的只读 git 操作。
// 通过接口抽象便于在测试中用 fixture 临时仓库或自定义实现注入。
// 该接口不提供任何 git 写操作；Agent 的 git 写约束属于协议/Prompt 层，
// shared develop 的机器级保护由外部 branch protection 承担。
type Git interface {
	// DiffNameOnly 返回 base 到工作区（含已提交与未提交、不含未跟踪文件）的变更相对路径。
	DiffNameOnly(base string) ([]string, error)
	// DiffNameOnlyRange 返回 from..to 已提交区间内变更的相对路径。
	DiffNameOnlyRange(from, to string) ([]string, error)
	// UntrackedFiles 返回工作区未跟踪文件（相对路径，已排除 .gitignore）。
	UntrackedFiles() ([]string, error)
	// ShowFile 返回 ref 上 path 的内容；文件不存在时返回 os.ErrNotExist。
	ShowFile(ref, path string) (string, error)
	// PathExistsAt 报告 path 在 ref 上是否存在。
	PathExistsAt(ref, path string) (bool, error)
	// RevParse 返回 ref 解析为 commit 后的完整 SHA。
	RevParse(ref string) (string, error)
	// CommitExists 报告 sha 是否为仓库中真实存在的 commit 对象。
	CommitExists(sha string) (bool, error)
	// IsAncestor 报告 ancestor 是否为 desc 的祖先 commit。
	// 正常 false（非祖先）与 git runtime error（如非法 ref）必须区分。
	IsAncestor(ancestor, desc string) (bool, error)
	// StatusPorcelain 返回 git status --porcelain 的输出行；空表示 working tree clean。
	StatusPorcelain() ([]string, error)
	// CurrentBranch 返回当前分支名；detached HEAD 时返回 "HEAD"。
	CurrentBranch() (string, error)
}

// ExecGit 是基于 os/exec 调用 git 的只读实现，工作在指定仓库根目录。
type ExecGit struct {
	Root string
}

func (g *ExecGit) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func (g *ExecGit) DiffNameOnly(base string) ([]string, error) {
	out, err := g.run("diff", "--name-only", base)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (g *ExecGit) DiffNameOnlyRange(from, to string) ([]string, error) {
	out, err := g.run("diff", "--name-only", from+".."+to)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (g *ExecGit) UntrackedFiles() ([]string, error) {
	out, err := g.run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (g *ExecGit) ShowFile(ref, path string) (string, error) {
	out, err := g.run("show", ref+":"+path)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk") {
			return "", os.ErrNotExist
		}
		return "", err
	}
	return out, nil
}

func (g *ExecGit) PathExistsAt(ref, path string) (bool, error) {
	cmd := exec.Command("git", "cat-file", "-e", ref+":"+path)
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	err := cmd.Run()
	if err != nil {
		// 对象不存在（exit 128）视为路径不存在；其它错误向上抛。
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 128 {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file -e %s:%s: %w", ref, path, err)
	}
	return true, nil
}

func (g *ExecGit) RevParse(ref string) (string, error) {
	out, err := g.run("rev-parse", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g *ExecGit) CommitExists(sha string) (bool, error) {
	cmd := exec.Command("git", "cat-file", "-e", sha+"^{commit}")
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	err := cmd.Run()
	if err != nil {
		// 对象不存在（exit 128）视为「非真实 commit」；其它错误向上抛。
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 128 {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file -e %s^{commit}: %w", sha, err)
	}
	return true, nil
}

func (g *ExecGit) IsAncestor(ancestor, desc string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, desc)
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			// exit 1 = 非祖先（正常 false）；其它（如 128 非法 ref）视为 runtime error。
			if ee.ExitCode() == 1 {
				return false, nil
			}
			return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s", ancestor, desc, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, desc, err)
	}
	return true, nil
}

func (g *ExecGit) StatusPorcelain() ([]string, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (g *ExecGit) CurrentBranch() (string, error) {
	out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
