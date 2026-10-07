package boot

import (
	"context"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// migrationBaselineVersion 是内嵌 baseline 迁移的版本号，与 migrations 包保持一致。
const migrationBaselineVersion = uint(20261001000001)

// migrationRelatedTables 是 readiness 测试需要清空的表（含测试探针表）。
// 注意顺序：order_items 通过外键引用 orders（ON DELETE CASCADE），故 order_items 排在 orders 之前；
// recommend_items 通过外键引用 recommend_positions（ON DELETE CASCADE），故 recommend_items 排在 recommend_positions 之前；
// refresh_tokens/cart_items/favorites/product_likes/banners 无外键、置前；addresses 通过外键引用 users（ON DELETE CASCADE），
// 故 addresses 排在 users 之前；inventories/inventory_logs 通过外键引用 skus，skus 通过外键引用
// products，products 通过外键引用 categories，因此被引用方必须排在引用方之后，
// 否则 DROP TABLE 会因外键依赖失败。
var migrationRelatedTables = []string{
	"recommend_items", "recommend_positions",
	"flash_sale_orders", "flash_sale_activity_skus", "flash_sale_activities",
	"order_items", "orders",
	"refresh_tokens", "cart_items", "article_favorites", "article_likes", "articles", "favorites", "product_likes", "banners", "reviews", "role_permissions", "admin_roles", "permissions", "roles",
	"admins", "inventory_logs", "inventories", "skus", "product_images", "products", "categories", "addresses", "users",
	"schema_migrations", "migration_probe",
}

// dropAllMigrationTables 清空迁移相关表，返回错误（供 setup/cleanup 复用）。
func dropAllMigrationTables(ctx context.Context) error {
	for _, table := range migrationRelatedTables {
		if _, err := g.DB().Exec(ctx, "DROP TABLE IF EXISTS `"+table+"`"); err != nil {
			return err
		}
	}
	return nil
}

// restoreMigrationBaseline 清空后重建 baseline，使库回到干净已迁移状态。
func restoreMigrationBaseline() error {
	ctx := context.Background()
	if err := dropAllMigrationTables(ctx); err != nil {
		return err
	}
	return migrations.Up(ctx)
}

// setupReadiness 配置数据库、清空迁移相关表，并注册 cleanup 恢复 baseline。
func setupReadiness(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	if err := applyDatabaseConfig(ctx); err != nil {
		t.Fatalf("apply database config: %v", err)
	}
	if err := dropAllMigrationTables(ctx); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	t.Cleanup(func() {
		if err := restoreMigrationBaseline(); err != nil {
			t.Errorf("restore baseline: %v", err)
		}
	})
	return ctx
}

// TestCheckSchemaReadyFailsOnEmptyDB 覆盖 INV-005/AC-005：
// 未执行 migrate 的空库上，serve 的只读 readiness check 应 fail-fast。
func TestCheckSchemaReadyFailsOnEmptyDB(t *testing.T) {
	ctx := setupReadiness(t)

	if err := checkSchemaReady(ctx); err == nil {
		t.Fatal("expected checkSchemaReady to fail on empty db")
	}
}

// TestCheckSchemaReadyFailsOnDirty 覆盖 INV-002/AC-004：
// schema_migrations 处于 dirty 状态时，readiness check 应 fail-fast，提示人工 force。
func TestCheckSchemaReadyFailsOnDirty(t *testing.T) {
	ctx := setupReadiness(t)

	if _, err := g.DB().Exec(ctx, "CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)"); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := g.DB().Exec(ctx, "INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)", migrationBaselineVersion, true); err != nil {
		t.Fatalf("insert dirty: %v", err)
	}

	if err := checkSchemaReady(ctx); err == nil {
		t.Fatal("expected checkSchemaReady to fail on dirty db")
	}
}

// TestCheckSchemaReadyPassesWhenMigrated 覆盖 INV-004/AC-005：
// migration 已就绪后，readiness check 通过，seed 得以继续执行。
func TestCheckSchemaReadyPassesWhenMigrated(t *testing.T) {
	ctx := setupReadiness(t)

	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := checkSchemaReady(ctx); err != nil {
		t.Fatalf("expected checkSchemaReady to pass after migrate up, got: %v", err)
	}
}
