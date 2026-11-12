package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
}

// controllerMethod 记录 controller 方法签名与函数体事实。
type controllerMethod struct {
	ReqType string
	ResType string
	// ServiceCalls 是函数体内 service.X().Method(...) 形式的被调用方法名，
	// 用于把错误码精确归集到 logic 函数。
	ServiceCalls []string
	// Codes 是函数体内直接引用的业务错误码常量。
	Codes []string
}

// parseControllers 解析 internal/controller/<domain>/*.go，
// 建立 (domain, method) -> (Req, Res) 的映射。
// 这是 DTO 与路由关联的唯一事实来源，避免用 handler 名猜测响应类型。
func parseControllers(repoRoot string) (map[string]controllerMethod, error) {
	out := map[string]controllerMethod{}
	dirs, err := filepath.Glob(filepath.Join(repoRoot, "internal", "controller", "*"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		info, statErr := statDir(dir)
		if statErr != nil || !info {
			continue
		}
		domain := filepath.Base(dir)
		if _, ok := domains[domain]; !ok {
			continue
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return nil, err
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			imports := map[string]string{}
			for _, spec := range f.Imports {
				p := strings.Trim(spec.Path.Value, `"`)
				alias := p[strings.LastIndex(p, "/")+1:]
				if spec.Name != nil {
					alias = spec.Name.Name
				}
				imports[alias] = p
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Type.Params == nil || fn.Type.Results == nil {
					continue
				}
				reqType := selectorTypeName(fn.Type.Params, imports)
				resType := selectorTypeName(fn.Type.Results, imports)
				if reqType == "" || resType == "" {
					continue
				}
				key := domain + "." + fn.Name.Name
				calls, bodyCodes := collectControllerBody(fn, imports)
				if prev, ok := out[key]; ok && (prev.ReqType != reqType || prev.ResType != resType) {
					return nil, fmt.Errorf("%s: controller 方法签名冲突（%s vs %s）", key, prev.ReqType, reqType)
				}
				out[key] = controllerMethod{
					ReqType:      reqType,
					ResType:      resType,
					ServiceCalls: calls,
					Codes:        bodyCodes,
				}
			}
		}
	}
	return out, nil
}

// collectControllerBody 提取 service 方法调用名与函数体内直接引用的错误码。
func collectControllerBody(fn *ast.FuncDecl, imports map[string]string) (calls, bodyCodes []string) {
	if fn.Body == nil {
		return nil, nil
	}
	seenCall := map[string]bool{}
	seenCode := map[string]bool{}
	codesAliases := map[string]bool{}
	for alias, p := range imports {
		if strings.HasSuffix(p, "/internal/codes") {
			codesAliases[alias] = true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// service.X().Method(...) 形式。
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if innerSel, ok := inner.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := innerSel.X.(*ast.Ident); ok && pkg.Name == "service" {
					if !seenCall[sel.Sel.Name] {
						seenCall[sel.Sel.Name] = true
						calls = append(calls, sel.Sel.Name)
					}
				}
			}
		}
		// codes.CodeXxx 引用。
		if x, ok := sel.X.(*ast.Ident); ok && codesAliases[x.Name] && strings.HasPrefix(sel.Sel.Name, "Code") {
			if !seenCode[sel.Sel.Name] {
				seenCode[sel.Sel.Name] = true
				bodyCodes = append(bodyCodes, sel.Sel.Name)
			}
		}
		return true
	})
	return calls, bodyCodes
}

func statDir(path string) (bool, error) {
	entries, err := filepath.Glob(filepath.Join(path, "*.go"))
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// selectorTypeName 从字段列表中提取形如 *v1.XxxReq 的类型名。
func selectorTypeName(fields *ast.FieldList, imports map[string]string) string {
	for _, f := range fields.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		p := imports[x.Name]
		if apiImportRe.MatchString(p) {
			return sel.Sel.Name
		}
	}
	return ""
}

