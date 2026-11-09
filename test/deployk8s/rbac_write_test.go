package deployk8s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证「**只**授予交付清单里的权限，发布能完整运行」，以及写路径 /
// 预检路径在资源名不匹配时必须 fail-closed。
//
// T2-06.1.5 / F-01 的关键变化：交付的 Role 用 `resourceNames` 把 leases 的
// get/update/patch 限定到 `surgecart-deploy-lock`。真实 RBAC 的
// ResourceNameMatches 要求请求带对象名，因此：
//
//   - 预检必须用 `auth can-i get leases.coordination.k8s.io/<lock> -n <ns>`；
//   - 不带对象名的预检在真实集群上恒为 no（旧 fake 却返回 yes —— 这是 F-01）；
//   - 对象名写错时，预检阶段就必须失败（不是等到 replace 被 403）。
//
// 因此本文件的用例不仅断言退出码，还断言**授权对象**、**API 调用次数**与
// **最终资源状态**（锁是否释放、迁移记录、应用是否落地）。

// ---------------------------------------------------------------------------
// 工具：按真实 RBAC 清单生成 fake 的权限规则
// ---------------------------------------------------------------------------

type rbacManifestDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups     []string `yaml:"apiGroups"`
		Resources     []string `yaml:"resources"`
		Verbs         []string `yaml:"verbs"`
		ResourceNames []string `yaml:"resourceNames"`
	} `yaml:"rules"`
}

