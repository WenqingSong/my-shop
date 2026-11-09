package fakestate

import (
	"path/filepath"
	"testing"
)

// 本文件锁定 Fake Kubernetes 的**授权语义**（T2-06.1.5 / 审计 F-01）。
//
// 为什么需要独立的一层：deployk8s 的行为测试只能证明「脚本按什么对象名发起了
// 预检」，不能证明「替身对 resourceNames 的判定与真实 RBAC 一致」。替身一旦在
// 这里放水（例如对空对象名放行），脚本的预检写错也会全绿 —— 这正是 F-01 的形态。
//
// 对照的真实实现（Kubernetes 1.29）：
//
//	plugin/pkg/auth/authorizer/rbac/rbac.go:
//	  ResourceNameMatches(rule, name) = len(rule.ResourceNames)==0 || has(rule.ResourceNames, name)
//	kubectl/pkg/cmd/auth/cani.go:
//	  SplitN(args[1], "/", 2) → TYPE/NAME，NAME 是**对象名**；子资源用 --subresource
const testLockName = "surgecart-deploy-lock"

// deliveredLeaseRules 与交付清单里 leases 的两条规则同形：
//   - get/update/patch 用 resourceNames 限定到锁对象；
//   - create 单独一条（对象尚不存在，不能带 resourceNames）。
func deliveredLeaseRules() []Rule {
	return []Rule{
		{
			APIGroups:     []string{"coordination.k8s.io"},
			Resources:     []string{"leases"},
			Verbs:         []string{"get", "update", "patch"},
			ResourceNames: []string{testLockName},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs:     []string{"create"},
		},
	}
}

func stateWithRules(rules []Rule) *State {
	return &State{Permissions: Permissions{Rules: rules}}
}

// TestResourceNamesObjectLevelSemantics 覆盖 T3–T7：
//
//	正确名称的 get/update/patch → 允许
//	错误名称                    → 拒绝
//	无对象名                    → 拒绝（真实 RBAC 的 ResourceNameMatches 语义）
func TestResourceNamesObjectLevelSemantics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		verb   string
		object string
		want   Decision
	}{
		{name: "T3 get 正确名称", verb: "get", object: testLockName, want: DecisionAllowed},
		{name: "T4 update 正确名称", verb: "update", object: testLockName, want: DecisionAllowed},
		{name: "T5 patch 正确名称", verb: "patch", object: testLockName, want: DecisionAllowed},
		{name: "T6 get 错误名称", verb: "get", object: "another-pipeline-lock", want: DecisionForbidden},
		{name: "T6 update 错误名称", verb: "update", object: "another-pipeline-lock", want: DecisionForbidden},
		{name: "T6 patch 错误名称", verb: "patch", object: "another-pipeline-lock", want: DecisionForbidden},
		{name: "T7 get 无对象名", verb: "get", object: "", want: DecisionForbidden},
		{name: "T7 update 无对象名", verb: "update", object: "", want: DecisionForbidden},
		{name: "T7 patch 无对象名", verb: "patch", object: "", want: DecisionForbidden},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := stateWithRules(deliveredLeaseRules())
			decision, err := Authorize(state, tc.verb, "coordination.k8s.io", "leases", "surgecart", tc.object)
			if err != nil {
				t.Fatalf("判定返回错误: %v", err)
			}
			if decision != tc.want {
				t.Fatalf("Authorize(%s leases name=%q) = %s，期望 %s", tc.verb, tc.object, decision, tc.want)
			}
		})
	}
}

// TestResourceNamesRuleDoesNotAuthorizeUnnamedRequests 直接锁定 F-01 的根因：
// 「无对象名的检查」绝不能因为规则覆盖了 (verb, resource) 就被放行。
func TestResourceNamesRuleDoesNotAuthorizeUnnamedRequests(t *testing.T) {
	t.Parallel()
	state := stateWithRules(deliveredLeaseRules())
	// 关键回归：这行在旧实现里返回 allowed（ruleMatches 对 name=="" 放行）。
	decision, _ := Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", "")
	if decision != DecisionForbidden {
		t.Fatalf("无对象名的 get leases 必须被 resourceNames 规则拒绝（真实 RBAC 的 ResourceNameMatches 语义），实际 %s", decision)
	}
	// 但同一份规则下，正确的对象名必须放行（否则预检无论如何都过不去）。
	decision, _ = Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", testLockName)
	if decision != DecisionAllowed {
		t.Fatalf("正确对象名的 get leases 必须允许，实际 %s", decision)
	}
}

