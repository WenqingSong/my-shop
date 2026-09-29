package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/admin/v1"
)

// IAdmin 定义后台管理员身份与 RBAC 服务。
//
// 涉及「目标管理员」的操作（禁用/删除/分配角色）都需要传入当前已认证的管理员 id
// （由 Controller 从 AdminPrincipal 提取），用于超级管理员保护与自操作防护；
// 目标管理员 id 来自 URL 路径，绝不信任请求体中的身份信息。
type IAdmin interface {
	// Login 校验管理员凭据，写入 Redis 管理员会话并签发 type=admin 的 access token。
	Login(ctx context.Context, req *v1.LoginReq) (*v1.LoginRes, error)
	// Me 返回当前管理员 id/username/is_super/所属角色。
	Me(ctx context.Context, adminID int64) (*v1.MeRes, error)
	// Logout 撤销 sid 对应的管理员会话（幂等）。
	Logout(ctx context.Context, sid string) (*v1.LogoutRes, error)

	// CreateAdmin 创建普通管理员（is_super=0）。
	CreateAdmin(ctx context.Context, req *v1.CreateAdminReq) (*v1.CreateAdminRes, error)
	// UpdateAdminStatus 禁用/启用目标管理员；拒绝操作超级管理员与自身。
	UpdateAdminStatus(ctx context.Context, currentAdminID int64, req *v1.UpdateAdminStatusReq) (*v1.UpdateAdminStatusRes, error)
	// DeleteAdmin 删除目标管理员；拒绝操作超级管理员与自身。
	DeleteAdmin(ctx context.Context, currentAdminID int64, req *v1.DeleteAdminReq) error
	// AssignAdminRole 为目标管理员分配角色；拒绝操作超级管理员与自身。
	AssignAdminRole(ctx context.Context, currentAdminID int64, req *v1.AssignAdminRoleReq) error
	// RemoveAdminRole 移除目标管理员的角色；拒绝操作超级管理员与自身。
	RemoveAdminRole(ctx context.Context, currentAdminID int64, req *v1.RemoveAdminRoleReq) error

	// CreateRole 创建角色。
	CreateRole(ctx context.Context, req *v1.CreateRoleReq) (*v1.CreateRoleRes, error)
	// ListRole 返回角色列表。
	ListRole(ctx context.Context) (*v1.ListRoleRes, error)
	// UpdateRole 更新角色。
	UpdateRole(ctx context.Context, req *v1.UpdateRoleReq) (*v1.UpdateRoleRes, error)
	// DeleteRole 删除角色（事务级联删除关联行）。
	DeleteRole(ctx context.Context, id int64) error
	// AssignRolePermission 为角色分配权限。
	AssignRolePermission(ctx context.Context, req *v1.AssignRolePermissionReq) error
	// RemoveRolePermission 移除角色权限。
	RemoveRolePermission(ctx context.Context, req *v1.RemoveRolePermissionReq) error

	// CreatePermission 创建权限。
	CreatePermission(ctx context.Context, req *v1.CreatePermissionReq) (*v1.CreatePermissionRes, error)
	// ListPermission 返回权限列表。
	ListPermission(ctx context.Context) (*v1.ListPermissionRes, error)
	// UpdatePermission 更新权限。
	UpdatePermission(ctx context.Context, req *v1.UpdatePermissionReq) (*v1.UpdatePermissionRes, error)
	// DeletePermission 删除权限（事务级联删除关联行）。
	DeletePermission(ctx context.Context, id int64) error
}

var localAdmin IAdmin

// Admin 返回后台管理员身份与 RBAC 服务实现。
func Admin() IAdmin {
	if localAdmin == nil {
		panic("implement not found for interface IAdmin, forgot register?")
	}
	return localAdmin
}

// RegisterAdmin 注册后台管理员身份与 RBAC 服务实现。
func RegisterAdmin(a IAdmin) {
	localAdmin = a
}
