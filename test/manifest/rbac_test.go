package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本文件断言 T2-06.1.4 的权限契约（审计 A-1 / A-2 / A-5 / A-9）。
//
// 三方必须一致，任何一方漂移都要让门禁失败：
//
//  1. scripts/deploy-k8s.sh 的 REQUIRED_PERMISSIONS —— 脚本**声明**需要的权限
//     （唯一事实来源；格式 `<verb>|<group>|<resource>|<scope>|<name>|<call site>`）；
//  2. 脚本里真实出现的 kubectl 调用 —— 脚本**实际**使用的权限；
//  3. manifest/kubernetes/examples/deploy-lock/rbac.yaml —— **交付**的授权范围。
//
// 双向比对：
//   - 脚本用到的必须被授权（否则按示例授权的身份在权限预检阶段失败，退出码 3）；
//   - 被授权但脚本不用的必须显式声明为 operator-only（否则「最小权限」只是口号）。
//
// 为什么必须是自动化断言：T2-06.1.2 的示例漏了 pods、T2-06.1.3 的示例漏了
// namespaces/secrets/deployments/pods-log 与全部写动词。手工看 YAML 看不出
// 「脚本还需要什么」，也看不出「多授了什么」。

// permission 是归一化后的 RBAC 权限（scope 为空表示「不区分作用域」）。
type permission struct {
	verb     string
	group    string
	resource string
	scope    string
	// name 是脚本按对象名检查/操作的那个对象（受 RBAC resourceNames 限制时非空）。
	// 空 = 请求没有对象名（create/list 或不受限的 get）。
	name     string
	callSite string
}

func (p permission) String() string {
	group := p.group
	if group == "" || group == "core" {
		group = "core"
	}
	scope := p.scope
	if scope == "" {
		scope = "any"
	}
	name := p.name
	if name == "" {
		name = "<无对象名>"
	}
	return fmt.Sprintf("%s %s/%s (%s, %s)", p.verb, p.resource, name, group, scope)
}

// ref 返回 `kubectl auth can-i` 接受的资源写法：非 core group 需要带 group 后缀；
// 有对象名时写成 `TYPE/NAME`（kubectl 唯一支持的对象级写法）。
func (p permission) ref() string {
	base := p.resource
	if p.group != "" && p.group != "core" {
		base = p.resource + "." + p.group
	}
	if p.name != "" {
		return base + "/" + p.name
	}
	return base
}

// key 用于集合比较（不含调用点与对象名：对象名是「怎么检查」，不是「授了什么」）。
//
// 名字是否匹配由 grants() 与 §5 的对象名断言单独保证 —— 把它们混进 key 会让
// 「表里少了 objectName」变成「少了一条权限」，错误信息指向错误的方向。
func (p permission) key() string {
	return p.verb + "|" + p.group + "|" + p.resource + "|" + p.scope
}

// ---------------------------------------------------------------------------
// 1. 解析 REQUIRED_PERMISSIONS
// ---------------------------------------------------------------------------

var requiredPermissionsPattern = regexp.MustCompile(`(?s)REQUIRED_PERMISSIONS=\((.*?)\n\)`)

// requiredPermissions 解析脚本里的 REQUIRED_PERMISSIONS。
//
// 格式契约（任何违反都让测试失败，不给「宽松解析」留后门）：
//
//	"<verb>|<group>|<resource>|<scope>|<name>|<call site>"
//
// 6 个字段必须全部存在；group 用 core 表示 core/v1；scope 只能是 namespace/cluster；
// name 是受 resourceNames 限制的对象名（不受限时为空字段）。
func requiredPermissions(t *testing.T, script string) []permission {
	t.Helper()
	block := requiredPermissionsPattern.FindStringSubmatch(script)
	if block == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 REQUIRED_PERMISSIONS：权限契约的唯一事实来源不能缺失")
	}
	entryPattern := regexp.MustCompile(`^"([^"|]+)\|([^"|]*)\|([^"|]+)\|([^"|]+)\|([^"|]*)\|([^"|]*)"$`)
	var out []permission
	seen := map[string]bool{}
	for _, line := range strings.Split(block[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := entryPattern.FindStringSubmatch(trimmed)
		if match == nil {
			t.Fatalf("REQUIRED_PERMISSIONS 里的条目格式无法解析: %q（期望 \"<verb>|<group>|<resource>|<scope>|<name>|<call site>\"）", trimmed)
		}
		entry := permission{verb: match[1], group: match[2], resource: match[3], scope: match[4], name: match[5], callSite: match[6]}
		if entry.group == "" {
			t.Fatalf("REQUIRED_PERMISSIONS 条目 %q 的 group 为空：core/v1 必须显式写成 core（空字段会让解析模糊）", trimmed)
		}
		// 归一化：清单里 core/v1 写作 ""，脚本里为了可读性写作 core。
		if entry.group == "core" {
			entry.group = ""
		}
		if entry.scope != "namespace" && entry.scope != "cluster" {
			t.Fatalf("REQUIRED_PERMISSIONS 条目 %q 的 scope 必须是 namespace 或 cluster，实际 %q", trimmed, entry.scope)
		}
		if entry.callSite == "" {
			t.Fatalf("REQUIRED_PERMISSIONS 条目 %q 缺少调用点：无法证明这条权限真的被使用", trimmed)
		}
		if seen[entry.key()] {
			t.Fatalf("REQUIRED_PERMISSIONS 出现重复条目: %q", trimmed)
		}
		seen[entry.key()] = true
		out = append(out, entry)
	}
	if len(out) == 0 {
		t.Fatal("REQUIRED_PERMISSIONS 为空：权限预检实际不会执行")
	}
	return out
}

