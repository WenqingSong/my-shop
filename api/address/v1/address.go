package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// Address 收货地址结构，创建/查询/更新复用。
type Address struct {
	Id            int64       `json:"id" dc:"地址 id"`
	RecipientName string      `json:"recipient_name" dc:"收货人姓名"`
	Phone         string      `json:"phone" dc:"手机号"`
	Province      string      `json:"province" dc:"省（自由文本）"`
	City          string      `json:"city" dc:"市（自由文本）"`
	District      string      `json:"district" dc:"区（自由文本）"`
	Detail        string      `json:"detail" dc:"详细地址"`
	IsDefault     bool        `json:"is_default" dc:"是否默认地址"`
	CreatedAt     *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt     *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// CreateReq 创建地址请求。请求体不含也不接受 user_id，归属取自已认证 Principal。
type CreateReq struct {
	g.Meta        `path:"/addresses" method:"post" tags:"收货地址" summary:"创建收货地址"`
	RecipientName string `json:"recipient_name" dc:"收货人姓名，trim 后 1~32 字符"`
	Phone         string `json:"phone" dc:"手机号，格式 ^1[3-9]\\d{9}$"`
	Province      string `json:"province" dc:"省（自由文本，非空）"`
	City          string `json:"city" dc:"市（自由文本，非空）"`
	District      string `json:"district" dc:"区（自由文本，非空）"`
	Detail        string `json:"detail" dc:"详细地址，trim 后 1~255 字符"`
	IsDefault     *bool  `json:"is_default" dc:"是否默认地址；首条地址忽略此值自动置为默认"`
}

// CreateRes 创建地址响应。
type CreateRes struct {
	Address
}

// ListReq 查询本人地址列表请求。
type ListReq struct {
	g.Meta `path:"/addresses" method:"get" tags:"收货地址" summary:"查询本人地址列表"`
}

// ListRes 查询本人地址列表响应。
type ListRes struct {
	Items []*Address `json:"items" dc:"地址列表（仅本人）"`
}

// DetailReq 查询本人地址详情请求。
type DetailReq struct {
	g.Meta `path:"/addresses/:id" method:"get" tags:"收货地址" summary:"查询本人地址详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"地址 id"`
}

// DetailRes 查询本人地址详情响应。
type DetailRes struct {
	Address
}

// UpdateReq 更新本人地址请求。请求体不含也不接受 user_id。
type UpdateReq struct {
	g.Meta        `path:"/addresses/:id" method:"put" tags:"收货地址" summary:"更新本人地址"`
	Id            int64   `json:"id" in:"path" v:"required" dc:"地址 id"`
	RecipientName *string `json:"recipient_name" dc:"收货人姓名"`
	Phone         *string `json:"phone" dc:"手机号"`
	Province      *string `json:"province" dc:"省（自由文本）"`
	City          *string `json:"city" dc:"市（自由文本）"`
	District      *string `json:"district" dc:"区（自由文本）"`
	Detail        *string `json:"detail" dc:"详细地址"`
	IsDefault     *bool   `json:"is_default" dc:"是否默认地址"`
}

// UpdateRes 更新本人地址响应。
type UpdateRes struct {
	Address
}

// DeleteReq 删除本人地址请求。
type DeleteReq struct {
	g.Meta `path:"/addresses/:id" method:"delete" tags:"收货地址" summary:"删除本人地址"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"地址 id"`
}

// DeleteRes 删除本人地址响应（无业务字段）。
type DeleteRes struct{}
