package workflow

import "regexp"

// 本文件定义 Workflow V2 的 state.yaml 结构化 schema 与合法枚举。
//
// V2 与 V1 的根本差异：删除全局 phase 状态机（13 值 phase、Transition Authority、
// STALE 持久状态），只保存 Git 无法推导的工作流决策事实。生命周期由 Owner 与 Gate 控制，
// 状态有效性由 immutable Evidence Snapshot + Neutral Tail 机械推导（INV-1 ~ INV-7）。

// SchemaV3 是当前 Workflow V2（major）任务的 state schema_version。
//
// schema version 与 Workflow major version 是不同概念：本轮仍是 Workflow V2，
// 只是 state schema 从 2 演进到 3（Generic Shared Resource Reservation）。
const SchemaV3 = 3

// SchemaV2 是历史任务的 state schema_version，仅作 Legacy Read Compatibility。
// 读取后 runtime normalize 为 generic reservations，绝不写回磁盘。
const SchemaV2 = 2

// SchemaV4 是 Workflow V4 任务的 state schema_version。
// V4 在保留 V2/V3 核心决策事实语义的基础上，新增 Task 工作类型、Git 归属/基线、
// Contract Revision、DESIGN、Handoff/ACK、Event Log 定位与 Program 归属等 V4 专属事实。
// V4 不引入全局 phase / STALE / DONE，也不把 NEXT_ROLE 当作可驱动 Agent 调度的状态字段。
const SchemaV4 = 4

// resourceKindPattern 约束 resource kind 必须是稳定机器 key（lower_snake_case）。
// 非法 kind 是 Workflow State / Config 的 ERROR（exit 2），不是业务 Gate FAIL。
var resourceKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidResourceKind 报告 kind 是否为合法 resource kind。
func ValidResourceKind(kind string) bool {
	return resourceKindPattern.MatchString(kind)
}

// TaskType 是 V4 Task 工作流模式的合法枚举。
//
// NORMAL：无 Contract/Design 前置，直接 Coder 实现的普通任务；
// COMPLEX：需要 Analyst 出 Contract 的复杂任务；
// DESIGN：以产出架构 Design 为目标的 DESIGN 工作流任务。
//
// Program 是独立协同单元（COORDINATED），不属于 Task 工作流模式，不得通过 task_type 冒充；
// 其互操作通过独立的 program.id / program.version 引用表达（见 ProgramRef），
// Program 自身的状态解析属于 Task 12。
type TaskType = string

const (
	TaskTypeNormal  TaskType = "NORMAL"
	TaskTypeComplex TaskType = "COMPLEX"
	TaskTypeDesign  TaskType = "DESIGN"
)

var taskTypeSet = map[TaskType]struct{}{
	TaskTypeNormal:  {},
	TaskTypeComplex: {},
	TaskTypeDesign:  {},
}

// ValidTaskType 报告 s 是否为合法 V4 task_type。
func ValidTaskType(s string) bool {
	_, ok := taskTypeSet[s]
	return ok
}

// DesignStatus 是 V4 design.status（架构审批）的合法枚举。
type DesignStatus = string

const (
	DesignNotRequired DesignStatus = "NOT_REQUIRED"
	DesignPending     DesignStatus = "PENDING"
	DesignApproved    DesignStatus = "APPROVED"
	DesignRejected    DesignStatus = "REJECTED"
)

var designStatusSet = map[DesignStatus]struct{}{
	DesignNotRequired: {},
	DesignPending:     {},
	DesignApproved:    {},
	DesignRejected:    {},
}

// ValidDesignStatus 报告 s 是否为合法 V4 design.status。
func ValidDesignStatus(s string) bool {
	_, ok := designStatusSet[s]
	return ok
}

// ContractStatus 是 contract.status 的合法枚举。
type ContractStatus = string

const (
	ContractPending     ContractStatus = "PENDING"
	ContractApproved    ContractStatus = "APPROVED"
	ContractRejected    ContractStatus = "REJECTED"
	ContractNotRequired ContractStatus = "NOT_REQUIRED"
)

var contractStatusSet = map[ContractStatus]struct{}{
	ContractPending:     {},
	ContractApproved:    {},
	ContractRejected:    {},
	ContractNotRequired: {},
}

// ValidContractStatus 报告 s 是否为合法 contract.status。
func ValidContractStatus(s string) bool {
	_, ok := contractStatusSet[s]
	return ok
}

// ReviewStatus 是 review.status 的合法枚举。
type ReviewStatus = string

const (
	ReviewNotRequested    ReviewStatus = "NOT_REQUESTED"
	ReviewPending         ReviewStatus = "PENDING"
	ReviewClean           ReviewStatus = "CLEAN"
	ReviewChangesRequired ReviewStatus = "CHANGES_REQUIRED"
)

