package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrorCode 是 internal/codes/codes.go 中的一个业务错误码事实。
type ErrorCode struct {
	Code     int
	Constant string
	HTTP     int
	Message  string
	Domain   string
}

// httpStatusValues 是 net/http 状态常量到数值的映射（只登记 codes.go 实际使用的常量）。
var httpStatusValues = map[string]int{
	"StatusOK":                  200,
	"StatusBadRequest":          400,
	"StatusUnauthorized":        401,
	"StatusForbidden":           403,
	"StatusNotFound":            404,
	"StatusConflict":            409,
	"StatusTooManyRequests":     429,
	"StatusInternalServerError": 500,
	"StatusServiceUnavailable":  503,
}

// domainByConstantPrefix 按最长前缀把常量归入业务域。
// 顺序敏感：长前缀必须排在短前缀之前。
var domainByConstantPrefix = []struct {
	Prefix string
	Domain string
}{
	{"CodeFlashSale", "flashsale"},
	{"CodeRecommend", "recommendation"},
	{"CodePermission", "iam"},
	{"CodeSuperAdmin", "iam"},
	{"CodeSelfOperation", "iam"},
	{"CodeRefreshToken", "iam"},
	{"CodeInvalidCredentials", "iam"},
	{"CodeUsernameExists", "iam"},
	{"CodeSessionNotFound", "iam"},
	{"CodeUserNotFound", "iam"},
	{"CodeAdmin", "iam"},
	{"CodeRole", "iam"},
	{"CodeCategory", "category"},
	{"CodeProduct", "product"},
	{"CodeSku", "sku"},
	{"CodeInventory", "inventory"},
	{"CodeAddress", "address"},
	{"CodeCart", "cart"},
	{"CodeOrder", "order"},
	{"CodeReview", "review"},
	{"CodeFavorite", "favorite"},
	{"CodeLike", "like"},
	{"CodeBanner", "banner"},
	{"CodeArticle", "article"},
	{"CodeUpload", "upload"},
	{"CodeRefund", "refund"},
	{"CodeCoupon", "coupon"},
	{"CodeInternalError", "common"},
	{"CodeInvalidArgument", "common"},
	{"CodeUnauthorized", "common"},
	{"CodeForbidden", "common"},
	{"CodeNotFound", "common"},
	{"CodeServiceUnavailable", "common"},
	{"CodeOK", "common"},
}

func domainOfConstant(name string) string {
	for _, p := range domainByConstantPrefix {
		if strings.HasPrefix(name, p.Prefix) {
			return p.Domain
		}
	}
	return "unknown"
}

// parseErrorCodes 从 internal/codes/codes.go 提取错误码全集与码表映射。
// codes.go 是错误码的唯一事实源；本函数不做任何补全或推测。
func parseErrorCodes(repoRoot string) ([]*ErrorCode, error) {
	path := filepath.Join(repoRoot, "internal", "codes", "codes.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	constValues := map[string]int{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					continue
				}
				n, err := strconv.Atoi(lit.Value)
				if err != nil {
					return nil, fmt.Errorf("%s: 常量 %s 的值 %q 不是整数", path, name.Name, lit.Value)
				}
				constValues[name.Name] = n
			}
		}
	}

	table := map[string]struct {
		http int
		msg  string
	}{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "codeTable" || i >= len(vs.Values) {
					continue
				}
				cl, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					valLit, ok := kv.Value.(*ast.CompositeLit)
					if !ok || len(valLit.Elts) != 2 {
						continue
					}
					httpSel, ok := valLit.Elts[0].(*ast.SelectorExpr)
					if !ok {
						continue
					}
					httpCode, ok := httpStatusValues[httpSel.Sel.Name]
					if !ok {
						return nil, fmt.Errorf("%s: 未登记的 HTTP 状态常量 %s", path, httpSel.Sel.Name)
					}
					msgLit, ok := valLit.Elts[1].(*ast.BasicLit)
					if !ok || msgLit.Kind != token.STRING {
						continue
					}
					msg, err := strconv.Unquote(msgLit.Value)
					if err != nil {
						return nil, fmt.Errorf("%s: message 解析失败: %w", path, err)
					}
					table[key.Name] = struct {
						http int
						msg  string
					}{httpCode, msg}
				}
			}
		}
	}

	var out []*ErrorCode
	seen := map[int]string{}
	for _, name := range sortedKeys(constValues) {
		info, ok := table[name]
		if !ok {
			return nil, fmt.Errorf("%s: 常量 %s 缺少码表映射（禁止推测补全）", path, name)
		}
		value := constValues[name]
		if prev, ok := seen[value]; ok {
			return nil, fmt.Errorf("%s: 错误码 %d 重复定义（%s 与 %s）", path, value, prev, name)
		}
		seen[value] = name
		out = append(out, &ErrorCode{
			Code:     value,
			Constant: name,
			HTTP:     info.http,
			Message:  info.msg,
			Domain:   domainOfConstant(name),
		})
	}
	return out, nil
}

// buildErrorCodeRegistry 生成 components/error-codes.yaml 的结构。
func buildErrorCodeRegistry(list []*ErrorCode, crossDomain map[int][]string) *OMap {
	codes := make([]any, 0, len(list))
	for _, c := range list {
		item := NewOMap().
			Set("code", c.Code).
			Set("constant", c.Constant).
			Set("http_status", c.HTTP).
			Set("message", c.Message).
			Set("domain", c.Domain)
		if users, ok := crossDomain[c.Code]; ok && len(users) > 0 {
			item.Set("reused_by_domains", toAnySlice(users))
		}
		codes = append(codes, item)
	}
	return NewOMap().
		Set("source", "internal/codes/codes.go").
		Set("note", "错误码、HTTP 状态与 message 均直接来自 codes.go；未做任何补全或推测。").
		Set("codes", codes)
}

func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
