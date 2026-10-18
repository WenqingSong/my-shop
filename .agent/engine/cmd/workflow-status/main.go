// Command workflow-status 是 Workflow V4 的只读诊断与恢复前提检查工具（Task 04）。
//
// 它只读 state.yaml、事件日志与 Git 事实，绝不修改任何 Workflow Artifact，
// 不自动清除 blocked.active，不启动任何 Agent，不代理任何 git 写操作。
//
// 用法：
//
//	workflow-status inspect <task-id> [--root DIR]
//	workflow-status recovery-check <task-id> [--root DIR]
//
// exit code：
//   - inspect：0 正常产出诊断；2 输入/运行级错误（task 不存在、state.yaml 无法读取）。
//   - recovery-check：0 可继续（blocked 未激活且唯一入口 Gate PASS）；
//     1 不可继续（blocked 激活、Gate FAIL 或无法唯一确定下一 Gate）；
//     2 输入/运行级错误。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"workflow-v2-engine/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage(w *os.File) {
	fmt.Fprintln(w, "usage: workflow-status <inspect|recovery-check|index> <task-id> [--root DIR]")
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

	if len(positional) < 1 {
		usage(os.Stderr)
		return 2
	}

	switch positional[0] {
	case "index":
		return runIndex(root, positional[1:])
	case "inspect":
		return runInspect(root, positional[1:])
	case "recovery-check":
		return runRecoveryCheck(root, positional[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: 未知子命令 %q\n", positional[0])
		usage(os.Stderr)
		return 2
	}
}

// newValidator 构造 Validator。机器配置缺失时对 inspect 非致命，对 recovery-check 致命。
func newValidator(root string) (*workflow.Validator, *workflow.Config, error) {
	cfg, err := workflow.LoadConfig(root)
	if err != nil {
		cfg = nil
	}
	return &workflow.Validator{Root: root, Git: &workflow.ExecGit{Root: root}, Config: cfg}, cfg, err
}

func runInspect(root string, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-status inspect <task-id> [--root DIR]")
		return 2
	}
	if root == "" {
		root = findRoot()
	}
	taskDir, err := resolveTaskDir(root, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	v, _, _ := newValidator(root)
	d, err := workflow.Inspect(v, taskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	printDiagnosis(d)
	return 0
}

func runRecoveryCheck(root string, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-status recovery-check <task-id> [--root DIR]")
		return 2
	}
	if root == "" {
		root = findRoot()
	}
	taskDir, err := resolveTaskDir(root, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	v, _, cerr := newValidator(root)
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cerr)
		return 2
	}

	rep, err := workflow.CheckRecovery(v, taskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	printRecovery(rep)
	if rep.CanContinue {
		return 0
	}
	return 1
}

func printDiagnosis(d workflow.Diagnosis) {
	fmt.Printf("task_id: %s\n", d.TaskID)
	if d.SchemaKnown {
		fmt.Printf("schema_version: %d\n", d.SchemaVersion)
	} else {
		fmt.Printf("schema_version: UNKNOWN\n")
		fmt.Printf("state_error: %s\n", d.StateError)
	}

	if d.SchemaKnown {
		if d.BlockedActive {
			fmt.Printf("blocked:\n")
			fmt.Printf("  active: true\n")
			fmt.Printf("  by: %s\n", orEmpty(d.BlockedBy))
			fmt.Printf("  reason: %s\n", d.BlockedReason)
		} else {
			fmt.Printf("blocked:\n")
			fmt.Printf("  active: false\n")
		}
	}

	fmt.Printf("facts:\n")
	for _, f := range d.Facts {
		fmt.Printf("  - %s\n", f)
	}

	fmt.Printf("events (%d):\n", len(d.Events))
	for _, ev := range d.Events {
		fmt.Printf("  - %s [%s] %s scope=%s:%s sha=%s reason=%s\n",
			ev.Timestamp.UTC().Format(time.RFC3339Nano), ev.Role, ev.Event, ev.ScopeKind, ev.ScopeID, orEmpty(ev.SHA), ev.Reason)
	}

	if len(d.EventErrors) > 0 {
		fmt.Printf("event_errors (%d):\n", len(d.EventErrors))
		for _, e := range d.EventErrors {
			fmt.Printf("  - %s\n", e.Error())
		}
	}

	if len(d.Anomalies) > 0 {
		fmt.Printf("anomalies (%d):\n", len(d.Anomalies))
		for _, a := range d.Anomalies {
			fmt.Printf("  - %s: %s\n", a.Kind, a.Detail)
		}
	}

	fmt.Printf("candidate_gates:\n")
	if len(d.CandidateGates) == 0 {
		fmt.Printf("  - UNKNOWN\n")
	} else {
		for _, g := range d.CandidateGates {
			fmt.Printf("  - %s\n", g)
		}
	}
	fmt.Printf("next_gate_certain: %t\n", d.NextGateCertain)
}

func printRecovery(r workflow.RecoveryReport) {
	fmt.Printf("task_id: %s\n", r.TaskID)
	fmt.Printf("blocked_active: %t\n", r.BlockedActive)
	if r.BlockedActive {
		fmt.Printf("blocked_by: %s\n", orEmpty(r.BlockedBy))
		fmt.Printf("blocked_reason: %s\n", r.BlockedReason)
	}

	if len(r.GateChecks) > 0 {
		fmt.Printf("gate_checks:\n")
		for _, gc := range r.GateChecks {
			fmt.Printf("  - gate=%s status=%s", gc.Gate, gc.Result.Status)
			if gc.Result.Status == workflow.StatusFail {
				for _, iss := range gc.Result.Issues {
					fmt.Printf(" %s", iss.String())
				}
			}
			if gc.Result.Status == workflow.StatusError {
				fmt.Printf(" error=%s", gc.Result.Error)
			}
			fmt.Println()
		}
	}

	if r.CanContinue {
		fmt.Printf("can_continue: true\n")
		fmt.Printf("next_gate: %s\n", r.NextGate)
	} else {
		fmt.Printf("can_continue: false\n")
		if r.BlockedActive {
			who := r.BlockedBy
			if who == "" {
				who = "UNKNOWN"
			}
			fmt.Printf("required: 需要角色 %s 或 Owner 消除阻塞后重新运行入口 Gate\n", who)
		} else if r.UnknownReason != "" {
			fmt.Printf("next_gate: UNKNOWN\n")
			fmt.Printf("unknown_reason: %s\n", r.UnknownReason)
		} else {
			fmt.Printf("next_gate: %s\n", r.NextGate)
			fmt.Printf("gate_status: FAIL\n")
		}
	}
}

func orEmpty(s string) string {
	if s == "" {
		return ""
	}
	return s
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

// resolveTaskDir 把位置参数解析为相对仓库根的任务目录。
// 支持直接路径（含 .agent/tasks/<id>）与 task-id 简写（自动补全 .agent/tasks/<id>）。
// 相对路径按仓库根解析（而非当前工作目录）。
func resolveTaskDir(root, a string) (string, error) {
	candidates := []string{a}
	if !filepath.IsAbs(a) {
		candidates = append(candidates, filepath.Join(".agent", "tasks", a))
	}
	for _, c := range candidates {
		if rel, err := normalizeTaskRel(root, c); err == nil {
			return rel, nil
		}
	}
	return "", fmt.Errorf("未找到任务目录 %q（尝试了 %q 与 .agent/tasks/%s）", a, a, a)
}

func normalizeTaskRel(root, a string) (string, error) {
	if !filepath.IsAbs(a) {
		a = filepath.Join(root, a)
	}
	abs, err := filepath.Abs(a)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	} else if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", fmt.Errorf("任务路径 %s 不在仓库根 %s 内", a, root)
	}
	return rel, nil
}

// runIndex emits derived data to stdout; callers may redirect it to ignored files.
func runIndex(root string, args []string) int {
	format := "tasks"
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--format" {
			fmt.Fprintln(os.Stderr, "usage: workflow-status index [--format tasks|programs|meta] [--root DIR]")
			return 2
		}
		format = args[1]
	}
	if format != "tasks" && format != "programs" && format != "meta" {
		fmt.Fprintln(os.Stderr, "error: unknown index format", format)
		return 2
	}
	if root == "" {
		root = findRoot()
	}
	snapshot, err := workflow.BuildIndex(root, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	var output string
	switch format {
	case "tasks":
		output = snapshot.TSV()
	case "programs":
		output = snapshot.ProgramsMarkdown()
	case "meta":
		output = snapshot.Metadata()
	}
	if _, err := fmt.Fprint(os.Stdout, output); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	return 0
}
