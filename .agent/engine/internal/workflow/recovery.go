package workflow

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件实现 Workflow V4 的最小可靠故障诊断与人工恢复入口（Task 04）。
//
// 职责边界：
//   - 事件日志读取与解析（Event Log 仅用于诊断，绝不参与审批权威计算）；
//   - 只读状态诊断（inspect），无法确定时明确输出 UNKNOWN；
//   - 只读恢复前提检查（recovery-check），复用现有 Validator Gate，绝不复制一套 Gate 引擎；
//   - 不修改 State、不修改事件日志、不自动清除 blocked.active、不启动任何 Agent、不代理 git 写操作。
//
// 事件格式与 Task 03 写入端冻结一致，双方通过本文件的解析规则对接。

// ---------------------------------------------------------------------------
// 事件日志读取协议
// ---------------------------------------------------------------------------
//
// 事件类型枚举（EventStart / EventHandoffPrepared / EventAck / EventBlocked /
// EventCompleted）与 scope 枚举（ScopeTask / ScopeProgram），以及合法值判定函数
// ValidEventType / ValidScopeKind，统一由 Task 03 写入端（event_writer.go）作为
// 唯一来源定义。本文件只读取与解析，不再维护第二套独立合法值表。

// EventType 是 Workflow V4 事件类型（string 别名，便于语义表达）。
type EventType = string

// ScopeKind 是事件作用域（string 别名，便于语义表达）。
type ScopeKind = string

// ParsedEvent 是单条已解析的 Workflow 事件（Recovery 内部诊断用）。
// 磁盘协议结构统一使用 Task 03 写入端的 Event；解析时先把 JSON 解码为 Event，
// 经过严格校验后再转换为 ParsedEvent。
// Line 是事件在日志文件中的 1-based 行号，便于定位非法记录。
type ParsedEvent struct {
	Line      int
	Timestamp time.Time
	Role      string
	Event     EventType
	ScopeKind ScopeKind
	ScopeID   string
	SHA       string
	Reason    string
}

// EventError 描述一条无法正确解析/校验的事件记录。
// 非法记录必须报告错误，调用方不得悄悄忽略后声称日志完整。
type EventError struct {
	Line    int
	Raw     string
	Problem string
}

func (e EventError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Problem)
	}
	return e.Problem
}

// TaskEventLogPath 返回 task 事件日志的固定路径（相对仓库根）。
func TaskEventLogPath(taskID string) string {
	return filepath.Join(".agent", "tasks", taskID, "events.md")
}

// ProgramEventLogPath 返回 program 事件日志的固定路径（相对仓库根）。
func ProgramEventLogPath(programID string) string {
	return filepath.Join(".agent", "programs", programID, "events.md")
}

// eventHeader 是事件日志允许的首行标题。
const eventHeader = "# Workflow Events"

// ParseEventLog 解析事件日志内容。
//
// 返回成功解析的事件列表与错误列表（可同时非空：部分记录合法、部分非法）。
// 错误列表非空表示日志不完整，调用方必须据此报告，不得忽略。
// 文件不存在 / 文件为空由调用方在上层区分，本函数对空内容返回 (nil, nil)。
func ParseEventLog(content string) ([]ParsedEvent, []EventError) {
	var events []ParsedEvent
	var errs []EventError
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == eventHeader {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			errs = append(errs, EventError{Line: lineNo, Raw: line, Problem: `非法记录：事件行必须以 "- " 开头后跟 JSON 对象`})
			continue
		}
		rawJSON := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		ev, err := parseEvent(rawJSON)
		if err != nil {
			errs = append(errs, EventError{Line: lineNo, Raw: line, Problem: err.Error()})
			continue
		}
		ev.Line = lineNo
		events = append(events, ev)
	}
	return events, errs
}

