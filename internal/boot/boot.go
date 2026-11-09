package boot

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// 说明：配置默认值统一由 internal/config 契约注册表持有，本包不再保留第二份默认值，
// 以免「代码默认值」与契约文档/注册表漂移。此处仅保留非配置类常量。
const (
	defaultDependencyTimeout = 30 * time.Second
	defaultGracefulTimeout   = 10
	checkRetryInterval       = time.Second
)

// Bootstrap 应用配置（支持环境变量覆盖），校验 JWT 密钥、检查 MySQL/Redis 连通性，
// 只读校验 schema 就绪（不建表、不执行 DDL），随后执行超级管理员与权限 seed，
// 最后交由 serve 启动 HTTP。数据库表结构由 `my-shop migrate up` 负责建立。
func Bootstrap(ctx context.Context) error {
	// T2-01：配置校验必须是启动的第一步，且早于任何数据库/Redis 连接装配：
	//   - Validate 逐项解析全部已登记配置，非法 bool/int/duration 立即失败（不静默回退）；
	//   - ValidateExternalEndpoints 在 Kubernetes 严格模式下要求关键连接参数由环境变量显式注入。
	if err := ValidateConfig(ctx); err != nil {
		return err
	}
	if err := applyServerConfig(ctx); err != nil {
		return err
	}
	if err := applyDatabaseConfig(ctx); err != nil {
		return err
	}
	if err := applyRedisConfig(ctx); err != nil {
		return err
	}
	if _, err := auth.Secret(ctx); err != nil {
		return err
	}
	timeout, err := dependencyTimeout(ctx)
	if err != nil {
		return err
	}
	if err := waitForDependencies(ctx, timeout); err != nil {
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

// ValidateConfig 校验统一配置契约：先逐项解析全部登记配置，再按部署模式做严格连接校验。
//
// 该方法不建立任何连接，可被 serve 与该任务之外的运维命令复用；必须在实际连接数据库与
// Redis 之前调用（Bootstrap 已保证顺序）。
func ValidateConfig(ctx context.Context) error {
	if err := config.Validate(ctx); err != nil {
		return gerror.Wrap(err, "配置校验失败")
	}
	if err := config.ValidateExternalEndpoints(ctx); err != nil {
		return gerror.Wrap(err, "部署严格配置校验失败")
	}
	return nil
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

// dependencyTimeout 返回启动依赖检查允许重试的最长时间
// （startup.dependency.timeout，环境变量 STARTUP_DEPENDENCY_TIMEOUT，单位秒）。
// 配置非正值时保持既有语义：回退默认 30s。
func dependencyTimeout(ctx context.Context) (time.Duration, error) {
	secs, err := config.Int(ctx, config.KeyStartupDependencyTimeout)
	if err != nil {
		return 0, err
	}
	if secs <= 0 {
		secs = int(defaultDependencyTimeout / time.Second)
	}
	return time.Duration(secs) * time.Second, nil
}

func applyServerConfig(ctx context.Context) error {
	addr, err := ServerAddress(ctx)
	if err != nil {
		return err
	}
	drain, err := ServerGracefulTimeout(ctx)
	if err != nil {
		return err
	}
	g.Server().SetAddr(addr)
	// 优雅关闭 HTTP drain 超时（秒）：SIGINT/SIGTERM 后停收新连接、等待在途请求完成的最长时间。
	// 对应环境变量 SERVER_GRACEFUL_TIMEOUT；默认 10s。
	// 该值同时是 T2-03 终止预算的 http_drain_timeout 段（见 ServerGracefulTimeout）。
	g.Server().SetGracefulTimeout(int(drain / time.Second))
	return nil
}

// ServerAddress 返回 HTTP 监听地址（server.address，环境变量 SERVER_ADDRESS）。
// serve 需要先自行监听（受控 listener）再交给 GoFrame，因此与 applyServerConfig 共用同一来源。
func ServerAddress(ctx context.Context) (string, error) {
	return config.String(ctx, config.KeyServerAddress)
}

// ServerGracefulTimeout 返回 HTTP drain 预算，供 serve 的终止流程与终止预算计算共用同一配置来源
// （server.graceful_timeout，环境变量 SERVER_GRACEFUL_TIMEOUT）。配置值为秒，非正值回退默认 10s。
func ServerGracefulTimeout(ctx context.Context) (time.Duration, error) {
	seconds, err := config.Int(ctx, config.KeyServerGracefulTimeout)
	if err != nil {
		return 0, err
	}
	if seconds <= 0 {
		seconds = defaultGracefulTimeout
	}
	return time.Duration(seconds) * time.Second, nil
}

func applyDatabaseConfig(ctx context.Context) error {
	timeZoneExtra, err := databaseTimeZoneExtra(ctx)
	if err != nil {
		return err
	}
	read := &configReader{ctx: ctx}
	node := gdb.ConfigNode{
		Type:     read.string(config.KeyDatabaseDefaultType),
		Host:     read.string(config.KeyDatabaseDefaultHost),
		Port:     read.string(config.KeyDatabaseDefaultPort),
		User:     read.string(config.KeyDatabaseDefaultUser),
		Pass:     read.string(config.KeyDatabaseDefaultPass),
		Name:     read.string(config.KeyDatabaseDefaultName),
		Charset:  read.string(config.KeyDatabaseDefaultCharset),
		Protocol: "tcp",
		Debug:    read.boolean(config.KeyDatabaseDefaultDebug),
		Extra:    timeZoneExtra,
	}
	if err := read.err; err != nil {
		return err
	}
	// 数据库密码无内置默认回退：缺失（config 空且无 DATABASE_DEFAULT_PASS）时 fail-fast，
	// 不回退 "root" 等默认密码（对齐 auth.Secret() 的 fail-fast 语义，见 docs/design/deployment.md §3）。
	if node.Pass == "" {
		return gerror.New("未配置 database.default.pass（DATABASE_DEFAULT_PASS）：数据库密码无内置默认值，必须显式注入")
	}
	return gdb.SetConfigGroup(gdb.DefaultGroupName, gdb.ConfigGroup{node})
}

// databaseTimeZoneExtra 依据 dashboard.timezone（默认 Asia/Shanghai）计算 MySQL 会话 time_zone
// 的 DSN 参数，使连接层的 NOW()/CURDATE()/CURRENT_TIMESTAMP 写值与 Go 端日期切分时区一致，
// 消除 Go↔MySQL 漂移（详见 docs/design/dashboard.md §3 时区落地设计）。
// 配置非法或含夏令时（DST）时 fail-fast 返回错误，不静默回退：DST 时区无法用固定偏移表达，
// 跨切换会与 Go 端 DST 感知的边界计算漂移（CLEAN-002）。
func databaseTimeZoneExtra(ctx context.Context) (string, error) {
	name, err := config.String(ctx, config.KeyDashboardTimezone)
	if err != nil {
		return "", err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return "", gerror.Wrapf(err, "dashboard.timezone 配置非法 %q", name)
	}
	if hasDST(loc) {
		return "", gerror.Newf("dashboard.timezone %q 含夏令时，仅支持无 DST 时区（如 Asia/Shanghai）", name)
	}
	_, offset := time.Now().In(loc).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	tz := fmt.Sprintf("%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
	// DSN 系统变量值须 url.QueryEscape 并用单引号包裹（%27），驱动会执行 SET time_zone='+08:00'。
	return "time_zone=%27" + url.QueryEscape(tz) + "%27", nil
}

// hasDST 判断时区是否使用夏令时：若当前年内任意时刻的绝对偏移发生变化，则视为存在 DST 切换。
// 采样当前年每日（含闰年 366 天），足以覆盖现实中的 DST 起始/结束切换点。
// 仅用于在启动时拒绝含 DST 的时区（CLEAN-002），不承诺给出精确的切换区间。
func hasDST(loc *time.Location) bool {
	now := time.Now()
	base := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	_, baseOff := base.In(loc).Zone()
	for i := 0; i <= 366; i++ {
		ts := base.AddDate(0, 0, i)
		if _, off := ts.In(loc).Zone(); off != baseOff {
			return true
		}
	}
	return false
}

func applyRedisConfig(ctx context.Context) error {
	read := &configReader{ctx: ctx}
	cfg := &gredis.Config{
		Address: read.string(config.KeyRedisDefaultAddress),
		Db:      read.integer(config.KeyRedisDefaultDB),
		Pass:    read.string(config.KeyRedisDefaultPass),
	}
	if err := read.err; err != nil {
		return err
	}
	gredis.SetConfig(cfg)
	return nil
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

// configReader 顺序读取一组配置项并保留首个错误。
//
// 用于「一次性装配一个配置节点」的场景（DB/Redis），避免为每个字段重复写
// if err != nil；首个错误被保留后短路后续读取，最终由调用方统一返回，
// 不会因为后续字段成功而覆盖前面的配置错误。
type configReader struct {
	ctx context.Context
	err error
}

// string 读取字符串配置项。
func (r *configReader) string(key config.Key) string {
	if r.err != nil {
		return ""
	}
	value, err := config.String(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// boolean 读取布尔配置项。
func (r *configReader) boolean(key config.Key) bool {
	if r.err != nil {
		return false
	}
	value, err := config.Bool(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// integer 读取 int 配置项。
func (r *configReader) integer(key config.Key) int {
	if r.err != nil {
		return 0
	}
	value, err := config.Int(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}
