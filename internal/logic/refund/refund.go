// Package refund 实现「已支付未发货普通订单仅退款」业务闭环：
// 发起申请、撤销、审核（同意/拒绝）、执行退款并确认结果（成功/失败）、退款失败后人工关闭，
// 以及用户/管理员查询与审计（append-only）。
//
// 关键正确性边界（与 docs/design/refund.md 一致）：
//   - 退款与发货互斥：申请与发货用「同一 orders 行 SELECT ... FOR UPDATE」串行化；
//     退款成功与发货用「订单 status=20 条件更新」行锁闸门。
//   - 退款执行幂等/只补偿一次：退款成功单事务「订单 20→70 + 库存恢复一次 + 退款状态→60」。
//   - 关闭仅从「退款失败(30)」迁入（条件更新 WHERE status=30）。
package refund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 退款状态（DB TINYINT，见 docs/design/refund.md 状态机）。
const (
	refundStatusApplying        = 10 // 申请中
	refundStatusPendingRefund   = 20 // 待退款
	refundStatusRefundFailed    = 30 // 退款失败
	refundStatusReviewRejected  = 40 // 审核拒绝（终态）
	refundStatusRevoked         = 50 // 已撤销（终态）
	refundStatusRefundSucceeded = 60 // 退款成功（终态）
	refundStatusClosed          = 70 // 已关闭（终态）
)

// 有效退款状态（阻塞发货、阻止重复申请）集合。
var activeRefundStatuses = []int{refundStatusApplying, refundStatusPendingRefund, refundStatusRefundFailed}

// 订单状态 paid(20)：与 internal/logic/order 状态机一致（order 模块未导出常量，此处按设计文档硬编码）。
const orderStatusPaid = 20

// 审计动作（refund_audits.action）。
const (
	auditActionApply          = "apply"
	auditActionReviewApprove  = "review_approve"
	auditActionReviewReject   = "review_reject"
	auditActionExecuteSuccess = "execute_success"
	auditActionExecuteFailure = "execute_failure"
	auditActionClose          = "close"
	auditActionRevoke         = "revoke"
)

// 审计操作者类型（refund_audits.operator_type）。
const (
	operatorTypeUser  = 1
	operatorTypeAdmin = 2
)

// maxReasonLen 是退款相关原因的最大字符数（与 VARCHAR(255) 对齐）。
const maxReasonLen = 255

// 后台退款列表分页默认值/上限。
const (
	adminDefaultPage = 1
	adminDefaultSize = 20
	adminMaxSize     = 100
)

type sRefund struct{}

func init() {
	service.RegisterRefund(New())
}

// New 创建并返回退款服务实现。
func New() *sRefund {
	return &sRefund{}
}

// refundRow 是 refund_requests 表的一条记录。
type refundRow struct {
	Id            int64       `json:"id"`
	OrderId       int64       `json:"order_id"`
	UserId        int64       `json:"user_id"`
	Status        int         `json:"status"`
	Amount        int64       `json:"amount"`
	Reason        string      `json:"reason"`
	ReviewReason  string      `json:"review_reason"`
	ReviewedBy    *int64      `json:"reviewed_by"`
	ReviewedAt    *gtime.Time `json:"reviewed_at"`
	ExecuteReason string      `json:"execute_reason"`
	ExecutedBy    *int64      `json:"executed_by"`
	ExecutedAt    *gtime.Time `json:"executed_at"`
	CreatedAt     *gtime.Time `json:"created_at"`
	UpdatedAt     *gtime.Time `json:"updated_at"`
}

// auditRow 是 refund_audits 表的一条记录。
type auditRow struct {
	Id               int64       `json:"id"`
	RefundId         int64       `json:"refund_id"`
	Action           string      `json:"action"`
	OperatorType     int         `json:"operator_type"`
	OperatorId       int64       `json:"operator_id"`
	OperatorUsername string      `json:"operator_username"`
	BeforeStatus     int         `json:"before_status"`
	AfterStatus      int         `json:"after_status"`
	Reason           string      `json:"reason"`
	CreatedAt        *gtime.Time `json:"created_at"`
}

