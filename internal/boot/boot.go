package boot

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
)

const (
	defaultServerAddress     = ":8000"
	defaultDBType            = "mysql"
	defaultDBHost            = "127.0.0.1"
	defaultDBPort            = "3306"
	defaultDBUser            = "root"
	defaultDBPass            = "root"
	defaultDBName            = "my_shop"
	defaultDBCharset         = "utf8"
	defaultRedisAddress      = "127.0.0.1:6379"
	defaultRedisDB           = 0
	defaultRedisPass         = ""
	defaultDependencyTimeout = 30 * time.Second
	checkRetryInterval       = time.Second
)

// createUsersTableSQL 是 users 表的幂等 DDL，可在每日重置环境下重复执行。
const createUsersTableSQL = `
CREATE TABLE IF NOT EXISTS users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username      VARCHAR(24)     NOT NULL,
  password_hash VARCHAR(60)     NOT NULL,
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createCategoriesTableSQL 是 categories 表的幂等 DDL，可在每日重置环境下重复执行。
// 复合唯一键 (parent_id, name) 保证同级分类名唯一，是同级重名判定的存储层兜底。
const createCategoriesTableSQL = `
CREATE TABLE IF NOT EXISTS categories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  parent_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  name       VARCHAR(64)     NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_parent_name (parent_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createAdminsTableSQL 是 admins 表的幂等 DDL（后台管理员身份）。
// username 唯一约束是「管理员用户名唯一」的存储层兜底。
const createAdminsTableSQL = `
CREATE TABLE IF NOT EXISTS admins (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username      VARCHAR(24)     NOT NULL,
  password_hash VARCHAR(60)     NOT NULL,
  status        TINYINT         NOT NULL DEFAULT 1,
  is_super      TINYINT         NOT NULL DEFAULT 0,
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_admin_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createRolesTableSQL 是 roles 表的幂等 DDL（角色）。
const createRolesTableSQL = `
CREATE TABLE IF NOT EXISTS roles (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)     NOT NULL,
  description VARCHAR(255)    NOT NULL DEFAULT '',
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_role_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createPermissionsTableSQL 是 permissions 表的幂等 DDL（权限，code 为稳定唯一 key）。
const createPermissionsTableSQL = `
CREATE TABLE IF NOT EXISTS permissions (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code        VARCHAR(64)     NOT NULL,
  name        VARCHAR(64)     NOT NULL,
  description VARCHAR(255)    NOT NULL DEFAULT '',
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_permission_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createAdminRolesTableSQL 是 admin_roles 关联表的幂等 DDL（管理员↔角色，复合主键保证唯一）。
const createAdminRolesTableSQL = `
CREATE TABLE IF NOT EXISTS admin_roles (
  admin_id BIGINT UNSIGNED NOT NULL,
  role_id  BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (admin_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// createRolePermissionsTableSQL 是 role_permissions 关联表的幂等 DDL（角色↔权限，复合主键保证唯一）。
const createRolePermissionsTableSQL = `
CREATE TABLE IF NOT EXISTS role_permissions (
  role_id       BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
`

// Bootstrap 应用配置（支持环境变量覆盖），校验 JWT 密钥、检查 MySQL/Redis 连通性，
// 并在 HTTP 服务启动前确保 users 与 categories 表存在。
func Bootstrap(ctx context.Context) error {
	applyServerConfig(ctx)
	if err := applyDatabaseConfig(ctx); err != nil {
		return err
	}
	applyRedisConfig(ctx)
	if _, err := auth.Secret(ctx); err != nil {
		return err
	}
	if err := waitForDependencies(ctx, dependencyTimeout(ctx)); err != nil {
		return err
	}
	if err := ensureTables(ctx); err != nil {
		return err
	}
	if err := seedSuperAdmin(ctx); err != nil {
		return err
	}
	return seedPermissions(ctx)
}

// ensureTables 幂等创建业务所需的数据表。
func ensureTables(ctx context.Context) error {
	if err := ensureUsersTable(ctx); err != nil {
		return err
	}
	if err := ensureCategoriesTable(ctx); err != nil {
		return err
	}
	if err := ensureAdminsTable(ctx); err != nil {
		return err
	}
	if err := ensureRolesTable(ctx); err != nil {
		return err
	}
	if err := ensurePermissionsTable(ctx); err != nil {
		return err
	}
	if err := ensureAdminRolesTable(ctx); err != nil {
		return err
	}
	return ensureRolePermissionsTable(ctx)
}

// ensureUsersTable 幂等创建 users 表。
func ensureUsersTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createUsersTableSQL); err != nil {
		return gerror.Wrap(err, "确保 users 表存在")
	}
	glog.Info(ctx, "users 表已就绪")
	return nil
}

// ensureCategoriesTable 幂等创建 categories 表。
func ensureCategoriesTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createCategoriesTableSQL); err != nil {
		return gerror.Wrap(err, "确保 categories 表存在")
	}
	glog.Info(ctx, "categories 表已就绪")
	return nil
}

