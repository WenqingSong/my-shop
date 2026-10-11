// Package v1 定义退款（Refund）公开 API 契约：用户端发起/撤销/查询与后台审核/执行/关闭/查询。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 退款状态枚举（API 出参为字符串枚举；DB 存 TINYINT 10/20/30/40/50/60/70，见 docs/design/refund.md）。
const (
	StatusApplying        = "applying"         // 申请中（10）
	StatusPendingRefund   = "pending_refund"   // 待退款（20）
	StatusRefundFailed    = "refund_failed"    // 退款失败（30）
	StatusReviewRejected  = "review_rejected"  // 审核拒绝（40，终态）
	StatusRevoked         = "revoked"          // 已撤销（50，终态）
	StatusRefundSucceeded = "refund_succeeded" // 退款成功（60，终态）
	StatusClosed          = "closed"           // 已关闭（70，终态）
)

// 审核结果枚举。
const (
	ReviewResultApprove = "approve"
	ReviewResultReject  = "reject"
)

// 执行结果枚举（人工 Mock 确认）。
const (
	ExecuteResultSuccess = "success"
	ExecuteResultFailure = "failure"
)

// Refund 退款申请（查询与写接口复用）。
type Refund struct {
	Id            int64       `json:"id" dc:"退款申请 id"`
	OrderId       int64       `json:"order_id" dc:"订单 id（软引用）"`
	UserId        int64       `json:"user_id" dc:"申请者（订单所有者）id"`
	Status        string      `json:"status" dc:"状态：applying/pending_refund/refund_failed/review_rejected/revoked/refund_succeeded/closed"`
	Amount        int64       `json:"amount" dc:"退款金额（整数分，= 订单 total_amount 快照，服务端派生）"`
	Reason        string      `json:"reason" dc:"申请原因"`
	ReviewReason  string      `json:"review_reason" dc:"审核原因"`
	ReviewedBy    *int64      `json:"reviewed_by" dc:"审核管理员 id（可空）"`
	ReviewedAt    *gtime.Time `json:"reviewed_at" dc:"审核时间（可空）"`
	ExecuteReason string      `json:"execute_reason" dc:"执行/关闭原因"`
	ExecutedBy    *int64      `json:"executed_by" dc:"执行/关闭管理员 id（可空）"`
	ExecutedAt    *gtime.Time `json:"executed_at" dc:"执行/关闭时间（可空）"`
	CreatedAt     *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt     *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ApplyReq 发起退款申请请求（仅本人、已支付未发货订单）。
type ApplyReq struct {
	g.Meta `path:"/orders/:id/refund" method:"post" tags:"退款" summary:"发起退款申请"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"订单 id"`
	Reason string `json:"reason" dc:"申请原因（必填）"`
}

// ApplyRes 发起退款申请响应。
type ApplyRes struct {
	Refund
}

// RevokeReq 撤销退款申请请求（仅本人、申请中）。
type RevokeReq struct {
	g.Meta `path:"/refunds/:id/revoke" method:"post" tags:"退款" summary:"撤销退款申请"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"退款申请 id"`
}

// RevokeRes 撤销退款申请响应。
type RevokeRes struct {
	Refund
}

// DetailReq 查询本人退款详情请求。
type DetailReq struct {
	g.Meta `path:"/refunds/:id" method:"get" tags:"退款" summary:"查询本人退款详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"退款申请 id"`
}

// DetailRes 退款详情响应。
type DetailRes struct {
	Refund
}

// ListReq 查询本人退款记录列表请求（可选 order_id 过滤）。
type ListReq struct {
	g.Meta  `path:"/refunds" method:"get" tags:"退款" summary:"查询本人退款记录列表"`
	OrderId int64 `json:"order_id" dc:"按订单过滤；<=0 忽略"`
}

// ListRes 退款记录列表响应。
type ListRes struct {
	Items []*Refund `json:"items" dc:"退款记录列表（按 id 倒序）"`
}

// AdminListReq 后台退款分页列表请求（只读，持 refund:list）。
type AdminListReq struct {
	g.Meta  `path:"/admin/refunds" method:"get" tags:"退款" summary:"后台退款分页列表"`
	Page    *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size    *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	OrderId *int64 `json:"order_id" dc:"订单 id，精确匹配；<=0 返回 400"`
	UserId  *int64 `json:"user_id" dc:"用户 id，精确匹配；<=0 返回 400"`
	Status  string `json:"status" dc:"状态字符串枚举，非法返回 400"`
}

// AdminListRes 后台退款分页列表响应。
type AdminListRes struct {
	Items []*Refund `json:"items" dc:"退款列表（按 id 倒序）"`
	Total int       `json:"total" dc:"总记录数"`
	Page  int       `json:"page" dc:"当前页码"`
	Size  int       `json:"size" dc:"每页数量"`
}

// AdminDetailReq 后台退款详情请求（只读，持 refund:view）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/refunds/:id" method:"get" tags:"退款" summary:"后台退款详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"退款申请 id"`
}