// parseRouteFiles 解析两个路由注册文件，返回按注册顺序排列的 API 事实。
//
// 认证语义必须来自路由组的中间件（Auth / AdminAuth / *SignatureOnly），
// 不能凭变量名猜测：routes_frontend.go 中的登出组变量名为 user，但挂的是 AuthSignatureOnly。
func parseRouteFiles(repoRoot string) ([]*API, error) {
	files := []string{
		filepath.Join(repoRoot, "internal", "cmd", "routes_frontend.go"),
		filepath.Join(repoRoot, "internal", "cmd", "routes_admin.go"),
	}
	var out []*API
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		imports := map[string]string{}
		for _, spec := range f.Imports {
			p := strings.Trim(spec.Path.Value, `"`)
			alias := p[strings.LastIndex(p, "/")+1:]
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = p
		}

		// controller 变量 -> API 业务域。
		ctrlDomain := map[string]string{}
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			lhs, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			domain := filepath.Base(imports[pkg.Name])
			if _, ok := domains[domain]; ok {
				ctrlDomain[lhs.Name] = domain
			}
			return true
		})

		var parseErr error
		// scope：组变量名 -> 该组挂载的认证中间件名。
		visitBlock := func(block *ast.BlockStmt, scope map[string]string) {}
		visitBlock = func(block *ast.BlockStmt, scope map[string]string) {
			if block == nil || parseErr != nil {
				return
			}
			for _, stmt := range block.List {
				if parseErr != nil {
					return
				}
				call := callOfStmt(stmt)
				if call == nil {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if sel.Sel.Name == "Group" {
					if len(call.Args) != 2 {
						continue
					}
					fl, ok := call.Args[1].(*ast.FuncLit)
					if !ok || fl.Type.Params == nil || len(fl.Type.Params.List) == 0 {
						continue
					}
					paramField := fl.Type.Params.List[0]
					if len(paramField.Names) != 1 {
						continue
					}
					param := paramField.Names[0].Name
					child := map[string]string{}
					for k, v := range scope {
						child[k] = v
					}
					if mw := groupMiddleware(fl.Body, param); mw != "" {
						child[param] = mw
					}
					visitBlock(fl.Body, child)
					continue
				}

				method := strings.ToUpper(sel.Sel.Name)
				if !httpMethods[method] {
					continue
				}
				if len(call.Args) != 2 {
					parseErr = fmt.Errorf("%s: %s 调用的参数数量不是 2", fset.Position(call.Pos()), method)
					return
				}
				pathLit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || pathLit.Kind != token.STRING {
					parseErr = fmt.Errorf("%s: %s 的路径不是字符串字面量", fset.Position(call.Pos()), method)
					return
				}
				routePath := strings.Trim(pathLit.Value, `"`)

				handlerSel, ok := call.Args[1].(*ast.SelectorExpr)
				if !ok {
					parseErr = fmt.Errorf("%s: %s %s 的 handler 不是选择器表达式",
						fset.Position(call.Pos()), method, routePath)
					return
				}
				ctrlVar, ok := handlerSel.X.(*ast.Ident)
				if !ok {
					parseErr = fmt.Errorf("%s: %s %s 的 handler 接收者不是标识符",
						fset.Position(call.Pos()), method, routePath)
					return
				}
				domain, ok := ctrlDomain[ctrlVar.Name]
				if !ok {
					parseErr = fmt.Errorf("%s: 无法解析 handler 接收者 %q 的业务域",
						fset.Position(call.Pos()), ctrlVar.Name)
					return
				}

				auth, mode, perm, err := routeSecurity(sel.X, scope)
				if err != nil {
					parseErr = fmt.Errorf("%s: %s %s: %w", fset.Position(call.Pos()), method, routePath, err)
					return
				}
				out = append(out, &API{
					Method:     method,
					Path:       routePath,
					Handler:    handlerSel.Sel.Name,
					Domain:     domain,
					Auth:       auth,
					AuthMode:   mode,
					Permission: perm,
					Source:     filepath.Base(path),
				})
			}
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Register") {
				continue
			}
			visitBlock(fn.Body, map[string]string{})
		}
		if parseErr != nil {
			return nil, parseErr
		}
	}
	return out, nil
}

// callOfStmt 提取语句中的调用表达式（表达式语句或单值赋值）。
func callOfStmt(stmt ast.Stmt) *ast.CallExpr {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		c, _ := s.X.(*ast.CallExpr)
		return c
	case *ast.AssignStmt:
		if len(s.Rhs) == 1 {
			c, _ := s.Rhs[0].(*ast.CallExpr)
			return c
		}
	}
	return nil
}

// groupMiddleware 读取路由组函数体顶层挂载的认证中间件名。
func groupMiddleware(body *ast.BlockStmt, param string) string {
	for _, stmt := range body.List {
		call := callOfStmt(stmt)
		if call == nil {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Middleware" {
			continue
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != param {
			continue
		}
		for _, arg := range call.Args {
			if as, ok := arg.(*ast.SelectorExpr); ok {
				return as.Sel.Name
			}
		}
	}
	return ""
}

// routeSecurity 从路由组表达式与作用域推导认证/权限语义。
// 无法识别时报错，避免静默产出错误的认证描述。
func routeSecurity(x ast.Expr, scope map[string]string) (auth, mode, permission string, err error) {
	switch v := x.(type) {
	case *ast.Ident:
		switch scope[v.Name] {
		case "Auth":
			return "user", "", "", nil
		case "AuthSignatureOnly":
			return "user", "signature_only", "", nil
		case "AdminAuth":
			return "admin", "", "", nil
		case "AdminAuthSignatureOnly":
			return "admin", "signature_only", "", nil
		}
		switch v.Name {
		case "root":
			return "public", "", "", nil
		case "user":
			return "user", "", "", nil
		case "admin":
			return "admin", "", "", nil
		case "sig":
			return "admin", "signature_only", "", nil
		default:
			return "", "", "", fmt.Errorf("无法识别的路由组 %q（未挂载已知认证中间件）", v.Name)
		}
	case *ast.CallExpr:
		id, ok := v.Fun.(*ast.Ident)
		if !ok || id.Name != "require" || len(v.Args) != 1 {
			return "", "", "", fmt.Errorf("无法识别的权限组表达式")
		}
		lit, ok := v.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", "", "", fmt.Errorf("require 的权限码不是字符串字面量")
		}
		code := strings.Trim(lit.Value, `"`)
		if code == "" {
			return "", "", "", fmt.Errorf("require 的权限码为空")
		}
		return "admin", "", code, nil
	}
	return "", "", "", fmt.Errorf("无法识别的路由组表达式 %T", x)
}

// parseBoundControllers 识别 root.Bind(X.NewV1()) 形式的自动绑定路由（当前仅 health）。
// GoFrame 会按 DTO 的 g.Meta path/method 自动注册，因此这里只需返回业务域。
func parseBoundControllers(repoRoot string) ([]string, error) {
	path := filepath.Join(repoRoot, "internal", "cmd", "cmd.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	imports := map[string]string{}
	for _, spec := range f.Imports {
		p := strings.Trim(spec.Path.Value, `"`)
		alias := p[strings.LastIndex(p, "/")+1:]
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		imports[alias] = p
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Bind" || len(call.Args) != 1 {
			return true
		}
		inner, ok := call.Args[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		innerSel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := innerSel.X.(*ast.Ident)
		if !ok {
			return true
		}
		domain := filepath.Base(imports[pkg.Name])
		if _, ok := domains[domain]; ok {
			out = append(out, domain)
		}
		return true
	})
	return out, nil
}
