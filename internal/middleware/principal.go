package middleware

import "context"

// Principal 是认证中间件注入到请求上下文中的当前用户身份。
// /me、/logout 等受保护接口只信任该字段，不信任请求自带的任何身份信息。
type Principal struct {
	UserID int64
	// Sid 是当前 token 对应的会话标识，/logout 据此撤销会话。
	Sid string
}

type principalContextKey struct{}

var principalKey = principalContextKey{}

// PrincipalFromContext 从 context 中读取认证中间件注入的 Principal。
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	if !ok || p == nil {
		return nil, false
	}
	return p, true
}

// AdminPrincipal 是 AdminAuth 中间件注入到请求上下文中的当前管理员身份。
// 后台受保护接口只信任该字段，不信任请求自带的任何身份信息。
type AdminPrincipal struct {
	// AdminID 是管理员 id（取自 JWT sub）。
	AdminID int64
	// Sid 是当前 token 对应的管理员会话标识，/admin/logout 据此撤销会话。
	Sid string
	// IsSuper 标记是否为超级管理员（授权时直接放行）。
	IsSuper bool
}

type adminPrincipalContextKey struct{}

var adminPrincipalKey = adminPrincipalContextKey{}

// AdminPrincipalFromContext 从 context 中读取 AdminAuth 注入的 AdminPrincipal。
func AdminPrincipalFromContext(ctx context.Context) (*AdminPrincipal, bool) {
	p, ok := ctx.Value(adminPrincipalKey).(*AdminPrincipal)
	if !ok || p == nil {
		return nil, false
	}
	return p, true
}
