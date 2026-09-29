// Package admin 实现后台管理员身份与 RBAC v1 API。
package admin

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/admin/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现后台管理员身份与 RBAC v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回后台管理员身份与 RBAC v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Login 处理管理员登录。
func (c *ControllerV1) Login(ctx context.Context, req *v1.LoginReq) (res *v1.LoginRes, err error) {
	return service.Admin().Login(ctx, req)
}

// Me 返回当前登录管理员。
func (c *ControllerV1) Me(ctx context.Context, req *v1.MeReq) (res *v1.MeRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Admin().Me(ctx, p.AdminID)
}

// Logout 登出当前管理员：从 AdminPrincipal 取 sid 撤销会话。
func (c *ControllerV1) Logout(ctx context.Context, req *v1.LogoutReq) (res *v1.LogoutRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if _, err := service.Admin().Logout(ctx, p.Sid); err != nil {
		return nil, err
	}
	return nil, nil
}

// CreateAdmin 创建普通管理员。
func (c *ControllerV1) CreateAdmin(ctx context.Context, req *v1.CreateAdminReq) (res *v1.CreateAdminRes, err error) {
	return service.Admin().CreateAdmin(ctx, req)
}

// UpdateAdminStatus 禁用/启用目标管理员。当前管理员 id 取自 AdminPrincipal，不信任请求体。
func (c *ControllerV1) UpdateAdminStatus(ctx context.Context, req *v1.UpdateAdminStatusReq) (res *v1.UpdateAdminStatusRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Admin().UpdateAdminStatus(ctx, p.AdminID, req)
}

// DeleteAdmin 删除目标管理员。
func (c *ControllerV1) DeleteAdmin(ctx context.Context, req *v1.DeleteAdminReq) (res *v1.DeleteAdminRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Admin().DeleteAdmin(ctx, p.AdminID, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// AssignAdminRole 为目标管理员分配角色。
func (c *ControllerV1) AssignAdminRole(ctx context.Context, req *v1.AssignAdminRoleReq) (res *v1.AssignAdminRoleRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Admin().AssignAdminRole(ctx, p.AdminID, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// RemoveAdminRole 移除目标管理员的角色。
func (c *ControllerV1) RemoveAdminRole(ctx context.Context, req *v1.RemoveAdminRoleReq) (res *v1.RemoveAdminRoleRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if err := service.Admin().RemoveAdminRole(ctx, p.AdminID, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// CreateRole 创建角色。
func (c *ControllerV1) CreateRole(ctx context.Context, req *v1.CreateRoleReq) (res *v1.CreateRoleRes, err error) {
	return service.Admin().CreateRole(ctx, req)
}

// ListRole 角色列表。
func (c *ControllerV1) ListRole(ctx context.Context, req *v1.ListRoleReq) (res *v1.ListRoleRes, err error) {
	return service.Admin().ListRole(ctx)
}

// UpdateRole 更新角色。
func (c *ControllerV1) UpdateRole(ctx context.Context, req *v1.UpdateRoleReq) (res *v1.UpdateRoleRes, err error) {
	return service.Admin().UpdateRole(ctx, req)
}

// DeleteRole 删除角色。
func (c *ControllerV1) DeleteRole(ctx context.Context, req *v1.DeleteRoleReq) (res *v1.DeleteRoleRes, err error) {
	if err := service.Admin().DeleteRole(ctx, req.Id); err != nil {
		return nil, err
	}
	return nil, nil
}

// AssignRolePermission 为角色分配权限。
func (c *ControllerV1) AssignRolePermission(ctx context.Context, req *v1.AssignRolePermissionReq) (res *v1.AssignRolePermissionRes, err error) {
	if err := service.Admin().AssignRolePermission(ctx, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// RemoveRolePermission 移除角色权限。
func (c *ControllerV1) RemoveRolePermission(ctx context.Context, req *v1.RemoveRolePermissionReq) (res *v1.RemoveRolePermissionRes, err error) {
	if err := service.Admin().RemoveRolePermission(ctx, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// CreatePermission 创建权限。
func (c *ControllerV1) CreatePermission(ctx context.Context, req *v1.CreatePermissionReq) (res *v1.CreatePermissionRes, err error) {
	return service.Admin().CreatePermission(ctx, req)
}

// ListPermission 权限列表。
func (c *ControllerV1) ListPermission(ctx context.Context, req *v1.ListPermissionReq) (res *v1.ListPermissionRes, err error) {
	return service.Admin().ListPermission(ctx)
}

// UpdatePermission 更新权限。
func (c *ControllerV1) UpdatePermission(ctx context.Context, req *v1.UpdatePermissionReq) (res *v1.UpdatePermissionRes, err error) {
	return service.Admin().UpdatePermission(ctx, req)
}

// DeletePermission 删除权限。
func (c *ControllerV1) DeletePermission(ctx context.Context, req *v1.DeletePermissionReq) (res *v1.DeletePermissionRes, err error) {
	if err := service.Admin().DeletePermission(ctx, req.Id); err != nil {
		return nil, err
	}
	return nil, nil
}
