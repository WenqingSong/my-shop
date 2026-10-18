package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// 本文件实现 V4 COORDINATED Program 的独立 Program State（Task 12）。
//
// 与 Task State 的关键差异：
//   - 物理隔离：Program State 位于 .agent/programs/<program-id>/program-state.yaml，
//     Task State 位于 .agent/tasks/<task-id>/state.yaml，二者绝不混用同一文件；
//   - 语义隔离：Program State 只承载「Program 层」的决策事实（子 Task 清单、依赖、
//     阶段性/最终集成结果、Program 整体审查与 Owner 决策），不把 Program 状态塞入
//     Task 的 Schema V4（Task State 只通过 program.id / program.version 引用归属）。
//
// V4 层级冻结为 Program → Task，绝不允许 Program → Program 或 Task → Task 嵌套：
//   - Program → Program：本 schema 不提供任何「父 Program」字段，strict decode
//     （KnownFields）会拒绝任何试图声明父 Program 的未知字段；
//   - Task → Task：ProgramTask 不提供任何「子 Task」字段，子 Task 一旦出现在 plan 里
//     即被 strict decode 拒绝；且 task_type 只能是 NORMAL/COMPLEX/DESIGN，不存在
//     可表达「Program」的 Task 工作流模式。
//
// 本结构只做解析与 fail-closed 校验，不启动任何 Agent、不创建任何子 Task、
// 不驱动任何调度。

// ProgramSchemaV1 是 Program State 的 schema_version。
const ProgramSchemaV1 = 1

// ProgramTask 是 Owner 原规划中的一个子 Task（唯一权威来源是 Owner 的初始规划）。
type ProgramTask struct {
	TaskID       string   `yaml:"task_id"`
	TaskType     TaskType `yaml:"task_type"` // NORMAL | COMPLEX | DESIGN（复用 Task 枚举）
	Branch       string   `yaml:"branch"`    // 该子 Task 的独立分支
	Dependencies []string `yaml:"dependencies,omitempty"`
}

// ProgramPhaseIntegration 记录一次阶段性集成结果（不可变 Evidence）。
// 阶段 PASS 不等于 Program 整体 PASS，两者必须分列。
type ProgramPhaseIntegration struct {
	BatchID  string   `yaml:"batch_id"`
	Status   string   `yaml:"status"` // PASS | FAIL
	Tasks    []string `yaml:"tasks"`
	SHA      string   `yaml:"sha"`      // 阶段性合并结果 commit SHA
	Evidence string   `yaml:"evidence"` // 阶段 Evidence 文件路径（program 作用域）
}

// ProgramFinalIntegration 是 Program 最终整体集成结果（不可变 Evidence）。
type ProgramFinalIntegration struct {
	Status   string `yaml:"status"` // NOT_RUN | PASS | FAIL
	SHA      string `yaml:"sha"`    // 最终集成 commit SHA
	Evidence string `yaml:"evidence"`
}

// ProgramReview 是 Program Cleaner 对 Program 整体的独立审查结论。
//
// 时序（F5）：Assembler 产生最终集成快照 C0 → Cleaner 审查 C0 → Cleaner 产生正式报告 →
// 报告被提交为证据 Commit C1 → Program State 登记 C0 与 C1 的关系。因此：
//
//   - Target（C0）：被审查的最终集成 commit，必须等于 final_integration.sha；
//   - Evidence：Cleaner 审查完成后产生的正式审查报告路径（相对仓库根，受限路径）；
//   - EvidenceCommit（C1）：保存该审查报告的后续 commit（C0 之后产生，C0 不得预含报告）；
//   - Status：CLEAN 仅对 Target 有效，且必须绑定合法的 Evidence + EvidenceCommit。
//
// Evidence / EvidenceCommit 为向后兼容的可选扩展：旧状态（仅 status/target）仍可解析，
// 但缺失证据会在 Program Cleaner Gate / 最终交付 Gate FAIL。注意 C1 中的报告只绑定 C0
// （不得填写 C1 自己的 SHA，避免自引用循环）；C1 的引用由后续 Review State 提交（C2）保存。
type ProgramReview struct {
	Status         string `yaml:"status"`                    // NOT_REQUESTED | PENDING | CLEAN | CHANGES_REQUIRED
	Target         string `yaml:"target"`                    // 被审查的最终集成 commit SHA（C0）
	Evidence       string `yaml:"evidence,omitempty"`        // 正式审查报告路径（绑定 EvidenceCommit）
	EvidenceCommit string `yaml:"evidence_commit,omitempty"` // 保存审查报告的 commit SHA（C1）
}

