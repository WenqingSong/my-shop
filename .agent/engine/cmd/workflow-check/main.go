// Command workflow-check 是 Agent Workflow V2 的独立只读 Gate 校验器。
//
// 它与宿主业务运行二进制分离，只读校验 .agent/tasks/<task>/state.yaml，
// 不修改任何 Workflow Artifact、Registry，不承担 Orchestrator 职责，不代理任何 git 写操作。
//
// 用法：
//
//	workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready> <task> [--root DIR]
//	workflow-check route <task> [--allow-legacy] [--root DIR]
//	workflow-check version [--json]
//
// route 子命令是路线事实自检（R-1 ~ R-4），不是 Gate：它不产生 Handoff 授权，
// 也不改变任何 Gate 语义。默认要求 route 事实存在；历史任务需要显式 --allow-legacy。
//
// exit code：
//   - 0：PASS；
//   - 1：invariant / gate 不满足；
//   - 2：validator / 输入 / 运行级错误（含对非 V2 task 调用 gate）。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"workflow-v2-engine/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

var validGates = map[string]bool{
	"coder-start":      true,
	"cleaner-start":    true,
	"owner-gate-start": true,
	"delivery-start":   true,
	"merge-ready":      true,
}

func run(args []string) int {
	var root string
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--root":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --root 缺少值")
				return 2
			}
			i++
			root = args[i]
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		default:
			positional = append(positional, a)
		}
	}
	// --root 允许相对路径；必须在这里归一化，否则后面用 filepath.Rel 比较相对 root
	// 与绝对任务路径会误判「不在仓库根内」。
	root = absRoot(root)

	// version 是自描述命令：不依赖 --root、不读取 .agent/workflow.yaml，
	// 也不参与任何 Gate 判定，因此必须在解析仓库根之前处理。
	if len(positional) >= 1 && positional[0] == "version" {
		return runVersion(positional[1:], os.Stdout, os.Stderr)
	}

	if len(positional) >= 1 && positional[0] == "route" {
		return runRoute(positional[1:], root)
	}

	if len(positional) < 1 || positional[0] != "gate" {
		fmt.Fprintln(os.Stderr, "usage: workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready> <task> [--root DIR]")
		fmt.Fprintln(os.Stderr, "       workflow-check route <task> [--allow-legacy] [--root DIR]")
		return 2
	}
	if len(positional) < 3 {
		fmt.Fprintln(os.Stderr, "error: 缺少 gate 名或 task")
		return 2
	}
	gateName := positional[1]
	taskArg := positional[2]
	if !validGates[gateName] {
		fmt.Fprintf(os.Stderr, "error: 未知 gate %q\n", gateName)
		return 2
	}

	if root == "" {
		root = findRoot()
	}

	cfg, err := workflow.LoadConfig(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	git := &workflow.ExecGit{Root: root}
	v := &workflow.Validator{Root: root, Git: git, Config: cfg}

	taskDir, err := resolveTaskDir(root, taskArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	var res workflow.Result
	switch gateName {
	case "coder-start":
		res = v.GateCoderStart(taskDir)
	case "cleaner-start":
		res = v.GateCleanerStart(taskDir)
	case "owner-gate-start":
		res = v.GateOwnerGateStart(taskDir)
	case "delivery-start":
		res = v.GateDeliveryStart(taskDir)
	case "merge-ready":
		res = v.GateMergeReady(taskDir)
	}
	return printResult(gateName, res)
}

func printResult(gateName string, res workflow.Result) int {
	switch res.Status {
	case workflow.StatusPass:
		fmt.Printf("PASS     gate=%s\n", gateName)
		return 0
	case workflow.StatusFail:
		for _, iss := range res.Issues {
			fmt.Printf("FAIL     gate=%s %s\n", gateName, iss.String())
		}
		return 1
	case workflow.StatusError:
		fmt.Printf("ERROR    gate=%s %s\n", gateName, res.Error)
		return 2
	default:
		fmt.Printf("ERROR    gate=%s unknown status %q\n", gateName, res.Status)
		return 2
	}
}

// runRoute 处理 `workflow-check route <task> [--allow-legacy] [--root DIR]`。
//
// route 自检只依赖 state.yaml / task.md 与只读 Git 事实，不读取 .agent/workflow.yaml
// （Registry 与 integration branch 与路线事实无关），因此 Config 留空。
func runRoute(args []string, root string) int {
	allowLegacy := false
	var taskArg string
	for _, a := range args {
		switch {
		case a == "--allow-legacy":
			allowLegacy = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "error: route 不支持参数 %q\n", a)
			fmt.Fprintln(os.Stderr, "usage: workflow-check route <task> [--allow-legacy] [--root DIR]")
			return 2
		case taskArg == "":
			taskArg = a
		default:
			fmt.Fprintln(os.Stderr, "error: route 只接受一个 task 参数")
			return 2
		}
	}
	if taskArg == "" {
		fmt.Fprintln(os.Stderr, "usage: workflow-check route <task> [--allow-legacy] [--root DIR]")
		return 2
	}
	if root == "" {
		root = findRoot()
	}
	root = absRoot(root)
	git := &workflow.ExecGit{Root: root}
	v := &workflow.Validator{Root: root, Git: git}

	taskDir, err := resolveTaskDir(root, taskArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	return printRouteReport(taskDir, v.CheckRoute(taskDir, !allowLegacy))
}

func printRouteReport(taskDir string, rep workflow.RouteReport) int {
	task := filepath.Base(taskDir)
	switch rep.Result.Status {
	case workflow.StatusPass:
		if rep.Legacy {
			fmt.Printf("ROUTE_LEGACY task=%s route=<absent>\n", task)
		} else {
			fmt.Printf("ROUTE_OK     task=%s route=%s\n", task, rep.Route)
		}
		return 0
	case workflow.StatusFail:
		for _, iss := range rep.Result.Issues {
			fmt.Printf("ROUTE_FAIL   %s\n", iss.String())
		}
		return 1
	case workflow.StatusError:
		fmt.Printf("ROUTE_ERROR  task=%s %s\n", task, rep.Result.Error)
		return 2
	default:
		fmt.Printf("ROUTE_ERROR  task=%s unknown status %q\n", task, rep.Result.Status)
		return 2
	}
}

// absRoot 把 --root 归一化为绝对路径；空值保持为空（由调用方解析仓库根）。
// 相对路径按当前工作目录解析，与 Git 自身的行为一致。
func absRoot(root string) string {
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
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

// resolveTaskDir 把位置参数解析为相对仓库根的任务目录；若指向 state.yaml 文件则取其父目录。
// 相对路径按仓库根解析（而非当前工作目录）。
func resolveTaskDir(root, a string) (string, error) {
	if !filepath.IsAbs(a) {
		a = filepath.Join(root, a)
	}
	abs, err := filepath.Abs(a)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("任务路径 %s 不在仓库根 %s 内", a, root)
	}
	return rel, nil
}