// lockNameOf 解析脚本里的 LOCK_NAME（受 resourceNames 限制的那个 Lease 对象名）。
func lockNameOf(t *testing.T, script string) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^LOCK_NAME="([^"]+)"`).FindStringSubmatch(script)
	if match == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 LOCK_NAME")
	}
	return match[1]
}

func permissionSet(entries []permission) map[string]permission {
	set := make(map[string]permission, len(entries))
	for _, entry := range entries {
		set[entry.key()] = entry
	}
	return set
}

// ---------------------------------------------------------------------------
// 2. 解析 RBAC 清单
// ---------------------------------------------------------------------------

type rbacRule struct {
	APIGroups     []string `yaml:"apiGroups"`
	Resources     []string `yaml:"resources"`
	Verbs         []string `yaml:"verbs"`
	ResourceNames []string `yaml:"resourceNames"`
}

type rbacSubject struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type rbacDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Rules   []rbacRule `yaml:"rules"`
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []rbacSubject `yaml:"subjects"`
}

func rbacPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
}

// readScript 读取发布脚本（权限契约的宿主）。
func readScript(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh"))
	if err != nil {
		t.Fatalf("读取发布脚本: %v", err)
	}
	return string(raw)
}

func loadRBAC(t *testing.T, path string) []rbacDocument {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v（发布锁必须提供最小权限示例）", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	var docs []rbacDocument
	for {
		var doc rbacDocument
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		t.Fatalf("%s 没有解析出任何资源", path)
	}
	return docs
}

func documentsByKind(docs []rbacDocument, kind string) []rbacDocument {
	var out []rbacDocument
	for _, doc := range docs {
		if doc.Kind == kind {
			out = append(out, doc)
		}
	}
	return out
}

// operatorOnlyGrants 读取 Role/ClusterRole 上的 operator-only 例外声明。
//
// 交付清单里唯一允许「授权了但脚本不用」的项是 leases patch（人工 annotate 恢复用）。
// 例外必须写在清单自身的 annotations 里（`<resource>:<verb>`，逗号分隔），
// 不允许写在测试里——否则改清单时没人看得到这个例外。
func operatorOnlyGrants(t *testing.T, docs []rbacDocument) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, doc := range docs {
		if doc.Kind != "Role" && doc.Kind != "ClusterRole" {
			continue
		}
		raw := strings.TrimSpace(doc.Metadata.Annotations["deploy-lock.surgecart.io/operator-only-verbs"])
		if raw == "" {
			continue
		}
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if !strings.Contains(item, ":") {
				t.Fatalf("%s 的 operator-only-verbs 条目 %q 格式应为 <resource>:<verb>", doc.Metadata.Name, item)
			}
			out[item] = true
		}
	}
	return out
}

// grantIndex 把一个或多个 Role/ClusterRole 的规则摊平成可查询的集合。
type grantIndex struct {
	docs []rbacDocument
}

// matchesGroup / matchesResource / matchesVerb 支持 RBAC 的通配符语义（用于识别
// 过度授权：通配符本身会被其它断言单独拒绝，这里只需要正确判定「被授权」）。
func containsAny(values []string, want, wildcard string) bool {
	for _, value := range values {
		if value == want || value == wildcard {
			return true
		}
	}
	return false
}

