package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Git 抽象 Review Target 实质变化检测与 Registry 权威读取所需的只读 git 操作。
// 通过接口抽象便于在测试中用 fixture 临时仓库或自定义实现注入。
type Git interface {
	// DiffNameOnly 返回 base 到工作区（含已提交与未提交、不含未跟踪文件）的变更相对路径。
	DiffNameOnly(base string) ([]string, error)
	// UntrackedFiles 返回工作区未跟踪文件（相对路径，已排除 .gitignore）。
	UntrackedFiles() ([]string, error)
	// ShowFile 返回 ref 上 path 的内容；文件不存在时返回 os.ErrNotExist。
	ShowFile(ref, path string) (string, error)
	// PathExistsAt 报告 path 在 ref 上是否存在。
	PathExistsAt(ref, path string) (bool, error)
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
