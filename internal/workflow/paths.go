package workflow

import (
	"path"
	"strings"
)

// 本文件定义 Review Target 的实质变化判定（default-deny + 白名单豁免）。
//
// 白名单（Review-neutral，唯一豁免）：findings.md、core-logic.md、delivery.md、
// state.yaml 中由合法 Transition 产生的机械状态持久化、其他 Contract 明确列出的
// 纯 Workflow Evidence。Cleaner 更新这些文件不使 CLEAN 失效。
//
// 必须触发集合（保证下限，非穷举）：Production Code（internal/、api/、main.go）、
// Business Tests（*_test.go）、contract.md、task.md 的 Scope/AC/Requirement、
// docs/design/*、migration SQL、runtime config、与本 Task 相关的 Registry 语义变化。
// default-deny：未明确豁免的实质变化默认导致旧 CLEAN 失效。

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

// mustTriggerPrefixes 是「必须触发」的目录前缀（Production Code、Design、runtime config、Registry）。
var mustTriggerPrefixes = []string{
	"internal/",
	"api/",
	"docs/design/",
	"manifest/",
	".agent/registry/",
}

// mustTriggerSuffixes 是「必须触发」的文件后缀（Business Tests、migration SQL）。
var mustTriggerSuffixes = []string{
	"_test.go",
	".sql",
}

// mustTriggerBasenames 是「必须触发」的精确文件名（主入口、Contract、Task 定义）。
// task.md 被保守整体纳入：机械上无法区分 Scope/AC/Requirement 与其它章节，
// default-deny 下 task.md 的任何变化都视为实质变化（实现风险已记录，交 Cleaner 复审）。
var mustTriggerBasenames = map[string]struct{}{
	"main.go":     {},
	"contract.md": {},
	"task.md":     {},
}

// isMustTrigger 报告变更路径是否命中「必须触发的最小集合」。
func isMustTrigger(p string) bool {
	clean := path.Clean(p)
	for _, prefix := range mustTriggerPrefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	for _, suffix := range mustTriggerSuffixes {
		if strings.HasSuffix(clean, suffix) {
			return true
		}
	}
	if _, ok := mustTriggerBasenames[path.Base(clean)]; ok {
		return true
	}
	return false
}

// isSubstantialChange 报告一个变更路径是否为实质变化：
// 非白名单，且命中必须触发集合 或 位于 review.target_paths 内。
func isSubstantialChange(p string, targetPaths []string) bool {
	if isReviewNeutral(p) {
		return false
	}
	if isMustTrigger(p) {
		return true
	}
	for _, tp := range targetPaths {
		if path.Clean(tp) == path.Clean(p) {
			return true
		}
	}
	return false
}
