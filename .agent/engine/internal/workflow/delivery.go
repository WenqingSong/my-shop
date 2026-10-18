package workflow

import (
	"fmt"
	"path/filepath"
	"strings"
)

// 本文件实现 Workflow V4 最终交付的受控执行入口（Finding Fix B：Final Delivery Wiring）。
//
// 设计原则（与 Workflow V4 禁止自动 Agent 调度一致）：
//
//   - Plan* 严格只读：只消费 ProgramValidator / Validator 的公开 Gate，读取 Program / DESIGN
//     的最终 Evidence，核验来源分支与真实远端状态；绝不合并、绝不推送父分支。
//
//   - Execute* 仅在人类 Owner 显式授权后执行：授权必须绑定具体对象（Program ID / task slug）、
//     最终 Evidence SHA 与目标父分支；三者与只读计划不一致即拒绝，绝不因 Final Gate PASS 而
//     自动 push，也不接受未验证的 --yes 或任意可编辑的 owner.status=ACCEPTED 单独放行。
//
//   - 受控调用 Merger.Integrate：隔离合并 + 真实测试 + 父分支未漂移复核 + fast-forward-only
//     推送精确目标 SHA，禁止 force push；完成后复核远端父分支 HEAD。
//
// 信任边界（必须明示）：本引擎无法在机器层证明「调用者就是 Owner」。Owner 身份由运行环境
// （谁能进入交付工作区执行本入口）与 shared branch protection（谁能推送父分支）共同承担。
// DeliveryAuthorization 不是身份认证，它只是把一次交付动作显式绑定到不可变的 Evidence SHA
// 与目标父分支，防止布尔开关或可编辑状态文件被单独用于绕过证据检查。

// DeliveryKind 是最终交付对象的类型。
type DeliveryKind = string

const (
	// DeliveryTask 表示 NORMAL/COMPLEX Task。
	DeliveryTask DeliveryKind = "task"
	// DeliveryProgram 表示 Program 最终交付（F3）。
	DeliveryProgram DeliveryKind = "program"
	// DeliveryDesign 表示 DESIGN 最终交付（F4）。
	DeliveryDesign DeliveryKind = "design"
)

// DeliverySource 是一次最终交付的来源：来源分支 + 确切提交 SHA。
type DeliverySource struct {
	Branch string // 来源分支名（Program=最终集成分支；DESIGN=task_branch）
	SHA    string // 交付的确切提交（Program=final_integration.sha；DESIGN=task_branch 当前 HEAD）
}

// DeliveryPlan 是一次只读交付计划，用于向 Owner 明确展示即将更新的父分支、源 SHA 与目标对象。
type DeliveryPlan struct {
	Kind             DeliveryKind
	TargetID         string // program-id 或 task-slug
	ParentBranch     string // 目标父分支
	ParentBase       string // 初始化绑定的父分支 Base
	Source           DeliverySource
	FinalEvidenceSHA string   // 最终 Evidence SHA（授权锚点；Program=final_integration.sha，DESIGN=final_target）
	TestCommand      []string // 合并结果必须运行的真实测试命令
}

// DeliveryAuthorization 是人类 Owner 执行交付时的显式授权，绑定具体对象 + 最终 Evidence SHA + 目标父分支。
// 它不是身份认证，也不能用布尔值伪造：三个事实必须与只读计划完全一致才会被执行。
type DeliveryAuthorization struct {
	SourceSHA    string // Task=实际来源 tip；DESIGN/Program=FinalEvidenceSHA
	ParentBranch string // 必须等于计划父分支
}

// DeliveryController 是最终交付的受控执行入口（Merger 专属交付适配层）。
// 它不新增 Agent Role、不新增 Workflow、不自动调度、不自行产生 Owner ACCEPT。
type DeliveryController struct {
	Root        string
	Git         Git     // 只读 Git（核验 SHA / 分支 / 祖先）
	Merger      *Merger // 隔离集成原语
	Config      *Config // DESIGN Gate 需要（Program 交付不使用，可空）
	TestCommand []string
}

