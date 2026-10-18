package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是 Phase 02 跨模块集成测试：使用 Task 03 的真实 EventWriter.Append() 写入，
// 再使用 Task 04 的真实读取接口（ReadTaskEventLog / ParseEventLog）解析，
// 验证写入端与读取端的磁盘协议完全兼容。禁止用互不关联的 Mock Fixture 冒充联调。

// writerEvent 构造一条最小合法写入端事件。
func writerEvent(typ, reason string) Event {
	return Event{
		Timestamp: "2026-10-16T05:00:00Z",
		Role:      "Coder",
		Event:     typ,
		ScopeKind: ScopeTask,
		ScopeID:   "demo",
		SHA:       "",
		Reason:    reason,
	}
}

// TestWriterReaderRoundTripAllEventTypes 覆盖 START / HANDOFF_PREPARED / ACK /
// BLOCKED / COMPLETED 全枚举，并按文件顺序多事件往返，验证行号定位准确。
func TestWriterReaderRoundTripAllEventTypes(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)

	types := []string{EventStart, EventHandoffPrepared, EventAck, EventBlocked, EventCompleted}
	for _, typ := range types {
		reason := ""
		if typ == EventBlocked {
			reason = "缺少批准的 Contract"
		}
		if err := w.Append(writerEvent(typ, reason)); err != nil {
			t.Fatalf("Append(%s) 失败: %v", typ, err)
		}
	}

	events, errs, err := ReadTaskEventLog(dir, "demo")
	if err != nil {
		t.Fatalf("ReadTaskEventLog 失败: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("合法事件不应有解析错误: %v", errs)
	}
	if len(events) != len(types) {
		t.Fatalf("期望 %d 条事件，实际 %d", len(types), len(events))
	}
	for i, typ := range types {
		if events[i].Event != typ {
			t.Fatalf("events[%d].Event = %q, 期望 %q", i, events[i].Event, typ)
		}
		// 头行 + 空行 + 1-based 事件行：首条事件位于第 3 行。
		if events[i].Line != i+3 {
			t.Fatalf("events[%d].Line = %d, 期望 %d", i, events[i].Line, i+3)
		}
	}
}

// TestWriterReaderSpecialCharsReason 覆盖 reason 中含引号、反斜杠、换行等特殊字符的往返。
func TestWriterReaderSpecialCharsReason(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)
	reason := "含\"引号\"、\\反斜杠、\n换行、<标签>&符号"
	if err := w.Append(writerEvent(EventBlocked, reason)); err != nil {
		t.Fatalf("Append 失败: %v", err)
	}

	events, errs, err := ReadTaskEventLog(dir, "demo")
	if err != nil || len(errs) != 0 {
		t.Fatalf("读取失败: err=%v errs=%v", err, errs)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d", len(events))
	}
	if events[0].Reason != reason {
		t.Fatalf("reason 往返不一致: got=%q want=%q", events[0].Reason, reason)
	}

	// 事件文件必须保持单行（换行被 JSON 转义，不产生字面换行）。
	raw, err := os.ReadFile(filepath.Join(dir, ".agent", "tasks", "demo", "events.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "\n换行") {
		t.Fatal("reason 中的换行未按 JSON 转义，破坏了单行事件格式")
	}
}

// TestWriterRejectsInvalidEvents 验证写入端对非法事件 fail-closed，
// 保证非法 scope_id 与 BLOCKED 缺 reason 无法进入磁盘，读取端不会收到这类合法来源的非法记录。
func TestWriterRejectsInvalidEvents(t *testing.T) {
	dir := t.TempDir()
	w := NewEventWriter(dir)

	t.Run("非法 scope_id 路径穿越", func(t *testing.T) {
		e := writerEvent(EventStart, "")
		e.ScopeID = "../etc"
		if err := w.Append(e); err == nil {
			t.Fatal("期望拒绝非法 scope_id，实际 nil")
		}
	})
	t.Run("BLOCKED 缺 reason", func(t *testing.T) {
		e := writerEvent(EventBlocked, "")
		if err := w.Append(e); err == nil {
			t.Fatal("期望拒绝 BLOCKED 缺 reason，实际 nil")
		}
	})
}

// TestReaderStrictMalformedContent 验证读取端对磁盘上出现的非法内容 fail-closed。
// 这些内容无法通过真实 Writer（Writer 会拒绝），因此直接构造磁盘原始内容，
// 再用真实 Reader（ParseEventLog）验证其严格报错，不静默忽略。
func TestReaderStrictMalformedContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"非法 JSON", `- {not json}` + "\n", "非法 JSON"},
		{"缺失 timestamp", `- {"role":"Coder","event":"START","scope_kind":"task","scope_id":"demo"}` + "\n", "时间戳"},
		{"缺失 role", `- {"timestamp":"2026-10-16T05:00:00Z","event":"START","scope_kind":"task","scope_id":"demo"}` + "\n", "role"},
		{"未知字段", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo","extra":"x"}` + "\n", "非法 JSON"},
		{"非法 SHA", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo","sha":"abc123"}` + "\n", "sha"},
		{"非法 scope_id 格式", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"../etc"}` + "\n", "scope_id"},
		{"BLOCKED 缺 reason", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"BLOCKED","scope_kind":"task","scope_id":"demo"}` + "\n", "reason"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseEventLog(c.content)
			if len(errs) != 1 || !strings.Contains(errs[0].Problem, c.want) {
				t.Fatalf("期望报错含 %q，实际 %v", c.want, errs)
			}
		})
	}
}

// TestReaderEventFileNotExist 覆盖事件文件不存在时读取端返回无事件且无错误。
func TestReaderEventFileNotExist(t *testing.T) {
	dir := t.TempDir()
	events, errs, err := ReadTaskEventLog(dir, "no-such-task")
	if err != nil {
		t.Fatalf("文件不存在不应返回 error，实际 %v", err)
	}
	if len(events) != 0 || len(errs) != 0 {
		t.Fatalf("文件不存在应返回 0 事件 0 错误，实际 events=%v errs=%v", events, errs)
	}
}

// TestReaderScopeMismatch 覆盖事件文件内容中的 scope_id 与读取 task 不一致时，
// 读取端通过 scopeConsistencyErrors 报告（与 Inspect 的行为一致）。
// 说明：写入端 EventWriter 始终按 scope_id 决定文件路径、保证二者一致，因此
// 不一致场景只能来自文件被篡改，这里直接构造磁盘内容再用真实读取接口校验。
func TestReaderScopeMismatch(t *testing.T) {
	dir := t.TempDir()
	raw := "# Workflow Events\n\n" +
		`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"other-task","sha":"","reason":""}` + "\n"
	path := filepath.Join(dir, ".agent", "tasks", "demo", "events.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	events, errs, err := ReadTaskEventLog(dir, "demo")
	if err != nil || len(errs) != 0 {
		t.Fatalf("读取失败: err=%v errs=%v", err, errs)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d", len(events))
	}
	consistency := scopeConsistencyErrors(events, "demo")
	if len(consistency) == 0 {
		t.Fatal("scope_id 与 task 不一致应被报告")
	}
	found := false
	for _, ce := range consistency {
		if strings.Contains(ce.Problem, "不一致") {
			found = true
		}
	}
	if !found {
		t.Fatalf("期望 scope 不一致错误，实际 %v", consistency)
	}
}
