package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const (
	// revokedReasonRotated 表示 refresh token 因轮换而失效。
	revokedReasonRotated = "rotated"
	// revokedReasonRevoked 表示 refresh token 因主动撤销/登出/重放而失效。
	revokedReasonRevoked = "revoked"
)

// errRefreshNotRotated 是事务内条件 UPDATE 影响 0 行（并发后到者/已过期/已撤销）的内部信号，
// 调用方据此重读该行并按真实状态判定，不将其视为底层技术错误。
var errRefreshNotRotated = errors.New("refresh token 未被轮换（条件更新影响 0 行）")

// refreshTokenRow 是 refresh_tokens 表的一行。
type refreshTokenRow struct {
	ID            int64       `orm:"id"`
	TokenHash     string      `orm:"token_hash"`
	FamilyID      string      `orm:"family_id"`
	UserID        int64       `orm:"user_id"`
	ParentID      *int64      `orm:"parent_id"`
	Generation    int         `orm:"generation"`
	Sid           string      `orm:"sid"`
	AuthEpoch     int64       `orm:"auth_epoch"`
	ExpiresAt     *gtime.Time `orm:"expires_at"`
	RevokedAt     *gtime.Time `orm:"revoked_at"`
	RevokedReason *string     `orm:"revoked_reason"`
}

// Refresh 凭有效 refresh token 换取新 access token 与新 refresh token（轮换，复用同 sid）。
// 判定顺序（Contract）：hash 不存在/已撤销 → INVALID；已轮换 → REUSE（全量撤销）；过期 → EXPIRED；否则轮换。
// 轮换 = session upsert + 事务内条件 UPDATE 旧 token 置 rotated + INSERT 新后代 + 签新 access token。
func (s *sIam) Refresh(ctx context.Context, req *v1.RefreshReq, userAgent, ip string) (*v1.RefreshRes, error) {
	plaintext := strings.TrimSpace(req.RefreshToken)
	if plaintext == "" {
		return nil, codes.New(codes.CodeRefreshTokenInvalid)
	}
	tokenHash := auth.HashRefreshToken(plaintext)

	row, err := findRefreshByHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if row == nil {
		// 未知/无效 token：统一 INVALID，不泄露存在性。
		return nil, codes.New(codes.CodeRefreshTokenInvalid)
	}
	if err := s.respondRefreshRow(ctx, row); err != nil {
		return nil, err
	}

	// 校验账号状态与版本：读 users（status、auth_epoch），要求 status==1 且 row.auth_epoch == users.auth_epoch。
	// row.auth_epoch 是本次登录绑定的版本，绝不重读 users.auth_epoch 来签发新凭证；
	// 禁用用户 / 版本落后（禁用后旧 refresh）→ 1002，无任何副作用。
	user, err := findUserAuth(ctx, row.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.Status != userStatusEnabled || user.AuthEpoch != row.AuthEpoch {
		return nil, codes.New(codes.CodeUnauthorized)
	}

	// 有效 → session upsert（先于轮换；失败 500 且未轮换，重试安全）。
	ttl, err := auth.SessionTTL(ctx)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取会话 TTL: %w", err))
	}
	refreshTTL, err := auth.RefreshTTL(ctx)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取 refresh TTL: %w", err))
	}
	state, err := auth.UpsertSessionAlive(ctx, row.Sid, row.UserID, ttl, auth.SessionMeta{
		LoginAt:   gtime.Now().Unix(),
		UserAgent: userAgent,
		IP:        ip,
		AuthEpoch: row.AuthEpoch,
	})
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("确保会话存活: %w", err))
	}
	if state == auth.SessionUpsertRevoked {
		// 防御：sid 已撤销（理论不出现）→ 拒绝 refresh，不轮换。
		return nil, codes.New(codes.CodeRefreshTokenInvalid)
	}

	// 生成新 refresh token 明文与哈希（明文仅本次返回）。
	newPlaintext, err := auth.NewRefreshToken()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成新 refresh token: %w", err))
	}
	newHash := auth.HashRefreshToken(newPlaintext)

	rotated, err := rotateRefresh(ctx, row, newHash, refreshTTL)
	if err != nil {
		return nil, err
	}
	if !rotated {
		// 条件 UPDATE 影响 0 行：并发后到者（reuse）、恰逢过期或已被撤销，
		// 重读并按真实状态判定，不得统一按 reuse 处理。
		return nil, s.handleRefreshNotRotated(ctx, tokenHash)
	}

	// 签新 access token（复用同 sid，恒为 type=user，不信任请求体身份）。
	accessToken, err := auth.Generate(ctx, auth.TypeUser, row.UserID, row.Sid)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("签发 access token: %w", err))
	}

	return &v1.RefreshRes{
		AccessToken:  accessToken,
		RefreshToken: newPlaintext,
		TokenType:    "Bearer",
		ExpiresIn:    auth.ExpiresIn,
	}, nil
}

