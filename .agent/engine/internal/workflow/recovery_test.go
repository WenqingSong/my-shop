package workflow

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 事件日志解析测试（纯函数）
// ---------------------------------------------------------------------------

func TestParseEventLogValid(t *testing.T) {
	content := `# Workflow Events
- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo","sha":"","reason":""}
- {"timestamp":"2026-10-16T05:01:00Z","role":"Coder","event":"BLOCKED","scope_kind":"task","scope_id":"demo","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reason":"缺少批准的 Contract"}
`
	events, errs := ParseEventLog(content)
	if len(errs) != 0 {
		t.Fatalf("合法事件不应报错，实际 %v", errs)
	}
	if len(events) != 2 {
		t.Fatalf("期望 2 条事件，实际 %d", len(events))
	}
	if events[0].Event != EventStart || events[0].Role != "Coder" || events[0].ScopeID != "demo" {
		t.Fatalf("events[0] = %+v", events[0])
	}
	if events[1].Event != EventBlocked || events[1].SHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || events[1].Reason != "缺少批准的 Contract" {
		t.Fatalf("events[1] = %+v", events[1])
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-10-16T05:01:00Z")
	if !events[1].Timestamp.Equal(want) {
		t.Fatalf("events[1].Timestamp = %v, 期望 %v", events[1].Timestamp, want)
	}
	if events[1].Line != 3 {
		t.Fatalf("events[1].Line = %d, 期望 3", events[1].Line)
	}
}

func TestParseEventLogEmptyAndHeader(t *testing.T) {
	for name, content := range map[string]string{
		"empty":  "",
		"header": "# Workflow Events\n",
		"blank":  "\n\n",
	} {
		events, errs := ParseEventLog(content)
		if len(events) != 0 || len(errs) != 0 {
			t.Fatalf("%s: 期望 0 事件 0 错误，实际 events=%v errs=%v", name, events, errs)
		}
	}
}

func TestParseEventLogInvalidJSON(t *testing.T) {
	events, errs := ParseEventLog("- {not json}\n")
	if len(events) != 0 {
		t.Fatalf("非法 JSON 不应产出事件，实际 %v", events)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "非法 JSON") {
		t.Fatalf("期望报告非法 JSON，实际 %v", errs)
	}
}

func TestParseEventLogMissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"缺 timestamp", `{"role":"Coder","event":"START","scope_kind":"task","scope_id":"demo"}`, "时间戳"},
		{"缺 role", `{"timestamp":"2026-10-16T05:00:00Z","event":"START","scope_kind":"task","scope_id":"demo"}`, "role"},
		{"缺 event", `{"timestamp":"2026-10-16T05:00:00Z","role":"Coder","scope_kind":"task","scope_id":"demo"}`, "事件类型"},
		{"缺 scope_kind", `{"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_id":"demo"}`, "scope_kind"},
		{"缺 scope_id", `{"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task"}`, "scope_id"},
		{"空 role", `{"timestamp":"2026-10-16T05:00:00Z","role":"","event":"START","scope_kind":"task","scope_id":"demo"}`, "role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseEventLog("- " + c.json + "\n")
			if len(errs) != 1 || !strings.Contains(errs[0].Problem, c.want) {
				t.Fatalf("期望报告缺失字段 %q，实际 %v", c.want, errs)
			}
		})
	}
}

func TestParseEventLogInvalidTimestamp(t *testing.T) {
	_, errs := ParseEventLog(`- {"timestamp":"not-a-time","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "时间戳") {
		t.Fatalf("期望报告非法 timestamp，实际 %v", errs)
	}
}

func TestParseEventLogInvalidEventTypeAndScope(t *testing.T) {
	_, errs := ParseEventLog(`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"CRASHED","scope_kind":"task","scope_id":"demo"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "事件类型") {
		t.Fatalf("期望报告非法 event，实际 %v", errs)
	}
	_, errs = ParseEventLog(`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"repo","scope_id":"demo"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "scope_kind") {
		t.Fatalf("期望报告非法 scope_kind，实际 %v", errs)
	}
}

func TestParseEventLogBlockedMissingReason(t *testing.T) {
	_, errs := ParseEventLog(`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"BLOCKED","scope_kind":"task","scope_id":"demo"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "reason") {
		t.Fatalf("期望报告 BLOCKED 缺 reason，实际 %v", errs)
	}
}

