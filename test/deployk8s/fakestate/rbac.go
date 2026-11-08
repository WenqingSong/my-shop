package fakestate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件是 T2-06.1.4 的**唯一**授权判定入口（审计 A-5 / C-8）。
//
// 设计要点：
//   - 授权是「允许列表 + 默认拒绝」：没有匹配的 allow 规则就是 Forbidden，
//     通配符 "*" 只有在规则里显式写出来才生效（最小权限）；
//   - 判定依据是 Kubernetes RBAC 的同名字段（API group / resource / verb /
//     resourceNames / namespaces），因此可以把**真实的 RBAC 清单**喂给 fake，
//     「删除 RBAC 任一项 → 行为测试失败」才成立；
//   - 历史写法 Permissions.Get/Create/Update/Patch/Denied 在 Load() 时被翻译成规则，
//     不再有第二套判定逻辑。
//
// 边界：这不是 RBAC 授权器的完整实现（不处理 aggregationRule、RoleBinding 的
// subject 匹配、resourceNames 的通配）。它只覆盖 deploy-k8s.sh 用到的子集。

// Decision 是授权判定的结果。
type Decision string

const (
	// DecisionAllowed 表示有规则明确允许。
	DecisionAllowed Decision = "allowed"
	// DecisionForbidden 表示没有规则允许（默认拒绝），或有 deny 规则命中。
	DecisionForbidden Decision = "forbidden"
	// DecisionUnknown 表示**无法判定**（SubjectAccessReview 不可用 / API 故障）。
	// 调用方必须 fail-closed，但不得把它说成「被拒绝」。
	DecisionUnknown Decision = "unknown"
)

// Rule 是一条授权规则，字段语义与 Kubernetes RBAC 的 PolicyRule 相同。
//
//   - APIGroups：core 组写 ""；"*" 表示全部组；
//   - Resources：支持子资源 "pods/log"；子资源必须**显式**列出（只有 "pods" 的规则
//     不授予 "pods/log"，与 RBAC 一致）；"*" 表示全部资源；
//   - Verbs：get / list / create / update / patch / delete / deletecollection / "*"；
//   - ResourceNames：空 = 全部对象名；非空 = 仅这些对象名；
//   - Namespaces：空 = 全部命名空间；非空 = 仅这些命名空间（由 Role 的
//     metadata.namespace 填充）；
//   - Effect：""/"allow" = 允许；"deny" = 拒绝（只用于兼容旧的 Denied 写法，
//     真实 RBAC 没有 deny 规则，LoadRBACRules 永远不会产出 Effect="deny"）。
type Rule struct {
	APIGroups     []string `json:"apiGroups,omitempty" yaml:"apiGroups"`
	Resources     []string `json:"resources,omitempty" yaml:"resources"`
	Verbs         []string `json:"verbs,omitempty" yaml:"verbs"`
	ResourceNames []string `json:"resourceNames,omitempty" yaml:"resourceNames"`
	Namespaces    []string `json:"namespaces,omitempty" yaml:"namespaces"`
	Effect        string   `json:"effect,omitempty" yaml:"effect,omitempty"`
	// Source 标记规则来源："" = 显式/从 RBAC 载入的规则；"legacy" = 由历史字段合成。
	// 合成规则每次 Normalize 都重新生成，因此旧测试在 Load 之后修改
	// Permissions.Update 之类的开关仍然生效（否则「先归一化、再改开关」会被忽略）。
	Source string `json:"source,omitempty" yaml:"-"`
}

// ruleSourceLegacy 标记由历史字段（Get/Create/Update/Patch/Denied）合成的规则。
const ruleSourceLegacy = "legacy"

func (r Rule) denies() bool { return strings.EqualFold(r.Effect, "deny") }

// SplitResource 把 kubectl 里常见的资源写法归一为 (group, resource)。
//
//	"leases.coordination.k8s.io" → ("coordination.k8s.io", "leases")
//	"jobs.batch"                 → ("batch", "jobs")
//	"pods"                       → ("", "pods")
//	"pods/log"                   → ("", "pods/log")
//	"leases.coordination.k8s.io/surgecart-deploy-lock" → 同上，最后一个 "/" 之后是名字
func SplitResource(arg string) (group, resource string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", ""
	}
	// 子资源（"pods/log"）要保留：它决定 RBAC 的匹配项，不能丢掉。
	suffix := ""
	if slash := strings.Index(arg, "/"); slash >= 0 {
		arg, suffix = arg[:slash], arg[slash:]
	}
	if dot := strings.Index(arg, "."); dot > 0 {
		return arg[dot+1:], arg[:dot] + suffix
	}
	return "", arg + suffix
}

// splitSubresource 把 "pods/log" 拆成 ("pods", "log")。
func splitSubresource(resource string) (base, sub string) {
	if slash := strings.Index(resource, "/"); slash >= 0 {
		return resource[:slash], resource[slash+1:]
	}
	return resource, ""
}