// classifyRefreshRow 返回 refresh 判定结果对应的业务错误码（无副作用）：
// reason='rotated' → REUSE；revoked_at 非空 → INVALID；已过期 → EXPIRED；否则 OK（可继续轮换）。
// 初始读取与「事务内条件 UPDATE 影响 0 行后的重读」共用，保证两条路径判定一致。
func classifyRefreshRow(row *refreshTokenRow) codes.Code {
	if row.RevokedReason != nil && *row.RevokedReason == revokedReasonRotated {
		return codes.CodeRefreshTokenReuse
	}
	if row.RevokedAt != nil {
		return codes.CodeRefreshTokenInvalid
	}
	if row.ExpiresAt == nil || row.ExpiresAt.Before(gtime.Now()) {
		return codes.CodeRefreshTokenExpired
	}
	return codes.CodeOK
}

// respondRefreshRow 按分类结果执行副作用并返回终端错误；分类为 OK 时返回 nil（可继续轮换）。
func (s *sIam) respondRefreshRow(ctx context.Context, row *refreshTokenRow) error {
	switch classifyRefreshRow(row) {
	case codes.CodeRefreshTokenReuse:
		// 已轮换 token 再次提交 → reuse：撤销该用户全部 families + 全部 sessions，不产生新 token。
		if err := s.revokeAllByUser(ctx, row.UserID); err != nil {
			return err
		}
		return codes.New(codes.CodeRefreshTokenReuse)
	case codes.CodeRefreshTokenInvalid:
		return codes.New(codes.CodeRefreshTokenInvalid)
	case codes.CodeRefreshTokenExpired:
		return codes.New(codes.CodeRefreshTokenExpired)
	default:
		return nil
	}
}

// handleRefreshNotRotated 处理事务内条件 UPDATE 影响 0 行：重读该行并按真实状态判定，
// 即并发后到者（reuse）/ 恰逢过期（expired）/ 已被撤销（invalid），不得统一按 reuse 处理。
func (s *sIam) handleRefreshNotRotated(ctx context.Context, tokenHash string) error {
	current, err := findRefreshByHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	if current == nil {
		// 理论不出现（refresh_tokens 行不被物理删除）；防御性按 INVALID 处理。
		return codes.New(codes.CodeRefreshTokenInvalid)
	}
	return s.respondRefreshRow(ctx, current)
}

// rotateRefresh 在事务内将旧 token 置 rotated 并 INSERT 新后代。
// 返回 rotated=true 表示本次成功轮换；false 表示条件 UPDATE 影响 0 行（并发后到者/已过期/已撤销）。
// 「旧 token 置 rotated + 新后代 INSERT」同事务，任一失败整体回滚，旧 token 保持可重试。
func rotateRefresh(ctx context.Context, old *refreshTokenRow, newTokenHash string, ttl int64) (bool, error) {
	rotated := false
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 条件 UPDATE：仅当「未撤销且未过期」时置 rotated；并发由 InnoDB 行锁串行化，
		// 后到事务在首事务提交后重估条件 → 影响 0 行。
		res, e := tx.Ctx(ctx).Exec(
			"UPDATE refresh_tokens SET revoked_at = NOW(), revoked_reason = ? WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > NOW()",
			revokedReasonRotated, old.TokenHash,
		)
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("轮换旧 refresh token: %w", e))
		}
		n, e := res.RowsAffected()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取轮换影响行数: %w", e))
		}
		if n == 0 {
			return errRefreshNotRotated
		}
		// INSERT 新后代：同 sid、同 family_id、parent_id=旧 id、generation+1、auth_epoch=旧行值（继承）、expires_at=now+ttl。
		if _, e := tx.Ctx(ctx).Exec(
			"INSERT INTO refresh_tokens (token_hash, family_id, user_id, parent_id, generation, sid, auth_epoch, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, DATE_ADD(NOW(), INTERVAL ? SECOND))",
			newTokenHash, old.FamilyID, old.UserID, old.ID, old.Generation+1, old.Sid, old.AuthEpoch, ttl,
		); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入新 refresh token: %w", e))
		}
		rotated = true
		return nil
	})
	if errors.Is(err, errRefreshNotRotated) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rotated, nil
}

