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
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
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

// Bootstrap 应用配置（支持环境变量覆盖），校验 JWT 密钥、检查 MySQL/Redis 连通性，
// 只读校验 schema 就绪（不建表、不执行 DDL），随后执行超级管理员与权限 seed，
// 最后交由 serve 启动 HTTP。数据库表结构由 `my-shop migrate up` 负责建立。
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
	if err := checkSchemaReady(ctx); err != nil {
		return err
	}
	if err := seedSuperAdmin(ctx); err != nil {
		return err
	}
	return seedPermissions(ctx)
}

// checkSchemaReady 只读校验 schema 就绪：schema_migrations 已初始化、非 dirty、
// 且已应用到最新版本。任一不满足即 fail-fast，不执行任何 DDL。
func checkSchemaReady(ctx context.Context) error {
	current, dirty, latest, err := migrations.Status(ctx)
	if err != nil {
		return gerror.Wrap(err, "读取数据库 migration 状态")
	}
	if dirty {
		return gerror.Newf("数据库 migration 处于 dirty 状态（版本 %d 执行失败），请人工执行 `my-shop migrate force <version>` 恢复后再启动", current)
	}
	if current < latest {
		return gerror.Newf("数据库 schema 未就绪（当前版本 %d < 最新版本 %d），请先执行 `my-shop migrate up`", current, latest)
	}
	glog.Infof(ctx, "schema 已就绪（版本 %d）", current)
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
