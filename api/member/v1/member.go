// Package v1 定义会员等级、会员权益与成长值的前台/后台公开契约。
//
// 金额与数值口径：
//   - growth_threshold / growth_value / change_amount 均为整数成长值；
//     本项目成长值口径 = 已支付订单实付金额（整数分），即 1 分消费 = 1 成长值（见 docs/design/member.md）。
//   - discount_rate_bp 为万分比折扣率：10000=原价（无折扣）、9000=9 折、8800=8.8 折。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 会员等级状态（DB TINYINT）。
const (
	LevelStatusDisabled = 0 // 停用：不参与新的等级判定，已存在的会员档案保留
	LevelStatusEnabled  = 1 // 启用
)

// MemberLevel 是会员等级配置（前台只读列表与后台维护复用）。
type MemberLevel struct {
	Id              int64       `json:"id" dc:"等级 id"`
	LevelNo         int         `json:"level_no" dc:"等级序号（1、2、3…，全局唯一，须与门槛同向递增）"`
	Name            string      `json:"name" dc:"等级名称"`
	GrowthThreshold int64       `json:"growth_threshold" dc:"达到该累计成长值可升入本级（全局唯一，整数成长值）"`
	DiscountRateBp  int         `json:"discount_rate_bp" dc:"等级折扣率万分比：10000=原价、9000=9 折、8800=8.8 折"`
	Status          int         `json:"status" dc:"状态：1=启用、0=停用"`
	CreatedAt       *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt       *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// Membership 是用户当前会员信息（含等级权益与下一等级进度）。
type Membership struct {
	UserId         int64       `json:"user_id" dc:"用户 id"`
	LevelId        *int64      `json:"level_id" dc:"当前等级 id；用户无会员档案时为 null（视为成长值 0 解析出的等级）"`
	LevelNo        *int        `json:"level_no" dc:"当前等级序号；无适用等级时为 null"`
	LevelName      string      `json:"level_name" dc:"当前等级名称；无适用等级时为空串"`
	GrowthValue    int64       `json:"growth_value" dc:"当前累计成长值（整数，非负）"`
	DiscountRateBp int         `json:"discount_rate_bp" dc:"当前生效折扣率万分比；无适用等级时为 10000（不打折）"`
	UpgradedAt     *gtime.Time `json:"upgraded_at" dc:"最近一次等级变更时间；null 表示尚未升级过"`
	NextLevelId    *int64      `json:"next_level_id" dc:"下一等级 id；已是最高等级或未配置更高等级时为 null"`
	NextLevelName  string      `json:"next_level_name" dc:"下一等级名称；无更高等级时为空串"`
	NextThreshold  *int64      `json:"next_level_growth_threshold" dc:"下一等级成长值门槛；无更高等级时为 null"`
}

// GrowthLog 是成长值流水（append-only，按时间倒序返回）。
type GrowthLog struct {
	Id           int64       `json:"id" dc:"流水 id"`
	ChangeAmount int64       `json:"change_amount" dc:"带符号变动值：正=增加、负=扣减"`
	GrowthBefore int64       `json:"growth_before" dc:"变动前成长值"`
	GrowthAfter  int64       `json:"growth_after" dc:"变动后成长值（恒 ≥ 0）"`
	ChangeType   string      `json:"change_type" dc:"语义类型：order_reward/refund_deduct（当前仅实现这两种）"`
	BizType      string      `json:"biz_type" dc:"关联业务类型：order/refund（当前仅实现这两种）"`
	BizId        *int64      `json:"biz_id" dc:"关联业务 id（订单 id 或退款单 id）"`
	Remark       string      `json:"remark" dc:"备注"`
	CreatedAt    *gtime.Time `json:"created_at" dc:"发生时间"`
}

// LevelsReq 查询会员等级配置（登录用户可读，仅返回启用等级）。
type LevelsReq struct {
	g.Meta `path:"/members/levels" method:"get" tags:"会员" summary:"查询会员等级配置"`
}

// LevelsRes 会员等级配置列表响应（按 level_no 升序）。
type LevelsRes struct {
	Items []*MemberLevel `json:"items" dc:"启用的等级配置，按 level_no 升序"`
}

// MyReq 查询当前用户会员信息与权益（作用于 Principal.UserID）。
type MyReq struct {
	g.Meta `path:"/my/membership" method:"get" tags:"会员" summary:"查询本人会员等级与权益"`
}

// MyRes 当前用户会员信息响应。
type MyRes struct {
	Membership
}

// GrowthLogsReq 查询本人成长值流水（分页，按时间倒序）。
type GrowthLogsReq struct {
	g.Meta `path:"/my/membership/growth-logs" method:"get" tags:"会员" summary:"查询本人成长值流水"`
	Page   int `json:"page" dc:"页码，默认 1；<1 按 1 处理"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100；越界按上限截断"`
}

// GrowthLogsRes 成长值流水分页响应。
type GrowthLogsRes struct {
	Items []*GrowthLog `json:"items" dc:"成长值流水（按 id 倒序）"`
	Total int          `json:"total" dc:"总记录数"`
	Page  int          `json:"page" dc:"当前页码"`
	Size  int          `json:"size" dc:"每页数量"`
}

// AdminListLevelsReq 后台查询全部会员等级配置（含停用，持 member:list）。
type AdminListLevelsReq struct {
	g.Meta `path:"/admin/member-levels" method:"get" tags:"会员" summary:"后台查询会员等级配置"`
}

// AdminListLevelsRes 后台会员等级配置列表响应（按 level_no 升序，含停用）。
type AdminListLevelsRes struct {
	Items []*MemberLevel `json:"items" dc:"全部等级配置，按 level_no 升序"`
}

// AdminCreateLevelReq 后台创建会员等级（持 member:create）。
// 字段校验在 Logic 层完成：任一项非法返回 20002，序号/门槛重复返回 20004，
// 新增后若破坏「level_no 与 growth_threshold 同向递增」也返回 20002。
type AdminCreateLevelReq struct {
	g.Meta          `path:"/admin/member-levels" method:"post" tags:"会员" summary:"创建会员等级"`
	LevelNo         int    `json:"level_no" dc:"等级序号，必须为正整数且全局唯一"`
	Name            string `json:"name" dc:"等级名称，trim 后 1~64 字符"`
	GrowthThreshold int64  `json:"growth_threshold" dc:"成长值门槛，必须 ≥0 且全局唯一"`
	DiscountRateBp  int    `json:"discount_rate_bp" dc:"折扣率万分比，取值 1~10000（10000=原价）"`
	Status          int    `json:"status" dc:"状态：1=启用、0=停用；缺省按 1 处理"`
}

// AdminCreateLevelRes 创建会员等级响应。
type AdminCreateLevelRes struct {
	MemberLevel
}

// AdminUpdateLevelReq 后台更新会员等级（持 member:update，全量覆盖式更新）。
type AdminUpdateLevelReq struct {
	g.Meta          `path:"/admin/member-levels/:id" method:"put" tags:"会员" summary:"更新会员等级"`
	Id              int64  `json:"id" in:"path" v:"required" dc:"等级 id"`
	LevelNo         int    `json:"level_no" dc:"等级序号，必须为正整数且全局唯一"`
	Name            string `json:"name" dc:"等级名称，trim 后 1~64 字符"`
	GrowthThreshold int64  `json:"growth_threshold" dc:"成长值门槛，必须 ≥0 且全局唯一"`
	DiscountRateBp  int    `json:"discount_rate_bp" dc:"折扣率万分比，取值 1~10000"`
	Status          int    `json:"status" dc:"状态：1=启用、0=停用"`
}

// AdminUpdateLevelRes 更新会员等级响应。
type AdminUpdateLevelRes struct {
	MemberLevel
}

// AdminDeleteLevelReq 后台删除会员等级（持 member:delete；被用户会员记录引用时返回 20003）。
type AdminDeleteLevelReq struct {
	g.Meta `path:"/admin/member-levels/:id" method:"delete" tags:"会员" summary:"删除会员等级"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"等级 id"`
}

// AdminDeleteLevelRes 删除会员等级响应（无业务字段）。
type AdminDeleteLevelRes struct{}

// AdminMemberReq 后台查询指定用户的会员信息（持 member:view）。
type AdminMemberReq struct {
	g.Meta `path:"/admin/members/:user_id" method:"get" tags:"会员" summary:"后台查询用户会员信息"`
	UserId int64 `json:"user_id" in:"path" v:"required" dc:"用户 id"`
}

// AdminMemberRes 用户会员信息响应（无会员档案时 level 字段为 null、成长值为 0）。
type AdminMemberRes struct {
	Membership
	HasRecord bool `json:"has_record" dc:"该用户是否已有 user_memberships 档案；false 时按成长值 0 解析等级"`
}
