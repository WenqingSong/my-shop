package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 本文件实现 Workflow V3.2.0 的「路线事实」（Route Authority）。
//
// 路线（NORMAL / COMPLEX / ARCHITECT）是 TaskBuilder 的决策，不是 Engine 的决策：
// Engine 只校验「已记录的路线事实」与其余可验证状态是否自相矛盾，绝不根据
// Contract、Scope 或聊天文本反推某个任务「应该」是哪条路线。
//
// 可验证的路线不变量（R-1 ~ R-4）：
//
//	R-1 route 枚举合法；缺省表示历史任务（Legacy），不推断、不放宽。
//	R-2 route ↔ contract.status：NORMAL 不得携带独立 Contract 状态，
//	    COMPLEX / ARCHITECT 不得以 NOT_REQUIRED 跳过独立审批。
//	R-3 route ↔ task.md `## Complexity`：两者必须一致；只保留模板占位视为未声明。
//	R-4 route ↔ Evidence：
//	    a) Contract Approval 所在 commit 记录的路线必须仍是当前路线（旧路线审批不得复用）；
//	    b) ARCHITECT 的 Review 证据必须是结构快照（architecture.md 位于 review.target），
//	       COMPLEX / NORMAL 不得复用 ARCHITECT 的结构快照。
//
// 上述检查只使用 Engine 能真实读取的事实（state.yaml、task.md、Git commit 内容），
// 不声称认证 Agent 身份或 Session 隔离。

// complexityHeading 是 task.md 中供人阅读的路线章节标题。
const complexityHeading = "Complexity"

// complexityLabelPattern 匹配 Complexity 章节行首可选的「字段名 + 冒号」写法。
var complexityLabelPattern = regexp.MustCompile(`(?i)^(complexity|复杂度|路线)\s*[:：]?\s*`)

// routeTokenPattern 匹配文本中出现的路线关键字。
var routeTokenPattern = regexp.MustCompile(`\b(NORMAL|COMPLEX|ARCHITECT)\b`)

// routeDecorationTrim 是行首常见的 Markdown 装饰字符。
const routeDecorationTrim = "-*+>#`'\"| \t"

// ParseComplexity 从 task.md 的 `## Complexity` 章节解析路线声明。
//
// 解析规则（fail-closed，宁可不认也不猜）：
//   - 只认 `## Complexity` 章节内、以路线关键字开头且全文只出现一个路线关键字的行；
//   - 保留模板占位（如 `NORMAL / COMPLEX / ARCHITECT`）视为「未声明」；
//   - 章节缺失或没有唯一声明时返回 ok=false，调用方应拒绝路线校验。
func ParseComplexity(md string) (string, bool) {
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		level, title, isHeading := markdownHeading(lines[i])
		if !isHeading || level != 2 || !strings.EqualFold(title, complexityHeading) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if lvl, _, next := markdownHeading(lines[j]); next && lvl <= level {
				break
			}
			line := strings.TrimSpace(lines[j])
			if line == "" {
				continue
			}
			if tokens := routeTokensIn(line); len(tokens) == 1 && routePrefixMatch(line, tokens[0]) {
				return tokens[0], true
			}
		}
		// 只认第一个 `## Complexity` 章节：出现多条路线或没有唯一声明都视为未声明。
		return "", false
	}
	return "", false
}

// markdownHeading 解析 ATX 标题，返回级别与标题文本。
func markdownHeading(line string) (int, string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(t) && t[level] == '#' {
		level++
	}
	if level > 6 {
		return 0, "", false
	}
	if level < len(t) && t[level] != ' ' && t[level] != '\t' {
		return 0, "", false
	}
	return level, strings.TrimSpace(t[level:]), true
}

