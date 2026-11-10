package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// componentNode 唯一标识一个组件：组件类型 + 组件名。
// 例如 {schemas, Product} 对应 #/components/schemas/Product。
type componentNode struct {
	Type string
	Name string
}

func (n componentNode) String() string { return n.Type + "/" + n.Name }

// retainedComponent 是允许保留的「未被引用组件」条目。
// 必须逐项列出类型 + 名称 + 理由，禁止按类型通配。
type retainedComponent struct {
	Type   string
	Name   string
	Reason string
}

// unusedAllowlistRelPath 是未引用组件允许保留清单的仓库相对路径。
// 清单独立成文件而不是写死在 Go 里，便于逐条 review 与追踪变更。
const unusedAllowlistRelPath = "tools/openapi-gen/unused-components-allowlist.yaml"

// allowlistFile 是清单文件的结构：按「类型 + 理由」分组，组内逐个列名。
// 分组只承载理由，不承载通配语义：每个名字都必须显式出现。
type allowlistFile struct {
	Version int              `yaml:"version"`
	Groups  []allowlistGroup `yaml:"groups"`
}

type allowlistGroup struct {
	Type     string   `yaml:"type"`
	Category string   `yaml:"category"`
	Reason   string   `yaml:"reason"`
	Names    []string `yaml:"names"`
}

// loadUnusedAllowlist 读取并展开允许保留清单。
func loadUnusedAllowlist(repoRoot string) ([]retainedComponent, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, unusedAllowlistRelPath))
	if err != nil {
		return nil, fmt.Errorf("读取未引用组件白名单失败: %w", err)
	}
	var f allowlistFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", unusedAllowlistRelPath, err)
	}
	return expandAllowlist(&f)
}

// validateAllowlistStruct 校验清单结构；非法结构直接拒绝，避免"通配式放行"。
func validateAllowlistStruct(f *allowlistFile) error {
	if f.Version != 1 {
		return fmt.Errorf("%s: 不支持的 version=%d", unusedAllowlistRelPath, f.Version)
	}
	if len(f.Groups) == 0 {
		return fmt.Errorf("%s: groups 为空", unusedAllowlistRelPath)
	}
	for _, g := range f.Groups {
		if strings.TrimSpace(g.Type) == "" || strings.TrimSpace(g.Reason) == "" {
			return fmt.Errorf("%s: 分组 %q 缺少 type 或 reason", unusedAllowlistRelPath, g.Category)
		}
		if len(g.Names) == 0 {
			return fmt.Errorf("%s: 分组 %q 未列名（禁止空分组通配）", unusedAllowlistRelPath, g.Category)
		}
		for _, n := range g.Names {
			name := strings.TrimSpace(n)
			if name == "" || strings.ContainsAny(name, "*?/") {
				return fmt.Errorf("%s: 分组 %q 含非法名称 %q（不允许通配）", unusedAllowlistRelPath, g.Category, n)
			}
		}
	}
	return nil
}

// expandAllowlist 校验并展开分组为逐组件条目。
func expandAllowlist(f *allowlistFile) ([]retainedComponent, error) {
	if err := validateAllowlistStruct(f); err != nil {
		return nil, err
	}
	var out []retainedComponent
	for _, g := range f.Groups {
		for _, n := range g.Names {
			out = append(out, retainedComponent{Type: g.Type, Name: strings.TrimSpace(n), Reason: g.Reason})
		}
	}
	return out, nil
}

// componentGraph 保存组件声明、定义与引用边。
type componentGraph struct {
	declared   map[componentNode]bool
	defs       map[componentNode]any
	defFile    map[componentNode]string
	typeOfFile map[string]string
	edges      map[componentNode][]componentNode
	roots      []componentNode
}

