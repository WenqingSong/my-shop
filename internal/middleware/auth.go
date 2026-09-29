package middleware

import (
	"context"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const (
	bearerPrefix = "Bearer "
	// adminStatusEnabled 表示管理员启用状态（admins.status=1）。
	adminStatusEnabled = 1
)

// Auth 解析用户侧 token（type=user），校验签名、有效期与会话有效性，注入 Principal。
// 缺失/非法/签名无效/过期/会话无效统一返回 401；type 不符返回 403；Redis 查询失败 fail-closed 返回 401。
func Auth(r *ghttp.Request) {
	claims, ok := authenticateToken(r)
	if !ok {
		return
	}
	p, ok := parseUserPrincipal(r, claims)
	if !ok {
		return
	}

	valid, err := auth.ValidateSession(r.Context(), p.Sid, p.UserID)
	if err != nil {
		// Redis 不可用/查询失败：安全优先，fail-closed 拒绝放行，服务端记录底层错误。
		glog.Errorf(r.Context(), "会话校验失败（fail-closed）: %v", err)
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}
	if !valid {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	r.SetCtxVar(principalKey, p)
	r.Middleware.Next()
}

// AuthSignatureOnly 仅校验签名与有效期（不查会话），注入 Principal{UserID, Sid}。
// 用于 /logout 这类需要幂等撤销的受保护接口，保证重复登出即使 session 已撤销/缺失也能到达 handler。
func AuthSignatureOnly(r *ghttp.Request) {
	claims, ok := authenticateToken(r)
	if !ok {
		return
	}
	p, ok := parseUserPrincipal(r, claims)
	if !ok {
		return
	}
	r.SetCtxVar(principalKey, p)
	r.Middleware.Next()
}

// AdminAuth 解析管理员侧 token（type=admin），校验签名、有效期、管理员会话与 admins 状态，
// 注入 AdminPrincipal{AdminID, Sid, IsSuper}。
// 判定顺序：验签+exp → 401；type≠admin → 403；会话无效 → 401；status=禁用/不存在 → 401。
func AdminAuth(r *ghttp.Request) {
	claims, ok := authenticateToken(r)
	if !ok {
		return
	}
	p, ok := parseAdminPrincipal(r, claims)
	if !ok {
		return
	}

	valid, err := auth.ValidateAdminSession(r.Context(), p.Sid, p.AdminID)
	if err != nil {
		glog.Errorf(r.Context(), "管理员会话校验失败（fail-closed）: %v", err)
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}
	if !valid {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	// 每请求查 admins：禁用/不存在 → 401（禁用即时失效），并读取 is_super。
	admin, err := findAdmin(r.Context(), p.AdminID)
	if err != nil {
		glog.Errorf(r.Context(), "查询管理员失败（fail-closed）: %v", err)
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}
	if admin == nil || admin.Status != adminStatusEnabled {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}
	p.IsSuper = admin.IsSuper

	r.SetCtxVar(adminPrincipalKey, p)
	r.Middleware.Next()
}

// AdminAuthSignatureOnly 仅校验签名、有效期与 type=admin（不查会话与 admins 状态），
// 注入 AdminPrincipal{AdminID, Sid}。用于 /admin/logout 这类需要幂等撤销的受保护接口。
func AdminAuthSignatureOnly(r *ghttp.Request) {
	claims, ok := authenticateToken(r)
	if !ok {
		return
	}
	p, ok := parseAdminPrincipal(r, claims)
	if !ok {
		return
	}
	r.SetCtxVar(adminPrincipalKey, p)
	r.Middleware.Next()
}

// RequirePermission 返回授权中间件：校验当前管理员是否持有指定权限 code。
// 超级管理员（IsSuper）直接放行；否则查询权限 code 集合，命中放行、未命中 403；
// DB 查询失败 fail-closed 返回 500（绝不进 handler）。
func RequirePermission(code string) ghttp.HandlerFunc {
	return func(r *ghttp.Request) {
		p, ok := AdminPrincipalFromContext(r.Context())
		if !ok {
			r.SetError(codes.New(codes.CodeUnauthorized))
			return
		}
		if p.IsSuper {
			r.Middleware.Next()
			return
		}

		has, err := hasPermission(r.Context(), p.AdminID, code)
		if err != nil {
			glog.Errorf(r.Context(), "授权查询失败（fail-closed）: %v", err)
			r.SetError(codes.New(codes.CodeInternalError))
			return
		}
		if !has {
			r.SetError(codes.New(codes.CodeForbidden))
			return
		}
		r.Middleware.Next()
	}
}

// authenticateToken 提取 Bearer token 并验签（含 exp/issuer），失败已 SetError(401)，返回 ok=false。
func authenticateToken(r *ghttp.Request) (*auth.Claims, bool) {
	token, ok := bearerToken(r)
	if !ok {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	claims, err := auth.Parse(r.Context(), token)
	if err != nil {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	return claims, true
}

// parseUserPrincipal 从 claims 解析用户侧 Principal：type 必须为 user（否则 403），
// sub/sid 非法返回 401。
func parseUserPrincipal(r *ghttp.Request, claims *auth.Claims) (*Principal, bool) {
	if claims.Type != auth.TypeUser {
		r.SetError(codes.New(codes.CodeForbidden))
		return nil, false
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	if claims.Sid == "" {
		// 有状态升级后，token 必须携带 sid，旧的无 sid token 不再放行。
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	return &Principal{UserID: userID, Sid: claims.Sid}, true
}

// parseAdminPrincipal 从 claims 解析管理员侧 Principal：type 必须为 admin（否则 403），
// sub/sid 非法返回 401。
func parseAdminPrincipal(r *ghttp.Request, claims *auth.Claims) (*AdminPrincipal, bool) {
	if claims.Type != auth.TypeAdmin {
		r.SetError(codes.New(codes.CodeForbidden))
		return nil, false
	}
	adminID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || adminID <= 0 {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	if claims.Sid == "" {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	return &AdminPrincipal{AdminID: adminID, Sid: claims.Sid}, true
}

// adminRecord 是 admins 表的最小查询结果（认证阶段所需字段）。
type adminRecord struct {
	Status  int
	IsSuper bool
}

// findAdmin 按 id 查询管理员的状态与超级管理员标记，不存在返回 nil。
func findAdmin(ctx context.Context, id int64) (*adminRecord, error) {
	record, err := g.DB().Model("admins").Ctx(ctx).
		Fields("status", "is_super").
		Where("id", id).
		One()
	if err != nil {
		return nil, err
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &adminRecord{
		Status:  record["status"].Int(),
		IsSuper: record["is_super"].Int() == 1,
	}, nil
}

// hasPermission 判断管理员（非超级）是否经其角色持有指定权限 code。
// 查询链路：admins → admin_roles → role_permissions → permissions。
func hasPermission(ctx context.Context, adminID int64, code string) (bool, error) {
	n, err := g.DB().Model("permissions p").
		Ctx(ctx).
		InnerJoin("role_permissions rp", "rp.permission_id = p.id").
		InnerJoin("admin_roles ar", "ar.role_id = rp.role_id").
		Where("ar.admin_id", adminID).
		Where("p.code", code).
		Count()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// bearerToken 从 Authorization 头提取 Bearer token，格式非法返回 false。
func bearerToken(r *ghttp.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, bearerPrefix))
	if token == "" {
		return "", false
	}
	return token, true
}
