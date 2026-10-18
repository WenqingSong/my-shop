package workflow

import (
	"fmt"
	"sort"
)

// 本文件实现 Program Assembler（Task 13）。
//
// Assembler 负责：检查已登记 Task、校验依赖图、确定允许的集成批次、使用确切已登记
// SHA、在隔离分支执行阶段性合并、执行跨模块接口/契约检查、保存不可变 Evidence。
//
// 边界：
//   - Assembler 通过 MergeOps 复用工作包 B 的隔离 Git 合并原语，不复制第二套合并工具；
//   - MergeOps 未提供（B 尚未交付）时，Assembler 仍可独立完成依赖校验、Registry、
//     Evidence 绑定与集成计划逻辑，实际合并执行标记为 PENDING_INTEGRATION；
//   - 阶段性集成绝不更新正式父分支，阶段 PASS 绝不冒充整个 Program 的 PASS。

// MergeOps 是 Assembler 复用的隔离 Git 合并原语契约（由工作包 B 提供）。
// 本包只定义契约，不在生产代码中复制 B 的合并实现。
type MergeOps interface {
	// MergeSources 在隔离环境把 sources（确切 SHA）合并到 base，返回合并结果 commit SHA。
	// 失败返回 error（不得返回伪造的 PASS SHA）。
	MergeSources(base string, sources []string) (string, error)
}

// IntegrationBatch 是一次集成批次：一组 task 与其确切已登记 SHA。
type IntegrationBatch struct {
	Tasks []string          // 本批 task_id
	SHAs  map[string]string // task_id -> 已登记 delivery_sha（后续集成的确切 SHA）
}

// IntegrationPlan 是 Assembler 生成的集成计划。
type IntegrationPlan struct {
	ProgramID    string
	ParentBranch string
	BaseCommit   string
	Batches      []IntegrationBatch
}

// Assembler 是 Program 集成计划与校验器。
type Assembler struct {
	Store         *RegistryStore
	Git           Git // 只读 Git（SHA 真实性核验）
	LoadTaskState TaskStateLoader
	Merge         MergeOps // B 提供；为 nil 时仅做计划与校验（实际合并标记 PENDING_INTEGRATION）
}

// Plan 检查已登记 Task、校验依赖图、生成集成批次（绑定确切已登记 SHA）。
func (a *Assembler) Plan(program ProgramState) (IntegrationPlan, Result) {
	plan := IntegrationPlan{
		ProgramID:    program.ProgramID,
		ParentBranch: program.ParentBranch,
		BaseCommit:   program.BaseCommit,
	}
	reg, err := a.Store.Load(program.ProgramID)
	if err != nil {
		return plan, Result{Status: StatusError, Error: err.Error()}
	}

	// 1. 全部必需子 Task 已登记。
	registered := map[string]RegisteredDelivery{}
	for _, d := range reg.Deliveries {
		registered[d.TaskID] = d
	}
	var missing []string
	for _, t := range program.Tasks {
		if _, ok := registered[t.TaskID]; !ok {
			missing = append(missing, t.TaskID)
		}
	}
	if len(missing) > 0 {
		return plan, Result{
			Status: StatusFail,
			Issues: []Issue{{
				Task:     program.ProgramID,
				Check:    "Assembler Plan",
				Actual:   fmt.Sprintf("未登记: %v", missing),
				Expected: "全部子 Task 已登记",
				Reason:   "依赖已登记 Task 才能集成",
			}},
		}
	}

	// 2. 依赖图合法（无环）。
	if issues := ValidateDependencyGraph(program.Tasks); len(issues) > 0 {
		return plan, Result{Status: StatusFail, Issues: issues}
	}

	// 3. 交付 SHA 未失效（仍为真实 commit）。
	for _, t := range program.Tasks {
		d := registered[t.TaskID]
		if a.Git != nil {
			if ok, err := a.Git.CommitExists(d.DeliverySHA); err != nil {
				return plan, Result{Status: StatusError, Error: fmt.Sprintf("核验 %s delivery_sha: %v", t.TaskID, err)}
			} else if !ok {
				return plan, Result{
					Status: StatusFail,
					Issues: []Issue{{
						Task:     t.TaskID,
						Check:    "Assembler Plan",
						Actual:   d.DeliverySHA,
						Expected: "真实 commit",
						Reason:   "已登记 delivery_sha 已失效",
					}},
				}
			}
		}
	}

	// 4. 依赖满足性：每个 task 的依赖都必须已登记。
	for _, t := range program.Tasks {
		for _, dep := range t.Dependencies {
			if _, ok := registered[dep]; !ok {
				return plan, Result{
					Status: StatusFail,
					Issues: []Issue{{
						Task:     t.TaskID,
						Check:    "Assembler Plan",
						Actual:   fmt.Sprintf("依赖 %s 未登记", dep),
						Expected: "依赖已登记",
						Reason:   "依赖未满足不能集成",
					}},
				}
			}
		}
	}

	// 5. 跨 Task 契约校验（共享资源冲突 / COMPLEX 缺 APPROVED Contract）。
	// 输入必须来自已登记 delivery_sha 的准确 Task Evidence，不得采用可能前进的分支 HEAD。
	if res := validateCrossTaskContracts(a.Store, a.LoadTaskState, program.ProgramID, programTaskIDs(program)); res.Status != StatusPass {
		return plan, res
	}

	// 6. 拓扑分批，绑定确切已登记 SHA。
	batches, err := ComputeBatches(program.Tasks)
	if err != nil {
		return plan, Result{Status: StatusError, Error: err.Error()}
	}
	for _, batch := range batches {
		ib := IntegrationBatch{Tasks: batch, SHAs: map[string]string{}}
		for _, id := range batch {
			ib.SHAs[id] = registered[id].DeliverySHA
		}
		plan.Batches = append(plan.Batches, ib)
	}
	return plan, Result{Status: StatusPass}
}

