package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
)

// IIam 定义 IAM（注册/登录/当前用户/登出/会话管理）服务。
type IIam interface {
	// Register 校验并持久化新用户。
	Register(ctx context.Context, req *v1.RegisterReq) (*v1.RegisterRes, error)
	// Login 校验凭据，写入 Redis 会话（含元数据）并签发含 sid 的 access token。
	// userAgent/ip 由 Controller 从 HTTP 请求提取后传入，用于会话列表区分设备。
	Login(ctx context.Context, req *v1.LoginReq, userAgent, ip string) (*v1.LoginRes, error)
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
