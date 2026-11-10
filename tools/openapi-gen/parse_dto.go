package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

var apiImportRe = regexp.MustCompile(`^cnb\.cool/go-cloud-devops/my-shop/api/([^/]+)/v1$`)

// rawStruct 是解析阶段（未做类型解析）的结构体事实。
type rawStruct struct {
	Domain   string
	Name     string
	Doc      string
	File     string
	Line     int
	Stmt     *ast.StructType
	Imports  map[string]string // alias -> import path
	PkgTypes map[string]bool   // 本包内已声明的类型名
}

// parseDTOs 解析全部 api/*/v1/*.go，返回按 domain 分组的结构体事实。
func parseDTOs(repoRoot string) (map[string]*StructDef, []string, error) {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "api", "*", "v1", "*.go"))
	if err != nil {
		return nil, nil, err
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("未找到任何 api/*/v1/*.go")
	}

	var raw []*rawStruct
	var parseOrder []string
	fset := token.NewFileSet()

	for _, path := range paths {
		domain := domainFromAPIPath(path)
		if _, ok := domains[domain]; !ok {
			return nil, nil, fmt.Errorf("%s: 未登记的 API 业务域 %q", path, domain)
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}

		imports := map[string]string{}
		for _, spec := range f.Imports {
			p := strings.Trim(spec.Path.Value, `"`)
			alias := ""
			if spec.Name != nil {
				alias = spec.Name.Name
			} else {
				parts := strings.Split(p, "/")
				alias = parts[len(parts)-1]
			}
			imports[alias] = p
		}

		pkgTypes := map[string]bool{}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				pkgTypes[ts.Name.Name] = true
			}
		}

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				doc := ""
				if ts.Doc != nil {
					doc = normalizeDoc(ts.Doc.Text())
				} else if gd.Doc != nil {
					doc = normalizeDoc(gd.Doc.Text())
				}
				pos := fset.Position(ts.Pos())
				rel, _ := filepath.Rel(repoRoot, path)
				raw = append(raw, &rawStruct{
					Domain:   domain,
					Name:     ts.Name.Name,
					Doc:      doc,
					File:     rel,
					Line:     pos.Line,
					Stmt:     st,
					Imports:  imports,
					PkgTypes: pkgTypes,
				})
				parseOrder = append(parseOrder, domain+"."+ts.Name.Name)
			}
		}
	}

	// 先建立全部 Schema 名，供跨包引用使用。
	names := map[string]string{}
	for _, rs := range raw {
		name, err := schemaNameFor(rs.Domain, rs.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", rs.File, rs.Line, err)
		}
		names[rs.Domain+"."+rs.Name] = name
	}

	defs := map[string]*StructDef{}
	for _, rs := range raw {
		key := rs.Domain + "." + rs.Name
		def := &StructDef{
			Domain:     rs.Domain,
			Name:       rs.Name,
			SchemaName: names[key],
			Doc:        rs.Doc,
			Shape:      shapeFor(rs.Name),
			SourceFile: rs.File,
			Line:       rs.Line,
		}
		fields, meta, err := parseFields(rs, names)
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d %s: %w", rs.File, rs.Line, rs.Name, err)
		}
		def.Fields = fields
		def.Meta = meta
		defs[key] = def
	}
	return defs, parseOrder, nil
}

// shapeFor 判断结构体是请求还是响应形态。
// 仅 ActivitySkuInput 是请求侧嵌套类型，其余非 Req 类型按响应序列化语义处理。
func shapeFor(name string) string {
	if strings.HasSuffix(name, "Req") {
		return "request"
	}
	return "response"
}

func domainFromAPIPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "api" {
			return parts[i+1]
		}
	}
	return ""
}

func normalizeDoc(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, " ")
}

