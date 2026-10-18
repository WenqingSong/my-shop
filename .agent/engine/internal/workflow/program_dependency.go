package workflow

import (
	"fmt"
	"sort"
)

// 本文件实现 Program 依赖图校验与集成批次计算（Task 13 的 Assembler 前提逻辑）。
//
// 规则（fail-closed）：
//   - 依赖只能指向已规划的 task（ValidateProgramState 已保证）；
//   - 依赖图必须无环（自依赖已在 ValidateProgramState 拒绝）；
//   - 集成批次按拓扑层划分：某 task 的依赖全部位于更早批次，依赖未满足不得进入本批；
//   - 批次顺序稳定（按 task_id 字典序），保证结果可复现。

// ValidateDependencyGraph 校验依赖图无环、依赖存在，返回问题列表（空 = 通过）。
func ValidateDependencyGraph(tasks []ProgramTask) []Issue {
	index := map[string]int{}
	for i, t := range tasks {
		index[t.TaskID] = i
	}

	var issues []Issue
	// 环检测：DFS 三色标记。
	state := make([]int, len(tasks)) // 0=未访问 1=访问中 2=完成
	var stack []string
	var visit func(i int)
	visit = func(i int) {
		state[i] = 1
		stack = append(stack, tasks[i].TaskID)
		for _, dep := range tasks[i].Dependencies {
			j, ok := index[dep]
			if !ok {
				issues = append(issues, Issue{
					Task:     tasks[i].TaskID,
					Check:    "Program Dependency",
					Actual:   fmt.Sprintf("依赖未规划的 task %s", dep),
					Expected: "依赖必须指向已规划 task",
					Reason:   "依赖图引用越界",
				})
				continue
			}
			switch state[j] {
			case 1:
				issues = append(issues, Issue{
					Task:     tasks[i].TaskID,
					Check:    "Program Dependency",
					Actual:   fmt.Sprintf("存在环: %v → %s", stack, dep),
					Expected: "依赖图无环",
					Reason:   "检测到循环依赖",
				})
			case 0:
				visit(j)
			}
		}
		stack = stack[:len(stack)-1]
		state[i] = 2
	}
	for i := range tasks {
		if state[i] == 0 {
			visit(i)
		}
	}
	return issues
}

// ComputeBatches 按拓扑层计算集成批次。
// 返回 [][]string：每一批是一组 task_id（组内无相互依赖），组间按依赖顺序。
func ComputeBatches(tasks []ProgramTask) ([][]string, error) {
	if issues := ValidateDependencyGraph(tasks); len(issues) > 0 {
		return nil, fmt.Errorf("依赖图非法: %s", issues[0].String())
	}

	index := map[string]int{}
	for i, t := range tasks {
		index[t.TaskID] = i
	}

	// depCount[i] = task i 尚有多少未入批的依赖；dependents[i] = 依赖 task i 的 task 列表。
	depCount := make([]int, len(tasks))
	dependents := make([][]int, len(tasks))
	for i, t := range tasks {
		depCount[i] = len(t.Dependencies)
		for _, dep := range t.Dependencies {
			j := index[dep]
			dependents[j] = append(dependents[j], i)
		}
	}

	// 批次内按 task_id 排序保证确定性。
	sortByID := func(ids []int) []string {
		out := make([]string, len(ids))
		for k, id := range ids {
			out[k] = tasks[id].TaskID
		}
		sort.Strings(out)
		return out
	}

	var batches [][]string
	placed := make([]bool, len(tasks))
	for len(batches) == 0 || !allPlaced(placed) {
		var ready []int
		for i := range tasks {
			if !placed[i] && depCount[i] == 0 {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			// 无环图下不应发生；防御性报错。
			return nil, fmt.Errorf("无法继续分批（存在未被满足的依赖）")
		}
		for _, i := range ready {
			placed[i] = true
			for _, d := range dependents[i] {
				depCount[d]--
			}
		}
		batches = append(batches, sortByID(ready))
	}
	return batches, nil
}

func allPlaced(placed []bool) bool {
	for _, p := range placed {
		if !p {
			return false
		}
	}
	return true
}
