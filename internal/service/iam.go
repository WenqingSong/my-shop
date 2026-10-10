package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
)

// IIam 定义 IAM（注册/登录/当前用户/登出/会话管理）服务。
type IIam interface {
	// Register 校验并持久化新用户。
	Register(ctx context.Context, req *v1.RegisterReq) (*v1.RegisterRes, error)
	// Login 校验凭据，写入 Redis 会话（含元数据）并签发含 sid 的 access token 与 refresh token。
	// userAgent/ip 由 Controller 从 HTTP 请求提取后传入，用于会话列表区分设备。
	Login(ctx context.Context, req *v1.LoginReq, userAgent, ip string) (*v1.LoginRes, error)
	// Refresh 凭有效 refresh token 换取新 access token 与新 refresh token（轮换，复用同 sid）。
	// userAgent/ip 由 Controller 从 HTTP 请求提取后传入，用于 session upsert 重建时写入设备元数据。
	Refresh(ctx context.Context, req *v1.RefreshReq, userAgent, ip string) (*v1.RefreshRes, error)
	// Me 返回 userID（已完成认证）对应的用户。
	Me(ctx context.Context, userID int64) (*v1.MeRes, error)
	// Logout 撤销 sid 对应的会话（幂等）。
	Logout(ctx context.Context, sid string) (*v1.LogoutRes, error)
	// ListSessions 返回 userID 的全部有效会话，并标识 currentSid 为当前会话。
	ListSessions(ctx context.Context, userID int64, currentSid string) (*v1.ListSessionsRes, error)
	// RevokeSessionByID 撤销 userID 名下指定的一个会话；目标不存在或非本人返回 SESSION_NOT_FOUND。
	RevokeSessionByID(ctx context.Context, userID int64, targetSid string) error
	// RevokeOtherSessions 撤销 userID 除 currentSid 外的全部会话。
	RevokeOtherSessions(ctx context.Context, userID int64, currentSid string) error
	// RevokeAllSessions 撤销 userID 的全部会话（含当前）。
	RevokeAllSessions(ctx context.Context, userID int64) error
	// GetUserStatus 查询目标普通用户的账号状态（后台域）；目标不存在返回 USER_NOT_FOUND。
	GetUserStatus(ctx context.Context, targetUserID int64) (*v1.GetUserStatusRes, error)
	// UpdateUserStatus 禁用/启用目标普通用户（后台域），operatorAdminID 为已认证管理员 id（来自 AdminPrincipal）。
	// 实际状态迁移同事务更新 status（禁用时递增 auth_epoch）+ 写审计 +（禁用）撤销 refresh family；禁用后 best-effort 撤销 Redis 会话。
	UpdateUserStatus(ctx context.Context, operatorAdminID int64, req *v1.UpdateUserStatusReq) (*v1.UpdateUserStatusRes, error)
}

var localIam IIam

// Iam 返回 IAM 服务实现。
func Iam() IIam {
	if localIam == nil {
		panic("implement not found for interface IIam, forgot register?")
	}
	return localIam
}

// RegisterIam 注册 IAM 服务实现。
func RegisterIam(i IIam) {
	localIam = i
}
