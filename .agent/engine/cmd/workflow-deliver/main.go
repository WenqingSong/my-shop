// Command workflow-deliver 是 Workflow V4 最终交付的受控执行入口（Finding Fix B）。
//
// plan 严格只读；verify task 隔离测试并记录 delivery 证据；execute 需显式授权：
//
//	workflow-deliver plan <task|program|design> <id> [--root DIR] [--source-branch B] [--test "cmd args..."]
//	workflow-deliver verify task <id> [--root DIR] [--test "cmd args..."]
//
//	workflow-deliver execute <task|program|design> <id> --source-sha SHA --parent-branch B [--root DIR] [--source-branch B] [--test "cmd args..."]
//
// 规则：
//   - plan 严格只读：只消费公开 Gate、读取 Evidence、核验来源分支与远端状态，绝不合并/推送；
//   - execute 不得被任何 Gate 自动调用，只允许人类 Owner 明确批准后触发；授权必须绑定
//     具体对象、授权 SHA（Task=实际来源 tip；DESIGN/Program=最终 Evidence）与目标父分支，
//     三者与只读计划不一致即拒绝（exit 1），不接受 --yes；
//   - 真正执行走 Merger.Integrate：隔离合并 + 真实测试 + 父分支未漂移 + fast-forward-only，
//     禁止 force push，完成后复核远端父分支 HEAD。
//
// 信任边界：本 CLI 无法在机器层证明调用者就是 Owner，Owner 身份由运行环境与 shared branch
// protection 承担；--source-sha / --parent-branch 是事实绑定，不是身份认证。
//
// exit code：0 PASS；1 交付/授权 FAIL；2 输入/运行级错误。
package main

import (
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
	fmt.Fprintln(w, "usage: workflow-deliver <plan|verify|execute> <task|program|design> <id> [flags]")
	fmt.Fprintln(w, "  plan:")
	fmt.Fprintln(w, "    workflow-deliver plan program <program-id> --source-branch B [--root DIR] [--test CMD]")
	fmt.Fprintln(w, "    workflow-deliver plan design <task-slug> [--root DIR] [--test CMD]")
	fmt.Fprintln(w, "    workflow-deliver plan task <task-id> [--root DIR] [--test CMD]")
	fmt.Fprintln(w, "    workflow-deliver verify task <task-id> [--root DIR] [--test CMD] (写 delivery 证据，不 commit/push)")
	fmt.Fprintln(w, "  execute (需显式授权 --source-sha 与 --parent-branch):")
	fmt.Fprintln(w, "    workflow-deliver execute program <program-id> --source-branch B --source-sha SHA --parent-branch B [--root DIR] [--test CMD]")
	fmt.Fprintln(w, "    workflow-deliver execute design <task-slug> --source-sha SHA --parent-branch B [--root DIR] [--test CMD]")
	fmt.Fprintln(w, "    workflow-deliver execute task <task-id> --source-sha SHA --parent-branch B [--root DIR] [--test CMD]")
}

type flags struct {
	root         string
	sourceBranch string
	sourceSHA    string
	parentBranch string
	testCmd      string
	positional   []string
}

func parseFlags(args []string) flags {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--root":
			if i+1 < len(args) {
				i++
				f.root = args[i]
			}
		case strings.HasPrefix(a, "--root="):
			f.root = strings.TrimPrefix(a, "--root=")
		case a == "--source-branch":
			if i+1 < len(args) {
				i++
				f.sourceBranch = args[i]
			}
		case strings.HasPrefix(a, "--source-branch="):
			f.sourceBranch = strings.TrimPrefix(a, "--source-branch=")
		case a == "--source-sha":
			if i+1 < len(args) {
				i++
				f.sourceSHA = args[i]
			}
		case strings.HasPrefix(a, "--source-sha="):
			f.sourceSHA = strings.TrimPrefix(a, "--source-sha=")
		case a == "--parent-branch":
			if i+1 < len(args) {
				i++
				f.parentBranch = args[i]
			}
		case strings.HasPrefix(a, "--parent-branch="):
			f.parentBranch = strings.TrimPrefix(a, "--parent-branch=")
		case a == "--test":
			if i+1 < len(args) {
				i++
				f.testCmd = args[i]
			}
		case strings.HasPrefix(a, "--test="):
			f.testCmd = strings.TrimPrefix(a, "--test=")
		default:
			f.positional = append(f.positional, a)
		}
	}
	return f
}