// routeTokensIn 返回一行内出现的去重路线关键字（按 NORMAL / COMPLEX / ARCHITECT 顺序）。
func routeTokensIn(line string) []string {
	matches := routeTokenPattern.FindAllString(strings.ToUpper(line), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, m := range matches {
		seen[m] = struct{}{}
	}
	out := make([]string, 0, 3)
	for _, route := range []Route{RouteNormal, RouteComplex, RouteArchitect} {
		if _, ok := seen[route]; ok {
			out = append(out, route)
		}
	}
	return out
}

// routePrefixMatch 报告一行在去掉 Markdown 装饰与可选字段名后，是否以该路线关键字开头。
//
// 这条规则用于排除「原因：不是 ARCHITECT」这类解释性句子：
// 只有真正以路线关键字开头的行才被当作声明。
func routePrefixMatch(line, route string) bool {
	normalized := strings.TrimLeft(strings.TrimSpace(line), routeDecorationTrim)
	normalized = complexityLabelPattern.ReplaceAllString(normalized, "")
	normalized = strings.TrimLeft(normalized, routeDecorationTrim)
	normalized = strings.ToUpper(normalized)
	if !strings.HasPrefix(normalized, route) {
		return false
	}
	rest := normalized[len(route):]
	if rest == "" {
		return true
	}
	// 关键字后紧跟的必须是分隔/标点，不能是字母数字（避免 NORMALIZE 之类误匹配）。
	c := rest[0]
	return !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_'
}

// routeValid 校验 route 与其余可验证状态事实的一致性（R-2 ~ R-4）。
// route 缺省（Legacy）时不推断路线，直接通过。
func (v *Validator) routeValid(taskSlug string, s State) Result {
	if s.Route == "" {
		return Result{Status: StatusPass}
	}
	if !ValidRoute(s.Route) {
		return failResult(taskSlug, "Route Enum (R-1)", s.Route, "NORMAL | COMPLEX | ARCHITECT", "route 取值非法")
	}
	if res := routeContractConsistent(taskSlug, s); res.Status != StatusPass {
		return res
	}
	if res := v.routeTaskDocAgrees(taskSlug, s.Route); res.Status != StatusPass {
		return res
	}
	if res := v.routeApprovalAgrees(taskSlug, s); res.Status != StatusPass {
		return res
	}
	return v.routeReviewEvidenceAgrees(taskSlug, s)
}

// routeContractConsistent 实现 R-2：路线决定 Contract 是否需要独立审批。
func routeContractConsistent(taskSlug string, s State) Result {
	const check = "Route Contract (R-2)"
	if RouteRequiresContractApproval(s.Route) {
		if s.Contract.Status == ContractNotRequired {
			return failResult(taskSlug, check,
				fmt.Sprintf("route=%s contract.status=%s", s.Route, s.Contract.Status),
				"PENDING | APPROVED",
				fmt.Sprintf("%s 必须有独立 Contract 审批，不得以 NOT_REQUIRED 直接交接", s.Route))
		}
		return Result{Status: StatusPass}
	}
	// NORMAL：目标清楚、边界有限，不需要独立 Contract。
	if s.Contract.Status != ContractNotRequired {
		return failResult(taskSlug, check,
			fmt.Sprintf("route=%s contract.status=%s", s.Route, s.Contract.Status),
			ContractNotRequired,
			"NORMAL 不需要独立 Contract，不得携带待审批或已批准的 Contract 状态")
	}
	return Result{Status: StatusPass}
}

// routeTaskDocAgrees 实现 R-3：state.route 必须与 task.md 的 Complexity 一致。
func (v *Validator) routeTaskDocAgrees(taskSlug, route string) Result {
	const check = "Route Task Document (R-3)"
	raw, err := os.ReadFile(filepath.Join(v.Root, taskDocPath(taskSlug)))
	if err != nil {
		if os.IsNotExist(err) {
			return failResult(taskSlug, check, "task.md 不存在", "声明 `## Complexity` 的 task.md",
				"route 必须能由 task.md 的 Complexity 核对；缺少书面事实时拒绝")
		}
		return Result{Status: StatusError, Error: fmt.Sprintf("read %s: %v", taskDocPath(taskSlug), err)}
	}
	declared, ok := ParseComplexity(string(raw))
	if !ok {
		return failResult(taskSlug, check, "task.md 未声明唯一 Complexity", route,
			"`## Complexity` 必须给出唯一路线，不得保留模板占位或同时列出多条路线")
	}
	if declared != route {
		return failResult(taskSlug, check, "task.md Complexity="+declared, route,
			"state.yaml 的 route 与 task.md 的 Complexity 不一致")
	}
	return Result{Status: StatusPass}
}

// routeApprovalAgrees 实现 R-4a：Contract Approval 必须属于当前路线。
//
// Contract Evidence Commit（A1）里保存着当时的 state.yaml。若该 commit 记录的
// route 与当前 route 不同，说明现有审批属于旧路线，不得复用；路线改变后必须
// 重新形成 A1/A2 审批证据（而不是只改一个字段）。
func (v *Validator) routeApprovalAgrees(taskSlug string, s State) Result {
	const check = "Route Contract Evidence (R-4a)"
	if s.Contract.Status != ContractApproved || s.Contract.Target == "" {
		return Result{Status: StatusPass}
	}
	target, res := v.resolveImmutableCommit(taskSlug, check, "contract.target", s.Contract.Target)
	if res.Status != StatusPass {
		return res
	}
	raw, err := v.Git.ShowFile(target, statePath(taskSlug))
	if err != nil {
		if os.IsNotExist(err) {
			return failResult(taskSlug, check, "contract.target 中无 state.yaml", "记录 route 的 state.yaml",
				"无法核对 Contract Approval 所属路线")
		}
		return Result{Status: StatusError, Error: fmt.Sprintf("show state.yaml@%s: %v", target, err)}
	}
	approvedRoute, ok, err := extractRouteFact([]byte(raw))
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("parse state.yaml@%s: %v", target, err)}
	}
	if !ok {
		return failResult(taskSlug, check, "contract.target 的 state.yaml 未记录 route", s.Route,
			"Contract Approval 未绑定路线；声明路线后必须重新审批 Contract")
	}
	if approvedRoute != s.Route {
		return failResult(taskSlug, check, "contract.target 记录的 route="+approvedRoute, s.Route,
			"现有 Contract Approval 属于旧路线，不得复用；路线改变须重新审批 Contract")
	}
	return Result{Status: StatusPass}
}

