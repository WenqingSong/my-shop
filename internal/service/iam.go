package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
)

// IIam 定义 IAM（注册/登录/当前用户/登出）服务。
type IIam interface {
	// Register 校验并持久化新用户。
	Register(ctx context.Context, req *v1.RegisterReq) (*v1.RegisterRes, error)
	// Login 校验凭据，写入 Redis 会话并签发含 sid 的 access token。
	Login(ctx context.Context, req *v1.LoginReq) (*v1.LoginRes, error)
	// Me 返回 userID（已完成认证）对应的用户。
	Me(ctx context.Context, userID int64) (*v1.MeRes, error)
	// Logout 撤销 sid 对应的会话（幂等）。
	Logout(ctx context.Context, sid string) (*v1.LogoutRes, error)
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
