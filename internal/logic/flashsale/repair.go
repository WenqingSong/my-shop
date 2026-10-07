// 秒杀 V4（故障恢复）：人工修复入口 + 审计记录。
// 管理员经后台接口修复异常请求（仅 dead→queued），修复与审计记录同一事务写入 flash_sale_request_audits
// （append-only，无 UPDATE/DELETE 接口），实现「授权与审计原子性、审计不可篡改」。
package flashsale

import (
	"context"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// repairActionDeadToQueued 是「死信 → 重排队」修复动作（audit.action）。
const repairActionDeadToQueued = "dead_to_queued"

// requestAuditRow 是 flash_sale_request_audits 表的一条记录。
type requestAuditRow struct {
	Id               int64       `json:"id"`
	RequestId        int64       `json:"request_id"`
	OperatorAdminId  int64       `json:"operator_admin_id"`
	OperatorUsername string      `json:"operator_username"`
	Action           string      `json:"action"`
	BeforeStatus     int         `json:"before_status"`
	AfterStatus      int         `json:"after_status"`
	Reason           string      `json:"reason"`
	CreatedAt        *gtime.Time `json:"created_at"`
}

// RepairRequest 人工修复异常请求（仅 dead→queued）：条件更新 WHERE status=dead 保证并发下仅迁移一次，
// 与审计记录同事务写入 flash_sale_request_audits（append-only）。调用前已由 RequirePermission("flash_sale:repair") 授权。
func (s *sFlashSale) RepairRequest(ctx context.Context, adminID int64, req *v1.RepairRequestReq) (*v1.RepairRequestRes, error) {
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.TargetStatus != v1.RequestStatusQueued {
		// 仅支持 dead→queued；其他目标状态属非法输入。
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	r, err := s.findRequestByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeNotFound)
	}
	if r.Status != requestStatusDead {
		return nil, codes.New(codes.CodeFlashSaleRequestNotRepairable)
	}

	username, err := findAdminUsername(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if username == "" {
		// AdminAuth 已校验管理员存在，此处仅防御（不泄漏底层细节）。
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名失败"))
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := tx.Model("flash_sale_order_requests").Ctx(ctx).
			Where("id", req.Id).
			Where("status", requestStatusDead).
			Data(g.Map{
				"status":          requestStatusQueued,
				"retry_count":     0,
				"next_attempt_at": nil,
				"last_error_code": nil,
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("修复秒杀请求状态: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			// 并发下已被其他管理员修复/状态已变化，仅 dead 可修复。
			return codes.New(codes.CodeFlashSaleRequestNotRepairable)
		}
		if _, e := tx.Model("flash_sale_request_audits").Ctx(ctx).Data(g.Map{
			"request_id":        req.Id,
			"operator_admin_id": adminID,
			"operator_username": username,
			"action":            repairActionDeadToQueued,
			"before_status":     requestStatusDead,
			"after_status":      requestStatusQueued,
			"reason":            reason,
		}).Insert(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入秒杀修复审计: %w", e))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &v1.RepairRequestRes{Id: req.Id, Status: v1.RequestStatusQueued}, nil
}

// ListRequestAudits 查询请求的修复审计记录（append-only，按时间升序）。
func (s *sFlashSale) ListRequestAudits(ctx context.Context, requestID int64) (*v1.ListRequestAuditsRes, error) {
	if requestID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	var rows []*requestAuditRow
	if err := g.DB().Model("flash_sale_request_audits").Ctx(ctx).
		Where("request_id", requestID).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀修复审计: %w", err))
	}
	items := make([]*v1.RequestAudit, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.RequestAudit{
			Id:               r.Id,
			RequestId:        r.RequestId,
			OperatorAdminId:  r.OperatorAdminId,
			OperatorUsername: r.OperatorUsername,
			Action:           r.Action,
			BeforeStatus:     r.BeforeStatus,
			AfterStatus:      r.AfterStatus,
			Reason:           r.Reason,
			CreatedAt:        r.CreatedAt,
		})
	}
	return &v1.ListRequestAuditsRes{Items: items}, nil
}

// findRequestByID 按 id 查询异步请求，未命中返回 nil。
func (s *sFlashSale) findRequestByID(ctx context.Context, id int64) (*orderRequestRow, error) {
	var rows []*orderRequestRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀请求: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
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
