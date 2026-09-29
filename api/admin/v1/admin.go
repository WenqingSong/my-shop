package v1

import "github.com/gogf/gf/v2/frame/g"

// LoginReq 管理员登录请求。
type LoginReq struct {
	g.Meta   `path:"/admin/login" method:"post" tags:"后台认证" summary:"管理员登录"`
	Username string `json:"username" v:"required" dc:"管理员用户名"`
	Password string `json:"password" v:"required" dc:"管理员密码"`
}

// LoginRes 管理员登录响应。
type LoginRes struct {
	AccessToken string `json:"access_token" dc:"签发的 JWT access token（type=admin）"`
	TokenType   string `json:"token_type" dc:"token 类型，恒为 \"Bearer\""`
	ExpiresIn   int    `json:"expires_in" dc:"token 有效期（秒），恒为 3600"`
}

// MeReq 获取当前登录管理员请求。
type MeReq struct {
	g.Meta `path:"/admin/me" method:"get" tags:"后台认证" summary:"获取当前登录管理员"`
}

// MeRes 获取当前登录管理员响应。
type MeRes struct {
	Id       int64    `json:"id" dc:"管理员 id"`
	Username string   `json:"username" dc:"管理员用户名"`
	IsSuper  bool     `json:"is_super" dc:"是否超级管理员"`
	Roles    []string `json:"roles" dc:"所属角色名列表"`
}

// LogoutReq 登出当前管理员请求。sid 来自当前 token，不接受客户端指定。
type LogoutReq struct {
	g.Meta `path:"/admin/logout" method:"post" tags:"后台认证" summary:"登出当前管理员"`
}

// LogoutRes 登出当前管理员响应（无业务字段，成功时 data 为 null）。
type LogoutRes struct{}

// CreateAdminReq 创建普通管理员请求（is_super 恒为 0）。
type CreateAdminReq struct {
	g.Meta   `path:"/admin/admins" method:"post" tags:"管理员" summary:"创建普通管理员"`
	Username string `json:"username" v:"required" dc:"管理员用户名，3~24 位大小写字母或数字"`
	Password string `json:"password" v:"required" dc:"管理员密码，8~24 位"`
}

// CreateAdminRes 创建普通管理员响应。
type CreateAdminRes struct {
	Id       int64  `json:"id" dc:"新管理员 id"`
	Username string `json:"username" dc:"新管理员用户名"`
}

// UpdateAdminStatusReq 禁用/启用管理员请求。
type UpdateAdminStatusReq struct {
	g.Meta `path:"/admin/admins/:id/status" method:"put" tags:"管理员" summary:"禁用/启用管理员"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"管理员 id"`
	Status int   `json:"status" v:"required|in:0,1" dc:"1 启用 / 0 禁用"`
}

// UpdateAdminStatusRes 禁用/启用管理员响应（无业务字段）。
type UpdateAdminStatusRes struct{}

// DeleteAdminReq 删除管理员请求。
type DeleteAdminReq struct {
	g.Meta `path:"/admin/admins/:id" method:"delete" tags:"管理员" summary:"删除管理员"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"管理员 id"`
}

// DeleteAdminRes 删除管理员响应（无业务字段）。
type DeleteAdminRes struct{}

// AssignAdminRoleReq 为管理员分配角色请求。
type AssignAdminRoleReq struct {
	g.Meta `path:"/admin/admins/:id/roles" method:"post" tags:"管理员" summary:"为管理员分配角色"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"管理员 id"`
	RoleId int64 `json:"role_id" v:"required" dc:"角色 id"`
}

// AssignAdminRoleRes 为管理员分配角色响应（无业务字段）。
type AssignAdminRoleRes struct{}

// RemoveAdminRoleReq 移除管理员角色请求。
type RemoveAdminRoleReq struct {
	g.Meta `path:"/admin/admins/:id/roles/:role_id" method:"delete" tags:"管理员" summary:"移除管理员角色"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"管理员 id"`
	RoleId int64 `json:"role_id" in:"path" v:"required" dc:"角色 id"`
}

// RemoveAdminRoleRes 移除管理员角色响应（无业务字段）。
type RemoveAdminRoleRes struct{}

// Role 角色结构。
type Role struct {
	Id          int64  `json:"id" dc:"角色 id"`
	Name        string `json:"name" dc:"角色名"`
	Description string `json:"description" dc:"角色描述"`
}

// CreateRoleReq 创建角色请求。
type CreateRoleReq struct {
	g.Meta      `path:"/admin/roles" method:"post" tags:"角色" summary:"创建角色"`
	Name        string `json:"name" v:"required" dc:"角色名，非空且不超过 64 字符"`
	Description string `json:"description" dc:"角色描述，不超过 255 字符"`
}

