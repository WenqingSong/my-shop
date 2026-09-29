package boot

import (
	"context"
	"sync"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	"github.com/gogf/gf/v2/frame/g"
	"golang.org/x/crypto/bcrypt"
)

const testSuperPassword = "super-secret-123"

// setupAdminSeed 配置数据库、幂等建表并清空 admins，返回 context。
func setupAdminSeed(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	if err := applyDatabaseConfig(ctx); err != nil {
		t.Fatalf("apply database config: %v", err)
	}
	if err := ensureTables(ctx); err != nil {
		t.Fatalf("ensure tables: %v", err)
	}
	if _, err := g.DB().Exec(ctx, "DELETE FROM admins"); err != nil {
		t.Fatalf("clean admins: %v", err)
	}
	return ctx
}

func superAdminCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	n, err := g.DB().Model("admins").Ctx(ctx).Where("is_super", 1).Count()
	if err != nil {
		t.Fatalf("count super admin: %v", err)
	}
	return n
}

func superAdminUsername(t *testing.T, ctx context.Context) string {
	t.Helper()
	v, err := g.DB().Model("admins").Ctx(ctx).Where("is_super", 1).Value("username")
	if err != nil {
		t.Fatalf("query super admin username: %v", err)
	}
	return v.String()
}

func superAdminHash(t *testing.T, ctx context.Context) string {
	t.Helper()
	v, err := g.DB().Model("admins").Ctx(ctx).Where("is_super", 1).Value("password_hash")
	if err != nil {
		t.Fatalf("query super admin hash: %v", err)
	}
	return v.String()
}

// TestSeedSuperAdminIdempotent 覆盖 AC-001/INV-001：seed 幂等，仅创建一个 is_super=1，
// 且再次 seed 不覆盖密码。
func TestSeedSuperAdminIdempotent(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", testSuperPassword)
	ctx := setupAdminSeed(t)

	if err := seedSuperAdmin(ctx); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if n := superAdminCount(t, ctx); n != 1 {
		t.Fatalf("expected 1 super admin after first seed, got %d", n)
	}
	if u := superAdminUsername(t, ctx); u != defaultSuperUsername {
		t.Fatalf("expected default super admin username %q, got %q", defaultSuperUsername, u)
	}
	hash1 := superAdminHash(t, ctx)
	if err := bcrypt.CompareHashAndPassword([]byte(hash1), []byte(testSuperPassword)); err != nil {
		t.Fatalf("super admin password hash mismatch: %v", err)
	}

	// 再次 seed：跳过且不覆盖密码。
	if err := seedSuperAdmin(ctx); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if n := superAdminCount(t, ctx); n != 1 {
		t.Fatalf("expected still 1 super admin after second seed, got %d", n)
	}
	if hash2 := superAdminHash(t, ctx); hash2 != hash1 {
		t.Fatalf("super admin password hash should not change, got %q vs %q", hash2, hash1)
	}
}

// TestSeedSuperAdminMissingPasswordFails 覆盖 AC-001：无超级管理员且未配置密码时 fail-fast。
func TestSeedSuperAdminMissingPasswordFails(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", "")
	ctx := setupAdminSeed(t)

	if err := seedSuperAdmin(ctx); err == nil {
		t.Fatal("expected seed to fail when password is empty and super admin absent")
	}
	if n := superAdminCount(t, ctx); n != 0 {
		t.Fatalf("expected no super admin created on failure, got %d", n)
	}
}

// TestSeedSuperAdminConcurrent 覆盖 INV-001/AC-001：并发 seed（模拟多实例同时启动）仅创建一个超级管理员，
// 其余并发写命中唯一约束后按幂等跳过，不返回错误、不产生重复行。
func TestSeedSuperAdminConcurrent(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", testSuperPassword)
	ctx := setupAdminSeed(t)

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- seedSuperAdmin(ctx)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent seed should be idempotent, got error: %v", err)
		}
	}
	if got := superAdminCount(t, ctx); got != 1 {
		t.Fatalf("expected exactly 1 super admin after concurrent seed, got %d", got)
	}
}
