package workflow

import (
	"fmt"
	"path/filepath"
)

// 本文件实现 V4 正式 Handoff 的出口 Gate（handoff-prepared）与入口 Gate（handoff-entry）。
//
// 固定顺序（协议，非调度）：
//   当前角色完成交付物 → 持久化 Handoff Artifact（与证据 Commit 绑定）→ commit/push + clean-exit
//   → 出口 Gate PASS → 当前 Session 停止 → Owner 手动启动下一角色 → 下一角色检查入口 Gate
//   → 下一角色持久化 ACK → 开始执行。
//
// 出口 Gate 不要求下一角色尚未产生的 ACK；入口 Gate 校验 Handoff 存在且有效，同样不要求 ACK。
// 两个 Gate 都只读、都不启动任何 Agent。

// handoffTaskSlug 返回 Handoff 校验用的任务 slug（与其它 Gate 一致）。
func handoffTaskSlug(taskDir string) string {
	return filepath.Base(filepath.Clean(taskDir))
}

// requireHandoff 是 Handoff Gate 的共享前置：读取并解析 state、校验 schema、要求 V4 且 Handoff 已齐备。
// 返回错误即直接作为结果返回。
func (v *Validator) requireHandoff(taskDir string) (State, Result) {
	taskSlug := handoffTaskSlug(taskDir)
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return State{}, res
	}
	if s.SchemaVersion != SchemaV4 {
		return State{}, Result{Status: StatusError, Error: fmt.Sprintf("handoff 协议仅适用于 schema_version=4 任务（当前 %d）", s.SchemaVersion)}
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return State{}, Result{Status: StatusFail, Issues: issues}
	}
	if s.Handoff == nil {
		return State{}, failResult(taskSlug, "Handoff", "handoff section 缺失", "存在已准备好的 Handoff", "缺 Handoff 不得通过")
	}
	if !s.Handoff.Prepared {
		return State{}, failResult(taskSlug, "Handoff", "handoff.prepared=false", "true", "Handoff 尚未 Prepared，不得通过")
	}
	if !handoffPreparedComplete(s.Handoff) {
		return State{}, failResult(taskSlug, "Handoff", "handoff 字段不完整", "scope/scope_id/next_role/workflow/evidence/next_action/source_commit 齐备", "Handoff 缺失交接必要字段")
	}
	return s, Result{Status: StatusPass}
}

// handoffEvidenceValid 校验 Handoff 的不可变来源 Commit 与版本化 Evidence Snapshot 的 Git 真实性：
//   - source_commit 是真实 commit 且是当前 feature HEAD 的祖先（含等于）；
//   - 每条 evidence item 的 SHA 是真实 commit、是 HEAD 祖先，且（若指定 path）该 commit 内确实存在该文件。
//
// 这是「已失效 Evidence 不得通过」与「非法 SHA 不得通过」的机械判定。它不做任何写操作。
func (v *Validator) handoffEvidenceValid(taskSlug string, h *Handoff) Result {
	if ok, err := v.Git.CommitExists(h.SourceCommit); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("check handoff.source_commit %s: %v", h.SourceCommit, err)}
	} else if !ok {
		return failResult(taskSlug, "Handoff Evidence", h.SourceCommit, "真实 commit", "handoff.source_commit 不是真实 commit（非法 SHA）")
	}
	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}
	if ok, err := v.Git.IsAncestor(h.SourceCommit, head); err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", h.SourceCommit, err)}
	} else if !ok {
		return failResult(taskSlug, "Handoff Evidence", h.SourceCommit, "当前 feature HEAD 的祖先", "handoff.source_commit 不在当前 feature 历史中（Evidence 已失效）")
	}

	for i, item := range h.Evidence.Items {
		if ok, err := v.Git.CommitExists(item.SHA); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("check handoff.evidence.items[%d].sha %s: %v", i, item.SHA, err)}
		} else if !ok {
			return failResult(taskSlug, "Handoff Evidence", fmt.Sprintf("evidence[%d].sha=%s", i, item.SHA), "真实 commit", "evidence SHA 不是真实 commit（非法 SHA）")
		}
		if ok, err := v.Git.IsAncestor(item.SHA, head); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", item.SHA, err)}
		} else if !ok {
			return failResult(taskSlug, "Handoff Evidence", fmt.Sprintf("evidence[%d].sha=%s", i, item.SHA), "当前 feature HEAD 的祖先", "evidence SHA 不在当前 feature 历史中（Evidence 已失效）")
		}
		if item.Path != "" {
			if ok, err := v.Git.PathExistsAt(item.SHA, item.Path); err != nil {
				return Result{Status: StatusError, Error: fmt.Sprintf("check evidence[%d].path %s@%s: %v", i, item.Path, item.SHA, err)}
			} else if !ok {
				return failResult(taskSlug, "Handoff Evidence", fmt.Sprintf("evidence[%d].path=%s@%s", i, item.Path, item.SHA), "该 commit 内含该文件", "evidence 文件在指定 commit 中不存在（Evidence 已失效）")
			}
		}
	}
	return Result{Status: StatusPass}
}

// GateHandoffPrepared 是 Handoff 出口 Gate：当前角色已持久化并 commit/push 一个有效 Handoff，
// 且工作区 clean、remote synced，才允许当前 Session 停止。
//
// 它明确不要求下一角色的 ACK（出口不因未收到 ACK 而阻断）。
func (v *Validator) GateHandoffPrepared(taskDir string) Result {
	taskSlug := handoffTaskSlug(taskDir)
	s, res := v.requireHandoff(taskDir)
	if res.Status != StatusPass {
		return res
	}
	if res := v.handoffEvidenceValid(taskSlug, s.Handoff); res.Status != StatusPass {
		return res
	}
	// Commit 绑定：Handoff Artifact 必须是已提交快照的一部分，不能 commit 后修改 Handoff
	// 产生脏工作区却误报 Ready。由 handoffReady（非 detached、clean、remote 存在、local==remote）
	// 机械保证：工作区 clean 即所有 Artifact（含 state.yaml 中的 handoff）都已 commit；local==remote
	// 即已 push，下一 Session 接手的事实稳定。
	if res := v.handoffReady(); res.Status != StatusPass {
		return res
	}
	return Result{Status: StatusPass}
}

// GateHandoffEntry 是 Handoff 入口 Gate：下一角色开始正式职责执行前，校验收到的 Handoff 存在且有效。
//
// 它不要求 ACK（ACK 由下一角色在入口通过后自行持久化），也不要求下一角色工作区状态。
func (v *Validator) GateHandoffEntry(taskDir string) Result {
	taskSlug := handoffTaskSlug(taskDir)
	s, res := v.requireHandoff(taskDir)
	if res.Status != StatusPass {
		return res
	}
	return v.handoffEvidenceValid(taskSlug, s.Handoff)
}
