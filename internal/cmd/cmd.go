package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
	"cnb.cool/go-cloud-devops/my-shop/internal/storage"
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

	// 启动后台超时取消扫描器：扫描 status=待支付 且 expire_at 已过的订单，逐单原子取消并恢复库存。
	startOrderCancelScanner(ctx)
	// 启动秒杀缓存对账扫描器：按「活动 × SKU」粒度将 Redis remaining 刷成 total_stock - sold - inflight_queued，并回补缺失预热。
	startFlashSaleReconcileScanner(ctx)
	// 启动秒杀异步消费扫描器：出队 queued 请求并事务落单/失败/重试/死信。
	startFlashSaleConsumeScanner(ctx)

	s := g.Server()
	// 初始化本地图片存储：seed 轮播图占位图，并将 banner 目录映射为静态路由 /storage/banners。
	if err := configureBannerStorage(ctx, s); err != nil {
		return err
	}

	// 秒杀指标采集端点（Prometheus 文本格式，仅在 flash_sale.metrics.enabled 开启时暴露；
	// 位于 Response 中间件之外，直接输出原始文本，不经统一 JSON 响应包装）。
	registerMetricsRoute(ctx, s)

	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.Response)
		root.Bind(health.NewV1())

		RegisterFrontendRoutes(root)
		RegisterAdminRoutes(root)
	})
	s.Run()
	return nil
}

// configureBannerStorage 初始化轮播图图片的本地存储：seed 3 张占位图，并将 banner 目录
// 映射为 /storage/banners 静态路由（LocalStorage 为 V1 唯一实现，后续可替换 MinIO/OSS/S3）。
func configureBannerStorage(ctx context.Context, s *ghttp.Server) error {
	local := storage.NewLocal(storageRoot(ctx))
	if _, err := local.PrepareBannerPlaceholders(ctx); err != nil {
		return err
	}
	s.AddStaticPath(storage.BannerURLPrefix, local.BannerDir())
	return nil
}

// registerMetricsRoute 在指标开关开启时挂载 GET /metrics（Prometheus 文本格式，不经统一 JSON 响应包装）。
// 生产入口与集成测试复用本函数，避免采集端点暴露面在两地维护出现漂移。
func registerMetricsRoute(ctx context.Context, s *ghttp.Server) {
	if !metrics.Enabled(ctx) {
		return
	}
	s.BindHandler("GET:/metrics", func(r *ghttp.Request) {
		metrics.Handler().ServeHTTP(r.Response.Writer, r.Request)
	})
}

// storageRoot 读取本地存储根目录（环境变量 STORAGE_LOCAL_ROOT 可覆盖），默认 ./storage。
func storageRoot(ctx context.Context) string {
	v, err := g.Cfg().GetEffective(ctx, "storage.local.root", "./storage")
	if err != nil || v == nil {
		return "./storage"
	}
	return v.String()
}

// orderCancelScanBatch 是每轮超时取消扫描处理的最大订单数。
const orderCancelScanBatch = 100

// startOrderCancelScanner 启动订单超时取消后台扫描器（goroutine + ticker）。
// 周期由 order.cancel_scan_interval 配置（秒，默认 60）；多实例并发依赖「条件状态更新 + RowsAffected」
// 原子闸门保证同一订单只被取消并恢复一次库存，故无需优雅停机通知。
func startOrderCancelScanner(ctx context.Context) {
	interval := g.Cfg().MustGet(ctx, "order.cancel_scan_interval", 60).Int()
	if interval <= 0 {
		interval = 60
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.Order().CancelExpired(ctx, orderCancelScanBatch); err != nil {
					glog.Warningf(ctx, "订单超时取消扫描失败: %v", err)
				}
			}
		}
	}()
}

// flashSaleReconcileScanBatch 是每轮秒杀缓存对账扫描处理的最大活动数。
const flashSaleReconcileScanBatch = 100

// flashSaleConsumeScanBatch 是每轮秒杀异步消费扫描处理的最大请求数。
const flashSaleConsumeScanBatch = 100

// startFlashSaleConsumeScanner 启动秒杀异步消费后台扫描器（goroutine + ticker）。
// 周期由 flash_sale.consume_scan_interval 配置（秒，默认 1，保证低延迟落单）；
// 出队依赖 FOR UPDATE SKIP LOCKED + 状态原子更新保证同一请求只被处理一次，多实例并发安全。
func startFlashSaleConsumeScanner(ctx context.Context) {
	interval := g.Cfg().MustGet(ctx, "flash_sale.consume_scan_interval", 1).Int()
	if interval <= 0 {
		interval = 1
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.FlashSale().ConsumeQueued(ctx, flashSaleConsumeScanBatch); err != nil {
					glog.Warningf(ctx, "秒杀异步消费扫描失败: %v", err)
				}
			}
		}
	}()
}

// startFlashSaleReconcileScanner 启动秒杀缓存对账后台扫描器（goroutine + ticker）。
// 周期由 flash_sale.reconcile_scan_interval 配置（秒，默认 60）；对账为无状态、幂等写入
// 权威值（remaining = total_stock - sold），多实例并发安全。
func startFlashSaleReconcileScanner(ctx context.Context) {
	interval := g.Cfg().MustGet(ctx, "flash_sale.reconcile_scan_interval", 60).Int()
	if interval <= 0 {
		interval = 60
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.FlashSale().ReconcileCache(ctx, flashSaleReconcileScanBatch); err != nil {
					glog.Warningf(ctx, "秒杀缓存对账扫描失败: %v", err)
				}
			}
		}
	}()
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
