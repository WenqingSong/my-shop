package workflow

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// 本文件实现 Workflow V2 的五个显式 Gate 及其共享判定。
//
// Gate 是 Handoff 前的机械出站检查，Validator 只读。
// 共享判定围绕三个 Evidence Binding（Contract / Review / Delivery）+ 三个横切不变量
// （resourcesAuthorized / ownerAcceptanceValid / routeValid）+ handoffReady。
//
// routeValid 自 V3.2.0 起在五个 Gate 上统一生效：它校验 state.route（路线事实）
// 与 Contract、task.md Complexity、Contract Approval 证据和 Review 证据形态是否
// 自洽；route 缺省的历史任务不做任何推断（见 route.go）。

// failResult 构造一个单 issue 的 FAIL 结果。
func failResult(task, check, actual, expected, reason string) Result {
	return Result{
		Status: StatusFail,
		Issues: []Issue{{
			Task:     task,
			Check:    check,
			Actual:   actual,
			Expected: expected,
			Reason:   reason,
		}},
	}
}

// blockedResult 构造 blocked.active 拦截结果（E-3）。
// Actual / Expected 在五个 Gate 上完全一致，只有 Reason 描述各自被禁止的动作。
func blockedResult(taskSlug, check, reason string) Result {
	return failResult(taskSlug, check, "blocked.active=true", "false", reason)
}

// blockedActive 校验横切不变量「blocked.active=true 时任何 Gate 都不得 PASS」。
// 五个 Gate 统一在 schema 校验之后立即执行，避免先运行其他昂贵检查掩盖阻塞事实。
func blockedActive(taskSlug string, s State, check, reason string) Result {
	if s.Blocked.Active {
		return blockedResult(taskSlug, check, reason)
	}
	return Result{Status: StatusPass}
}

