package workflow

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// 本文件实现 V4 state.yaml 的版本化解析与校验。
//
// V4 在 V3 核心决策事实语义之上新增：
//   - task_type（Task 工作流模式 NORMAL/COMPLEX/DESIGN）
//   - git（初始化时确认的 parent_branch / base_commit / task_branch）
//   - contract.revision / contract.history（Contract Revision）
//   - design（DESIGN 架构审批 / 定版）
//   - handoff（Handoff Prepared / ACK / 入口，仅交接协议记录，不驱动 Agent 调度）
//   - event_log（仅日志定位，非审批权威）
//   - program（Program 归属引用，不承载 Program 内部状态）
//
// V4 不引入全局 phase / STALE / DONE；next_role 不作为可驱动 Agent 调度的状态字段。

// stateFileV4 是 V4 state.yaml 的磁盘表示。
// 与 V2/V3 的 stateFile 分离：V4 文档只能包含 V4 字段，任何未知顶层/嵌套字段都被 KnownFields(true) 严格拒绝。
// 可选 section（design/handoff/event_log/program）用指针区分「缺失」与「存在但为空」。
type stateFileV4 struct {
	SchemaVersion int                  `yaml:"schema_version"`
	TaskID        string               `yaml:"task_id"`
	TaskType      TaskType             `yaml:"task_type"`
	Git           *GitBaseline         `yaml:"git"`
	Contract      Contract             `yaml:"contract"`
	Resources     map[string]yaml.Node `yaml:"resources"`
	Review        Review               `yaml:"review"`
	Owner         Owner                `yaml:"owner"`
	Delivery      Delivery             `yaml:"delivery"`
	Blocked       Blocked              `yaml:"blocked"`
	Design        *Design              `yaml:"design"`
	Handoff       *Handoff             `yaml:"handoff"`
	EventLog      *EventLog            `yaml:"event_log"`
	Program       *ProgramRef          `yaml:"program"`
	Intake        *IntakeRef           `yaml:"intake"`
}

// parseStateV4 解析 V4 state.yaml，strict decode 后 normalize 为统一内部 State。
// 返回 error 属于 schema / 输入 ERROR（exit 2），不属于业务 Gate FAIL。
func parseStateV4(raw []byte) (State, error) {
	var f stateFileV4
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return State{}, fmt.Errorf("解析 state.yaml: %w", err)
	}

	s := State{
		SchemaVersion: f.SchemaVersion,
		TaskID:        f.TaskID,
		Contract:      f.Contract,
		Review:        f.Review,
		Owner:         f.Owner,
		Delivery:      f.Delivery,
		Blocked:       f.Blocked,
		TaskType:      f.TaskType,
		Git:           f.Git,
		Design:        f.Design,
		Handoff:       f.Handoff,
		EventLog:      f.EventLog,
		Program:       f.Program,
		Intake:        f.Intake,
	}

	res, err := normalizeResources(f.SchemaVersion, f.Resources)
	if err != nil {
		return State{}, err
	}
	s.Resources = res
	if err := validateReservations(s.Resources.Reservations); err != nil {
		return State{}, err
	}
	if err := validateStateV4(s); err != nil {
		return State{}, err
	}
	return s, nil
}

// validateStateV4 校验 V4 必填事实与枚举（fail-closed）。
//
// 注意：task_id 非空与 contract/review/owner/delivery 枚举由共享的 schemaValidation 统一校验
// （对 V2/V3/V4 一致）；本函数只负责 V4 专属的必填事实（task_type / git.*）、V4 专属枚举
// （design.status）与 V4 专属结构完整性（contract.revision/history、design 版本与 target、
// program 引用）。可选 section（design/program 等）缺失时不校验，不误拒绝早期合法 Task。
func validateStateV4(s State) error {
	if s.TaskType == "" {
		return fmt.Errorf("缺少必填 task_type（NORMAL | COMPLEX | DESIGN）")
	}
	if !ValidTaskType(s.TaskType) {
		return fmt.Errorf("非法的 task_type=%q（期望 NORMAL | COMPLEX | DESIGN）", s.TaskType)
	}
	if s.Git == nil {
		return fmt.Errorf("缺少必填 git（parent_branch / base_commit / task_branch）")
	}
	if s.Git.ParentBranch == "" {
		return fmt.Errorf("缺少必填 git.parent_branch")
	}
	if s.Git.BaseCommit == "" {
		return fmt.Errorf("缺少必填 git.base_commit")
	}
	if s.Git.TaskBranch == "" {
		return fmt.Errorf("缺少必填 git.task_branch")
	}
	if err := validateContractV4(s.Contract); err != nil {
		return err
	}
	if err := validateDesignV4(s.Design); err != nil {
		return err
	}
	if err := validateHandoffV4(s.Handoff); err != nil {
		return err
	}
	if s.Program != nil {
		if s.Program.ID == "" {
			return fmt.Errorf("program.id 不得为空")
		}
		if s.Program.Version <= 0 {
			return fmt.Errorf("非法的 program.version=%d（必须为正数）", s.Program.Version)
		}
	}
	if s.Intake != nil {
		if s.Intake.ID == "" {
			return fmt.Errorf("intake.id 不得为空")
		}
	}
	return nil
}

// validateContractV4 校验 V4 专属的 contract.revision / contract.history 结构完整性。
// 只做结构校验（非负版本号、有效 target），不实现 Contract 重新审批判定（属于 Task 2）。
func validateContractV4(c Contract) error {
	if c.Revision < 0 {
		return fmt.Errorf("非法的 contract.revision=%d（不得为负数）", c.Revision)
	}
	for i, h := range c.History {
		if h.Revision <= 0 {
			return fmt.Errorf("非法的 contract.history[%d].revision=%d（必须为正数）", i, h.Revision)
		}
		if h.Target == "" {
			return fmt.Errorf("contract.history[%d].target 不得为空", i)
		}
	}
	return nil
}

// validateDesignV4 校验 V4 专属的 design 结构完整性（design 为 nil 表示 section 缺失，直接通过）。
// 只做版本号非负与 target 基本一致性校验，不实现 DESIGN Gate（属于 Phase 05）。
func validateDesignV4(d *Design) error {
	if d == nil {
		return nil
	}
	if !ValidDesignStatus(d.Status) {
		return fmt.Errorf("非法的 design.status=%q（期望 NOT_REQUIRED | PENDING | APPROVED | REJECTED）", d.Status)
	}
	if d.ApprovedVersion < 0 {
		return fmt.Errorf("非法的 design.approved_version=%d（不得为负数）", d.ApprovedVersion)
	}
	if d.FinalVersion < 0 {
		return fmt.Errorf("非法的 design.final_version=%d（不得为负数）", d.FinalVersion)
	}
	if d.Status == DesignApproved && d.ApprovedTarget == "" {
		return fmt.Errorf("design.status=APPROVED 必须绑定 approved_target")
	}
	if d.FinalVersion > 0 && d.FinalTarget == "" {
		return fmt.Errorf("design.final_version=%d 要求绑定 final_target", d.FinalVersion)
	}
	return nil
}
