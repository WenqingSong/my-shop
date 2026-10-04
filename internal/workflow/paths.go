package workflow

import "path"

// 本文件定义 Review Target 的实质变化判定（default-deny + 白名单豁免）。
//
// default-deny：自 review.target_base 起，任何非白名单变化默认使旧 CLEAN 失效。
// 白名单（Review-neutral，唯一豁免，显式有限）：findings.md、core-logic.md、
// delivery.md、合法 state.yaml 机械状态持久化、Contract 明确批准的其他纯 Workflow Evidence。
// Cleaner 更新这些文件不使 CLEAN 失效。
//
// review.target_paths 是 Cleaner 对本轮审查范围的 Evidence / Audit Record（审查了什么），
// 不是 STALE 判定边界、不是允许变化列表、也不是隐式白名单；「不在 target_paths」≠「可以忽略变化」。

// reviewNeutralBasenames 是 Review-neutral 白名单（按 basename 匹配，唯一豁免）。
var reviewNeutralBasenames = map[string]struct{}{
	"findings.md":   {},
	"core-logic.md": {},
	"delivery.md":   {},
	"state.yaml":    {},
}

// isReviewNeutral 报告变更路径是否为 Review-neutral（白名单豁免），
// 即该变化不会使 CLEAN 失效。
func isReviewNeutral(p string) bool {
	_, ok := reviewNeutralBasenames[path.Base(p)]
	return ok
}

// isSubstantialChange 报告变更路径是否为实质变化（default-deny）：
// 除白名单外的任何变化都视为实质变化，会导致旧 CLEAN 客观失效。
func isSubstantialChange(p string) bool {
	return !isReviewNeutral(p)
}
