package workflow

import "path"

// 本文件定义 Review-neutral / Delivery-neutral 白名单（task-scoped，禁止 basename-only）。
//
// 白名单必须精确限定在当前 Task 的物理目录下（旧平铺 .agent/tasks/<id>/ 或分类
// .agent/tasks/<category>/<id>/）：
//   - review-neutral：CLEAN evidence commit 之后允许的 Artifact tail（INV-4）；
//   - delivery-neutral：delivery.feature_head 之后允许的 Artifact tail（更窄，INV-6）。
//
// 其他目录、其他 Task 的同名文件一律判 substantive。

// reviewNeutralBasenames 是 review tail 允许的文件名（精确匹配，不含子目录）。
//
// events.md 是当前 Task 的诊断事件日志（见 EventLogProtocol.md），属于 neutral metadata，
// 与 state.yaml 同级。它必须严格位于当前 Task 目录下，由 taskScoped 精确限定；
// 其他 Task 的同名文件、子目录下的同名文件、以及 .agent/programs/*/events.md 一律 substantive
// （Program 事件路径不进入 task-scoped 白名单，单独考虑作用域，default-deny）。
var reviewNeutralBasenames = map[string]struct{}{
	"findings.md":       {},
	"core-logic.md":     {},
	"owner-decision.md": {},
	"delivery.md":       {},
	"state.yaml":        {},
	"events.md":         {},
}

// deliveryNeutralBasenames 是 delivery tail 允许的文件名（更窄）。
var deliveryNeutralBasenames = map[string]struct{}{
	"delivery.md": {},
	"state.yaml":  {},
	"events.md":   {},
}

// taskScoped 报告路径 p 是否精确位于当前 Task 的任一候选目录下（不含更深子目录）。
//
// 同时接受旧平铺与分类布局：Task 目录移动本身不应把 Artifact tail 误判为实质变化，
// 但候选集合只包含同一个 Task ID，不会把其他 Task 的同名文件当成 neutral。
func taskScoped(dirs []string, p string) bool {
	for _, dir := range dirs {
		if path.Dir(p) == dir {
			return true
		}
	}
	return false
}

// isReviewNeutral 报告变更路径 p 是否为当前 Task 的 review-neutral Artifact。
func isReviewNeutral(dirs []string, p string) bool {
	if !taskScoped(dirs, p) {
		return false
	}
	_, ok := reviewNeutralBasenames[path.Base(p)]
	return ok
}

// isDeliveryNeutral 报告变更路径 p 是否为当前 Task 的 delivery-neutral Artifact。
func isDeliveryNeutral(dirs []string, p string) bool {
	if !taskScoped(dirs, p) {
		return false
	}
	_, ok := deliveryNeutralBasenames[path.Base(p)]
	return ok
}

// isReviewSubstantial 报告变更路径 p 是否为 review 视角的实质变化（default-deny）。
func isReviewSubstantial(dirs []string, p string) bool {
	return !isReviewNeutral(dirs, p)
}

// isDeliverySubstantial 报告变更路径 p 是否为 delivery 视角的实质变化（default-deny）。
func isDeliverySubstantial(dirs []string, p string) bool {
	return !isDeliveryNeutral(dirs, p)
}
