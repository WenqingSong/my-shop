package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// 本文件使用「真实二进制 + 真实独立进程 + 真实 MySQL」验证 migration 的进程级语义，
// 不以 goroutine 或 Mock 代替：
//   - 两个进程并发执行时由数据库级 GET_LOCK 互斥；
//   - 锁被占用时另一个进程等待或明确失败，不会并发改 Schema；
//   - 持锁会话断开（Pod 被杀）后锁自动释放，不永久阻塞；
//   - 进程被 SIGKILL 中途终止后，数据库状态仍可解释、可恢复；
//   - migrate 不依赖 Redis / Kafka / JWT / HTTP 等无关服务。

var (
	buildOnce  sync.Once
	buildPath  string
	buildError error
)

// repoRoot 返回仓库根目录（go test 的工作目录是包目录 internal/migrations）。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("解析仓库根目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "manifest", "config", "config.yaml")); err != nil {
		t.Fatalf("仓库根目录缺少 manifest/config/config.yaml: %v", err)
	}
	return root
}

// migrateBinary 构建（并缓存）待测的真实二进制。
func migrateBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "my-shop-t205-*")
		if err != nil {
			buildError = fmt.Errorf("创建临时目录: %w", err)
			return
		}
		buildPath = filepath.Join(dir, "my-shop")
		cmd := exec.Command("go", "build", "-trimpath", "-o", buildPath, ".")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildError = fmt.Errorf("go build 失败: %w\n%s", err, out)
		}
	})
	if buildError != nil {
		t.Fatalf("%v", buildError)
	}
	return buildPath
}

// mustConfig 读取数据库配置（测试机环境变量优先，其次代码默认值）。
func mustConfig(t *testing.T, key config.Key) string {
	t.Helper()
	value, err := config.String(context.Background(), key)
	if err != nil {
		t.Fatalf("读取配置 %s: %v", key, err)
	}
	return value
}

// baseChildEnv 返回子进程的确定环境：数据库连接来自当前测试配置，
// 并显式关闭严格模式（需要严格模式的用例自行覆盖）。
func baseChildEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"DATABASE_DEFAULT_TYPE":                 mustConfig(t, config.KeyDatabaseDefaultType),
		"DATABASE_DEFAULT_HOST":                 mustConfig(t, config.KeyDatabaseDefaultHost),
		"DATABASE_DEFAULT_PORT":                 mustConfig(t, config.KeyDatabaseDefaultPort),
		"DATABASE_DEFAULT_USER":                 mustConfig(t, config.KeyDatabaseDefaultUser),
		"DATABASE_DEFAULT_PASS":                 mustConfig(t, config.KeyDatabaseDefaultPass),
		"DATABASE_DEFAULT_NAME":                 mustConfig(t, config.KeyDatabaseDefaultName),
		"DATABASE_DEFAULT_CHARSET":              mustConfig(t, config.KeyDatabaseDefaultCharset),
		"DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS": "false",
	}
}

// withEnv 在 baseChildEnv 上覆盖若干键。
func withEnv(t *testing.T, overrides map[string]string) map[string]string {
	t.Helper()
	env := baseChildEnv(t)
	for key, value := range overrides {
		env[key] = value
	}
	return env
}

// childProcessEnv 把 map 转成 exec.Cmd 需要的环境变量列表（同名键以 map 为准）。
func childProcessEnv(env map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(env))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, override := env[name]; override {
			continue
		}
		out = append(out, kv)
	}
	for name, value := range env {
		out = append(out, name+"="+value)
	}
	return out
}

// newMigrateCommand 构造子进程命令（工作目录为仓库根，保证能找到 manifest/config/config.yaml）。
func newMigrateCommand(t *testing.T, env map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(migrateBinary(t), args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = childProcessEnv(env)
	return cmd
}

// runMigrateProcess 同步执行 migrate 子命令，返回合并输出与退出码。
func runMigrateProcess(t *testing.T, env map[string]string, args ...string) (string, int, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, migrateBinary(t), args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = childProcessEnv(env)

	started := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(started)

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("migrate %v 超时（90s）\n%s", args, out)
	}
	if err == nil {
		return string(out), 0, elapsed
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("执行 migrate %v 失败: %v\n%s", args, err, out)
	}
	return string(out), exitErr.ExitCode(), elapsed
}