// AdminDetailRes 后台退款详情响应。
type AdminDetailRes struct {
	Refund
}

// ListAuditsReq 查询退款审计记录请求（只读，持 refund:view）。
type ListAuditsReq struct {
	g.Meta `path:"/admin/refunds/:id/audits" method:"get" tags:"退款" summary:"查询退款审计记录"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"退款申请 id"`
}

// ListAuditsRes 退款审计记录响应。
type ListAuditsRes struct {
	Items []*RefundAudit `json:"items" dc:"审计记录列表（按 id 升序）"`
}

// RefundAudit 退款审计记录（append-only，不可修改删除）。
type RefundAudit struct {
	Id               int64       `json:"id" dc:"审计记录 id"`
	RefundId         int64       `json:"refund_id" dc:"退款申请 id"`
	Action           string      `json:"action" dc:"动作：apply/review_approve/review_reject/execute_success/execute_failure/close/revoke"`
	OperatorType     int         `json:"operator_type" dc:"操作者类型：1=user、2=admin"`
	OperatorId       int64       `json:"operator_id" dc:"操作者 id"`
	OperatorUsername string      `json:"operator_username" dc:"操作者用户名快照"`
	BeforeStatus     int         `json:"before_status" dc:"操作前状态（apply 为 0）"`
	AfterStatus      int         `json:"after_status" dc:"操作后状态"`
	Reason           string      `json:"reason" dc:"原因"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"创建时间"`
}

// ReviewReq 审核退款申请请求（后台，持 refund:review）。
type ReviewReq struct {
	g.Meta `path:"/admin/refunds/:id/review" method:"post" tags:"退款" summary:"审核退款申请"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"退款申请 id"`
	Result string `json:"result" v:"required" dc:"审核结果：approve/reject"`
	Reason string `json:"reason" v:"required" dc:"审核原因"`
}

// ReviewRes 审核退款申请响应。
type ReviewRes struct {
	Refund
}

// ExecuteReq 执行退款请求（后台，持 refund:execute；人工 Mock 确认结果）。
type ExecuteReq struct {
	g.Meta `path:"/admin/refunds/:id/execute" method:"post" tags:"退款" summary:"执行退款并确认结果"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"退款申请 id"`
	Result string `json:"result" v:"required" dc:"执行结果：success/failure"`
	Reason string `json:"reason" v:"required" dc:"执行结果原因"`
}

// ExecuteRes 执行退款响应。
type ExecuteRes struct {
	Refund
}

// CloseReq 关闭退款请求（后台，持 refund:execute；仅退款失败→已关闭）。
type CloseReq struct {
	g.Meta `path:"/admin/refunds/:id/close" method:"post" tags:"退款" summary:"关闭退款（恢复履约）"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"退款申请 id"`
	Reason string `json:"reason" v:"required" dc:"关闭原因"`
}

// CloseRes 关闭退款响应。
type CloseRes struct {
	Refund
}
