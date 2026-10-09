package boot

import (
	"strings"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
)

// TestCheckSuperAdminConditionExists 覆盖 AC-008/INV-002：数据库已有超级管理员时，
// 即使未提供 ADMIN_SUPER_PASSWORD 也通过（幂等，不覆盖既有密码）。
func TestCheckSuperAdminConditionExists(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", testSuperPassword)
	ctx := setupAdminSeed(t)
	if err := seedSuperAdmin(ctx); err != nil {
		t.Fatalf("seed super admin: %v", err)
	}

	// 已存在超管：清空密码环境变量后预检仍应通过。
	t.Setenv("ADMIN_SUPER_PASSWORD", "")
	if err := CheckSuperAdminCondition(ctx); err != nil {
		t.Fatalf("expected nil when super admin exists, got: %v", err)
	}
}

// TestCheckSuperAdminConditionMissingPasswordFails 覆盖 AC-008/INV-002：无超管且未配置密码时 fail-fast，
// 错误指认 ADMIN_SUPER_PASSWORD，不泄漏任何密码值。
func TestCheckSuperAdminConditionMissingPasswordFails(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", "")
	ctx := setupAdminSeed(t)

	err := CheckSuperAdminCondition(ctx)
	if err == nil {
		t.Fatal("expected error when super admin absent and password empty")
	}
	if !strings.Contains(err.Error(), "ADMIN_SUPER_PASSWORD") {
		t.Fatalf("error should mention ADMIN_SUPER_PASSWORD, got: %v", err)
	}
}

// TestCheckSuperAdminConditionPasswordPresentPasses 覆盖 AC-008/INV-002：无超管但已配置密码时通过
// （后续 seedSuperAdmin 在启动时创建）。
func TestCheckSuperAdminConditionPasswordPresentPasses(t *testing.T) {
	t.Setenv("ADMIN_SUPER_PASSWORD", testSuperPassword)
	ctx := setupAdminSeed(t)

	if err := CheckSuperAdminCondition(ctx); err != nil {
		t.Fatalf("expected nil when password present, got: %v", err)
	}
}

// TestCheckSuperAdminConditionTableNotExist 覆盖「admins 表不存在（尚未 migrate）」分支：
// 1146 应视为「超管不存在」而非 DB 错误，因此缺密码时仍 fail-fast 指认 ADMIN_SUPER_PASSWORD。
func TestCheckSuperAdminConditionTableNotExist(t *testing.T) {
	ctx := setupReadiness(t) // 清空所有迁移表，admins 不存在

	t.Setenv("ADMIN_SUPER_PASSWORD", "")
	err := CheckSuperAdminCondition(ctx)
	if err == nil {
		t.Fatal("expected error when admins table missing and password empty")
	}
	if !strings.Contains(err.Error(), "ADMIN_SUPER_PASSWORD") {
		t.Fatalf("error should mention ADMIN_SUPER_PASSWORD, got: %v", err)
	}

	// 表不存在但已配置密码：视为「超管不存在」，通过（后续 migrate + seed 创建）。
	t.Setenv("ADMIN_SUPER_PASSWORD", testSuperPassword)
	if err := CheckSuperAdminCondition(ctx); err != nil {
		t.Fatalf("expected nil when password present and table missing, got: %v", err)
	}
}

// TestSuperAdminExistsTableNotExist 直接验证 superAdminExists 在 admins 表不存在（1146）时返回 (false, nil)，
// 即把「未迁移」归类为「超管不存在」而非 DB 错误。
func TestSuperAdminExistsTableNotExist(t *testing.T) {
	ctx := setupReadiness(t)

	exists, err := superAdminExists(ctx)
	if err != nil {
		t.Fatalf("expected no error when table missing, got: %v", err)
	}
	if exists {
		t.Fatal("expected exists=false when admins table missing")
	}
}
