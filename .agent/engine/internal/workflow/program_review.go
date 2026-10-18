package workflow

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件实现 Program Cleaner 正式审查报告（Review Evidence）的严格解析与证据路径约束（F5）。
//
// 与 Task 级 review-neutral 白名单（paths.go）刻意分离：Program 审查报告是「Cleaner 审查 C0 后
// 产生的正式证据」，不是 review-neutral tail，而是独立绑定、可机器解析的 Evidence。报告内容必须
// 严格绑定 Program ID / 审查目标 / 审查结论，杜绝用 README.md 等任意普通文件冒充审查证据。

// ProgramCleanerReviewKind 是 Program Cleaner 正式审查报告的类型判别符（evidence type）。
// 它不是身份认证：机器无法验证「报告作者是否真的是 Cleaner」，身份信任仍由仓库权限与正式
// 工作流约束承担。报告文本中的任何角色声明都不作为可机器验证的身份。
const ProgramCleanerReviewKind = "program-cleaner-review"

// ProgramCleanerEvidenceName 是 Program Cleaner 审查报告在 Program 目录下的固定文件名。
const ProgramCleanerEvidenceName = "cleaner-review.yaml"

// ProgramCleanerReport 是 Program Cleaner 正式审查报告的机器可解析结构。
// 字段严格最小化，任何未知字段都会被 strict decode 拒绝。
type ProgramCleanerReport struct {
	ProgramID string `yaml:"program_id"` // 报告所属 Program ID
	Kind      string `yaml:"kind"`       // 证据类型，必须为 program-cleaner-review
	Target    string `yaml:"target"`     // 报告绑定的被审查 commit SHA（C0）
	Status    string `yaml:"status"`     // 审查结论，必须为 CLEAN
}

// ProgramCleanerEvidencePath 返回审查报告在 Program 目录下的固定路径（相对仓库根）。
func ProgramCleanerEvidencePath(programID string) string {
	return filepath.Join(ProgramDirPath(programID), ProgramCleanerEvidenceName)
}

// ParseProgramCleanerReport 严格解析审查报告（strict decode + fail-closed）。
// 返回 error 表示报告格式非法（gate 视角为 FAIL，而非输入 ERROR，由调用方决定语义）。
func ParseProgramCleanerReport(raw []byte) (ProgramCleanerReport, error) {
	var r ProgramCleanerReport
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return ProgramCleanerReport{}, fmt.Errorf("解析审查报告: %w", err)
	}
	if err := validateProgramCleanerReport(r); err != nil {
		return ProgramCleanerReport{}, err
	}
	return r, nil
}

// validateProgramCleanerReport 校验报告字段（fail-closed）。
func validateProgramCleanerReport(r ProgramCleanerReport) error {
	if !ValidScopeID(r.ProgramID) {
		return fmt.Errorf("审查报告 program_id=%q 非法", r.ProgramID)
	}
	if r.Kind != ProgramCleanerReviewKind {
		return fmt.Errorf("审查报告 kind=%q 非法（期望 %s）", r.Kind, ProgramCleanerReviewKind)
	}
	if !validRequiredSHA(r.Target) {
		return fmt.Errorf("审查报告 target=%q 非法（必须为完整 Commit SHA）", r.Target)
	}
	if r.Status != ReviewClean {
		return fmt.Errorf("审查报告 status=%q 非法（期望 CLEAN）", r.Status)
	}
	return nil
}

// normalizeEvidencePath 将证据路径归一化为斜杠分隔的 clean 相对路径（拒绝路径穿越）。
func normalizeEvidencePath(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}

// isProgramCleanerEvidencePath 报告 p 是否严格等于当前 Program 的审查证据固定路径。
// 默认拒绝（default-deny）：任何其他路径（README.md、任意普通文件、其他 program 目录等）一律不通过。
// 该判定同时用于：review.evidence 路径约束，以及证据 commit C1 相对 C0 的证据 tail 白名单。
func isProgramCleanerEvidencePath(programID, p string) bool {
	return normalizeEvidencePath(p) == ProgramCleanerEvidencePath(programID)
}
