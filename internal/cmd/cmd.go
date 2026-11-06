package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
	"cnb.cool/go-cloud-devops/my-shop/internal/storage"
	"cnb.cool/go-cloud-devops/my-shop/internal/termination"
	"cnb.cool/go-cloud-devops/my-shop/internal/worker"
)

// 命令结构：my-shop [serve|migrate <up|force|version>|qiniu <check>|admin <check>|legacy-migrate <dry-run|import>]。
// - 无参数或 `serve`：启动 HTTP 服务（schema 就绪检查 + seed + 路由）。
// - `migrate up`：显式执行所有未应用 migration（serve 不自动执行）。
// - `migrate force <version>`：标记版本已应用（baseline 接管 / dirty 恢复），不执行 SQL。
// - `migrate version`：只读查看当前版本与 dirty 状态。
// - `qiniu check`：校验七牛云 required 配置与指定 bucket 可用性（供 make up 启动前预检）。
// - `admin check`：校验超级管理员创建条件（供 make up 启动前预检）。
// - `legacy-migrate <dry-run|import>`：历史电商数据迁移（staging → 目标库）。
var (
	Main = gcmd.Command{
		Name:  "my-shop",
		Usage: "my-shop [serve|migrate <up|force|version>|qiniu <check>|admin <check>|legacy-migrate <dry-run|import>]",
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

	qiniuCmd = gcmd.Command{
		Name:  "qiniu",
		Usage: "my-shop qiniu <check>",
		Brief: "七牛云对象存储运维命令",
	}

	qiniuCheckCmd = gcmd.Command{
		Name:  "check",
		Usage: "my-shop qiniu check",
		Brief: "校验七牛云 required 配置与指定 bucket 可用性（失败非零退出）",
		Func:  qiniuCheck,
	}

	adminCmd = gcmd.Command{
		Name:  "admin",
		Usage: "my-shop admin <check>",
		Brief: "后台管理员运维命令",
	}

	adminCheckCmd = gcmd.Command{
		Name:  "check",
		Usage: "my-shop admin check",
		Brief: "校验超级管理员创建条件（失败非零退出）",
		Func:  adminCheck,
	}
)

func init() {
	_ = Main.AddCommand(&serveCmd, &migrateCmd, &qiniuCmd, &adminCmd)
	_ = migrateCmd.AddCommand(&migrateUpCmd, &migrateForceCmd, &migrateVersionCmd)
	_ = qiniuCmd.AddCommand(&qiniuCheckCmd)
	_ = adminCmd.AddCommand(&adminCheckCmd)
}

// serve 启动 HTTP 服务：Bootstrap（就绪检查 + seed）→ 路由挂载 → Server 启动。
func serve(ctx context.Context, _ *gcmd.Parser) error {
	if err := boot.Bootstrap(ctx); err != nil {
		return err
	}
	// 七牛云为启动 required dependency：结构（region/ttl/大小/白名单）、存在性（AK/SK/bucket/domain）
	// 与真实可用性（GetBucketInfo 最小权限只读）任一失败即启动 fail-fast（非零退出）。
	// 运行期签发路径仍由 17002 防御性守卫，不承担启动错误表达。
	if err := service.Upload().ValidateConfig(ctx); err != nil {
		return err
	}

	// 秒杀后台组件不得直接继承信号 ctx（B4.1），由 runtimeContext 剥离取消传播。
	runtimeCtx := runtimeContext(ctx)

	// 秒杀链路装配：dispatch_mode 决定唯一消费入口（db_scanner 或 kafka），
	// 两者严格互斥。Kafka 模式下 Producer / Outbox Publisher / Consumer 在此真正启动；
	// 配置矛盾或 Kafka 配置非法会 fail-fast，不带着「无消费者」状态对外提供服务。
	runtime, err := worker.StartFlashSaleRuntime(runtimeCtx)
	if err != nil {
		return err
	}
	// 关闭顺序由 runtime 统一编排（Publisher → Consumer → Producer → Scanner），
	// 所有等待受 flash_sale.shutdown_timeout 约束；超时记录明确日志但不改变进程退出语义。
	defer func() {
		if err := runtime.Shutdown(); err != nil {
			logx.Warning(context.Background(), "flashsale_runtime_shutdown_failed", "秒杀后台组件关闭未完全成功", logx.Fields{
				"error": err.Error(),
			})
		}
	}()

	// 终止预算契约（T2-03）：termination_grace_period 必须覆盖
	// preStop + HTTP drain + Runtime 关闭 + 安全余量。
	// 预算不足即启动失败：否则进程会在终止宽限期结束时被 SIGKILL，关闭顺序随之失效。
	// HTTP drain 与 Runtime 关闭预算都取自实际生效配置（而非默认常量），避免日志与行为漂移。
	budget, err := termination.Load(ctx, boot.ServerGracefulTimeout(ctx), runtime.ShutdownTimeout())
	if err != nil {
		return err
	}
	budget.LogStartup(ctx)

	s := g.Server()
	// 初始化本地图片存储：seed 轮播图占位图，并将 banner 目录映射为静态路由 /storage/banners。
	if err := configureBannerStorage(ctx, s); err != nil {
		return err
	}

	// 秒杀指标采集端点（Prometheus 文本格式，仅在 flash_sale.metrics.enabled 开启时暴露；
	// 位于 Response 中间件之外，直接输出原始文本，不经统一 JSON 响应包装）。
	registerMetricsRoute(ctx, s)

	s.Group("/", func(root *ghttp.RouterGroup) {
		// RequestID 注册于 Response 之前（外层）：统一读取/生成 Request ID、回写响应头，
		// 并在请求结束时输出含 request_id 的结构化访问日志（与 Nginx 访问日志关联）。
		root.Middleware(middleware.RequestID)
		root.Middleware(middleware.Response)
		root.Bind(health.NewV1())

		RegisterFrontendRoutes(root)
		RegisterAdminRoutes(root)
	})
	// serveHTTP 返回后（HTTP 已完成有界 drain 或 drain 超时），
	// 由 defer 中的 runtime.Shutdown 停止后台组件并收尾。
	return serveHTTP(ctx, s, budget.HTTPDrainTimeout)
}

// runtimeContext 返回脱离信号取消的后台运行期 ctx（B4.1 语义）。
//
// 若把 signal.NotifyContext 派生出的 ctx 直接交给 Runtime，SIGTERM 会在信号到达瞬间
// 同时取消 Publisher / Consumer / Scanner，绕过 runtime.Shutdown 的
// 「Publisher → Consumer → Producer → Scanner」顺序编排，并让正在进行的 PublishBatch
// 被就地中断（已领取的 Outbox 记录只能等租约到期后重发）。
//
// WithoutCancel 保留 ctx 中的值（如链路标识），只剥离取消传播；
// 运行期统一由 HTTP drain 结束后调用的 runtime.Shutdown 控制关闭顺序。
func runtimeContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// serveHTTP 启动 HTTP 服务并在预算内完成摘流与 drain。
//
// 时序：SIGTERM/SIGINT/SIGQUIT → 停止接收新连接（net/http Shutdown 立即关闭 listener）
// → 等待在途请求完成，最多 drainTimeout → 返回。返回后由 serve 的 defer 按
// Publisher → Consumer → Producer → Scanner 顺序关闭后台组件。
//
// 为什么不用 s.Run()：GoFrame v2.10.3 的信号处理调用 http.Server.Shutdown(context.TODO())，
// 没有 deadline，server.graceful_timeout 不参与该路径。若在途请求不结束，进程会无限等待，
// 最终在 Kubernetes terminationGracePeriodSeconds 到期时被 SIGKILL，关闭顺序完全失效。
func serveHTTP(ctx context.Context, s *ghttp.Server, drainTimeout time.Duration) error {
	// 先注册信号再启动监听，缩小「已监听但信号无人处理」的窗口。
	// 与 GoFrame 一致覆盖 SIGINT/SIGQUIT/SIGTERM（SIGKILL 不可捕获）。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := s.Start(); err != nil {
		return err
	}
	logx.Info(ctx, "http_server_started", "HTTP Server 已启动，等待终止信号", logx.Fields{
		"address":       s.GetListenedAddress(),
		"drain_timeout": drainTimeout.String(),
	})

	sig := <-sigCh
	logx.Info(ctx, "http_server_drain_begin", "收到终止信号，停止接收新连接并开始 drain 在途请求", logx.Fields{
		"signal":        sig.String(),
		"drain_timeout": drainTimeout.String(),
	})

	elapsed, timedOut, shutdownErr := drainHTTP(s.Shutdown, drainTimeout)
	if shutdownErr != nil {
		// 关闭错误不改变退出语义：后台组件仍需按顺序关闭。
		logx.Warning(ctx, "http_server_shutdown_failed", "HTTP Server 关闭返回错误（继续关闭秒杀后台组件）", logx.Fields{
			"error": shutdownErr.Error(),
		})
	}
	// 该日志是进程级关闭时序的显式证据：它必须早于 runtime 的关闭步骤日志。
	logx.Info(ctx, "http_server_stopped", "HTTP Server 已退出（优雅 drain 结束），开始关闭秒杀后台组件", logx.Fields{
		"signal":       sig.String(),
		"elapsed_ms":   elapsed.Milliseconds(),
		"timed_out":    timedOut,
		"drain_budget": drainTimeout.String(),
	})
	return nil
}