// ruleMatches 判断规则是否覆盖该请求。name 为空表示「调用方没有限定对象名」。
func (r Rule) ruleMatches(verb, group, resource, namespace, name string) bool {
	if !containsToken(r.Verbs, verb) {
		return false
	}
	if !containsToken(r.APIGroups, group) {
		return false
	}
	if !matchResourceToken(r.Resources, resource) {
		return false
	}
	// 命名空间：规则为空 = 全部。Role 级规则（Namespaces 非空）**不能**授予集群级资源
	// （namespaces/nodes/selfsubjectaccessreviews…）：那需要 ClusterRole。
	if len(r.Namespaces) > 0 && isClusterScoped(resource) {
		return false
	}
	if len(r.Namespaces) > 0 {
		ok := false
		for _, ns := range r.Namespaces {
			if ns == namespace || ns == "*" {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.ResourceNames) > 0 {
		// 真实写请求总是带 name，此时严格匹配；can-i 不带名字（name==""）时，
		// 只要规则覆盖该 (verb, resource) 就算允许 —— 否则 resourceNames 限定的
		// 最小权限示例会让 `auth can-i get leases` 恒为 no，预检失去意义。
		if name != "" {
			ok := false
			for _, n := range r.ResourceNames {
				if n == name || n == "*" {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
	}
	return true
}

// isClusterScoped 判断资源是否集群级（只有 ClusterRole 能授权）。
//
// 只列本仓库用到的子集；未知资源按 namespaced 处理（保守：Role 规则仍然有效）。
func isClusterScoped(resource string) bool {
	switch resource {
	case "namespaces", "nodes", "persistentvolumes", "clusterroles", "clusterrolebindings",
		"selfsubjectaccessreviews", "selfsubjectrulesreviews", "subjectaccessreviews",
		"customresourcedefinitions", "storageclasses":
		return true
	default:
		return false
	}
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == "*" || token == want {
			return true
		}
	}
	return false
}

// matchResourceToken 支持 "*"、"pods"、"pods/log"、"pods/*"。
func matchResourceToken(tokens []string, resource string) bool {
	base, sub := splitSubresource(resource)
	for _, token := range tokens {
		switch {
		case token == "*", token == resource:
			return true
		case sub != "" && token == base+"/*":
			return true
		}
	}
	return false
}

// Authorize 是唯一的授权判定入口（默认拒绝）。
//
//   - State.AuthCanIError != "" → (DecisionUnknown, error)：无法判定；
//   - 任一条 Effect="deny" 的规则命中 → Forbidden；
//   - 否则某条 allow 规则命中 → Allowed；
//   - 否则 → Forbidden。
func Authorize(state *State, verb, group, resource, namespace, name string) (Decision, error) {
	if state == nil {
		return DecisionForbidden, nil
	}
	if state.AuthCanIError != "" {
		return DecisionUnknown, fmt.Errorf("%s", state.AuthCanIError)
	}
	// apply 不是 RBAC 动词：真实 kubectl apply = GET → 不存在 POST(create) /
	// 存在 PATCH(patch)。所以「能不能 apply」= create 或 patch 或 update 任一允许；
	// 真正的写权限在 apply 落盘时按对象是否存在用 create/patch 精确判定。
	if verb == "apply" {
		for _, candidate := range []string{"create", "patch", "update"} {
			if decision, _ := Authorize(state, candidate, group, resource, namespace, name); decision == DecisionAllowed {
				return DecisionAllowed, nil
			}
		}
		return DecisionForbidden, nil
	}
	permissions := state.Permissions
	permissions.Normalize()

	verb = normalizeVerb(verb)
	for _, rule := range permissions.Rules {
		if rule.denies() && rule.ruleMatches(verb, group, resource, namespace, name) {
			return DecisionForbidden, nil
		}
	}
	for _, rule := range permissions.Rules {
		if rule.denies() {
			continue
		}
		if rule.ruleMatches(verb, group, resource, namespace, name) {
			return DecisionAllowed, nil
		}
	}
	return DecisionForbidden, nil
}

// normalizeVerb 把 kubectl 的动词写成 RBAC 的动词。
func normalizeVerb(verb string) string {
	switch verb {
	case "get", "list", "create", "update", "patch", "delete", "deletecollection", "watch":
		return verb
	default:
		return verb
	}
}

// Permissions 的兼容层：把历史布尔开关与 Denied 翻译成规则。
//
// 幂等：重复调用不会重复追加规则（Denied 翻译后即清空）。
func (p *Permissions) Normalize() {
	// 1) 丢弃上一次由历史字段合成的规则：它们每次都要按当前开关重新生成。
	//    显式规则（含从 RBAC 载入的规则）原样保留，且一旦存在就不再合成 allow 规则 ——
	//    否则「载入真实 RBAC」会被默认的宽松开关悄悄放大成全员放行。
	kept := make([]Rule, 0, len(p.Rules))
	hasExplicit := false
	for _, rule := range p.Rules {
		if rule.Source == ruleSourceLegacy {
			continue
		}
		hasExplicit = true
		kept = append(kept, rule)
	}

	// 2) 历史布尔开关 → allow 规则。
	if !hasExplicit {
		var verbs []string
		if p.Get {
			verbs = append(verbs, "get", "list")
		}
		if p.Create {
			// 历史开关不区分「对象生命周期」的两个动词：Create 同时覆盖 delete。
			// （旧实现从不授权 delete，所以这里没有需要保持的旧语义。）
			verbs = append(verbs, "create", "delete")
		}
		if p.Update {
			verbs = append(verbs, "update")
		}
		if p.Patch {
			verbs = append(verbs, "patch")
		}
		if len(verbs) > 0 {
			kept = append(kept, Rule{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     verbs,
				Source:    ruleSourceLegacy,
			})
		}
	}

	// 3) 历史的按资源拒绝写法 → deny 规则（Denied 本身保留为来源，不在这里清空）。
	for _, denied := range p.Denied {
		verb, resource := splitDenied(denied)
		rule := Rule{
			APIGroups: []string{"*"},
			Verbs:     []string{verb},
			Effect:    "deny",
			Source:    ruleSourceLegacy,
		}
		if resource == "" {
			rule.Resources = []string{"*"}
		} else {
			rule.Resources = []string{resource}
		}
		kept = append(kept, rule)
	}
	p.Rules = kept
}

// splitDenied 解析 "<verb> <resource>"（历史格式）；只有 "<verb>" 时 resource 为空。
func splitDenied(entry string) (verb, resource string) {
	fields := strings.Fields(entry)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], fields[1]
	}
}

// rbacDocument 是 RBAC 清单里本实现关心的部分。
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
}

// RBACRulesFromYAML 解析多文档 YAML 中的 Role / ClusterRole，返回规则列表。
//
//   - Role：metadata.namespace 填入每条规则的 Namespaces（角色只在该命名空间生效）；
//   - ClusterRole：Namespaces 留空（全部命名空间）；
//   - 只读取 rules[]；RoleBinding / ServiceAccount 等文档被忽略。
func RBACRulesFromYAML(raw []byte) ([]Rule, error) {
	var rules []Rule
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc rbacDocument
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("解析 RBAC 清单: %w", err)
		}
		if doc.Kind != "Role" && doc.Kind != "ClusterRole" {
			continue
		}
		for _, rule := range doc.Rules {
			converted := Rule{
				APIGroups:     append([]string(nil), rule.APIGroups...),
				Resources:     append([]string(nil), rule.Resources...),
				Verbs:         append([]string(nil), rule.Verbs...),
				ResourceNames: append([]string(nil), rule.ResourceNames...),
			}
			if doc.Kind == "Role" && doc.Metadata.Namespace != "" {
				converted.Namespaces = []string{doc.Metadata.Namespace}
			}
			rules = append(rules, converted)
		}
	}
	return rules, nil
}

