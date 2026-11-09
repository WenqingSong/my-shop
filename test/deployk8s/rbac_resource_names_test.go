package deployk8s

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件是 T2-06.1.5 / F-01 的**语义层**回归：
//
//	Fake 的授权判定必须与真实 Kubernetes RBAC 的 resourceNames 语义一致，
//	并且交付的预检必须按具体对象名检查受限制的权限。
//
// 背景（真实 RBAC 的 ResourceNameMatches）：
//
//	if len(rule.ResourceNames) == 0 { return true }
//	return has(rule.ResourceNames, requestedName)
//
// 由此得到四条必须成立的性质：
//  1. 带正确对象名的 get/update/patch 允许；
//  2. 错误对象名拒绝；
//  3. **没有对象名**的检查拒绝（`auth can-i get leases` 在真实集群上是 no）；
//  4. create/list/watch/deletecollection 不匹配 resourceNames 规则（对象名不可知）。
//
// 旧 Fake 对「空对象名」特殊放行（`if name != "" { ... }`），使性质 3 不成立，
// 于是交付的 RBAC 在 Fake 上「可用」、在真实集群上不可用 —— 这正是 F-01。

// deliveredLeaseRules 断言交付清单里 leases 规则的形状，并返回它们。
//
// 这不是「复制一份期望值」：规则本身从交付 YAML 解析，测试只断言
// 「get/update 被 resourceNames 限定到 LOCK_NAME、create 不被限定」这一契约。
func deliveredLeaseRules(t *testing.T) []fakestate.Rule {
	t.Helper()
	rules, err := fakestate.LoadRBACRules(filepath.Join(repoRootPath, "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml"))
	if err != nil {
		t.Fatalf("解析交付 RBAC: %v", err)
	}
	var leaseRules []fakestate.Rule
	for _, rule := range rules {
		if contains(rule.Resources, "leases") {
			leaseRules = append(leaseRules, rule)
		}
	}
	if len(leaseRules) == 0 {
		t.Fatal("交付的 RBAC 里没有 leases 规则：本文件的前提不成立")
	}
	scoped := 0
	for _, rule := range leaseRules {
		if len(rule.ResourceNames) == 0 {
			// create 必须由**不受 resourceNames 限制**的规则授予。
			if !contains(rule.Verbs, "create") {
				t.Fatalf("不受 resourceNames 限制的 leases 规则只应授予 create，实际 verbs=%v", rule.Verbs)
			}
			continue
		}
		scoped++
		if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != knownLeaseName {
			t.Fatalf("leases 的 resourceNames 必须精确等于 %q，实际 %v", knownLeaseName, rule.ResourceNames)
		}
		for _, verb := range rule.Verbs {
			if !fakestate.VerbSupportsResourceName(verb) {
				t.Fatalf("leases 的 resourceNames 规则包含 %q：该动词无法按对象名授权", verb)
			}
		}
	}
	if scoped != 1 {
		t.Fatalf("交付清单里应有 1 条受 resourceNames 限制的 leases 规则，实际 %d 条", scoped)
	}
	return leaseRules
}

// TestDeliveredRBACLeaseAuthorizeMatrix 是 T3–T7、T10 的判定矩阵：
// 直接用交付清单驱动 fakestate.Authorize，逐格断言。
func TestDeliveredRBACLeaseAuthorizeMatrix(t *testing.T) {
	t.Parallel()
	rules := deliveredLeaseRules(t)
	state := &fakestate.State{Permissions: fakestate.Permissions{Rules: rules}}

	check := func(verb, namespace, name string, want fakestate.Decision) {
		t.Helper()
		got, err := fakestate.Authorize(state, verb, "coordination.k8s.io", "leases", namespace, name)
		if err != nil {
			t.Fatalf("Authorize(%s leases/%q) 返回错误: %v", verb, name, err)
		}
		if got != want {
			t.Fatalf("Authorize(%s leases ns=%q name=%q) = %s，期望 %s", verb, namespace, name, got, want)
		}
	}

	// T3 / T4 / T5：正确对象名 → 允许。
	check("get", "surgecart", knownLeaseName, fakestate.DecisionAllowed)
	check("update", "surgecart", knownLeaseName, fakestate.DecisionAllowed)
	check("patch", "surgecart", knownLeaseName, fakestate.DecisionAllowed)

	// T6：错误对象名 → 拒绝。
	check("get", "surgecart", "another-pipeline-lock", fakestate.DecisionForbidden)
	check("update", "surgecart", "another-pipeline-lock", fakestate.DecisionForbidden)
	check("patch", "surgecart", "another-pipeline-lock", fakestate.DecisionForbidden)

	// T7：无对象名 → 拒绝（旧 Fake 会放行，F-01 的根因）。
	check("get", "surgecart", "", fakestate.DecisionForbidden)
	check("update", "surgecart", "", fakestate.DecisionForbidden)
	check("patch", "surgecart", "", fakestate.DecisionForbidden)

	// create 由不受 resourceNames 限制的规则授予：带不带名字都允许
	// （真实 RBAC 里 create 的对象名不参与 resourceNames 匹配）。
	check("create", "surgecart", "", fakestate.DecisionAllowed)
	check("create", "surgecart", knownLeaseName, fakestate.DecisionAllowed)

	// list 未被授予（交付清单里没有 list leases）：默认拒绝。
	check("list", "surgecart", "", fakestate.DecisionForbidden)

	// 命名空间仍然参与判定（Role 的 metadata.namespace）。
	check("get", "default", knownLeaseName, fakestate.DecisionForbidden)
}

