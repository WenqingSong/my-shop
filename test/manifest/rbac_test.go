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

// 本文件断言 T2-06.1.3 / F-01 的权限契约：
//
//	生产脚本实际需要的 (verb, resource) ⊆ 交付 RBAC 的授权范围
//
// 为什么必须是自动化断言：T2-06.1.2 的 RBAC 示例漏了 core API 的 pods get/list，
// 而脚本在发布前要按 job-name 读取 Pod、按 component 标签扫描残留迁移 Pod。
// 手工检查 YAML 看不出「脚本还需要什么」，只有把三方拉到一起比对才能证明完整性：
//
//  1. 脚本的权限预检表（LEASE_PERMISSION_CHECKS）：脚本**声明**需要的权限；
//  2. 脚本里真实出现的 kubectl 资源操作：脚本**实际**使用的权限；
//  3. manifest/kubernetes/examples/deploy-lock/rbac.yaml：**交付**的授权范围。
//
// 三者不一致（声明漏了实际使用、授权漏了声明）都必须让门禁失败。

// permission 是归一化后的 RBAC 权限三元组。
type permission struct {
	verb     string
	group    string
	resource string
}

func (p permission) String() string {
	group := p.group
	if group == "" {
		group = "core"
	}
	return fmt.Sprintf("%s %s (%s)", p.verb, p.resource, group)
}

// lockPermissions 是发布锁链路涉及的资源：只有这些资源受本契约约束。
// 其它资源（namespace/secret/deployment）属于发布流程之外的前置条件，不在 deploy-lock Role 里。
var lockResourceGroups = map[string]string{
	"leases": "coordination.k8s.io",
	"jobs":   "batch",
	"pods":   "",
}

// appResources 是脚本会读、但不属于部署锁 RBAC 的资源（占位值用于识别而不是断言）。
var appResources = map[string]bool{
	"namespace": true, "secret": true, "deployment": true,
	"deployment,service,pod": true, "pod": true,
}

// scriptPermissionChecks 解析脚本里的 LEASE_PERMISSION_CHECKS 数组。
func scriptPermissionChecks(t *testing.T, script string) []permission {
	t.Helper()
	block := regexp.MustCompile(`(?s)LEASE_PERMISSION_CHECKS=\((.*?)\n\)`).FindStringSubmatch(script)
	if block == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 LEASE_PERMISSION_CHECKS 数组：权限契约的唯一事实来源不能缺失")
	}
	var out []permission
	for _, line := range strings.Split(block[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := regexp.MustCompile(`^"([a-z]+) ([^"]+)"$`).FindStringSubmatch(trimmed)
		if match == nil {
			t.Fatalf("LEASE_PERMISSION_CHECKS 里的条目格式无法解析: %q（期望 \"<verb> <resource.group>\"）", trimmed)
		}
		parsed, err := parseResourceRef(match[1], match[2])
		if err != nil {
			t.Fatalf("LEASE_PERMISSION_CHECKS 条目 %q: %v", trimmed, err)
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		t.Fatal("LEASE_PERMISSION_CHECKS 为空：权限预检实际不会执行")
	}
	return out
}

// parseResourceRef 把 `leases.coordination.k8s.io` / `jobs.batch` / `pods` 解析成三元组。
func parseResourceRef(verb, ref string) (permission, error) {
	resource, group := ref, ""
	if index := strings.Index(ref, "."); index > 0 {
		resource, group = ref[:index], ref[index+1:]
	}
	expectedGroup, known := lockResourceGroups[resource]
	if !known {
		return permission{}, fmt.Errorf("未知的锁链路资源 %q：请同步 lockResourceGroups 与 RBAC 示例", resource)
	}
	if group != expectedGroup {
		return permission{}, fmt.Errorf("资源 %q 的 group 应为 %q，实际 %q", resource, expectedGroup, group)
	}
	return permission{verb: verb, group: group, resource: resource}, nil
}

// scriptPermissionUsage 从脚本里真实出现的 kubectl 调用推导 (verb, resource)。
//
// 只看 `${KUBECTL[@]}` / `${LEASE_KUBECTL[@]}` 之后的那一段参数，跳过 flag 与其取值，
// 第一个非 flag 词是子命令，紧随其后的资源词（如果可以识别）就是本次操作的对象。
//
// 注意：`kubectl get pods -l ...` 在 RBAC 语义里用的是 **list** 动词（按集合读取），
// 因此这里的推导只会得到 `list pods`；脚本仍然声明并预检 `get pods`（只读、无副作用，
// 且 F-01 要求两个动词都预检），契约测试会强制 RBAC 也必须授予它。
func scriptPermissionUsage(t *testing.T, script string) []permission {
	t.Helper()
	callPattern := regexp.MustCompile(`\$\{(?:LEASE_)?KUBECTL\[@\]\}([^\n]*)`)
	verbs := map[string]bool{"get": true, "create": true, "replace": true, "delete": true, "patch": true}
	flagWithValue := map[string]bool{
		"-n": true, "--namespace": true, "-o": true, "--output": true, "-f": true,
		"--filename": true, "-l": true, "--selector": true, "--tail": true,
		"--timeout": true, "--for": true, "--type": true, "-p": true,
	}
	var found []permission
	seen := map[permission]bool{}
	add := func(key permission) {
		if !seen[key] {
			seen[key] = true
			found = append(found, key)
		}
	}

	for _, match := range callPattern.FindAllStringSubmatch(script, -1) {
		// `${KUBECTL[@]}"` 里的收尾引号会留在捕获结果里：去掉行首的引号再切词。
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
				// 无值 flag（--overwrite / --all-containers 等）
			case verb == "" && verbs[token]:
				verb = token
			case verb == "":
				// version / kustomize / auth / logs / describe / wait / annotate：
				// 不是资源操作，整条调用跳过。
				verb = "-"
			case resource == "":
				resource = token
			}
		}
		if verb == "" || verb == "-" || resource == "" {
			continue
		}
		// create/replace 作用在 -f 指向的 Lease 清单上（PUT 需要 update 动词）。
		if verb == "create" || verb == "replace" {
			if !strings.Contains(filename, "lease-") {
				continue
			}
			resource = "lease"
			if verb == "replace" {
				verb = "update"
			}
		}
		perm, known := rawsToPermission(verb, resource)
		if !known {
			if appResources[resource] {
				continue
			}
			t.Fatalf("scripts/deploy-k8s.sh 使用了未知资源 %q（verb=%s）：需要同步 lockResourceGroups 与 RBAC 示例", resource, verb)
		}
		add(perm)
	}
	return found
}

