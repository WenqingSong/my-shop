package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// validEvent 返回一条最小合法事件。
func validEvent() Event {
	return Event{
		Timestamp: "2026-10-16T05:00:00Z",
		Role:      "Coder",
		Event:     EventStart,
		ScopeKind: ScopeTask,
		ScopeID:   "demo",
		SHA:       "",
		Reason:    "",
	}
}

// readEvents 解析 events.md，返回按出现顺序排列的事件（供测试校验可解析性与顺序）。
func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	var events []Event
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line == eventsHeaderLine {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			t.Fatalf("事件行必须以 \"- \" 开头，实际 %q", line)
		}
		var e Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "- ")), &e); err != nil {
			t.Fatalf("事件行不是合法 JSON：%q: %v", line, err)
		}
		events = append(events, e)
	}
	return events
}

const eventsHeaderLine = "# Workflow Events"

// --- 事件类型 / scope 枚举校验 ---

func TestValidEventType(t *testing.T) {
	for _, e := range []string{EventStart, EventHandoffPrepared, EventAck, EventBlocked, EventCompleted} {
		if !ValidEventType(e) {
			t.Fatalf("ValidEventType(%s) 应为 true", e)
		}
	}
	for _, e := range []string{"", "start", "END", "NEXT_ROLE", "STALE"} {
		if ValidEventType(e) {
			t.Fatalf("ValidEventType(%q) 应为 false", e)
		}
	}
}

func TestValidScopeKind(t *testing.T) {
	for _, k := range []string{ScopeTask, ScopeProgram} {
		if !ValidScopeKind(k) {
			t.Fatalf("ValidScopeKind(%s) 应为 true", k)
		}
	}
	for _, k := range []string{"", "Task", "TASK", "programs", "."} {
		if ValidScopeKind(k) {
			t.Fatalf("ValidScopeKind(%q) 应为 false", k)
		}
	}
}

// --- 字段校验（AC-03 / AC-04）---

func TestEventValidate(t *testing.T) {
	base := validEvent()
	tests := []struct {
		name   string
		mutate func(*Event)
		want   string
	}{
		{"合法事件", func(*Event) {}, ""},
		{"非法 scope_kind", func(e *Event) { e.ScopeKind = "registry" }, "scope_kind"},
		{"空 scope_id", func(e *Event) { e.ScopeID = "" }, "scope_id"},
		{"scope_id 路径穿越 ..", func(e *Event) { e.ScopeID = ".." }, "scope_id"},
		{"scope_id 含斜杠", func(e *Event) { e.ScopeID = "a/b" }, "scope_id"},
		{"scope_id 含反斜杠", func(e *Event) { e.ScopeID = "a\\b" }, "scope_id"},
		{"scope_id 以点开头", func(e *Event) { e.ScopeID = ".hidden" }, "scope_id"},
		{"非法事件类型", func(e *Event) { e.Event = "NEXT_ROLE" }, "事件类型"},
		{"空 role", func(e *Event) { e.Role = "" }, "role"},
		{"非法时间戳", func(e *Event) { e.Timestamp = "2026-10-16 05:00:00" }, "时间戳"},
		{"非 UTC 时间戳", func(e *Event) { e.Timestamp = "2026-10-16T05:00:00+02:00" }, "时间戳"},
		{"非法 SHA", func(e *Event) { e.SHA = "abc123" }, "sha"},
		{"BLOCKED 缺 reason", func(e *Event) { e.Event = EventBlocked; e.Reason = "" }, "reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base
			tt.mutate(&e)
			err := e.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("期望合法，实际 %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望错误含 %q，实际 nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.want, err)
			}
		})
	}
}

func TestValidTimestamp(t *testing.T) {
	for _, ts := range []string{
		"2026-10-16T05:00:00Z",
		"2026-10-16T05:00:00.123456789Z",
		"2026-10-16T05:00:00+00:00",
	} {
		if !validTimestamp(ts) {
			t.Fatalf("validTimestamp(%q) 应为 true", ts)
		}
	}
	for _, ts := range []string{
		"",
		"2026-10-16T05:00:00",
		"2026-10-16T05:00:00+02:00",
		"2026-10-16 05:00:00Z",
		"not-a-time",
	} {
		if validTimestamp(ts) {
			t.Fatalf("validTimestamp(%q) 应为 false", ts)
		}
	}
}

func TestValidSHA(t *testing.T) {
	if !validSHA("") {
		t.Fatal("空 SHA 应合法（无关联 Commit）")
	}
	if !validSHA(strings.Repeat("a", 40)) {
		t.Fatal("40 位十六进制 SHA 应合法")
	}
	if !validSHA(strings.Repeat("A", 64)) {
		t.Fatal("64 位十六进制 SHA 应合法")
	}
	for _, s := range []string{"abc", strings.Repeat("g", 40), strings.Repeat("a", 39), strings.Repeat("a", 65)} {
		if validSHA(s) {
			t.Fatalf("validSHA(%q) 应为 false", s)
		}
	}
}

