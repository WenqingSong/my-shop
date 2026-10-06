package workflow

import (
	"fmt"
	"path/filepath"
)

// 本文件实现 Workflow V2 的五个显式 Gate 及其共享判定。
//
// Gate 是 Handoff 前的机械出站检查，Validator 只读。
// 共享判定围绕三个 Evidence Binding（Contract / Review / Delivery）+ 两个横切不变量
// （resourcesAuthorized / ownerAcceptanceValid）+ handoffReady。

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
	if s.Blocked.Active {
		return failResult(taskSlug, "Coder Start", "blocked.active=true", "false", "任务被阻塞，不得开始实现")
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
	if ok, err := v.Git.IsAncestor(s.Review.Target, head); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", s.Review.Target, err)}
	} else if !ok {
		return failResult(taskSlug, "Cleaner Start", s.Review.Target, "当前 feature HEAD 的祖先", "review.target 不在当前 feature 历史中")
	}
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
	if s.Blocked.Active {
		return failResult(taskSlug, "Owner Gate Start", "blocked.active=true", "false", "任务被阻塞，不得请求 Owner 决策")
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
	if s.Owner.ReviewTarget != s.Review.Target {
		return failResult(taskSlug, "Delivery Start", fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget), s.Review.Target, "Owner Acceptance 必须绑定当前 review.target")
	}
	if s.Blocked.Active {
		return failResult(taskSlug, "Delivery Start", "blocked.active=true", "false", "任务被阻塞，不得开始验收")
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
	if s.Blocked.Active {
		return failResult(taskSlug, "Merge Ready", "blocked.active=true", "false", "任务被阻塞，不得 Merge Ready")
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
	if s.Delivery.ReviewTarget != s.Review.Target {
		return failResult(taskSlug, "Merge Ready", fmt.Sprintf("delivery.review_target=%s", s.Delivery.ReviewTarget), s.Review.Target, "delivery 必须绑定当前 review.target")
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
		if ok, err := v.Git.CommitExists(s.Contract.Target); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("check contract.target %s: %v", s.Contract.Target, err)}
		} else if !ok {
			return failResult(taskSlug, "Contract (INV-2)", s.Contract.Target, "真实 commit", "contract.target 不是真实 commit")
		}
		head, err := v.featureHead()
		if err != nil {
			return Result{Status: StatusError, Error: err.Error()}
		}
		if ok, err := v.Git.IsAncestor(s.Contract.Target, head); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", s.Contract.Target, err)}
		} else if !ok {
			return failResult(taskSlug, "Contract (INV-2)", s.Contract.Target, "当前 feature HEAD 的祖先", "contract.target 不在当前 feature 历史中")
		}
		approvedContract, err := v.Git.ShowFile(s.Contract.Target, contractPath(taskSlug))
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("show contract.md@%s: %v", s.Contract.Target, err)}
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
// 它不识别所谓「Evidence Commit 类型」，只机械检查：target 为真实 commit + tail 满足白名单。
func (v *Validator) reviewTailValid(taskSlug string, s State, featureHead string) Result {
	if s.Review.Target == "" {
		return failResult(taskSlug, "Review Tail (INV-4)", "review.target 为空", "<evidence-commit-sha>", "review.target 必须绑定实现 Evidence Commit")
	}
	if ok, err := v.Git.CommitExists(s.Review.Target); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check review.target %s: %v", s.Review.Target, err)}
	} else if !ok {
		return failResult(taskSlug, "Review Tail (INV-4)", s.Review.Target, "真实 commit", "review.target 不是真实 commit")
	}
	changed, err := v.Git.DiffNameOnlyRange(s.Review.Target, featureHead)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("diff %s..%s: %v", s.Review.Target, featureHead, err)}
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
	if s.Owner.ReviewTarget != s.Review.Target {
		return failResult(taskSlug, "Owner Acceptance (INV-5)", fmt.Sprintf("owner.review_target=%s", s.Owner.ReviewTarget), s.Review.Target, "Owner Acceptance 必须绑定当前 review.target")
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
	if ok, err := v.Git.IsAncestor(s.Delivery.FeatureHead, remoteFeatureHead); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s %s: %v", s.Delivery.FeatureHead, remoteFeatureHead, err)}
	} else if !ok {
		return failResult(taskSlug, "Delivery Freshness (INV-6)", s.Delivery.FeatureHead, "当前 remote feature HEAD 的祖先", "delivery.feature_head 不在当前 remote feature 历史中")
	}
	changed, err := v.Git.DiffNameOnlyRange(s.Delivery.FeatureHead, remoteFeatureHead)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("diff %s..%s: %v", s.Delivery.FeatureHead, remoteFeatureHead, err)}
	}
	for _, p := range changed {
		if isDeliverySubstantial(taskSlug, p) {
			return failResult(taskSlug, "Delivery Freshness (INV-6)", fmt.Sprintf("%s 在 feature_head 之后发生变化", p), "仅当前 task 的 delivery-neutral artifacts", "Delivery PASS 后出现非 delivery-neutral 变化")
		}
	}
	if developHead != s.Delivery.DevelopBase {
		return failResult(taskSlug, "Delivery Freshness (INV-6)", fmt.Sprintf("%s=%s", v.developRef(), developHead), s.Delivery.DevelopBase, "develop 已前进，旧 Delivery PASS 不再 Merge Ready")
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
