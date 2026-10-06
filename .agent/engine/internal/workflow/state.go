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

// resourceKindPattern 约束 resource kind 必须是稳定机器 key（lower_snake_case）。
// 非法 kind 是 Workflow State / Config 的 ERROR（exit 2），不是业务 Gate FAIL。
var resourceKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidResourceKind 报告 kind 是否为合法 resource kind。
func ValidResourceKind(kind string) bool {
	return resourceKindPattern.MatchString(kind)
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
type Contract struct {
	Status ContractStatus `yaml:"status"`
	Target string         `yaml:"target"` // 包含最终批准 contract.md 的 Evidence Commit SHA；"" 表示未绑定
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

// State 是 Workflow V2 state.yaml 的结构化表示。
type State struct {
	SchemaVersion int       `yaml:"schema_version"`
	TaskID        string    `yaml:"task_id"`
	Contract      Contract  `yaml:"contract"`
	Resources     Resources `yaml:"resources"`
	Review        Review    `yaml:"review"`
	Owner         Owner     `yaml:"owner"`
	Delivery      Delivery  `yaml:"delivery"`
	Blocked       Blocked   `yaml:"blocked"`
}

// HasRequiredResources 报告 Task 是否声明了任何全局资源需求。
func (s State) HasRequiredResources() bool {
	return len(s.Resources.Reservations) > 0
}