// CheckCrossTaskContracts 校验跨 Task 契约与不变量（Task 13「跨模块契约」机械层）。
// 覆盖两类可机械判定的跨任务不变量：
//   - 全局共享资源冲突：两个不同 Task 预留了同一 resource kind 的同一 value；
//   - COMPLEX Task 未绑定 APPROVED Contract（跨任务契约缺失）。
//
// 返回问题列表（空 = 通过）。出现任何问题都应阻塞整体集成。
func CheckCrossTaskContracts(tasks []State) []Issue {
	var issues []Issue

	// resource kind -> value -> task_id
	claims := map[string]map[string]string{}
	for _, s := range tasks {
		for _, kind := range sortedKeys(s.Resources.Reservations) {
			if claims[kind] == nil {
				claims[kind] = map[string]string{}
			}
			for _, v := range s.Resources.Reservations[kind] {
				if prev, ok := claims[kind][v]; ok && prev != s.TaskID {
					issues = append(issues, Issue{
						Task:     s.TaskID,
						Check:    "Cross-task Contract",
						Actual:   fmt.Sprintf("resource %s value=%s 与 task %s 冲突", kind, v, prev),
						Expected: "全局共享资源无跨 Task 冲突",
						Reason:   "多个子 Task 预留了同一资源，跨模块契约冲突",
					})
				}
				claims[kind][v] = s.TaskID
			}
		}
	}

	// COMPLEX Task 必须绑定 APPROVED Contract。
	for _, s := range tasks {
		if s.TaskType != TaskTypeComplex {
			continue
		}
		if s.Contract.Status != ContractApproved || s.Contract.Target == "" {
			issues = append(issues, Issue{
				Task:     s.TaskID,
				Check:    "Cross-task Contract",
				Actual:   fmt.Sprintf("contract.status=%s target=%q", s.Contract.Status, s.Contract.Target),
				Expected: "APPROVED 且绑定 contract.target",
				Reason:   "COMPLEX 子 Task 缺少已批准 Contract，跨模块契约不满足",
			})
		}
	}
	return issues
}

// programTaskIDs 返回 Program 规划中的全部 task_id（保持规划顺序）。
func programTaskIDs(program ProgramState) []string {
	ids := make([]string, 0, len(program.Tasks))
	for _, t := range program.Tasks {
		ids = append(ids, t.TaskID)
	}
	return ids
}

// loadTaskEvidence 通过注入的 loader 读取 task state；loader 缺失时回退到 Store.Root 文件加载。
func loadTaskEvidence(load TaskStateLoader, store *RegistryStore, taskID string) (State, error) {
	if load != nil {
		return load(taskID)
	}
	if store != nil && store.Root != "" {
		return LoadTaskStateFile(store.Root, taskID)
	}
	return State{}, fmt.Errorf("未配置 LoadTaskState 且无 Store Root，无法加载 Task Evidence")
}

// validateCrossTaskContracts 从 Program Registry 的已登记交付加载准确 Task Evidence，并校验跨 Task 契约（F1）。
//
// 关键约束：
//   - 输入必须来自已登记 delivery_sha：state.delivery.feature_head 必须与已登记 delivery_sha 一致，
//     否则视为漂移，fail-closed（绝不采用可能前进的分支 HEAD）；
//   - 缺失已登记 Evidence、资源冲突、COMPLEX 缺 APPROVED Contract 均 fail-closed。
//
// 该函数是 Plan / Integrate / Gate 三条路径共用的唯一跨 Task 契约校验入口。
func validateCrossTaskContracts(store *RegistryStore, load TaskStateLoader, programID string, taskIDs []string) Result {
	reg, err := store.Load(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	registered := map[string]RegisteredDelivery{}
	for _, d := range reg.Deliveries {
		registered[d.TaskID] = d
	}

	ids := append([]string(nil), taskIDs...)
	sort.Strings(ids)

	states := make([]State, 0, len(ids))
	for _, id := range ids {
		d, ok := registered[id]
		if !ok {
			return failResult(id, "Cross-task Contract", id, "已登记交付",
				"缺已登记 Task Evidence，不能校验跨 Task 契约")
		}
		st, err := loadTaskEvidence(load, store, id)
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("加载 %s 证据: %v", id, err)}
		}
		if st.Delivery.FeatureHead != d.DeliverySHA {
			return failResult(id, "Cross-task Contract",
				fmt.Sprintf("state.feature_head=%s delivery_sha=%s", st.Delivery.FeatureHead, d.DeliverySHA),
				d.DeliverySHA,
				"跨 Task 契约必须使用已登记版本的准确 Evidence，不得采用漂移后的分支状态")
		}
		states = append(states, st)
	}

	if issues := CheckCrossTaskContracts(states); len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	return Result{Status: StatusPass}
}
