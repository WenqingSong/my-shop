package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// 命令结构：my-shop [serve|migrate <up|force|version>]。
// - 无参数或 `serve`：启动 HTTP 服务（schema 就绪检查 + seed + 路由）。
// - `migrate up`：显式执行所有未应用 migration（serve 不自动执行）。
// - `migrate force <version>`：标记版本已应用（baseline 接管 / dirty 恢复），不执行 SQL。
// - `migrate version`：只读查看当前版本与 dirty 状态。
var (
	Main = gcmd.Command{
		Name:  "my-shop",
		Usage: "my-shop [serve|migrate <up|force|version>]",
		Brief: "my-shop 应用（HTTP 服务与数据库迁移）",
		Func: func(ctx context.Context, parser *gcmd.Parser) error {
			// 无参数时默认启动 HTTP 服务，保持向后兼容。
			return serve(ctx, parser)
		},
	}

	serveCmd = gcmd.Command{
		Name:  "serve",
		Usage: "my-shop serve",
		Brief: "启动 HTTP 服务（schema 就绪检查 + seed + 路由）",
		Func:  serve,
	}

	migrateCmd = gcmd.Command{
		Name:  "migrate",
		Usage: "my-shop migrate <up|force|version>",
		Brief: "管理数据库 migration（显式执行，serve 不自动执行）",
	}

	migrateUpCmd = gcmd.Command{
		Name:  "up",
		Usage: "my-shop migrate up",
		Brief: "应用所有未执行的 migration，失败 fail-fast",
		Func:  migrateUp,
	}

	migrateForceCmd = gcmd.Command{
		Name:  "force",
		Usage: "my-shop migrate force <version>",
		Brief: "标记指定版本为已应用（不执行 SQL，用于 baseline 接管 / dirty 恢复）",
		Func:  migrateForce,
		Arguments: []gcmd.Argument{
			{Name: "version", IsArg: true, Brief: "要标记为已应用的版本号（14 位时间戳数字）"},
		},
	}

	migrateVersionCmd = gcmd.Command{
		Name:  "version",
		Usage: "my-shop migrate version",
		Brief: "查看当前 migration 版本与 dirty 状态",
		Func:  migrateVersion,
	}
)

func init() {
	_ = Main.AddCommand(&serveCmd, &migrateCmd)
	_ = migrateCmd.AddCommand(&migrateUpCmd, &migrateForceCmd, &migrateVersionCmd)
}

// serve 启动 HTTP 服务：Bootstrap（就绪检查 + seed）→ 路由挂载 → Server 启动。
func serve(ctx context.Context, _ *gcmd.Parser) error {
	if err := boot.Bootstrap(ctx); err != nil {
		return err
	}

	s := g.Server()
	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.Response)
		root.Bind(health.NewV1())

		RegisterFrontendRoutes(root)
		RegisterAdminRoutes(root)
	})
	s.Run()
	return nil
}

// migrateUp 执行所有未应用 migration。
func migrateUp(ctx context.Context, _ *gcmd.Parser) error {
	return migrations.Up(ctx)
}

// migrateForce 标记指定版本已应用（不执行 SQL）。
func migrateForce(ctx context.Context, parser *gcmd.Parser) error {
	version, err := migrateForceVersion(parser)
	if err != nil {
		return err
	}
	return migrations.Force(ctx, version)
}

// migrateVersion 只读打印当前版本与 dirty 状态。
func migrateVersion(ctx context.Context, _ *gcmd.Parser) error {
	current, dirty, latest, err := migrations.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("version: %d (dirty=%t)\n", current, dirty)
	if current < latest {
		fmt.Printf("latest available: %d（有待执行的 migration，请执行 `my-shop migrate up`）\n", latest)
	} else {
		fmt.Printf("latest available: %d（已是最新）\n", latest)
	}
	return nil
}

// migrateForceVersion 从命令参数中解析 force 的版本号（14 位时间戳数字）。
// 期望命令行形态为 [binary, migrate, force, <version>]，取最后一个位置参数。
func migrateForceVersion(parser *gcmd.Parser) (uint, error) {
	args := parser.GetArgAll()
	if len(args) < 4 {
		return 0, gerror.New("缺少 version 参数：my-shop migrate force <version>")
	}
	raw := args[len(args)-1]
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, gerror.Wrapf(err, "version 参数 %q 非法（应为 14 位时间戳数字）", raw)
	}
	return uint(v), nil
}
