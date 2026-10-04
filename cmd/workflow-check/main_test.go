package main

import (
	"os"
	"path/filepath"
	"testing"
)

const legalState = `task: demo
phase: NEW
review:
  status: NONE
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  error_code_domains: []
  migrations: []
blocked:
  is_blocked: false
  reason: ""
`

func writeTask(t *testing.T, dir, slug, state string) {
	t.Helper()
	taskDir := filepath.Join(dir, ".agent", "tasks", slug)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("创建任务目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("# task\n"), 0o644); err != nil {
		t.Fatalf("写 task.md 失败: %v", err)
	}
	if state != "" {
		if err := os.WriteFile(filepath.Join(taskDir, "state.yaml"), []byte(state), 0o644); err != nil {
			t.Fatalf("写 state.yaml 失败: %v", err)
		}
	}
}

// TestRunExitCodes 覆盖 Contract 的 exit code 语义：
// 0 = 合法，1 = invariant 不合法，2 = 解析级错误。
func TestRunExitCodes(t *testing.T) {
	t.Run("合法状态返回 0", func(t *testing.T) {
		dir := t.TempDir()
		writeTask(t, dir, "demo", legalState)
		if code := run([]string{"--root", dir}); code != 0 {
			t.Fatalf("合法状态期望 exit 0，实际 %d", code)
		}
	})

	t.Run("非法 phase 返回 1", func(t *testing.T) {
		dir := t.TempDir()
		writeTask(t, dir, "demo", "task: demo\nphase: BOGUS\nreview:\n  status: NONE\nowner_verification:\n  status: PENDING\ndelivery:\n  status: NONE\n")
		if code := run([]string{"--root", dir}); code != 1 {
			t.Fatalf("非法 phase 期望 exit 1，实际 %d", code)
		}
	})

	t.Run("malformed YAML 返回 2", func(t *testing.T) {
		dir := t.TempDir()
		writeTask(t, dir, "demo", "phase: [unclosed\n")
		if code := run([]string{"--root", dir}); code != 2 {
			t.Fatalf("malformed YAML 期望 exit 2，实际 %d", code)
		}
	})

	t.Run("缺 state.yaml 且未提供 cutover 返回 0（SKIPPED）", func(t *testing.T) {
		dir := t.TempDir()
		writeTask(t, dir, "legacy", "")
		if code := run([]string{"--root", dir}); code != 0 {
			t.Fatalf("无 cutover 时缺 state.yaml 期望 SKIPPED 且 exit 0，实际 %d", code)
		}
	})
}