func run(args []string) int {
	if len(args) < 1 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "plan":
		return runPlan(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "execute":
		return runExecute(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: 未知子命令 %q\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

func resolveRoot(f flags) string {
	if f.root != "" {
		return f.root
	}
	return findRoot()
}

func testCommand(f flags) []string {
	if f.testCmd != "" {
		return strings.Fields(f.testCmd)
	}
	return []string{"go", "test", "./..."}
}

func newController(root string, test []string) *workflow.DeliveryController {
	cfg, _ := workflow.LoadConfig(root) // DESIGN Gate 可选；Program 交付不使用 Config。
	return &workflow.DeliveryController{
		Root:        root,
		Git:         &workflow.ExecGit{Root: root},
		Merger:      &workflow.Merger{Root: root, Git: &workflow.ExecGitOps{Root: root}},
		Config:      cfg,
		TestCommand: test,
	}
}

func runPlan(args []string) int {
	f := parseFlags(args)
	if len(f.positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: workflow-deliver plan <task|program|design> <id> [flags]")
		return 2
	}
	kind, id := f.positional[0], f.positional[1]
	root := resolveRoot(f)
	dc := newController(root, testCommand(f))

	switch kind {
	case "task":
		plan, res := dc.PlanTask(id)
		if res.Status != workflow.StatusPass {
			return printResult("plan task", res)
		}
		return printPlan(plan)
	case "program":
		plan, res := dc.PlanProgram(id, f.sourceBranch)
		if res.Status != workflow.StatusPass {
			return printResult("plan program", res)
		}
		return printPlan(plan)
	case "design":
		plan, res := dc.PlanDesign(id)
		if res.Status != workflow.StatusPass {
			return printResult("plan design", res)
		}
		return printPlan(plan)
	default:
		fmt.Fprintf(os.Stderr, "error: 未知交付对象 %q（期望 task|program|design）\n", kind)
		return 2
	}
}

func runVerify(args []string) int {
	f := parseFlags(args)
	if len(f.positional) != 2 || (f.positional[0] != "task" && f.positional[0] != "design") {
		fmt.Fprintln(os.Stderr, "usage: workflow-deliver verify <task|design> <task-id> [--root DIR] [--test CMD]")
		return 2
	}
	dc := newController(resolveRoot(f), testCommand(f))
	var rep workflow.MergeReport
	var res workflow.Result
	if f.positional[0] == "design" {
		rep, res = dc.VerifyDesign(f.positional[1])
	} else {
		rep, res = dc.VerifyTask(f.positional[1])
	}
	if rep.CleanupWarning != "" {
		fmt.Fprintln(os.Stderr, "warning:", rep.CleanupWarning)
	}
	code := printResult("verify "+f.positional[0], res)
	if code == 0 {
		fmt.Println("delivery.md 与 state.yaml 已写入；明确 stage 这两个文件，commit/push 后交 Collector（Program子Task）或使用适用的最终交付入口；父分支未更新。")
	}
	return code
}

func runExecute(args []string) int {
	f := parseFlags(args)
	if len(f.positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: workflow-deliver execute <task|program|design> <id> --source-sha SHA --parent-branch B [flags]")
		return 2
	}
	kind, id := f.positional[0], f.positional[1]
	if f.sourceSHA == "" || f.parentBranch == "" {
		fmt.Fprintln(os.Stderr, "error: execute 需要显式授权 --source-sha 与 --parent-branch（不接受 --yes）")
		return 2
	}
	root := resolveRoot(f)
	dc := newController(root, testCommand(f))
	auth := workflow.DeliveryAuthorization{SourceSHA: f.sourceSHA, ParentBranch: f.parentBranch}

	var rep workflow.MergeReport
	var res workflow.Result
	switch kind {
	case "task":
		rep, res = dc.ExecuteTask(id, auth)
	case "program":
		if f.sourceBranch == "" {
			fmt.Fprintln(os.Stderr, "error: program 交付需要 --source-branch")
			return 2
		}
		rep, res = dc.ExecuteProgram(id, f.sourceBranch, auth)
	case "design":
		rep, res = dc.ExecuteDesign(id, auth)
	default:
		fmt.Fprintf(os.Stderr, "error: 未知交付对象 %q（期望 task|program|design）\n", kind)
		return 2
	}

	if rep.CleanupWarning != "" {
		fmt.Fprintln(os.Stderr, "warning:", rep.CleanupWarning)
	}
	if res.Status != workflow.StatusPass {
		return printResult("execute "+kind, res)
	}
	printExecute(kind, id, rep)
	return 0
}

func printPlan(plan workflow.DeliveryPlan) int {
	fmt.Printf("PASS     plan kind=%s\n", plan.Kind)
	fmt.Printf("target:            %s\n", plan.TargetID)
	fmt.Printf("parent_branch:     %s\n", plan.ParentBranch)
	fmt.Printf("parent_base:       %s\n", plan.ParentBase)
	fmt.Printf("source_branch:     %s\n", plan.Source.Branch)
	fmt.Printf("source_sha:        %s\n", plan.Source.SHA)
	fmt.Printf("final_evidence:    %s\n", plan.FinalEvidenceSHA)
	fmt.Printf("test_command:      %s\n", strings.Join(plan.TestCommand, " "))
	fmt.Println("note: plan 为只读，未执行合并；目标 SHA 将在 execute 的隔离合并后确定。")
	return 0
}

func printExecute(kind, id string, rep workflow.MergeReport) {
	fmt.Printf("PASS     execute %s %s\n", kind, id)
	fmt.Printf("merged:            %t\n", rep.Merged)
	fmt.Printf("merge_commit:      %s\n", rep.MergeCommit)
	fmt.Printf("test_passed:       %t\n", rep.TestPassed)
	fmt.Printf("parent_updated:    %t\n", rep.ParentUpdated)
	fmt.Printf("parent_final_sha:  %s\n", rep.ParentFinalSHA)
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

func findRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return wd
}