// ensureAdminsTable 幂等创建 admins 表。
func ensureAdminsTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createAdminsTableSQL); err != nil {
		return gerror.Wrap(err, "确保 admins 表存在")
	}
	glog.Info(ctx, "admins 表已就绪")
	return nil
}

// ensureRolesTable 幂等创建 roles 表。
func ensureRolesTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createRolesTableSQL); err != nil {
		return gerror.Wrap(err, "确保 roles 表存在")
	}
	glog.Info(ctx, "roles 表已就绪")
	return nil
}

// ensurePermissionsTable 幂等创建 permissions 表。
func ensurePermissionsTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createPermissionsTableSQL); err != nil {
		return gerror.Wrap(err, "确保 permissions 表存在")
	}
	glog.Info(ctx, "permissions 表已就绪")
	return nil
}

// ensureAdminRolesTable 幂等创建 admin_roles 关联表。
func ensureAdminRolesTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createAdminRolesTableSQL); err != nil {
		return gerror.Wrap(err, "确保 admin_roles 表存在")
	}
	glog.Info(ctx, "admin_roles 表已就绪")
	return nil
}

// ensureRolePermissionsTable 幂等创建 role_permissions 关联表。
func ensureRolePermissionsTable(ctx context.Context) error {
	if _, err := g.DB().Exec(ctx, createRolePermissionsTableSQL); err != nil {
		return gerror.Wrap(err, "确保 role_permissions 表存在")
	}
	glog.Info(ctx, "role_permissions 表已就绪")
	return nil
}

// dependencyTimeout returns the maximum time the startup dependency check may
// keep retrying. It can be overridden via the STARTUP_DEPENDENCY_TIMEOUT
// environment variable (in seconds).
func dependencyTimeout(ctx context.Context) time.Duration {
	secs := cfgInt(ctx, "startup.dependency.timeout", int(defaultDependencyTimeout/time.Second))
	if secs <= 0 {
		secs = int(defaultDependencyTimeout / time.Second)
	}
	return time.Duration(secs) * time.Second
}

func applyServerConfig(ctx context.Context) {
	g.Server().SetAddr(cfgString(ctx, "server.address", defaultServerAddress))
}

func applyDatabaseConfig(ctx context.Context) error {
	node := gdb.ConfigNode{
		Type:     cfgString(ctx, "database.default.type", defaultDBType),
		Host:     cfgString(ctx, "database.default.host", defaultDBHost),
		Port:     cfgString(ctx, "database.default.port", defaultDBPort),
		User:     cfgString(ctx, "database.default.user", defaultDBUser),
		Pass:     cfgString(ctx, "database.default.pass", defaultDBPass),
		Name:     cfgString(ctx, "database.default.name", defaultDBName),
		Charset:  cfgString(ctx, "database.default.charset", defaultDBCharset),
		Protocol: "tcp",
		Debug:    cfgBool(ctx, "database.default.debug", false),
	}
	return gdb.SetConfigGroup(gdb.DefaultGroupName, gdb.ConfigGroup{node})
}

func applyRedisConfig(ctx context.Context) {
	cfg := &gredis.Config{
		Address: cfgString(ctx, "redis.default.address", defaultRedisAddress),
		Db:      cfgInt(ctx, "redis.default.db", defaultRedisDB),
		Pass:    cfgString(ctx, "redis.default.pass", defaultRedisPass),
	}
	gredis.SetConfig(cfg)
}

// waitForDependencies retries the dependency connectivity check until it
// succeeds or the timeout elapses, so that a freshly started MySQL/Redis
// container has time to become ready.
func waitForDependencies(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		lastErr = checkDependencies(ctx)
		if lastErr == nil {
			glog.Info(ctx, "all dependencies are reachable (mysql, redis)")
			return nil
		}
		if time.Now().After(deadline) {
			return gerror.Wrapf(lastErr, "dependency check failed after %s", timeout)
		}
		glog.Warningf(ctx, "dependency check failed, retrying: %v", lastErr)
		select {
		case <-ctx.Done():
			return gerror.Wrapf(ctx.Err(), "dependency check canceled")
		case <-time.After(checkRetryInterval):
		}
	}
}

// checkDependencies pings MySQL and Redis exactly once each.
func checkDependencies(ctx context.Context) error {
	if err := g.DB().PingMaster(); err != nil {
		return gerror.Wrap(err, "mysql connectivity check failed")
	}
	if _, err := g.Redis().Do(ctx, "PING"); err != nil {
		return gerror.Wrap(err, "redis connectivity check failed")
	}
	return nil
}

func cfgString(ctx context.Context, key, def string) string {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil {
		glog.Warningf(ctx, "read config %q failed: %v", key, err)
		return def
	}
	if v == nil {
		return def
	}
	return v.String()
}

func cfgBool(ctx context.Context, key string, def bool) bool {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil {
		glog.Warningf(ctx, "read config %q failed: %v", key, err)
		return def
	}
	if v == nil {
		return def
	}
	return v.Bool()
}

func cfgInt(ctx context.Context, key string, def int) int {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil {
		glog.Warningf(ctx, "read config %q failed: %v", key, err)
		return def
	}
	if v == nil {
		return def
	}
	return v.Int()
}