// buildComponentGraph 从主文档与组件文件构建引用图。
// componentDocs 以组件目录内的文件名（如 schemas.yaml）为键。
func buildComponentGraph(doc map[string]any, componentDocs map[string]map[string]any) (*componentGraph, error) {
	comp, _ := doc["components"].(map[string]any)
	if comp == nil {
		return nil, fmt.Errorf("主文档缺少 components")
	}
	g := &componentGraph{
		declared:   map[componentNode]bool{},
		defs:       map[componentNode]any{},
		defFile:    map[componentNode]string{},
		typeOfFile: map[string]string{},
		edges:      map[componentNode][]componentNode{},
	}

	for typ, namesAny := range comp {
		names, ok := namesAny.(map[string]any)
		if !ok {
			continue
		}
		for name, raw := range names {
			node := componentNode{Type: typ, Name: name}
			g.declared[node] = true
			// 主文档中的组件通常是 { $ref: ./components/<file>#/<name> } 的占位。
			def := raw
			file := ""
			if m, ok := raw.(map[string]any); ok {
				if ref := asString(m["$ref"]); ref != "" {
					targetFile, fragment := splitRef(ref)
					if base := path.Base(targetFile); base != "" && base != "." {
						g.typeOfFile[base] = typ
						if fileDoc, ok := componentDocs[base]; ok {
							if v, err := resolveJSONPointer(fileDoc, fragment); err == nil {
								def = v
								file = base
							}
						}
					}
				}
			}
			g.defs[node] = def
			g.defFile[node] = file
		}
	}

	// 引用边：组件定义内部的 $ref。
	for node, def := range g.defs {
		seen := map[componentNode]bool{}
		walkComponentValue(def, func(ref string) {
			if target, ok := g.parseRef(ref, node); ok && !seen[target] {
				seen[target] = true
				g.edges[node] = append(g.edges[node], target)
			}
		}, func(scheme string) {
			target := componentNode{Type: "securitySchemes", Name: scheme}
			if g.declared[target] && !seen[target] {
				seen[target] = true
				g.edges[node] = append(g.edges[node], target)
			}
		})
	}

	// 根节点：paths 与主文档中除 components 之外的扩展字段。
	seenRoot := map[componentNode]bool{}
	addRoot := func(n componentNode) {
		if !g.declared[n] || seenRoot[n] {
			return
		}
		seenRoot[n] = true
		g.roots = append(g.roots, n)
	}
	for key, v := range doc {
		if key == "components" {
			continue
		}
		walkComponentValue(v, func(ref string) {
			if target, ok := g.parseRef(ref, componentNode{}); ok {
				addRoot(target)
			}
		}, func(scheme string) {
			addRoot(componentNode{Type: "securitySchemes", Name: scheme})
		})
	}
	sort.Slice(g.roots, func(i, j int) bool { return g.roots[i].String() < g.roots[j].String() })
	return g, nil
}

// parseRef 把 $ref 解析成组件节点。
// 支持三种写法：
//   - #/components/<type>/<name>（主文档内引用）
//   - #/<name>（组件文件内的本地引用，类型由 owner 所在文件决定）
//   - <file>#/<name> 或 ./components/<file>#/<name>（跨组件文件引用）
func (g *componentGraph) parseRef(ref string, owner componentNode) (componentNode, bool) {
	file, fragment := splitRef(ref)
	if file == "" {
		if strings.HasPrefix(fragment, "/components/") {
			parts := strings.Split(strings.TrimPrefix(fragment, "/components/"), "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return componentNode{}, false
			}
			return componentNode{Type: parts[0], Name: parts[1]}, true
		}
		name := strings.TrimPrefix(fragment, "/")
		if name == "" || strings.Contains(name, "/") {
			return componentNode{}, false
		}
		// 组件文件内本地引用：#/Name，类型由 owner 定义所在文件决定。
		if f := g.defFile[owner]; f != "" {
			if typ, ok := g.typeOfFile[f]; ok {
				return componentNode{Type: typ, Name: name}, true
			}
		}
		return componentNode{}, false
	}
	name := strings.TrimPrefix(fragment, "/")
	if name == "" || strings.Contains(name, "/") {
		return componentNode{}, false
	}
	typ, ok := g.typeOfFile[path.Base(file)]
	if !ok {
		return componentNode{}, false
	}
	return componentNode{Type: typ, Name: name}, true
}

