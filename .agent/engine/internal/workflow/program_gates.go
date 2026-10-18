package workflow

import (
	"fmt"
	"os"
)

// 本文件实现 Program 级只读 Gate（Task 14 最终交付 + Assembler 前置）。
//
// 合法交付链路：
//   Program Discovery → Owner 决策 → Program 初始化 → 独立 Task × N → Collector 登记
//   → Assembler 集成 → Program Cleaner → OwnerGate → Owner 最终 ACCEPT → Merger。
//
// Program 最终 Gate 不做任何合并、不推送父分支；它只机械核验：
//   - 全部必需子 Task 已登记；
//   - 依赖关系满足；
//   - 最终集成包含正确 SHA 与不可变 Evidence；
//   - Program Cleaner 独立审查 CLEAN；
//   - Owner 最终 ACCEPT；
//   - 目标父分支未漂移（漂移则要求重新验证）。
//
// 最终交付由 Merger（工作包 B）在隔离环境验证并交付，而非 Collector / Assembler 直接推送。

// ProgramValidator 提供 Program 级只读校验。
type ProgramValidator struct {
	Root          string
	Git           Git
	Store         *RegistryStore
	LoadTaskState TaskStateLoader
}

// LoadProgramState 读取 program-state.yaml。
func (pv *ProgramValidator) LoadProgramState(programID string) (ProgramState, error) {
	return LoadProgramState(pv.Root, programID)
}

// LoadRegistry 读取 registry.yaml。
func (pv *ProgramValidator) LoadRegistry(programID string) (ProgramRegistry, error) {
	return pv.Store.Load(programID)
}