// routeReviewEvidenceAgrees 实现 R-4b：Review 证据形态必须与路线匹配。
//
// ARCHITECT 的 D1 必须包含 architecture.md。
//
// 反向不成立：仅仅「树里含 architecture.md」不足以证明该快照是结构交付快照。
// COMPLEX / NORMAL 只在 review.target 自身改写了 architecture.md 时拒绝，
// 见函数体内的说明。
func (v *Validator) routeReviewEvidenceAgrees(taskSlug string, s State) Result {
	const check = "Route Review Evidence (R-4b)"
	if s.Review.Target == "" {
		return Result{Status: StatusPass}
	}
	archPath := architecturePath(taskSlug)
	exists, err := v.Git.PathExistsAt(s.Review.Target, archPath)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("cat-file %s:%s: %v", s.Review.Target, archPath, err)}
	}
	if s.Route == RouteArchitect {
		if !exists {
			return failResult(taskSlug, check,
				fmt.Sprintf("review.target=%s 缺少 %s", s.Review.Target, archPath),
				"包含 architecture.md 的结构快照",
				"ARCHITECT 的 Review 证据必须是结构交付快照")
		}
		return Result{Status: StatusPass}
	}
	if !exists {
		return Result{Status: StatusPass}
	}
	// architecture.md 存在于快照的树里，不证明该快照是结构交付快照：它可能来自更早的
	// ARCHITECT 历史，之后 Owner 明确把路线改为 COMPLEX，业务实现仍要继续
	// （`.agent/tasks/<task>/architecture.md` 不会被自动删除）。因此只拒绝
	// 「该快照本身改写/引入 architecture.md」的结构交付提交，不拒绝仅仅继承历史
	// 结构文件的新实现快照：既不误拒绝合法路线变更，也不允许把 D1 直接复用为
	// COMPLEX 的 Review 证据。
	touched, err := v.Git.CommitTouchesPath(s.Review.Target, archPath)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("diff-tree %s -- %s: %v", s.Review.Target, archPath, err)}
	}
	if touched {
		return failResult(taskSlug, check,
			fmt.Sprintf("review.target=%s 自身改写了 %s", s.Review.Target, archPath),
			"不改写 architecture.md 的实现快照",
			fmt.Sprintf("该快照是 ARCHITECT 结构交付证据，不得作为 %s 的 Review 证据", s.Route))
	}
	return Result{Status: StatusPass}
}

// RouteReport 是路线事实自检（非 Gate）的结果。
type RouteReport struct {
	// Route 是该任务记录的路线；空串表示未记录（Legacy）。
	Route string
	// Legacy 表示该任务没有 route 事实。
	Legacy bool
	// Result 是校验结论。
	Result Result
}

// CheckRoute 对单个 task 执行路线事实自检（R-1 ~ R-4），供 TaskBuilder 出站自检使用。
//
// 它不是 Gate：不产生 Handoff 授权，也不改变任何 Gate 语义；NORMAL 的 Handoff
// 仍需 `gate coder-start` PASS，COMPLEX / ARCHITECT 的下一角色入口 Gate 仍由
// Analyst 在其出口运行。
//
// requireRoute=true 时缺少 route 即 FAIL：新任务必须记录路线事实，Engine 无法把
// 「新任务遗漏 route」与「历史任务无 route」区分开，因此历史任务需要显式
// requireRoute=false（CLI 的 --allow-legacy）才能通过。
func (v *Validator) CheckRoute(taskDir string, requireRoute bool) RouteReport {
	taskSlug := filepath.Base(filepath.Clean(taskDir))
	s, res := v.loadStateFile(taskDir)
	if res.Status != StatusPass {
		return RouteReport{Result: res}
	}
	if issues := schemaValidation(s); len(issues) > 0 {
		return RouteReport{Route: s.Route, Result: Result{Status: StatusFail, Issues: issues}}
	}
	if s.Route == "" {
		if requireRoute {
			return RouteReport{Legacy: true, Result: failResult(taskSlug, "Route Authority (R-1)",
				"state.yaml 未记录 route", "NORMAL | COMPLEX | ARCHITECT",
				"新任务必须记录路线事实；历史任务只能在显式允许 legacy 时跳过路线校验")}
		}
		return RouteReport{Legacy: true, Result: Result{Status: StatusPass}}
	}
	if res := v.routeValid(taskSlug, s); res.Status != StatusPass {
		return RouteReport{Route: s.Route, Result: res}
	}
	return RouteReport{Route: s.Route, Result: Result{Status: StatusPass}}
}
