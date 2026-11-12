package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// crossPkgCode 是「同包调用图覆盖不到、经人工核实后补充登记」的跨包错误码。
//
// 背景（R5）：analyzeLogic 只解析 internal/logic/<domain> 包内的函数体，
// controller→service→logic 的链路中，logic 里再调 service.<Other>() 的跨域调用不在覆盖范围内。
// 这些跨包调用会返回对方业务域的错误码（例如下单扣库存返回 6001），如果契约不声明，
// 前端只能靠线上错误码反向猜测。
//
// 登记规则（由 validateCrossPackageRegistry 强制）：
//  1. API 必须真实存在；
//  2. 错误码常量必须真实存在于 internal/codes/codes.go，且不是成功码；
//  3. Service/Method 必须能定位到 internal/logic/<domain> 的实现函数，且该函数确实引用该常量；
//  4. 该 API 的调用链中必须真的出现 service.<Service>().<Method>() 调用；
//  5. 不能登记已被同包分析覆盖的码（避免白名单变成「随手加」）。
type crossPkgCode struct {
	Service string // service.<Service>() 访问器名
	Method  string // <Service>().<Method>() 方法名
	Code    string // codes.CodeXxx 常量名
	Why     string // 触发条件（人工核实）
}

// crossPackageErrorCodes 以 "METHOD /path" 为键登记跨包补充错误码。
// 只登记经过代码核实、且调用链中错误确实向外传播（未被吞掉/降级）的码。
var crossPackageErrorCodes = map[string][]crossPkgCode{
	"POST /orders": {
		{Service: "Address", Method: "Detail", Code: "CodeAddressNotFound",
			Why: "收货地址不存在或不属于当前用户；order.Create 对 error 直接 return"},
		{Service: "Coupon", Method: "GetDiscount", Code: "CodeCouponNotFound",
			Why: "指定 coupon_id 时券不存在/非本人；order.Create 直接返回该 error"},
		{Service: "Coupon", Method: "GetDiscount", Code: "CodeCouponUnavailable",
			Why: "券不在使用期/未达门槛；事务内 LockInTx 预占失败（并发被占）同样返回 19007"},
		{Service: "Inventory", Method: "DeductInTx", Code: "CodeInventoryInsufficient",
			Why: "扣减库存条件更新 0 行（库存不足），整个下单事务回滚"},
	},
	"POST /admin/refunds/:id/execute": {
		{Service: "Order", Method: "CompleteRefundInTx", Code: "CodeOrderInvalidStatusTransition",
			Why: "订单非 paid(20)（0 行更新，如已发货），退款执行整体回滚"},
	},
}

// supplementalErrorCodes 返回该 operation 的跨包补充错误码常量名。
func supplementalErrorCodes(apiKey string) []string {
	entries, ok := crossPackageErrorCodes[apiKey]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}

// crossPackageEvidence 是校验补充登记表所需的代码事实。
type crossPackageEvidence struct {
	// svcToLogic：service 访问器名 -> internal/logic 下的包名（如 Inventory -> inventory）。
	svcToLogic map[string]string
	// logicCalls：domain -> 函数名 -> 该函数体中的 service.<X>().<M>() 调用。
	logicCalls map[string]map[string][]serviceCall
}

type serviceCall struct {
	Service string
	Method  string
}

// loadCrossPackageEvidence 解析 internal/logic，提取跨包调用事实。
func loadCrossPackageEvidence(repoRoot string) (*crossPackageEvidence, error) {
	ev := &crossPackageEvidence{
		svcToLogic: map[string]string{},
		logicCalls: map[string]map[string][]serviceCall{},
	}
	dirs, err := filepath.Glob(filepath.Join(repoRoot, "internal", "logic", "*"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		info, statErr := statDir(dir)
		if statErr != nil || !info {
			continue
		}
		domain := filepath.Base(dir)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return nil, err
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			svcAliases := map[string]bool{}
			for _, spec := range f.Imports {
				p := strings.Trim(spec.Path.Value, `"`)
				if strings.HasSuffix(p, "/internal/service") {
					alias := "service"
					if spec.Name != nil {
						alias = spec.Name.Name
					}
					svcAliases[alias] = true
				}
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if ev.logicCalls[domain] == nil {
					ev.logicCalls[domain] = map[string][]serviceCall{}
				}
				// service.Register<X>(...) 记录访问器名到包名的映射。
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if id, ok := sel.X.(*ast.Ident); ok && svcAliases[id.Name] &&
						strings.HasPrefix(sel.Sel.Name, "Register") {
						ev.svcToLogic[strings.TrimPrefix(sel.Sel.Name, "Register")] = domain
					}
					// service.<Service>().<Method>() 跨包调用。
					inner, ok := sel.X.(*ast.CallExpr)
					if !ok {
						return true
					}
					innerSel, ok := inner.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := innerSel.X.(*ast.Ident)
					if !ok || !svcAliases[pkg.Name] {
						return true
					}
					sc := serviceCall{Service: innerSel.Sel.Name, Method: sel.Sel.Name}
					for _, exist := range ev.logicCalls[domain][fn.Name.Name] {
						if exist == sc {
							return true
						}
					}
					ev.logicCalls[domain][fn.Name.Name] = append(ev.logicCalls[domain][fn.Name.Name], sc)
					return true
				})
			}
		}
	}
	return ev, nil
}

