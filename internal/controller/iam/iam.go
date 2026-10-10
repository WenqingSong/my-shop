package iam

import (
	"context"

	"github.com/gogf/gf/v2/net/ghttp"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现 IAM v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回 IAM v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Register 处理用户注册。
func (c *ControllerV1) Register(ctx context.Context, req *v1.RegisterReq) (res *v1.RegisterRes, err error) {
	return service.Iam().Register(ctx, req)
}

// Login 处理用户登录并返回 access token。
// 从 HTTP 请求提取 User-Agent 与客户端 IP，传入 logic 作为会话元数据（用于区分设备）。
func (c *ControllerV1) Login(ctx context.Context, req *v1.LoginReq) (res *v1.LoginRes, err error) {
	var userAgent, ip string
	if r := ghttp.RequestFromCtx(ctx); r != nil {
		userAgent = r.Header.Get("User-Agent")
		ip = r.GetClientIp()
	}
	return service.Iam().Login(ctx, req, userAgent, ip)
}

// Refresh 用 refresh token 换取新双 token（公开接口，无 Auth）。
// 从 HTTP 请求提取 User-Agent 与客户端 IP，用于 session upsert 重建时写入设备元数据。
func (c *ControllerV1) Refresh(ctx context.Context, req *v1.RefreshReq) (res *v1.RefreshRes, err error) {
	var userAgent, ip string
	if r := ghttp.RequestFromCtx(ctx); r != nil {
		userAgent = r.Header.Get("User-Agent")
		ip = r.GetClientIp()
	}
	return service.Iam().Refresh(ctx, req, userAgent, ip)
}

// Me 返回当前登录用户。
func (c *ControllerV1) Me(ctx context.Context, req *v1.MeReq) (res *v1.MeRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Iam().Me(ctx, principal.UserID)
}

// Logout 登出当前会话：从 Principal 取 sid 撤销会话，成功时返回 data=null。
func (c *ControllerV1) Logout(ctx context.Context, req *v1.LogoutReq) (res *v1.LogoutRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if _, err := service.Iam().Logout(ctx, principal.Sid); err != nil {
		return nil, err
	}
	return nil, nil
}

// ListSessions 返回当前用户的全部有效会话列表。
func (c *ControllerV1) ListSessions(ctx context.Context, req *v1.ListSessionsReq) (res *v1.ListSessionsRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Iam().ListSessions(ctx, principal.UserID, principal.Sid)
}

// RevokeSession 撤销当前用户指定的一个会话。目标 sid 来自 URL 路径，不信任请求体。
func (c *ControllerV1) RevokeSession(ctx context.Context, req *v1.RevokeSessionReq) (res *v1.RevokeSessionRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Iam().RevokeSessionByID(ctx, principal.UserID, req.Sid); err != nil {
		return nil, err
	}
	return nil, nil
}

// RevokeOthers 撤销当前用户除当前会话外的全部会话。
func (c *ControllerV1) RevokeOthers(ctx context.Context, req *v1.RevokeOthersReq) (res *v1.RevokeOthersRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Iam().RevokeOtherSessions(ctx, principal.UserID, principal.Sid); err != nil {
		return nil, err
	}
	return nil, nil
}

// RevokeAll 全部退出：撤销当前用户的全部会话（含当前）。
func (c *ControllerV1) RevokeAll(ctx context.Context, req *v1.RevokeAllReq) (res *v1.RevokeAllRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Iam().RevokeAllSessions(ctx, principal.UserID); err != nil {
		return nil, err
	}
	return nil, nil
}

// GetUserStatus 查询目标用户账号状态（后台域，经 RequirePermission("user:read")）。
func (c *ControllerV1) GetUserStatus(ctx context.Context, req *v1.GetUserStatusReq) (res *v1.GetUserStatusRes, err error) {
	return service.Iam().GetUserStatus(ctx, req.Id)
}

// UpdateUserStatus 禁用/启用目标用户（后台域，经 RequirePermission("user:status")）。
// 操作管理员 id 取自已认证 AdminPrincipal，不信任请求体身份。
func (c *ControllerV1) UpdateUserStatus(ctx context.Context, req *v1.UpdateUserStatusReq) (res *v1.UpdateUserStatusRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Iam().UpdateUserStatus(ctx, p.AdminID, req)
}
