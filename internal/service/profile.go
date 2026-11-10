package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/profile/v1"
)

// IProfile 定义当前登录用户资料服务。数据归属恒为已认证的 userID，
// 不接受客户端传入的用户 id；对外只暴露可公开的资料字段。
type IProfile interface {
	// Get 查询 userID 的资料；用户不存在返回 USER_NOT_FOUND（404）。
	Get(ctx context.Context, userID int64) (*v1.GetRes, error)
	// Update 更新 userID 的资料；字段省略表示不修改、空串表示清空。
	// 手机号唯一性由 users.uk_phone 兜底，冲突映射为 PHONE_EXISTS（409）。
	Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error)
}

var localProfile IProfile

// Profile 返回用户资料服务实现。
func Profile() IProfile {
	if localProfile == nil {
		panic("implement not found for interface IProfile, forgot register?")
	}
	return localProfile
}

// RegisterProfile 注册用户资料服务实现。
func RegisterProfile(s IProfile) {
	localProfile = s
}
