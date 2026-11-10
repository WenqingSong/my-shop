// Package v1 定义一级分销（推荐关系 + 订单佣金）的前台/后台公开契约。
//
// 边界（见 docs/design/distribution.md）：
//   - 只做一级分销：一个用户至多一个推荐人，推荐人就是最终受益人，不存在层级链/多级返佣；
//   - 佣金记录是「应收/已结算事实」，**不是可消费余额**，不进入钱包、无提现入口；
//   - 金额一律整数分（1 元 = 100 分），比例一律万分比（500 = 5%）。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 佣金状态（DB TINYINT 10/20/30；API 出参与查询过滤使用字符串枚举）。
const (
	CommissionStatusPending  = "pending"  // 待结算（10）
	CommissionStatusSettled  = "settled"  // 已结算（20）
	CommissionStatusReversed = "reversed" // 已冲回（30，终态）
)

// 冲回原因（distribution_commissions.reversal_reason）。
const (
	ReversalReasonRefund = "refund" // 退款成功触发
	ReversalReasonCancel = "cancel" // 订单取消触发（V1 未使用，预留）
)

// Referral 是推荐关系（被推荐人 → 一级推荐人），一次性绑定、不可改绑。
type Referral struct {
	UserId           int64       `json:"user_id" dc:"被推荐人（下级）用户 id"`
	ReferrerUserId   int64       `json:"referrer_user_id" dc:"一级推荐人用户 id（佣金受益人）"`
	ReferrerUsername string      `json:"referrer_username" dc:"推荐人用户名（实时联查；用户删除时为空串）"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"绑定时间"`
}

// Commission 是一条订单佣金记录（金额/比例均为快照）。
type Commission struct {
	Id                int64       `json:"id" dc:"佣金记录 id"`
	OrderId           int64       `json:"order_id" dc:"订单 id（一单至多一条佣金）"`
	OrderNo           string      `json:"order_no" dc:"订单号（实时联查；订单删除时为空串）"`
	BuyerUserId       int64       `json:"buyer_user_id" dc:"买家用户 id（下单时快照）"`
	BeneficiaryUserId int64       `json:"beneficiary_user_id" dc:"受益推荐人用户 id（下单时快照）"`
	BaseAmount        int64       `json:"base_amount" dc:"计佣基数（整数分，商品实收金额快照，不含运费）"`
	RateBp            int         `json:"rate_bp" dc:"佣金比例（万分比快照，500=5%）"`
	CommissionAmount  int64       `json:"commission_amount" dc:"佣金金额（整数分快照，向下取整到分）"`
	Status            string      `json:"status" dc:"状态：pending=待结算、settled=已结算、reversed=已冲回（终态）"`
	SettledAt         *gtime.Time `json:"settled_at" dc:"结算时间；未结算为 null"`
	ReversedAmount    int64       `json:"reversed_amount" dc:"已冲回金额（整数分）；整单退款时等于 commission_amount"`
	ReversedAt        *gtime.Time `json:"reversed_at" dc:"冲回时间；未冲回为 null"`
	RefundId          *int64      `json:"refund_id" dc:"触发冲回的退款单 id；未冲回为 null"`
	ReversalReason    string      `json:"reversal_reason" dc:"冲回原因：refund/cancel；未冲回为空串"`
	CreatedAt         *gtime.Time `json:"created_at" dc:"创建时间"`
}

// BindReq 绑定推荐人（仅登录用户，作用于 Principal.UserID；一次性绑定、不可改绑）。
type BindReq struct {
	g.Meta         `path:"/referrals/bind" method:"post" tags:"分销" summary:"绑定推荐人"`
	ReferrerUserId int64 `json:"referrer_user_id" dc:"推荐人用户 id；等于本人返回 21003，不存在/已禁用返回 21005，成环返回 21004"`
}

// BindRes 绑定推荐人响应（重复绑定同一推荐人幂等返回既有关系）。
type BindRes struct {
	Referral
}

// MyReferralReq 查询本人推荐关系与直接下级数量。
type MyReferralReq struct {
	g.Meta `path:"/my/referral" method:"get" tags:"分销" summary:"查询本人推荐关系"`
}

// MyReferralRes 本人推荐关系响应；未绑定时 bound=false 且各字段为空。
type MyReferralRes struct {
	Bound            bool        `json:"bound" dc:"是否已绑定推荐人"`
	ReferrerUserId   *int64      `json:"referrer_user_id" dc:"我的推荐人用户 id；未绑定为 null"`
	ReferrerUsername string      `json:"referrer_username" dc:"我的推荐人用户名；未绑定或用户已删除为空串"`
	BoundAt          *gtime.Time `json:"bound_at" dc:"绑定时间；未绑定为 null"`
	InviteeCount     int         `json:"invitee_count" dc:"我直接推荐的下级人数（一级，不含多级）"`
}

// MyCommissionsReq 查询本人佣金明细（作为受益人，分页）。
type MyCommissionsReq struct {
	g.Meta `path:"/my/commissions" method:"get" tags:"分销" summary:"查询本人佣金明细"`
	Page   int    `json:"page" dc:"页码，默认 1；<1 按 1 处理"`
	Size   int    `json:"size" dc:"每页数量，默认 20，最大 100；越界按上限截断"`
	Status string `json:"status" dc:"状态过滤：pending/settled/reversed；空=全部，非法值返回 400"`
}