// parseEvent 解析并校验单条事件 JSON。返回 error 表示该条记录非法（不静默忽略）。
//
// 分两步：先做 Go JSON 解码（语法 + 未知字段 + 尾随数据），再复用写入端
// Event.Validate() 做完整协议有效性校验（与 Writer 同一套规则），两者不是同一件事——
// 解码成功不代表协议合法。
func parseEvent(rawJSON string) (ParsedEvent, error) {
	// 步骤 1：解码为统一磁盘协议结构 Event，拒绝未知字段与尾随数据。
	var wire Event
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return ParsedEvent{}, fmt.Errorf("非法 JSON: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return ParsedEvent{}, fmt.Errorf("非法 JSON：单行内存在多余数据")
	}

	// 步骤 2：复用写入端 Event.Validate()，保证 Reader 与 Writer 校验规则一致
	//（scope_kind / scope_id 格式 / 事件类型 / role / UTC 时间戳 / SHA 格式 / BLOCKED reason）。
	if err := wire.Validate(); err != nil {
		return ParsedEvent{}, err
	}

	// 步骤 3：转换为诊断层 ParsedEvent（解析后的 UTC 时间）。
	ts, err := time.Parse(time.RFC3339Nano, wire.Timestamp)
	if err != nil {
		// Validate 已保证时间戳合法，此分支仅作防御。
		return ParsedEvent{}, fmt.Errorf("非法 timestamp=%q（期望 RFC3339Nano）", wire.Timestamp)
	}
	return ParsedEvent{
		Timestamp: ts,
		Role:      wire.Role,
		Event:     wire.Event,
		ScopeKind: wire.ScopeKind,
		ScopeID:   wire.ScopeID,
		SHA:       wire.SHA,
		Reason:    wire.Reason,
	}, nil
}

// ReadTaskEventLog 读取 task 事件日志并解析。
// 文件不存在返回 (nil, nil, nil)（无事件，非错误）；其它读错误返回 error。
func ReadTaskEventLog(root, taskID string) ([]ParsedEvent, []EventError, error) {
	path := filepath.Join(root, TaskEventLogPath(taskID))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	events, errs := ParseEventLog(string(raw))
	return events, errs, nil
}

// scopeConsistencyErrors 校验事件作用域与 task 一致。不一致属于非法记录，必须报告。
func scopeConsistencyErrors(events []ParsedEvent, taskID string) []EventError {
	var errs []EventError
	for _, ev := range events {
		if ev.ScopeKind != ScopeTask {
			errs = append(errs, EventError{Line: ev.Line, Raw: "", Problem: fmt.Sprintf("事件 scope_kind=%q 与 task 作用域不一致", ev.ScopeKind)})
		}
		if ev.ScopeID != taskID {
			errs = append(errs, EventError{Line: ev.Line, Raw: "", Problem: fmt.Sprintf("事件 scope_id=%q 与 task_id=%q 不一致", ev.ScopeID, taskID)})
		}
	}
	return errs
}

// ---------------------------------------------------------------------------
// 状态诊断
// ---------------------------------------------------------------------------

// Anomaly 描述一处可观察的异常 / 中断情况（仅诊断，不自动处置）。
type Anomaly struct {
	Kind   string
	Detail string
}

// Diagnosis 是 workflow-status inspect 的结构化诊断结果。
type Diagnosis struct {
	TaskID          string
	SchemaVersion   int
	SchemaKnown     bool   // state.yaml 是否成功解析
	StateError      string // state 解析失败时的错误信息
	BlockedActive   bool
	BlockedBy       string
	BlockedReason   string
	Facts           []string
	Events          []ParsedEvent
	EventErrors     []EventError
	Anomalies       []Anomaly
	CandidateGates  []string // 可能需要重跑的入口 Gate
	NextGateCertain bool     // 是否能唯一确定下一 Gate
}

// Inspect 对指定 task 做只读诊断。
// 仅当 state.yaml 无法读取时返回 error；state 解析失败会写入 Diagnosis.StateError 并输出 UNKNOWN。
func Inspect(v *Validator, taskDir string) (Diagnosis, error) {
	d := Diagnosis{}
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	d.TaskID = taskSlug

	statePath := filepath.Join(v.Root, taskDir, "state.yaml")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return d, fmt.Errorf("读取 %s: %w", statePath, err)
	}
	s, perr := parseState(raw)
	if perr != nil {
		d.StateError = perr.Error()
		d.SchemaKnown = false
	} else {
		d.SchemaVersion = s.SchemaVersion
		d.SchemaKnown = true
		d.TaskID = s.TaskID
		d.BlockedActive = s.Blocked.Active
		d.BlockedBy = s.Blocked.By
		d.BlockedReason = s.Blocked.Reason
		d.Facts = append(d.Facts, collectStateFacts(s)...)
		d.CandidateGates, d.NextGateCertain = candidateGates(s)
	}

	events, eerrs, eerr := ReadTaskEventLog(v.Root, d.TaskID)
	if eerr != nil {
		d.EventErrors = append(d.EventErrors, EventError{Problem: fmt.Sprintf("读取事件日志失败: %v", eerr)})
	} else {
		d.Events = events
		d.EventErrors = append(d.EventErrors, eerrs...)
		d.EventErrors = append(d.EventErrors, scopeConsistencyErrors(events, d.TaskID)...)
	}

	d.Facts = append(d.Facts, collectGitFacts(v)...)
	d.Anomalies = analyze(v, s, perr == nil, events)
	return d, nil
}