// orderSnapshot 是 Apply 锁定时读取的订单行关键字段。
type orderSnapshot struct {
	UserId      int64
	Status      int
	TotalAmount int64
}

// Apply 发起退款申请：事务内锁定订单行 → 校验本人 + 已支付未发货 → 写退款申请（uk_active 兜底）+ 审计。
// 金额 = orders.total_amount（服务端派生，不信任客户端金额）。
func (s *sRefund) Apply(ctx context.Context, userID, orderID int64, reason string) (*v1.ApplyRes, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(reason) > maxReasonLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if orderID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	username, err := findUsername(ctx, userID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		// Auth 已校验用户存在，此处仅防御（不泄漏底层细节）。
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户用户名失败"))
	}

	var refundID int64
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		snap, e := lockOrderInTx(ctx, tx, orderID)
		if e != nil {
			return e
		}
		if snap == nil {
			// 订单不存在与非本人统一 18001（404，防枚举），不产生任何写入。
			return codes.New(codes.CodeRefundNotFound)
		}
		if snap.UserId != userID {
			return codes.New(codes.CodeRefundNotFound)
		}
		if snap.Status != orderStatusPaid {
			return codes.New(codes.CodeRefundOrderNotRefundable)
		}

		id, e := tx.Model("refund_requests").Ctx(ctx).Data(g.Map{
			"order_id": orderID,
			"user_id":  userID,
			"status":   refundStatusApplying,
			"amount":   snap.TotalAmount,
			"reason":   reason,
		}).InsertAndGetId()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				// uk_active 兜底：同一订单已存在有效退款申请。
				return codes.New(codes.CodeRefundAlreadyExists)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入退款申请: %w", e))
		}
		refundID = id

		if e := s.insertAudit(ctx, tx, id, auditActionApply, operatorTypeUser, userID, username, 0, refundStatusApplying, reason); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	r, err := s.loadRefundByID(ctx, refundID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("发起退款申请后未找到记录"))
	}
	return &v1.ApplyRes{Refund: *r}, nil
}

// Revoke 撤销本人申请（仅申请中 10→50）：不恢复库存、不改订单状态，可再次申请。
func (s *sRefund) Revoke(ctx context.Context, userID, id int64) (*v1.RevokeRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findOwnedRefund(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}

	username, err := findUsername(ctx, userID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户用户名失败"))
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		before, e := s.lockRefundStatusInTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if before == 0 {
			return codes.New(codes.CodeRefundNotFound)
		}
		if before != refundStatusApplying {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		result, e := tx.Model("refund_requests").Ctx(ctx).
			Where("id", id).
			Where("status", refundStatusApplying).
			Data(g.Map{"status": refundStatusRevoked}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销退款申请: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		return s.insertAudit(ctx, tx, id, auditActionRevoke, operatorTypeUser, userID, username, refundStatusApplying, refundStatusRevoked, "")
	})
	if err != nil {
		return nil, err
	}

	rr, err := s.loadRefundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rr == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销退款申请后未找到记录"))
	}
	return &v1.RevokeRes{Refund: *rr}, nil
}

// Detail 查询本人退款详情；不存在或非本人统一 18001。
func (s *sRefund) Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findOwnedRefund(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}
	return &v1.DetailRes{Refund: *toRefund(r)}, nil
}

// List 查询本人退款记录列表（按 id 倒序，可选 order_id 过滤）。
func (s *sRefund) List(ctx context.Context, userID int64, orderID *int64) (*v1.ListRes, error) {
	model := g.DB().Model("refund_requests").Ctx(ctx).Where("user_id", userID)
	if orderID != nil && *orderID > 0 {
		model = model.Where("order_id", *orderID)
	}
	var rows []*refundRow
	if err := model.Order("id DESC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询退款列表: %w", err))
	}
	items := make([]*v1.Refund, 0, len(rows))
	for _, r := range rows {
		items = append(items, toRefund(r))
	}
	return &v1.ListRes{Items: items}, nil
}

