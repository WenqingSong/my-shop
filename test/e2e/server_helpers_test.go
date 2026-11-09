package e2e

// 本文件承载迁移后仍需要、但 cmd 包未导出的生产入口辅助逻辑的等价实现，
// 供集成/E2E 测试复用，避免在测试包内重复维护「服务启动同款」行为。

import (
	"context"

	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/storage"
)

// registerMetricsRoute 在指标开关开启时挂载 GET /metrics（与生产 serve 同款注册）。
// 测试环境默认关闭指标（flash_sale.metrics.enabled=false），本函数通常为 no-op。
// 迁移后无法访问 cmd 包的未导出函数，故在此复制等价逻辑，保持与生产入口行为一致。
func registerMetricsRoute(ctx context.Context, s *ghttp.Server) {
	if !metrics.Enabled(ctx) {
		return
	}
	s.BindHandler("GET:/metrics", func(r *ghttp.Request) {
		metrics.Handler().ServeHTTP(r.Response.Writer, r.Request)
	})
}

// configureBannerStorage 初始化轮播图图片的本地存储：seed 3 张占位图，并将 banner 目录
// 映射为 /storage/banners 静态路由（与生产 serve 同款）。迁移后无法访问 cmd 包未导出函数，
// 故在此复制等价逻辑。
func configureBannerStorage(ctx context.Context, s *ghttp.Server) error {
	root, err := config.String(ctx, config.KeyStorageLocalRoot)
	if err != nil {
		return err
	}
	local := storage.NewLocal(root)
	if _, err := local.PrepareBannerPlaceholders(ctx); err != nil {
		return err
	}
	s.AddStaticPath(storage.BannerURLPrefix, local.BannerDir())
	return nil
}