// PlanProgram 只读生成 Program 最终交付计划（F3）。
// sourceBranch 是最终集成结果所在的分支：Program State schema 未记录该分支，必须显式提供，
// 并与 origin/<sourceBranch> 的真实远端状态核验一致。
func (dc *DeliveryController) PlanProgram(programID, sourceBranch string) (DeliveryPlan, Result) {
	plan := DeliveryPlan{Kind: DeliveryProgram, TargetID: programID, TestCommand: dc.TestCommand}
	if sourceBranch == "" {
		return plan, failResult(programID, "Delivery Source Branch", "sourceBranch 为空",
			"最终集成来源分支", "Program 最终集成 Evidence SHA 必须绑定真实来源分支")
	}

	pv := &ProgramValidator{Root: dc.Root, Git: dc.Git, Store: &RegistryStore{Root: dc.Root}}
	program, err := pv.LoadProgramState(programID)
	if err != nil {
		return plan, Result{Status: StatusError, Error: err.Error()}
	}

	// 1. 专属 Final Gate（只读）。
	if res := pv.GateFinalDelivery(programID); res.Status != StatusPass {
		return plan, res
	}

	// 2. 交付专属绑定（不复制 Gate 逻辑，只补齐当前 Gate 未覆盖的 SHA 绑定）：
	//    Cleaner Evidence 与 Owner ACCEPT 必须绑定同一个最终集成 SHA。
	//    PENDING_INTEGRATION：F-A 正在增强 GateFinalDelivery（新增 Cleaner 证据与 Owner ACCEPT
	//    的 SHA 绑定检查）。届时此处的绑定判断应与增强后 Gate 保持一致（不得冲突、不得放宽）。
	finalSHA := program.FinalIntegration.SHA
	if program.Review.Target != finalSHA {
		return plan, failResult(programID, "Delivery Cleaner Evidence", program.Review.Target, finalSHA,
			"Cleaner Evidence 必须绑定最终集成 SHA")
	}
	if program.Owner.Target != finalSHA {
		return plan, failResult(programID, "Delivery Owner ACCEPT", program.Owner.Target, finalSHA,
			"Owner ACCEPT 必须绑定最终集成 SHA")
	}

	// 3. 真实远端状态：来源分支当前必须指向最终集成 SHA（只读核验）。
	if err := dc.verifySourceBranch(sourceBranch, finalSHA); err != nil {
		return plan, Result{Status: StatusFail, Issues: []Issue{{
			Task: programID, Check: "Delivery Source", Actual: err.Error(),
			Expected: "origin/" + sourceBranch + " == " + finalSHA, Reason: "来源分支或 SHA 非法",
		}}}
	}

	plan.ParentBranch = program.ParentBranch
	plan.ParentBase = program.BaseCommit
	plan.Source = DeliverySource{Branch: sourceBranch, SHA: finalSHA}
	plan.FinalEvidenceSHA = finalSHA
	return plan, Result{Status: StatusPass}
}

// ExecuteProgram 在显式授权下执行 Program 最终交付（F3）。
func (dc *DeliveryController) ExecuteProgram(programID, sourceBranch string, auth DeliveryAuthorization) (MergeReport, Result) {
	var rep MergeReport
	plan, res := dc.PlanProgram(programID, sourceBranch)
	if res.Status != StatusPass {
		return rep, res
	}
	if res := dc.authorize(programID, plan, auth); res.Status != StatusPass {
		return rep, res
	}
	return dc.integrate(plan)
}

// PlanDesign 只读生成 DESIGN 最终交付计划（F4）。
func (dc *DeliveryController) PlanDesign(taskSlug string) (DeliveryPlan, Result) {
	plan := DeliveryPlan{Kind: DeliveryDesign, TargetID: taskSlug, TestCommand: dc.TestCommand}

	s, err := LoadTaskStateFile(dc.Root, taskSlug)
	if err != nil {
		return plan, Result{Status: StatusError, Error: err.Error()}
	}
	if s.TaskType != TaskTypeDesign {
		return plan, failResult(taskSlug, "Delivery Design Task Type", s.TaskType, TaskTypeDesign,
			"DESIGN 交付仅适用于 DESIGN 任务")
	}
	if s.Git == nil || s.Design == nil {
		return plan, Result{Status: StatusError, Error: "DESIGN state 缺少 git/design section"}
	}

	// 1. 专属 Final Gate（GateDesignFinalApproval）：覆盖 approved_target / review_target /
	//    final_target 有效性、两个 Owner Checkpoint、批准对象实质漂移失效、父分支绑定。
	v := &Validator{Root: dc.Root, Git: dc.Git, Config: dc.Config}
	taskDir := filepath.Join(".agent", "tasks", taskSlug)
	if res := v.GateDesignFinalApproval(taskDir); res.Status != StatusPass {
		return plan, res
	}

	// 2. 最终 Evidence SHA = final_target（授权锚点）；实际交付来源 = task_branch 当前 HEAD。
	//    final_target 必须是 HEAD 的祖先：批准的最终快照仍包含在待交付分支内且未被替换。
	finalTarget := s.Design.FinalTarget
	head, err := dc.Git.RevParse("HEAD")
	if err != nil {
		return plan, Result{Status: StatusError, Error: fmt.Sprintf("rev-parse HEAD: %v", err)}
	}
	if ok, err := dc.Git.IsAncestor(finalTarget, head); err != nil {
		return plan, Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", finalTarget, err)}
	} else if !ok {
		return plan, failResult(taskSlug, "Delivery Design Final Target", finalTarget, "当前 task_branch HEAD 的祖先",
			"最终批准快照不在待交付分支历史中")
	}

	plan.ParentBranch = s.Git.ParentBranch
	plan.ParentBase = s.Git.BaseCommit
	plan.Source = DeliverySource{Branch: s.Git.TaskBranch, SHA: head}
	plan.FinalEvidenceSHA = finalTarget
	return plan, Result{Status: StatusPass}
}

