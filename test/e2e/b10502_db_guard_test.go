package e2e

// B1-05-02：E2E 测试的数据库隔离防护。
//
// setupIsolationServer / setupOrderServer 会 DELETE users / admins / roles / permissions
// 等身份与业务表。命中禁用清单或无法确认实际库名时立即失败，不执行任何写入。
//
// 注意：GoFrame 的 g.DB() 默认读取 manifest/config/config.yaml（env 仅作为缺省回退），
// 因此必须先经 boot.ApplyDatabaseConfig 把统一配置层（环境变量优先）注入 gdb，再读取
// SELECT DATABASE()；否则会把配置文件的默认端口当成「实际连接」。

import (
	"context"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// b10502ForbiddenDBs 是严禁被 E2E 破坏性清理的数据库（名称 → 用途）。
var b10502ForbiddenDBs = map[string]string{
	"my_shop":               "开发/生产用途库",
	"my_shop_staging":       "历史数据 staging 库（只读源）",
	"my_shop_b10304_verify": "B1-03-04 历史数据迁移验证库",
}

// requireIsolatedDestructiveDBE2E 确认 E2E 清理只作用于独立测试库。
// 以实际连接（SELECT DATABASE()）为准，并与统一配置层的库名交叉核对。
func requireIsolatedDestructiveDBE2E(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	// 先按统一配置层（环境变量优先）初始化 gdb，保证后续 g.DB() 与迁移/服务使用同一目标库。
	if err := boot.ApplyDatabaseConfig(ctx); err != nil {
		t.Fatalf("初始化数据库配置失败：%v（拒绝执行破坏性 E2E 清理）", err)
	}
	v, err := g.DB().GetValue(ctx, "SELECT DATABASE()")
	if err != nil {
		t.Fatalf("无法确认实际连接的数据库：%v（拒绝执行破坏性 E2E 清理）", err)
	}
	actual := strings.TrimSpace(v.String())
	if actual == "" {
		t.Fatal("实际连接的数据库名为空（拒绝执行破坏性 E2E 清理）")
	}

	configured, err := config.String(ctx, config.KeyDatabaseDefaultName)
	if err != nil {
		t.Fatalf("读取 database.default.name 失败：%v（拒绝执行破坏性 E2E 清理）", err)
	}
	if strings.TrimSpace(configured) != actual {
		t.Fatalf("配置库名 %q 与实际连接 %q 不一致（拒绝执行破坏性 E2E 清理）", configured, actual)
	}

	if reason, forbidden := b10502ForbiddenDBs[actual]; forbidden {
		t.Fatalf("拒绝在 %q（%s）执行破坏性 E2E 清理：请把 DATABASE_DEFAULT_NAME 指向独立测试库", actual, reason)
	}
}
