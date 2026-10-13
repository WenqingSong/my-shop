package cmd

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/logic/legacymigrate"
)

// 历史数据迁移子命令：my-shop legacy-migrate <dry-run|import>。
// dry-run：只解析、映射、校验并输出将执行的变更，不写目标库（AC-010）。
// import：真实导入 staging 历史数据到目标库，幂等可重复执行（AC-008）。
var (
	legacyMigrateCmd = gcmd.Command{
		Name:  "legacy-migrate",
		Usage: "my-shop legacy-migrate <dry-run|import>",
		Brief: "历史电商数据迁移（staging → 目标库）",
	}

	legacyMigrateDryRunCmd = gcmd.Command{
		Name:  "dry-run",
		Usage: "my-shop legacy-migrate dry-run",
		Brief: "只解析、映射、校验并输出将执行的变更，不写目标库",
		Func: func(ctx context.Context, _ *gcmd.Parser) error {
			return runLegacyMigrate(ctx, true)
		},
	}

	legacyMigrateImportCmd = gcmd.Command{
		Name:  "import",
		Usage: "my-shop legacy-migrate import",
		Brief: "真实导入历史数据到目标库（幂等，可重复执行）",
		Func: func(ctx context.Context, _ *gcmd.Parser) error {
			return runLegacyMigrate(ctx, false)
		},
	}
)

func init() {
	_ = Main.AddCommand(&legacyMigrateCmd)
	_ = legacyMigrateCmd.AddCommand(&legacyMigrateDryRunCmd, &legacyMigrateImportCmd)
}

// runLegacyMigrate 打开 staging 源库与目标库连接，执行迁移并输出报告。
func runLegacyMigrate(ctx context.Context, dryRun bool) error {
	source, target, err := legacymigrate.OpenConnections(ctx)
	if err != nil {
		return err
	}
	defer source.Close()
	defer target.Close()

	result, err := legacymigrate.NewEngine(source, target).Run(ctx, dryRun)
	if err != nil {
		return err
	}
	fmt.Print(result.String())
	return nil
}