// TestResourceNamesSemanticsForEveryVerb 覆盖 Fake 对 resourceNames 的完整语义
// （T6 / T7 / T10 + create/list 的特殊处理），并使用一份**人工构造**的规则，
// 避免结论依赖交付清单的具体形状。
func TestResourceNamesSemanticsForEveryVerb(t *testing.T) {
	t.Parallel()
	state := &fakestate.State{Permissions: fakestate.Permissions{Rules: []fakestate.Rule{
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			// 故意把 list/create 也放进受 resourceNames 限制的规则：
			// 真实 RBAC 不会因此授予它们（对象名不可知）。
			Verbs:         []string{"get", "list", "create", "update", "patch", "delete", "deletecollection"},
			ResourceNames: []string{"named-lock"},
			Namespaces:    []string{"surgecart"},
		},
		{
			// T10：不受 resourceNames 限制的规则仍按正常语义匹配（有名字/无名字都允许）。
			APIGroups:  []string{""},
			Resources:  []string{"pods"},
			Verbs:      []string{"get", "list"},
			Namespaces: []string{"surgecart"},
		},
	}}}

	check := func(verb, group, resource, namespace, name string, want fakestate.Decision) {
		t.Helper()
		got, err := fakestate.Authorize(state, verb, group, resource, namespace, name)
		if err != nil {
			t.Fatalf("Authorize(%s %s/%s) 返回错误: %v", verb, group, resource, err)
		}
		if got != want {
			t.Fatalf("Authorize(%s %s/%s ns=%q name=%q) = %s，期望 %s",
				verb, group, resource, namespace, name, got, want)
		}
	}

	coordination := "coordination.k8s.io"
	// 能带对象名的动词：精确匹配才允许。
	for _, verb := range []string{"get", "update", "patch", "delete"} {
		check(verb, coordination, "leases", "surgecart", "named-lock", fakestate.DecisionAllowed)
		check(verb, coordination, "leases", "surgecart", "other-lock", fakestate.DecisionForbidden)
		check(verb, coordination, "leases", "surgecart", "", fakestate.DecisionForbidden)
	}
	// 集合请求：即使规则写了这些动词，resourceNames 也无法授予它们。
	for _, verb := range []string{"list", "create", "deletecollection", "watch"} {
		check(verb, coordination, "leases", "surgecart", "", fakestate.DecisionForbidden)
		check(verb, coordination, "leases", "surgecart", "named-lock", fakestate.DecisionForbidden)
	}
	// T10：不受 resourceNames 限制的规则保持正常行为。
	check("get", "", "pods", "surgecart", "", fakestate.DecisionAllowed)
	check("get", "", "pods", "surgecart", "any-pod", fakestate.DecisionAllowed)
	check("list", "", "pods", "surgecart", "", fakestate.DecisionAllowed)
	check("get", "", "pods", "default", "", fakestate.DecisionForbidden)
}

// TestResourceNamesAreExactMatchNotWildcard 覆盖「resourceNames 不支持通配」：
// "*" 只能出现在 apiGroups/resources/verbs 里；在 resourceNames 里它是字面量。
func TestResourceNamesAreExactMatchNotWildcard(t *testing.T) {
	t.Parallel()
	state := &fakestate.State{Permissions: fakestate.Permissions{Rules: []fakestate.Rule{{
		APIGroups:     []string{"coordination.k8s.io"},
		Resources:     []string{"leases"},
		Verbs:         []string{"get"},
		ResourceNames: []string{"*"},
	}}}}

	for _, name := range []string{"anything", "surgecart-deploy-lock", ""} {
		got, err := fakestate.Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", name)
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		if got != fakestate.DecisionForbidden {
			t.Fatalf("resourceNames 里的 \"*\" 必须按字面量处理（name=%q），实际 %s", name, got)
		}
	}
	// 字面量 "*" 本身仍然可以精确匹配。
	got, _ := fakestate.Authorize(state, "get", "coordination.k8s.io", "leases", "surgecart", "*")
	if got != fakestate.DecisionAllowed {
		t.Fatalf("resourceNames=[\"*\"] 只应匹配字面量 \"*\"，实际 %s", got)
	}
}

