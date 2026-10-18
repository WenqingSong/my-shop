package workflow

import "fmt"

// 本文件实现 Workflow V4 正式 Handoff 协议的类型、角色/工作流枚举与结构校验。
//
// Handoff 只表达「交接信息」，绝不驱动 Agent 自动调度：NEXT_ROLE / NEXT_ACTION 是
// 持久化交接事实，不是可触发调度器的状态字段。下一角色由 Owner 手动启动，入口 Gate 校验
// 通过后才进入正式职责执行（见 handoff_gate.go）。
//
// 审批权威仍以 State + Git Evidence 为准（版本化 Evidence Snapshot + 不可变来源 Commit），
// 禁止仅以可编辑的布尔值（如 prepared 一个 bool）证明交接有效。事件日志仅作诊断记录。

// HandoffScope 是 Handoff 的作用域类型（所属 Task 或 Intake）。
type HandoffScope = string

const (
	HandoffScopeTask   HandoffScope = "task"
	HandoffScopeIntake HandoffScope = "intake"
)

// handoffScopeSet 是合法 Handoff 作用域集合。
var handoffScopeSet = map[HandoffScope]struct{}{
	HandoffScopeTask:   {},
	HandoffScopeIntake: {},
}

// ValidHandoffScope 报告 s 是否为合法 Handoff 作用域。
func ValidHandoffScope(s string) bool {
	_, ok := handoffScopeSet[s]
	return ok
}

// 固定角色（Owner 是人类，其余为 Agent；见 Workflow V4 冻结边界）。
// 这些是 Handoff target_role / ack.acked_by 的合法枚举。
const (
	RoleOwner         = "Owner"
	RoleDiscovery     = "Discovery"
	RoleTaskBuilder   = "TaskBuilder"
	RoleAnalyst       = "Analyst"
	RoleArchitect     = "Architect"
	RoleCoder         = "Coder"
	RoleDocsSpecWriter = "DocsSpecWriter"
	RoleCleaner       = "Cleaner"
	RoleOwnerGate     = "OwnerGate"
	RoleCollector     = "Collector"
	RoleAssembler     = "Assembler"
	RoleMerger        = "Merger"
)

// roleSet 是合法角色集合。
var roleSet = map[string]struct{}{
	RoleOwner:          {},
	RoleDiscovery:      {},
	RoleTaskBuilder:    {},
	RoleAnalyst:        {},
	RoleArchitect:      {},
	RoleCoder:          {},
	RoleDocsSpecWriter: {},
	RoleCleaner:        {},
	RoleOwnerGate:      {},
	RoleCollector:      {},
	RoleAssembler:      {},
	RoleMerger:         {},
}

// ValidRole 报告 s 是否为合法固定角色。
func ValidRole(s string) bool {
	_, ok := roleSet[s]
	return ok
}

// WorkflowKind 是 V4 固定工作流的合法枚举（含 Program 的 COORDINATED）。
// 与 TaskType（NORMAL/COMPLEX/DESIGN）不同：WorkflowKind 额外包含 COORDINATED，
// 用于 Handoff.workflow 表达「对应工作流」这一更宽泛的交接事实。
type WorkflowKind = string

const (
	WorkflowNormal      WorkflowKind = "NORMAL"
	WorkflowComplex     WorkflowKind = "COMPLEX"
	WorkflowDesign      WorkflowKind = "DESIGN"
	WorkflowCoordinated WorkflowKind = "COORDINATED"
)

// workflowSet 是合法工作流集合。
var workflowSet = map[WorkflowKind]struct{}{
	WorkflowNormal:      {},
	WorkflowComplex:     {},
	WorkflowDesign:      {},
	WorkflowCoordinated: {},
}

// ValidWorkflow 报告 s 是否为合法 V4 固定工作流。
func ValidWorkflow(s string) bool {
	_, ok := workflowSet[s]
	return ok
}

// EvidenceItem 是 Evidence Snapshot 中的一条不可变证据引用。
// SHA 是证据所在 Evidence Commit（完整十六进制），Path 是该 commit 内证据文件的相对路径
// （可为空，表示仅引用 commit 本身）。
type EvidenceItem struct {
	Kind string `yaml:"kind"` // 证据类别（opaque non-empty，如 contract / review / design / discovery-report / owner-decision）
	SHA  string `yaml:"sha"`  // 不可变 Evidence Commit SHA
	Path string `yaml:"path"` // 该 commit 内证据文件相对路径；可为空
}

// EvidenceSnapshot 是版本化 Evidence Snapshot。
// 交接有效性必须由版本化快照证明（version > 0 + 每条 item 的不可变 SHA），
// 不能仅靠可编辑的布尔值。
type EvidenceSnapshot struct {
	Version int            `yaml:"version"`
	Items   []EvidenceItem `yaml:"items"`
}

// HandoffAck 记录下一角色对 Handoff 的确认（ACK）对象及版本。
// 它是「ACK 与交接目标一致」的机器可校验锚点：target 必须等于 Handoff.source_commit。
type HandoffAck struct {
	Version int    `yaml:"version"`   // ACK 协议版本（必须为正数）
	AckedBy string `yaml:"acked_by"`  // ACK 角色（固定角色枚举）
	Target  string `yaml:"target"`    // 被 ACK 的 Handoff 来源 Commit（== source_commit）
}

