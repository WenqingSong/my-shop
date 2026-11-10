package v1

import (
	"github.com/gogf/gf/v2/frame/g"
)

// Profile 当前登录用户的可对外资料。不含密码哈希、认证版本、状态等内部认证信息。
type Profile struct {
	Id        int64   `json:"id" dc:"用户 id"`
	Username  string  `json:"username" dc:"用户名（登录标识，只读）"`
	Nickname  string  `json:"nickname" dc:"昵称；空串表示未设置"`
	AvatarUrl string  `json:"avatar_url" dc:"头像地址；空串表示未设置"`
	Phone     *string `json:"phone" dc:"手机号；null 表示未设置"`
	Birthday  *string `json:"birthday" dc:"生日，YYYY-MM-DD；null 表示未设置"`
}

// GetReq 查询当前登录用户资料请求。用户 id 来自已认证 Principal，不接受请求参数指定。
type GetReq struct {
	g.Meta `path:"/profile" method:"get" tags:"用户资料" summary:"查询当前登录用户资料"`
}

// GetRes 查询当前登录用户资料响应。
type GetRes struct {
	Profile
}

// UpdateReq 更新当前登录用户资料请求。用户 id 来自已认证 Principal，不接受请求体指定。
// 字段省略（null/未提供）表示不修改；空串表示主动清空（nickname/avatar_url 清为空串，
// phone/birthday 清为 NULL）。
type UpdateReq struct {
	g.Meta    `path:"/profile" method:"put" tags:"用户资料" summary:"更新当前登录用户资料"`
	Nickname  *string `json:"nickname" dc:"昵称；省略表示不修改，空串表示清空；trim 后 ≤32 字符"`
	AvatarUrl *string `json:"avatar_url" dc:"头像 URL；省略表示不修改，空串表示清空；非空须为 http/https 且 ≤512 字符"`
	Phone     *string `json:"phone" dc:"手机号；省略表示不修改，空串表示清空为未设置；格式 ^1[3-9]\\d{9}$"`
	Birthday  *string `json:"birthday" dc:"生日；省略表示不修改，空串表示清空；格式 YYYY-MM-DD"`
}

// UpdateRes 更新当前登录用户资料响应（返回更新后的完整资料）。
type UpdateRes struct {
	Profile
}
