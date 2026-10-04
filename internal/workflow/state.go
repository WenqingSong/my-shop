package workflow

// 本文件定义 state.yaml 的结构化 schema、13 值 phase 集合、
// 各正交子事实的合法枚举，以及 Transition Authority 常量表。
//
// state.yaml 是 Task 当前状态的唯一机器权威源：phase 只表达生命周期阶段，
// CLEAN/STALE 只在 review.status，Owner Verification 只在 owner_verification.status，
// Delivery Result 只在 delivery.status，阻塞只在 blocked。禁止同一事实双写。

// Phase 是 state.yaml 中唯一的 Task 生命周期阶段权威。
type Phase = string

// 13 值 phase 集合（见 docs/design/agent-workflow.md §3.1）。
const (
	PhaseNew                      Phase = "NEW"
	PhaseReadyForAnalyst          Phase = "READY_FOR_ANALYST"
	PhaseReadyForCoder            Phase = "READY_FOR_CODER"
	PhaseWaitingForOwnerApproval  Phase = "WAITING_FOR_OWNER_APPROVAL"
	PhaseApproved                 Phase = "APPROVED"
	PhaseImplementing             Phase = "IMPLEMENTING"
	PhaseReadyForReview           Phase = "READY_FOR_REVIEW"
	PhaseInReview                 Phase = "IN_REVIEW"
	PhaseChangesRequired          Phase = "CHANGES_REQUIRED"
	PhaseContractRevisionRequired Phase = "CONTRACT_REVISION_REQUIRED"
	PhaseWaitingForOwnerAccept    Phase = "WAITING_FOR_OWNER_ACCEPTANCE"
	PhaseDelivering               Phase = "DELIVERING"
	PhaseDone                     Phase = "DONE"
)

var allPhases = []Phase{
	PhaseNew,
	PhaseReadyForAnalyst,
	PhaseReadyForCoder,
	PhaseWaitingForOwnerApproval,
	PhaseApproved,
	PhaseImplementing,
	PhaseReadyForReview,
	PhaseInReview,
	PhaseChangesRequired,
	PhaseContractRevisionRequired,
	PhaseWaitingForOwnerAccept,
	PhaseDelivering,
	PhaseDone,
}

var phaseSet = func() map[Phase]struct{} {
	m := make(map[Phase]struct{}, len(allPhases))
	for _, p := range allPhases {
		m[p] = struct{}{}
	}
	return m
}()

// ValidPhase 报告 p 是否为合法的 13 值 phase 之一。
func ValidPhase(p string) bool {
	_, ok := phaseSet[p]
	return ok
}

// ReviewStatus 是 review.status 的合法枚举（绑定 review.target）。
type ReviewStatus = string

const (
	ReviewNone  ReviewStatus = "NONE"
	ReviewClean ReviewStatus = "CLEAN"
	ReviewStale ReviewStatus = "STALE"
)

var reviewStatusSet = map[ReviewStatus]struct{}{
	ReviewNone:  {},
	ReviewClean: {},
	ReviewStale: {},
}

// OwnerVerificationStatus 是 owner_verification.status 的合法枚举。
type OwnerVerificationStatus = string

const (
	OwnerVerificationNotRequired OwnerVerificationStatus = "NOT_REQUIRED"
	OwnerVerificationPending     OwnerVerificationStatus = "PENDING"
	OwnerVerificationAccepted    OwnerVerificationStatus = "ACCEPTED"
)

var ownerVerificationStatusSet = map[OwnerVerificationStatus]struct{}{
	OwnerVerificationNotRequired: {},
	OwnerVerificationPending:     {},
	OwnerVerificationAccepted:    {},
}

// DeliveryStatus 是 delivery.status 的合法枚举。
type DeliveryStatus = string

const (
	DeliveryNone            DeliveryStatus = "NONE"
	DeliveryPass            DeliveryStatus = "PASS"
	DeliveryConditionalPass DeliveryStatus = "CONDITIONAL_PASS"
	DeliveryFail            DeliveryStatus = "FAIL"
)

var deliveryStatusSet = map[DeliveryStatus]struct{}{
	DeliveryNone:            {},
	DeliveryPass:            {},
	DeliveryConditionalPass: {},
	DeliveryFail:            {},
}

// Review 是 review 子事实：审查结论绑定 review.target。
type Review struct {
	Status      ReviewStatus `yaml:"status"`
	TargetBase  string       `yaml:"target_base"`
	TargetPaths []string     `yaml:"target_paths"`
}

// OwnerVerification 是 owner_verification 子事实。
type OwnerVerification struct {
	Status OwnerVerificationStatus `yaml:"status"`
}