// collectStateFacts 汇总 State 能够证明的决策事实（state.yaml 是持久决策事实记录）。
func collectStateFacts(s State) []string {
	var facts []string
	if s.TaskType != "" { // V4 专属；V2/V3 归一化后为空，不输出
		facts = append(facts, fmt.Sprintf("task_type=%s", s.TaskType))
	}
	if s.Git != nil {
		facts = append(facts,
			fmt.Sprintf("git.parent_branch=%s", s.Git.ParentBranch),
			fmt.Sprintf("git.base_commit=%s", s.Git.BaseCommit),
			fmt.Sprintf("git.task_branch=%s", s.Git.TaskBranch),
		)
	}
	facts = append(facts, fmt.Sprintf("contract.status=%s", s.Contract.Status))
	if s.Contract.Target != "" {
		facts = append(facts, fmt.Sprintf("contract.target=%s", s.Contract.Target))
	}
	facts = append(facts, fmt.Sprintf("review.status=%s", s.Review.Status))
	if s.Review.Target != "" {
		facts = append(facts, fmt.Sprintf("review.target=%s", s.Review.Target))
	}
	facts = append(facts, fmt.Sprintf("owner.status=%s", s.Owner.Status))
	if s.Owner.ReviewTarget != "" {
		facts = append(facts, fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget))
	}
	facts = append(facts, fmt.Sprintf("delivery.status=%s", s.Delivery.Status))
	if s.Delivery.FeatureHead != "" {
		facts = append(facts, fmt.Sprintf("delivery.feature_head=%s", s.Delivery.FeatureHead))
	}
	if s.Delivery.DevelopBase != "" {
		facts = append(facts, fmt.Sprintf("delivery.develop_base=%s", s.Delivery.DevelopBase))
	}
	if s.Blocked.Active {
		facts = append(facts, "blocked.active=true")
		if s.Blocked.By != "" {
			facts = append(facts, fmt.Sprintf("blocked.by=%s", s.Blocked.By))
		}
		if s.Blocked.Reason != "" {
			facts = append(facts, fmt.Sprintf("blocked.reason=%s", s.Blocked.Reason))
		}
	}
	if s.Handoff != nil && (s.Handoff.Prepared || s.Handoff.Entry != "" || s.Handoff.SourceCommit != "") {
		facts = append(facts, fmt.Sprintf("handoff.prepared=%t", s.Handoff.Prepared))
		if s.Handoff.Scope != "" && s.Handoff.ScopeID != "" {
			facts = append(facts, fmt.Sprintf("handoff.scope=%s/%s", s.Handoff.Scope, s.Handoff.ScopeID))
		}
		if s.Handoff.NextRole != "" {
			facts = append(facts, fmt.Sprintf("handoff.next_role=%s", s.Handoff.NextRole))
		}
		if s.Handoff.Workflow != "" {
			facts = append(facts, fmt.Sprintf("handoff.workflow=%s", s.Handoff.Workflow))
		}
		if s.Handoff.SourceCommit != "" {
			facts = append(facts, fmt.Sprintf("handoff.source_commit=%s", s.Handoff.SourceCommit))
		}
		if s.Handoff.Ack != nil {
			facts = append(facts, fmt.Sprintf("handoff.ack.acked_by=%s", s.Handoff.Ack.AckedBy))
		}
		if s.Handoff.Entry != "" {
			facts = append(facts, fmt.Sprintf("handoff.entry=%s", s.Handoff.Entry))
		}
	}
	return facts
}