// deliveredRBACRules 把交付的 RBAC 清单翻译成 fake 的 permissions.rules。
//
//   - Role：metadata.namespace 填入 namespaces（角色只在该命名空间生效）；
//   - ClusterRole：namespaces 留空（集群级资源全部命名空间可见）；
//   - RoleBinding / ClusterRoleBinding 不含 rules，跳过。
func deliveredRBACRules(t *testing.T) []map[string]any {
	t.Helper()
	path := filepath.Join(repoRootPath, "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	var rules []map[string]any
	for {
		var doc rbacManifestDoc
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		if doc.Kind != "Role" && doc.Kind != "ClusterRole" {
			continue
		}
		for _, rule := range doc.Rules {
			entry := map[string]any{
				"apiGroups": rule.APIGroups,
				"resources": rule.Resources,
				"verbs":     rule.Verbs,
			}
			if len(rule.ResourceNames) > 0 {
				entry["resourceNames"] = rule.ResourceNames
			}
			if doc.Kind == "Role" && doc.Metadata.Namespace != "" {
				entry["namespaces"] = []string{doc.Metadata.Namespace}
			}
			rules = append(rules, entry)
		}
	}
	if len(rules) == 0 {
		t.Fatalf("%s 没有解析出任何 rules：写路径用例会失去意义", path)
	}
	return rules
}

// injectPermissions 直接改写 fake 的 state.json 里的 permissions 段。
//
// 为什么不用 f.updateState：它走 fakestate.State 结构体，会把本工作区还在演进中的
// 字段（rules）丢掉。直接写 JSON 对两版 fake 都安全（旧版忽略未知字段）。
//
// 注意：必须在所有 f.updateState 之后调用（updateState 会覆盖整个 state.json）。
func injectPermissions(t *testing.T, f *fixture, permissions map[string]any) {
	t.Helper()
	path := filepath.Join(f.stateDir, fakestate.StateFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 fake 状态: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("解析 fake 状态: %v", err)
	}
	state["permissions"] = permissions
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("序列化 fake 状态: %v", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("写入 fake 状态: %v", err)
	}
}

// writeEnforcementMissing 是「写路径没有授权」时的失败信息。
//
// fake 只在 `auth can-i` 上判权限、写请求一律放行的话，本文件的所有结论都不成立，
// 因此这是**失败**（不是 Skip）：审计 A-5 要求写路径本身 fail-closed。
func writeEnforcementMissing(name string) string {
	return "A-5 未闭环：fake 的写路径没有执行授权（" + name + " 因此「通过」）。" +
		"fake 必须在 create/replace/apply 落盘前调用 fakestate.Authorize" +
		"（INTERFACE_CONTRACT.md §1.3）。"
}

// injectRules 注入 RBAC 规则，并探测当前工作区的 fake 是否**理解** permissions.rules。
//
// 探测方式：Load → Save 一个往返。若 "rules" 被丢掉，说明当前 fake 还不认识规则，
// 此时任何「按规则授权」的结论都不可信 —— 调用方必须直接失败（不得 Skip）。
func injectRules(t *testing.T, f *fixture, rules []map[string]any) bool {
	t.Helper()
	injectPermissions(t, f, map[string]any{"rules": rules})
	state, err := fakestate.Load(f.stateDir)
	if err != nil {
		t.Fatalf("探测 fake 能力时读取状态: %v", err)
	}
	if err := fakestate.Save(f.stateDir, state); err != nil {
		t.Fatalf("探测 fake 能力时写回状态: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(f.stateDir, fakestate.StateFileName))
	if err != nil {
		t.Fatalf("探测 fake 能力时读取状态文件: %v", err)
	}
	return strings.Contains(string(raw), `"rules"`)
}

// applyVerbMismatch 识别「fake 把 apply 已存在对象判成 update」这一接口分歧的签名。
//
// 真实 kubectl apply 对已存在对象发送 PATCH，因此交付 RBAC 里没有 update；
// fake 若按 update 授权，就会返回 `cannot update resource "<r>"`。这种情况下失败
// 的原因在接口而不在交付清单，用例必须直接失败并指名分歧，而不是去加过度的 update 权限。
func applyVerbMismatch(output string) string {
	matches := regexp.MustCompile(`cannot update resource "([^"]+)"`).FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var names []string
	for _, match := range matches {
		if !seen[match[1]] {
			seen[match[1]] = true
			names = append(names, match[1])
		}
	}
	return strings.Join(names, ",")
}

// ---------------------------------------------------------------------------
// 工具：授权调用的断言
// ---------------------------------------------------------------------------

// authCalls 返回所有 `auth can-i ...` 调用的尾部（从 "auth" 开始），用于断言
// 「预检到底检查了哪个对象」——只看退出码无法发现 F-01 这类缺陷。
func authCalls(f *fixture) []string {
	var out []string
	for _, call := range f.calls() {
		if idx := strings.Index(call, " auth can-i "); idx >= 0 {
			out = append(out, strings.TrimSpace(call[idx+1:]))
		}
	}
	return out
}

// requireAuthCall 断言存在一条包含 want 的 can-i 调用。
func (f *fixture) requireAuthCall(t *testing.T, want string) {
	t.Helper()
	for _, call := range authCalls(f) {
		if strings.Contains(call, want) {
			return
		}
	}
	t.Fatalf("缺少授权检查 %q；实际 can-i 调用:\n%s", want, strings.Join(authCalls(f), "\n"))
}

// requireNoAuthCall 断言不存在包含 unwanted 的 can-i 调用。
func (f *fixture) requireNoAuthCall(t *testing.T, unwanted string) {
	t.Helper()
	for _, call := range authCalls(f) {
		if strings.Contains(call, unwanted) {
			t.Fatalf("不应出现授权检查 %q（它会给出与真实 RBAC 不同的结论）；实际 can-i 调用:\n%s",
				unwanted, strings.Join(authCalls(f), "\n"))
		}
	}
}

// requireNoAuthDenied 断言预检期间没有出现任何 denied / unknown。
func (f *fixture) requireNoAuthDenied(t *testing.T) {
	t.Helper()
	for _, prefix := range []string{"auth.denied", "auth.error", "auth.forbidden"} {
		if count := f.countEvents(prefix); count != 0 {
			t.Fatalf("成功的发布不应出现 %s 事件（%d 次）；事件序列:\n%s",
				prefix, count, strings.Join(f.events(), "\n"))
		}
	}
}

// ---------------------------------------------------------------------------
// T1 / T2：只载入交付的 RBAC → 首次发布 / 后续升级必须成功
// ---------------------------------------------------------------------------

// TestDeliveredRBACOnlyUpgradeSucceeds（T2）是「仅授予交付的 RBAC 权限，升级发布
// 能完整运行」的证据。
//
// 断言四层事实，缺一不可：
//  1. 退出码 0 + DEPLOY_K8S_SUCCESS；
//  2. 授权对象：Lease 的 get/update 按**对象名**检查（create 不带名字，
//     pods/log 走 --subresource）——这正是 F-01 的修复点；
//  3. API 调用次数：迁移/应用各 apply 一次、锁只获取一次、零 denied/unknown；
//  4. 最终资源状态：锁已释放、迁移记录 COMPLETE、应用已落地、命名空间仍在。
func TestDeliveredRBACOnlyUpgradeSucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("只载入交付的 RBAC → 升级发布"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	if res.code != 0 {
		if reason := applyVerbMismatch(res.output()); reason != "" {
			t.Fatalf("fake 仍把 apply 已存在对象按 update 授权（真实 kubectl apply 用 patch）。被拒对象：%s\n--- stdout ---\n%s\n--- stderr ---\n%s",
				reason, res.stdout, res.stderr)
		}
		t.Fatalf("只载入交付的 RBAC 时发布失败（退出码 %d）：交付的授权不足以支撑真实写路径\n--- stdout ---\n%s\n--- stderr ---\n%s",
			res.code, res.stdout, res.stderr)
	}
	res.requireSuccess(t)

	// 2) 授权对象。
	f.requireAuthCall(t, "auth can-i get leases.coordination.k8s.io/"+knownLeaseName+" -n surgecart")
	f.requireAuthCall(t, "auth can-i update leases.coordination.k8s.io/"+knownLeaseName+" -n surgecart")
	f.requireAuthCall(t, "auth can-i create leases.coordination.k8s.io -n surgecart")
	// create 不能带对象名（resourceNames 对 create 无效）。
	f.requireNoAuthCall(t, "auth can-i create leases.coordination.k8s.io/")
	// 受 resourceNames 限制的 get/update 不得再出现无对象名形式（F-01）。
	f.requireNoAuthCall(t, "auth can-i get leases.coordination.k8s.io -n")
	f.requireNoAuthCall(t, "auth can-i update leases.coordination.k8s.io -n")
	// 子资源必须走 --subresource；位置参数 pods/log 会被 kubectl 当成「名为 log 的 Pod」。
	f.requireAuthCall(t, "auth can-i get pods --subresource=log -n surgecart")
	f.requireNoAuthCall(t, "auth can-i get pods/log")
	f.requireNoAuthDenied(t)

	// 3) API 调用次数。
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("迁移清单必须 apply 恰好 1 次，实际 %d 次；事件:\n%s", got, strings.Join(f.events(), "\n"))
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("应用清单必须 apply 恰好 1 次，实际 %d 次；事件:\n%s", got, strings.Join(f.events(), "\n"))
	}
	if got := f.countEvents("lease.acquire"); got != 1 {
		t.Fatalf("发布锁必须只获取 1 次，实际 %d 次；事件:\n%s", got, strings.Join(f.events(), "\n"))
	}
	if got := f.countEvents("lease.release"); got != 1 {
		t.Fatalf("发布锁必须只释放 1 次，实际 %d 次；事件:\n%s", got, strings.Join(f.events(), "\n"))
	}

	// 4) 最终资源状态。
	state := f.state()
	if state.Lease == nil {
		t.Fatal("发布结束后 Lease 对象必须仍然存在（释放 ≠ 删除）")
	}
	if state.Lease.HolderIdentity != "" {
		t.Fatalf("发布成功后必须释放锁（holderIdentity 为空），实际 %q", state.Lease.HolderIdentity)
	}
	if recordState, job, uid := f.leaseRecord(); recordState != "MIGRATION_COMPLETE" || job == "" || uid == "" {
		t.Fatalf("最终迁移记录应为 MIGRATION_COMPLETE 且保留 job/uid，实际 state=%q job=%q uid=%q",
			recordState, job, uid)
	}
	if !state.Applied {
		t.Fatal("发布成功后应用清单必须已经落地（state.Applied=false）")
	}
	if !state.MigrationApplied {
		t.Fatal("发布成功后迁移 Job 必须已经 apply（state.MigrationApplied=false）")
	}
	if !state.NamespaceExists {
		t.Fatal("升级发布不得删除命名空间")
	}
}

// TestDeliveredRBACOnlyFirstDeploySucceeds（T1）覆盖审计 A-1 的完整路径：
// 命名空间不存在 → 集群级 ClusterRole（namespaces get/create）→ 创建命名空间
// → 锁 → 迁移 → 应用。
func TestDeliveredRBACOnlyFirstDeploySucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})
	f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("只载入交付的 RBAC → 首次发布"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	if res.code != 0 {
		if reason := applyVerbMismatch(res.output()); reason != "" {
			t.Fatalf("fake 仍把 apply 已存在对象按 update 授权（同 TestDeliveredRBACOnlyUpgradeSucceeds），被拒对象：%s", reason)
		}
		if strings.Contains(res.output(), "缺少 namespaces 的") {
			t.Fatalf("交付的 RBAC 仍然缺少集群级 namespaces 权限（审计 A-1 未闭环）：\n%s", res.output())
		}
		t.Fatalf("只载入交付的 RBAC 时首次发布失败（退出码 %d）\n--- stdout ---\n%s\n--- stderr ---\n%s",
			res.code, res.stdout, res.stderr)
	}
	res.requireSuccess(t)

	// 授权对象：首次部署同样必须按对象名检查 Lease，且集群级权限不带 -n。
	f.requireAuthCall(t, "auth can-i get leases.coordination.k8s.io/"+knownLeaseName+" -n surgecart")
	f.requireAuthCall(t, "auth can-i update leases.coordination.k8s.io/"+knownLeaseName+" -n surgecart")
	f.requireAuthCall(t, "auth can-i get namespaces")
	f.requireAuthCall(t, "auth can-i create namespaces")
	f.requireNoAuthCall(t, "auth can-i get leases.coordination.k8s.io -n")
	f.requireNoAuthDenied(t)

	// API 调用次数与顺序：命名空间引导发生在锁之前，迁移/应用各一次。
	if got := f.countEvents("apply.namespace"); got != 1 {
		t.Fatalf("首次发布必须创建命名空间恰好 1 次，实际 %d 次；事件:\n%s", got, strings.Join(f.events(), "\n"))
	}
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("迁移清单必须 apply 恰好 1 次，实际 %d 次", got)
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("应用清单必须 apply 恰好 1 次，实际 %d 次", got)
	}
	f.requireEventOrder(t, "apply.namespace", "lease.acquire", "apply.migration", "apply.application")

	// 最终资源状态。
	state := f.state()
	if !state.NamespaceExists {
		t.Fatal("首次发布必须创建命名空间")
	}
	if state.Lease == nil || state.Lease.HolderIdentity != "" {
		t.Fatalf("首次发布成功后锁必须已释放，实际: %+v", state.Lease)
	}
	if recordState, _, _ := f.leaseRecord(); recordState != "MIGRATION_COMPLETE" {
		t.Fatalf("首次发布的迁移记录应为 MIGRATION_COMPLETE，实际 %q", recordState)
	}
	if !state.Applied {
		t.Fatal("首次发布必须落地应用清单")
	}
}

