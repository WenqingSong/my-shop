// Command workflow-event 是 Workflow V4 事件日志的最小写入 CLI。
//
// 它只负责把一条已校验的事件追加到指定 Workflow 事件文件，不读取/解析事件、
// 不参与审批与 Gate 判定，不代理任何 git 写操作。
//
// 用法：
//
//	workflow-event append --scope-kind <task|program> --scope-id <id> --role <role> --event <type> \
//	    [--sha <sha>] [--reason <text>] [--timestamp <RFC3339Nano>] [--root DIR]
//
// 事件类型：START | HANDOFF_PREPARED | ACK | BLOCKED | COMPLETED
//
// exit code：
//   - 0：追加成功；
//   - 1：事件校验失败（非法 scope / 事件类型 / 时间 / SHA / BLOCKED 缺 reason）；
//   - 2：用法或 I/O 错误。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"workflow-v2-engine/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}
	switch args[0] {
	case "append":
		return runAppend(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: 未知子命令 %q\n", args[0])
		printUsage()
		return 2
	}
}

func runAppend(args []string) int {
	fs := flag.NewFlagSet("append", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printUsage() }

	scopeKind := fs.String("scope-kind", "", "scope 类型：task | program")
	scopeID := fs.String("scope-id", "", "Task/Program ID")
	role := fs.String("role", "", "产生事件的角色")
	event := fs.String("event", "", "事件类型：START | HANDOFF_PREPARED | ACK | BLOCKED | COMPLETED")
	sha := fs.String("sha", "", "关联的完整 Commit SHA（可选）")
	reason := fs.String("reason", "", "附加说明（BLOCKED 时必填）")
	timestamp := fs.String("timestamp", "", "UTC RFC3339Nano 时间（缺省为当前时间）")
	root := fs.String("root", "", "仓库根目录（缺省通过 git 定位）")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: 多余的位置参数: %v\n", fs.Args())
		return 2
	}

	if *timestamp == "" {
		*timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if *root == "" {
		*root = findRoot()
	}

	e := workflow.Event{
		Timestamp: *timestamp,
		Role:      *role,
		Event:     *event,
		ScopeKind: *scopeKind,
		ScopeID:   *scopeID,
		SHA:       *sha,
		Reason:    *reason,
	}

	// 校验失败 → exit 1；通过后再写入，剩余错误属 I/O → exit 2。
	if err := e.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := workflow.NewEventWriter(*root).Append(e); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	fmt.Printf("appended event=%s scope=%s/%s\n", e.Event, e.ScopeKind, e.ScopeID)
	return 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `usage: workflow-event append --scope-kind <task|program> --scope-id <id> --role <role> --event <type> [--sha <sha>] [--reason <text>] [--timestamp <RFC3339Nano>] [--root DIR]

事件类型：START | HANDOFF_PREPARED | ACK | BLOCKED | COMPLETED
exit code：0 成功；1 事件校验失败；2 用法或 I/O 错误`)
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
