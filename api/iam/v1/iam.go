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
	AccessToken string `json:"access_token" dc:"签发的 JWT access token"`
	TokenType   string `json:"token_type" dc:"token 类型，恒为 \"Bearer\""`
	ExpiresIn   int    `json:"expires_in" dc:"token 有效期（秒），恒为 3600"`
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
