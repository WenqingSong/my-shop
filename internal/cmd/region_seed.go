package cmd

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/regiondata"
)

// 地区字典初始化子命令：my-shop region-seed <dry-run|import>。
//
// 数据来源为仓库内嵌的 regions_v1.tsv（由历史 system_city 提取，见 internal/regiondata），
// 不依赖开发者本机的 tmp/shopping.sql 或旧 staging 库；导入幂等、可重复执行，
// 已有数据内容冲突时拒绝写入而不是覆盖。详见 docs/dev/region-data-initialization.md。
var (
	regionSeedCmd = gcmd.Command{
		Name:  "region-seed",
		Usage: "my-shop region-seed <dry-run|import>",
		Brief: "地区字典（regions）初始化：幂等、可重复执行",
	}

	regionSeedDryRunCmd = gcmd.Command{
		Name:  "dry-run",
		Usage: "my-shop region-seed dry-run",
		Brief: "只校验数据文件与目标库差异，不写入任何数据",
		Func: func(ctx context.Context, _ *gcmd.Parser) error {
			return runRegionSeed(ctx, true)
		},
	}

	regionSeedImportCmd = gcmd.Command{
		Name:  "import",
		Usage: "my-shop region-seed import",
		Brief: "幂等写入地区字典（新增缺失节点；冲突时拒绝写入）",
		Func: func(ctx context.Context, _ *gcmd.Parser) error {
			return runRegionSeed(ctx, false)
		},
	}
)

func init() {
	_ = Main.AddCommand(&regionSeedCmd)
	_ = regionSeedCmd.AddCommand(&regionSeedDryRunCmd, &regionSeedImportCmd)
}

// runRegionSeed 初始化 GoFrame 数据库配置后执行地区字典导入，并输出目标库与统计。
func runRegionSeed(ctx context.Context, dryRun bool) error {
	if err := boot.ApplyDatabaseConfig(ctx); err != nil {
		return err
	}
	db := g.DB()

	dbName, err := db.GetValue(ctx, "SELECT DATABASE()")
	if err != nil {
		return fmt.Errorf("连接目标库失败（请先执行 `my-shop migrate up`）：%w", err)
	}
	dbVersion, err := db.GetValue(ctx, "SELECT VERSION()")
	if err != nil {
		return fmt.Errorf("读取 MySQL 版本失败：%w", err)
	}

	stats, err := regiondata.Ensure(ctx, db, dryRun)
	if err != nil {
		return err
	}

	mode := "import"
	if dryRun {
		mode = "dry-run"
	}
	fmt.Printf("模式: %s\n", mode)
	fmt.Printf("目标库: %s（MySQL %s）\n", dbName.String(), dbVersion.String())
	fmt.Printf("数据版本: %s（来源 %s.%s，导出 %s，sha256 %s）\n",
		stats.Meta.DataVersion, stats.Meta.SourceFile, stats.Meta.SourceTable,
		stats.Meta.SourceExportDate, stats.Meta.SourceSHA256)
	fmt.Printf("数据文件: 合计 %d（省 %d / 市 %d / 区县 %d）\n",
		stats.Meta.Total, stats.Meta.Provinces, stats.Meta.Cities, stats.Meta.Districts)
	fmt.Printf("目标库已有: %d，已一致: %d，待写入: %d\n", stats.Existing, stats.Matched, stats.Inserted)
	if dryRun {
		fmt.Println("dry-run：未写入任何数据")
	}
	return nil
}