// TestCreateIsNotNameScoped：create 的授权属性没有对象名（名字在请求体里）。
// 因此：
//   - 交付清单里 create 规则不带 resourceNames，create（name=""）允许；
//   - 若有人给 create 加了 resourceNames，真实请求仍然匹配不到它 —— 替身不能
//     「帮忙」放行，否则会把一条必失败的最小权限配置伪装成可用。
func TestCreateIsNotNameScoped(t *testing.T) {
	t.Parallel()
	state := stateWithRules(deliveredLeaseRules())
	decision, _ := Authorize(state, "create", "coordination.k8s.io", "leases", "surgecart", "")
	if decision != DecisionAllowed {
		t.Fatalf("未加 resourceNames 的 create 规则必须允许 create，实际 %s", decision)
	}

	broken := stateWithRules([]Rule{{
		APIGroups:     []string{"coordination.k8s.io"},
		Resources:     []string{"leases"},
		Verbs:         []string{"create"},
		ResourceNames: []string{testLockName},
	}})
	decision, _ = Authorize(broken, "create", "coordination.k8s.io", "leases", "surgecart", "")
	if decision != DecisionForbidden {
		t.Fatalf("带 resourceNames 的 create 规则不能放行无对象名的 create（真实集群上永远匹配不到），实际 %s", decision)
	}
}

// TestListIsNotSatisfiedByResourceNamesRule：list 请求没有对象名。
// 显式加一条 resourceNames + list 的规则（Kubernetes 允许这样写，但它永远匹配不到
// 真实 list 请求）时，替身必须同样判定为拒绝。
func TestListIsNotSatisfiedByResourceNamesRule(t *testing.T) {
	t.Parallel()
	state := stateWithRules([]Rule{{
		APIGroups:     []string{"coordination.k8s.io"},
		Resources:     []string{"leases"},
		Verbs:         []string{"list"},
		ResourceNames: []string{testLockName},
	}})
	decision, _ := Authorize(state, "list", "coordination.k8s.io", "leases", "surgecart", "")
	if decision != DecisionForbidden {
		t.Fatalf("list 没有对象名，不能被 resourceNames 规则放行，实际 %s", decision)
	}
}

// TestResourceNamesHasNoWildcard：RBAC 的 resourceNames 是逐字比较，没有通配符。
// "*" 只是一个普通的对象名 —— 旧实现把它当通配符，放宽了替身语义。
func TestResourceNamesHasNoWildcard(t *testing.T) {
	t.Parallel()
	state := stateWithRules([]Rule{{
		APIGroups:     []string{"coordination.k8s.io"},
		Resources:     []string{"leases"},
		Verbs:         []string{"get"},
		ResourceNames: []string{"*"},
	}})
	for _, name := range []string{"surgecart-deploy-lock", "anything-else"} {
		decision, _ := Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", name)
		if decision != DecisionForbidden {
			t.Fatalf("resourceNames 里的 %q 不是通配符：name=%q 必须拒绝，实际 %s", "*", name, decision)
		}
	}
}

// TestUnrestrictedRulesKeepNormalSemantics 覆盖 T10：不带 resourceNames 的规则
// 仍按正常 RBAC 语义匹配（任意对象名、无对象名都允许；动词/资源/组仍然受限）。
func TestUnrestrictedRulesKeepNormalSemantics(t *testing.T) {
	t.Parallel()
	state := stateWithRules([]Rule{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{"batch"}, Resources: []string{"jobs"}, Verbs: []string{"get"}},
	})
	for _, tc := range []struct {
		verb, group, resource, name string
		want                        Decision
	}{
		{"get", "", "pods", "", DecisionAllowed},
		{"get", "", "pods", "surgecart-abc", DecisionAllowed},
		{"list", "", "pods", "", DecisionAllowed},
		{"get", "batch", "jobs", "surgecart-migrate-abc", DecisionAllowed},
		{"create", "", "pods", "", DecisionForbidden},     // 动词未被授予
		{"get", "batch", "leases", "", DecisionForbidden}, // 资源/组不匹配
		{"delete", "", "pods", "surgecart-abc", DecisionForbidden},
	} {
		decision, _ := Authorize(state, tc.verb, tc.group, tc.resource, "surgecart", tc.name)
		if decision != tc.want {
			t.Fatalf("Authorize(%s %s.%s name=%q) = %s，期望 %s", tc.verb, tc.group, tc.resource, tc.name, decision, tc.want)
		}
	}
}