// collectGitFacts 汇总可观察的 Git 事实；git 不可用时明确 UNKNOWN，绝不猜测。
func collectGitFacts(v *Validator) []string {
	var facts []string
	if v.Git == nil {
		return []string{"git=UNKNOWN（未提供 Git 接口）"}
	}
	if branch, err := v.Git.CurrentBranch(); err == nil {
		facts = append(facts, fmt.Sprintf("current_branch=%s", branch))
	} else {
		facts = append(facts, fmt.Sprintf("current_branch=UNKNOWN（%v）", err))
	}
	if head, err := v.Git.RevParse("HEAD"); err == nil {
		facts = append(facts, fmt.Sprintf("HEAD=%s", head))
	} else {
		facts = append(facts, fmt.Sprintf("HEAD=UNKNOWN（%v）", err))
	}
	if dirty, err := v.Git.StatusPorcelain(); err == nil {
		if len(dirty) > 0 {
			facts = append(facts, "working_tree=dirty")
		} else {
			facts = append(facts, "working_tree=clean")
		}
	} else {
		facts = append(facts, fmt.Sprintf("working_tree=UNKNOWN（%v）", err))
	}
	return facts
}

// analyze 基于 State + 事件日志推导可观察异常。stateOK 为 false 表示 state 无法解析。
func analyze(v *Validator, s State, stateOK bool, events []ParsedEvent) []Anomaly {
	var anomalies []Anomaly
	if !stateOK {
		anomalies = append(anomalies, Anomaly{Kind: "STATE_UNPARSEABLE", Detail: "state.yaml 无法解析，无法确定状态与恢复位置"})
		return anomalies
	}

	// State 为 BLOCKED 但无 BLOCKED 事件。
	if s.Blocked.Active && !hasEventType(events, EventBlocked) {
		anomalies = append(anomalies, Anomaly{Kind: "BLOCKED_WITHOUT_EVENT", Detail: "State 为 BLOCKED，但事件日志中没有 BLOCKED 事件"})
	}

	if len(events) > 0 {
		last := events[len(events)-1]
		// 最后事件是 START，且未观察到 COMPLETED（不能单凭此证明 Agent 崩溃）。
		if last.Event == EventStart && !hasEventType(events, EventCompleted) {
			anomalies = append(anomalies, Anomaly{Kind: "START_WITHOUT_COMPLETED", Detail: "最后事件为 START 且未观察到 COMPLETED（不能仅据此证明 Agent 崩溃）"})
		}
		// State 与事件记录矛盾。
		if !s.Blocked.Active && last.Event == EventBlocked {
			anomalies = append(anomalies, Anomaly{Kind: "STATE_EVENT_CONFLICT", Detail: "State 未阻塞，但最后事件为 BLOCKED（阻塞可能已解除但未记录）"})
		}
		if s.Blocked.Active && last.Event == EventCompleted {
			anomalies = append(anomalies, Anomaly{Kind: "STATE_EVENT_CONFLICT", Detail: "State 为 BLOCKED，但最后事件为 COMPLETED"})
		}
	}

	// 交接协议：HANDOFF_PREPARED 后无 ACK；ACK 后无 COMPLETED。
	for i, ev := range events {
		if ev.Event == EventHandoffPrepared && !hasEventTypeAfter(events, i, EventAck) {
			anomalies = append(anomalies, Anomaly{Kind: "HANDOFF_WITHOUT_ACK", Detail: fmt.Sprintf("第 %d 条为 HANDOFF_PREPARED，但之后没有 ACK", ev.Line)})
		}
		if ev.Event == EventAck && !hasEventTypeAfter(events, i, EventCompleted) {
			anomalies = append(anomalies, Anomaly{Kind: "ACK_WITHOUT_COMPLETED", Detail: fmt.Sprintf("第 %d 条为 ACK，但之后没有 COMPLETED", ev.Line)})
		}
	}

	// 工作区非 clean。
	if v != nil && v.Git != nil {
		if dirty, err := v.Git.StatusPorcelain(); err == nil && len(dirty) > 0 {
			anomalies = append(anomalies, Anomaly{Kind: "WORKTREE_DIRTY", Detail: fmt.Sprintf("工作区非 clean（%d 处变更）", len(dirty))})
		}
	}
	return anomalies
}

func hasEventType(events []ParsedEvent, typ EventType) bool {
	for _, e := range events {
		if e.Event == typ {
			return true
		}
	}
	return false
}