var reviewStatusSet = map[ReviewStatus]struct{}{
	ReviewNotRequested:    {},
	ReviewPending:         {},
	ReviewClean:           {},
	ReviewChangesRequired: {},
}

// ValidReviewStatus 报告 s 是否为合法 review.status。
func ValidReviewStatus(s string) bool {
	_, ok := reviewStatusSet[s]
	return ok
}

// OwnerStatus 是 owner.status 的合法枚举。
type OwnerStatus = string

const (
	OwnerPending     OwnerStatus = "PENDING"
	OwnerAccepted    OwnerStatus = "ACCEPTED"
	OwnerRejected    OwnerStatus = "REJECTED"
	OwnerNotRequired OwnerStatus = "NOT_REQUIRED"
)

var ownerStatusSet = map[OwnerStatus]struct{}{
	OwnerPending:     {},
	OwnerAccepted:    {},
	OwnerRejected:    {},
	OwnerNotRequired: {},
}

// ValidOwnerStatus 报告 s 是否为合法 owner.status。
func ValidOwnerStatus(s string) bool {
	_, ok := ownerStatusSet[s]
	return ok
}

// DeliveryStatus 是 delivery.status 的合法枚举。
type DeliveryStatus = string

const (
	DeliveryNotRun  DeliveryStatus = "NOT_RUN"
	DeliveryPass    DeliveryStatus = "PASS"
	DeliveryFail    DeliveryStatus = "FAIL"
	DeliveryBlocked DeliveryStatus = "BLOCKED"
)

var deliveryStatusSet = map[DeliveryStatus]struct{}{
	DeliveryNotRun:  {},
	DeliveryPass:    {},
	DeliveryFail:    {},
	DeliveryBlocked: {},
}

// ValidDeliveryStatus 报告 s 是否为合法 delivery.status。
func ValidDeliveryStatus(s string) bool {
	_, ok := deliveryStatusSet[s]
	return ok
}

// Contract 记录 Owner 批准的 Contract 绑定事实。
//
// V4 扩展 Revision / History 用于 Contract Revision（当前版本与历史版本关联）。
// 审批权威始终是 immutable target（Evidence Commit SHA），不是任何可编辑的 approved_by 文本；
// Revision 只是版本编号，不替代 target 的证据绑定。Revision/History 的实际 Gate 判定由 Task 2 实现。
type Contract struct {
	Status   ContractStatus     `yaml:"status"`
	Target   string             `yaml:"target"`             // 包含最终批准 contract.md 的 Evidence Commit SHA；"" 表示未绑定
	Revision int                `yaml:"revision,omitempty"` // V4：当前批准版本号；0 表示未版本化
	History  []ContractRevision `yaml:"history,omitempty"`  // V4：历史版本关联（按版本升序）
}

// ContractRevision 是 contract.history 中的单个历史版本关联。
type ContractRevision struct {
	Revision int    `yaml:"revision"`
	Target   string `yaml:"target"`
}

// Resources 声明 Task 需要的全局共享资源（只声明，不自证满足）。
//
// P2 起泛化为 resource kind → reservation value 列表，不再认识任何具体资源类型。
// 每个 value 对 Workflow Core 都是 opaque non-empty string；具体格式正确性属于 Project Policy。
// schema v2 的 migrations / error_code_domains 由 state_load.go 的兼容层 normalize 到此结构。
type Resources struct {
	Reservations map[string][]string `yaml:"reservations"`
}

// Review 是 Cleaner 审查结论，绑定 immutable implementation Evidence Commit。
type Review struct {
	Status ReviewStatus `yaml:"status"`
	Target string       `yaml:"target"` // C1 implementation Evidence Commit SHA
}

// Owner 记录 Owner 在 OwnerGate Session 中的核心逻辑决策，绑定当前 CLEAN review target。
type Owner struct {
	Status       OwnerStatus `yaml:"status"`
	ReviewTarget string      `yaml:"review_target"` // 绑定 Owner 接受的 CLEAN review.target
}

// Delivery 记录 Deliverer 的集成验证结论，绑定三个不可变 SHA。
type Delivery struct {
	Status       DeliveryStatus `yaml:"status"`
	ReviewTarget string         `yaml:"review_target"` // 被验证的业务实现 C1
	FeatureHead  string         `yaml:"feature_head"`  // 实际参与集成验证的 feature snapshot
	DevelopBase  string         `yaml:"develop_base"`  // 验证时的 shared develop base
}

// Blocked 记录真实执行阻塞，不承担生命周期作用。
type Blocked struct {
	Active bool   `yaml:"active"`
	By     string `yaml:"by"`
	Reason string `yaml:"reason"`
}

