package workflow

import "path"

// 本文件定义 Review-neutral / Delivery-neutral 白名单（task-scoped，禁止 basename-only）。
//
// 白名单必须精确限定在 .agent/tasks/<current-task>/ 下：
//   - review-neutral：CLEAN evidence commit 之后允许的 Artifact tail（INV-4）；
//   - delivery-neutral：delivery.feature_head 之后允许的 Artifact tail（更窄，INV-6）。
//
// 其他目录、其他 task 的同名文件（如 .agent/tasks/other/state.yaml）一律判 substantive。

// reviewNeutralBasenames 是 review tail 允许的文件名（精确匹配，不含子目录）。
var reviewNeutralBasenames = map[string]struct{}{
	"findings.md":       {},
	"core-logic.md":     {},
	"owner-decision.md": {},
	"delivery.md":       {},
	"state.yaml":        {},
}

// deliveryNeutralBasenames 是 delivery tail 允许的文件名（更窄）。
var deliveryNeutralBasenames = map[string]struct{}{
	"delivery.md": {},
	"state.yaml":  {},
}

// taskScoped 报告路径 p 是否精确位于 .agent/tasks/<task>/ 下（不含更深的子目录）。
func taskScoped(taskSlug, p string) bool {
	return path.Dir(p) == path.Join(".agent", "tasks", taskSlug)
}

// isReviewNeutral 报告变更路径 p 是否为当前 task 的 review-neutral Artifact。
func isReviewNeutral(taskSlug, p string) bool {
	if !taskScoped(taskSlug, p) {
		return false
	}
	_, ok := reviewNeutralBasenames[path.Base(p)]
	return ok
}

// isDeliveryNeutral 报告变更路径 p 是否为当前 task 的 delivery-neutral Artifact。
func isDeliveryNeutral(taskSlug, p string) bool {
	if !taskScoped(taskSlug, p) {
		return false
	}
	_, ok := deliveryNeutralBasenames[path.Base(p)]
	return ok
}

// isReviewSubstantial 报告变更路径 p 是否为 review 视角的实质变化（default-deny）。
func isReviewSubstantial(taskSlug, p string) bool {
	return !isReviewNeutral(taskSlug, p)
}

// isDeliverySubstantial 报告变更路径 p 是否为 delivery 视角的实质变化（default-deny）。
func isDeliverySubstantial(taskSlug, p string) bool {
	return !isDeliveryNeutral(taskSlug, p)
}