// TestMigrateProcessIsIndependentOfOtherServices 验证「Migration 不依赖应用启动流程」：
// 子进程环境里 Redis 不可达、Kafka 配置非法/开启、JWT 与七牛凭据缺失，
// migrate up 仍必须成功建表并返回 0。
func TestMigrateProcessIsIndependentOfOtherServices(t *testing.T) {
	db := setupCleanDB(t)
	env := withEnv(t, map[string]string{
		// migration 不连接 Redis：地址故意不可达。
		"REDIS_DEFAULT_ADDRESS": "127.0.0.1:1",
		// migration 不装配 Kafka：开关打开且 broker 非法。
		"FLASH_SALE_KAFKA_ENABLED": "true",
		"FLASH_SALE_KAFKA_BROKERS": "127.0.0.1:1",
		// serve 需要的凭据全部缺失：migration 不应读取它们。
		"AUTH_JWT_SECRET":      "",
		"ADMIN_SUPER_PASSWORD": "",
		"QINIU_ACCESS_KEY":     "",
		"QINIU_SECRET_KEY":     "",
		"QINIU_BUCKET":         "",
		"QINIU_DOMAIN":         "",
	})

	out, code, _ := runMigrateProcess(t, env, "migrate", "up")
	if code != 0 {
		t.Fatalf("migrate up 不应依赖 Redis/Kafka/JWT/七牛，退出码 %d\n%s", code, out)
	}
	if !strings.Contains(out, "migration 执行完成") {
		t.Fatalf("输出未包含迁移完成日志:\n%s", out)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("期望版本 %d，实际 %d", latestMigrationVersion, v)
	}
	if dirtyState(t, db) {
		t.Error("成功后 dirty 必须为 false")
	}
	for _, table := range businessTables {
		if !tableExists(t, db, table) {
			t.Errorf("表 %s 未被创建", table)
		}
	}

	// 重复执行：同一数据库第二次 migrate up 必须返回 0 且不重复执行已完成迁移。
	secondOut, secondCode, _ := runMigrateProcess(t, env, "migrate", "up")
	if secondCode != 0 {
		t.Fatalf("重复执行 migrate up 应成功，退出码 %d\n%s", secondCode, secondOut)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("重复执行后版本不应变化，期望 %d，实际 %d", latestMigrationVersion, v)
	}
}

// TestMigrateProcessConcurrentExecution 覆盖真实双进程并发：
// 两个独立进程同时对空库执行 migrate up，最终 Schema 与迁移记录必须唯一且正确。
func TestMigrateProcessConcurrentExecution(t *testing.T) {
	db := setupCleanDB(t)
	env := baseChildEnv(t)

	first := newMigrateCommand(t, env, "migrate", "up")
	second := newMigrateCommand(t, env, "migrate", "up")
	var firstOut, secondOut strings.Builder
	first.Stdout, first.Stderr = &firstOut, &firstOut
	second.Stdout, second.Stderr = &secondOut, &secondOut

	if err := first.Start(); err != nil {
		t.Fatalf("启动进程 A: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("启动进程 B: %v", err)
	}
	firstErr := first.Wait()
	secondErr := second.Wait()

	for name, result := range map[string]struct {
		err error
		out string
	}{
		"进程 A": {firstErr, firstOut.String()},
		"进程 B": {secondErr, secondOut.String()},
	} {
		if result.err == nil {
			continue
		}
		var exitErr *exec.ExitError
		if !errors.As(result.err, &exitErr) {
			t.Fatalf("%s 执行失败: %v", name, result.err)
		}
		// 允许的唯一失败是「锁等待 10s 超时」：说明另一个进程仍在迁移。
		// 这属于既定策略（失败 + 由 Job backoffLimit 重试），不是并发破坏。
		if !strings.Contains(result.out, "未获取到数据库级 migration 锁") {
			t.Fatalf("%s 并发执行出现非锁冲突失败（退出码 %d）:\n%s", name, exitErr.ExitCode(), result.out)
		}
		t.Logf("%s 因锁等待超时退出，符合「明确失败」策略", name)
	}

	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("并发执行后期望版本 %d，实际 %d", latestMigrationVersion, v)
	}
	var rows int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatalf("统计 schema_migrations: %v", err)
	}
	if rows != 1 {
		t.Errorf("schema_migrations 必须只有一行，实际 %d 行", rows)
	}
	for _, table := range businessTables {
		if !tableExists(t, db, table) {
			t.Errorf("并发执行后表 %s 缺失", table)
		}
	}

	// 并发结束后再执行一次必须成功（幂等收敛）。
	if out, code, _ := runMigrateProcess(t, env, "migrate", "up"); code != 0 {
		t.Fatalf("并发执行后再迁移应成功，退出码 %d\n%s", code, out)
	}
}

