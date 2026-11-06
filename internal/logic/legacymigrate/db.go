package legacymigrate

import (
	"context"
	"database/sql"
	"fmt"
	"net"

	go_mysql "github.com/go-sql-driver/mysql"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// OpenConnections 依据配置打开 staging 源库与目标库连接。
// source 指向隔离 staging schema（legacy.staging.name，默认 my_shop_staging）；
// target 指向目标库（database.default.name，默认 my_shop）。
// host/port/user/pass 复用 database.default（同一 MySQL 实例），仅 schema 名不同，
// 保证原始 SQL 只进入 staging、目标库连接绝不执行 SET FOREIGN_KEY_CHECKS=0（AC-002）。
func OpenConnections(ctx context.Context) (source, target *sql.DB, err error) {
	host, err := config.String(ctx, config.KeyDatabaseDefaultHost)
	if err != nil {
		return nil, nil, err
	}
	port, err := config.String(ctx, config.KeyDatabaseDefaultPort)
	if err != nil {
		return nil, nil, err
	}
	user, err := config.String(ctx, config.KeyDatabaseDefaultUser)
	if err != nil {
		return nil, nil, err
	}
	pass, err := config.String(ctx, config.KeyDatabaseDefaultPass)
	if err != nil {
		return nil, nil, err
	}
	targetName, err := config.String(ctx, config.KeyDatabaseDefaultName)
	if err != nil {
		return nil, nil, err
	}
	stagingName, err := config.String(ctx, config.KeyLegacyStagingName)
	if err != nil {
		return nil, nil, err
	}

	// 数据库密码无内置默认回退：缺失（config 空且无 DATABASE_DEFAULT_PASS）时 fail-fast，
	// 不回退 "root" 等默认密码（docs/design/deployment.md §3 无内置默认回退）。
	if pass == "" {
		return nil, nil, fmt.Errorf("未配置 database.default.pass（DATABASE_DEFAULT_PASS）：数据库密码无内置默认值，必须显式注入")
	}
	if stagingName == targetName {
		return nil, nil, fmt.Errorf("staging 库与目标库同名 %q，必须隔离（AC-002）", stagingName)
	}

	source, err = openDB(buildDSN(host, port, user, pass, stagingName))
	if err != nil {
		return nil, nil, fmt.Errorf("打开 staging 库连接: %w", err)
	}
	target, err = openDB(buildDSN(host, port, user, pass, targetName))
	if err != nil {
		source.Close()
		return nil, nil, fmt.Errorf("打开目标库连接: %w", err)
	}
	return source, target, nil
}

// openDB 建立连接并 Ping 校验连通性。
func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// buildDSN 构造 MySQL DSN（multiStatements 关闭：迁移工具只执行单语句，避免注入多语句）。
func buildDSN(host, port, user, pass, name string) string {
	cfg := go_mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = name
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	cfg.MultiStatements = false
	return cfg.FormatDSN()
}
