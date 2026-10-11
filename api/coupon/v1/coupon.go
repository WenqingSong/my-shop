// Package v1 定义优惠券（Coupon）V1 的公开 API 契约：
// 管理员模板管理（创建/查询/停止领取）与用户侧领取/查询我的券。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 用户券状态枚举（API 出参为字符串枚举；DB 存 TINYINT 10/20/30）。
const (
	StatusAvailable = "available" // 可用（10）
	StatusLocked    = "locked"    // 已预占（20）
	StatusUsed      = "used"      // 已使用（30）
)

// 模板状态（DB TINYINT）。
const (
	TemplateStatusActive  = 1 // 正常可领取
	TemplateStatusStopped = 0 // 已停止新领取
)

// Template 优惠券模板（后台管理响应）。
type Template struct {
	Id              int64       `json:"id" dc:"模板 id"`
	Name            string      `json:"name" dc:"模板名"`
	ThresholdAmount int64       `json:"threshold_amount" dc:"满减门槛（整数分，0=无门槛）"`
	DiscountAmount  int64       `json:"discount_amount" dc:"减免金额（整数分）"`
	TotalQuantity   int64       `json:"total_quantity" dc:"发放总量"`
	IssuedQuantity  int64       `json:"issued_quantity" dc:"已发放数量"`
	ClaimStartTime  *gtime.Time `json:"claim_start_time" dc:"领取开始时间"`
	ClaimEndTime    *gtime.Time `json:"claim_end_time" dc:"领取结束时间"`
	UseStartTime    *gtime.Time `json:"use_start_time" dc:"使用开始时间"`
	UseEndTime      *gtime.Time `json:"use_end_time" dc:"使用结束时间"`
	Status          int         `json:"status" dc:"1=正常可领取、0=已停止新领取"`
	CreatedAt       *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt       *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// UserCoupon 用户已领取券（含模板关键字段快照，供「我的券」展示）。
type UserCoupon struct {
	Id              int64       `json:"id" dc:"券 id"`
	TemplateId      int64       `json:"template_id" dc:"模板 id"`
	Status          string      `json:"status" dc:"状态：available/locked/used"`
	OrderId         *int64      `json:"order_id" dc:"当前预占/核销该券的订单 id（释放时为空）"`
	ReceivedAt      *gtime.Time `json:"received_at" dc:"领取时间"`
	LockedAt        *gtime.Time `json:"locked_at" dc:"预占时间"`
	UsedAt          *gtime.Time `json:"used_at" dc:"核销时间"`
	Name            string      `json:"name" dc:"模板名"`
	ThresholdAmount int64       `json:"threshold_amount" dc:"满减门槛（整数分）"`
	DiscountAmount  int64       `json:"discount_amount" dc:"减免金额（整数分）"`
	UseStartTime    *gtime.Time `json:"use_start_time" dc:"使用开始时间"`
	UseEndTime      *gtime.Time `json:"use_end_time" dc:"使用结束时间"`
}

// AdminCreateReq 创建优惠券模板请求（后台，需 coupon:create）。
type AdminCreateReq struct {
	g.Meta          `path:"/admin/coupons" method:"post" tags:"优惠券" summary:"创建优惠券模板"`
	Name            string      `json:"name" dc:"模板名（trim 后 1~64 字符）"`
	ThresholdAmount int64       `json:"threshold_amount" dc:"满减门槛（整数分，≥0 且 ≤99999999，0=无门槛）"`
	DiscountAmount  int64       `json:"discount_amount" dc:"减免金额（整数分，>0 且 ≤99999999）"`
	TotalQuantity   int64       `json:"total_quantity" dc:"发放总量（>0）"`
	ClaimStartTime  *gtime.Time `json:"claim_start_time" dc:"领取开始时间（必须早于领取结束）"`
	ClaimEndTime    *gtime.Time `json:"claim_end_time" dc:"领取结束时间"`
	UseStartTime    *gtime.Time `json:"use_start_time" dc:"使用开始时间（必须早于使用结束）"`
	UseEndTime      *gtime.Time `json:"use_end_time" dc:"使用结束时间"`
}

// AdminCreateRes 创建优惠券模板响应。
type AdminCreateRes struct {
	Template
}

// AdminListReq 查询优惠券模板列表请求（后台，需 coupon:list）。
type AdminListReq struct {
	g.Meta `path:"/admin/coupons" method:"get" tags:"优惠券" summary:"查询优惠券模板列表"`
}

// AdminListRes 查询优惠券模板列表响应。
type AdminListRes struct {
	Items []*Template `json:"items" dc:"模板列表（按 id 倒序）"`
}

// AdminStopReq 停止新领取请求（后台，需 coupon:stop）。
type AdminStopReq struct {
	g.Meta `path:"/admin/coupons/:id/stop" method:"post" tags:"优惠券" summary:"停止优惠券领取"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"模板 id"`
}

// AdminStopRes 停止新领取响应。
type AdminStopRes struct {
	Template
}

// ClaimReq 领取优惠券请求（前台登录用户，作用于 Principal.UserID）。
type ClaimReq struct {
	g.Meta     `path:"/coupons/:template_id/claim" method:"post" tags:"优惠券" summary:"领取优惠券"`
	TemplateId int64 `json:"template_id" in:"path" v:"required" dc:"模板 id"`
}

// ClaimRes 领取优惠券响应。
type ClaimRes struct {
	UserCoupon
}

// ListMineReq 查询我的优惠券请求（前台登录用户，作用于 Principal.UserID）。
type ListMineReq struct {
	g.Meta `path:"/my/coupons" method:"get" tags:"优惠券" summary:"查询我的优惠券"`
}

// ListMineRes 查询我的优惠券响应。
type ListMineRes struct {
	Items []*UserCoupon `json:"items" dc:"我的优惠券列表（按 id 倒序）"`
}