// TestParseEventLogInvalidSHA 覆盖 Reader 对非法 SHA 格式 fail-closed（与 Writer 一致）。
func TestParseEventLogInvalidSHA(t *testing.T) {
	_, errs := ParseEventLog(`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo","sha":"abc123"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "sha") {
		t.Fatalf("期望报告非法 sha，实际 %v", errs)
	}
}

// TestParseEventLogInvalidScopeIDFormat 覆盖 Reader 对非法 scope_id 格式 fail-closed（与 Writer 一致）。
func TestParseEventLogInvalidScopeIDFormat(t *testing.T) {
	_, errs := ParseEventLog(`- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"../etc"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, "scope_id") {
		t.Fatalf("期望报告非法 scope_id，实际 %v", errs)
	}
}

func TestParseEventLogNotDashPrefixed(t *testing.T) {
	_, errs := ParseEventLog(`{"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo"}` + "\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Problem, `"- "`) {
		t.Fatalf("期望报告非 - 开头，实际 %v", errs)
	}
}

func TestParseEventLogMixedValidInvalid(t *testing.T) {
	content := `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"demo"}
- broken
- {"timestamp":"2026-10-16T05:01:00Z","role":"Coder","event":"ACK","scope_kind":"task","scope_id":"demo"}
`
	events, errs := ParseEventLog(content)
	if len(events) != 2 {
		t.Fatalf("期望 2 条合法事件，实际 %d", len(events))
	}
	if len(errs) != 1 {
		t.Fatalf("期望 1 条错误，实际 %d: %v", len(errs), errs)
	}
}

func TestEventValidators(t *testing.T) {
	for _, e := range []EventType{EventStart, EventHandoffPrepared, EventAck, EventBlocked, EventCompleted} {
		if !ValidEventType(e) {
			t.Fatalf("ValidEventType(%s) 应为 true", e)
		}
	}
	if ValidEventType("CRASHED") || ValidEventType("") || !ValidScopeKind("task") || !ValidScopeKind("program") || ValidScopeKind("repo") {
		t.Fatal("非法枚举不应通过校验")
	}
}

func TestEventLogPaths(t *testing.T) {
	if TaskEventLogPath("demo") != ".agent/tasks/demo/events.md" {
		t.Fatalf("TaskEventLogPath = %q", TaskEventLogPath("demo"))
	}
	if ProgramEventLogPath("prog") != ".agent/programs/prog/events.md" {
		t.Fatalf("ProgramEventLogPath = %q", ProgramEventLogPath("prog"))
	}
}

// ---------------------------------------------------------------------------
// 候选入口 Gate 推导（纯函数）
// ---------------------------------------------------------------------------

func baseV4State() State {
	return State{
		SchemaVersion: SchemaV4,
		TaskID:        "demo",
		TaskType:      TaskTypeNormal,
		Git:           &GitBaseline{ParentBranch: "develop", BaseCommit: "abc", TaskBranch: "feature/demo"},
		Contract:      Contract{Status: ContractNotRequired},
		Review:        Review{Status: ReviewNotRequested},
		Owner:         Owner{Status: OwnerPending},
		Delivery:      Delivery{Status: DeliveryNotRun},
	}
}

