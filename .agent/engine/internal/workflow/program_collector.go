package workflow

import (
	"fmt"
)

// 本文件实现 Program Collector（Task 13）。
//
// Collector 只负责「交付登记」，绝不合并业务代码、绝不执行阶段性/最终集成。
// 它验证子 Task 属于当前 Program、已完成独立审查（CLEAN）、Owner 已 ACCEPT、
// 交付 SHA/分支/Evidence 有效，然后把交付登记到 Program Registry。
//
// Collector 无 Git Merge 写权限：本类型不提供任何 merge / branch 写操作，
// 仅通过只读 Git 接口核验 SHA 与祖先关系。

// TaskStateLoader 读取指定 task 的 state（可注入，便于测试）。
type TaskStateLoader func(taskID string) (State, error)

// Collector 是 Program 交付登记的校验与登记器。
type Collector struct {
	Store         *RegistryStore
	Git           Git // 只读 Git（SHA 存在性 / 分支核验）
	LoadTaskState TaskStateLoader
}

// Collect 校验并登记一个子 Task 的交付。
//
// 返回 Result：
//   - PASS：登记成功（registry 已写入）；
//   - FAIL：校验失败（未登记），Issues 说明原因；
//   - ERROR：运行级错误（IO / 冲突等），Error 说明原因。
//
// 登记对象 d 中的证据字段必须由调用方（CollectorAgent）从子 Task 的真实 state.yaml
// 机械提取；本方法再次交叉核验，防伪造/漂移。
func (c *Collector) Collect(program ProgramState, d RegisteredDelivery) Result {
	taskID := d.TaskID

	// 1. Task 属于当前 Program 规划。
	plan, ok := findPlanTask(program, taskID)
	if !ok {
		return failResult(taskID, "Collector Register", taskID, "program 规划内的 task",
			"子 Task 不属于当前 Program 的 Owner 原规划")
	}

	// 2. 独立 Task 已完成必要审查 + Owner ACCEPT + 交付 SHA/分支/Evidence。
	st, err := c.loadTaskState(taskID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if res := c.validateTaskEvidence(program, plan, st, d); res.Status != StatusPass {
		return res
	}

	// 3. 登记到 Program Registry（冲突/重复在此被拒绝）。
	if err := c.Store.Register(program.ProgramID, d); err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	return Result{Status: StatusPass}
}

// Validate 只读校验交付是否可登记（不写盘）。
func (c *Collector) Validate(program ProgramState, d RegisteredDelivery) Result {
	plan, ok := findPlanTask(program, d.TaskID)
	if !ok {
		return failResult(d.TaskID, "Collector Register", d.TaskID, "program 规划内的 task",
			"子 Task 不属于当前 Program 的 Owner 原规划")
	}
	st, err := c.loadTaskState(d.TaskID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	return c.validateTaskEvidence(program, plan, st, d)
}

func (c *Collector) loadTaskState(taskID string) (State, error) {
	if c.LoadTaskState == nil {
		return State{}, fmt.Errorf("Collector 未配置 LoadTaskState")
	}
	return c.LoadTaskState(taskID)
}

// validateTaskEvidence 交叉核验交付记录与子 Task 真实 state 的机械事实。
func (c *Collector) validateTaskEvidence(program ProgramState, plan ProgramTask, st State, d RegisteredDelivery) Result {
	check := "Collector Register"
	taskID := plan.TaskID

	// Task 归属：state.program 必须存在且 id 指向当前 Program（F2）。
	if st.Program == nil {
		return failResult(taskID, check, "state.program 缺失", "program 归属引用",
			"子 Task 未声明 program 归属，不能作为 Program 子 Task 登记")
	}
	if st.Program.ID != program.ProgramID {
		return failResult(taskID, check, fmt.Sprintf("state.program.id=%s", st.Program.ID), program.ProgramID,
			"子 Task 声明的 program 归属与当前 Program 不一致，拒绝登记")
	}
	// Program 版本绑定：Program State schema 目前未定义 program 级 version 字段，
	// 因此 state.program.version 无法与 Program 侧匹配；它已在 V4 解析层强制为正整数，
	// Collector 此处仍防御性拒绝非正 version，绝不默默忽略。
	if st.Program.Version <= 0 {
		return failResult(taskID, check, fmt.Sprintf("state.program.version=%d", st.Program.Version), "正整数",
			"子 Task 声明的 program.version 非法")
	}
	// 分支一致性：state.git.task_branch 必须等于计划分支。
	if st.Git == nil || st.Git.TaskBranch == "" {
		return failResult(taskID, check, "state.git.task_branch 缺失", plan.Branch, "缺少任务分支事实")
	}
	if st.Git.TaskBranch != plan.Branch {
		return failResult(taskID, check, fmt.Sprintf("task_branch=%s", st.Git.TaskBranch), plan.Branch,
			"任务分支与 Owner 规划分支不一致")
	}

	// 独立审查完成：review CLEAN + target 真实。
	if st.Review.Status != ReviewClean {
		return failResult(taskID, check, fmt.Sprintf("review.status=%s", st.Review.Status), "CLEAN",
			"子 Task 尚未完成独立审查")
	}
	if !validRequiredSHA(st.Review.Target) {
		return failResult(taskID, check, "review.target 为空", "<evidence-commit-sha>", "缺少 Review Evidence")
	}

	// Owner ACCEPT：绑定当前 review.target。
	if st.Owner.Status != OwnerAccepted {
		return failResult(taskID, check, fmt.Sprintf("owner.status=%s", st.Owner.Status), "ACCEPTED",
			"子 Task 尚未获得 Owner ACCEPT")
	}
	if st.Owner.ReviewTarget != st.Review.Target {
		return failResult(taskID, check, fmt.Sprintf("owner.review_target=%s", st.Owner.ReviewTarget), st.Review.Target,
			"Owner Acceptance 必须绑定当前 review.target")
	}

	// 交付 SHA：已登记交付版本必须真实存在且来自已验证交付。
	if st.Delivery.Status != DeliveryPass {
		return failResult(taskID, check, fmt.Sprintf("delivery.status=%s", st.Delivery.Status), "PASS",
			"子 Task 尚未通过独立交付验证")
	}
	if !validRequiredSHA(st.Delivery.FeatureHead) {
		return failResult(taskID, check, "delivery.feature_head 为空", "<evidence-commit-sha>", "缺少交付 feature snapshot")
	}

	// 登记记录的证据必须与真实 state 一致（防伪造）。
	if d.ReviewTarget != st.Review.Target {
		return failResult(taskID, check, fmt.Sprintf("review_target=%s", d.ReviewTarget), st.Review.Target,
			"登记 review_target 与子 Task 实际 review.target 不一致")
	}
	if d.OwnerTarget != st.Review.Target {
		return failResult(taskID, check, fmt.Sprintf("owner_target=%s", d.OwnerTarget), st.Review.Target,
			"登记 owner_target 与子 Task 实际 review.target 不一致")
	}
	if d.DeliverySHA != st.Delivery.FeatureHead {
		return failResult(taskID, check, fmt.Sprintf("delivery_sha=%s", d.DeliverySHA), st.Delivery.FeatureHead,
			"登记 delivery_sha 与子 Task 实际 delivery.feature_head 不一致")
	}
	if d.Branch != plan.Branch {
		return failResult(taskID, check, d.Branch, plan.Branch, "登记分支与规划分支不一致")
	}

	// SHA 真实性（只读 Git 核验，Git 缺失时跳过机械核验但不伪造通过）。
	if c.Git != nil {
		if ok, err := c.Git.CommitExists(d.DeliverySHA); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("核验 delivery_sha: %v", err)}
		} else if !ok {
			return failResult(taskID, check, d.DeliverySHA, "真实 commit", "delivery_sha 不是真实 commit")
		}
		if ok, err := c.Git.CommitExists(d.ReviewTarget); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("核验 review_target: %v", err)}
		} else if !ok {
			return failResult(taskID, check, d.ReviewTarget, "真实 commit", "review_target 不是真实 commit")
		}
	}
	return Result{Status: StatusPass}
}

func findPlanTask(program ProgramState, taskID string) (ProgramTask, bool) {
	for _, t := range program.Tasks {
		if t.TaskID == taskID {
			return t, true
		}
	}
	return ProgramTask{}, false
}
