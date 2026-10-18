// Command workflow-check 是 Agent Workflow V2 的独立只读 Gate 校验器。
//
// 它与宿主业务运行二进制分离，只读校验 .agent/tasks/<task>/state.yaml，
// 不修改任何 Workflow Artifact、Registry，不承担 Orchestrator 职责，不代理任何 git 写操作。
//
// 用法：
//
//	workflow-check config [--root DIR]
//	workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready> <task> [--root DIR]
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
	"coder-start":                 true,
	"cleaner-start":               true,
	"owner-gate-start":            true,
	"delivery-start":              true,
	"merge-ready":                 true,
	"handoff-prepared":            true,
	"handoff-entry":               true,
	"discovery-start":             true,
	"discovery-decision":          true,
	"discovery-handoff":           true,
	"taskbuilder-init":            true,
	"design-architect-start":      true,
	"design-architecture-review":  true,
	"design-docsspecwriter-start": true,
	"design-cleaner-start":        true,
	"design-owner-gate-start":     true,
	"design-final-approval":       true,
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

	if len(positional) == 1 && positional[0] == "config" {
		if root == "" {
			root = findRoot()
		}
		if _, err := workflow.LoadConfig(root); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR config:", err)
			return 2
		}
		fmt.Println("PASS config: workflow.yaml schema/registry paths valid (not Owner approval)")
		return 0
	}
	if len(positional) < 1 || positional[0] != "gate" {
		fmt.Fprintln(os.Stderr, "usage: workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready|handoff-prepared|handoff-entry|discovery-start|discovery-decision|discovery-handoff|taskbuilder-init|design-architect-start|design-architecture-review|design-docsspecwriter-start|design-cleaner-start|design-owner-gate-start|design-final-approval> <task|intake> [--root DIR]")
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
	case "handoff-prepared":
		res = v.GateHandoffPrepared(taskDir)
	case "handoff-entry":
		res = v.GateHandoffEntry(taskDir)
	case "discovery-start":
		res = v.GateDiscoveryStart(taskDir)
	case "discovery-decision":
		res = v.GateDiscoveryDecision(taskDir)
	case "discovery-handoff":
		res = v.GateDiscoveryHandoff(taskDir)
	case "taskbuilder-init":
		res = v.GateTaskBuilderInit(taskDir)
	case "design-architect-start":
		res = v.GateDesignArchitectStart(taskDir)
	case "design-architecture-review":
		res = v.GateDesignArchitectureReview(taskDir)
	case "design-docsspecwriter-start":
		res = v.GateDesignDocsSpecWriterStart(taskDir)
	case "design-cleaner-start":
		res = v.GateDesignCleanerStart(taskDir)
	case "design-owner-gate-start":
		res = v.GateDesignOwnerGateStart(taskDir)
	case "design-final-approval":
		res = v.GateDesignFinalApproval(taskDir)
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