// reachable 返回从根节点出发可达的组件集合（含递归嵌套与 security requirement）。
func (g *componentGraph) reachable() map[componentNode]bool {
	out := map[componentNode]bool{}
	queue := append([]componentNode{}, g.roots...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if out[n] {
			continue
		}
		out[n] = true
		queue = append(queue, g.edges[n]...)
	}
	return out
}

// unused 返回声明了但不可达的组件（按类型、名称排序）。
func (g *componentGraph) unused() []componentNode {
	reach := g.reachable()
	var out []componentNode
	for n := range g.declared {
		if !reach[n] {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// validateComponentUsage 校验未引用组件与允许保留清单的一致性。
// 返回问题列表（空表示通过）与允许保留且确实未引用的条目。
func validateComponentUsage(g *componentGraph, allow []retainedComponent) (problems []string, retained []string) {
	reach := g.reachable()
	allowSet := map[componentNode]retainedComponent{}
	for _, a := range allow {
		n := componentNode{Type: a.Type, Name: a.Name}
		if strings.TrimSpace(a.Reason) == "" {
			problems = append(problems, fmt.Sprintf("允许保留清单项 %s 缺少保留理由", n))
			continue
		}
		if prev, ok := allowSet[n]; ok {
			problems = append(problems, fmt.Sprintf("允许保留清单重复登记 %s（已有理由：%s）", n, prev.Reason))
			continue
		}
		allowSet[n] = a
		if !g.declared[n] {
			problems = append(problems, fmt.Sprintf("允许保留清单项 %s 已不存在于 components（过期记录，请删除）", n))
			continue
		}
		if reach[n] {
			problems = append(problems, fmt.Sprintf("允许保留清单项 %s 实际已被引用（应从清单移除）", n))
		}
	}

	for _, n := range g.unused() {
		if a, ok := allowSet[n]; ok {
			retained = append(retained, fmt.Sprintf("%s: %s", n, a.Reason))
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"组件 %s 未被 paths / 其他组件 / securitySchemes 引用，且不在允许保留清单中"+
				"（若确需保留，请在 retainedUnusedComponents 中逐项登记理由）", n))
	}
	return problems, retained
}

// checkUnusedComponents 读取 openapi/ 下的主文档与组件文件，执行未引用组件检查。
func checkUnusedComponents(repoRoot string, doc map[string]any) ([]string, []string, error) {
	componentDocs, err := loadComponentDocs(filepath.Join(repoRoot, "openapi"))
	if err != nil {
		return nil, nil, err
	}
	g, err := buildComponentGraph(doc, componentDocs)
	if err != nil {
		return nil, nil, err
	}
	allow, err := loadUnusedAllowlist(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	problems, retained := validateComponentUsage(g, allow)
	return problems, retained, nil
}

// loadComponentDocs 读取 openapi/components/*.yaml，键为文件名。
func loadComponentDocs(openapiDir string) (map[string]map[string]any, error) {
	componentDocs := map[string]map[string]any{}
	files, err := filepath.Glob(filepath.Join(openapiDir, "components", "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := readYAML(f)
		if err != nil {
			return nil, err
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		componentDocs[filepath.Base(f)] = m
	}
	return componentDocs, nil
}

// walkComponentValue 递归遍历 YAML 值，收集 $ref 与 security requirement。
func walkComponentValue(v any, onRef func(string), onSecurity func(string)) {
	switch t := v.(type) {
	case map[string]any:
		if ref := asString(t["$ref"]); ref != "" {
			onRef(ref)
		}
		for k, vv := range t {
			if k == "security" {
				for _, req := range asSlice(vv) {
					m, ok := req.(map[string]any)
					if !ok {
						continue
					}
					for scheme := range m {
						onSecurity(scheme)
					}
				}
				continue
			}
			walkComponentValue(vv, onRef, onSecurity)
		}
	case []any:
		for _, vv := range t {
			walkComponentValue(vv, onRef, onSecurity)
		}
	}
}

// splitRef 拆分 $ref 为「文件部分」与「fragment 部分」（不含 #）。
func splitRef(ref string) (file, fragment string) {
	if i := strings.Index(ref, "#"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}