// --- 路径计算与路径穿越（AC-07）---

func TestEventsRelPath(t *testing.T) {
	got, err := eventsRelPath(ScopeTask, "demo")
	if err != nil || got != filepath.Join(".agent", "tasks", "demo", "events.md") {
		t.Fatalf("task 路径 = %q, err=%v", got, err)
	}
	got, err = eventsRelPath(ScopeProgram, "prog-1")
	if err != nil || got != filepath.Join(".agent", "programs", "prog-1", "events.md") {
		t.Fatalf("program 路径 = %q, err=%v", got, err)
	}
}

func TestEventsRelPathRejectsTraversal(t *testing.T) {
	for _, id := range []string{"", "..", "../etc", "a/b", "a\\b", ".", ".hidden", "a\x00b", "a b"} {
		if _, err := eventsRelPath(ScopeTask, id); err == nil {
			t.Fatalf("scope_id=%q 应被拒绝（路径穿越/非法）", id)
		}
	}
	if _, err := eventsRelPath("registry", "demo"); err == nil {
		t.Fatal("非法 scope_kind 应被拒绝")
	}
}

// --- 追加与格式（AC-01 / AC-02 / AC-05）---

func TestAppendCreatesFileWithHeader(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err != nil {
		t.Fatalf("Append: %v", err)
	}
	path := filepath.Join(dir, ".agent", "tasks", "demo", "events.md")
	want := "# Workflow Events\n\n" +
		"- {\"timestamp\":\"2026-10-16T05:00:00Z\",\"role\":\"Coder\",\"event\":\"START\",\"scope_kind\":\"task\",\"scope_id\":\"demo\",\"sha\":\"\",\"reason\":\"\"}\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("文件内容不符。\nwant: %q\ngot:  %q", want, string(got))
	}
}

func TestAppendDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	first := validEvent()
	if err := w.Append(first); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	path := filepath.Join(dir, ".agent", "tasks", "demo", "events.md")
	before, _ := os.ReadFile(path)

	second := first
	second.Event = EventCompleted
	second.Reason = "done"
	if err := w.Append(second); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatalf("已有事件被覆盖。\nbefore=%q\nafter=%q", before, after)
	}
	events := readEvents(t, path)
	if len(events) != 2 {
		t.Fatalf("期望 2 条事件，实际 %d", len(events))
	}
}

func TestAppendPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	events := []Event{
		{Timestamp: "2026-10-16T05:00:00Z", Role: "Coder", Event: EventStart, ScopeKind: ScopeTask, ScopeID: "demo", SHA: "", Reason: ""},
		{Timestamp: "2026-10-16T05:01:00Z", Role: "Coder", Event: EventHandoffPrepared, ScopeKind: ScopeTask, ScopeID: "demo", SHA: "", Reason: "handoff"},
		{Timestamp: "2026-10-16T05:02:00Z", Role: "Coder", Event: EventBlocked, ScopeKind: ScopeTask, ScopeID: "demo", SHA: "", Reason: "缺 contract"},
		{Timestamp: "2026-10-16T05:03:00Z", Role: "Owner", Event: EventCompleted, ScopeKind: ScopeTask, ScopeID: "demo", SHA: "", Reason: "ok"},
	}
	for _, e := range events {
		if err := w.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got := readEvents(t, filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if len(got) != len(events) {
		t.Fatalf("期望 %d 条，实际 %d", len(events), len(got))
	}
	for i := range events {
		if got[i].Event != events[i].Event || got[i].Reason != events[i].Reason || got[i].Timestamp != events[i].Timestamp {
			t.Fatalf("第 %d 条顺序/内容不符：got=%+v want=%+v", i, got[i], events[i])
		}
	}
}

func TestAppendProgramScope(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	e := validEvent()
	e.ScopeKind = ScopeProgram
	e.ScopeID = "prog-1"
	if err := w.Append(e); err != nil {
		t.Fatalf("Append program: %v", err)
	}
	path := filepath.Join(dir, ".agent", "programs", "prog-1", "events.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("program events.md 未创建: %v", err)
	}
}

// --- reason 特殊字符 JSON 编码 ---

func TestAppendReasonSpecialChars(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	e := validEvent()
	e.Event = EventBlocked
	e.Reason = "含\"引号\"、\n换行、<标签>&符号"
	if err := w.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(raw)
	events := readEvents(t, filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d", len(events))
	}
	if events[0].Reason != e.Reason {
		t.Fatalf("reason 往返不一致：got=%q want=%q", events[0].Reason, e.Reason)
	}
	// 事件行必须保持单行（不含字面换行）。
	if strings.Contains(content, "换行\n") {
		t.Fatal("reason 中的换行未按 JSON 转义，破坏了单行事件格式")
	}
}

// --- 单进程并发追加（AC-06）---

func TestAppendConcurrentSingleProcess(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	const n = 64
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := validEvent()
			e.Event = EventAck
			e.Reason = fmt.Sprintf("ack-%03d", i)
			errs[i] = w.Append(e)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发 Append[%d] 失败: %v", i, err)
		}
	}
	events := readEvents(t, filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if len(events) != n {
		t.Fatalf("期望 %d 条事件，实际 %d", n, len(events))
	}
	reasons := make([]string, 0, n)
	for _, e := range events {
		reasons = append(reasons, e.Reason)
	}
	sort.Strings(reasons)
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("ack-%03d", i)
		if reasons[i] != want {
			t.Fatalf("缺失或重复事件 reason=%q，第 %d 期望 %q", reasons[i], i, want)
		}
	}
}