// AdminList 后台分页查询全部退款申请（支持 order_id/user_id/status 过滤，按 id 倒序）。
func (s *sRefund) AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error) {
	page, size, err := s.adminPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("refund_requests").Ctx(ctx)
	if req.OrderId != nil {
		if *req.OrderId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("order_id", *req.OrderId)
	}
	if req.UserId != nil {
		if *req.UserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("user_id", *req.UserId)
	}
	if status := strings.TrimSpace(req.Status); status != "" {
		st, e := refundStatusFromString(status)
		if e != nil {
			return nil, e
		}
		model = model.Where("status", st)
	}

	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计退款申请: %w", err))
	}
	var rows []*refundRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询退款列表: %w", err))
	}
	items := make([]*v1.Refund, 0, len(rows))
	for _, r := range rows {
		items = append(items, toRefund(r))
	}
	return &v1.AdminListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// AdminDetail 后台按 id 查询任意退款详情；不存在返回 18001。
func (s *sRefund) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findByRefundID(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}
	return &v1.AdminDetailRes{Refund: *toRefund(r)}, nil
}

// ListAudits 查询退款审计记录（append-only，按 id 升序）。
func (s *sRefund) ListAudits(ctx context.Context, id int64) (*v1.ListAuditsRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	// 退款不存在时返回 18001（详情/审计同样防枚举）。
	if r, err := s.findByRefundID(ctx, id); err != nil {
		return nil, err
	} else if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}
	var rows []*auditRow
	if err := g.DB().Model("refund_audits").Ctx(ctx).Where("refund_id", id).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询退款审计: %w", err))
	}
	items := make([]*v1.RefundAudit, 0, len(rows))
	for _, r := range rows {
		items = append(items, toAudit(r))
	}
	return &v1.ListAuditsRes{Items: items}, nil
}

