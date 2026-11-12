package migrations

// B1-05-02：迁移测试的数据库隔离防护。
//
// setupCleanDB / setupSyntheticMigrationDB 会 DROP 全部业务表，属于破坏性操作。
// 历史事故表明：一旦 DATABASE_DEFAULT_NAME 仍指向 my_shop / my_shop_staging /
// my_shop_b10304_verify，测试会直接抹掉真实数据。
//
// 本防护以「实际连接」为准（SELECT DATABASE()），不以配置文件或环境变量名称推断；
// 命中禁用清单或无法确认库名时立即失败，不执行任何 DDL。

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// b10502ForbiddenDBs 是严禁被破坏性测试清空的数据库（名称 → 用途）。
var b10502ForbiddenDBs = map[string]string{
	"my_shop":               "开发/生产用途库",
	"my_shop_staging":       "历史数据 staging 库（只读源）",
	"my_shop_b10304_verify": "B1-03-04 历史数据迁移验证库",
}

// requireIsolatedDestructiveDB 在任何 DROP/DELETE 之前确认实际连接的库不是禁用库。
// 无法确认（查询失败、库名为空、配置值与实际连接不一致）同样视为阻塞，直接失败。
func requireIsolatedDestructiveDB(t *testing.T, db *sql.DB) {
	t.Helper()

	var actual string
	if err := db.QueryRowContext(context.Background(), "SELECT DATABASE()").Scan(&actual); err != nil {
		t.Fatalf("无法确认实际连接的数据库：%v（拒绝执行破坏性迁移测试）", err)
	}
	actual = strings.TrimSpace(actual)
	if actual == "" {
		t.Fatal("实际连接的数据库名为空（拒绝执行破坏性迁移测试）")
	}

	configured, err := config.String(context.Background(), config.KeyDatabaseDefaultName)
	if err != nil {
		t.Fatalf("读取 database.default.name 失败：%v（拒绝执行破坏性迁移测试）", err)
	}
	if strings.TrimSpace(configured) != actual {
		t.Fatalf("配置库名 %q 与实际连接 %q 不一致（拒绝执行破坏性迁移测试）", configured, actual)
	}

	if reason, forbidden := b10502ForbiddenDBs[actual]; forbidden {
		t.Fatalf("拒绝在 %q（%s）执行破坏性迁移测试：请把 DATABASE_DEFAULT_NAME 指向独立测试库", actual, reason)
	}
}