// revokeAllByUser 撤销该用户全部 refresh families + 全部 access sessions（reuse 全量撤销）。
// family 撤销（MySQL）为权威；session 撤销（Redis）为 best-effort，失败仅记录日志，
// 由 session TTL 与后续鉴权校验兜底（非跨系统事务）。
func (s *sIam) revokeAllByUser(ctx context.Context, userID int64) error {
	if err := revokeAllFamiliesByUser(ctx, userID); err != nil {
		return err
	}
	if err := auth.RevokeAllSessions(ctx, userID); err != nil {
		glog.Errorf(ctx, "reuse 全量撤销会话失败（family 已撤销，session 兜底）: %v", err)
	}
	return nil
}

// findRefreshByHash 按 token_hash 查询 refresh_tokens；不存在返回 (nil, nil)。
func findRefreshByHash(ctx context.Context, tokenHash string) (*refreshTokenRow, error) {
	var rows []*refreshTokenRow
	if err := g.DB().Model("refresh_tokens").Ctx(ctx).
		Where("token_hash", tokenHash).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询 refresh token: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// insertRefreshRoot 登录时写入 refresh family 根（parent_id=NULL，generation=0，绑定登录时读到的 auth_epoch）。
func insertRefreshRoot(ctx context.Context, tokenHash, familyID string, userID int64, sid string, authEpoch int64, ttl int64) error {
	_, err := g.DB().Exec(ctx,
		"INSERT INTO refresh_tokens (token_hash, family_id, user_id, parent_id, generation, sid, auth_epoch, expires_at) VALUES (?, ?, ?, NULL, 0, ?, ?, DATE_ADD(NOW(), INTERVAL ? SECOND))",
		tokenHash, familyID, userID, sid, authEpoch, ttl,
	)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入 refresh token: %w", err))
	}
	return nil
}

// revokeFamilyBySid 撤销指定 sid 对应的 refresh family 全部未撤销成员（logout / 撤销指定会话）。
// 幂等：已撤销成员跳过。
func revokeFamilyBySid(ctx context.Context, sid string) error {
	_, err := g.DB().Exec(ctx,
		"UPDATE refresh_tokens SET revoked_at = NOW(), revoked_reason = ? WHERE sid = ? AND revoked_at IS NULL",
		revokedReasonRevoked, sid,
	)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销 refresh family: %w", err))
	}
	return nil
}

// revokeFamiliesExceptSid 撤销该用户除 currentSid 外全部 refresh families（revoke-others）。
func revokeFamiliesExceptSid(ctx context.Context, userID int64, currentSid string) error {
	_, err := g.DB().Exec(ctx,
		"UPDATE refresh_tokens SET revoked_at = NOW(), revoked_reason = ? WHERE user_id = ? AND sid <> ? AND revoked_at IS NULL",
		revokedReasonRevoked, userID, currentSid,
	)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销其他 refresh family: %w", err))
	}
	return nil
}

// revokeAllFamiliesByUser 撤销该用户全部 refresh families（revoke-all / reuse）。
func revokeAllFamiliesByUser(ctx context.Context, userID int64) error {
	_, err := g.DB().Exec(ctx,
		"UPDATE refresh_tokens SET revoked_at = NOW(), revoked_reason = ? WHERE user_id = ? AND revoked_at IS NULL",
		revokedReasonRevoked, userID,
	)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销全部 refresh family: %w", err))
	}
	return nil
}
