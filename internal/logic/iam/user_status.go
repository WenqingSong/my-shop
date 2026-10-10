package iam

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const (
	// userStatusActionEnable / userStatusActionDisable 是 user_status_audits.action 的取值。
	userStatusActionEnable  = "enable"
	userStatusActionDisable = "disable"
	// auditResultSuccess 是 user_status_audits.result 的取值（仅记录成功变更）。
	auditResultSuccess = 1
	// maxUserStatusReasonLen 是禁用/启用原因的最大字符数（与 user_status_audits.reason VARCHAR(255) 对齐）。
	maxUserStatusReasonLen = 255
)

// GetUserStatus 查询目标普通用户的账号状态（后台域，调用前已由 RequirePermission("user:read") 授权）。
// 目标不存在返回 2015（404），不泄露任何内部信息。
func (s *sIam) GetUserStatus(ctx context.Context, targetUserID int64) (*v1.GetUserStatusRes, error) {
	user, err := findUserStatus(ctx, targetUserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, codes.New(codes.CodeUserNotFound)
	}
	return &v1.GetUserStatusRes{Id: targetUserID, Status: user.Status}, nil
}

// UpdateUserStatus 禁用/启用目标普通用户（后台域，调用前已由 RequirePermission("user:status") 授权）。
// operatorAdminID 为已认证管理员 id（来自 AdminPrincipal），operator_username 为操作时 admins.username 快照。
//
// 事务内以 SELECT ... FOR UPDATE 锁定目标行，串行化并发状态变更，区分三态：
//   - 无行 → 2015（404，无任何写入）；
//   - status == 目标 → 幂等成功（no-op，不写审计、不递增版本、不撤销）；
//   - status != 目标 → 实际迁移：更新 status（禁用时 auth_epoch+1）+ 写审计 +（禁用）撤销该用户全部 refresh family。
//
// 事务提交后（仅禁用实际迁移）best-effort 撤销 Redis 会话，失败仅日志——每请求 status/版本校验兜底。
func (s *sIam) UpdateUserStatus(ctx context.Context, operatorAdminID int64, req *v1.UpdateUserStatusReq) (*v1.UpdateUserStatusRes, error) {
	if req.Status != userStatusEnabled && req.Status != userStatusDisabled {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(reason) > maxUserStatusReasonLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	operatorUsername, err := findAdminUsername(ctx, operatorAdminID)
	if err != nil {
		return nil, err
	}
	if operatorUsername == "" {
		// AdminAuth 已校验管理员存在，此处仅防御（不泄漏底层细节）。
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员用户名失败"))
	}

	// disabled 记录本次是否发生了实际「→禁用」迁移（据此在事务提交后联动撤销 Redis 会话）。
	disabled := false
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		record, e := tx.Model("users").Ctx(ctx).
			Fields("id", "status", "auth_epoch").
			Where("id", req.Id).
			LockUpdate().
			One()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定目标用户: %w", e))
		}
		if record == nil || record.IsEmpty() {
			return codes.New(codes.CodeUserNotFound)
		}
		beforeStatus := record["status"].Int()

		// 三态判定：已一致 → 幂等 no-op；否则实际迁移。
		if beforeStatus == req.Status {
			return nil
		}

		if req.Status == userStatusDisabled {
			// 仅「启用→禁用」实际迁移递增认证版本，使所有旧会话/旧 refresh 永久失效。
			if _, e := tx.Ctx(ctx).Exec(
				"UPDATE users SET status = 0, auth_epoch = auth_epoch + 1 WHERE id = ?",
				req.Id,
			); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("禁用用户: %w", e))
			}
		} else {
			if _, e := tx.Ctx(ctx).Exec(
				"UPDATE users SET status = 1 WHERE id = ?",
				req.Id,
			); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("启用用户: %w", e))
			}
		}

		// 写审计（仅实际迁移；before/after 反映真实迁移）。
		action := userStatusActionEnable
		if req.Status == userStatusDisabled {
			action = userStatusActionDisable
		}
		if _, e := tx.Model("user_status_audits").Ctx(ctx).Data(g.Map{
			"target_user_id":    req.Id,
			"operator_admin_id": operatorAdminID,
			"operator_username": operatorUsername,
			"action":            action,
			"before_status":     beforeStatus,
			"after_status":      req.Status,
			"reason":            reason,
			"result":            auditResultSuccess,
		}).Insert(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入用户状态审计: %w", e))
		}

		// 禁用场景：同事务撤销该用户全部 refresh family（与状态/版本/审计原子）。
		if req.Status == userStatusDisabled {
			if _, e := tx.Ctx(ctx).Exec(
				"UPDATE refresh_tokens SET revoked_at = NOW(), revoked_reason = ? WHERE user_id = ? AND revoked_at IS NULL",
				revokedReasonRevoked, req.Id,
			); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销 refresh family: %w", e))
			}
			disabled = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Redis 会话撤销在事务提交后 best-effort：失败仅日志，不使禁用失败（每请求 status/版本校验兜底）。
	if disabled {
		if err := auth.RevokeAllSessions(ctx, req.Id); err != nil {
			glog.Errorf(ctx, "禁用用户后撤销会话失败（status/版本校验兜底）: %v", err)
		}
	}

	return &v1.UpdateUserStatusRes{}, nil
}

// findUserStatus 按 id 查询前台用户的状态，不存在返回 nil。
func findUserStatus(ctx context.Context, id int64) (*userRecord, error) {
	record, err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "status").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询用户状态: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &userRecord{
		ID:     record["id"].Int64(),
		Status: record["status"].Int(),
	}, nil
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
