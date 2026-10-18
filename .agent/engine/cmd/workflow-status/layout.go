package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"workflow-v2-engine/internal/workflow"
)

// runLayout 实现项目本地 Task 目录分类扩展的只读查询与受控迁移。
//
// 上游 V4.3.0 未提供任务目录分类；本子命令是项目扩展，语义与边界见
// .agent/specs/TaskLayoutExtension.md。exit code 与其它 CLI 保持一致：
// 0 通过/成功、1 检测到不一致或被拒绝、2 输入或运行级错误。
func runLayout(root string, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: workflow-status layout <list|check|plan|migrate|rollback|resolve> [--root DIR]")
		return 2
	}
	sub := args[0]
	rest := args[1:]
	apply := false
	journal := ""
	format := "tsv"
	var positional []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--apply":
			apply = true
		case a == "--journal":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "error: --journal 缺少值")
				return 2
			}
			i++
			journal = rest[i]
		case strings.HasPrefix(a, "--journal="):
			journal = strings.TrimPrefix(a, "--journal=")
		case a == "--format":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "error: --format 缺少值")
				return 2
			}
			i++
			format = rest[i]
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		default:
			positional = append(positional, a)
		}
	}

	switch sub {
	case "list":
		return layoutList(root, format)
	case "check":
		return layoutCheck(root)
	case "plan":
		return layoutPlan(root)
	case "migrate":
		return layoutMigrate(root, apply)
	case "rollback":
		return layoutRollback(root, journal, apply)
	case "resolve":
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, "usage: workflow-status layout resolve <task-id|path> [--root DIR]")
			return 2
		}
		rel, err := workflow.ResolveTaskArg(root, positional[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		fmt.Println(rel)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 layout 子命令 %q\n", sub)
		return 2
	}
}

func layoutList(root, format string) int {
	m, err := workflow.LoadTaskLayout(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	dirs, err := workflow.ListTaskDirs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	basis := map[string]string{}
	for _, t := range m.Tasks {
		basis[t.ID] = t.Basis
	}
	switch format {
	case "tsv":
		fmt.Println("# DERIVED — 分类不是审批证据；权威状态见 State 与 Evidence")
		fmt.Println("task_id\tcategory\tbasis\tpath\tlegacy")
		for _, d := range dirs {
			cat := workflow.TaskCategoryOf(m, dirs, d.ID)
			b := basis[d.ID]
			if cat == "" {
				cat = "-"
			}
			if b == "" {
				b = "-"
			}
			legacy := "false"
			if d.Legacy {
				legacy = "true"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", d.ID, cat, b, d.Rel, legacy)
		}
	case "ids":
		ids := make([]string, 0, len(dirs))
		for _, d := range dirs {
			ids = append(ids, d.ID)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Println(id)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 format %q（期望 tsv|ids）\n", format)
		return 2
	}
	return 0
}

func layoutCheck(root string) int {
	issues, err := workflow.ValidateTaskLayout(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if len(issues) == 0 {
		fmt.Println("PASS layout: 分类清单与磁盘布局一致，分类依据可核验")
		return 0
	}
	for _, issue := range issues {
		fmt.Println("FAIL layout:", issue.String())
	}
	fmt.Fprintf(os.Stderr, "FAIL layout: %d 项不一致\n", len(issues))
	return 1
}

func layoutPlan(root string) int {
	plan, issues, err := workflow.PlanTaskLayout(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	for _, issue := range issues {
		fmt.Println("CONFLICT layout:", issue.String())
	}
	for _, id := range plan.InPlace {
		fmt.Printf("KEEP %s\n", id)
	}
	for _, mv := range plan.Moves {
		fmt.Printf("MOVE %s %s -> %s (%d files)\n", mv.ID, mv.From, mv.To, len(mv.Files))
	}
	fmt.Fprintf(os.Stderr, "plan: %d 移动、%d 已就位、%d 冲突\n", len(plan.Moves), len(plan.InPlace), len(issues))
	if len(issues) != 0 {
		return 1
	}
	return 0
}

func layoutMigrate(root string, apply bool) int {
	plan, issues, err := workflow.PlanTaskLayout(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if len(issues) != 0 {
		for _, issue := range issues {
			fmt.Println("CONFLICT layout:", issue.String())
		}
		fmt.Fprintln(os.Stderr, "拒绝迁移：先修复冲突（不移动任何目录）")
		return 1
	}
	for _, mv := range plan.Moves {
		prefix := "PLAN"
		if apply {
			prefix = "MOVE"
		}
		fmt.Printf("%s %s -> %s\n", prefix, mv.From, mv.To)
	}
	if len(plan.Moves) == 0 {
		fmt.Printf("PASS layout: 全部 %d 个 Task 已在分类目录，无需迁移\n", len(plan.InPlace))
		return 0
	}
	if !apply {
		fmt.Fprintf(os.Stderr, "dry-run：共 %d 项待移动；加 --apply 执行并写入 %s\n", len(plan.Moves), workflow.TaskLayoutJournalPath)
		return 0
	}
	journal, err := workflow.ApplyTaskLayout(root, plan, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: 迁移失败（已尝试恢复原布局）: %v\n", err)
		return 2
	}
	fmt.Printf("PASS layout: 移动 %d 个 Task；回滚日志 %s（source_sha=%s）\n", len(journal.Tasks), workflow.TaskLayoutJournalPath, journal.SourceSHA)
	fmt.Println("提示：目录移动尚未提交，请检查 git status 后再提交。")
	return 0
}

func layoutRollback(root, journal string, apply bool) int {
	rel := journal
	if rel == "" {
		rel = workflow.TaskLayoutJournalPath
	}
	j, moved, err := workflow.RollbackTaskLayout(root, rel, !apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	for _, mv := range j.Tasks {
		if apply {
			fmt.Printf("ROLLBACK %s %s -> %s\n", mv.ID, mv.To, mv.From)
		} else {
			fmt.Printf("PLAN-ROLLBACK %s %s -> %s\n", mv.ID, mv.To, mv.From)
		}
	}
	if !apply {
		fmt.Fprintf(os.Stderr, "dry-run：可回滚 %d 项；加 --apply 执行\n", moved)
		return 0
	}
	fmt.Printf("PASS layout: 回滚 %d 个 Task（日志 %s）\n", moved, rel)
	return 0
}