// TestRoleRulesDoNotGrantOtherNamespaces：Role 级规则（Namespaces 非空）只在
// 自己的命名空间生效 —— 预检的 -n 必须与规则一致，否则「预检通过、写入 403」。
func TestRoleRulesDoNotGrantOtherNamespaces(t *testing.T) {
	t.Parallel()
	rules := deliveredLeaseRules()
	for i := range rules {
		rules[i].Namespaces = []string{"surgecart"}
	}
	state := stateWithRules(rules)
	decision, _ := Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", testLockName)
	if decision != DecisionAllowed {
		t.Fatalf("同命名空间的具名 get 必须允许，实际 %s", decision)
	}
	decision, _ = Authorize(state, "get", "coordination.k8s.io", "leases", "other", testLockName)
	if decision != DecisionForbidden {
		t.Fatalf("跨命名空间的具名 get 必须拒绝，实际 %s", decision)
	}
}

// TestParseResourceArgFollowsKubectl：`auth can-i` 的 TYPE/NAME 解析必须与
// kubectl 一致（"/" 之后是对象名；子资源只能由 --subresource 表达）。
func TestParseResourceArgFollowsKubectl(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		arg  string
		grp  string
		res  string
		name string
	}{
		{"", "", "", ""},
		{"pods", "", "pods", ""},
		{"pods/log", "", "pods", "log"},
		{"pods/nginx-abc", "", "pods", "nginx-abc"},
		{"leases.coordination.k8s.io", "coordination.k8s.io", "leases", ""},
		{"leases.coordination.k8s.io/surgecart-deploy-lock", "coordination.k8s.io", "leases", "surgecart-deploy-lock"},
		{"jobs.batch/bar", "batch", "jobs", "bar"},
		{"selfsubjectaccessreviews.authorization.k8s.io", "authorization.k8s.io", "selfsubjectaccessreviews", ""},
	} {
		grp, res, name := ParseResourceArg(tc.arg)
		if grp != tc.grp || res != tc.res || name != tc.name {
			t.Fatalf("ParseResourceArg(%q) = (%q,%q,%q)，期望 (%q,%q,%q)",
				tc.arg, grp, res, name, tc.grp, tc.res, tc.name)
		}
	}
}

// TestDeliveredRBACLeaseSemantics 把**交付的** RBAC 清单喂给替身，验证对象级语义。
// 这条链路让「删掉/改错 resourceNames」直接表现为授权失败，而不是默默变宽。
func TestDeliveredRBACLeaseSemantics(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
	rules, err := LoadRBACRules(path)
	if err != nil {
		t.Fatalf("读取交付的 RBAC 清单: %v", err)
	}
	state := stateWithRules(rules)

	for _, tc := range []struct {
		name   string
		verb   string
		object string
		want   Decision
	}{
		{"交付规则：get 正确名称", "get", testLockName, DecisionAllowed},
		{"交付规则：update 正确名称", "update", testLockName, DecisionAllowed},
		{"交付规则：patch 正确名称", "patch", testLockName, DecisionAllowed},
		{"交付规则：get 无对象名", "get", "", DecisionForbidden},
		{"交付规则：update 无对象名", "update", "", DecisionForbidden},
		{"交付规则：get 错误名称", "get", "another-pipeline-lock", DecisionForbidden},
		{"交付规则：create 无对象名", "create", "", DecisionAllowed},
	} {
		decision, _ := Authorize(state, tc.verb, "coordination.k8s.io", "leases", "surgecart", tc.object)
		if decision != tc.want {
			t.Fatalf("%s：Authorize(%s leases name=%q) = %s，期望 %s", tc.name, tc.verb, tc.object, decision, tc.want)
		}
	}

	// pods/log 必须显式授权（get pods 的规则不覆盖子资源）——脚本用
	// `auth can-i get pods --subresource=log` 检查，替身把子资源合并成 "pods/log"。
	decision, _ := Authorize(state, "get", "", "pods/log", "surgecart", "")
	if decision != DecisionAllowed {
		t.Fatalf("交付规则必须允许 get pods/log（诊断能力），实际 %s", decision)
	}
	decision, _ = Authorize(state, "delete", "coordination.k8s.io", "leases", "surgecart", testLockName)
	if decision != DecisionForbidden {
		t.Fatalf("交付规则不授予 delete，实际 %s", decision)
	}
}