// GateCoderStart 确认 Coder 已经被授权开始实现（INV-2）。
func (v *Validator) GateCoderStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := blockedActive(taskSlug, s, "Coder Start", "任务被阻塞，不得开始实现"); res.Status != StatusPass {
		return res
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	if res := v.contractValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.resourcesAuthorized(s); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// GateCleanerStart 确认 Cleaner 拿到的是可复核、已发布到 feature remote 的 implementation snapshot。
func (v *Validator) GateCleanerStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := blockedActive(taskSlug, s, "Cleaner Start", "任务被阻塞，不得开始清理复核"); res.Status != StatusPass {
		return res
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if s.Review.Status != ReviewPending {
		return failResult(taskSlug, "Cleaner Start", fmt.Sprintf("review.status=%s", s.Review.Status), "PENDING", "Cleaner 只能审查 Coder 已发起的 PENDING Review Request")
	}
	if s.Review.Target == "" {
		return failResult(taskSlug, "Cleaner Start", "review.target 为空", "<evidence-commit-sha>", "review.target 必须绑定 implementation Evidence Commit")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	// review.target 的祖先校验已下沉到 reviewTailValid（E-1），
	// owner-gate-start / delivery-start / merge-ready 通过 cleanValidity 复用同一实现，
	// 保证四个 Gate 的 Review Target 语义完全一致。
	if res := v.reviewTailValid(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// GateOwnerGateStart 确认 OwnerGate 只能围绕一个仍然有效的 CLEAN snapshot 取得 Owner 决策。
func (v *Validator) GateOwnerGateStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := blockedActive(taskSlug, s, "Owner Gate Start", "任务被阻塞，不得请求 Owner 决策"); res.Status != StatusPass {
		return res
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := v.cleanValidity(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// GateDeliveryStart 确认 Deliverer 只能开始验收一个已被 CLEAN、Owner 明确接受、且 feature 已同步的候选。
func (v *Validator) GateDeliveryStart(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := blockedActive(taskSlug, s, "Delivery Start", "任务被阻塞，不得开始验收"); res.Status != StatusPass {
		return res
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.contractValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.resourcesAuthorized(s); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := v.cleanValidity(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	if s.Owner.Status != OwnerAccepted {
		return failResult(taskSlug, "Delivery Start", fmt.Sprintf("owner.status=%s", s.Owner.Status), "ACCEPTED", "Deliverer 只能验收 Owner 已接受的实现")
	}
	reviewTarget, res := v.resolveImmutableCommit(taskSlug, "Delivery Start", "review.target", s.Review.Target)
	if res.Status != StatusPass {
		return res
	}
	ownerTarget, res := v.resolveImmutableCommit(taskSlug, "Delivery Start", "owner.review_target", s.Owner.ReviewTarget)
	if res.Status != StatusPass {
		return res
	}
	if ownerTarget != reviewTarget {
		return failResult(taskSlug, "Delivery Start", fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget), reviewTarget, "Owner Acceptance 必须绑定当前 review.target")
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// GateMergeReady 是最终 Agent Gate：告诉 Owner 该 feature 在刚才验证的 develop 基线上仍保持可集成含义。
func (v *Validator) GateMergeReady(taskDir string) Result {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if res := blockedActive(taskSlug, s, "Merge Ready", "任务被阻塞，不得 Merge Ready"); res.Status != StatusPass {
		return res
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.contractValid(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.resourcesAuthorized(s); res.Status != StatusPass {
		return res
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := v.cleanValidity(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	if res := v.ownerAcceptanceValid(taskSlug, s, head); res.Status != StatusPass {
		return res
	}
	if s.Delivery.Status != DeliveryPass {
		return failResult(taskSlug, "Merge Ready", fmt.Sprintf("delivery.status=%s", s.Delivery.Status), "PASS", "merge-ready 要求 delivery PASS")
	}
	reviewTarget, res := v.resolveImmutableCommit(taskSlug, "Merge Ready", "review.target", s.Review.Target)
	if res.Status != StatusPass {
		return res
	}
	deliveryTarget, res := v.resolveImmutableCommit(taskSlug, "Merge Ready", "delivery.review_target", s.Delivery.ReviewTarget)
	if res.Status != StatusPass {
		return res
	}
	if deliveryTarget != reviewTarget {
		return failResult(taskSlug, "Merge Ready", fmt.Sprintf("delivery.review_target=%s", s.Delivery.ReviewTarget), reviewTarget, "delivery 必须绑定当前 review.target")
	}
	if s.Delivery.FeatureHead == "" {
		return failResult(taskSlug, "Merge Ready", "delivery.feature_head 为空", "<evidence-commit-sha>", "delivery 必须绑定实际验证的 feature_head")
	}
	if s.Delivery.DevelopBase == "" {
		return failResult(taskSlug, "Merge Ready", "delivery.develop_base 为空", "<evidence-commit-sha>", "delivery 必须绑定验证时的 develop_base")
	}

	remoteFeatureHead, err := v.currentRemoteFeatureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	developHead, err := v.Git.RevParse(v.developRef())
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s: %v", v.developRef(), err)}
	}
	if res := v.deliveryFreshness(taskSlug, s, remoteFeatureHead, developHead); res.Status != StatusPass {
		return res
	}
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// currentRemoteFeatureHead 返回当前分支对应的 origin/<branch> 的完整 SHA。
func (v *Validator) currentRemoteFeatureHead() (string, error) {
	branch, err := v.Git.CurrentBranch()
	if err != nil {
		return "", err
	}
	if branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD")
	}
	return v.Git.RevParse("origin/" + branch)
}

// fullSHAHexLen 是 Git 完整 Commit SHA 的十六进制长度。
const fullSHAHexLen = 40

// commitRefPattern 约束 Evidence 字段的 Commit 字面值：只允许 7~40 位十六进制 SHA 缩写。
//
// 这直接排除 HEAD、分支名、可移动 Tag、remote-tracking 引用等「可变引用」：
// 它们今天指向 C1，明天可以指向任何 Commit，作为 Evidence 不可信。
// 只接受十六进制形态同时也消除了命令注入面（>= 引用字符串不可能匹配该 pattern）。
var commitRefPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// validImmutableCommitLiteral 报告字面值是否具备不可变 Commit 引用的形态。
func validImmutableCommitLiteral(literal string) bool {
	return commitRefPattern.MatchString(literal)
}

// resolveImmutableCommit 把 Evidence 字段的 Commit 字面值解析为不可变完整 SHA（E-2）。
//
// 统一算法（fail-closed）：
//  1. 字面值必须匹配 commitRefPattern（拒绝任何可变引用与非法形态）；
//  2. 字面值必须能唯一解析为真实 Commit；
//  3. 字面值必须是解析结果的前缀（小写化比较），锁死「缩写 → 具体 Commit」的唯一映射；
//  4. 返回完整 SHA，供祖先判断与等值比较统一使用，
//     避免历史合法短 SHA 与完整 SHA 因裸字符串比较被误拒绝。
//
// 无法解析 / 未通过前述任一环节的引用都不会被静默放过。
func (v *Validator) resolveImmutableCommit(taskSlug, check, field, literal string) (string, Result) {
	if !validImmutableCommitLiteral(literal) {
		return "", failResult(taskSlug, check, fmt.Sprintf("%s=%q", field, literal), "7~40 位十六进制 SHA 缩写",
			field+" 必须绑定不可变 Evidence Commit SHA，不接受 HEAD / 分支名 / Tag 等可变引用")
	}
	exists, err := v.Git.CommitExists(literal)
	if err != nil {
		return "", Result{Status: StatusError, Error: fmt.Sprintf("check %s %s: %v", field, literal, err)}
	}
	if !exists {
		return "", failResult(taskSlug, check, fmt.Sprintf("%s=%q", field, literal), "真实 commit",
			field+" 不是真实 commit")
	}
	full, err := v.Git.RevParse(literal)
	if err != nil {
		return "", Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s %s: %v", field, literal, err)}
	}
	full = strings.TrimSpace(full)
	if len(full) != fullSHAHexLen {
		return "", Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s %s: 非预期的输出 %q", field, literal, full)}
	}
	if !strings.HasPrefix(full, strings.ToLower(literal)) {
		return "", failResult(taskSlug, check, fmt.Sprintf("%s=%q 解析结果 %s", field, literal, full), "字面值必须是所解析 commit 的十六进制前缀",
			field+" 指向的不是字面值所声明的 commit（引用被移动或缩写有歧义）")
	}
	return full, Result{Status: StatusPass}
}

// contractValid 校验 Contract 授权（INV-2）：APPROVED 需绑定真实 Evidence Commit，
// 且 target 中的 contract.md 与当前 contract.md 完全一致；NOT_REQUIRED 直接通过。
func (v *Validator) contractValid(taskSlug string, s State) Result {
	switch s.Contract.Status {
	case ContractNotRequired:
		return Result{Status: StatusPass}
	case ContractApproved:
		if s.Contract.Target == "" {
			return failResult(taskSlug, "Contract (INV-2)", "contract.target 为空", "<evidence-commit-sha>", "APPROVED 必须绑定批准版本 contract.md 的 Evidence Commit")
		}
		target, res := v.resolveImmutableCommit(taskSlug, "Contract (INV-2)", "contract.target", s.Contract.Target)
		if res.Status != StatusPass {
			return res
		}
		head, err := v.featureHead()
		if err != nil {
			return Result{Status: StatusError, Error: err.Error()}
		}
		if ok, err := v.Git.IsAncestor(target, head); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", target, err)}
		} else if !ok {
			return failResult(taskSlug, "Contract (INV-2)", target, "当前 feature HEAD 的祖先", "contract.target 不在当前 feature 历史中")
		}
		approvedContract, err := v.Git.ShowFile(target, contractPath(taskSlug))
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("show contract.md@%s: %v", target, err)}
		}
		currentContract, err := v.readWorkingTree(contractPath(taskSlug))
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("read working tree contract.md: %v", err)}
		}
		if approvedContract != currentContract {
			return failResult(taskSlug, "Contract (INV-2)", "contract.md 内容已偏离 approved target", "与 contract.target 一致", "Contract Approval 已失效")
		}
		return Result{Status: StatusPass}
	default:
		return failResult(taskSlug, "Contract (INV-2)", fmt.Sprintf("contract.status=%s", s.Contract.Status), "APPROVED | NOT_REQUIRED", "Coder 开始前 Contract 必须有效")
	}
}

// reviewTailValid 校验 review.target..featureHead 之间只允许当前 task 的 review-neutral artifacts（INV-4）。
// 它不识别所谓「Evidence Commit 类型」，只机械检查三条：
//  1. review.target 是不可变 Evidence Commit（E-2）；
//  2. review.target 是当前 feature HEAD 的祖先或同一 Commit（E-1）；
//  3. tail 满足 review-neutral 白名单。
//
// 第 2 条是 CLEAN 有效性的必要前提：Tree Diff 为空只说明两棵树相同，
// 不能证明 target 属于当前 feature 历史；被历史改写孤儿化的 target
// 即便 Tree 完全一致，也必须判 CLEAN 无效。
func (v *Validator) reviewTailValid(taskSlug string, s State, featureHead string) Result {
	if s.Review.Target == "" {
		return failResult(taskSlug, "Review Tail (INV-4)", "review.target 为空", "<evidence-commit-sha>", "review.target 必须绑定实现 Evidence Commit")
	}
	target, res := v.resolveImmutableCommit(taskSlug, "Review Tail (INV-4)", "review.target", s.Review.Target)
	if res.Status != StatusPass {
		return res
	}
	if ok, err := v.Git.IsAncestor(target, featureHead); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", target, err)}
	} else if !ok {
		return failResult(taskSlug, "Review Tail (INV-4)", target, "当前 feature HEAD 的祖先或同一 commit",
			"review.target 不在当前 feature 历史中，CLEAN 无法由此推导")
	}
	changed, err := v.Git.DiffNameOnlyRange(target, featureHead)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("diff %s..%s: %v", target, featureHead, err)}
	}
	for _, p := range changed {
		if isReviewSubstantial(taskSlug, p) {
			return failResult(taskSlug, "Review Tail (INV-4)", fmt.Sprintf("%s 在 review.target 之后发生变化", p), "仅当前 task 的 review-neutral artifacts", "出现未覆盖 CLEAN 的实质变化")
		}
	}
	return Result{Status: StatusPass}
}

// cleanValidity 校验 CLEAN 仍有效（INV-3/INV-4）：review.status==CLEAN 且 tail 满足白名单。
func (v *Validator) cleanValidity(taskSlug string, s State, featureHead string) Result {
	if s.Review.Status != ReviewClean {
		return failResult(taskSlug, "CLEAN Validity (INV-3)", fmt.Sprintf("review.status=%s", s.Review.Status), "CLEAN", "CLEAN Validity 只适用于 CLEAN 状态")
	}
	return v.reviewTailValid(taskSlug, s, featureHead)
}

// ownerAcceptanceValid 校验 Owner Acceptance 绑定当前 CLEAN review.target（INV-5）。
func (v *Validator) ownerAcceptanceValid(taskSlug string, s State, featureHead string) Result {
	if s.Owner.Status != OwnerAccepted {
		return failResult(taskSlug, "Owner Acceptance (INV-5)", fmt.Sprintf("owner.status=%s", s.Owner.Status), "ACCEPTED", "Owner 尚未接受")
	}
	if s.Review.Status != ReviewClean {
		return failResult(taskSlug, "Owner Acceptance (INV-5)", fmt.Sprintf("review.status=%s", s.Review.Status), "CLEAN", "Owner ACCEPTED 要求 review CLEAN")
	}
	reviewTarget, res := v.resolveImmutableCommit(taskSlug, "Owner Acceptance (INV-5)", "review.target", s.Review.Target)
	if res.Status != StatusPass {
		return res
	}
	ownerTarget, res := v.resolveImmutableCommit(taskSlug, "Owner Acceptance (INV-5)", "owner.review_target", s.Owner.ReviewTarget)
	if res.Status != StatusPass {
		return res
	}
	// 完整 SHA 等值比较：历史短 SHA 与完整 SHA 若指向同一 Commit 必须判为同一绑定。
	if ownerTarget != reviewTarget {
		return failResult(taskSlug, "Owner Acceptance (INV-5)", fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget), reviewTarget, "Owner Acceptance 必须绑定当前 review.target")
	}
	return v.cleanValidity(taskSlug, s, featureHead)
}

// deliveryFreshness 校验 Delivery PASS 仍有效（INV-6）：
//   - delivery.feature_head 是当前 remote feature HEAD 的祖先；
//   - feature_head..remote HEAD 只允许当前 task 的 delivery-neutral artifacts；
//   - 当前 origin/<integration_branch> == delivery.develop_base。
func (v *Validator) deliveryFreshness(taskSlug string, s State, remoteFeatureHead, developHead string) Result {
	if s.Delivery.FeatureHead == "" {
		return failResult(taskSlug, "Delivery Freshness (INV-6)", "delivery.feature_head 为空", "<evidence-commit-sha>", "delivery PASS 必须绑定 feature_head")
	}
	featureHead, res := v.resolveImmutableCommit(taskSlug, "Delivery Freshness (INV-6)", "delivery.feature_head", s.Delivery.FeatureHead)
	if res.Status != StatusPass {
		return res
	}
	if ok, err := v.Git.IsAncestor(featureHead, remoteFeatureHead); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s %s: %v", featureHead, remoteFeatureHead, err)}
	} else if !ok {
		return failResult(taskSlug, "Delivery Freshness (INV-6)", featureHead, "当前 remote feature HEAD 的祖先", "delivery.feature_head 不在当前 remote feature 历史中")
	}
	changed, err := v.Git.DiffNameOnlyRange(featureHead, remoteFeatureHead)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("diff %s..%s: %v", featureHead, remoteFeatureHead, err)}
	}
	for _, p := range changed {
		if isDeliverySubstantial(taskSlug, p) {
			return failResult(taskSlug, "Delivery Freshness (INV-6)", fmt.Sprintf("%s 在 feature_head 之后发生变化", p), "仅当前 task 的 delivery-neutral artifacts", "Delivery PASS 后出现非 delivery-neutral 变化")
		}
	}
	developBase, res := v.resolveImmutableCommit(taskSlug, "Delivery Freshness (INV-6)", "delivery.develop_base", s.Delivery.DevelopBase)
	if res.Status != StatusPass {
		return res
	}
	// 集成分支基线不变性：完整 SHA 等值比较。develop 前进（含 rebase 后的新 SHA）必须拒绝旧 Delivery Evidence。
	if developHead != developBase {
		return failResult(taskSlug, "Delivery Freshness (INV-6)", fmt.Sprintf("%s=%s", v.developRef(), developHead), developBase, "develop 已前进，旧 Delivery PASS 不再 Merge Ready")
	}
	return Result{Status: StatusPass}
}