// --- 跨进程 flock 阻塞（AC-06 能力边界）---

func TestAppendBlocksOnCrossProcessLock(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	abs := filepath.Join(dir, ".agent", "tasks", "demo", "events.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	holder, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock holder: %v", err)
	}
	defer syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)

	done := make(chan error, 1)
	go func() {
		done <- w.Append(validEvent())
	}()

	select {
	case err := <-done:
		t.Fatalf("应被 flock 阻塞，却提前完成（err=%v）", err)
	case <-time.After(100 * time.Millisecond):
		// 仍在阻塞，符合预期。
	}

	// 释放锁后 Append 应能完成。
	syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("释放锁后 Append 应成功: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("释放锁后 Append 未在超时内完成")
	}
}

// --- I/O 错误正确返回（不伪造成功）---

func TestAppendReturnsIOError(t *testing.T) {
	dir := t.TempDir()
	// 将 .agent 建成普通文件，使目录打开失败。
	if err := os.WriteFile(filepath.Join(dir, ".agent"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望 I/O 错误，实际 nil")
	}
}

// --- 符号链接安全（fail-closed，不追踪链接）---

// TestAppendRejectsSymlinkedEventsFile 覆盖最终 events.md 为符号链接（仓库内部目标）。
func TestAppendRejectsSymlinkedEventsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(target, []byte("precious"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	evDir := filepath.Join(dir, ".agent", "tasks", "demo")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(evDir, "events.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望拒绝 symlink events.md，实际 nil")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "precious" {
		t.Fatalf("仓库内部目标文件被修改: %q", got)
	}
}

// TestAppendRejectsSymlinkedEventsFileExternal 覆盖最终 events.md 为符号链接（仓库外部目标）。
func TestAppendRejectsSymlinkedEventsFileExternal(t *testing.T) {
	dir := t.TempDir()
	extDir := t.TempDir()
	target := filepath.Join(extDir, "precious.txt")
	if err := os.WriteFile(target, []byte("precious"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	evDir := filepath.Join(dir, ".agent", "tasks", "demo")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(evDir, "events.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望拒绝 symlink events.md（外部目标），实际 nil")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "precious" {
		t.Fatalf("仓库外部目标文件被修改: %q", got)
	}
}

// TestAppendRejectsSymlinkedScopeDir 覆盖 <scope-id> 目录为符号链接。
func TestAppendRejectsSymlinkedScopeDir(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(t.TempDir(), "real-demo")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "events.md"), []byte("precious"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	tasksDir := filepath.Join(dir, ".agent", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(tasksDir, "demo")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望拒绝 symlink scope 目录，实际 nil")
	}
	got, err := os.ReadFile(filepath.Join(targetDir, "events.md"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "precious" {
		t.Fatalf("scope 目录目标内容被修改: %q", got)
	}
}

// TestAppendRejectsSymlinkedTasksDir 覆盖 tasks 目录为符号链接（programs 走同一代码路径）。
func TestAppendRejectsSymlinkedTasksDir(t *testing.T) {
	dir := t.TempDir()
	targetTasks := filepath.Join(t.TempDir(), "real-tasks")
	if err := os.MkdirAll(filepath.Join(targetTasks, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetTasks, "demo", "events.md"), []byte("precious"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	agentDir := filepath.Join(dir, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(targetTasks, filepath.Join(agentDir, "tasks")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望拒绝 symlink tasks 目录，实际 nil")
	}
	got, err := os.ReadFile(filepath.Join(targetTasks, "demo", "events.md"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "precious" {
		t.Fatalf("tasks 目录目标内容被修改: %q", got)
	}
}

// TestAppendRejectsSymlinkedAgentDir 覆盖 .agent 目录为符号链接。
func TestAppendRejectsSymlinkedAgentDir(t *testing.T) {
	dir := t.TempDir()
	targetAgent := filepath.Join(t.TempDir(), "real-agent")
	if err := os.MkdirAll(filepath.Join(targetAgent, "tasks", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetAgent, "tasks", "demo", "events.md"), []byte("precious"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(targetAgent, filepath.Join(dir, ".agent")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := NewEventWriter(dir)
	if err := w.Append(validEvent()); err == nil {
		t.Fatal("期望拒绝 symlink .agent 目录，实际 nil")
	}
	got, err := os.ReadFile(filepath.Join(targetAgent, "tasks", "demo", "events.md"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "precious" {
		t.Fatalf(".agent 目录目标内容被修改: %q", got)
	}
}