// GitBaseline 记录 V4 Task 初始化时确认的 Git 归属与基线（初始化决策事实）。
//
// parent_branch / base_commit / task_branch 是 Task 初始化那一刻由 Owner/TaskBuilder 固定的持久事实，
// 不得在后续读取时通过「当前分支」重新解释。Git SHA 真实性、分支存在性与实际 HEAD
// 仍由后续 Gate 从 Git 实时核验（本结构不取代 Git 校验）。
type GitBaseline struct {
	ParentBranch string `yaml:"parent_branch"`
	BaseCommit   string `yaml:"base_commit"`
	TaskBranch   string `yaml:"task_branch"`
}

// Design 记录 V4 DESIGN 工作流的架构审批与定版事实。
// 设计文件不得被误归类为 review-neutral（见 paths.go 白名单，docs/design/* 属于 substantive）。
// DESIGN 工作流的实际执行 Gate 由 Phase 05 实现；本结构只提供数据表示与解析。
//
// 三个 Target 的职责（互不混同，均为不可变 Evidence Commit SHA）：
//   - approved_target：Owner 已批准架构的基线 Evidence Commit SHA；
//   - review_target：Cleaner 实际审查的设计交付快照 Evidence Commit SHA；
//   - final_target：Owner 最终批准的设计交付快照 Evidence Commit SHA。
//
// 最终批准时，后续 Gate 可校验 final_target 与已通过审查的交付对象（review_target）一致，
// 并核验已批准架构（approved_target）未发生未经授权的实质修改。
type Design struct {
	Status          DesignStatus `yaml:"status"`
	ApprovedVersion int          `yaml:"approved_version"`
	ApprovedTarget  string       `yaml:"approved_target"` // Owner 批准架构的 Evidence Commit SHA；"" 表示未绑定
	FinalVersion    int          `yaml:"final_version"`
	FinalTarget     string       `yaml:"final_target"` // Owner 最终批准的设计交付 Evidence Commit SHA；"" 表示未绑定
	ReviewTarget    string       `yaml:"review_target"` // Cleaner 审查的设计交付快照 Evidence Commit SHA；"" 表示未绑定
}

// EventLog 只记录事件日志的定位信息，不承载任何审批权威。
// 事件日志与决策状态分离：state.yaml 中只允许存定位引用，不允许把日志条目当作审批依据。
// 事件内容与写入机制属于 Task 3。
type EventLog struct {
	Path string `yaml:"path"`
}

// ProgramRef 记录 V4 Task 与 Program 互操作所需的公共标识与版本。
// 它只表达「本 Task 属于哪个 Program」，不承载 Program Registry、集成状态或子 Task 状态；
// Program 的独立 State Schema 与解析属于 Task 12。
type ProgramRef struct {
	ID      string `yaml:"id"`
	Version int    `yaml:"version"`
}

// IntakeRef 记录本 Task 由哪个 Pre-Task Intake 初始化（仅归属引用，不承载 Intake 内部状态）。
// 它是 TaskBuilder 出口 Gate「未经 Discovery 与 Owner 决策不得初始化」的机器可校验锚点：
// 通过 intake.id 定位到 .agent/intake/<id>/taskbuilder-handoff.md，再校验其 Owner 决策、范围与
// Git 基线是否与当前 Task 的 state.yaml 一致。
type IntakeRef struct {
	ID string `yaml:"id"`
}

// State 是 Workflow V2/V3/V4 state.yaml 的统一内部表示。
//
// 核心字段（Contract/Resources/Review/Owner/Delivery/Blocked）在三版间共享，
// V2/V3 解析归一化到这些核心字段；V4 额外填充 V4 专属字段（TaskType/Git/Design/Handoff/EventLog/Program）。
// V2/V3 归一化后 V4 专属字段保持零值/nil，绝不写回磁盘。
type State struct {
	SchemaVersion int       `yaml:"schema_version"`
	TaskID        string    `yaml:"task_id"`
	Contract      Contract  `yaml:"contract"`
	Resources     Resources `yaml:"resources"`
	Review        Review    `yaml:"review"`
	Owner         Owner     `yaml:"owner"`
	Delivery      Delivery  `yaml:"delivery"`
	Blocked       Blocked   `yaml:"blocked"`

	// --- V4 专属字段（V2/V3 归一化后为零值/nil） ---
	TaskType TaskType     `yaml:"task_type,omitempty"`
	Git      *GitBaseline `yaml:"git,omitempty"`
	Design   *Design      `yaml:"design,omitempty"`
	Handoff  *Handoff     `yaml:"handoff,omitempty"`
	EventLog *EventLog    `yaml:"event_log,omitempty"`
	Program  *ProgramRef  `yaml:"program,omitempty"`
	Intake   *IntakeRef   `yaml:"intake,omitempty"`
}

// HasRequiredResources 报告 Task 是否声明了任何全局资源需求。
func (s State) HasRequiredResources() bool {
	return len(s.Resources.Reservations) > 0
}