// grants 判断某个权限是否被授予。
//
// 对象名（resourceNames）语义必须与真实 RBAC 一致（T2-06.1.5 / 审计 F-01）：
//   - 规则的 resourceNames 非空时，只匹配**带相同对象名**的请求。因此
//     entry.name 为空的检查（create/list）不能靠这类规则「算作已授权」；
//   - entry.name 非空时，交付清单必须真的用 resourceNames 限定到这个名字 ——
//     否则「按对象名检查」就失去了最小权限的依据（也说明表与清单已经脱节）。
func (g grantIndex) grants(entry permission) bool {
	for _, doc := range g.docs {
		for _, rule := range doc.Rules {
			if !containsAny(rule.APIGroups, entry.group, "*") {
				continue
			}
			if !containsAny(rule.Resources, entry.resource, "*") {
				continue
			}
			if !containsAny(rule.Verbs, entry.verb, "*") {
				continue
			}
			if len(rule.ResourceNames) > 0 {
				if entry.name == "" {
					// 真实集群上，无对象名的请求不会匹配带 resourceNames 的规则。
					continue
				}
				if !containsAny(rule.ResourceNames, entry.name, "") {
					continue
				}
				return true
			}
			if entry.name != "" {
				// 未限定对象名的规则虽然也能放行，但它无法证明「按对象名检查」这条
				// 契约成立；交付清单必须显式限定（否则删掉 resourceNames 也能测试全绿）。
				continue
			}
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 3. 脚本里真实出现的 kubectl 调用（只用于「声明 ⊇ 实际使用」）
// ---------------------------------------------------------------------------

// kubectlResources 把 kubectl 的资源写法映射成 RBAC 三元组。
var kubectlResources = map[string][2]string{
	"namespace":             {"", "namespaces"},
	"namespaces":            {"", "namespaces"},
	"secret":                {"", "secrets"},
	"secrets":               {"", "secrets"},
	"lease":                 {"coordination.k8s.io", "leases"},
	"leases":                {"coordination.k8s.io", "leases"},
	"job":                   {"batch", "jobs"},
	"jobs":                  {"batch", "jobs"},
	"pod":                   {"", "pods"},
	"pods":                  {"", "pods"},
	"deployment":            {"apps", "deployments"},
	"deployments":           {"apps", "deployments"},
	"configmap":             {"", "configmaps"},
	"configmaps":            {"", "configmaps"},
	"service":               {"", "services"},
	"services":              {"", "services"},
	"persistentvolumeclaim": {"", "persistentvolumeclaims"},
}

// scriptKubectlUsage 从脚本里真实出现的 `${KUBECTL[@]}` 调用推导 (verb, group, resource)。
//
// 只看 `${KUBECTL[@]}` / `${LEASE_KUBECTL[@]}` 之后的那一段参数，跳过 flag 及其取值，
// 第一个非 flag 词是子命令，紧随其后的资源词就是对象。
//
// 明确**不做**的三件事（都在别处覆盖，避免这里猜错反而制造假安全感）：
//   - `apply -f <file>`：目标资源由清单内容决定 → 由 TestRequiredPermissionsCoverBuiltManifests
//     从真实构建产物推导；
//   - `delete`：脚本永不删除资源；
//   - `describe` / `wait` / `version` / `kustomize` / `auth`：不是资源操作。
func scriptKubectlUsage(t *testing.T, script string) []permission {
	t.Helper()
	callPattern := regexp.MustCompile(`\$\{(?:LEASE_)?KUBECTL\[@\]\}([^\n]*)`)
	verbs := map[string]bool{"get": true, "create": true, "replace": true, "delete": true, "patch": true, "logs": true}
	flagWithValue := map[string]bool{
		"-n": true, "--namespace": true, "-o": true, "--output": true, "-f": true,
		"--filename": true, "-l": true, "--selector": true, "--tail": true,
		"--timeout": true, "--for": true, "--type": true, "-p": true,
	}
	var found []permission
	seen := map[string]bool{}
	add := func(key permission) {
		if !seen[key.key()] {
			seen[key.key()] = true
			found = append(found, key)
		}
	}

	for _, match := range callPattern.FindAllStringSubmatch(script, -1) {
		tail := strings.TrimLeft(match[1], `"`)
		tokens := strings.Fields(tail)
		verb, resource, filename := "", "", ""
		for i := 0; i < len(tokens); i++ {
			token := tokens[i]
			switch {
			case flagWithValue[token]:
				if token == "-f" || token == "--filename" {
					if i+1 < len(tokens) {
						filename = tokens[i+1]
					}
				}
				i++
			case strings.HasPrefix(token, "-"):
				// 无值 flag
			case verb == "" && verbs[token]:
				verb = token
			case verb == "":
				verb = "-"
			case resource == "":
				resource = token
			}
		}
		if verb == "" || verb == "-" || resource == "" {
			continue
		}
		if verb == "delete" {
			t.Fatalf("scripts/deploy-k8s.sh 出现 delete 调用：发布脚本永不删除资源（清理必须人工执行），实际: %s", strings.TrimSpace(match[1]))
		}
		// `create/replace -f .../lease-*.yaml`：Lease 清单（replace = PUT = update 动词）。
		if verb == "create" || verb == "replace" {
			if !strings.Contains(filename, "lease-") {
				continue
			}
			apply := permission{verb: verb, group: "coordination.k8s.io", resource: "leases"}
			if verb == "replace" {
				apply.verb = "update"
			}
			add(apply)
			continue
		}
		// `logs job/<name>`：真正的权限是 core 的 pods/log get。
		if verb == "logs" {
			add(permission{verb: "get", group: "", resource: "pods/log"})
			continue
		}
		// 支持 `get deployment,service,pod -l ...`（逗号分隔的多资源 = 对每个 list）。
		for _, single := range strings.Split(resource, ",") {
			mapped, known := kubectlResources[single]
			if !known {
				t.Fatalf("scripts/deploy-k8s.sh 使用了未知资源 %q（verb=%s）：需要同步 kubectlResources 与 REQUIRED_PERMISSIONS", single, verb)
			}
			permVerb := verb
			// 复数 + 标签选择器在 RBAC 语义里是 list；单数按名称读取是 get。
			if permVerb == "get" && !strings.HasSuffix(single, "s") {
				permVerb = "get"
			} else if permVerb == "get" {
				permVerb = "list"
			}
			add(permission{verb: permVerb, group: mapped[0], resource: mapped[1]})
		}
	}
	return found
}

// ---------------------------------------------------------------------------
// 4. 断言
// ---------------------------------------------------------------------------

// TestRequiredPermissionsCoversAuditFindings 断言表本身覆盖了审计点名的每一项。
//
// 这是「表不能被悄悄改小」的回归锁：删掉一条 → 这里先失败，而不是等到真实集群上
// 首次发布 403。
func TestRequiredPermissionsCoversAuditFindings(t *testing.T) {
	script := readScript(t)
	entries := requiredPermissions(t, script)
	set := permissionSet(entries)
	lockName := lockNameOf(t, script)

	required := []permission{
		// 发布锁：get/update 必须按对象名检查（T2-06.1.5 / 审计 F-01）；
		// create 例外 —— create 请求没有对象名，resourceNames 无法约束它。
		{verb: "get", group: "coordination.k8s.io", resource: "leases", scope: "namespace", name: lockName},
		{verb: "create", group: "coordination.k8s.io", resource: "leases", scope: "namespace"},
		{verb: "update", group: "coordination.k8s.io", resource: "leases", scope: "namespace", name: lockName},
		// 迁移 Job
		{verb: "get", group: "batch", resource: "jobs", scope: "namespace"},
		{verb: "list", group: "batch", resource: "jobs", scope: "namespace"},
		{verb: "create", group: "batch", resource: "jobs", scope: "namespace"},
		{verb: "patch", group: "batch", resource: "jobs", scope: "namespace"},
		// Pod / 日志
		{verb: "get", group: "", resource: "pods", scope: "namespace"},
		{verb: "list", group: "", resource: "pods", scope: "namespace"},
		{verb: "get", group: "", resource: "pods/log", scope: "namespace"}, // 审计 A-8
		// 前置条件与 Application 清单
		{verb: "get", group: "", resource: "secrets", scope: "namespace"}, // 审计 A-3
		{verb: "get", group: "apps", resource: "deployments", scope: "namespace"},
		{verb: "list", group: "apps", resource: "deployments", scope: "namespace"},
		{verb: "create", group: "apps", resource: "deployments", scope: "namespace"},
		{verb: "patch", group: "apps", resource: "deployments", scope: "namespace"},
		{verb: "get", group: "", resource: "configmaps", scope: "namespace"},
		{verb: "create", group: "", resource: "configmaps", scope: "namespace"},
		{verb: "patch", group: "", resource: "configmaps", scope: "namespace"},
		{verb: "get", group: "", resource: "services", scope: "namespace"},
		{verb: "create", group: "", resource: "services", scope: "namespace"},
		{verb: "patch", group: "", resource: "services", scope: "namespace"},
		{verb: "get", group: "", resource: "persistentvolumeclaims", scope: "namespace"},
		{verb: "create", group: "", resource: "persistentvolumeclaims", scope: "namespace"},
		{verb: "patch", group: "", resource: "persistentvolumeclaims", scope: "namespace"},
		// 集群级（审计 A-1 / A-9）
		{verb: "get", group: "", resource: "namespaces", scope: "cluster"},
		{verb: "create", group: "", resource: "namespaces", scope: "cluster"},
		{verb: "patch", group: "", resource: "namespaces", scope: "cluster"}, // apply 部署清单里的 Namespace
		{verb: "create", group: "authorization.k8s.io", resource: "selfsubjectaccessreviews", scope: "cluster"},
	}
	for _, want := range required {
		got, ok := set[want.key()]
		if !ok {
			t.Fatalf("REQUIRED_PERMISSIONS 缺少 %s：这是审计点名的权限，缺失时按示例授权的身份会在真实集群上 403", want)
		}
		if want.name != "" && got.name != want.name {
			t.Fatalf("REQUIRED_PERMISSIONS 的 %s 对象名为 %q，期望 %q：受 resourceNames 限制的操作必须按具体对象名预检（审计 F-01）",
				want, got.name, want.name)
		}
		if want.name == "" && got.name != "" {
			t.Fatalf("REQUIRED_PERMISSIONS 的 %s 带了对象名 %q：create/list 等没有对象名的请求不能按对象名检查（真实 RBAC 里它们不匹配 resourceNames 规则）",
				want, got.name)
		}
	}

	// 对象名只允许出现在 leases 上（本仓库唯一的 resourceNames 资源）；且必须等于 LOCK_NAME。
	for _, entry := range entries {
		if entry.name == "" {
			continue
		}
		if entry.resource != "leases" {
			t.Fatalf("%s 带对象名 %q，但只有 leases 受 resourceNames 限制：给不受限的操作加名字会让预检检查一个不存在的对象", entry, entry.name)
		}
		if entry.name != lockName {
			t.Fatalf("%s 的对象名 %q 与 LOCK_NAME=%q 不一致：预检会去查一个 RBAC 没有授权的对象", entry, entry.name, lockName)
		}
	}

	// 禁止删除动词：脚本永不删除资源（清理必须人工执行）。
	for _, entry := range entries {
		if entry.verb == "delete" || entry.verb == "deletecollection" || entry.verb == "*" {
			t.Fatalf("REQUIRED_PERMISSIONS 出现删除动词 %q：发布脚本永不删除资源", entry.verb)
		}
		if entry.group == "*" || entry.resource == "*" {
			t.Fatalf("REQUIRED_PERMISSIONS 出现通配符条目 %s：最小权限不允许通配", entry)
		}
	}

	// 集群级只能是集群级资源（namespaces / selfsubjectaccessreviews）。
	for _, entry := range entries {
		if entry.scope != "cluster" {
			continue
		}
		if entry.resource != "namespaces" && entry.resource != "selfsubjectaccessreviews" {
			t.Fatalf("%s 被标为 cluster 作用域，但发布脚本只用它访问命名空间内资源", entry)
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].String() < entries[j].String() })
	t.Logf("REQUIRED_PERMISSIONS 共 %d 项：", len(entries))
	for _, entry := range entries {
		t.Logf("  %s  ← %s", entry, entry.callSite)
	}
}

// TestRequiredPermissionsCoverBuiltManifests 把 REQUIRED_PERMISSIONS 与**真实构建产物**
// 对齐：apply 会写哪些资源，就必须有对应的 get/create/patch。
//
// 这是「以脚本实际调用为准」的另一半证据：表是从构建产物推导出来的，
// 不是凭印象列的。
func TestRequiredPermissionsCoverBuiltManifests(t *testing.T) {
	entries := requiredPermissions(t, readScript(t))
	declared := permissionSet(entries)

	// kind → (group, resource)
	kinds := map[string][2]string{
		"Deployment":            {"apps", "deployments"},
		"Service":               {"", "services"},
		"ConfigMap":             {"", "configmaps"},
		"PersistentVolumeClaim": {"", "persistentvolumeclaims"},
		"Job":                   {"batch", "jobs"},
		"Namespace":             {"", "namespaces"},
	}

	// kind 出现的作用域：Namespace 是集群级，其余都在命名空间内。
	scopeOf := func(kind string) string {
		if kind == "Namespace" {
			return "cluster"
		}
		return "namespace"
	}

	collected := map[string]bool{}
	for _, entrypoint := range []string{
		"manifest/kubernetes/overlays/production",
		"manifest/kubernetes/migration/overlays/production",
	} {
		built := build(t, entrypoint)
		for _, doc := range built {
			collected[kindOf(doc)] = true
		}
	}
	if len(collected) == 0 {
		t.Fatal("没有从构建产物里读到任何 kind：本测试会失去意义")
	}
	for kind := range collected {
		mapped, known := kinds[kind]
		if !known {
			t.Fatalf("构建产物出现未登记的 kind %q：需要同步本测试与 REQUIRED_PERMISSIONS（它会被 apply 写入）", kind)
		}
		scope := scopeOf(kind)
		// `kubectl apply` = GET 读现状 + CREATE 或 PATCH。
		//
		// Namespace 也算：migration 与 application 两个构建产物都包含
		// `shared/namespace.yaml`，命名空间已存在时 apply 会走 PATCH。
		verbs := []string{"get", "create", "patch"}
		for _, verb := range verbs {
			key := permission{verb: verb, group: mapped[0], resource: mapped[1], scope: scope}.key()
			if _, ok := declared[key]; !ok {
				t.Fatalf("构建产物里有 %s，但 REQUIRED_PERMISSIONS 缺少 %s %s（%s）：apply 时会被拒绝",
					kind, verb, mapped[1], scope)
			}
		}
	}
	t.Logf("构建产物中的 kind（%d 种）全部有对应的 get/create/patch 授权", len(collected))
}

// verbSupportsResourceName 判断动词能否按对象名授权（与 Kubernetes RBAC 一致）：
// 只有 get/update/patch/delete 的请求带对象名；create 的名字在授权时不可知，
// list/watch/deletecollection 是集合请求。
func verbSupportsResourceName(verb string) bool {
	switch verb {
	case "get", "update", "patch", "delete":
		return true
	default:
		return false
	}
}

// leaseResourceNamesViolations 返回「交付清单里 leases 规则的 resourceNames」与
// 「预检对象名（LOCK_NAME）」之间的全部不一致项（空切片 = 一致）。
//
// 契约（T2-06.1.5 / 审计 F-01）：
//   - 必须存在 leases 规则，且至少有一条按对象名限定的规则；
//   - get/update/patch/delete → resourceNames **精确等于** [LOCK_NAME]：
//     名字缺失或写错，对象级预检在真实集群上恒为 no；多出别的名字（扩大范围）
//     等于允许改写同命名空间的其它 Lease，最小权限被悄悄放大；
//   - create → 不能带 resourceNames（真实 create 请求的授权属性没有对象名，
//     带了的规则永远匹配不到）；
//   - list/watch/deletecollection → 不能带 resourceNames（集合请求没有名字），
//     这类规则只会给人「已经授权了」的错觉。
//
// 抽成纯函数的目的：负控元测试（TestResourceNamesContractDetectsWeakenedManifest）
// 要在**人为削弱**的清单副本上证明这套断言真的会失败，而不是永远为真。
func leaseResourceNamesViolations(docs []rbacDocument, lockName string) []string {
	var problems []string
	leaseRules, nameScopedRules := 0, 0
	for _, doc := range docs {
		if doc.Kind != "Role" && doc.Kind != "ClusterRole" {
			continue
		}
		for _, rule := range doc.Rules {
			if !containsAny(rule.APIGroups, "coordination.k8s.io", "") || !containsAny(rule.Resources, "leases", "") {
				continue
			}
			leaseRules++
			for _, verb := range rule.Verbs {
				switch {
				case verb == "create":
					if len(rule.ResourceNames) > 0 {
						problems = append(problems, fmt.Sprintf(
							"%s 给 leases 的 create 加了 resourceNames=%v：真实 create 请求的授权属性没有对象名，这条规则永远匹配不到，create 会 403",
							doc.Metadata.Name, rule.ResourceNames))
					}
				case !verbSupportsResourceName(verb):
					if len(rule.ResourceNames) > 0 {
						problems = append(problems, fmt.Sprintf(
							"%s 给 leases 的 %q 加了 resourceNames=%v：该动词是集合请求（没有对象名），这条规则永远匹配不到真实请求",
							doc.Metadata.Name, verb, rule.ResourceNames))
					}
				default:
					nameScopedRules++
					if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != lockName {
						problems = append(problems, fmt.Sprintf(
							"%s 授予了 leases 的 %q，但 resourceNames=%v 不是精确的 [%q]：名字缺失/写错会让对象级预检在真实集群上恒为 no，多出的名字等于放大了最小权限",
							doc.Metadata.Name, verb, rule.ResourceNames, lockName))
					}
				}
			}
		}
	}
	if leaseRules == 0 {
		problems = append(problems, "没有任何 leases 规则：发布锁无法工作")
	} else if nameScopedRules == 0 {
		problems = append(problems, "没有任何按对象名（resourceNames）限定的 leases 规则：get/update/patch 退化为对整个资源的授权")
	}
	return problems
}

// patchLeaseRules 对每个 Role/ClusterRole 里的 leases 规则调用 mutate，返回是否真的改过。
// 负控用例用它构造「被削弱的清单副本」。
func patchLeaseRules(docs []rbacDocument, mutate func(rule *rbacRule) bool) bool {
	patched := false
	for docIndex := range docs {
		if docs[docIndex].Kind != "Role" && docs[docIndex].Kind != "ClusterRole" {
			continue
		}
		for ruleIndex := range docs[docIndex].Rules {
			rule := &docs[docIndex].Rules[ruleIndex]
			if !containsAny(rule.Resources, "leases", "") {
				continue
			}
			if mutate(rule) {
				patched = true
			}
		}
	}
	return patched
}

// cloneRBACDocs 深拷贝解析结果，避免负控修改污染其它断言。
func cloneRBACDocs(docs []rbacDocument) []rbacDocument {
	out := make([]rbacDocument, len(docs))
	for i, doc := range docs {
		out[i] = doc
		out[i].Rules = make([]rbacRule, len(doc.Rules))
		for j, rule := range doc.Rules {
			out[i].Rules[j] = rbacRule{
				APIGroups:     append([]string(nil), rule.APIGroups...),
				Resources:     append([]string(nil), rule.Resources...),
				Verbs:         append([]string(nil), rule.Verbs...),
				ResourceNames: append([]string(nil), rule.ResourceNames...),
			}
		}
	}
	return out
}

// TestResourceNamesContractDetectsWeakenedManifest 是反向契约的**负控**（T2-06.1.5）：
//
// 只断言「交付清单现在是对的」不足以证明契约有效 —— 断言可能因为写死或恒真而永远
// 通过。这里在**人为削弱**的清单副本上重跑同一个比对函数，要求它必须报出问题：
// 删除 resourceNames、改成错误名称、扩大授权范围、给 create 加名字、删除整条规则。
func TestResourceNamesContractDetectsWeakenedManifest(t *testing.T) {
	script := readScript(t)
	docs := loadRBAC(t, rbacPath(t))
	lockName := lockNameOf(t, script)

	if problems := leaseResourceNamesViolations(docs, lockName); len(problems) != 0 {
		t.Fatalf("交付清单本身就有 %d 处不一致，负控失去意义：%v", len(problems), problems)
	}

	cases := []struct {
		name   string
		mutate func(docs []rbacDocument) bool
	}{
		{
			name: "删除 resourceNames（退化为整资源授权）",
			mutate: func(docs []rbacDocument) bool {
				return patchLeaseRules(docs, func(rule *rbacRule) bool {
					if len(rule.ResourceNames) == 0 {
						return false
					}
					rule.ResourceNames = nil
					return true
				})
			},
		},
		{
			name: "改成错误的 Lease 名称",
			mutate: func(docs []rbacDocument) bool {
				return patchLeaseRules(docs, func(rule *rbacRule) bool {
					if len(rule.ResourceNames) == 0 {
						return false
					}
					rule.ResourceNames = []string{"another-pipeline-lock"}
					return true
				})
			},
		},
		{
			name: "扩大 resourceNames（追加第二个 Lease）",
			mutate: func(docs []rbacDocument) bool {
				return patchLeaseRules(docs, func(rule *rbacRule) bool {
					if len(rule.ResourceNames) == 0 {
						return false
					}
					rule.ResourceNames = append(rule.ResourceNames, "another-pipeline-lock")
					return true
				})
			},
		},
		{
			name: "给 create 加 resourceNames",
			mutate: func(docs []rbacDocument) bool {
				return patchLeaseRules(docs, func(rule *rbacRule) bool {
					if !containsAny(rule.Verbs, "create", "") || len(rule.ResourceNames) > 0 {
						return false
					}
					rule.ResourceNames = []string{lockName}
					return true
				})
			},
		},
		{
			name: "删除整条带 resourceNames 的 leases 规则",
			mutate: func(docs []rbacDocument) bool {
				removed := false
				for docIndex := range docs {
					if docs[docIndex].Kind != "Role" && docs[docIndex].Kind != "ClusterRole" {
						continue
					}
					kept := make([]rbacRule, 0, len(docs[docIndex].Rules))
					for _, rule := range docs[docIndex].Rules {
						if containsAny(rule.Resources, "leases", "") && len(rule.ResourceNames) > 0 {
							removed = true
							continue
						}
						kept = append(kept, rule)
					}
					docs[docIndex].Rules = kept
				}
				return removed
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mutated := cloneRBACDocs(docs)
			if !tc.mutate(mutated) {
				t.Fatal("没有找到可修改的 leases 规则：负控的前提不成立")
			}
			if problems := leaseResourceNamesViolations(mutated, lockName); len(problems) == 0 {
				t.Fatalf("%s 之后契约仍然通过：反向契约没有区分能力", tc.name)
			}
		})
	}
}

// TestDeliveredRBACMatchesRequiredPermissions 是核心双向契约。
func TestDeliveredRBACMatchesRequiredPermissions(t *testing.T) {
	script := readScript(t)
	entries := requiredPermissions(t, script)
	path := rbacPath(t)
	docs := loadRBAC(t, path)

	lockName := lockNameOf(t, script)

	namespace := "surgecart"
	if meta := mapAt(t, appDeployment(t), "metadata"); meta["namespace"] != nil {
		namespace = stringOf(meta["namespace"])
	}

	var namespacedRoles, clusterRoles []rbacDocument
	for _, doc := range docs {
		switch doc.Kind {
		case "Role":
			if doc.Metadata.Namespace != namespace {
				t.Fatalf("%s 的 Role %s 命名空间为 %q，脚本使用 %q", path, doc.Metadata.Name, doc.Metadata.Namespace, namespace)
			}
			namespacedRoles = append(namespacedRoles, doc)
		case "ClusterRole":
			clusterRoles = append(clusterRoles, doc)
		}
	}
	if len(namespacedRoles) == 0 {
		t.Fatalf("%s 缺少命名空间级 Role", path)
	}
	if len(clusterRoles) == 0 {
		t.Fatalf("%s 缺少 ClusterRole：namespaces 是集群级资源，Role 无法授予（审计 A-1 的根因）", path)
	}

	nsGrants := grantIndex{docs: namespacedRoles}
	clusterGrants := grantIndex{docs: clusterRoles}

	// 1) 交付 RBAC ⊇ REQUIRED_PERMISSIONS：按示例授权的身份必须能跑完整个发布会话。
	//    entry.name 非空的条目要求清单用 resourceNames 限定到同一个名字 ——
	//    否则「按对象名检查」在真实集群上就会变成 no（审计 F-01 的形态）。
	for _, entry := range entries {
		var granted bool
		if entry.scope == "cluster" {
			granted = clusterGrants.grants(entry)
		} else {
			granted = nsGrants.grants(entry)
		}
		if !granted {
			t.Fatalf("%s 没有授权 %s（调用点 %s）：按示例授权的身份会在权限预检阶段失败（退出码 3）", path, entry, entry.callSite)
		}
	}

	// 2) 交付 RBAC ⊆ REQUIRED_PERMISSIONS + operator-only：不允许静默过度授权。
	operatorOnly := operatorOnlyGrants(t, docs)
	declaredKeys := map[string]bool{}
	for _, entry := range entries {
		declaredKeys[entry.resource+":"+entry.verb] = true
	}
	var overGranted []string
	for _, doc := range append(append([]rbacDocument{}, namespacedRoles...), clusterRoles...) {
		for _, rule := range doc.Rules {
			for _, group := range rule.APIGroups {
				if group == "*" {
					t.Fatalf("%s 的 %s 出现通配 apiGroups: *", path, doc.Metadata.Name)
				}
			}
			for _, resource := range rule.Resources {
				if resource == "*" {
					t.Fatalf("%s 的 %s 出现通配 resources: *", path, doc.Metadata.Name)
				}
				for _, verb := range rule.Verbs {
					switch verb {
					case "*", "delete", "deletecollection":
						t.Fatalf("%s 的 %s 授予了 %q：发布脚本永不删除资源，最小权限不含删除", path, doc.Metadata.Name, verb)
					}
					if declaredKeys[resource+":"+verb] {
						continue
					}
					if operatorOnly[resource+":"+verb] {
						overGranted = append(overGranted, fmt.Sprintf("%s/%s（%s）", doc.Metadata.Name, resource, verb))
						continue
					}
					t.Fatalf("%s 的 %s 授权了 %s 的 %q，但 REQUIRED_PERMISSIONS 里没有对应调用点：过度授权（若确实需要人工使用，请在 Role 的 deploy-lock.surgecart.io/operator-only-verbs 里显式声明）",
						path, doc.Metadata.Name, resource, verb)
				}
			}
		}
	}
	if len(overGranted) > 0 {
		sort.Strings(overGranted)
		t.Logf("operator-only 例外（脚本不用，仅供人工恢复）：%v", overGranted)
	}

	// 3) namespaced 资源不得出现在 ClusterRole 里（那会放大到所有命名空间）。
	for _, doc := range clusterRoles {
		for _, rule := range doc.Rules {
			for _, resource := range rule.Resources {
				if resource == "namespaces" || resource == "selfsubjectaccessreviews" {
					continue
				}
				t.Fatalf("%s 的 %s 授予了命名空间级资源 %q：必须写在 Role 里，否则该身份可以在所有命名空间操作它",
					path, doc.Metadata.Name, resource)
			}
		}
	}

	// 4) 集群级资源不得出现在 Role 里（写了也无效，只会在真实集群上静默失败）。
	for _, doc := range namespacedRoles {
		for _, rule := range doc.Rules {
			for _, resource := range rule.Resources {
				if resource == "namespaces" || resource == "selfsubjectaccessreviews" {
					t.Fatalf("%s 的 %s 把集群级资源 %q 写进了 Role：Role 无法授予集群级权限（审计 A-1）",
						path, doc.Metadata.Name, resource)
				}
			}
		}
	}

	// 5) 发布锁必须**逐动词**用 resourceNames 限定到脚本实际使用的 Lease
	//    （T2-06.1.5 / 审计 F-01 的反向契约）。规则本身在 leaseResourceNamesViolations
	//    里，抽成纯函数是为了让负控元测试能在人为削弱的清单副本上证明断言有区分能力。
	for _, problem := range leaseResourceNamesViolations(namespacedRoles, lockName) {
		t.Fatalf("%s：%s", path, problem)
	}

	// 6) 两个作用域的绑定都必须存在，且 subjects 完全一致（否则会出现「预检通过、
	//    写入被拒」或反过来的撕裂状态）。
	roleBindings := documentsByKind(docs, "RoleBinding")
	clusterBindings := documentsByKind(docs, "ClusterRoleBinding")
	for _, role := range namespacedRoles {
		found := false
		for _, binding := range roleBindings {
			if binding.RoleRef.Kind == "Role" && binding.RoleRef.Name == role.Metadata.Name && binding.Metadata.Namespace == namespace {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 的 Role %s 没有同名 RoleBinding：授权不会生效", path, role.Metadata.Name)
		}
	}
	for _, role := range clusterRoles {
		found := false
		for _, binding := range clusterBindings {
			if binding.RoleRef.Kind == "ClusterRole" && binding.RoleRef.Name == role.Metadata.Name {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 的 ClusterRole %s 没有同名 ClusterRoleBinding：集群级授权不会生效", path, role.Metadata.Name)
		}
	}
	var subjects []string
	for _, binding := range append(append([]rbacDocument{}, roleBindings...), clusterBindings...) {
		for _, subject := range binding.Subjects {
			subjects = append(subjects, subject.Kind+"/"+subject.Namespace+"/"+subject.Name)
		}
	}
	if len(subjects) == 0 {
		t.Fatalf("%s 的所有绑定都没有 subjects：这份示例无法直接使用", path)
	}
	for _, subject := range subjects[1:] {
		if subject != subjects[0] {
			t.Fatalf("%s 的绑定 subjects 不一致（%q vs %q）：三个绑定必须指向同一个发布身份", path, subjects[0], subject)
		}
	}
	if !strings.Contains(subjects[0], "REPLACE_WITH_") {
		t.Logf("提示：%s 的 subjects 已替换为真实身份（%s）", path, subjects[0])
	}
}

// TestScriptKubectlUsageIsDeclared 断言「脚本实际使用的权限 ⊆ 声明表」。
func TestScriptKubectlUsageIsDeclared(t *testing.T) {
	script := readScript(t)
	declared := permissionSet(requiredPermissions(t, script))
	used := scriptKubectlUsage(t, script)
	if len(used) == 0 {
		t.Fatal("没有从脚本里推导出任何资源操作：检查 scriptKubectlUsage 的解析规则（空集合会让本测试失去意义）")
	}
	// 同一资源在脚本里可能既有 get（按名称）又有 list（按标签），两者都要声明。
	for _, entry := range used {
		if _, ok := declared[permission{verb: entry.verb, group: entry.group, resource: entry.resource, scope: "namespace"}.key()]; ok {
			continue
		}
		if _, ok := declared[permission{verb: entry.verb, group: entry.group, resource: entry.resource, scope: "cluster"}.key()]; ok {
			continue
		}
		t.Fatalf("脚本使用了 %s，但 REQUIRED_PERMISSIONS 没有声明：缺少权限时会在运行到一半才失败", entry)
	}
	sort.Slice(used, func(i, j int) bool { return used[i].String() < used[j].String() })
	t.Logf("脚本实际使用 %d 项权限：%v", len(used), used)
}

// TestUsageDocumentsRequiredPermissions 断言 usage() 的授权说明与清单同步。
//
// 审计 A-8 的一半是「文档不说 pods/log」：只改清单不改 usage() 会让运维按 --help
// 授权后仍然读不到日志。
func TestUsageDocumentsRequiredPermissions(t *testing.T) {
	script := readScript(t)
	usagePattern := regexp.MustCompile(`(?s)usage\(\) \{\n  cat <<'EOF'\n(.*?)\nEOF`)
	match := usagePattern.FindStringSubmatch(script)
	if match == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 usage() 的 heredoc")
	}
	usage := match[1]
	for _, fragment := range []string{
		"pods/log",
		"namespaces",
		"selfsubjectaccessreviews",
		"leases.coordination.k8s.io",
		"deployments.apps",
		"persistentvolumeclaims",
		"不授予 delete",
		"REQUIRED_PERMISSIONS",
	} {
		if !strings.Contains(usage, fragment) {
			t.Fatalf("usage() 的权限说明缺少 %q：--help 里的授权说明必须与 REQUIRED_PERMISSIONS 同步（审计 A-8）", fragment)
		}
	}
}
