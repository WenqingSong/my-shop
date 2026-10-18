package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件实现 Pre-Task Intake 的机器可读事实记录（intake.yaml）与 Markdown front-matter 解析。
//
// Intake 是正式 Task 分支创建之前的受控调查单元：Discovery 用它记录原始需求、父分支候选与
// 当前 Git Base，Owner 决策与对应 Intake Evidence 绑定，TaskBuilder 后续据此验证并初始化 Task。
//
// intake.yaml 不是「另一份 State Schema」：它不含 schema_version 生命周期、不含 phase/状态机，
// 只记录 Intake 阶段的不可变事实（intake_id / parent_branch / base_commit），用于保证「初始父分支
// 与 Commit 不会丢失」。Task 执行状态仍以 .agent/tasks/<id>/state.yaml 为唯一权威。

// IntakeRecord 是 Pre-Task Intake 的机器可读事实记录。
type IntakeRecord struct {
	IntakeID     string `yaml:"intake_id"`
	ParentBranch string `yaml:"parent_branch"`
	BaseCommit   string `yaml:"base_commit"`
}

// IntakeRoot 是 Intake 记录必须严格位于其下的目录（相对仓库根）。
const IntakeRoot = ".agent/intake"

// intakeDir 返回 Intake 目录相对路径。
func intakeDir(intakeID string) string {
	return filepath.Join(IntakeRoot, intakeID)
}

// intakeYAMLPath 返回 intake.yaml 相对路径。
func intakeYAMLPath(intakeID string) string {
	return filepath.Join(intakeDir(intakeID), "intake.yaml")
}

// ownerDecisionPath 返回 owner-decision.md 相对路径。
func ownerDecisionPath(intakeID string) string {
	return filepath.Join(intakeDir(intakeID), "owner-decision.md")
}

// taskBuilderHandoffPath 返回 taskbuilder-handoff.md 相对路径。
func taskBuilderHandoffPath(intakeID string) string {
	return filepath.Join(intakeDir(intakeID), "taskbuilder-handoff.md")
}

// validIntakeID 报告 intake_id 是否为安全单一路径段（复用 scope_id 的安全约束，拒绝路径穿越）。
func validIntakeID(id string) bool {
	return ValidScopeID(id)
}

// validateIntakeRecord 校验 intake.yaml 的结构完整性（fail-closed）。
func validateIntakeRecord(r IntakeRecord) error {
	if !validIntakeID(r.IntakeID) {
		return fmt.Errorf("非法的 intake_id=%q（必须为不穿越路径的单一标识）", r.IntakeID)
	}
	if r.ParentBranch == "" {
		return fmt.Errorf("intake.parent_branch 不得为空")
	}
	if r.BaseCommit == "" || !validSHA(r.BaseCommit) {
		return fmt.Errorf("非法的 intake.base_commit=%q（期望完整 Commit SHA）", r.BaseCommit)
	}
	return nil
}

// parseIntakeRecord 解析并校验 intake.yaml。
func parseIntakeRecord(raw []byte) (IntakeRecord, error) {
	var r IntakeRecord
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return IntakeRecord{}, fmt.Errorf("解析 intake.yaml: %w", err)
	}
	if err := validateIntakeRecord(r); err != nil {
		return IntakeRecord{}, err
	}
	return r, nil
}

// loadIntakeRecord 从仓库根读取并解析 intake.yaml（intakeID 已校验安全）。
func (v *Validator) loadIntakeRecord(intakeID string) (IntakeRecord, Result) {
	if !validIntakeID(intakeID) {
		return IntakeRecord{}, Result{Status: StatusError, Error: fmt.Sprintf("非法的 intake_id=%q", intakeID)}
	}
	raw, err := os.ReadFile(filepath.Join(v.Root, intakeYAMLPath(intakeID)))
	if err != nil {
		if os.IsNotExist(err) {
			return IntakeRecord{}, Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: 文件不存在", intakeYAMLPath(intakeID))}
		}
		return IntakeRecord{}, Result{Status: StatusError, Error: fmt.Sprintf("读取 %s: %v", intakeYAMLPath(intakeID), err)}
	}
	r, err := parseIntakeRecord(raw)
	if err != nil {
		return IntakeRecord{}, Result{Status: StatusError, Error: fmt.Sprintf("解析 %s: %v", intakeYAMLPath(intakeID), err)}
	}
	return r, Result{Status: StatusPass}
}

// extractFrontMatter 提取 Markdown 文件开头的 YAML front-matter。
// 约定：文件以 "---\n" 开始，遇到第二个独占一行的 "---" 结束；否则视为无 front-matter。
func extractFrontMatter(content []byte) ([]byte, bool) {
	s := string(content)
	if !strings.HasPrefix(s, "---\n") {
		return nil, false
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, false
	}
	return []byte(rest[:idx]), true
}

// loadMarkdownFrontMatter 读取相对路径 Markdown 文件并提取其 front-matter（若存在）。
// 文件缺失视为「无 front-matter」（ok=false, err=nil），由调用方 Gate 判定 FAIL 而非 ERROR：
// 这对应「无 Owner Decision 时第二阶段入口 FAIL」等业务语义。其它读取错误返回 error。
func (v *Validator) loadMarkdownFrontMatter(rel string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(v.Root, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	fm, ok := extractFrontMatter(raw)
	return fm, ok, nil
}
