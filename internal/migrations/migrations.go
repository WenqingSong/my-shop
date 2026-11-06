// Package migrations 基于 golang-migrate v4 提供数据库迁移能力。
//
// 迁移文件通过 //go:embed 内嵌，运行时经 iofs source 挂载，不依赖工作目录；
// 版本号采用 14 位时间戳（YYYYMMDDHHMMSS），数字升序即时间升序；
// 追踪表 schema_migrations 与 MySQL GET_LOCK 并发串行化均由 golang-migrate 提供。
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strconv"
	"strings"

	go_mysql "github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/os/glog"

	migrate "github.com/golang-migrate/migrate/v4"
	migrate_mysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// migrationDir 是内嵌文件系统中存放迁移文件的子目录。
const migrationDir = "sql"

// embeddedMigrations 内嵌全部迁移文件（仅 up 文件）。
//
//go:embed sql/*.sql
var embeddedMigrations embed.FS

// migrationFS 是迁移源文件系统，默认使用内嵌迁移文件；
// 测试可替换为自定义 FS 以注入额外/非法迁移（仅测试内部使用）。
var migrationFS fs.FS = embeddedMigrations

// Up 按版本升序执行所有未应用的 migration。
// 已全部应用时返回 nil（幂等，不视为错误）。
func Up(ctx context.Context) error {
	m, err := newMigrate(ctx)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			glog.Info(ctx, "没有待执行的 migration")
			return nil
		}
		return gerror.Wrap(err, "执行 migration 失败")
	}
	glog.Info(ctx, "migration 执行完成")
	return nil
}

// Force 标记指定版本为已应用（dirty 置 false），不执行任何 SQL。
// 用于已有环境 baseline 接管，以及 dirty 状态的人工恢复。
func Force(ctx context.Context, version uint) error {
	m, err := newMigrate(ctx)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Force(int(version)); err != nil {
		return gerror.Wrapf(err, "force 版本 %d 失败", version)
	}
	glog.Infof(ctx, "已强制标记版本 %d 为已应用", version)
	return nil
}

// Status 只读返回当前 migration 状态：
// current 为已应用的最高版本（未初始化时为 0），dirty 为是否处于失败中间态，
// latest 为内嵌迁移文件的最高版本。
//
// 该函数只读，不创建 schema_migrations、不执行任何 DDL，保证 serve 的 readiness
// check 不产生副作用。
func Status(ctx context.Context) (current uint, dirty bool, latest uint, err error) {
	latest = latestVersion()

	dsn, err := buildDSN(ctx)
	if err != nil {
		return 0, false, latest, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, false, latest, gerror.Wrap(err, "打开数据库连接")
	}
	defer db.Close()

	var version int64
	err = db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || isTableNotExist(err) {
			// schema_migrations 不存在或为空：视为未初始化。
			return 0, false, latest, nil
		}
		return 0, false, latest, gerror.Wrap(err, "查询 schema_migrations")
	}
	return uint(version), dirty, latest, nil
}

// newMigrate 构造 golang-migrate 实例：
// source 使用内嵌 iofs，database 使用 MySQL driver（默认 GET_LOCK 串行化并发迁移）。
func newMigrate(ctx context.Context) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationFS, migrationDir)
	if err != nil {
		return nil, gerror.Wrap(err, "初始化 migration source")
	}

	dsn, err := buildDSN(ctx)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, gerror.Wrap(err, "打开数据库连接")
	}

	driver, err := migrate_mysql.WithInstance(db, &migrate_mysql.Config{})
	if err != nil {
		db.Close()
		return nil, gerror.Wrap(err, "初始化 MySQL migration driver")
	}

	m, err := migrate.NewWithInstance("iofs", src, "mysql", driver)
	if err != nil {
		db.Close()
		return nil, gerror.Wrap(err, "初始化 migration")
	}
	return m, nil
}

// closeMigrate 关闭 migration 实例（释放 source 与数据库连接），忽略关闭错误。
func closeMigrate(m *migrate.Migrate) {
	_, _ = m.Close()
}

// buildDSN 从 database.default.* 配置构造 MySQL DSN，启用 multiStatements 以支持
// 单文件多语句迁移（baseline 为多语句单文件）。
// 密码无内置默认回退：缺失（config 空且无 DATABASE_DEFAULT_PASS）时 fail-fast，
// 不回退 "root" 等默认密码（docs/design/migration.md §5）。
func buildDSN(ctx context.Context) (string, error) {
	read := &dbConfigReader{ctx: ctx}
	pass := read.value(config.KeyDatabaseDefaultPass)
	user := read.value(config.KeyDatabaseDefaultUser)
	host := read.value(config.KeyDatabaseDefaultHost)
	port := read.value(config.KeyDatabaseDefaultPort)
	name := read.value(config.KeyDatabaseDefaultName)
	charset := read.value(config.KeyDatabaseDefaultCharset)
	if err := read.err; err != nil {
		return "", err
	}
	if pass == "" {
		return "", gerror.New("未配置 database.default.pass（DATABASE_DEFAULT_PASS）：数据库密码无内置默认值，必须显式注入")
	}
	cfg := go_mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = name
	cfg.Params = map[string]string{"charset": charset}
	cfg.MultiStatements = true
	return cfg.FormatDSN(), nil
}

// dbConfigReader 顺序读取 database.default.* 并保留首个错误，
// 避免在同一段 DSN 构造里重复写 if err != nil。
type dbConfigReader struct {
	ctx context.Context
	err error
}

// value 读取字符串配置项，失败时记录首个错误。
func (r *dbConfigReader) value(key config.Key) string {
	if r.err != nil {
		return ""
	}
	value, err := config.String(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// latestVersion 返回内嵌迁移文件的最高版本号（按数字升序取最大值）。
func latestVersion() uint {
	var latest uint
	entries, err := fs.ReadDir(migrationFS, migrationDir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if v, err := parseVersion(e.Name()); err == nil && v > latest {
			latest = v
		}
	}
	return latest
}

// parseVersion 解析迁移文件名的前导数字版本号（形如 {version}_{title}.up.sql）。
func parseVersion(name string) (uint, error) {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0, fmt.Errorf("迁移文件名 %q 缺少 version 前缀", name)
	}
	v, err := strconv.ParseUint(name[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("迁移文件名 %q version 非法: %w", name, err)
	}
	return uint(v), nil
}

// isTableNotExist 判断是否为 MySQL「表不存在」错误（1146）。
func isTableNotExist(err error) bool {
	var mysqlErr *go_mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1146
}