// MyCommissionsRes 佣金明细分页响应。
type MyCommissionsRes struct {
	Items []*Commission `json:"items" dc:"佣金明细（按 id 倒序）"`
	Total int           `json:"total" dc:"总记录数"`
	Page  int           `json:"page" dc:"当前页码"`
	Size  int           `json:"size" dc:"每页数量"`
}

// MyCommissionSummaryReq 查询本人佣金汇总（按状态聚合）。
type MyCommissionSummaryReq struct {
	g.Meta `path:"/my/commissions/summary" method:"get" tags:"分销" summary:"查询本人佣金汇总"`
}

// MyCommissionSummaryRes 佣金汇总响应。
type MyCommissionSummaryRes struct {
	PendingCount    int64 `json:"pending_count" dc:"待结算记录数"`
	PendingAmount   int64 `json:"pending_amount" dc:"待结算佣金合计（整数分）"`
	SettledCount    int64 `json:"settled_count" dc:"已结算记录数"`
	SettledAmount   int64 `json:"settled_amount" dc:"已结算佣金合计（整数分）"`
	ReversedCount   int64 `json:"reversed_count" dc:"已冲回记录数"`
	ReversedAmount  int64 `json:"reversed_amount" dc:"已冲回佣金合计（整数分）"`
	TotalCommission int64 `json:"total_commission" dc:"累计计提佣金合计（整数分，含已冲回）"`
}

// AdminReferralsReq 后台分页查询推荐关系（持 distribution:list）。
type AdminReferralsReq struct {
	g.Meta         `path:"/admin/referrals" method:"get" tags:"分销" summary:"后台推荐关系列表"`
	Page           *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size           *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
	UserId         *int64 `json:"user_id" dc:"被推荐人用户 id，精确匹配；<=0 返回 400"`
	ReferrerUserId *int64 `json:"referrer_user_id" dc:"推荐人用户 id，精确匹配；<=0 返回 400"`
}

// AdminReferralsRes 后台推荐关系分页响应。
type AdminReferralsRes struct {
	Items []*AdminReferralItem `json:"items" dc:"推荐关系列表（按 id 倒序）"`
	Total int                  `json:"total" dc:"总记录数"`
	Page  int                  `json:"page" dc:"当前页码"`
	Size  int                  `json:"size" dc:"每页数量"`
}

// AdminReferralItem 后台推荐关系列表项（含双方用户名）。
type AdminReferralItem struct {
	UserId           int64       `json:"user_id" dc:"被推荐人用户 id"`
	Username         string      `json:"username" dc:"被推荐人用户名（实时联查；用户删除时为空串）"`
	ReferrerUserId   int64       `json:"referrer_user_id" dc:"推荐人用户 id"`
	ReferrerUsername string      `json:"referrer_username" dc:"推荐人用户名（实时联查；用户删除时为空串）"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"绑定时间"`
}

// AdminCommissionsReq 后台分页查询佣金（持 distribution:list）。
type AdminCommissionsReq struct {
	g.Meta            `path:"/admin/commissions" method:"get" tags:"分销" summary:"后台佣金列表"`
	Page              *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size              *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
	OrderId           *int64 `json:"order_id" dc:"订单 id，精确匹配；<=0 返回 400"`
	BuyerUserId       *int64 `json:"buyer_user_id" dc:"买家用户 id，精确匹配；<=0 返回 400"`
	BeneficiaryUserId *int64 `json:"beneficiary_user_id" dc:"受益推荐人用户 id，精确匹配；<=0 返回 400"`
	Status            string `json:"status" dc:"状态过滤：pending/settled/reversed；空=全部，非法值返回 400"`
	StartTime         string `json:"start_time" dc:"创建时间起点，RFC3339（含时区偏移），左闭"`
	EndTime           string `json:"end_time" dc:"创建时间终点，RFC3339（含时区偏移），右开"`
	Sort              string `json:"sort" dc:"排序字段白名单 id/created_at，默认 id，非法返回 400"`
	Order             string `json:"order" dc:"排序方向 asc/desc，默认 desc，非法返回 400"`
}

// AdminCommissionsRes 后台佣金分页响应。
type AdminCommissionsRes struct {
	Items []*Commission `json:"items" dc:"佣金列表（按排序字段倒序，默认 id DESC）"`
	Total int           `json:"total" dc:"总记录数"`
	Page  int           `json:"page" dc:"当前页码"`
	Size  int           `json:"size" dc:"每页数量"`
}

// AdminCommissionReq 后台查询佣金详情（持 distribution:view）。
type AdminCommissionReq struct {
	g.Meta `path:"/admin/commissions/:id" method:"get" tags:"分销" summary:"后台佣金详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"佣金记录 id"`
}

// AdminCommissionRes 佣金详情响应。
type AdminCommissionRes struct {
	Commission
}

// AdminSettleCommissionReq 后台结算佣金（持 distribution:settle；仅 pending→settled）。
type AdminSettleCommissionReq struct {
	g.Meta `path:"/admin/commissions/:id/settle" method:"post" tags:"分销" summary:"结算佣金"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"佣金记录 id"`
}

// AdminSettleCommissionRes 结算佣金响应（幂等：已结算重复调用返回当前记录）。
type AdminSettleCommissionRes struct {
	Commission
}
