// Command workflow-program validates Programs and exposes controlled collect/assemble operations.
//
// gate/plan are read-only. collect writes Registry; assemble pins a new local C0 branch.
// No command approves Owner decisions or pushes the parent.
//
// 用法：
//
//	workflow-program gate <assembler-start|program-cleaner|final-delivery> <program-id> [--root DIR]
//	workflow-program plan <program-id> [--root DIR]
//
// exit code：
//   - 0：PASS；
//   - 1：Gate / 计划校验 FAIL；
//   - 2：输入 / 运行级错误。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"workflow-v2-engine/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage(w *os.File) {
	fmt.Fprintln(w, "usage: workflow-program <gate|plan|collect|assemble> ... [--root DIR]")
	fmt.Fprintln(w, "  workflow-program gate <assembler-start|program-cleaner|final-delivery> <program-id> [--root DIR]")
	fmt.Fprintln(w, "  workflow-program plan <program-id> [--root DIR]")
	fmt.Fprintln(w, "  workflow-program collect <program-id> --task ID --expected-revision N [--root DIR]")
	fmt.Fprintln(w, "  workflow-program assemble <program-id> --source-branch NEW [--root DIR] -- COMMAND [ARG...]")
}

func run(args []string) int {
	var root string
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i:]...)
			break
		}
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
	if root == "" {
		root = findRoot()
	}

	switch positional[0] {
	case "collect", "assemble":
		return runWrite(root, positional[0], positional[1:])
	case "gate":
		return runGate(root, positional[1:])
	case "plan":
		return runPlan(root, positional[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: 未知子命令 %q\n", positional[0])
		usage(os.Stderr)
		return 2
	}
}

func newProgramValidator(root, programID string) *workflow.ProgramValidator {
	return &workflow.ProgramValidator{
		Root:          root,
		Git:           &workflow.ExecGit{Root: root},
		Store:         &workflow.RegistryStore{Root: root},
		LoadTaskState: workflow.ProgramTaskStateLoader(root, programID),
	}
}

func runGate(root string, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: workflow-program gate <assembler-start|program-cleaner|final-delivery> <program-id> [--root DIR]")
		return 2
	}
	gate := args[0]
	programID := args[1]
	switch gate {
	case "assembler-start", "program-cleaner", "final-delivery":
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 program gate %q\n", gate)
		return 2
	}
	pv := newProgramValidator(root, programID)
	var res workflow.Result
	switch gate {
	case "assembler-start":
		res = pv.GateAssemblerStart(programID)
	case "program-cleaner":
		res = pv.GateProgramCleaner(programID)
	case "final-delivery":
		res = pv.GateFinalDelivery(programID)
	}
	return printResult("program gate="+gate, res)
}

func runPlan(root string, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-program plan <program-id> [--root DIR]")
		return 2
	}
	programID := args[0]
	pv := newProgramValidator(root, programID)
	program, err := pv.LoadProgramState(programID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR    plan %s\n", err)
		return 2
	}
	asm := &workflow.Assembler{
		Store:         &workflow.RegistryStore{Root: root},
		Git:           &workflow.ExecGit{Root: root},
		LoadTaskState: workflow.ProgramTaskStateLoader(root, programID),
	}
	plan, res := asm.Plan(program)
	if res.Status != workflow.StatusPass {
		return printResult("program plan", res)
	}
	fmt.Printf("program_id: %s\n", plan.ProgramID)
	fmt.Printf("parent_branch: %s\n", plan.ParentBranch)
	fmt.Printf("base_commit: %s\n", plan.BaseCommit)
	for i, b := range plan.Batches {
		fmt.Printf("batch_%d:\n", i+1)
		for _, id := range b.Tasks {
			fmt.Printf("  - task=%s sha=%s\n", id, b.SHAs[id])
		}
	}
	return 0
}

func printResult(prefix string, res workflow.Result) int {
	switch res.Status {
	case workflow.StatusPass:
		fmt.Printf("PASS     %s\n", prefix)
		return 0
	case workflow.StatusFail:
		for _, iss := range res.Issues {
			fmt.Printf("FAIL     %s %s\n", prefix, iss.String())
		}
		return 1
	case workflow.StatusError:
		fmt.Printf("ERROR    %s %s\n", prefix, res.Error)
		return 2
	default:
		fmt.Printf("ERROR    %s unknown status %q\n", prefix, res.Status)
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

func runWrite(root, mode string, args []string) int {
	if len(args) < 1 || !workflow.ValidScopeID(args[0]) {
		fmt.Fprintln(os.Stderr, "valid program ID required")
		return 2
	}
	id := args[0]
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	task := fs.String("task", "", "planned task ID")
	revision := fs.Int("expected-revision", -1, "exact Registry revision")
	branch := fs.String("source-branch", "", "new local C0 branch")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var value any
	var err error
	if mode == "collect" {
		if *task == "" || *revision < 0 || *branch != "" || fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "collect requires --task ID --expected-revision N only")
			return 2
		}
		value, err = workflow.CollectProgram(root, id, *task, *revision)
	} else {
		if *branch == "" || fs.NArg() == 0 || *task != "" || *revision != -1 {
			fmt.Fprintln(os.Stderr, "assemble requires --source-branch NEW -- COMMAND [ARG...]")
			return 2
		}
		value, err = workflow.AssembleProgram(root, id, *branch, fs.Args())
	}
	result := map[string]any{"result": value, "status": "PASS"}
	code := 0
	if err != nil {
		result["status"] = "ERROR"
		result["error"] = err.Error()
		code = 2
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e := enc.Encode(result); e != nil {
		return 2
	}
	return code
}