// ProgramOwner 是 Owner 对 Program 整体的最终决策。
type ProgramOwner struct {
	Status string `yaml:"status"` // PENDING | ACCEPTED | REJECTED
	Target string `yaml:"target"` // Owner 接受的最终集成 Evidence Commit SHA
}

// ProgramState 是 Program 独立状态的统一内部表示。
type ProgramState struct {
	SchemaVersion     int                       `yaml:"schema_version"`
	ProgramID         string                    `yaml:"program_id"`
	ParentBranch      string                    `yaml:"parent_branch"` // 初始化时绑定的父分支
	BaseCommit        string                    `yaml:"base_commit"`   // 初始化时绑定的 Base Commit
	Tasks             []ProgramTask             `yaml:"tasks"`
	PhaseIntegrations []ProgramPhaseIntegration `yaml:"phase_integrations,omitempty"`
	FinalIntegration  ProgramFinalIntegration   `yaml:"final_integration"`
	Review            ProgramReview             `yaml:"review"`
	Owner             ProgramOwner              `yaml:"owner"`
}

// ProgramDirPath 返回 program 目录的相对路径（仓库根）。
func ProgramDirPath(programID string) string {
	return filepath.Join(".agent", "programs", programID)
}

// ProgramStatePath 返回 program-state.yaml 的固定路径（相对仓库根）。
func ProgramStatePath(programID string) string {
	return filepath.Join(ProgramDirPath(programID), "program-state.yaml")
}

// ParseProgramState 解析并校验 program-state.yaml（strict decode + fail-closed）。
// 返回 error 属于 schema / 输入 ERROR（exit 2）。
func ParseProgramState(raw []byte) (ProgramState, error) {
	var ps ProgramState
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&ps); err != nil {
		return ProgramState{}, fmt.Errorf("解析 program-state.yaml: %w", err)
	}
	if err := ValidateProgramState(ps); err != nil {
		return ProgramState{}, err
	}
	return ps, nil
}

// LoadProgramState 从仓库根读取指定 program 的 program-state.yaml。
func LoadProgramState(root, programID string) (ProgramState, error) {
	raw, err := os.ReadFile(filepath.Join(root, ProgramStatePath(programID)))
	if err != nil {
		return ProgramState{}, fmt.Errorf("读取 %s: %w", ProgramStatePath(programID), err)
	}
	return ParseProgramState(raw)
}

// LoadTaskStateFile 从仓库根读取指定 task 的 state.yaml（供 Collector/Assembler 复用）。
func LoadTaskStateFile(root, taskID string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(root, ".agent", "tasks", taskID, "state.yaml"))
	if err != nil {
		return State{}, fmt.Errorf("读取 .agent/tasks/%s/state.yaml: %w", taskID, err)
	}
	return parseState(raw)
}

