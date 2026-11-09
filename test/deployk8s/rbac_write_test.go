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

// 本文件验证审计 A-5 的**写路径**授权（不是 `auth can-i` 的自述）：
//
//	缺权限 / 授权范围不匹配 → 真实写请求（create / replace / apply）必须被拒绝，
//	而不是「预检说没权限」或者「预检说有权限、写入随便过」。
//
// 为什么要单独一层：`kubectl auth can-i` 只回答「理论上能不能」，它与真实写入之间
// 可能存在差异（resourceNames 限定、admission webhook、策略漂移、凭据过期）。
// 如果 fake 只在 auth can-i 上做权限判断，那么「缺任一权限 → 真实写路径失败」这条
// 断言永远无法被证伪。
//
// 依赖：Agent C 的 fakestate 写路径授权（INTERFACE_CONTRACT.md §1.1 Authorize /
// §1.3 写路径强制授权，v1.2 起 apply 对已存在对象先按 patch 授权）。
//
// 这三个用例是**硬断言**：写路径不执行授权、或 apply 仍按 update 授权，都会直接
// 失败（不是 Skip）。审计要求：不允许把「未验证」说成已验证。
//
// 实现方式：直接改写 fake 的 state.json，把 `permissions.rules` 换成真实规则。
// 这样不依赖 fakestate 的 Go API（C 还在演进），也不需要修改 Agent C 的文件。

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
// 的原因在接口而不在交付清单，用例必须 Skip 并指名分歧，而不是去加过度的 update 权限。
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

// 用例 1（写路径被拒）已迁移到 rbac_resource_name_test.go 的
// TestWritePathDeniedAfterPreflightFailsClosed：预检现在会按对象名检查，静态改坏
// resourceNames 会在预检阶段被拦住（由 TestWrongResourceNameFailsPreflightBeforeAnyWrite
// 覆盖），因此「预检通过、写入被拒」只能用真实的 TOCTOU 构造。

// ---------------------------------------------------------------------------
// 用例 2：只载入交付的 RBAC → 完整发布必须成功（升级路径，T2）
// ---------------------------------------------------------------------------
//
// 这是「仅授予交付的 RBAC 权限，发布能完整运行」的**权限层面**证据：
// permissions 里只有 RBAC 清单解析出的规则，没有任何 allow-all 兜底。
//
// 断言不止退出码：还要证明预检**按对象名**检查了 Lease、每条权限恰好检查一次、
// Lease 只 create 一次，以及最终资源状态（锁已释放 + 迁移记录 COMPLETE）。
//
// 已知分歧（已 message agent-c-fake）：C 的 fake 对「apply 已存在的对象」用
// verb=update 授权，而真实 kubectl apply 用 PATCH（GET → create 或 patch，从不 PUT）。
// 若因此在本用例里看到 `cannot update resource "deployments"`，说明分歧仍在，
// 用例失败并指向该分歧，而不是去给 RBAC 加上脚本根本不用的 update 权限
// （那会让「最小权限」失去意义，也会被反向契约测试判成过度授权）。
func TestDeliveredRBACOnlyUpgradeSucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("只载入交付的 RBAC → 升级发布"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	if res.code == 0 {
		res.requireSuccess(t)
		requireDeliveredRBACPreflightShape(t, f, "surgecart-deploy-lock")
		requireLeaseReleaseAndRecord(t, f)
		if got := countCalls(f, " create -f "); got != 1 {
			t.Fatalf("Lease create 调用次数 = %d，期望 1（原子创建）", got)
		}
		if state := f.state(); !state.Applied || !state.MigrationApplied {
			t.Fatalf("最终状态不完整：applied=%v migrationApplied=%v", state.Applied, state.MigrationApplied)
		}
		return
	}
	if reason := applyVerbMismatch(res.output()); reason != "" {
		t.Fatalf("fake 仍把 apply 已存在对象按 update 授权（真实 kubectl apply 用 patch）。被拒对象：%s；"+
			"Interface 契约 §1.3 v1.2 要求「先 patch，再退回 update」。\n--- stdout ---\n%s\n--- stderr ---\n%s",
			reason, res.stdout, res.stderr)
	}
	t.Fatalf("只载入交付的 RBAC 时发布失败（退出码 %d）：交付的授权不足以支撑真实写路径\n--- stdout ---\n%s\n--- stderr ---\n%s",
		res.code, res.stdout, res.stderr)
}

// ---------------------------------------------------------------------------
// 用例 3：只载入交付的 RBAC → 首次发布（空命名空间）必须成功（T1）
// ---------------------------------------------------------------------------
//
// 覆盖审计 A-1 的完整路径：命名空间不存在 → 集群级 ClusterRole（namespaces get/create）
// → 创建命名空间 → 锁 → 迁移 → 应用。这是旧示例（只有 Role）必然 403 的那条路径。
func TestDeliveredRBACOnlyFirstDeploySucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})
	f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("只载入交付的 RBAC → 首次发布"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	if res.code == 0 {
		res.requireSuccess(t)
		if !f.state().NamespaceExists {
			t.Fatal("首次发布必须创建命名空间")
		}
		requireDeliveredRBACPreflightShape(t, f, "surgecart-deploy-lock")
		requireLeaseReleaseAndRecord(t, f)
		if state := f.state(); !state.Applied || !state.MigrationApplied {
			t.Fatalf("最终状态不完整：applied=%v migrationApplied=%v", state.Applied, state.MigrationApplied)
		}
		// 命名空间引导必须发生在锁与迁移之前（否则锁无处落地）。
		f.requireEventOrder(t, "apply.namespace", "apply.migration", "apply.application")
		return
	}
	if reason := applyVerbMismatch(res.output()); reason != "" {
		t.Fatalf("fake 仍把 apply 已存在对象按 update 授权（同 TestDeliveredRBACOnlyUpgradeSucceeds），被拒对象：%s", reason)
	}
	if strings.Contains(res.output(), "缺少 namespaces 的") {
		t.Fatalf("交付的 RBAC 仍然缺少集群级 namespaces 权限（审计 A-1 未闭环）：\n%s", res.output())
	}
	t.Fatalf("只载入交付的 RBAC 时首次发布失败（退出码 %d）\n--- stdout ---\n%s\n--- stderr ---\n%s",
		res.code, res.stdout, res.stderr)
}