// validateCrossPackageRegistry 校验补充登记表与当前代码事实一致。
func validateCrossPackageRegistry(repoRoot string, cms map[string]controllerMethod, apis []*API) []string {
	var problems []string
	if len(crossPackageErrorCodes) == 0 {
		return nil
	}
	codeList, err := parseErrorCodes(repoRoot)
	if err != nil {
		return []string{fmt.Sprintf("校验跨包错误码登记失败: %v", err)}
	}
	codesByName := map[string]*ErrorCode{}
	for _, c := range codeList {
		codesByName[c.Constant] = c
	}
	graph, err := analyzeLogic(repoRoot)
	if err != nil {
		return []string{fmt.Sprintf("校验跨包错误码登记失败: %v", err)}
	}
	ev, err := loadCrossPackageEvidence(repoRoot)
	if err != nil {
		return []string{fmt.Sprintf("校验跨包错误码登记失败: %v", err)}
	}

	apiByKey := map[string]*API{}
	for _, a := range apis {
		apiByKey[a.Key()] = a
	}

	seen := map[string]bool{}
	keys := make([]string, 0, len(crossPackageErrorCodes))
	for k := range crossPackageErrorCodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		api, ok := apiByKey[key]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"跨包错误码登记了不存在的接口 %s（路由已删除或路径写错）", key))
			continue
		}
		cm := cms[api.Domain+"."+api.Handler]
		reachable := graph.reachableFuncs(api.Domain, cm.ServiceCalls)
		covered := map[string]bool{}
		for name := range graph.constantsFor(api.Domain, cm.ServiceCalls) {
			covered[name] = true
		}
		for _, name := range cm.Codes {
			covered[name] = true
		}

		for _, e := range crossPackageErrorCodes[key] {
			uid := key + "|" + e.Code
			if seen[uid] {
				problems = append(problems, fmt.Sprintf("%s 重复登记错误码 %s", key, e.Code))
				continue
			}
			seen[uid] = true

			c, ok := codesByName[e.Code]
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"%s 登记的错误码 %s 在 internal/codes/codes.go 中不存在（禁止虚构）", key, e.Code))
				continue
			}
			if c.Code == 0 {
				problems = append(problems, fmt.Sprintf("%s 登记了成功码 %s", key, e.Code))
				continue
			}
			if covered[e.Code] {
				problems = append(problems, fmt.Sprintf(
					"%s 登记的错误码 %s 已被同包调用图覆盖，无需补充登记", key, e.Code))
			}
			target, ok := ev.svcToLogic[e.Service]
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"%s 登记的 service.%s() 找不到对应 internal/logic 包", key, e.Service))
				continue
			}
			if !graph.funcCodes[target][e.Method][e.Code] {
				problems = append(problems, fmt.Sprintf(
					"%s 登记 %s.%s 引用 %s，但 internal/logic/%s 中该方法并未引用该常量",
					key, e.Service, e.Method, e.Code, target))
			}
			if !callReachable(ev, api.Domain, reachable, e.Service, e.Method) {
				problems = append(problems, fmt.Sprintf(
					"%s 的调用链中不存在 service.%s().%s() 调用（登记已过期）",
					key, e.Service, e.Method))
			}
			if strings.TrimSpace(e.Why) == "" {
				problems = append(problems, fmt.Sprintf("%s 登记的 %s 缺少触发条件说明", key, e.Code))
			}
		}
	}
	return problems
}

// callReachable 判断跨包调用是否出现在「从 operation 入口可达的同包函数」中。
func callReachable(ev *crossPackageEvidence, domain string, reachable map[string]bool, service, method string) bool {
	for fn := range reachable {
		for _, sc := range ev.logicCalls[domain][fn] {
			if sc.Service == service && sc.Method == method {
				return true
			}
		}
	}
	return false
}
