package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageErrors(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("无参数期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"bogus"}); code != 2 {
		t.Fatalf("未知子命令期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"append"}); code != 1 {
		t.Fatalf("append 缺少必要字段期望 exit 1（校验失败），实际 %d", code)
	}
}

func TestRunAppendHappyPath(t *testing.T) {
	dir := t.TempDir()
	code := run([]string{
		"append",
		"--scope-kind", "task",
		"--scope-id", "demo",
		"--role", "Coder",
		"--event", "START",
		"--timestamp", "2026-10-16T05:00:00Z",
		"--root", dir,
	})
	if code != 0 {
		t.Fatalf("期望 exit 0，实际 %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if err != nil {
		t.Fatalf("读取事件文件: %v", err)
	}
	if !strings.HasPrefix(string(raw), "# Workflow Events\n\n- {\"timestamp\":\"2026-10-16T05:00:00Z\",\"role\":\"Coder\",\"event\":\"START\"") {
		t.Fatalf("事件文件内容不符: %q", string(raw))
	}
}

func TestRunAppendValidationError(t *testing.T) {
	dir := t.TempDir()
	// BLOCKED 缺 reason → exit 1。
	code := run([]string{
		"append",
		"--scope-kind", "task",
		"--scope-id", "demo",
		"--role", "Coder",
		"--event", "BLOCKED",
		"--root", dir,
	})
	if code != 1 {
		t.Fatalf("BLOCKED 缺 reason 期望 exit 1，实际 %d", code)
	}
	// 非法事件类型 → exit 1。
	code = run([]string{
		"append",
		"--scope-kind", "task",
		"--scope-id", "demo",
		"--role", "Coder",
		"--event", "NEXT_ROLE",
		"--root", dir,
	})
	if code != 1 {
		t.Fatalf("非法事件类型期望 exit 1，实际 %d", code)
	}
	// 路径穿越 scope-id → exit 1。
	code = run([]string{
		"append",
		"--scope-kind", "task",
		"--scope-id", "../etc",
		"--role", "Coder",
		"--event", "START",
		"--root", dir,
	})
	if code != 1 {
		t.Fatalf("路径穿越 scope-id 期望 exit 1，实际 %d", code)
	}
}

func TestRunAppendIOError(t *testing.T) {
	dir := t.TempDir()
	// 将 .agent 建成普通文件，触发 I/O 错误 → exit 2。
	if err := os.WriteFile(filepath.Join(dir, ".agent"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	code := run([]string{
		"append",
		"--scope-kind", "task",
		"--scope-id", "demo",
		"--role", "Coder",
		"--event", "START",
		"--timestamp", "2026-10-16T05:00:00Z",
		"--root", dir,
	})
	if code != 2 {
		t.Fatalf("I/O 错误期望 exit 2，实际 %d", code)
	}
}

func TestRunAppendProgramScope(t *testing.T) {
	dir := t.TempDir()
	code := run([]string{
		"append",
		"--scope-kind", "program",
		"--scope-id", "prog-1",
		"--role", "Owner",
		"--event", "COMPLETED",
		"--root", dir,
	})
	if code != 0 {
		t.Fatalf("program 事件期望 exit 0，实际 %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent", "programs", "prog-1", "events.md")); err != nil {
		t.Fatalf("program events.md 未创建: %v", err)
	}
}