// rawsToPermission 把 kubectl 里的资源写法映射成 RBAC 三元组：
// 复数（`get jobs` / `get pods`）在 RBAC 里是 **list**，单数（`get job <name>`）是 **get**。
func rawsToPermission(verb, resource string) (permission, bool) {
	switch resource {
	case "lease", "leases":
		normalized := "leases"
		return permission{verb: verb, group: lockResourceGroups[normalized], resource: normalized}, true
	case "job":
		return permission{verb: verb, group: lockResourceGroups["jobs"], resource: "jobs"}, true
	case "jobs":
		permVerb := verb
		if verb == "get" {
			permVerb = "list"
		}
		return permission{verb: permVerb, group: lockResourceGroups["jobs"], resource: "jobs"}, true
	case "pod":
		return permission{verb: verb, group: lockResourceGroups["pods"], resource: "pods"}, true
	case "pods":
		permVerb := verb
		if verb == "get" {
			permVerb = "list"
		}
		return permission{verb: permVerb, group: lockResourceGroups["pods"], resource: "pods"}, true
	default:
		return permission{}, false
	}
}

// rbacDocument 是 RBAC 示例里本测试关心的字段。
type rbacDocument struct {
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
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
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

// TestRBACGrantsEveryPermissionTheScriptNeeds 是 F-01 的核心契约断言。
func TestRBACGrantsEveryPermissionTheScriptNeeds(t *testing.T) {
	scriptPath := filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh")
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("读取发布脚本: %v", err)
	}
	script := string(raw)

	declared := scriptPermissionChecks(t, script)
	used := scriptPermissionUsage(t, script)
	rbacPath := filepath.Join(repoRoot(t), "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
	docs := loadRBAC(t, rbacPath)

	var role *rbacDocument
	for i := range docs {
		switch docs[i].Kind {
		case "ClusterRole", "ClusterRoleBinding":
			t.Fatalf("%s 不得授予集群级权限（%s）：leases/jobs/pods 都是 namespaced 资源", rbacPath, docs[i].Kind)
		case "Role":
			role = &docs[i]
		}
	}
	if role == nil {
		t.Fatalf("%s 缺少 Role", rbacPath)
	}
	lockName := regexp.MustCompile(`(?m)^LOCK_NAME="([^"]+)"`).FindStringSubmatch(script)
	if lockName == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 LOCK_NAME")
	}

	// 授权判定：group/resource/verb 匹配；带 resourceNames 的规则必须包含脚本使用的锁名。
	granted := func(key permission) bool {
		for _, rule := range role.Rules {
			if !containsAny(rule.APIGroups, key.group, "*") {
				continue
			}
			if !containsAny(rule.Resources, key.resource, "*") {
				continue
			}
			if !containsAny(rule.Verbs, key.verb, "*") {
				continue
			}
			if len(rule.ResourceNames) > 0 && !containsAny(rule.ResourceNames, lockName[1], "") {
				continue
			}
			return true
		}
		return false
	}

	// 1) 脚本声明 ⊇ 脚本实际使用：声明表不能漏掉真实调用。
	if len(used) == 0 {
		t.Fatal("没有从脚本里推导出任何锁链路资源操作：检查 scriptPermissionUsage 的解析规则（空集合会让本测试失去意义）")
	}
	declaredSet := map[permission]bool{}
	for _, key := range declared {
		declaredSet[key] = true
	}
	for _, key := range used {
		if !declaredSet[key] {
			t.Fatalf("脚本使用了 %s，但权限预检表 LEASE_PERMISSION_CHECKS 没有声明：缺少权限时会在运行到一半才失败", key)
		}
	}

	// 2) 交付 RBAC ⊇ 脚本声明：按照示例授权的身份必须能跑完整个发布会话。
	for _, key := range declared {
		if !granted(key) {
			t.Fatalf("%s 没有授权 %s：按照示例授权的身份会在权限预检阶段失败（退出码 3）", rbacPath, key)
		}
	}

	// 3) 关键回归：pods 的 get/list 必须在授权范围内（T2-06.1.2 缺失的那一项）。
	for _, verb := range []string{"get", "list"} {
		key := permission{verb: verb, group: "", resource: "pods"}
		if !declaredSet[key] {
			t.Fatalf("权限预检表缺少 %s：Pod 生命周期门禁依赖它", key)
		}
		if !granted(key) {
			t.Fatalf("%s 缺少 %s：这是 T2-06.1.2 的 F-01 缺陷（首次发布就会失败）", rbacPath, key)
		}
	}

	// 4) 不允许过度授权（否则「最小权限」只是口号）。
	for _, rule := range role.Rules {
		for _, group := range rule.APIGroups {
			if group == "*" {
				t.Fatalf("%s 出现通配 apiGroups: *", rbacPath)
			}
		}
		for _, resource := range rule.Resources {
			if resource == "*" {
				t.Fatalf("%s 出现通配 resources: *", rbacPath)
			}
		}
		for _, verb := range rule.Verbs {
			if verb == "*" || verb == "delete" || verb == "deletecollection" {
				t.Fatalf("%s 授予了 %q：发布脚本永不删除资源，最小权限不含删除", rbacPath, verb)
			}
		}
	}

	// 5) 只读资源（jobs/pods）必须是只读授权。
	for _, rule := range role.Rules {
		readOnlyTarget := false
		for _, resource := range rule.Resources {
			if resource == "jobs" || resource == "pods" {
				readOnlyTarget = true
			}
		}
		if !readOnlyTarget {
			continue
		}
		for _, verb := range rule.Verbs {
			if verb != "get" && verb != "list" && verb != "watch" {
				t.Fatalf("%s 对 jobs/pods 授予了 %q：脚本只读取迁移生命周期", rbacPath, verb)
			}
		}
	}

	// 6) 命名空间必须与清单一致（脚本从 Application 清单读取 NAMESPACE）。
	namespace := mapAt(t, appDeployment(t), "metadata")["namespace"]
	if namespace == nil {
		namespace = "surgecart"
	}
	if role.Metadata.Namespace != "surgecart" {
		t.Fatalf("%s 的 Role 命名空间为 %q，脚本使用 %q", rbacPath, role.Metadata.Namespace, namespace)
	}

	sort.Slice(declared, func(i, j int) bool { return declared[i].String() < declared[j].String() })
	sort.Slice(used, func(i, j int) bool { return used[i].String() < used[j].String() })
	t.Logf("脚本声明 %d 项权限：%v", len(declared), declared)
	t.Logf("脚本实际使用 %d 项权限：%v", len(used), used)
}

func containsAny(values []string, want, wildcard string) bool {
	for _, value := range values {
		if value == want || value == wildcard {
			return true
		}
	}
	return false
}
