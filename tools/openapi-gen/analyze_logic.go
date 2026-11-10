package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// codeGraph 记录 internal/logic/<domain> 的函数级错误码引用与同包调用关系。
// 用它把错误码归集到 operation（handler → service 方法 → logic 函数），
// 而不是笼统地给整个业务域挂全量错误码。
type codeGraph struct {
	// funcCodes[domain][func] = 该函数体内直接引用的 codes.CodeXxx
	funcCodes map[string]map[string]map[string]bool
	// calls[domain][func] = 该函数体调用的同包函数名
	calls map[string]map[string]map[string]bool
	// all[domain] = 该域所有函数引用的并集（fallback）
	all map[string]map[string]bool
}

// analyzeLogic 解析 internal/logic/<domain>/*.go 的函数体。
func analyzeLogic(repoRoot string) (*codeGraph, error) {
	g := &codeGraph{
		funcCodes: map[string]map[string]map[string]bool{},
		calls:     map[string]map[string]map[string]bool{},
		all:       map[string]map[string]bool{},
	}
	dirs, err := filepath.Glob(filepath.Join(repoRoot, "internal", "logic", "*"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return nil, err
		}
		domain := filepath.Base(dir)
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			codesAliases := map[string]bool{}
			for _, spec := range f.Imports {
				p := strings.Trim(spec.Path.Value, `"`)
				if strings.HasSuffix(p, "/internal/codes") {
					alias := "codes"
					if spec.Name != nil {
						alias = spec.Name.Name
					}
					codesAliases[alias] = true
				}
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				name := fn.Name.Name
				if g.funcCodes[domain] == nil {
					g.funcCodes[domain] = map[string]map[string]bool{}
					g.calls[domain] = map[string]map[string]bool{}
					g.all[domain] = map[string]bool{}
				}
				if g.funcCodes[domain][name] == nil {
					g.funcCodes[domain][name] = map[string]bool{}
					g.calls[domain][name] = map[string]bool{}
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch t := n.(type) {
					case *ast.SelectorExpr:
						if x, ok := t.X.(*ast.Ident); ok && codesAliases[x.Name] &&
							strings.HasPrefix(t.Sel.Name, "Code") {
							g.funcCodes[domain][name][t.Sel.Name] = true
							g.all[domain][t.Sel.Name] = true
						}
						// 同包方法调用：s.foo() 形式。
						if _, ok := t.X.(*ast.Ident); ok {
							g.calls[domain][name][t.Sel.Name] = true
						}
					case *ast.CallExpr:
						// 同包函数调用：foo() 形式。
						if id, ok := t.Fun.(*ast.Ident); ok {
							g.calls[domain][name][id.Name] = true
						}
					}
					return true
				})
			}
		}
	}
	return g, nil
}

// constantsFor 按入口函数名收集可达函数的错误码常量（BFS，深度有界）。
func (g *codeGraph) constantsFor(domain string, entries []string) map[string]bool {
	out := map[string]bool{}
	funcs, ok := g.funcCodes[domain]
	if !ok {
		return out
	}
	queue := append([]string{}, entries...)
	visited := map[string]bool{}
	depth := 0
	for len(queue) > 0 && depth < 6 {
		next := make([]string, 0, len(queue))
		for _, fn := range queue {
			if fn == "" || visited[fn] {
				continue
			}
			visited[fn] = true
			if _, exists := funcs[fn]; !exists {
				continue
			}
			for c := range funcs[fn] {
				out[c] = true
			}
			for callee := range g.calls[domain][fn] {
				if !visited[callee] {
					next = append(next, callee)
				}
			}
		}
		queue = next
		depth++
	}
	return out
}

// allConstants 返回该业务域引用的全部错误码（找不到函数级入口时的 fallback）。
func (g *codeGraph) allConstants(domain string) map[string]bool {
	return g.all[domain]
}

// ConstantsFor 保留按域全量查询（供 contract-check 的跨域复用统计使用）。
func (g *codeGraph) ConstantsFor(domain string) []string {
	m := g.all[domain]
	if m == nil {
		return nil
	}
	return sortedKeys(m)
}