// CreateRoleRes 创建角色响应。
type CreateRoleRes struct {
	Id int64 `json:"id" dc:"新角色 id"`
}

// ListRoleReq 角色列表请求。
type ListRoleReq struct {
	g.Meta `path:"/admin/roles" method:"get" tags:"角色" summary:"角色列表"`
}

// ListRoleRes 角色列表响应。
type ListRoleRes struct {
	Items []*Role `json:"items" dc:"角色列表"`
}

// UpdateRoleReq 更新角色请求（仅提交需要变更的字段）。
type UpdateRoleReq struct {
	g.Meta      `path:"/admin/roles/:id" method:"put" tags:"角色" summary:"更新角色"`
	Id          int64   `json:"id" in:"path" v:"required" dc:"角色 id"`
	Name        *string `json:"name" dc:"角色名，非空且不超过 64 字符"`
	Description *string `json:"description" dc:"角色描述，不超过 255 字符"`
}

// UpdateRoleRes 更新角色响应。
type UpdateRoleRes struct {
	Role
}

// DeleteRoleReq 删除角色请求。
type DeleteRoleReq struct {
	g.Meta `path:"/admin/roles/:id" method:"delete" tags:"角色" summary:"删除角色"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"角色 id"`
}

// DeleteRoleRes 删除角色响应（无业务字段）。
type DeleteRoleRes struct{}

// AssignRolePermissionReq 为角色分配权限请求。
type AssignRolePermissionReq struct {
	g.Meta       `path:"/admin/roles/:id/permissions" method:"post" tags:"角色" summary:"为角色分配权限"`
	Id           int64 `json:"id" in:"path" v:"required" dc:"角色 id"`
	PermissionId int64 `json:"permission_id" v:"required" dc:"权限 id"`
}

// AssignRolePermissionRes 为角色分配权限响应（无业务字段）。
type AssignRolePermissionRes struct{}

// RemoveRolePermissionReq 移除角色权限请求。
type RemoveRolePermissionReq struct {
	g.Meta       `path:"/admin/roles/:id/permissions/:permission_id" method:"delete" tags:"角色" summary:"移除角色权限"`
	Id           int64 `json:"id" in:"path" v:"required" dc:"角色 id"`
	PermissionId int64 `json:"permission_id" in:"path" v:"required" dc:"权限 id"`
}

// RemoveRolePermissionRes 移除角色权限响应（无业务字段）。
type RemoveRolePermissionRes struct{}

// Permission 权限结构。
type Permission struct {
	Id          int64  `json:"id" dc:"权限 id"`
	Code        string `json:"code" dc:"权限 code（稳定唯一 key，资源:动作）"`
	Name        string `json:"name" dc:"权限名"`
	Description string `json:"description" dc:"权限描述"`
}

// CreatePermissionReq 创建权限请求。
type CreatePermissionReq struct {
	g.Meta      `path:"/admin/permissions" method:"post" tags:"权限" summary:"创建权限"`
	Code        string `json:"code" v:"required" dc:"权限 code，非空且不超过 64 字符"`
	Name        string `json:"name" v:"required" dc:"权限名，非空且不超过 64 字符"`
	Description string `json:"description" dc:"权限描述，不超过 255 字符"`
}

// CreatePermissionRes 创建权限响应。
type CreatePermissionRes struct {
	Id int64 `json:"id" dc:"新权限 id"`
}

// ListPermissionReq 权限列表请求。
type ListPermissionReq struct {
	g.Meta `path:"/admin/permissions" method:"get" tags:"权限" summary:"权限列表"`
}

// ListPermissionRes 权限列表响应。
type ListPermissionRes struct {
	Items []*Permission `json:"items" dc:"权限列表"`
}

// UpdatePermissionReq 更新权限请求（仅提交需要变更的字段）。
type UpdatePermissionReq struct {
	g.Meta      `path:"/admin/permissions/:id" method:"put" tags:"权限" summary:"更新权限"`
	Id          int64   `json:"id" in:"path" v:"required" dc:"权限 id"`
	Code        *string `json:"code" dc:"权限 code，非空且不超过 64 字符"`
	Name        *string `json:"name" dc:"权限名，非空且不超过 64 字符"`
	Description *string `json:"description" dc:"权限描述，不超过 255 字符"`
}

// UpdatePermissionRes 更新权限响应。
type UpdatePermissionRes struct {
	Permission
}

// DeletePermissionReq 删除权限请求。
type DeletePermissionReq struct {
	g.Meta `path:"/admin/permissions/:id" method:"delete" tags:"权限" summary:"删除权限"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"权限 id"`
}

// DeletePermissionRes 删除权限响应（无业务字段）。
type DeletePermissionRes struct{}
