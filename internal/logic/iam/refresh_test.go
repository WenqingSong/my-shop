package iam

import (
	"context"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789" // >= 32 bytes
	testAdminPassword = "test-admin-password-123"
)

// TestClassifyRefreshRow 覆盖 CLEAN-001 的判定分类：revoked_reason='rotated' → REUSE；
// revoked_at 非空（reason='revoked'）→ INVALID；revoked_at IS NULL 且已过期 → EXPIRED（不得误判为 REUSE）；
// 否则 OK。该分类是「0 行时按真实状态返回对应错误」的权威依据。
func TestClassifyRefreshRow(t *testing.T) {
	now := gtime.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	rotated := revokedReasonRotated
	revoked := revokedReasonRevoked

	cases := []struct {
		name string
		row  refreshTokenRow
		want codes.Code
	}{
		{
			name: "rotated → reuse",
			row:  refreshTokenRow{RevokedReason: &rotated, RevokedAt: now, ExpiresAt: future},
			want: codes.CodeRefreshTokenReuse,
		},
		{
			name: "revoked → invalid",
			row:  refreshTokenRow{RevokedReason: &revoked, RevokedAt: now, ExpiresAt: future},
			want: codes.CodeRefreshTokenInvalid,
		},
		{
			name: "expired but not revoked → expired",
			row:  refreshTokenRow{RevokedAt: nil, ExpiresAt: past},
			want: codes.CodeRefreshTokenExpired,
		},
		{
			name: "nil expires_at → expired",
			row:  refreshTokenRow{RevokedAt: nil, ExpiresAt: nil},
			want: codes.CodeRefreshTokenExpired,
		},
		{
			name: "valid → ok",
			row:  refreshTokenRow{RevokedAt: nil, ExpiresAt: future},
			want: codes.CodeOK,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyRefreshRow(&c.row); got != c.want {
				t.Fatalf("classifyRefreshRow = %d, want %d", got, c.want)
			}
		})
	}
}

// TestHandleRefreshNotRotatedExpired 覆盖 CLEAN-001 的回归：事务内条件 UPDATE 影响 0 行
// 且原因是「恰逢过期」时，重读判定必须返回 EXPIRED(2013) 而非 REUSE(2014)，且不触发全量撤销。
// 该测试直接驱动重读路径，能区分「重读区分」与「统一按 reuse 处理」两种实现。
func TestHandleRefreshNotRotatedExpired(t *testing.T) {
	ctx := setupIAMLogicDB(t)
	s := &sIam{}

	userID, err := g.DB().Model("users").Ctx(ctx).Data(g.Map{
		"username": "alice", "password_hash": "x",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// 使用随机明文生成哈希，避免与其它测试（如 controller 的固定 unknown token）哈希碰撞。
	plainA, err := auth.NewRefreshToken()
	if err != nil {
		t.Fatalf("new token A: %v", err)
	}
	plainB, err := auth.NewRefreshToken()
	if err != nil {
		t.Fatalf("new token B: %v", err)
	}
	hashA := auth.HashRefreshToken(plainA)
	hashB := auth.HashRefreshToken(plainB)

	// A 已过期但未撤销；B 有效未撤销（用于验证过期不触发全量撤销）。
	insertToken := func(hash, family, sid, expiresSQL string) {
		if _, err := g.DB().Exec(ctx,
			"INSERT INTO refresh_tokens (token_hash, family_id, user_id, parent_id, generation, sid, expires_at) VALUES (?, ?, ?, NULL, 0, ?, "+expiresSQL+")",
			hash, family, userID, sid); err != nil {
			t.Fatalf("insert refresh token: %v", err)
		}
	}
	insertToken(hashA, "familyA", "sidA", "DATE_SUB(NOW(), INTERVAL 1 HOUR)")
	insertToken(hashB, "familyB", "sidB", "DATE_ADD(NOW(), INTERVAL 3600 SECOND)")

	// 重读判定：A 判为过期（2013），而非 reuse（2014）。
	if err := s.handleRefreshNotRotated(ctx, hashA); codes.FromError(err) != codes.CodeRefreshTokenExpired {
		t.Fatalf("expected EXPIRED(2013), got code=%d err=%v", codes.FromError(err), err)
	}

	// A 仍仅过期、未被撤销；B 仍有效（过期不触发全量撤销）。
	for _, hash := range []string{hashA, hashB} {
		n, err := g.DB().Model("refresh_tokens").Ctx(ctx).
			Where("token_hash", hash).WhereNull("revoked_at").Count()
		if err != nil {
			t.Fatalf("count active: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected token %s active (revoked_at IS NULL), got %d", hash, n)
		}
	}
}

// setupIAMLogicDB 迁移建表并 Bootstrap 配置 MySQL/Redis，清空本测试涉及的数据，返回 ctx。
// 复用与 controller/iam 集成测试一致的启动路径（migrations.Up → boot.Bootstrap）。
func setupIAMLogicDB(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	clean := func() {
		for _, table := range []string{"refresh_tokens", "users"} {
			if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
				t.Errorf("clean %s: %v", table, err)
			}
		}
		if err := g.Redis().FlushDB(context.Background()); err != nil {
			t.Errorf("clean redis: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	return ctx
}
