package workflow

import (
	"fmt"
	"sort"
)

// 本文件是工作包 D（Assembler 的 MergeOps 契约）与工作包 B（Merger.Integrate 隔离集成原语）
// 之间的最小适配层。它是唯一允许的隔离合并复用点：Assembler 通过它调用 Merger 的真实
// 隔离集成原语，不复制第二套业务合并引擎，也不伪造 Integration PASS。
//
// 安全语义对齐（不得削弱 Merger 的任何安全约束）：
//   - ParentBranch 注入：保留 Merger 的目标父分支漂移检查（合并前/后各复核一次）；
//   - BranchOf 提供 delivery SHA -> 分支名：保留 Merger 的来源分支绑定校验，
//     缺失即拒绝，绝不静默跳过来源分支核验；
//   - OwnerAuthorized 恒为 false：阶段集成绝不更新正式父分支；
//   - TestCommand 注入：阶段集成必须对合并结果运行真实测试，缺失即拒绝。

// MergerMergeOps 把工作包 B 的 Merger.Integrate 适配为工作包 D 的 MergeOps 契约。
type MergerMergeOps struct {
	Merger       *Merger
	ParentBranch string            // 目标父分支名（如 develop）
	BranchOf     map[string]string // delivery SHA -> 分支名（用于 Merger 的来源分支绑定校验）
	TestCommand  []string          // 阶段集成必须运行的真实测试命令
	// Optional Program binding enables the same proven metadata-tail resolution
	// as the controlled Program CLI. Legacy callers retain exact SHA matching.
	ProgramID string
}

// MergeSources 在隔离环境把 sources（确切 SHA）合并到 base，返回合并结果 commit SHA。
// 任何失败都返回 error，绝不返回伪造的 PASS SHA。
func (a *MergerMergeOps) MergeSources(base string, sources []string) (string, error) {
	if a.Merger == nil {
		return "", fmt.Errorf("MergerMergeOps 未配置 Merger")
	}
	if a.ParentBranch == "" {
		return "", fmt.Errorf("阶段集成必须指定目标父分支")
	}
	if len(a.TestCommand) == 0 {
		return "", fmt.Errorf("阶段集成必须提供真实测试命令，不得跳过合并结果测试")
	}
	if base == "" {
		return "", fmt.Errorf("阶段集成必须指定父基线 base SHA")
	}
	if len(sources) == 0 {
		return "", fmt.Errorf("阶段集成必须至少有一个来源")
	}

	req := MergeRequest{
		ParentBranch:    a.ParentBranch,
		ParentBaseSHA:   base,
		OwnerAuthorized: false, // 阶段集成绝不更新正式父分支
		TestCommand:     a.TestCommand,
	}
	for _, sha := range sources {
		branch := a.BranchOf[sha]
		if branch == "" {
			// 拒绝跳过 Merger 的来源分支绑定校验：缺分支绑定即失败。
			return "", fmt.Errorf("来源 SHA %s 缺少分支绑定，拒绝跳过来源分支核验", short(sha))
		}
		source := MergeSource{Branch: branch, SHA: sha}
		if a.ProgramID != "" {
			p, err := LoadProgramState(a.Merger.Root, a.ProgramID)
			if err != nil {
				return "", err
			}
			reg, err := (&RegistryStore{Root: a.Merger.Root}).Load(a.ProgramID)
			if err != nil {
				return "", err
			}
			matched := false
			for _, d := range reg.Deliveries {
				if d.DeliverySHA == sha && d.Branch == branch {
					proof, err := ResolveProgramDelivery(a.Merger.Root, p, d.TaskID)
					if err != nil {
						return "", err
					}
					if proof.Delivery.DeliverySHA != sha || proof.Delivery.ReviewTarget != d.ReviewTarget || proof.Delivery.OwnerTarget != d.OwnerTarget {
						return "", fmt.Errorf("registered Program evidence changed")
					}
					source.SHA = proof.SourceTip
					matched = true
					break
				}
			}
			if !matched {
				return "", fmt.Errorf("source not registered in Program")
			}
		}
		req.Sources = append(req.Sources, source)
	}

	rep, err := a.Merger.Integrate(req)
	if err != nil {
		return "", err
	}
	if rep.Level != MergeL1 || rep.ParentUpdated {
		return "", fmt.Errorf("隔离集成被拒绝 (%s): %s", rep.Level, rep.Reason)
	}
	if !rep.Merged {
		return "", fmt.Errorf("隔离合并未完成: %s", rep.Reason)
	}
	if !rep.TestPassed {
		return "", fmt.Errorf("合并结果测试未通过: %s", rep.Reason)
	}
	if rep.MergeCommit == "" {
		return "", fmt.Errorf("隔离合并结果缺少 MergeCommit")
	}
	return rep.MergeCommit, nil
}

// Integrate 执行集成计划中的每个批次（阶段集成）。
// 每个批次从 plan.BaseCommit（父分支基线）合并该批次的已登记 delivery SHA；
// 依赖顺序由 git 历史保证（依赖 task 的分支历史包含被依赖 task）。
// 返回按批次顺序的合并结果 SHA。阶段集成绝不更新正式父分支。
func (a *Assembler) Integrate(plan IntegrationPlan) ([]string, Result) {
	if a.Merge == nil {
		return nil, Result{Status: StatusError, Error: "Assembler 未配置 MergeOps 隔离集成原语"}
	}
	// 重新校验跨 Task 契约：防直接调用 Integrate 绕过 Plan 的契约校验。
	if res := validateCrossTaskContracts(a.Store, a.LoadTaskState, plan.ProgramID, taskIDsFromPlan(plan)); res.Status != StatusPass {
		return nil, res
	}
	var commits []string
	for i, batch := range plan.Batches {
		shas := make([]string, 0, len(batch.Tasks))
		for _, id := range batch.Tasks {
			shas = append(shas, batch.SHAs[id])
		}
		sha, err := a.Merge.MergeSources(plan.BaseCommit, shas)
		if err != nil {
			return nil, Result{Status: StatusError, Error: fmt.Sprintf("批次 %d 集成失败: %v", i+1, err)}
		}
		commits = append(commits, sha)
	}
	return commits, Result{Status: StatusPass}
}

// taskIDsFromPlan 从集成计划批次收集全部 task_id（去重）。
func taskIDsFromPlan(plan IntegrationPlan) []string {
	set := map[string]struct{}{}
	for _, b := range plan.Batches {
		for _, id := range b.Tasks {
			set[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BuildDeliveryBranchMap 从 ProgramRegistry 构建 delivery SHA -> 分支名映射，
// 供 AssemblerAgent 构造 MergerMergeOps 时注入，保留 Merger 的来源分支绑定校验。
func BuildDeliveryBranchMap(reg ProgramRegistry) map[string]string {
	m := map[string]string{}
	for _, d := range reg.Deliveries {
		if d.DeliverySHA != "" && d.Branch != "" {
			m[d.DeliverySHA] = d.Branch
		}
	}
	return m
}
