package regiondata

// 地区字典初始化的真实 MySQL 集成测试（不使用 Mock）。
//
// 仅允许在隔离测试库执行（DATABASE_DEFAULT_NAME 白名单），覆盖：
//   - 空库/部分数据下的幂等写入与规模核对；
//   - 重复执行不产生重复记录；
//   - dry-run 不写库；
//   - 额外节点与内容冲突时拒绝写入（不覆盖）。
//
// 运行：
//
//	DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
//	  go test -count=1 -run 'TestEnsureIntegration' ./internal/regiondata/

import (
	"context"
	"os"
	"strings"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

var integrationSafeDBs = []string{"my_shop_b104_test", "my_shop_b104_integration"}

func isIntegrationSafeDB(name string) bool {
	for _, s := range integrationSafeDBs {
		if name == s {
			return true
		}
	}
	return false
}

func setupRegionDataDB(t *testing.T) context.Context {
	t.Helper()
	if name := os.Getenv("DATABASE_DEFAULT_NAME"); !isIntegrationSafeDB(name) {
		t.Skipf("跳过地区数据集成测试：DATABASE_DEFAULT_NAME=%q，要求隔离库 %v", name, integrationSafeDBs)
	}
	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// 与 serve / region-seed 使用同一份数据库配置来源，避免测试自行拼 DSN。
	if err := boot.ApplyDatabaseConfig(ctx); err != nil {
		t.Fatalf("初始化数据库配置: %v", err)
	}
	return ctx
}

// TestEnsureIntegrationIdempotentAndConflictDetection 覆盖幂等初始化与冲突拒绝。
func TestEnsureIntegrationIdempotentAndConflictDetection(t *testing.T) {
	ctx := setupRegionDataDB(t)
	meta := Metadata()

	// 首次：空库写入全部节点；已有数据则全部命中。
	stats, err := Ensure(ctx, g.DB(), false)
	if err != nil {
		t.Fatalf("首次初始化地区字典: %v", err)
	}
	if stats.Existing+stats.Inserted != meta.Total {
		t.Fatalf("首次初始化统计不一致: existing=%d inserted=%d total=%d",
			stats.Existing, stats.Inserted, meta.Total)
	}
	requireCounts(t)

	// 第二次：完全命中，不重复写入。
	stats, err = Ensure(ctx, g.DB(), false)
	if err != nil {
		t.Fatalf("重复初始化地区字典: %v", err)
	}
	if stats.Inserted != 0 || stats.Matched != meta.Total || stats.Existing != meta.Total {
		t.Fatalf("重复初始化不是幂等: %+v", stats)
	}

	// dry-run：只统计，不写库。
	stats, err = Ensure(ctx, g.DB(), true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if stats.Inserted != 0 || !stats.DryRun {
		t.Fatalf("dry-run 统计异常: %+v", stats)
	}
	requireCounts(t)

	// 额外节点：必须拒绝写入并报告，不删除既有数据。
	const extraID int64 = 99999999
	if _, err := g.DB().Exec(ctx,
		"INSERT INTO regions (id, parent_id, level, name) VALUES (?, 0, 1, ?)", extraID, "测试额外节点"); err != nil {
		t.Fatalf("插入测试额外节点: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Exec(context.Background(), "DELETE FROM regions WHERE id = ?", extraID)
	})
	if _, err := Ensure(ctx, g.DB(), false); err == nil || !strings.Contains(err.Error(), "额外") {
		t.Fatalf("存在额外节点时必须拒绝写入，实际 err=%v", err)
	}

	// 内容冲突：同 id 不同名称必须拒绝写入，且不覆盖已有行。
	v, err := g.DB().Model("regions").Ctx(ctx).Where("id", 1).Value("name")
	if err != nil {
		t.Fatalf("读取 id=1 名称: %v", err)
	}
	originName := v.String()
	if _, err := g.DB().Exec(ctx, "UPDATE regions SET name = ? WHERE id = 1", "被篡改的省名"); err != nil {
		t.Fatalf("制造内容冲突: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Exec(context.Background(), "UPDATE regions SET name = ? WHERE id = 1", originName)
	})
	if _, err := Ensure(ctx, g.DB(), false); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("存在内容冲突时必须拒绝写入，实际 err=%v", err)
	}
	current, err := g.DB().Model("regions").Ctx(ctx).Where("id", 1).Value("name")
	if err != nil {
		t.Fatalf("复核 id=1 名称: %v", err)
	}
	if current.String() != "被篡改的省名" {
		t.Fatalf("冲突时不应覆盖已有数据，实际 name=%q", current.String())
	}
}

// requireCounts 直接查库核对规模与层级分布。
func requireCounts(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	counts := map[int]int{}
	for level := 1; level <= 3; level++ {
		n, err := g.DB().Model("regions").Ctx(ctx).Where("level", level).Count()
		if err != nil {
			t.Fatalf("统计 level=%d: %v", level, err)
		}
		counts[level] = n
	}
	v, err := g.DB().GetValue(ctx, "SELECT COUNT(*) FROM regions")
	if err != nil {
		t.Fatalf("统计 regions 总数: %v", err)
	}
	if total := v.Int(); total != Metadata().Total || counts[1] != 34 || counts[2] != 369 || counts[3] != 3535 {
		t.Fatalf("regions 规模 = 总%d/省%d/市%d/区县%d，期望 %d/34/369/3535",
			total, counts[1], counts[2], counts[3], Metadata().Total)
	}
}