func hasEventTypeAfter(events []ParsedEvent, idx int, typ EventType) bool {
	for i := idx + 1; i < len(events); i++ {
		if events[i].Event == typ {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 候选入口 Gate 推导（纯函数，基于 State 事实，绝不猜测下一角色）
// ---------------------------------------------------------------------------

// candidateGates 根据 State 事实推导可能需要重跑的入口 Gate。
// 返回候选 Gate 列表（按推荐顺序）与是否能唯一确定（certain）。
// 无法唯一确定时返回多个候选或 nil，交由调用方输出 UNKNOWN。
func candidateGates(s State) ([]string, bool) {
	// DESIGN 工作流：候选 Gate 推导与 NORMAL/COMPLEX 不同（无 Coder/Deliverer 环节）。
	if s.TaskType == TaskTypeDesign {
		return candidateDesignGates(s)
	}

	// Contract 尚未生效：需先补齐 Contract 授权，入口是 coder-start。
	if s.Contract.Status == ContractPending || s.Contract.Status == ContractRejected {
		return []string{"coder-start"}, true
	}

	switch s.Review.Status {
	case ReviewNotRequested:
		// 尚未发起 Review：处于实现阶段，入口是 coder-start。
		return []string{"coder-start"}, true
	case ReviewPending:
		// 已发起 Review：入口是 cleaner-start。
		return []string{"cleaner-start"}, true
	case ReviewChangesRequired:
		// Review 要求修改：可能是 Coder 修复后重新发起 Review，也可能是重新 Review，无法唯一确定。
		return []string{"coder-start", "cleaner-start"}, false
	case ReviewClean:
		switch {
		case s.Owner.Status == OwnerAccepted:
			if s.Delivery.Status == DeliveryPass {
				return []string{"merge-ready"}, true
			}
			return []string{"delivery-start"}, true
		case s.Owner.Status == OwnerRejected:
			// Owner 拒绝：可能是 Coder 修复，也可能是 Owner 重新决策，无法唯一确定。
			return []string{"owner-gate-start", "coder-start"}, false
		default:
			// OwnerPending / OwnerNotRequired：等待 Owner 决策，入口是 owner-gate-start。
			return []string{"owner-gate-start"}, true
		}
	default:
		// review.status 未知/非法：无法确定下一 Gate。
		return nil, false
	}
}

// candidateDesignGates 为 DESIGN 任务推导候选入口 Gate（保守：无法唯一确定即返回 UNKNOWN）。
//
// DESIGN 固定流程无 Coder/Deliverer 环节，其 State 事实推导不能复用 NORMAL/COMPLEX 的
// coder-start/delivery-start/merge-ready 语义，必须映射到 DESIGN 专属 Gate：
//
//	架构未批准（NOT_REQUIRED/PENDING/REJECTED）→ Architect 继续或等待 Owner 决策，
//	无法从 design.status 唯一区分，返回多候选（UNKNOWN）；
//	架构 APPROVED 且 review 未发起 → design-docsspecwriter-start；
//	review PENDING → design-cleaner-start；
//	review CHANGES_REQUIRED → 返工与重审无法唯一区分，返回多候选（UNKNOWN）；
//	review CLEAN 且 owner 未 ACCEPT → design-owner-gate-start；
//	review CLEAN 且 owner ACCEPTED → design-final-approval。
func candidateDesignGates(s State) ([]string, bool) {
	if s.Design == nil {
		return nil, false
	}
	d := s.Design
	switch d.Status {
	case DesignApproved:
		switch s.Review.Status {
		case ReviewNotRequested:
			return []string{"design-docsspecwriter-start"}, true
		case ReviewPending:
			return []string{"design-cleaner-start"}, true
		case ReviewChangesRequired:
			return []string{"design-docsspecwriter-start", "design-cleaner-start"}, false
		case ReviewClean:
			if s.Owner.Status == OwnerAccepted {
				return []string{"design-final-approval"}, true
			}
			return []string{"design-owner-gate-start"}, true
		default:
			return nil, false
		}
	case DesignRejected:
		// Owner 拒绝架构：Architect 返工或等待 Owner 重新决策，无法唯一确定。
		return []string{"design-architect-start", "design-architecture-review"}, false
	case DesignNotRequired, DesignPending:
		// 架构未批准：Architect 继续或等待 Owner 决策，无法唯一确定。
		return []string{"design-architect-start", "design-architecture-review"}, false
	default:
		return nil, false
	}
}

// ---------------------------------------------------------------------------
// 恢复前提检查（只读）
// ---------------------------------------------------------------------------

// GateCheck 是单个入口 Gate 的运行结果。
type GateCheck struct {
	Gate   string
	Result Result
}

// RecoveryReport 是恢复前提检查的结构化结果。
type RecoveryReport struct {
	TaskID          string
	BlockedActive   bool
	BlockedBy       string
	BlockedReason   string
	CanContinue     bool
	NextGate        string // 唯一确定的下一 Gate；"" 表示 UNKNOWN
	NextGateCertain bool
	GateChecks      []GateCheck
	UnknownReason   string
}

// CheckRecovery 执行只读恢复前提检查。
//
// 规则：
//   - blocked.active=true → 不得报告可以继续（CanContinue=false）；
//   - 解除 BLOCKED 后，必须重新运行对应入口 Gate，不能只检查布尔值；
//   - 无法唯一确定下一 Gate 时，返回候选 Gate 运行结果并标记 UNKNOWN。
//
// 返回 error 表示读取/运行级错误（exit 2）；Gate FAIL 属于业务结果（CanContinue=false），不是 error。
func CheckRecovery(v *Validator, taskDir string) (RecoveryReport, error) {
	r := RecoveryReport{}
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	r.TaskID = taskSlug

	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return r, fmt.Errorf("读取 state 失败: %s", res.Error)
	}
	r.TaskID = s.TaskID
	r.BlockedActive = s.Blocked.Active
	r.BlockedBy = s.Blocked.By
	r.BlockedReason = s.Blocked.Reason

	if issues := schemaValidation(s); len(issues) > 0 {
		return r, fmt.Errorf("state schema 非法: %s", issues[0].String())
	}

	// ACTIVE BLOCKED：不允许继续执行。
	if s.Blocked.Active {
		r.CanContinue = false
		r.NextGate = ""
		r.NextGateCertain = false
		return r, nil
	}

	gates, certain := candidateGates(s)
	r.NextGateCertain = certain
	if len(gates) == 0 {
		r.CanContinue = false
		r.UnknownReason = "无法从 State 事实唯一确定下一入口 Gate（UNKNOWN）"
		return r, nil
	}

	for _, g := range gates {
		r.GateChecks = append(r.GateChecks, GateCheck{Gate: g, Result: runGate(v, g, taskDir)})
	}

	if certain && len(gates) == 1 {
		r.NextGate = gates[0]
		r.CanContinue = r.GateChecks[0].Result.Status == StatusPass
		return r, nil
	}

	// 多个候选：报告各候选结果，但不宣称唯一恢复入口。
	r.NextGate = ""
	r.CanContinue = false
	r.UnknownReason = "存在多个候选入口 Gate，无法唯一确定下一 Gate（UNKNOWN）"
	return r, nil
}

// runGate 复用现有 Validator Gate 校验接口，不复制独立 Gate 引擎。
func runGate(v *Validator, gate, taskDir string) Result {
	switch gate {
	case "coder-start":
		return v.GateCoderStart(taskDir)
	case "cleaner-start":
		return v.GateCleanerStart(taskDir)
	case "owner-gate-start":
		return v.GateOwnerGateStart(taskDir)
	case "delivery-start":
		return v.GateDeliveryStart(taskDir)
	case "merge-ready":
		return v.GateMergeReady(taskDir)
	case "design-architect-start":
		return v.GateDesignArchitectStart(taskDir)
	case "design-architecture-review":
		return v.GateDesignArchitectureReview(taskDir)
	case "design-docsspecwriter-start":
		return v.GateDesignDocsSpecWriterStart(taskDir)
	case "design-cleaner-start":
		return v.GateDesignCleanerStart(taskDir)
	case "design-owner-gate-start":
		return v.GateDesignOwnerGateStart(taskDir)
	case "design-final-approval":
		return v.GateDesignFinalApproval(taskDir)
	default:
		return Result{Status: StatusError, Error: fmt.Sprintf("未知 gate %q", gate)}
	}
}