// TestMigrateProcessWaitsForLockRelease 覆盖「另一个进程持有锁时等待」：
// 锁被占用期间子进程不得执行 DDL；锁释放后必须完成迁移并返回 0。
func TestMigrateProcessWaitsForLockRelease(t *testing.T) {
	db := setupCleanDB(t)
	env := baseChildEnv(t)
	holder := holdLock(t)

	cmd := newMigrateCommand(t, env, "migrate", "up")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 migrate 进程: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		t.Fatalf("锁被占用时子进程不得提前结束: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if tableExists(t, db, "schema_migrations") {
		t.Fatal("等待锁期间不得创建 schema_migrations")
	}

	if err := holder.Close(); err != nil {
		t.Logf("关闭持锁连接: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("锁释放后 migrate 应成功: %v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("锁释放后 migrate 未在 30s 内完成:\n%s", out.String())
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("期望版本 %d，实际 %d", latestMigrationVersion, v)
	}
}

// TestMigrateProcessFailsOnLockTimeout 覆盖「获取锁超时」：
// 另一个会话持续持锁时，migrate up 必须在约 10s 后明确失败，且不执行任何 DDL；
// 锁释放后重试必须成功（对应 K8s Job 的失败重试语义）。
func TestMigrateProcessFailsOnLockTimeout(t *testing.T) {
	db := setupCleanDB(t)
	env := baseChildEnv(t)
	holder := holdLock(t)

	out, code, elapsed := runMigrateProcess(t, env, "migrate", "up")
	if code == 0 {
		t.Fatalf("锁被占用时 migrate up 必须失败\n%s", out)
	}
	if !strings.Contains(out, "未获取到数据库级 migration 锁") {
		t.Fatalf("失败输出必须明确说明锁冲突:\n%s", out)
	}
	if elapsed < 9*time.Second || elapsed > 30*time.Second {
		t.Fatalf("锁等待时长应约为 driver 内置 10s 超时，实际 %s", elapsed)
	}
	if tableExists(t, db, "schema_migrations") {
		t.Error("锁超时不得创建 schema_migrations")
	}
	for _, table := range businessTables {
		if tableExists(t, db, table) {
			t.Errorf("锁超时不得创建业务表 %s", table)
		}
	}

	if err := holder.Close(); err != nil {
		t.Logf("关闭持锁连接: %v", err)
	}
	if retryOut, retryCode, _ := runMigrateProcess(t, env, "migrate", "up"); retryCode != 0 {
		t.Fatalf("锁释放后重试应成功，退出码 %d\n%s", retryCode, retryOut)
	}
}

// TestMigrateProcessStrictModeRejectsLoopbackBeforeConnect 覆盖「严格校验发生在连接之前」：
// 严格模式 + 回环地址必须在连接数据库之前失败，错误信息指出环境变量且不泄漏密码。
func TestMigrateProcessStrictModeRejectsLoopbackBeforeConnect(t *testing.T) {
	const sentinel = "sentinel-t205-password"
	env := withEnv(t, map[string]string{
		"DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS": "true",
		"DATABASE_DEFAULT_HOST":                 "127.0.0.1",
		"DATABASE_DEFAULT_PASS":                 sentinel,
		// 端口不可达：若校验顺序错误，会先出现 connection refused 而不是配置错误。
		"DATABASE_DEFAULT_PORT": "1",
	})

	out, code, elapsed := runMigrateProcess(t, env, "migrate", "up")
	if code == 0 {
		t.Fatalf("严格模式 + 回环地址必须失败\n%s", out)
	}
	if !strings.Contains(out, "严格配置模式") || !strings.Contains(out, "DATABASE_DEFAULT_HOST") {
		t.Fatalf("错误信息必须是严格配置校验，而不是连接错误:\n%s", out)
	}
	if strings.Contains(out, "connection refused") || strings.Contains(out, "connect:") {
		t.Fatalf("严格校验必须先于数据库连接:\n%s", out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("配置校验应立即失败，实际耗时 %s", elapsed)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("错误信息泄露了数据库密码:\n%s", out)
	}
}

// TestMigrateProcessWrongPasswordFailsWithoutLeak 覆盖「数据库凭据错误」：
// 明确报错、非零退出，且不泄漏密码。
func TestMigrateProcessWrongPasswordFailsWithoutLeak(t *testing.T) {
	const wrong = "t205-wrong-password-sentinel"
	env := withEnv(t, map[string]string{"DATABASE_DEFAULT_PASS": wrong})

	out, code, elapsed := runMigrateProcess(t, env, "migrate", "up")
	if code == 0 {
		t.Fatalf("错误密码必须失败\n%s", out)
	}
	if strings.Contains(out, wrong) {
		t.Fatalf("错误信息泄露了数据库密码:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "access denied") {
		t.Fatalf("应输出明确的身份验证失败信息:\n%s", out)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("身份验证失败应快速返回，实际 %s", elapsed)
	}
}

// TestMigrateProcessUnreachableDatabaseFailsFast 覆盖「数据库不可达」：
// 非零退出、快速失败，不进入迁移流程。
func TestMigrateProcessUnreachableDatabaseFailsFast(t *testing.T) {
	env := withEnv(t, map[string]string{"DATABASE_DEFAULT_PORT": "1"})

	out, code, elapsed := runMigrateProcess(t, env, "migrate", "up")
	if code == 0 {
		t.Fatalf("数据库不可达时必须失败\n%s", out)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("不可达数据库应快速失败，实际耗时 %s", elapsed)
	}
	if !strings.Contains(strings.ToLower(out), "connect") && !strings.Contains(out, "打开数据库连接") {
		t.Fatalf("应输出明确的连接失败信息:\n%s", out)
	}
}

// waitForFirstTable 轮询等待目标库出现第一张表（即迁移已开始执行 DDL）。
func waitForFirstTable(t *testing.T, db *sql.DB, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&count); err != nil {
			t.Fatalf("统计表数量: %v", err)
		}
		if count > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %s 后仍未观察到任何表，迁移未开始执行", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMigrateProcessKilledMidMigrationIsRecoverable 覆盖「Job 被终止」：
// 进程在迁移过程中被 SIGKILL 后，数据库要么已完成、要么有明确可解释的失败态
// （dirty 或 DDL 报错），绝不出现「无声的不一致」；清理后重跑必须成功。
func TestMigrateProcessKilledMidMigrationIsRecoverable(t *testing.T) {
	db := setupCleanDB(t)
	env := baseChildEnv(t)

	cmd := newMigrateCommand(t, env, "migrate", "up")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 migrate 进程: %v", err)
	}
	// 等到迁移真正开始建表后再强制终止（模拟 K8s 删除 Pod / OOMKilled），
	// 这样才覆盖「DDL 已部分执行」的中间态，而不是仅仅杀掉一个还在加载配置的进程。
	waitForFirstTable(t, db, 30*time.Second)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("终止 migrate 进程: %v", err)
	}
	_ = cmd.Wait()

	retryOut, retryCode, _ := runMigrateProcess(t, env, "migrate", "up")
	if retryCode == 0 {
		// 被杀时尚未执行任何 DDL（或被杀语句已被 MySQL 原子回滚）：重跑完成即恢复。
		t.Logf("终止后重跑成功（dirty=%t，版本 %d）", dirtyState(t, db), currentVersion(t, db))
		if v := currentVersion(t, db); v != latestMigrationVersion {
			t.Errorf("重跑成功后应处于最新版本 %d，实际 %d", latestMigrationVersion, v)
		}
		return
	}
	t.Logf("终止后重跑失败（dirty=%t）:\n%s", dirtyState(t, db), retryOut)

	// 被杀时已执行了部分 DDL：必须是可解释的失败（dirty 或表已存在等明确 SQL 错误），
	// 且不能自动 force、不能静默通过。
	if dirtyState(t, db) {
		if !strings.Contains(retryOut, "dirty") {
			t.Fatalf("dirty 状态必须明确报告:\n%s", retryOut)
		}
	} else if !strings.Contains(retryOut, "migration") {
		t.Fatalf("失败原因必须与 migration 相关:\n%s", retryOut)
	}
	if v := currentVersion(t, db); v == latestMigrationVersion && dirtyState(t, db) {
		t.Fatal("版本已是最新却仍 dirty：迁移记录与执行状态矛盾")
	}

	// 人工恢复路径：清空后重新迁移必须成功（证明状态可恢复，不是不可逆损坏）。
	if err := dropAllTables(db); err != nil {
		t.Fatalf("清理表: %v", err)
	}
	recoverOut, recoverCode, _ := runMigrateProcess(t, env, "migrate", "up")
	if recoverCode != 0 {
		t.Fatalf("清理后重新迁移应成功，退出码 %d\n%s", recoverCode, recoverOut)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("恢复后期望版本 %d，实际 %d", latestMigrationVersion, v)
	}
}
