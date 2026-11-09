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
	"github.com/golang-migrate/migrate/v4/database"
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

// Preflight 执行 migrate 子命令的启动前配置校验。
//
// 必须在任何数据库连接建立之前调用（cmd 层的 migrate 子命令已保证顺序）：
//   - 逐项解析 database.default.*，非法 bool/int 不会静默回退；
//   - 数据库密码必须显式注入，不回退任何内置默认值；
//   - Kubernetes 严格模式下 DATABASE_DEFAULT_HOST / DATABASE_DEFAULT_NAME 必须由
//     环境变量显式提供且不得是回环地址。
//
// 与 serve 的 ValidateConfig 差异仅为「不要求 Redis 地址」——migration 不连接 Redis。
func Preflight(ctx context.Context) error {
	if err := config.ValidateMigration(ctx); err != nil {
		return gerror.Wrap(err, "migration 前置校验失败")
	}
	return nil
}

// Up 按版本升序执行所有未应用的 migration。
// 已全部应用时返回 nil（幂等，不视为错误）。
//
// 并发安全：golang-migrate 的 MySQL driver 在独立连接上执行
// `SELECT GET_LOCK(<db:schema_migrations 派生 id>, 10)`，整个 Up 期间持有该会话级锁；
// 另一个进程/实例只会等待最多 10s，随后返回 database.ErrLocked（可安全重试）。
// 连接断开（Pod 被删除、进程被杀）时 MySQL 自动释放锁，不会永久阻塞后续迁移。
func Up(ctx context.Context) error {
	logMigrationTarget(ctx)

	m, err := newMigrate(ctx)
	if err != nil {
		return setupError(ctx, err)
	}
	defer closeMigrate(ctx, m)

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			glog.Info(ctx, "没有待执行的 migration（数据库已是内嵌迁移的最新版本）")
			return nil
		}
		return migrationError(ctx, err)
	}
	glog.Info(ctx, "migration 执行完成，dirty=false")
	return nil
}

// Force 标记指定版本为已应用（dirty 置 false），不执行任何 SQL。
// 用于已有环境 baseline 接管，以及 dirty 状态的人工恢复。
//
// 该操作不校验、不修复实际 schema：把 version 标记为已应用后，后续 migrate up
// 会跳过该版本。误用会让数据库记录与真实结构不一致，因此只能由人工在评估后执行。
func Force(ctx context.Context, version uint) error {
	logMigrationTarget(ctx)

	m, err := newMigrate(ctx)
	if err != nil {
		return setupError(ctx, err)
	}
	defer closeMigrate(ctx, m)

	if err := m.Force(int(version)); err != nil {
		return forceError(ctx, version, err)
	}
	glog.Infof(ctx, "已强制标记版本 %d 为已应用（未执行任何 SQL，也未校验实际 schema）", version)
	return nil
}

// migrationError 把 golang-migrate 的失败翻译成可操作的运维语义。
//
// 两类可预期的失败必须与「真正的 SQL/DDL 失败」区分开，否则运维会在锁冲突或
// dirty 状态下误以为迁移内容有错：
//   - 数据库级锁被占用：本次未执行任何 DDL，可安全重试（K8s Job 由 backoffLimit 重试）；
//   - dirty：上一次迁移失败留下了中间态，禁止自动继续、禁止自动 force。
//
// 原始错误仍然通过 %w 保留，便于日志与排障；ERR 文本不含数据库密码。
func migrationError(ctx context.Context, err error) error {
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		return gerror.Wrapf(err,
			"数据库处于 dirty 状态（版本 %d 的 migration 曾执行失败）：已拒绝继续执行，也不会自动 force；"+
				"请人工核对实际 schema（MySQL DDL 隐式提交，失败时可能已部分生效）后再执行 `my-shop migrate force <version>`",
			dirty.Version,
		)
	}
	if err := lockError(ctx, err); err != nil {
		return err
	}
	return gerror.Wrap(err, "执行 migration 失败")
}

// forceError 包装 force 失败，并保留锁冲突的语义。
func forceError(ctx context.Context, version uint, err error) error {
	if lockErr := lockError(ctx, err); lockErr != nil {
		return gerror.Wrapf(lockErr, "force 版本 %d 未做任何修改", version)
	}
	return gerror.Wrapf(err, "force 版本 %d 失败（未执行任何 SQL）", version)
}

// setupError 处理迁移实例初始化阶段的失败。
//
// 注意：golang-migrate 的 MySQL driver 在 WithInstance 阶段就会通过 GET_LOCK
// 保护 schema_migrations 的首次创建（ensureVersionTable）。因此并发保护的
// 「等待 10s 后失败」可能发生在初始化阶段而不是 m.Up() 阶段，两处都必须翻译锁语义。
func setupError(ctx context.Context, err error) error {
	if lockErr := lockError(ctx, err); lockErr != nil {
		return lockErr
	}
	return err
}

// lockError 识别数据库级 migration 锁被占用的错误；非锁错误返回 nil。
func lockError(ctx context.Context, err error) error {
	if !errors.Is(err, database.ErrLocked) {
		return nil
	}
	glog.Warning(ctx, "migration_lock_timeout", "等待数据库级 migration 锁超时，本次未执行任何 DDL")
	return gerror.Wrap(err,
		"未获取到数据库级 migration 锁：另一个 migration 正在执行（GET_LOCK 等待 10s 超时）；"+
			"本次未执行任何 DDL，可安全重试",
	)
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

// closeMigrate 关闭 migration 实例：释放 iofs source、迁移专用连接与 sql.DB。
//
// 关闭失败不能改变迁移结果，但必须留痕：连接未关闭会让 GET_LOCK 会话级锁
// 一直持有到进程退出，后续 Job 只能等到 10s 超时后失败。
func closeMigrate(ctx context.Context, m *migrate.Migrate) {
	sourceErr, dbErr := m.Close()
	if sourceErr != nil {
		glog.Warningf(ctx, "migration source 关闭失败: %v", sourceErr)
	}
	if dbErr != nil {
		glog.Warningf(ctx, "migration 数据库连接关闭失败: %v", dbErr)
	}
}

// logMigrationTarget 输出迁移目标（host:port/db），便于在 Job 日志中确认连的是哪个库。
// 只输出地址与库名，不输出用户名与密码。
func logMigrationTarget(ctx context.Context) {
	host, hostErr := config.String(ctx, config.KeyDatabaseDefaultHost)
	port, portErr := config.String(ctx, config.KeyDatabaseDefaultPort)
	name, nameErr := config.String(ctx, config.KeyDatabaseDefaultName)
	if hostErr != nil || portErr != nil || nameErr != nil {
		// 配置错误由 Preflight 与 buildDSN 返回更准确的错误，这里不覆盖主错误。
		return
	}
	glog.Infof(ctx, "migration 目标：%s", net.JoinHostPort(host, port)+"/"+name)
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