// GateAssemblerStart 是 Assembler 开始阶段性/最终集成的前置 Gate：
// 全部必需子 Task 已登记 + 依赖满足 + 依赖图合法 + 交付 SHA 未失效。
func (pv *ProgramValidator) GateAssemblerStart(programID string) Result {
	program, err := pv.LoadProgramState(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	reg, err := pv.LoadRegistry(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}

	registered := map[string]bool{}
	for _, d := range reg.Deliveries {
		registered[d.TaskID] = true
	}

	var issues []Issue
	for _, t := range program.Tasks {
		if !registered[t.TaskID] {
			issues = append(issues, Issue{
				Task:     programID,
				Check:    "Assembler Start",
				Actual:   fmt.Sprintf("task %s 未登记", t.TaskID),
				Expected: "全部必需子 Task 已登记",
				Reason:   "缺必需子 Task 不能集成",
			})
		}
		for _, dep := range t.Dependencies {
			if !registered[dep] {
				issues = append(issues, Issue{
					Task:     t.TaskID,
					Check:    "Assembler Start",
					Actual:   fmt.Sprintf("依赖 %s 未登记", dep),
					Expected: "依赖已登记",
					Reason:   "依赖未满足不能集成",
				})
			}
		}
	}
	if len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	if di := ValidateDependencyGraph(program.Tasks); len(di) > 0 {
		return Result{Status: StatusFail, Issues: di}
	}

	// 交付 SHA 未失效。
	for _, d := range reg.Deliveries {
		if pv.Git != nil {
			if ok, err := pv.Git.CommitExists(d.DeliverySHA); err != nil {
				return Result{Status: StatusError, Error: fmt.Sprintf("核验 %s delivery_sha: %v", d.TaskID, err)}
			} else if !ok {
				issues = append(issues, Issue{
					Task:     d.TaskID,
					Check:    "Assembler Start",
					Actual:   d.DeliverySHA,
					Expected: "真实 commit",
					Reason:   "已登记 delivery_sha 已失效",
				})
			}
		}
	}
	if len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}

	// 跨 Task 契约校验（共享资源冲突 / COMPLEX 缺 APPROVED Contract），输入来自已登记 Evidence。
	if res := pv.checkCrossTaskContracts(program); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// checkCrossTaskContracts 用 Program 规划中的全部 task_id 校验跨 Task 契约。
func (pv *ProgramValidator) checkCrossTaskContracts(program ProgramState) Result {
	return validateCrossTaskContracts(pv.Store, pv.LoadTaskState, program.ProgramID, programTaskIDs(program))
}

// GateFinalDelivery 是 Program 最终交付 Gate（Task 14）。
func (pv *ProgramValidator) GateFinalDelivery(programID string) Result {
	program, err := pv.LoadProgramState(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	reg, err := pv.LoadRegistry(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}

	var issues []Issue
	check := "Program Final Delivery"

	// 1. 全部必需子 Task 已登记。
	registered := map[string]RegisteredDelivery{}
	for _, d := range reg.Deliveries {
		registered[d.TaskID] = d
	}
	for _, t := range program.Tasks {
		if _, ok := registered[t.TaskID]; !ok {
			issues = append(issues, Issue{
				Task:     programID,
				Check:    check,
				Actual:   fmt.Sprintf("task %s 未登记", t.TaskID),
				Expected: "全部必需子 Task 已登记",
				Reason:   "缺必需子 Task",
			})
		}
		// 2. 依赖满足。
		for _, dep := range t.Dependencies {
			if _, ok := registered[dep]; !ok {
				issues = append(issues, Issue{
					Task:     t.TaskID,
					Check:    check,
					Actual:   fmt.Sprintf("依赖 %s 未登记", dep),
					Expected: "依赖已登记",
					Reason:   "依赖关系未满足",
				})
			}
		}
	}

	// 3. 跨 Task 契约校验（共享资源冲突 / COMPLEX 缺 APPROVED Contract）。
	if res := pv.checkCrossTaskContracts(program); res.Status != StatusPass {
		if res.Status == StatusError {
			return res
		}
		issues = append(issues, res.Issues...)
	}

	// 4. 最终集成 Evidence：状态 PASS + 正确 SHA + 证据文件。
	if program.FinalIntegration.Status != "PASS" {
		issues = append(issues, Issue{
			Task:     programID,
			Check:    check,
			Actual:   fmt.Sprintf("final_integration.status=%s", program.FinalIntegration.Status),
			Expected: "PASS",
			Reason:   "缺最终集成通过 Evidence",
		})
	}
	if !validRequiredSHA(program.FinalIntegration.SHA) {
		issues = append(issues, Issue{
			Task:     programID,
			Check:    check,
			Actual:   "final_integration.sha 缺失",
			Expected: "最终集成 commit SHA",
			Reason:   "缺最终集成 Evidence SHA",
		})
	}
	if program.FinalIntegration.Evidence == "" {
		issues = append(issues, Issue{
			Task:     programID,
			Check:    check,
			Actual:   "final_integration.evidence 缺失",
			Expected: "最终集成 Evidence 文件路径",
			Reason:   "缺最终集成 Evidence 引用",
		})
	}

	// 5. Program Cleaner 独立审查事实：消费 GateProgramCleaner（核对 target 绑定、
	// 独立审查证据、SHA 真实性、与最终交付对象一致性），绝不只依赖可编辑的 CLEAN 字符串。
	if res := pv.GateProgramCleaner(programID); res.Status != StatusPass {
		if res.Status == StatusError {
			return res
		}
		issues = append(issues, res.Issues...)
	}

	// 6. Owner 最终 ACCEPT。
	if program.Owner.Status != OwnerAccepted {
		issues = append(issues, Issue{
			Task:     programID,
			Check:    check,
			Actual:   fmt.Sprintf("owner.status=%s", program.Owner.Status),
			Expected: "ACCEPTED",
			Reason:   "Owner 最终未 ACCEPT，禁止交付",
		})
	}

	// 7. 目标父分支漂移检测。
	if pv.Git != nil {
		if res := pv.parentBranchStable(program); res.Status != StatusPass {
			if res.Status == StatusError {
				return res
			}
			issues = append(issues, res.Issues...)
		}
	}

	if len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	return Result{Status: StatusPass}
}

// GateProgramCleaner 是 Program Cleaner 的独立只读 Gate（Task 14 / F5）。
//
// 它核对 Cleaner 审查事实是否真实、有效、未漂移，并严格解析独立审查报告内容（默认拒绝
// 任意普通文件冒充）：
//   - review.status == CLEAN；
//   - review.target（C0）绑定最终集成 commit 且与 final_integration.sha 一致（未漂移）；
//   - review.target 是真实 commit；
//   - review.evidence 是受限的正式审查报告路径（.agent/programs/<id>/cleaner-review.yaml）；
//   - review.evidence_commit（C1）是真实 commit，且是 C0 的严格后代（报告在 C0 之后产生）；
//   - C1 中的审查报告可被读取并严格解析（program_id / kind / target / status 全部匹配）；
//   - C0..C1 之间仅允许审查报告本身，任何实质修改使旧 CLEAN 失效。
//
// 不生成 CLEAN，不替人类 Owner 作最终批准；全部只读。机器无法验证真实 Owner / Cleaner 身份，
// 当前信任边界是 Git 不可变 commit 与仓库权限：审查对象一旦发生实质修改，旧 CLEAN 不再有效。
func (pv *ProgramValidator) GateProgramCleaner(programID string) Result {
	program, err := pv.LoadProgramState(programID)
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	check := "Program Cleaner Review"
	var issues []Issue
	add := func(actual, expected, reason string) {
		issues = append(issues, Issue{Task: programID, Check: check, Actual: actual, Expected: expected, Reason: reason})
	}

	if program.Review.Status != ReviewClean {
		add(fmt.Sprintf("review.status=%s", program.Review.Status), "CLEAN", "Program Cleaner 尚未 CLEAN")
		return Result{Status: StatusFail, Issues: issues}
	}

	// 审查必须针对已完成的最终集成对象。
	if program.FinalIntegration.Status != "PASS" {
		add(fmt.Sprintf("final_integration.status=%s", program.FinalIntegration.Status), "PASS", "Cleaner 审查必须针对已完成的最终集成对象")
	}

	// 审查目标（C0）绑定最终集成 commit（对象一致，未漂移）。
	c0 := program.FinalIntegration.SHA
	if !validRequiredSHA(program.Review.Target) {
		add("review.target 缺失", "最终集成 commit SHA", "缺 Cleaner 审查目标绑定")
	} else if program.Review.Target != c0 {
		add(fmt.Sprintf("review.target=%s final_integration.sha=%s", program.Review.Target, c0),
			"review.target == final_integration.sha", "审查对象与当前最终交付对象不一致，旧 CLEAN 不再有效")
	}

	// 独立审查报告路径（受限，默认拒绝任意普通文件）。
	if program.Review.Evidence == "" {
		add("review.evidence 缺失", "正式审查报告路径", "缺独立审查报告，不能证明审查对象真实有效")
	} else if !isProgramCleanerEvidencePath(programID, program.Review.Evidence) {
		add(fmt.Sprintf("review.evidence=%s", program.Review.Evidence), ProgramCleanerEvidencePath(programID),
			"审查报告必须位于受限的 Program 审查证据路径，默认拒绝任意普通文件")
	}

	// 独立审查证据 commit（C1）。
	c1 := program.Review.EvidenceCommit
	if !validRequiredSHA(c1) {
		add("review.evidence_commit 缺失", "保存审查报告的 commit SHA", "缺证据 commit，不能证明审查报告在 C0 之后已固化")
	}

	// 只读 Git 校验：C0/C1 真实性、时序、报告内容、证据 tail。
	if pv.Git != nil {
		if err := pv.verifyCleanerEvidenceGit(program, c0, c1, &issues); err != nil {
			return Result{Status: StatusError, Error: err.Error()}
		}
	}

	if len(issues) > 0 {
		return Result{Status: StatusFail, Issues: issues}
	}
	return Result{Status: StatusPass}
}

// verifyCleanerEvidenceGit 执行 GateProgramCleaner 的只读 Git 机械核验，追加 issue 到 issues。
// 仅运行时错误（git 无法执行、对象不存在）返回 error（exit 2）；业务不满足通过 issue 表达（FAIL）。
func (pv *ProgramValidator) verifyCleanerEvidenceGit(program ProgramState, c0, c1 string, issues *[]Issue) error {
	check := "Program Cleaner Review"
	add := func(actual, expected, reason string) {
		*issues = append(*issues, Issue{Task: program.ProgramID, Check: check, Actual: actual, Expected: expected, Reason: reason})
	}

	// C0/C1 是真实 commit（记录真实性，后续只读 Git 操作仅在真实对象上进行）。
	c0Real, c1Real := false, false
	if validRequiredSHA(c0) {
		if ok, err := pv.Git.CommitExists(c0); err != nil {
			return fmt.Errorf("核验 review.target: %v", err)
		} else if ok {
			c0Real = true
		} else {
			add(c0, "真实 commit", "review.target 不是真实 commit，审查 SHA 失效")
		}
	}
	if validRequiredSHA(c1) {
		if ok, err := pv.Git.CommitExists(c1); err != nil {
			return fmt.Errorf("核验 review.evidence_commit: %v", err)
		} else if ok {
			c1Real = true
		} else {
			add(c1, "真实 commit", "review.evidence_commit 不是真实 commit，证据 SHA 失效")
		}
	}

	// C1 必须是 C0 的严格后代（报告在 C0 之后产生，C0 不得预含报告）。
	if validRequiredSHA(c0) && validRequiredSHA(c1) {
		if c0 == c1 {
			add(fmt.Sprintf("review.evidence_commit == review.target == %s", c0), "C1 严格晚于 C0",
				"证据 commit 必须产生在最终集成 commit 之后，不得要求 C0 预含审查报告")
		} else if c0Real && c1Real {
			if ok, err := pv.Git.IsAncestor(c0, c1); err != nil {
				return fmt.Errorf("is-ancestor %s %s: %v", c0, c1, err)
			} else if !ok {
				add(fmt.Sprintf("review.evidence_commit=%s 不是 review.target=%s 的后代", c1, c0), "C1 是 C0 的后代",
					"证据 commit 必须继承被审查的最终集成 commit，审查证据与审查对象脱节")
			}
		}
	}

	// C0 不得预含审查报告（杜绝时序倒置）：报告必须由 C1 首次新增。
	// 此前仅依赖 C0..C1 diff 白名单，无法区分「C1 新增报告」与「C0 已含报告、C1 仅修改」，
	// 会错误放行 C0 预存报告、C1 改为合法内容的时序倒置场景。
	if c0Real && program.Review.Evidence != "" && isProgramCleanerEvidencePath(program.ProgramID, program.Review.Evidence) {
		exists, err := pv.Git.PathExistsAt(c0, program.Review.Evidence)
		if err != nil {
			return fmt.Errorf("核验 C0 是否预含审查报告: %v", err)
		}
		if exists {
			add(fmt.Sprintf("审查报告 %s 已存在于 review.target=%s", program.Review.Evidence, c0),
				"C0 不得预含审查报告",
				"审查报告必须由证据 commit C1 首次新增，C0 预含报告属于时序倒置")
		}
	}

	// 读取 C1 中的审查报告并严格解析、核对一致性。
	if c1Real && program.Review.Evidence != "" && isProgramCleanerEvidencePath(program.ProgramID, program.Review.Evidence) {
		if err := pv.verifyCleanerReport(program, c1, issues); err != nil {
			return err
		}
	}

	// C0..C1 证据 tail：仅允许审查报告本身，任何实质修改使旧 CLEAN 失效；
	// 且审查报告必须由 C1 新增（不得预先存在于 C0，杜绝时序倒置）。
	if c0Real && c1Real && c0 != c1 {
		changed, err := pv.Git.DiffNameOnlyRange(c0, c1)
		if err != nil {
			return fmt.Errorf("diff %s..%s: %v", c0, c1, err)
		}
		reportAdded := false
		substantive := ""
		for _, p := range changed {
			if isProgramCleanerEvidencePath(program.ProgramID, p) {
				reportAdded = true
			} else if substantive == "" {
				substantive = p
			}
		}
		if substantive != "" {
			add(fmt.Sprintf("%s 在 C0 之后发生变化", substantive), "仅审查报告本身",
				"审查证据 commit 混入生产代码、设计文件或其他实质修改，旧 CLEAN 失效")
		}
		if !reportAdded {
			add("审查报告未在 C1 相对 C0 的新增文件中", "审查报告由 C1 新增",
				"审查报告必须由证据 commit 产生，不得预先存在于 C0（禁止时序倒置）")
		}
	}
	return nil
}

// verifyCleanerReport 读取 C1 中的审查报告并严格解析，核对与 Program 当前 Review 字段一致。
func (pv *ProgramValidator) verifyCleanerReport(program ProgramState, c1 string, issues *[]Issue) error {
	check := "Program Cleaner Review"
	add := func(actual, expected, reason string) {
		*issues = append(*issues, Issue{Task: program.ProgramID, Check: check, Actual: actual, Expected: expected, Reason: reason})
	}
	raw, err := pv.Git.ShowFile(c1, program.Review.Evidence)
	if err != nil {
		if os.IsNotExist(err) {
			add(fmt.Sprintf("review.evidence=%s 不在 review.evidence_commit=%s", program.Review.Evidence, c1),
				"审查报告存在于证据 commit", "证据 commit 不包含正式审查报告")
			return nil
		}
		return fmt.Errorf("读取审查报告 %s@%s: %v", program.Review.Evidence, c1, err)
	}
	rep, err := ParseProgramCleanerReport([]byte(raw))
	if err != nil {
		add(fmt.Sprintf("审查报告格式非法: %v", err), "合法的 Program Cleaner 审查报告",
			"审查报告必须严格可解析，缺少必要字段、类型错误或格式错误即 FAIL")
		return nil
	}
	if rep.ProgramID != program.ProgramID {
		add(fmt.Sprintf("报告 program_id=%s", rep.ProgramID), program.ProgramID, "审查报告 Program ID 与当前 Program 不一致")
	}
	if rep.Target != program.Review.Target {
		add(fmt.Sprintf("报告 target=%s", rep.Target), program.Review.Target, "审查报告声明的目标 SHA 与 review.target 不一致")
	}
	if rep.Status != program.Review.Status {
		add(fmt.Sprintf("报告 status=%s", rep.Status), program.Review.Status, "审查报告审查结论与 review.status 不一致")
	}
	return nil
}

// parentBranchStable 检测目标父分支是否漂移：初始化绑定的 base_commit 必须仍是
// 当前 origin/<parent_branch> 的祖先（未发生 rewrite / divergence）。
func (pv *ProgramValidator) parentBranchStable(program ProgramState) Result {
	ref := "origin/" + program.ParentBranch
	head, err := pv.Git.RevParse(ref)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("rev-parse %s: %v", ref, err)}
	}
	if ok, err := pv.Git.IsAncestor(program.BaseCommit, head); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s %s: %v", program.BaseCommit, head, err)}
	} else if !ok {
		return failResult(program.ProgramID, "Parent Branch Drift",
			fmt.Sprintf("%s=%s 不含 base_commit=%s", ref, head, program.BaseCommit),
			"base_commit 为父分支祖先",
			"目标父分支已漂移，需要重新验证后再交付")
	}
	return Result{Status: StatusPass}
}