func TestCandidateGates(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*State)
		want    []string
		certain bool
	}{
		{"contract pending", func(s *State) { s.Contract.Status = ContractPending }, []string{"coder-start"}, true},
		{"contract rejected", func(s *State) { s.Contract.Status = ContractRejected }, []string{"coder-start"}, true},
		{"review not requested", func(s *State) {}, []string{"coder-start"}, true},
		{"review pending", func(s *State) { s.Review.Status = ReviewPending }, []string{"cleaner-start"}, true},
		{"review changes required", func(s *State) { s.Review.Status = ReviewChangesRequired }, []string{"coder-start", "cleaner-start"}, false},
		{"review clean owner pending", func(s *State) { s.Review.Status = ReviewClean }, []string{"owner-gate-start"}, true},
		{"review clean owner accepted delivery not run", func(s *State) { s.Review.Status = ReviewClean; s.Owner.Status = OwnerAccepted }, []string{"delivery-start"}, true},
		{"review clean owner accepted delivery pass", func(s *State) {
			s.Review.Status = ReviewClean
			s.Owner.Status = OwnerAccepted
			s.Delivery.Status = DeliveryPass
		}, []string{"merge-ready"}, true},
		{"review clean owner rejected", func(s *State) { s.Review.Status = ReviewClean; s.Owner.Status = OwnerRejected }, []string{"owner-gate-start", "coder-start"}, false},
		{"review unknown", func(s *State) { s.Review.Status = "BOGUS" }, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := baseV4State()
			c.mutate(&s)
			got, certain := candidateGates(s)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("候选 Gate = %v, 期望 %v", got, c.want)
			}
			if certain != c.certain {
				t.Fatalf("certain = %t, 期望 %t", certain, c.certain)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 异常分析（纯函数，无 git）
// ---------------------------------------------------------------------------

func mkEvent(typ EventType) ParsedEvent {
	ts, _ := time.Parse(time.RFC3339Nano, "2026-10-16T05:00:00Z")
	return ParsedEvent{Line: 1, Timestamp: ts, Role: "Coder", Event: typ, ScopeKind: ScopeTask, ScopeID: "demo"}
}

func hasAnomaly(as []Anomaly, kind string) bool {
	for _, a := range as {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

func TestAnalyzeAnomalies(t *testing.T) {
	t.Run("blocked without event", func(t *testing.T) {
		s := baseV4State()
		s.Blocked.Active = true
		as := analyze(nil, s, true, nil)
		if !hasAnomaly(as, "BLOCKED_WITHOUT_EVENT") {
			t.Fatalf("期望 BLOCKED_WITHOUT_EVENT，实际 %v", as)
		}
	})
	t.Run("start without completed", func(t *testing.T) {
		s := baseV4State()
		as := analyze(nil, s, true, []ParsedEvent{mkEvent(EventStart)})
		if !hasAnomaly(as, "START_WITHOUT_COMPLETED") {
			t.Fatalf("期望 START_WITHOUT_COMPLETED，实际 %v", as)
		}
	})
	t.Run("handoff without ack", func(t *testing.T) {
		s := baseV4State()
		as := analyze(nil, s, true, []ParsedEvent{mkEvent(EventHandoffPrepared)})
		if !hasAnomaly(as, "HANDOFF_WITHOUT_ACK") {
			t.Fatalf("期望 HANDOFF_WITHOUT_ACK，实际 %v", as)
		}
	})
	t.Run("ack without completed", func(t *testing.T) {
		s := baseV4State()
		as := analyze(nil, s, true, []ParsedEvent{mkEvent(EventAck)})
		if !hasAnomaly(as, "ACK_WITHOUT_COMPLETED") {
			t.Fatalf("期望 ACK_WITHOUT_COMPLETED，实际 %v", as)
		}
	})
	t.Run("state unblocked but last event blocked", func(t *testing.T) {
		s := baseV4State()
		as := analyze(nil, s, true, []ParsedEvent{mkEvent(EventBlocked)})
		if !hasAnomaly(as, "STATE_EVENT_CONFLICT") {
			t.Fatalf("期望 STATE_EVENT_CONFLICT，实际 %v", as)
		}
	})
	t.Run("state unparseable", func(t *testing.T) {
		as := analyze(nil, State{}, false, nil)
		if !hasAnomaly(as, "STATE_UNPARSEABLE") {
			t.Fatalf("期望 STATE_UNPARSEABLE，实际 %v", as)
		}
	})
	t.Run("clean state no anomalies", func(t *testing.T) {
		s := baseV4State()
		as := analyze(nil, s, true, []ParsedEvent{mkEvent(EventStart), mkEvent(EventCompleted)})
		if len(as) != 0 {
			t.Fatalf("正常完成不应有异常，实际 %v", as)
		}
	})
}

// ---------------------------------------------------------------------------
// 集成：Inspect 与 CheckRecovery（含 git fixture）
// ---------------------------------------------------------------------------

type v4Opts struct {
	contractStatus string
	reviewStatus   string
	ownerStatus    string
	deliveryStatus string
	blockedActive  bool
	blockedBy      string
	blockedReason  string
}

func defaultV4Opts() v4Opts {
	return v4Opts{
		contractStatus: ContractNotRequired,
		reviewStatus:   ReviewNotRequested,
		ownerStatus:    OwnerPending,
		deliveryStatus: DeliveryNotRun,
	}
}

func v4StateYAML(base string, o v4Opts) string {
	return fmt.Sprintf(`schema_version: 4
task_id: demo
task_type: NORMAL
git:
  parent_branch: develop
  base_commit: %s
  task_branch: feature/agent-workflow-v2
contract:
  status: %s
  target: ""
resources:
  reservations: {}
review:
  status: %s
  target: ""
owner:
  status: %s
  review_target: ""
delivery:
  status: %s
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: %t
  by: %q
  reason: %q
`, base, o.contractStatus, o.reviewStatus, o.ownerStatus, o.deliveryStatus, o.blockedActive, o.blockedBy, o.blockedReason)
}

// setupV4 构建含 V4 state 的临时 git 仓库（feature 已同步远端、工作区 clean）。
func setupV4(t *testing.T, o v4Opts) *testRepo {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	base := r.git("rev-parse", "HEAD")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/state.yaml", v4StateYAML(base, o))
	r.commit("v4 state")
	r.syncOrigin("feature/agent-workflow-v2")
	return r
}

func TestInspectBlockedInfo(t *testing.T) {
	o := defaultV4Opts()
	o.blockedActive = true
	o.blockedBy = "Coder"
	o.blockedReason = "缺少批准的 Contract"
	r := setupV4(t, o)
	r.write(".agent/tasks/demo/events.md", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"BLOCKED","scope_kind":"task","scope_id":"demo","sha":"","reason":"缺少批准的 Contract"}`+"\n")
	r.commit("events")
	r.syncOrigin("feature/agent-workflow-v2")

	d, err := Inspect(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	if !d.SchemaKnown || d.SchemaVersion != SchemaV4 {
		t.Fatalf("SchemaKnown=%t Version=%d", d.SchemaKnown, d.SchemaVersion)
	}
	if d.TaskID != "demo" {
		t.Fatalf("TaskID = %q", d.TaskID)
	}
	if !d.BlockedActive || d.BlockedBy != "Coder" || d.BlockedReason != "缺少批准的 Contract" {
		t.Fatalf("Blocked = %+v", d)
	}
	if len(d.Events) != 1 || d.Events[0].Event != EventBlocked {
		t.Fatalf("Events = %+v", d.Events)
	}
	if len(d.EventErrors) != 0 {
		t.Fatalf("EventErrors = %v", d.EventErrors)
	}
}

func TestInspectEventScopeMismatch(t *testing.T) {
	o := defaultV4Opts()
	r := setupV4(t, o)
	r.write(".agent/tasks/demo/events.md", `- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"START","scope_kind":"task","scope_id":"other-task","sha":"","reason":""}`+"\n")
	r.commit("events")
	r.syncOrigin("feature/agent-workflow-v2")

	d, err := Inspect(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	if len(d.EventErrors) == 0 {
		t.Fatal("scope_id 不一致应报告错误")
	}
	found := false
	for _, e := range d.EventErrors {
		if strings.Contains(e.Problem, "不一致") {
			found = true
		}
	}
	if !found {
		t.Fatalf("期望 scope 不一致错误，实际 %v", d.EventErrors)
	}
}

func TestInspectStateUnparseableReportsUnknown(t *testing.T) {
	o := defaultV4Opts()
	r := setupV4(t, o)
	r.write(".agent/tasks/demo/state.yaml", "schema_version: 4\ntask_id: demo\n") // 缺 task_type/git
	r.commit("bad state")
	r.syncOrigin("feature/agent-workflow-v2")

	d, err := Inspect(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("Inspect 不应因解析失败返回 error，实际 %v", err)
	}
	if d.SchemaKnown {
		t.Fatal("state 解析失败时 SchemaKnown 应为 false")
	}
	if d.StateError == "" {
		t.Fatal("StateError 应非空")
	}
}

// TestInspectLegacyV3 覆盖 AC-10：工具对 V3 state 也能只读诊断（BLOCKED 信息），不回归。
func TestInspectLegacyV3(t *testing.T) {
	o := defaultV4Opts()
	o.blockedActive = true
	o.blockedBy = "Coder"
	o.blockedReason = "缺少批准的 Contract"
	r := setupV4(t, o)
	// 覆写为 schema v3（共享核心字段），确认版本无关读取不回归。
	v3 := `schema_version: 3
task_id: demo
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  reservations: {}
review:
  status: NOT_REQUESTED
  target: ""
owner:
  status: PENDING
  review_target: ""
delivery:
  status: NOT_RUN
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: true
  by: Coder
  reason: 缺少批准的 Contract
`
	r.write(".agent/tasks/demo/state.yaml", v3)
	r.commit("v3 state")
	r.syncOrigin("feature/agent-workflow-v2")

	d, err := Inspect(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("Inspect V3 失败: %v", err)
	}
	if !d.SchemaKnown || d.SchemaVersion != SchemaV3 {
		t.Fatalf("SchemaKnown=%t Version=%d, 期望 V3", d.SchemaKnown, d.SchemaVersion)
	}
	if !d.BlockedActive || d.BlockedReason != "缺少批准的 Contract" {
		t.Fatalf("V3 BLOCKED 信息 = %+v", d)
	}
}

func TestCheckRecoveryBlockedActive(t *testing.T) {
	o := defaultV4Opts()
	o.blockedActive = true
	o.blockedBy = "Coder"
	o.blockedReason = "缺少批准的 Contract"
	r := setupV4(t, o)

	rep, err := CheckRecovery(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("CheckRecovery 失败: %v", err)
	}
	if rep.CanContinue {
		t.Fatal("ACTIVE BLOCKED 不得报告可继续")
	}
	if !rep.BlockedActive {
		t.Fatal("BlockedActive 应为 true")
	}
}

func TestCheckRecoveryUnblockedGatePass(t *testing.T) {
	o := defaultV4Opts() // contract NOT_REQUIRED、无资源 → coder-start PASS
	r := setupV4(t, o)

	rep, err := CheckRecovery(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("CheckRecovery 失败: %v", err)
	}
	if !rep.CanContinue {
		t.Fatalf("解除 BLOCKED 且入口 Gate PASS 应可继续，实际 %+v", rep)
	}
	if rep.NextGate != "coder-start" {
		t.Fatalf("NextGate = %q, 期望 coder-start", rep.NextGate)
	}
}

func TestCheckRecoveryUnblockedGateFail(t *testing.T) {
	o := defaultV4Opts()
	o.contractStatus = ContractPending // coder-start 应 FAIL（Contract 未生效）
	r := setupV4(t, o)

	rep, err := CheckRecovery(r.validator(), ".agent/tasks/demo")
	if err != nil {
		t.Fatalf("CheckRecovery 失败: %v", err)
	}
	if rep.CanContinue {
		t.Fatal("解除 BLOCKED 但 Gate FAIL，不得报告可继续")
	}
	if len(rep.GateChecks) != 1 || rep.GateChecks[0].Result.Status != StatusFail {
		t.Fatalf("期望 coder-start FAIL，实际 %+v", rep)
	}
}