// Handoff 记录 V4 正式交接协议的持久事实。
// 字段含义：
//   - Prepared：是否已准备好交接（仅事实标记，不单独构成有效性证明）；
//   - Scope/ScopeID：所属 Task 或 Intake；
//   - NextRole：目标角色（NEXT_ROLE，仅交接信息，不驱动调度）；
//   - Workflow：对应工作流（NORMAL/COMPLEX/DESIGN/COORDINATED）；
//   - Evidence：需要接收的版本化 Evidence Snapshot；
//   - NextAction：下一角色需执行的已批准动作（NEXT_ACTION，仅交接信息）；
//   - SourceCommit：不可变来源 Commit（Handoff Artifact 所在已提交快照）；
//   - Ack：ACK 对象及版本（下一角色持久化，出口 Gate 不得要求）；
//   - Entry：执行入口。
type Handoff struct {
	Prepared     bool              `yaml:"prepared"`
	Scope        HandoffScope      `yaml:"scope"`
	ScopeID      string            `yaml:"scope_id"`
	NextRole     string            `yaml:"next_role"`
	Workflow     WorkflowKind      `yaml:"workflow"`
	Evidence     *EvidenceSnapshot `yaml:"evidence"`
	NextAction   string            `yaml:"next_action"`
	SourceCommit string            `yaml:"source_commit"`
	Ack          *HandoffAck       `yaml:"ack"`
	Entry        string            `yaml:"entry"`
}

// validateHandoffV4 校验 Handoff 的结构完整性（V4 专属，handoff 为 nil 表示 section 缺失，直接通过）。
// 只做字段/枚举/SHA 格式/ACK 一致性校验，不读取 Git（Git 证据真实性由 handoff_gate.go 运行时校验）。
func validateHandoffV4(h *Handoff) error {
	if h == nil {
		return nil
	}
	if !ValidHandoffScope(h.Scope) {
		return fmt.Errorf("非法的 handoff.scope=%q（期望 task | intake）", h.Scope)
	}
	if h.ScopeID == "" {
		return fmt.Errorf("handoff.scope_id 不得为空")
	}
	if !ValidRole(h.NextRole) {
		return fmt.Errorf("非法的 handoff.next_role=%q（期望固定角色枚举）", h.NextRole)
	}
	if !ValidWorkflow(h.Workflow) {
		return fmt.Errorf("非法的 handoff.workflow=%q（期望 NORMAL | COMPLEX | DESIGN | COORDINATED）", h.Workflow)
	}
	if err := validateEvidenceSnapshot(h.Evidence); err != nil {
		return err
	}
	if h.SourceCommit != "" && !validSHA(h.SourceCommit) {
		return fmt.Errorf("非法的 handoff.source_commit=%q（期望 40 或 64 位十六进制完整 Commit SHA）", h.SourceCommit)
	}
	if err := validateHandoffAck(h.Ack, h.SourceCommit); err != nil {
		return err
	}
	return nil
}

// validateEvidenceSnapshot 校验版本化 Evidence Snapshot 的结构完整性。
func validateEvidenceSnapshot(e *EvidenceSnapshot) error {
	if e == nil {
		return nil
	}
	if e.Version <= 0 {
		return fmt.Errorf("非法的 handoff.evidence.version=%d（必须为正数）", e.Version)
	}
	if len(e.Items) == 0 {
		return fmt.Errorf("handoff.evidence.items 不得为空（版本化 Evidence Snapshot 至少含一条证据）")
	}
	for i, item := range e.Items {
		if item.Kind == "" {
			return fmt.Errorf("handoff.evidence.items[%d].kind 不得为空", i)
		}
		if !validSHA(item.SHA) || item.SHA == "" {
			return fmt.Errorf("非法的 handoff.evidence.items[%d].sha=%q（期望完整 Commit SHA）", i, item.SHA)
		}
	}
	return nil
}

// validateHandoffAck 校验 ACK 对象的结构完整性与目标一致性。
// ACK 存在时：version 为正数、acked_by 为合法角色、target 必须等于 source_commit
// （ACK 与交接目标一致）。source_commit 为空时 target 也必须为空（尚无来源可 ACK）。
func validateHandoffAck(a *HandoffAck, sourceCommit string) error {
	if a == nil {
		return nil
	}
	if a.Version <= 0 {
		return fmt.Errorf("非法的 handoff.ack.version=%d（必须为正数）", a.Version)
	}
	if !ValidRole(a.AckedBy) {
		return fmt.Errorf("非法的 handoff.ack.acked_by=%q（期望固定角色枚举）", a.AckedBy)
	}
	if a.Target != sourceCommit {
		return fmt.Errorf("handoff.ack.target=%q 与 handoff.source_commit=%q 不一致（ACK 必须与交接目标一致）", a.Target, sourceCommit)
	}
	return nil
}

// handoffPreparedComplete 报告 Handoff 是否具备「已准备好」所需的全部字段（不含 Git 证据真实性）。
// 用于 Gate 的语义判定：prepared 为 true 但字段缺失时，结构校验已在 parse 阶段拦截；此处只做
// 「prepared 与必填字段齐备」的一致性检查，避免 prepared=true 但关键字段为空的残缺交接。
func handoffPreparedComplete(h *Handoff) bool {
	if h == nil || !h.Prepared {
		return false
	}
	return h.Scope != "" && h.ScopeID != "" && h.NextRole != "" && h.Workflow != "" &&
		h.Evidence != nil && h.NextAction != "" && h.SourceCommit != ""
}