// Review 审核退款申请（同意→待退款、拒绝→审核拒绝，仅申请中 10）。
func (s *sRefund) Review(ctx context.Context, adminID int64, req *v1.ReviewReq) (*v1.ReviewRes, error) {
	if req.Result != v1.ReviewResultApprove && req.Result != v1.ReviewResultReject {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(reason) > maxReasonLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findByRefundID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}

	username, err := findAdminUsername(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名失败"))
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		before, e := s.lockRefundStatusInTx(ctx, tx, req.Id)
		if e != nil {
			return e
		}
		if before == 0 {
			return codes.New(codes.CodeRefundNotFound)
		}
		if before != refundStatusApplying {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		target := refundStatusReviewRejected
		action := auditActionReviewReject
		if req.Result == v1.ReviewResultApprove {
			target = refundStatusPendingRefund
			action = auditActionReviewApprove
		}
		result, e := tx.Model("refund_requests").Ctx(ctx).
			Where("id", req.Id).
			Where("status", refundStatusApplying).
			Data(g.Map{
				"status":        target,
				"review_reason": reason,
				"reviewed_by":   adminID,
				"reviewed_at":   gdb.Raw("NOW()"),
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("审核退款申请: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		return s.insertAudit(ctx, tx, req.Id, action, operatorTypeAdmin, adminID, username, refundStatusApplying, target, reason)
	})
	if err != nil {
		return nil, err
	}

	rr, err := s.loadRefundByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if rr == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("审核退款申请后未找到记录"))
	}
	return &v1.ReviewRes{Refund: *rr}, nil
}

// Execute 执行退款并确认结果：
//   - success：待退款(20) 或 退款失败(30) → 退款成功(60)，单事务「先订单 20→70 闸门 + 恢复库存，命中再推进退款→60」；
//     已成功(60) 幂等返回；订单闸门 0 行（已发货/非 20）整体回滚。
//   - failure：仅待退款(20) → 退款失败(30)，订单/库存不变。
//
// 锁顺序遵循 Contract：success 先锁 orders（与 Ship/Apply 串行化），命中后再锁 refund（orders → refund，
// 与 Apply 一致，避免跨表死锁）。
func (s *sRefund) Execute(ctx context.Context, adminID int64, req *v1.ExecuteReq) (*v1.ExecuteRes, error) {
	if req.Result != v1.ExecuteResultSuccess && req.Result != v1.ExecuteResultFailure {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(reason) > maxReasonLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findByRefundID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}

	username, err := findAdminUsername(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名失败"))
	}

	if req.Result == v1.ExecuteResultSuccess {
		// 顺序幂等快速路径：已退款成功直接返回（不重复推进、不重复恢复库存）。
		if r.Status == refundStatusRefundSucceeded {
			return s.executeRes(ctx, req.Id)
		}
		if r.Status != refundStatusPendingRefund && r.Status != refundStatusRefundFailed {
			return nil, codes.New(codes.CodeRefundInvalidStatusTransition)
		}

		err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
			// 先订单闸门：orders 20→70 + 恢复库存；0 行（已发货/非 20）→ 返回错误整体回滚，
			// 与 Ship 的订单行锁互斥，杜绝「已发货 + 退款成功」及「退款=60 但订单=30」中间态。
			if e := service.Order().CompleteRefundInTx(ctx, tx, r.OrderId); e != nil {
				return e
			}
			// 命中后再锁读退款状态（orders → refund 顺序，与 Apply/Ship 一致）。
			before, e := s.lockRefundStatusInTx(ctx, tx, req.Id)
			if e != nil {
				return e
			}
			if before == 0 {
				return codes.New(codes.CodeRefundNotFound)
			}
			if before != refundStatusPendingRefund && before != refundStatusRefundFailed {
				return codes.New(codes.CodeRefundInvalidStatusTransition)
			}
			result, e := tx.Model("refund_requests").Ctx(ctx).
				Where("id", req.Id).
				Where("status", before).
				Data(g.Map{
					"status":         refundStatusRefundSucceeded,
					"execute_reason": reason,
					"executed_by":    adminID,
					"executed_at":    gdb.Raw("NOW()"),
				}).Update()
			if e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进退款成功: %w", e))
			}
			if affected, _ := result.RowsAffected(); affected == 0 {
				return codes.New(codes.CodeRefundInvalidStatusTransition)
			}
			return s.insertAudit(ctx, tx, req.Id, auditActionExecuteSuccess, operatorTypeAdmin, adminID, username, before, refundStatusRefundSucceeded, reason)
		})
		if err != nil {
			return nil, err
		}
		return s.executeRes(ctx, req.Id)
	}

	// failure：仅待退款(20) → 退款失败(30)，订单/库存不变。
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		before, e := s.lockRefundStatusInTx(ctx, tx, req.Id)
		if e != nil {
			return e
		}
		if before == 0 {
			return codes.New(codes.CodeRefundNotFound)
		}
		if before != refundStatusPendingRefund {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		result, e := tx.Model("refund_requests").Ctx(ctx).
			Where("id", req.Id).
			Where("status", refundStatusPendingRefund).
			Data(g.Map{
				"status":         refundStatusRefundFailed,
				"execute_reason": reason,
				"executed_by":    adminID,
				"executed_at":    gdb.Raw("NOW()"),
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进退款失败: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		return s.insertAudit(ctx, tx, req.Id, auditActionExecuteFailure, operatorTypeAdmin, adminID, username, refundStatusPendingRefund, refundStatusRefundFailed, reason)
	})
	if err != nil {
		return nil, err
	}
	return s.executeRes(ctx, req.Id)
}

// executeRes 加载退款详情并构造执行响应。
func (s *sRefund) executeRes(ctx context.Context, id int64) (*v1.ExecuteRes, error) {
	rr, err := s.loadRefundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rr == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("执行退款后未找到记录"))
	}
	return &v1.ExecuteRes{Refund: *rr}, nil
}