// ExecuteDesign 在显式授权下执行 DESIGN 最终交付（F4）。
func (dc *DeliveryController) ExecuteDesign(taskSlug string, auth DeliveryAuthorization) (MergeReport, Result) {
	var rep MergeReport
	st, err := LoadTaskStateFile(dc.Root, taskSlug)
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if st.Program != nil {
		return rep, failResult(taskSlug, "Delivery Program Child", st.Program.ID, "Collector登记后整体集成", "DESIGN子Task不能直接交付父分支")
	}
	plan, res := dc.PlanDesign(taskSlug)
	if res.Status != StatusPass {
		return rep, res
	}
	if res := dc.authorize(taskSlug, plan, auth); res.Status != StatusPass {
		return rep, res
	}
	return dc.integrate(plan)
}

// authorize 校验显式授权与只读计划完全一致（绑定具体对象 + 最终 Evidence SHA + 父分支）。
func (dc *DeliveryController) authorize(targetID string, plan DeliveryPlan, auth DeliveryAuthorization) Result {
	if auth.SourceSHA != plan.FinalEvidenceSHA {
		return failResult(targetID, "Delivery Authorization", auth.SourceSHA, plan.FinalEvidenceSHA,
			"Owner 授权绑定的 SHA 与最终 Evidence SHA 不一致，拒绝执行")
	}
	if auth.ParentBranch != plan.ParentBranch {
		return failResult(targetID, "Delivery Authorization", auth.ParentBranch, plan.ParentBranch,
			"Owner 授权绑定的父分支与计划父分支不一致，拒绝执行")
	}
	return Result{Status: StatusPass}
}

// integrate 受控调用 Merger.Integrate。仅在显式授权通过后到达此处，故 OwnerAuthorized 恒为 true；
// 它不会在任何 Gate 或 Plan 阶段被调用，也不存在自动审批路径。
func (dc *DeliveryController) integrate(plan DeliveryPlan) (MergeReport, Result) {
	var rep MergeReport
	if dc.Merger == nil {
		return rep, Result{Status: StatusError, Error: "DeliveryController 未配置 Merger"}
	}
	req := MergeRequest{
		ParentBranch:    plan.ParentBranch,
		ParentBaseSHA:   plan.ParentBase,
		Sources:         []MergeSource{{Branch: plan.Source.Branch, SHA: plan.Source.SHA}},
		OwnerAuthorized: true, // 仅由显式授权路径触发，绝不自动置位
		TestCommand:     plan.TestCommand,
	}
	var err error
	rep, err = dc.Merger.Integrate(req)
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if !rep.ParentUpdated {
		reason := rep.Reason
		if reason == "" {
			reason = "父分支未更新（已是最新或未授权）"
		}
		return rep, Result{Status: StatusFail, Issues: []Issue{{
			Task: plan.TargetID, Check: "Delivery Integrate", Actual: reason,
			Expected: "隔离合并 + 真实测试 + 授权更新父分支", Reason: reason,
		}}}
	}
	// 完成后复核远端父分支 HEAD（真实远端，非本地 refs/remotes 缓存）。
	if err := dc.verifyRemoteParentHead(plan.ParentBranch, rep.MergeCommit); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	return rep, Result{Status: StatusPass}
}

// verifySourceBranch 只读核验来源 SHA 为真实 commit，且来源分支当前指向该 SHA。
func (dc *DeliveryController) verifySourceBranch(branch, sha string) error {
	if dc.Git == nil {
		return fmt.Errorf("DeliveryController 未配置只读 Git，无法核验来源分支与 SHA")
	}
	if ok, err := dc.Git.CommitExists(sha); err != nil {
		return fmt.Errorf("check source sha %s: %v", sha, err)
	} else if !ok {
		return fmt.Errorf("来源 SHA %s 不是真实 commit", sha)
	}
	remote, err := dc.Git.RevParse("origin/" + branch)
	if err != nil {
		return fmt.Errorf("rev-parse origin/%s: %v", branch, err)
	}
	if remote != sha {
		return fmt.Errorf("来源分支 %s 当前 SHA %s 与固定 SHA %s 不一致", branch, short(remote), short(sha))
	}
	return nil
}

// verifyRemoteParentHead 通过 git ls-remote 复核远端父分支 HEAD（不依赖本地 refs/remotes 缓存）。
func (dc *DeliveryController) verifyRemoteParentHead(branch, want string) error {
	out, err := gitRun(dc.Root, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("复核远端父分支 %s: %v", branch, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return fmt.Errorf("复核远端父分支 %s：ls-remote 无输出", branch)
	}
	if fields[0] != want {
		return fmt.Errorf("复核远端父分支 %s：HEAD=%s 与合并结果 %s 不一致", branch, fields[0], short(want))
	}
	return nil
}