// ---------------------------------------------------------------------------
// T6 / T8 / T9：resourceNames 写成错误对象名 → 预检阶段失败，零资源写
// ---------------------------------------------------------------------------
//
// 交付的 Role 把 leases 的 get/update/patch 限定到 LOCK_NAME。把它改成另一个
// 名字后：
//   - 真实 RBAC 会拒绝针对 LOCK_NAME 的对象级 SSAR —— 预检必须在这里失败；
//   - 不能等到 `replace` 才 403（那意味着带着未知/错误权限已经进入了发布流程）；
//   - 失败必须是 fail-closed 且**零**资源写请求（不建锁、不迁移、不应用）。
func TestWrongLeaseResourceNameFailsAtPreflight(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	rules := deliveredRBACRules(t)
	patched := 0
	for _, rule := range rules {
		resourceNames, ok := rule["resourceNames"]
		if !ok {
			continue
		}
		names, _ := resourceNames.([]string)
		if len(names) != 1 || names[0] != knownLeaseName {
			continue
		}
		rule["resourceNames"] = []string{"another-pipeline-lock"}
		patched++
	}
	if patched == 0 {
		t.Fatal("交付的 RBAC 里没有把 leases 的 resourceNames 限定到 LOCK_NAME：本用例的前提不成立")
	}
	if !injectRules(t, f, rules) {
		t.Fatalf("%s", writeEnforcementMissing("错误 Lease 名称"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	// 预检必须**针对具体对象**报错（否则运维不知道该改哪个 Lease）。
	res.requireContains(t,
		"缺少 leases.coordination.k8s.io/"+knownLeaseName+" 的 get 权限",
		"Forbidden",
		"权限不足",
	)
	// 失败必须发生在预检，而不是运行期的 replace。
	res.requireNotContains(t, "权限预检通过")
	res.requireNotContains(t, "DEPLOY_K8S_SUCCESS")
	// 零资源写：不建锁、不迁移、不应用、不发日志请求。
	requireNoResourceMutations(t, f)
	state := f.state()
	if state.Lease != nil {
		t.Fatalf("预检失败时不得创建 Lease，实际: %+v", state.Lease)
	}
	if state.Applied || state.MigrationApplied {
		t.Fatalf("预检失败时不得 apply 任何清单（applied=%v migration=%v）", state.Applied, state.MigrationApplied)
	}
	for _, prefix := range []string{"apply.migration", "apply.application", "lease.acquire"} {
		f.requireNoEvent(t, prefix)
	}
}

// TestWrongLeaseResourceNameFailsAtPreflightForUpdate 单独确认 update 的对象级
// 预检也在生效：把 get/update/patch 规则拆成两条，只把 update 的 resourceNames
// 改名 —— get 仍然通过，失败必须由 update 的检查触发（调用点 lease_takeover/...）。
func TestWrongLeaseResourceNameFailsAtPreflightForUpdate(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	rules := deliveredRBACRules(t)
	next := make([]map[string]any, 0, len(rules)+1)
	replaced := false
	for _, rule := range rules {
		resourceNames, hasNames := rule["resourceNames"]
		verbs, _ := rule["verbs"].([]string)
		names, _ := resourceNames.([]string)
		if hasNames && len(verbs) == 3 && contains(verbs, "get") && contains(verbs, "update") &&
			len(names) == 1 && names[0] == knownLeaseName {
			// 同一条规则同时授予 get/update/patch；拆成「get 正确」+「update/patch 错误」，
			// 这样才能证明失败确实来自 update 的对象级检查。
			getRule := map[string]any{
				"apiGroups":     rule["apiGroups"],
				"resources":     rule["resources"],
				"verbs":         []string{"get"},
				"resourceNames": []string{knownLeaseName},
			}
			updateRule := map[string]any{
				"apiGroups":     rule["apiGroups"],
				"resources":     rule["resources"],
				"verbs":         []string{"update", "patch"},
				"resourceNames": []string{"another-pipeline-lock"},
			}
			if ns, ok := rule["namespaces"]; ok {
				getRule["namespaces"] = ns
				updateRule["namespaces"] = ns
			}
			next = append(next, getRule, updateRule)
			replaced = true
			continue
		}
		next = append(next, rule)
	}
	if !replaced {
		t.Fatal("未能替换 leases 的 get/update/patch 规则：本用例的前提不成立")
	}
	if !injectRules(t, f, next) {
		t.Fatalf("%s", writeEnforcementMissing("错误 Lease 名称（update）"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 3)
	res.requireContains(t,
		"缺少 leases.coordination.k8s.io/"+knownLeaseName+" 的 update 权限",
		"Forbidden",
	)
	// get 仍然被授权（证明失败来自 update 这一条，不是 get 连带失败）。
	res.requireNotContains(t, "缺少 leases.coordination.k8s.io/"+knownLeaseName+" 的 get 权限")
	requireNoResourceMutations(t, f)
}
