// Command workflow-check 是 Agent Workflow 状态机的独立只读校验器。
//
// 它与业务运行二进制 my-shop 分离，只读校验 .agent/tasks/*/state.yaml，
// 不修改任何 Workflow Artifact、Registry，不承担 Orchestrator 职责，不进行状态转换。
//
// exit code：
//   - 0：全部合法；
//   - 1：Workflow / invariant / gate 不合法；
//   - 2：Validator 自身无法执行（输入、环境或解析级错误）。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"cnb.cool/go-cloud-devops/my-shop/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("workflow-check", flag.ContinueOnError)
	root := fs.String("root", "", "仓库根目录（默认 git rev-parse --show-toplevel）")
	developRef := fs.String("develop-ref", "develop", "shared develop 引用，资源权威来源（如 develop / origin/develop）")
	cutover := fs.String("cutover", "", "State Machine V1 生效 commit，用于 Cutover Rule")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *root == "" {
		*root = findRoot()
	}

	git := &workflow.ExecGit{Root: *root}
	v := &workflow.Validator{Root: *root, Git: git, DevelopRef: *developRef, Cutover: *cutover}

	tasks := collectTasks(*root, fs.Args())
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "error: 未找到可校验的任务（.agent/tasks/* 为空或路径无效）")
		return 2
	}

	exitCode := 0
	for _, t := range tasks {
		res, err := v.ValidateTask(t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			if exitCode == 0 {
				exitCode = 2
			}
			continue
		}
		switch res.Status {
		case workflow.StatusPass:
			fmt.Printf("PASS     task=%s\n", res.Task)
		case workflow.StatusSkipped:
			fmt.Printf("SKIPPED  task=%s (%s)\n", res.Task, res.SkipReason)
		case workflow.StatusFail:
			for _, iss := range res.Issues {
				fmt.Printf("FAIL     %s\n", iss.String())
			}
			if exitCode != 2 {
				exitCode = 1
			}
		}
	}
	return exitCode
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

// collectTasks 收集要校验的任务目录（相对 root 的路径）。
// 未提供位置参数时扫描 .agent/tasks/* 下所有子目录。
func collectTasks(root string, args []string) []string {
	var tasks []string
	if len(args) == 0 {
		entries, err := os.ReadDir(filepath.Join(root, ".agent", "tasks"))
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.IsDir() {
				tasks = append(tasks, filepath.Join(".agent", "tasks", e.Name()))
			}
		}
		sort.Strings(tasks)
		return tasks
	}

	for _, a := range args {
		dir := resolveTaskDir(a)
		rel, err := filepath.Rel(root, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			fmt.Fprintf(os.Stderr, "error: 任务路径 %s 不在仓库根 %s 内\n", a, root)
			continue
		}
		tasks = append(tasks, rel)
	}
	sort.Strings(tasks)
	return tasks
}

// resolveTaskDir 把位置参数解析为任务目录：若指向 state.yaml 文件则取其父目录。
func resolveTaskDir(a string) string {
	abs, err := filepath.Abs(a)
	if err != nil {
		return a
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return filepath.Dir(abs)
	}
	return abs
}
