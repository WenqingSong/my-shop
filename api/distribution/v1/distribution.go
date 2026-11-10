// Package v1 定义一级分销（Distribution）公开 API 契约：
// 用户端「绑定推荐人 / 我的分销信息 / 我的下级 / 我的佣金」与后台「关系、分销用户、佣金、佣金统计」只读查询。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 佣金状态枚举（API 出参为字符串枚举；DB 存 TINYINT 10/20/30，见 docs/design/distribution.md）。
const (
	CommissionStatusPending  = "pending"  // 待结算（10）
	CommissionStatusSettled  = "settled"  // 已结算（20）
	CommissionStatusReversed = "reversed" // 已冲回（30，终态）
)

// Referral 一级推荐关系（user_referrals 一行 = 一个下级的唯一上级）。
type Referral struct {
	UserId         int64       `json:"user_id" dc:"被推荐人（下级）用户 id"`
	ReferrerUserId int64       `json:"referrer_user_id" dc:"一级推荐人（直接上级）用户 id"`
	CreatedAt      *gtime.Time `json:"created_at" dc:"绑定时间（关系建立后不可变更）"`
}

// Commission 订单佣金记录（金额均为整数分）。
type Commission struct {
	Id                int64       `json:"id" dc:"佣金记录 id"`
	OrderId           int64       `json:"order_id" dc:"订单 id（唯一索引，一个订单至多一条佣金）"`
	BuyerUserId       int64       `json:"buyer_user_id" dc:"买家用户 id 快照"`
	BeneficiaryUserId int64       `json:"beneficiary_user_id" dc:"受益推荐人用户 id 快照"`
	BaseAmount        int64       `json:"base_amount" dc:"计佣基数快照（整数分，= 订单实付金额 total_amount）"`
	RateBp            int64       `json:"rate_bp" dc:"计佣比例快照（万分比，500 = 5%）"`
	CommissionAmount  int64       `json:"commission_amount" dc:"佣金金额快照（整数分，向下取整）"`
	Status            string      `json:"status" dc:"状态：pending（待结算）/settled（已结算）/reversed（已冲回，终态）"`
	SettledAt         *gtime.Time `json:"settled_at" dc:"结算时间（可空；本任务不实现结算）"`
	ReversedAmount    int64       `json:"reversed_amount" dc:"已冲回金额（整数分；整单退款时等于 commission_amount）"`
	ReversedAt        *gtime.Time `json:"reversed_at" dc:"冲回时间（可空）"`
	RefundId          *int64      `json:"refund_id" dc:"触发冲回的退款单 id（可空，唯一索引）"`
	ReversalReason    string      `json:"reversal_reason" dc:"冲回原因：refund/cancel，未冲回为空串"`
	CreatedAt         *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt         *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// CommissionSummary 当前用户作为受益推荐人的佣金汇总（按状态分组合计，金额整数分）。
type CommissionSummary struct {
	PendingAmount  int64 `json:"pending_amount" dc:"待结算佣金合计（整数分）"`
	PendingCount   int   `json:"pending_count" dc:"待结算佣金笔数"`
	SettledAmount  int64 `json:"settled_amount" dc:"已结算佣金合计（整数分）"`
	SettledCount   int   `json:"settled_count" dc:"已结算佣金笔数"`
	ReversedAmount int64 `json:"reversed_amount" dc:"已冲回佣金合计（整数分）"`
	ReversedCount  int   `json:"reversed_count" dc:"已冲回佣金笔数"`
}

// Subordinate 直接下级（仅返回 id/用户名/绑定时间，不泄露其他用户资料）。
type Subordinate struct {
	UserId   int64       `json:"user_id" dc:"下级用户 id"`
	Username string      `json:"username" dc:"下级用户名"`
	BoundAt  *gtime.Time `json:"bound_at" dc:"绑定时间"`
}

// Distributor 分销用户汇总（以推荐人为维度聚合直接下级与佣金）。
type Distributor struct {
	ReferrerUserId   int64       `json:"referrer_user_id" dc:"推荐人用户 id"`
	SubordinateCount int         `json:"subordinate_count" dc:"直接下级人数"`
	CommissionCount  int         `json:"commission_count" dc:"佣金笔数"`
	CommissionAmount int64       `json:"commission_amount" dc:"佣金合计（整数分）"`
	ReversedAmount   int64       `json:"reversed_amount" dc:"已冲回合计（整数分）"`
	FirstBoundAt     *gtime.Time `json:"first_bound_at" dc:"最早绑定时间"`
}

// CommissionStatsItem 按佣金状态的统计项。
type CommissionStatsItem struct {
	Status         string `json:"status" dc:"状态：pending/settled/reversed"`
	Count          int    `json:"count" dc:"该状态佣金笔数"`
	Amount         int64  `json:"amount" dc:"该状态佣金合计（整数分）"`
	ReversedAmount int64  `json:"reversed_amount" dc:"该状态已冲回合计（整数分）"`
}

// BindReferralReq 绑定一级推荐人请求（被推荐人恒为当前登录用户，不接受请求体身份）。
type BindReferralReq struct {
	g.Meta         `path:"/distribution/referral" method:"post" tags:"分销" summary:"绑定我的上级推荐人"`
	ReferrerUserId int64 `json:"referrer_user_id" v:"required" dc:"推荐人（直接上级）用户 id，必须存在且启用，且不能是自己"`
}

// BindReferralRes 绑定推荐人响应：重复提交同一推荐人幂等返回既有关系。
type BindReferralRes struct {
	Referral
}

// MyInfoReq 查询我的分销信息请求。
type MyInfoReq struct {
	g.Meta `path:"/distribution/me" method:"get" tags:"分销" summary:"查询我的分销信息与佣金汇总"`
}

// MyInfoRes 我的分销信息响应。
type MyInfoRes struct {
	UserId           int64             `json:"user_id" dc:"当前用户 id"`
	Referrer         *Referral         `json:"referrer" dc:"我的上级推荐关系；未绑定为 null"`
	SubordinateCount int               `json:"subordinate_count" dc:"我的直接下级人数"`
	Commission       CommissionSummary `json:"commission" dc:"我作为受益人的佣金汇总"`
}

// ListSubordinatesReq 查询我的直接下级列表请求。
type ListSubordinatesReq struct {
	g.Meta `path:"/distribution/subordinates" method:"get" tags:"分销" summary:"查询我的直接下级列表"`
	Page   *int `json:"page" dc:"页码，默认 1；<1 或非整数归一为 1"`
	Size   *int `json:"size" dc:"每页数量，默认 20；<1 归一为 20，>100 截断为 100"`
}

// ListSubordinatesRes 直接下级列表响应（按绑定先后倒序）。
type ListSubordinatesRes struct {
	Items []*Subordinate `json:"items" dc:"直接下级列表"`
	Total int            `json:"total" dc:"总记录数"`
	Page  int            `json:"page" dc:"当前页码"`
	Size  int            `json:"size" dc:"每页数量"`
}

// ListCommissionsReq 查询我的佣金记录请求。
type ListCommissionsReq struct {
	g.Meta `path:"/distribution/commissions" method:"get" tags:"分销" summary:"查询我的佣金记录"`
	Page   *int   `json:"page" dc:"页码，默认 1；<1 或非整数归一为 1"`
	Size   *int   `json:"size" dc:"每页数量，默认 20；<1 归一为 20，>100 截断为 100"`
	Status string `json:"status" dc:"按状态过滤：pending/settled/reversed；空串为全部，非法值返回 400"`
}

// ListCommissionsRes 佣金记录响应（按 id 倒序）。
type ListCommissionsRes struct {
	Items []*Commission `json:"items" dc:"佣金记录列表"`
	Total int           `json:"total" dc:"总记录数"`
	Page  int           `json:"page" dc:"当前页码"`
	Size  int           `json:"size" dc:"每页数量"`
}

// AdminListReferralsReq 后台分销关系分页查询请求（只读，持 distribution:referral_list）。
type AdminListReferralsReq struct {
	g.Meta         `path:"/admin/distributions/referrals" method:"get" tags:"分销" summary:"后台分销关系分页列表"`
	Page           *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size           *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	UserId         *int64 `json:"user_id" dc:"被推荐人（下级）用户 id，精确匹配；<=0 返回 400"`
	ReferrerUserId *int64 `json:"referrer_user_id" dc:"推荐人用户 id，精确匹配；<=0 返回 400"`
}

// AdminListReferralsRes 后台分销关系列表响应。
type AdminListReferralsRes struct {
	Items []*Referral `json:"items" dc:"分销关系列表（按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// AdminListDistributorsReq 后台分销用户（推荐人维度）分页查询请求（只读，持 distribution:referral_list）。
type AdminListDistributorsReq struct {
	g.Meta `path:"/admin/distributions/distributors" method:"get" tags:"分销" summary:"后台分销用户汇总列表"`
	Page   *int `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size   *int `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
}

// AdminListDistributorsRes 后台分销用户汇总响应（按推荐人 user_id 升序）。
type AdminListDistributorsRes struct {
	Items []*Distributor `json:"items" dc:"分销用户汇总列表"`
	Total int            `json:"total" dc:"推荐人总数"`
	Page  int            `json:"page" dc:"当前页码"`
	Size  int            `json:"size" dc:"每页数量"`
}

// AdminListCommissionsReq 后台佣金记录分页查询请求（只读，持 distribution:commission_list）。
type AdminListCommissionsReq struct {
	g.Meta            `path:"/admin/distributions/commissions" method:"get" tags:"分销" summary:"后台佣金记录分页列表"`
	Page              *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size              *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	OrderId           *int64 `json:"order_id" dc:"订单 id，精确匹配；<=0 返回 400"`
	BuyerUserId       *int64 `json:"buyer_user_id" dc:"买家用户 id，精确匹配；<=0 返回 400"`
	BeneficiaryUserId *int64 `json:"beneficiary_user_id" dc:"受益推荐人用户 id，精确匹配；<=0 返回 400"`
	Status            string `json:"status" dc:"状态过滤：pending/settled/reversed；空串为全部，非法值返回 400"`
}

// AdminListCommissionsRes 后台佣金记录响应。
type AdminListCommissionsRes struct {
	Items []*Commission `json:"items" dc:"佣金记录列表（按 id 倒序）"`
	Total int           `json:"total" dc:"总记录数"`
	Page  int           `json:"page" dc:"当前页码"`
	Size  int           `json:"size" dc:"每页数量"`
}

// AdminCommissionStatsReq 后台佣金统计请求（只读，持 distribution:commission_list）。
type AdminCommissionStatsReq struct {
	g.Meta `path:"/admin/distributions/commissions/stats" method:"get" tags:"分销" summary:"后台佣金按状态统计"`
}

// AdminCommissionStatsRes 后台佣金统计响应（固定返回 pending/settled/reversed 三项，无数据时为 0）。
type AdminCommissionStatsRes struct {
	Items       []*CommissionStatsItem `json:"items" dc:"按状态统计（固定顺序 pending/settled/reversed）"`
	TotalCount  int                    `json:"total_count" dc:"佣金总笔数"`
	TotalAmount int64                  `json:"total_amount" dc:"佣金总金额（整数分）"`
}