// Delivery 是 delivery 子事实。
type Delivery struct {
	Status DeliveryStatus `yaml:"status"`
}

// RequiredResources 只声明 Task 需要的全局资源，不记录「已满足/SATISFIED」自证状态。
type RequiredResources struct {
	ErrorCodeDomains []string `yaml:"error_code_domains"`
	Migrations       []string `yaml:"migrations"`
}

// Blocked 是正交阻塞事实，任意 phase 皆可置阻塞。
type Blocked struct {
	IsBlocked bool   `yaml:"is_blocked"`
	Reason    string `yaml:"reason"`
}

// State 是 state.yaml 的结构化表示（见 docs/design/agent-workflow.md §2）。
type State struct {
	Task              string            `yaml:"task"`
	Phase             Phase             `yaml:"phase"`
	Review            Review            `yaml:"review"`
	OwnerVerification OwnerVerification `yaml:"owner_verification"`
	Delivery          Delivery          `yaml:"delivery"`
	RequiredResources RequiredResources `yaml:"required_resources"`
	Blocked           Blocked           `yaml:"blocked"`
}

// HasRequiredResources 报告 Task 是否声明了任何全局资源需求。
func (s State) HasRequiredResources() bool {
	return len(s.RequiredResources.ErrorCodeDomains) > 0 || len(s.RequiredResources.Migrations) > 0
}

// Transition 描述一条 phase 转换的 Decision Authority 与 File Writer。
// File Writer ≠ Decision Authority：Owner 的 ACCEPT/REJECT 由 Agent 在明确指令下机械持久化。
type Transition struct {
	From              Phase
	To                Phase
	DecisionAuthority string
	FileWriter        string
	Gate              string
}

// transitionAuthority 是 18 条 phase 转换的权威表（见 docs/design/agent-workflow.md §4.1）。
// 它作为常量固化，用于保证实现与 Contract/Design 一致，测试会断言其完整性。
var transitionAuthority = []Transition{
	{PhaseNew, PhaseReadyForAnalyst, "Task Builder", "Task Builder", "COMPLEX"},
	{PhaseNew, PhaseReadyForCoder, "Task Builder", "Task Builder", "NORMAL"},
	{PhaseReadyForAnalyst, PhaseWaitingForOwnerApproval, "Analyst", "Analyst", "写 Contract + 推荐"},
	{PhaseWaitingForOwnerApproval, PhaseApproved, "Owner（ACCEPT）", "Analyst（机械）", ""},
	{PhaseWaitingForOwnerApproval, PhaseContractRevisionRequired, "Owner（REJECT）", "Analyst（机械）", ""},
	{PhaseApproved, PhaseImplementing, "Coder", "Coder", "Contract APPROVED + Design 同步 + 资源 Gate"},
	{PhaseImplementing, PhaseReadyForReview, "Coder", "Coder", "自验完成"},
	{PhaseReadyForReview, PhaseInReview, "Cleaner", "Cleaner", ""},
	{PhaseInReview, PhaseWaitingForOwnerAccept, "Cleaner", "Cleaner", "review.status=CLEAN"},
	{PhaseInReview, PhaseChangesRequired, "Cleaner", "Cleaner", "有可修复缺陷"},
	{PhaseChangesRequired, PhaseImplementing, "Coder", "Coder", "Finding 修复"},
	{PhaseApproved, PhaseContractRevisionRequired, "Analyst", "Analyst", "实现期设计冲突"},
	{PhaseWaitingForOwnerAccept, PhaseDelivering, "Owner（主动进入）", "Deliverer（机械记录）", "review.status=CLEAN + owner_verification ∈ {ACCEPTED,NOT_REQUIRED} + 资源有效"},
	{PhaseWaitingForOwnerAccept, PhaseDone, "Owner（最终接受）", "Owner", "owner_verification ∈ {ACCEPTED,NOT_REQUIRED}"},
	{PhaseDelivering, PhaseDone, "Owner（最终接受）", "Owner", "delivery.status=PASS"},
	{PhaseDelivering, PhaseChangesRequired, "Deliverer（IMPLEMENTATION_DEFECT）", "Deliverer", "实现缺陷"},
	{PhaseDelivering, PhaseContractRevisionRequired, "Deliverer（CONTRACT_PROBLEM）", "Deliverer", "设计问题"},
	{PhaseDelivering, PhaseWaitingForOwnerAccept, "Deliverer（OUT_OF_SCOPE_EXISTING_ISSUE）", "Deliverer", "范围外既有问题"},
}