// Close 关闭退款（仅退款失败 30→70，恢复履约）：不恢复库存、不改订单状态、解除发货阻塞。
func (s *sRefund) Close(ctx context.Context, adminID int64, req *v1.CloseReq) (*v1.CloseRes, error) {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(reason) > maxReasonLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findByRefundID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeRefundNotFound)
	}

	username, err := findAdminUsername(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名失败"))
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		before, e := s.lockRefundStatusInTx(ctx, tx, req.Id)
		if e != nil {
			return e
		}
		if before == 0 {
			return codes.New(codes.CodeRefundNotFound)
		}
		if before != refundStatusRefundFailed {
			// 关闭仅允许从明确「退款失败」迁入，杜绝未确认失败即关闭导致的重复退款/退款后继续发货。
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		result, e := tx.Model("refund_requests").Ctx(ctx).
			Where("id", req.Id).
			Where("status", refundStatusRefundFailed).
			Data(g.Map{
				"status":         refundStatusClosed,
				"execute_reason": reason,
				"executed_by":    adminID,
				"executed_at":    gdb.Raw("NOW()"),
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("关闭退款: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeRefundInvalidStatusTransition)
		}
		return s.insertAudit(ctx, tx, req.Id, auditActionClose, operatorTypeAdmin, adminID, username, refundStatusRefundFailed, refundStatusClosed, reason)
	})
	if err != nil {
		return nil, err
	}

	rr, err := s.loadRefundByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if rr == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("关闭退款后未找到记录"))
	}
	return &v1.CloseRes{Refund: *rr}, nil
}

// HasActiveRequestInTx 在事务内判断订单是否存在有效退款申请（status ∈ {10,20,30}）。
// 供订单模块发货事务内检查退款互斥。
func (s *sRefund) HasActiveRequestInTx(ctx context.Context, tx gdb.TX, orderID int64) (bool, error) {
	n, err := tx.Model("refund_requests").Ctx(ctx).
		Where("order_id", orderID).
		WhereIn("status", activeRefundStatuses).
		Count()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询有效退款申请: %w", err))
	}
	return n > 0, nil
}

// lockOrderInTx 事务内锁定并读取订单行关键字段，未命中返回 nil。
func lockOrderInTx(ctx context.Context, tx gdb.TX, orderID int64) (*orderSnapshot, error) {
	record, err := tx.Model("orders").Ctx(ctx).
		Fields("user_id", "status", "total_amount").
		Where("id", orderID).
		LockUpdate().
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定订单: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &orderSnapshot{
		UserId:      record["user_id"].Int64(),
		Status:      record["status"].Int(),
		TotalAmount: record["total_amount"].Int64(),
	}, nil
}

// lockRefundStatusInTx 事务内锁定退款行并读取当前状态；不存在返回 0。
func (s *sRefund) lockRefundStatusInTx(ctx context.Context, tx gdb.TX, id int64) (int, error) {
	record, err := tx.Model("refund_requests").Ctx(ctx).
		Fields("status").
		Where("id", id).
		LockUpdate().
		One()
	if err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定退款申请: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return 0, nil
	}
	return record["status"].Int(), nil
}

// insertAudit 在事务内写入退款审计记录（append-only）。
func (s *sRefund) insertAudit(ctx context.Context, tx gdb.TX, refundID int64, action string, operatorType int, operatorID int64, operatorUsername string, beforeStatus, afterStatus int, reason string) error {
	if _, err := tx.Model("refund_audits").Ctx(ctx).Data(g.Map{
		"refund_id":         refundID,
		"action":            action,
		"operator_type":     operatorType,
		"operator_id":       operatorID,
		"operator_username": operatorUsername,
		"before_status":     beforeStatus,
		"after_status":      afterStatus,
		"reason":            reason,
	}).Insert(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入退款审计: %w", err))
	}
	return nil
}

