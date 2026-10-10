package v1

import "github.com/gogf/gf/v2/frame/g"

// RegisterReq 用户注册请求。
type RegisterReq struct {
	g.Meta   `path:"/register" method:"post" tags:"IAM" summary:"注册新用户"`
	Username string `json:"username" dc:"用户名，3~24 位大小写字母或数字"`
	Password string `json:"password" dc:"密码，8~24 位"`
}

// RegisterRes 用户注册响应。
type RegisterRes struct {
	Id       int64  `json:"id" dc:"新用户 id"`
	Username string `json:"username" dc:"注册用户名"`
}

// LoginReq 用户登录请求。
type LoginReq struct {
	g.Meta   `path:"/login" method:"post" tags:"IAM" summary:"用户名密码登录"`
	Username string `json:"username" dc:"用户名"`
	Password string `json:"password" dc:"密码"`
}

// LoginRes 用户登录响应。
type LoginRes struct {
	AccessToken  string `json:"access_token" dc:"签发的 JWT access token"`
	RefreshToken string `json:"refresh_token" dc:"签发的长期 refresh token（明文仅本次响应出现一次）"`
	TokenType    string `json:"token_type" dc:"token 类型，恒为 \"Bearer\""`
	ExpiresIn    int    `json:"expires_in" dc:"token 有效期（秒），恒为 3600"`
}

// RefreshReq 刷新 access token 请求。refresh token 自身即凭证，接口公开、无 Auth。
type RefreshReq struct {
	g.Meta       `path:"/refresh" method:"post" tags:"IAM" summary:"用 refresh token 换取新双 token"`
	RefreshToken string `json:"refresh_token" dc:"refresh token 明文"`
}

// RefreshRes 刷新响应。
type RefreshRes struct {
	AccessToken  string `json:"access_token" dc:"新签发的 JWT access token"`
	RefreshToken string `json:"refresh_token" dc:"新签发的 refresh token（旧 token 已失效）"`
	TokenType    string `json:"token_type" dc:"token 类型，恒为 \"Bearer\""`
	ExpiresIn    int    `json:"expires_in" dc:"token 有效期（秒），恒为 3600"`
}

// MeReq 获取当前登录用户请求。
type MeReq struct {
	g.Meta `path:"/me" method:"get" tags:"IAM" summary:"获取当前登录用户"`
}

// MeRes 获取当前登录用户响应。
type MeRes struct {
	Id       int64  `json:"id" dc:"当前用户 id"`
	Username string `json:"username" dc:"当前用户名"`
}

// LogoutReq 登出当前会话请求。sid 来自当前 token，不接受客户端指定。
type LogoutReq struct {
	g.Meta `path:"/logout" method:"post" tags:"IAM" summary:"登出当前会话"`
}

// LogoutRes 登出当前会话响应（无业务字段，成功时 data 为 null）。
type LogoutRes struct{}

// Session 会话条目（会话列表项）。
type Session struct {
	Sid       string `json:"sid" dc:"会话标识"`
	LoginAt   int64  `json:"login_at" dc:"登录时间（unix 秒）"`
	UserAgent string `json:"user_agent" dc:"客户端 User-Agent"`
	IP        string `json:"ip" dc:"客户端 IP"`
	Current   bool   `json:"current" dc:"是否为当前会话"`
}

// ListSessionsReq 会话列表请求。
type ListSessionsReq struct {
	g.Meta `path:"/sessions" method:"get" tags:"IAM" summary:"会话列表"`
}

// ListSessionsRes 会话列表响应。
type ListSessionsRes struct {
	Items []*Session `json:"items" dc:"会话列表"`
}

// RevokeSessionReq 撤销指定会话请求。目标 sid 来自 URL 路径，不接受请求体指定。
type RevokeSessionReq struct {
	g.Meta `path:"/sessions/:sid" method:"delete" tags:"IAM" summary:"撤销指定会话"`
	Sid    string `json:"sid" in:"path" dc:"目标会话标识"`
}

// RevokeSessionRes 撤销指定会话响应（无业务字段，成功时 data 为 null）。
type RevokeSessionRes struct{}

// RevokeOthersReq 撤销其他会话请求。
type RevokeOthersReq struct {
	g.Meta `path:"/sessions/revoke-others" method:"post" tags:"IAM" summary:"撤销其他会话"`
}

// RevokeOthersRes 撤销其他会话响应（无业务字段，成功时 data 为 null）。
type RevokeOthersRes struct{}

// RevokeAllReq 全部退出请求。
type RevokeAllReq struct {
	g.Meta `path:"/sessions/revoke-all" method:"post" tags:"IAM" summary:"全部退出"`
}

// RevokeAllRes 全部退出响应（无业务字段，成功时 data 为 null）。
type RevokeAllRes struct{}

// GetUserStatusReq 查询用户账号状态请求（后台域，AdminAuth + RequirePermission("user:read")）。
// 目标用户 id 来自 URL 路径，服务端不信任请求体身份。
type GetUserStatusReq struct {
	g.Meta `path:"/admin/users/:id/status" method:"get" tags:"IAM" summary:"查询用户账号状态"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"目标用户 id"`
}

// GetUserStatusRes 查询用户账号状态响应。
type GetUserStatusRes struct {
	Id     int64 `json:"id" dc:"用户 id"`
	Status int   `json:"status" dc:"1 启用 / 0 禁用"`
}

// UpdateUserStatusReq 禁用/启用用户请求（后台域，AdminAuth + RequirePermission("user:status")）。
// 目标用户 id 来自 URL 路径；reason 必填（trim 后非空、≤255）。
type UpdateUserStatusReq struct {
	g.Meta `path:"/admin/users/:id/status" method:"put" tags:"IAM" summary:"禁用/启用用户"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"目标用户 id"`
	Status int    `json:"status" v:"required|in:0,1" dc:"1 启用 / 0 禁用"`
	Reason string `json:"reason" v:"required" dc:"变更原因，非空且不超过 255 字符"`
}

// UpdateUserStatusRes 禁用/启用用户响应（无业务字段，成功时 data 为 null）。
type UpdateUserStatusRes struct{}