// resourcesAuthorized 校验 resources 已在 shared integration branch 的 Registry 授权（INV-2）。
// Registry 路径与 integration branch 来自机器配置 .agent/workflow.yaml。
// 未知 resource kind / registry 读取失败 → ERROR（exit 2）；未登记 / owner 不一致 / 状态无效 → FAIL（exit 1）。
func (v *Validator) resourcesAuthorized(s State) Result {
	if !s.HasRequiredResources() {
		return Result{Status: StatusPass}
	}
	regs, err := v.loadDevelopRegistries(s)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	issues := checkResourceAuthority(s.TaskID, s.Resources.Reservations, regs)
	if len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	return Result{Status: StatusPass}
}

// handoffReady 校验正常 Handoff 的 Clean Exit 前提：
// 非 detached HEAD、working tree clean、origin/<current-feature> 存在、local HEAD == remote feature HEAD。
func (v *Validator) handoffReady() Result {
	branch, err := v.Git.CurrentBranch()
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("current branch: %v", err)}
	}
	if branch == "HEAD" {
		return Result{Status: StatusError, Error: "detached HEAD，拒绝在非分支工作区运行 Gate"}
	}
	remoteRef := "origin/" + branch
	if ok, err := v.Git.CommitExists(remoteRef); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check %s: %v", remoteRef, err)}
	} else if !ok {
		return Result{Status: StatusError, Error: fmt.Sprintf("remote feature %s 不存在", remoteRef)}
	}
	if dirty, err := v.Git.StatusPorcelain(); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("status --porcelain: %v", err)}
	} else if len(dirty) > 0 {
		return Result{Status: StatusFail, Issues: []Issue{{Task: "", Check: "Handoff Ready", Actual: "working tree dirty", Expected: "clean", Reason: "Handoff 前工作区必须 clean"}}}
	}
	local, err := v.Git.RevParse("HEAD")
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("rev-parse HEAD: %v", err)}
	}
	remote, err := v.Git.RevParse(remoteRef)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s: %v", remoteRef, err)}
	}
	if local != remote {
		return Result{Status: StatusFail, Issues: []Issue{{Task: "", Check: "Handoff Ready", Actual: "local HEAD != remote feature HEAD", Expected: "synced", Reason: "Handoff 前必须 push 当前 feature"}}}
	}
	return Result{Status: StatusPass}
}