// ValidateProgramState 校验 Program State 的结构与冻结层级（fail-closed）。
func ValidateProgramState(ps ProgramState) error {
	if ps.SchemaVersion != ProgramSchemaV1 {
		return fmt.Errorf("不支持的 program schema_version=%d（期望 %d）", ps.SchemaVersion, ProgramSchemaV1)
	}
	if !ValidScopeID(ps.ProgramID) {
		return fmt.Errorf("非法的 program_id=%q（必须为不穿越路径的单一标识）", ps.ProgramID)
	}
	if ps.ParentBranch == "" {
		return fmt.Errorf("缺少必填 parent_branch")
	}
	if !validRequiredSHA(ps.BaseCommit) {
		return fmt.Errorf("非法的 base_commit=%q（必须为完整 Commit SHA）", ps.BaseCommit)
	}
	if len(ps.Tasks) == 0 {
		return fmt.Errorf("program 至少需要一个子 Task（tasks 不得为空）")
	}

	seen := map[string]struct{}{}
	for i, t := range ps.Tasks {
		if !ValidScopeID(t.TaskID) {
			return fmt.Errorf("tasks[%d].task_id=%q 非法（必须为不穿越路径的单一标识）", i, t.TaskID)
		}
		if !ValidTaskType(t.TaskType) {
			// 冻结层级：Program 不是 Task 工作流模式，task_type 不得为 PROGRAM。
			return fmt.Errorf("tasks[%d].task_type=%q 非法（期望 NORMAL | COMPLEX | DESIGN）", i, t.TaskType)
		}
		if t.Branch == "" {
			return fmt.Errorf("tasks[%d].branch 不得为空", i)
		}
		if _, dup := seen[t.TaskID]; dup {
			return fmt.Errorf("tasks 中存在重复的 task_id=%q", t.TaskID)
		}
		seen[t.TaskID] = struct{}{}
		for _, dep := range t.Dependencies {
			if dep == t.TaskID {
				return fmt.Errorf("tasks[%d] 自依赖 task_id=%q", i, t.TaskID)
			}
		}
	}

	// 依赖必须指向已规划的 task（不指向外部 Program / 未规划的 task）。
	for i, t := range ps.Tasks {
		for _, dep := range t.Dependencies {
			if _, ok := seen[dep]; !ok {
				return fmt.Errorf("tasks[%d].dependencies 引用未规划的 task_id=%q", i, dep)
			}
		}
	}

	// 阶段性集成结果：状态枚举与 SHA 绑定基本一致性。
	for i, p := range ps.PhaseIntegrations {
		if p.BatchID == "" {
			return fmt.Errorf("phase_integrations[%d].batch_id 不得为空", i)
		}
		if p.Status != "PASS" && p.Status != "FAIL" {
			return fmt.Errorf("phase_integrations[%d].status=%q 非法（期望 PASS | FAIL）", i, p.Status)
		}
		if p.SHA == "" {
			return fmt.Errorf("phase_integrations[%d].sha 不得为空", i)
		}
	}

	switch ps.FinalIntegration.Status {
	case "", "NOT_RUN":
	case "PASS", "FAIL":
		if !validRequiredSHA(ps.FinalIntegration.SHA) {
			return fmt.Errorf("final_integration.status=%q 必须绑定合法 sha", ps.FinalIntegration.Status)
		}
	default:
		return fmt.Errorf("final_integration.status=%q 非法（期望 NOT_RUN | PASS | FAIL）", ps.FinalIntegration.Status)
	}

	if err := validateProgramReview(ps.Review); err != nil {
		return err
	}
	if err := validateProgramOwner(ps.Owner); err != nil {
		return err
	}
	return nil
}

func validateProgramReview(r ProgramReview) error {
	switch r.Status {
	case "", ReviewNotRequested, ReviewPending, ReviewChangesRequired:
	case ReviewClean:
		if !validRequiredSHA(r.Target) {
			return fmt.Errorf("review.status=CLEAN 必须绑定 review.target")
		}
		if r.EvidenceCommit != "" && !validRequiredSHA(r.EvidenceCommit) {
			return fmt.Errorf("review.evidence_commit=%q 非法（必须为完整 Commit SHA）", r.EvidenceCommit)
		}
	default:
		return fmt.Errorf("review.status=%q 非法", r.Status)
	}
	return nil
}

func validateProgramOwner(o ProgramOwner) error {
	switch o.Status {
	case "", OwnerPending, OwnerRejected:
	case OwnerAccepted:
		if !validRequiredSHA(o.Target) {
			return fmt.Errorf("owner.status=ACCEPTED 必须绑定 owner.target")
		}
	default:
		return fmt.Errorf("owner.status=%q 非法", o.Status)
	}
	return nil
}

// validRequiredSHA 报告 s 是否为非空的合法完整 Commit SHA（40 或 64 位十六进制）。
func validRequiredSHA(s string) bool {
	return s != "" && validSHA(s)
}