// TestLeasePreflightIsObjectScopedAndPrecedesWrites（T3/T4/T5/T9）在**脚本层**
// 断言两件事：
//  1. 所有 can-i 调用都发生在第一次资源写之前（预检前移，不能带着未知权限写）；
//  2. Lease 的 get/update 检查使用 TYPE/NAME 形式，且没有无对象名的形式。
func TestLeasePreflightIsObjectScopedAndPrecedesWrites(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("预检顺序"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")
	res.requireSuccess(t)

	calls := f.calls()
	lastAuth, firstWrite := -1, len(calls)
	for i, call := range calls {
		if strings.Contains(call, " auth can-i ") {
			lastAuth = i
			continue
		}
		// 资源写的判据与 requireNoResourceMutations 一致：子命令 + 参数。
		for _, verb := range []string{" create ", " replace ", " apply ", " delete "} {
			if strings.Contains(" "+call+" ", verb) && firstWrite == len(calls) {
				firstWrite = i
			}
		}
	}
	if lastAuth < 0 {
		t.Fatal("没有观察到任何 auth can-i 调用：预检没有执行")
	}
	if firstWrite < len(calls) && lastAuth > firstWrite {
		t.Fatalf("权限预检必须早于第一次资源写：最后一次 can-i 在 #%d，第一次写在 #%d\n调用序列:\n%s",
			lastAuth, firstWrite, strings.Join(calls, "\n"))
	}

	// 对象级检查：get/update 各一次，必须带 LOCK_NAME。
	getCalls := 0
	updateCalls := 0
	for _, call := range authCalls(f) {
		switch {
		case strings.Contains(call, "auth can-i get leases.coordination.k8s.io"):
			getCalls++
			if !strings.Contains(call, "leases.coordination.k8s.io/"+knownLeaseName) {
				t.Fatalf("get leases 的预检必须带对象名 %q，实际: %s", knownLeaseName, call)
			}
		case strings.Contains(call, "auth can-i update leases.coordination.k8s.io"):
			updateCalls++
			if !strings.Contains(call, "leases.coordination.k8s.io/"+knownLeaseName) {
				t.Fatalf("update leases 的预检必须带对象名 %q，实际: %s", knownLeaseName, call)
			}
		}
	}
	if getCalls != 1 || updateCalls != 1 {
		t.Fatalf("leases 的 get/update 预检应各执行 1 次，实际 get=%d update=%d\n调用序列:\n%s",
			getCalls, updateCalls, strings.Join(authCalls(f), "\n"))
	}
}

// TestPermissionTableSelfCheckRejectsNamedCreate 覆盖脚本**自身**的权限表自检
// （阶段 0）：给 create 指定对象名是无效语义（resourceNames 对 create 无效），
// 必须在发出任何 can-i 之前 fail-closed（退出码 3、零资源写）。
//
// 这条用例改的是 fixture 里的脚本副本，而不是真实文件 —— fixture 会把脚本复制到
// 临时目录后执行，因此可以模拟「有人错误地给 create 加对象名」。
func TestPermissionTableSelfCheckRejectsNamedCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	path := filepath.Join(f.root, "scripts", "deploy-k8s.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 fixture 脚本: %v", err)
	}
	const original = `"create|coordination.k8s.io|leases|namespace|lease_create@L789"`
	const broken = `"create|coordination.k8s.io|leases|namespace|lease_create@L789|surgecart-deploy-lock"`
	if !strings.Contains(string(raw), original) {
		t.Fatalf("fixture 脚本里找不到 create leases 条目 %q：本用例的前提不成立", original)
	}
	patched := strings.Replace(string(raw), original, broken, 1)
	if err := os.WriteFile(path, []byte(patched), 0o755); err != nil {
		t.Fatalf("写入 fixture 脚本: %v", err)
	}

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "权限表自检失败", "create")
	requireNoResourceMutations(t, f)
	if lease := f.state().Lease; lease != nil {
		t.Fatalf("权限表自检失败时不得创建 Lease，实际: %+v", lease)
	}
}

// TestLeasePermissionTableCarriesObjectName 是**静态**反向契约（F-01）：
// 只要交付的 RBAC 用 resourceNames 限制了 Lease，权限表里的 get/update 就必须
// 带同样的对象名。删掉第 6 字段 → 预检退回无对象名形式 → 本测试失败。
func TestLeasePermissionTableCarriesObjectName(t *testing.T) {
	t.Parallel()
	entries := requiredPermissionsFromScript(t)
	named := map[string]string{}
	for _, entry := range entries {
		if entry.Resource != "leases" {
			if entry.Name != "" {
				t.Fatalf("非 leases 的条目不应带对象名（交付清单没有对它们用 resourceNames）：%+v", entry)
			}
			continue
		}
		named[entry.Verb] = entry.Name
	}
	for _, verb := range []string{"get", "update"} {
		name, ok := named[verb]
		if !ok {
			t.Fatalf("REQUIRED_PERMISSIONS 缺少 leases 的 %s 条目", verb)
		}
		if name != knownLeaseName {
			t.Fatalf("leases 的 %s 预检必须带对象名 %q（交付 RBAC 用 resourceNames 限制），实际 %q",
				verb, knownLeaseName, name)
		}
	}
	if name, ok := named["create"]; ok && name != "" {
		t.Fatalf("create 不能带对象名（resourceNames 对 create 无效），实际 %q", name)
	}
}