// drainHTTP 在预算内等待 HTTP Server 完成优雅 drain，
// 返回 (耗时, 是否超时, 关闭错误)。
//
// 超时后调用方必须继续执行后续关闭步骤：进程最终会退出，不能无限阻塞在 drain 上。
func drainHTTP(shutdown func() error, timeout time.Duration) (time.Duration, bool, error) {
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- shutdown()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return time.Since(started), false, err
	case <-timer.C:
		return time.Since(started), true, nil
	}
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

// qiniuCheck 校验七牛云 required 配置与指定 bucket 的真实可用性，供 `make up` 启动前预检调用。
// 复用 service.Upload().ValidateConfig（结构 + 存在性 + GetBucketInfo），与 serve 启动 fail-fast
// 共用同一事实源；失败返回错误 → gcmd 以非零退出码结束进程，错误只指认字段名/bucket，不泄漏凭据值。
func qiniuCheck(ctx context.Context, _ *gcmd.Parser) error {
	if err := service.Upload().ValidateConfig(ctx); err != nil {
		return err
	}
	fmt.Println("七牛云配置与 bucket 可用性校验通过")
	return nil
}

// adminCheck 校验超级管理员创建条件，供 `make up` 启动前预检调用。
// 复用 boot.CheckSuperAdminCondition（复用现有数据库访问与超管判断逻辑，不写 Shell SQL）；
// 失败返回错误 → gcmd 以非零退出码结束进程，错误只指认键名/依赖名，不泄漏密码值或堆栈。
func adminCheck(ctx context.Context, _ *gcmd.Parser) error {
	if err := boot.CheckSuperAdminCondition(ctx); err != nil {
		return err
	}
	fmt.Println("超级管理员创建条件校验通过")
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