// findByRefundID 按 id 查询退款行（后台无归属过滤），未命中返回 nil。
func (s *sRefund) findByRefundID(ctx context.Context, id int64) (*refundRow, error) {
	var rows []*refundRow
	if err := g.DB().Model("refund_requests").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询退款申请: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findOwnedRefund 按 id + user_id 查询退款行，未命中返回 nil。
func (s *sRefund) findOwnedRefund(ctx context.Context, userID, id int64) (*refundRow, error) {
	var rows []*refundRow
	if err := g.DB().Model("refund_requests").Ctx(ctx).Where("id", id).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询本人退款申请: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadRefundByID 按 id 加载退款详情，未命中返回 nil。
func (s *sRefund) loadRefundByID(ctx context.Context, id int64) (*v1.Refund, error) {
	r, err := s.findByRefundID(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return toRefund(r), nil
}

// toRefund 将退款行转换为对外结构。
func toRefund(r *refundRow) *v1.Refund {
	return &v1.Refund{
		Id:            r.Id,
		OrderId:       r.OrderId,
		UserId:        r.UserId,
		Status:        refundStatusToString(r.Status),
		Amount:        r.Amount,
		Reason:        r.Reason,
		ReviewReason:  r.ReviewReason,
		ReviewedBy:    r.ReviewedBy,
		ReviewedAt:    r.ReviewedAt,
		ExecuteReason: r.ExecuteReason,
		ExecutedBy:    r.ExecutedBy,
		ExecutedAt:    r.ExecutedAt,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

// toAudit 将审计行转换为对外结构。
func toAudit(r *auditRow) *v1.RefundAudit {
	return &v1.RefundAudit{
		Id:               r.Id,
		RefundId:         r.RefundId,
		Action:           r.Action,
		OperatorType:     r.OperatorType,
		OperatorId:       r.OperatorId,
		OperatorUsername: r.OperatorUsername,
		BeforeStatus:     r.BeforeStatus,
		AfterStatus:      r.AfterStatus,
		Reason:           r.Reason,
		CreatedAt:        r.CreatedAt,
	}
}

// refundStatusToString 将 DB 状态映射为 API 字符串枚举。
func refundStatusToString(s int) string {
	switch s {
	case refundStatusApplying:
		return v1.StatusApplying
	case refundStatusPendingRefund:
		return v1.StatusPendingRefund
	case refundStatusRefundFailed:
		return v1.StatusRefundFailed
	case refundStatusReviewRejected:
		return v1.StatusReviewRejected
	case refundStatusRevoked:
		return v1.StatusRevoked
	case refundStatusRefundSucceeded:
		return v1.StatusRefundSucceeded
	case refundStatusClosed:
		return v1.StatusClosed
	default:
		return ""
	}
}

// refundStatusFromString 将 API 字符串枚举映射回 DB 状态；未知状态返回错误。
func refundStatusFromString(s string) (int, error) {
	switch s {
	case v1.StatusApplying:
		return refundStatusApplying, nil
	case v1.StatusPendingRefund:
		return refundStatusPendingRefund, nil
	case v1.StatusRefundFailed:
		return refundStatusRefundFailed, nil
	case v1.StatusReviewRejected:
		return refundStatusReviewRejected, nil
	case v1.StatusRevoked:
		return refundStatusRevoked, nil
	case v1.StatusRefundSucceeded:
		return refundStatusRefundSucceeded, nil
	case v1.StatusClosed:
		return refundStatusClosed, nil
	default:
		return 0, codes.New(codes.CodeInvalidArgument)
	}
}

// adminPageParams 校验分页参数：缺失取默认值，page<1 或 size<1/size>100 返回 400。
func (s *sRefund) adminPageParams(page, size *int) (int, int, error) {
	p := adminDefaultPage
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	sz := adminDefaultSize
	if size != nil {
		if *size < 1 || *size > adminMaxSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		sz = *size
	}
	return p, sz, nil
}

// findUsername 查询普通用户用户名（审计操作者快照），不存在返回空串。
func findUsername(ctx context.Context, userID int64) (string, error) {
	record, err := g.DB().Model("users").Ctx(ctx).Fields("username").Where("id", userID).One()
	if err != nil {
		return "", codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户用户名: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return "", nil
	}
	return record["username"].String(), nil
}

// findAdminUsername 查询管理员用户名（审计操作者快照），不存在返回空串。
func findAdminUsername(ctx context.Context, adminID int64) (string, error) {
	record, err := g.DB().Model("admins").Ctx(ctx).Fields("username").Where("id", adminID).One()
	if err != nil {
		return "", codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return "", nil
	}
	return record["username"].String(), nil
}

// duplicateKeyName 从错误链中提取 MySQL 唯一约束冲突（1062）的键名，非重复键错误返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}