// parseFields 解析结构体字段，提取 g.Meta 元数据并保留嵌入关系。
func parseFields(rs *rawStruct, names map[string]string) ([]*Field, StructMeta, error) {
	var out []*Field
	var meta StructMeta
	for _, f := range rs.Stmt.Fields.List {
		tag := ""
		if f.Tag != nil {
			tag = strings.Trim(f.Tag.Value, "`")
		}
		st := reflect.StructTag(tag)

		tr, err := resolveType(rs, f.Type, names)
		if err != nil {
			return nil, meta, fmt.Errorf("字段解析失败: %w", err)
		}

		// g.Meta 不是业务 JSON 字段，只提取路由元数据。
		if tr.Kind == KindScalar && tr.Scalar == "meta" {
			meta = StructMeta{
				Path:    st.Get("path"),
				Method:  strings.ToUpper(st.Get("method")),
				Tags:    st.Get("tags"),
				Summary: st.Get("summary"),
			}
			continue
		}

		if len(f.Names) == 0 {
			// 匿名字段：仅支持业务结构体嵌入。
			if tr.Kind != KindRef {
				return nil, meta, fmt.Errorf("不支持的匿名字段类型（仅支持 g.Meta 与业务结构体嵌入）")
			}
			out = append(out, &Field{GoName: tr.Ref, Type: tr, Embedded: true})
			continue
		}

		jsonTag := st.Get("json")
		if jsonTag == "-" {
			continue
		}
		jsonName := strings.Split(jsonTag, ",")[0]
		if jsonName == "" {
			return nil, meta, fmt.Errorf("字段 %s 缺少 json 标签名", f.Names[0].Name)
		}
		if tr.Kind == KindScalar && tr.Scalar == "any" {
			return nil, meta, fmt.Errorf("字段 %s 为未支持的 any 类型，无法确认契约类型", f.Names[0].Name)
		}

		var v []string
		if vTag := st.Get("v"); vTag != "" {
			v = strings.Split(vTag, "|")
		}

		out = append(out, &Field{
			GoName:    f.Names[0].Name,
			JSONName:  jsonName,
			Dc:        st.Get("dc"),
			V:         v,
			In:        st.Get("in"),
			Default:   st.Get("d"),
			Type:      tr,
			OmitEmpty: strings.Contains(jsonTag, "omitempty"),
		})
	}
	return out, meta, nil
}

// resolveType 把 Go AST 类型表达式解析为 TypeRef。
func resolveType(rs *rawStruct, expr ast.Expr, names map[string]string) (*TypeRef, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		inner, err := resolveType(rs, t.X, names)
		if err != nil {
			return nil, err
		}
		inner.Pointer = true
		return inner, nil
	case *ast.ArrayType:
		if t.Len != nil {
			return nil, fmt.Errorf("不支持固定长度数组")
		}
		elem, err := resolveType(rs, t.Elt, names)
		if err != nil {
			return nil, err
		}
		return &TypeRef{Kind: KindArray, Elem: elem}, nil
	case *ast.Ident:
		switch t.Name {
		case "string", "bool", "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			return &TypeRef{Kind: KindScalar, Scalar: t.Name}, nil
		case "any", "interface":
			return &TypeRef{Kind: KindAny, Scalar: "any"}, nil
		}
		if rs.PkgTypes[t.Name] {
			key := rs.Domain + "." + t.Name
			name, ok := names[key]
			if !ok {
				return nil, fmt.Errorf("同包类型 %s 未登记 Schema 名", t.Name)
			}
			return &TypeRef{Kind: KindRef, Ref: name}, nil
		}
		return nil, fmt.Errorf("未知的标识符类型 %q", t.Name)
	case *ast.SelectorExpr:
		x, ok := t.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("不支持的选择器类型")
		}
		importPath, ok := rs.Imports[x.Name]
		if !ok {
			return nil, fmt.Errorf("未知的包别名 %q", x.Name)
		}
		switch {
		case importPath == "github.com/gogf/gf/v2/os/gtime" && t.Sel.Name == "Time":
			return &TypeRef{Kind: KindScalar, Scalar: "gtime"}, nil
		case importPath == "time" && t.Sel.Name == "Time":
			return &TypeRef{Kind: KindScalar, Scalar: "time"}, nil
		case importPath == "encoding/json" && t.Sel.Name == "Number":
			return &TypeRef{Kind: KindScalar, Scalar: "jsonnumber"}, nil
		case importPath == "github.com/gogf/gf/v2/frame/g" && t.Sel.Name == "Meta":
			return &TypeRef{Kind: KindScalar, Scalar: "meta"}, nil
		}
		m := apiImportRe.FindStringSubmatch(importPath)
		if m == nil {
			return nil, fmt.Errorf("不支持的跨包类型 %s.%s（%s）", x.Name, t.Sel.Name, importPath)
		}
		key := m[1] + "." + t.Sel.Name
		name, ok := names[key]
		if !ok {
			return nil, fmt.Errorf("跨包类型 %s 未登记 Schema 名", key)
		}
		return &TypeRef{Kind: KindRef, Ref: name}, nil
	case *ast.MapType:
		return nil, fmt.Errorf("不支持 map 类型作为契约字段")
	case *ast.InterfaceType:
		return &TypeRef{Kind: KindAny, Scalar: "any"}, nil
	default:
		return nil, fmt.Errorf("不支持的 Go 类型表达式 %T", expr)
	}
}
