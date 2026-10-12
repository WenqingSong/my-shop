package legacymigrate

import (
	"context"
	"database/sql"
	"fmt"
	"net"

	go_mysql "github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
)

// 数据库连接默认值，与 internal/migrations 及 internal/boot 保持一致（开发环境默认）。
const (
	defaultDBHost        = "127.0.0.1"
	defaultDBPort        = "3306"
	defaultDBUser        = "root"
	defaultDBPass        = "root"
	defaultDBName        = "my_shop"
	defaultStagingDBName = "my_shop_staging"
)

// OpenConnections 依据配置打开 staging 源库与目标库连接。
// source 指向隔离 staging schema（legacy.staging.name，默认 my_shop_staging）；
// target 指向目标库（database.default.name，默认 my_shop）。
// host/port/user/pass 复用 database.default（同一 MySQL 实例），仅 schema 名不同，
// 保证原始 SQL 只进入 staging、目标库连接绝不执行 SET FOREIGN_KEY_CHECKS=0（AC-002）。
func OpenConnections(ctx context.Context) (source, target *sql.DB, err error) {
	host := cfgString(ctx, "database.default.host", defaultDBHost)
	port := cfgString(ctx, "database.default.port", defaultDBPort)
	user := cfgString(ctx, "database.default.user", defaultDBUser)
	pass := cfgString(ctx, "database.default.pass", defaultDBPass)
	targetName := cfgString(ctx, "database.default.name", defaultDBName)
	stagingName := cfgString(ctx, "legacy.staging.name", defaultStagingDBName)

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
	cfg.Params = map[string]string{"charset": "utf8"}
	cfg.MultiStatements = false
	return cfg.FormatDSN()
}

// cfgString 从 GoFrame 配置读取字符串，读取失败回退默认值。
func cfgString(ctx context.Context, key, def string) string {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil || v == nil {
		return def
	}
	return v.String()
}