// LoadRBACRules 读取 RBAC 文件并解析成规则。
//
// 典型用法（把交付的授权清单喂给 fake，删除任一项 → 行为测试失败）：
//
//	rules, err := fakestate.LoadRBACRules("manifest/kubernetes/examples/deploy-lock/rbac.yaml")
//	if err != nil { ... }
//	f.updateState(func(st *fakestate.State) { st.Permissions.Rules = rules })
func LoadRBACRules(path string) ([]Rule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return RBACRulesFromYAML(raw)
}

// LoadRBACRulesFromPaths 合并多个 RBAC 文件（例如把授权拆成
// 「锁权限 / 工作负载权限 / 集群权限」三份物理文件）。
//
// 合并是**并集**：任一文件里删掉一条规则都会让对应 (verb, resource) 变成 no，
// 因此「删掉清单里任一条规则 → 行为测试失败」这条链路对多文件同样成立。
func LoadRBACRulesFromPaths(paths []string) ([]Rule, error) {
	var rules []Rule
	for _, path := range paths {
		loaded, err := LoadRBACRules(path)
		if err != nil {
			return nil, fmt.Errorf("读取 RBAC 清单 %s: %w", path, err)
		}
		rules = append(rules, loaded...)
	}
	return rules, nil
}

// LoadRBACRulesFromDir 读取目录下全部 *.yaml / *.yml（按文件名排序）并合并。
func LoadRBACRulesFromDir(dir string) ([]Rule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch {
		case strings.HasSuffix(entry.Name(), ".yaml"), strings.HasSuffix(entry.Name(), ".yml"):
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s 下没有 *.yaml/*.yml", dir)
	}
	sort.Strings(paths)
	return LoadRBACRulesFromPaths(paths)
}

// ManifestWriteRules 是「发布身份另外还需要 apply 部署清单」的显式授权。
//
// fake **不会**隐式放行 apply：deploy-lock 的 Role 只覆盖 leases/jobs/pods 的读取与
// 锁写入，用它跑完整发布会得到 403（这是真实集群里也会发生的事）。需要
// 「载入真实 RBAC + 跑完整发布」的测试必须显式追加这条规则，不能靠 fake 放水。
func ManifestWriteRules() []Rule {
	return []Rule{{
		APIGroups: []string{"", "apps", "batch"},
		Resources: []string{"namespaces", "configmaps", "secrets", "services", "deployments", "jobs"},
		Verbs:     []string{"get", "list", "create", "update", "patch"},
	}}
}
