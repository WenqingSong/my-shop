//go:build serve_lifecycle

// B4.1 进程级生命周期测试的测试基建：
//   - 本地七牛 UC/bucketInfo Mock（替代外网 GetBucketInfo，真实校验代码不变）；
//   - 真实 serve 子进程的启动、健康检查、信号发送与退出码采集；
//   - 子进程日志的结构化事件解析（用于断言关闭顺序）。
//
// 这些能力只存在于带 serve_lifecycle 构建标签的测试中，不进入生产二进制。
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	qmeta "github.com/qiniu/go-sdk/v7/storagev2/apis/query_bucket_v4"
	"github.com/twmb/franz-go/pkg/kgo"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// ---------------------------------------------------------------------------
// 七牛 Mock：只替换 endpoint，不替换校验逻辑
// ---------------------------------------------------------------------------

// lifecycleQiniuMock 模拟七牛 UC 域名与 bucketInfo 接口。
//
// 真实链路（upload.checkBucketAvailable）仍由 SDK 完成：签名、HTTP 请求、
// 响应解析与错误判定全部走生产代码；Mock 只负责返回「bucket 存在」这一事实。
// 客户端使用 SDK 固定的 X-Reqid 抗劫持校验，因此响应必须带 X-Reqid。
type lifecycleQiniuMock struct {
	srv      *httptest.Server
	certPath string

	mu              sync.Mutex
	bucketInfoCalls int
	lastBucket      string
}

func newLifecycleQiniuMock(t *testing.T) *lifecycleQiniuMock {
	t.Helper()
	m := &lifecycleQiniuMock{}
	m.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v2/bucketInfo"):
			m.mu.Lock()
			m.bucketInfoCalls++
			m.lastBucket = r.URL.Query().Get("bucket")
			m.mu.Unlock()
			w.Header().Set("X-Reqid", "lifecycle-mock-reqid")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ctime":"2026-01-01T00:00:00Z","region":"z0"}`)
		case strings.Contains(r.URL.Path, "/v4/query"):
			host := m.srv.URL
			resp := qmeta.Response{Hosts: qmeta.BucketQueryHosts{{
				RegionId:   "z0",
				TimeToLive: 3600,
				UpDomains: qmeta.UpDomains{
					AcceleratedUpDomains: []string{host},
					PreferedUpDomains:    []string{host},
					AlternativeUpDomains: []string{host},
				},
				IoDomains:  qmeta.IoDomains{PreferedIoDomains: []string{host}},
				RsDomains:  qmeta.RsDomains{PreferedRsDomains: []string{host}},
				RsfDomains: qmeta.RsfDomains{PreferedRsfDomains: []string{host}},
				ApiDomains: qmeta.ApiDomains{PreferedApiDomains: []string{host}},
				S3Domains:  qmeta.S3Domains{RegionAlias: "z0", PreferedApiDomains: []string{host}},
			}}}
			b, err := json.Marshal(&resp)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Reqid", "lifecycle-mock-reqid")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)

	certPath := filepath.Join(t.TempDir(), "qiniu-mock-ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.srv.Certificate().Raw})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("写入七牛 Mock 证书: %v", err)
	}
	m.certPath = certPath
	return m
}

func (m *lifecycleQiniuMock) URL() string      { return m.srv.URL }
func (m *lifecycleQiniuMock) CertPath() string { return m.certPath }

func (m *lifecycleQiniuMock) BucketInfoCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bucketInfoCalls
}

func (m *lifecycleQiniuMock) LastBucket() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastBucket
}

// ---------------------------------------------------------------------------
// 子进程管理
// ---------------------------------------------------------------------------

type lifecycleSyncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *lifecycleSyncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *lifecycleSyncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// lifecycleProc 是真实 serve 子进程句柄。
type lifecycleProc struct {
	t       *testing.T
	cmd     *exec.Cmd
	out     *lifecycleSyncBuffer
	logPath string
	done    chan error

	signalAt time.Time
	exitAt   time.Time
	waitErr  error
}

// lifecycleRepoRoot 返回仓库根目录（go test 的工作目录是包目录）。
func lifecycleRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("未能从 %s 向上找到 go.mod", wd)
		}
		dir = parent
	}
}

// mergeEnv 覆盖同名环境变量（同名键只保留最后一次设置，避免子进程读到父进程的旧值）。
func mergeEnv(overrides map[string]string) []string {
	returned := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, override := overrides[key]; override {
			continue
		}
		returned = append(returned, kv)
	}
	for k, v := range overrides {
		returned = append(returned, k+"="+v)
	}
	return returned
}

// freeTCPPort 申请一个空闲端口（关闭监听后交给子进程使用；存在极小的竞态窗口）。
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// buildLifecycleServeBinary 构建测试专用 serve 入口（与生产 main.go 同一命令装配）。
// 允许通过 SERVE_LIFECYCLE_BIN 复用外部已构建产物。
func buildLifecycleServeBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv("SERVE_LIFECYCLE_BIN")); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("SERVE_LIFECYCLE_BIN=%s 不可用: %v", bin, err)
		}
		return bin
	}
	out := filepath.Join(t.TempDir(), "my-shop-serveproc")
	cmd := exec.Command("go", "build", "-tags", "serve_lifecycle", "-o", out, "./test/serveproc")
	cmd.Dir = repoRoot
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 serve_lifecycle 二进制失败: %v\n%s", err, b)
	}
	return out
}

// startLifecycleServeProcess 启动真实 serve 子进程，日志同时写入内存与文件。
func startLifecycleServeProcess(t *testing.T, bin string, repoRoot string, env map[string]string) *lifecycleProc {
	t.Helper()
	artifactDir := strings.TrimSpace(os.Getenv("SERVE_LIFECYCLE_ARTIFACT_DIR"))
	if artifactDir == "" {
		artifactDir = t.TempDir()
	} else if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("创建产物目录 %s: %v", artifactDir, err)
	}
	logPath := filepath.Join(artifactDir, fmt.Sprintf("serve-%d.log", time.Now().UnixNano()))
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("创建子进程日志 %s: %v", logPath, err)
	}

	buf := &lifecycleSyncBuffer{}
	cmd := exec.Command(bin, "serve")
	cmd.Dir = repoRoot
	cmd.Env = mergeEnv(env)
	cmd.Stdout = io.MultiWriter(buf, f)
	cmd.Stderr = io.MultiWriter(buf, f)
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		t.Fatalf("启动 serve 子进程: %v", err)
	}
	t.Logf("serve 子进程已启动 pid=%d 日志=%s", cmd.Process.Pid, logPath)

	p := &lifecycleProc{t: t, cmd: cmd, out: buf, logPath: logPath, done: make(chan error, 1)}
	go func() {
		p.done <- cmd.Wait()
		_ = f.Close()
	}()
	t.Cleanup(func() { p.killIfAlive() })
	return p
}

func (p *lifecycleProc) Logs() string { return p.out.String() }

func (p *lifecycleProc) Alive() bool {
	if p.cmd.Process == nil {
		return false
	}
	return p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// waitExit 在超时内等待子进程自行退出，返回 (退出码, 从 signalAt 起算的耗时)。
// 超时直接失败，并保留日志；不会用 kill -9 伪装「退出」。
func (p *lifecycleProc) waitExit(timeout time.Duration) (int, time.Duration) {
	p.t.Helper()
	var err error
	select {
	case err = <-p.done:
		p.waitErr = err
		p.exitAt = time.Now()
	case <-time.After(timeout):
		p.t.Fatalf("serve 子进程未在 %s 内退出（pid=%d），可能关闭流程阻塞；日志尾部:\n%s",
			timeout, p.cmd.Process.Pid, lifecycleTail(p.Logs(), 40))
	}
	elapsed := p.exitAt.Sub(p.signalAt)
	state := p.cmd.ProcessState
	if state == nil {
		p.t.Fatalf("子进程状态缺失（wait err=%v）", err)
	}
	code := state.ExitCode()
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		p.t.Fatalf("serve 子进程被信号 %s 终止（退出码 %d），不是优雅退出", ws.Signal(), code)
	}
	return code, elapsed
}

func (p *lifecycleProc) sendSIGTERM() {
	p.t.Helper()
	if !p.Alive() {
		p.t.Fatalf("发送 SIGTERM 前子进程已退出；日志尾部:\n%s", lifecycleTail(p.Logs(), 40))
	}
	p.signalAt = time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.t.Fatalf("向 pid=%d 发送 SIGTERM: %v", p.cmd.Process.Pid, err)
	}
}

// killIfAlive 仅用于测试失败的收尾（不使用 kill -9 代替优雅关闭的断言路径）。
func (p *lifecycleProc) killIfAlive() {
	if p.cmd.Process == nil || !p.Alive() {
		return
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

// waitHealth 轮询真实 HTTP /health 直到 200 且 code=0。
func (p *lifecycleProc) waitHealth(base string, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if !p.Alive() {
			p.t.Fatalf("serve 子进程在健康检查通过前退出；日志尾部:\n%s", lifecycleTail(p.Logs(), 60))
		}
		res, err := isoRequest(base, "GET", "/health", nil, nil)
		if err == nil && res.Status == 200 && res.Code == 0 {
			return
		}
		last = fmt.Sprintf("err=%v status=%d code=%d", err, res.Status, res.Code)
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Fatalf("/health 未在 %s 内就绪（%s）；日志尾部:\n%s", timeout, last, lifecycleTail(p.Logs(), 60))
}

// waitLogEvent 轮询子进程日志，直到出现指定结构化事件（用于等待 Kafka Runtime 装配完成）。
func (p *lifecycleProc) waitLogEvent(event string, timeout time.Duration) lifecycleLogEvent {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range lifecycleParseEvents(p.Logs()) {
			if e.Name == event {
				return e
			}
		}
		if !p.Alive() {
			p.t.Fatalf("serve 子进程在出现事件 %q 前退出；日志尾部:\n%s", event, lifecycleTail(p.Logs(), 60))
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Fatalf("未在 %s 内观察到事件 %q；日志尾部:\n%s", timeout, event, lifecycleTail(p.Logs(), 60))
	return lifecycleLogEvent{}
}

// ---------------------------------------------------------------------------
// 日志事件解析
// ---------------------------------------------------------------------------

var (
	lifecycleLogTimeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:\d{2})`)
	lifecycleEventRe   = regexp.MustCompile(`"event":"([a-z0-9_]+)"`)
)

type lifecycleLogEvent struct {
	Name string
	At   time.Time
	Idx  int
	Line string
}

// lifecycleParseEvents 抽取 glog 行内结构化 JSON 的 event 与时间戳。
func lifecycleParseEvents(log string) []lifecycleLogEvent {
	lines := strings.Split(log, "\n")
	events := make([]lifecycleLogEvent, 0, 32)
	for i, line := range lines {
		m := lifecycleEventRe.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		at := time.Time{}
		if tm := lifecycleLogTimeRe.FindStringSubmatch(line); len(tm) == 2 {
			if parsed, err := time.Parse(time.RFC3339Nano, tm[1]); err == nil {
				at = parsed
			}
		}
		events = append(events, lifecycleLogEvent{Name: m[1], At: at, Idx: i, Line: line})
	}
	return events
}

// lifecycleEventIndex 返回事件首次出现的下标，未出现返回 -1。
func lifecycleEventIndex(events []lifecycleLogEvent, name string) int {
	for i, e := range events {
		if e.Name == name {
			return i
		}
	}
	return -1
}

// lifecycleTail 返回日志尾部 n 行（失败诊断用）。
func lifecycleTail(log string, n int) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// 测试环境与数据准备
// ---------------------------------------------------------------------------

// lifecycleEnv 是子进程与断言共享的隔离环境参数。
type lifecycleEnv struct {
	DBName     string
	RedisDB    string
	Broker     string
	Topic      string
	DLQTopic   string
	Group      string
	JWTSecret  string
	AdminPass  string
	DBPass     string
	ServerPort int
	StorageDir string
	ArtifactD  string
}

const (
	// JWT 密钥必须与 flashMintUserToken 签发 token 使用的密钥一致（isoJWTSecret）。
	lifecycleAdminPass = "lifecycle-admin-password-123"
)

// lifecycleRequireEnv 读取并校验隔离测试环境参数；缺失即失败，绝不静默跳过。
func lifecycleRequireEnv(t *testing.T) lifecycleEnv {
	t.Helper()
	env := lifecycleEnv{
		DBName:    strings.TrimSpace(os.Getenv("SERVE_LIFECYCLE_DB")),
		RedisDB:   strings.TrimSpace(os.Getenv("SERVE_LIFECYCLE_REDIS_DB")),
		Broker:    strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_BROKER")),
		Topic:     strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_TOPIC")),
		Group:     strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_GROUP")),
		JWTSecret: isoJWTSecret,
		AdminPass: lifecycleAdminPass,
		DBPass:    os.Getenv("DATABASE_DEFAULT_PASS"),
		ArtifactD: os.Getenv("SERVE_LIFECYCLE_ARTIFACT_DIR"),
	}
	missing := make([]string, 0, 3)
	if env.DBName == "" {
		missing = append(missing, "SERVE_LIFECYCLE_DB")
	}
	if env.RedisDB == "" {
		missing = append(missing, "SERVE_LIFECYCLE_REDIS_DB")
	}
	if env.Broker == "" {
		missing = append(missing, "FLASH_SALE_KAFKA_TEST_BROKER")
	}
	if env.Topic == "" {
		missing = append(missing, "FLASH_SALE_KAFKA_TEST_TOPIC")
	}
	if env.Group == "" {
		missing = append(missing, "FLASH_SALE_KAFKA_TEST_GROUP")
	}
	if len(missing) > 0 {
		t.Fatalf("缺少进程级测试环境变量: %s（请通过 scripts/test-serve-lifecycle.sh 执行）", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(env.DBName, "my_shop_serve_lifecycle") {
		t.Fatalf("SERVE_LIFECYCLE_DB=%q 必须是可销毁的专用测试库（前缀 my_shop_serve_lifecycle）", env.DBName)
	}
	if env.RedisDB == "0" {
		t.Fatalf("SERVE_LIFECYCLE_REDIS_DB 不得为 0（会清空开发 Redis）")
	}
	if env.DBPass == "" {
		t.Fatalf("缺少 DATABASE_DEFAULT_PASS（测试入口必须显式注入，不允许内置默认密码）")
	}
	env.DLQTopic = env.Topic + ".dlq"
	env.ServerPort = freeTCPPort(t)
	env.StorageDir = filepath.Join(t.TempDir(), "storage")
	return env
}

// childEnv 构造子进程环境：隔离库/Redis/Kafka + 本地七牛 Mock + 独立端口。
func (e lifecycleEnv) childEnv(mock *lifecycleQiniuMock, runID string) map[string]string {
	env := map[string]string{
		"DATABASE_DEFAULT_NAME":                    e.DBName,
		"DATABASE_DEFAULT_PASS":                    e.DBPass,
		"REDIS_DEFAULT_DB":                         e.RedisDB,
		"AUTH_JWT_SECRET":                          e.JWTSecret,
		"ADMIN_SUPER_PASSWORD":                     e.AdminPass,
		"SERVER_ADDRESS":                           fmt.Sprintf("127.0.0.1:%d", e.ServerPort),
		"SERVER_GRACEFUL_TIMEOUT":                  "10",
		"STORAGE_LOCAL_ROOT":                       e.StorageDir,
		"FLASH_SALE_DISPATCH_MODE":                 "kafka",
		"FLASH_SALE_KAFKA_ENABLED":                 "true",
		"FLASH_SALE_KAFKA_BROKERS":                 e.Broker,
		"FLASH_SALE_KAFKA_TOPIC":                   e.Topic,
		"FLASH_SALE_KAFKA_DLQ_TOPIC":               e.DLQTopic,
		"FLASH_SALE_KAFKA_CONSUMER_GROUP":          e.Group,
		"FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE":    "1",
		"FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL": "150ms",
		"FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF": "200ms",
		"FLASH_SALE_SHUTDOWN_TIMEOUT":              "30s",
		"FLASH_SALE_METRICS_ENABLED":               "true",
		"QINIU_ACCESS_KEY":                         "lifecycle-test-ak-" + runID,
		"QINIU_SECRET_KEY":                         "lifecycle-test-sk-" + runID,
		"QINIU_BUCKET":                             "lifecycle-bucket-" + runID,
		"QINIU_DOMAIN":                             "cdn.lifecycle.test",
		"SERVE_LIFECYCLE_QINIU_UC_HOST":            mock.URL(),
		"SSL_CERT_FILE":                            mock.CertPath(),
	}
	if e.ArtifactD != "" {
		env["SERVE_LIFECYCLE_ARTIFACT_DIR"] = e.ArtifactD
	}
	return env
}

// lifecycleSetupIsolatedDB 在专用库上执行迁移 + Bootstrap（seed），并清空秒杀/商品域表。
// 调用前必须已把 DATABASE_DEFAULT_NAME / REDIS_DEFAULT_DB 指向隔离资源。
func lifecycleSetupIsolatedDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("隔离库执行 migration: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("隔离库 Bootstrap（seed）: %v", err)
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("清空隔离 Redis DB: %v", err)
	}
	// 按外键依赖顺序清空业务表（仅限专用测试库）。
	for _, table := range []string{
		"flash_sale_request_audits", "flash_sale_order_requests", "flash_sale_orders",
		"flash_sale_activity_skus", "flash_sale_activities",
		"order_items", "orders", "cart_items",
		"inventory_logs", "inventories",
		"skus", "product_images", "products",
		"addresses", "categories",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("清空表 %s: %v", table, err)
		}
	}
	// 清理本用例固定使用的用户区间，使测试在同一个隔离库上可重复执行
	// （脚本通常还会重建库；这里只删除测试自己的 ID 区间）。
	if _, err := g.DB().Exec(ctx, "DELETE FROM users WHERE id >= 970000 AND id < 972000"); err != nil {
		t.Fatalf("清空测试用户区间: %v", err)
	}
}

// lifecycleRequestRow 是断言需要的请求事实。
type lifecycleRequestRow struct {
	Id         int64
	Status     int
	OrderID    *int64
	Published  bool
	Partition  *int64
	Offset     *int64
	PublishErr *string
}

func lifecycleLoadRequests(t *testing.T, activityID int64) map[string]lifecycleRequestRow {
	t.Helper()
	rows, err := g.DB().GetAll(context.Background(),
		`SELECT id, idempotency_key, status, flash_order_id, published_at, published_partition, published_offset
		   FROM flash_sale_order_requests WHERE activity_id = ?`, activityID)
	if err != nil {
		t.Fatalf("查询秒杀请求: %v", err)
	}
	out := make(map[string]lifecycleRequestRow, len(rows))
	for _, r := range rows {
		row := lifecycleRequestRow{
			Id:        r["id"].Int64(),
			Status:    r["status"].Int(),
			Published: !r["published_at"].IsNil(),
		}
		if !r["flash_order_id"].IsNil() {
			v := r["flash_order_id"].Int64()
			row.OrderID = &v
		}
		if !r["published_partition"].IsNil() {
			v := r["published_partition"].Int64()
			row.Partition = &v
		}
		if !r["published_offset"].IsNil() {
			v := r["published_offset"].Int64()
			row.Offset = &v
		}
		out[r["idempotency_key"].String()] = row
	}
	return out
}

func lifecycleRequestStats(t *testing.T, activityID int64) (queued, terminal, published int) {
	t.Helper()
	ctx := context.Background()
	lifecycleCount(t, g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("activity_id", activityID).Where("status", 0), &queued, "queued 请求")
	lifecycleCount(t, g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("activity_id", activityID).WhereIn("status", []int{1, 2, 3}), &terminal, "终态请求")
	lifecycleCount(t, g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("activity_id", activityID).Where("published_at IS NOT NULL"), &published, "已发布请求")
	return
}

// lifecycleCount 把 Count() 的 (int,error) 收敛为断言友好的结果。
func lifecycleCount(t *testing.T, model *gdb.Model, out *int, label string) {
	t.Helper()
	n, err := model.Count()
	if err != nil {
		t.Fatalf("统计 %s: %v", label, err)
	}
	*out = n
}

func lifecycleCountOrders(t *testing.T, activityID int64) int {
	t.Helper()
	var n int
	lifecycleCount(t, g.DB().Model("flash_sale_orders").Ctx(context.Background()).
		Where("activity_id", activityID), &n, "秒杀订单")
	return n
}

func lifecycleCountSuccess(t *testing.T, activityID int64) int {
	t.Helper()
	var n int
	lifecycleCount(t, g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Where("activity_id", activityID).Where("status", 1), &n, "成功请求")
	return n
}

func lifecycleSold(t *testing.T, activityID, skuID int64) int64 {
	t.Helper()
	v, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Where("activity_id", activityID).Where("sku_id", skuID).Value("sold")
	if err != nil {
		t.Fatalf("查询 sold: %v", err)
	}
	return v.Int64()
}

// ---------------------------------------------------------------------------
// 秒杀请求与 Kafka 断言
// ---------------------------------------------------------------------------

// lifecycleSubmission 是一条待提交的秒杀请求（一人一单，每个用户独立）。
type lifecycleSubmission struct {
	Key    string
	UserID int64
	Token  string
}

func lifecycleSubmissionKeys(m map[string]lifecycleSubmission) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// lifecycleWaitGroup 在超时内等待 WaitGroup，避免请求协程异常时测试无限挂起。
func lifecycleWaitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// lifecycleWaitAccepted 等待受理数达到目标，超时返回错误（不阻塞到测试超时）。
func lifecycleWaitAccepted(mu *sync.Mutex, accepted map[string]lifecycleSubmission, target int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		mu.Lock()
		n := len(accepted)
		mu.Unlock()
		if n >= target {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 %s 后仅受理 %d/%d 条", timeout, n, target)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// lifecycleWaitRequestTerminal 等待单条请求完成发布并落单（进程内预热请求）。
func lifecycleWaitRequestTerminal(t *testing.T, activityID int64, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last lifecycleRequestRow
	for time.Now().Before(deadline) {
		rows := lifecycleLoadRequests(t, activityID)
		row, ok := rows[key]
		if ok {
			last = row
			if row.Status == 1 && row.Published && row.Partition != nil && row.Offset != nil {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("预热请求 %s 未在 %s 内完成：%+v", key, timeout, last)
}

// lifecycleCollectKafkaRequestIDs 从 Topic 头部消费，统计实际存在的 request_id 及出现次数。
func lifecycleCollectKafkaRequestIDs(t *testing.T, broker, topic string) map[int64]int {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(broker, ",")...),
		kgo.ConsumerGroup(fmt.Sprintf("serve-lifecycle-verify-%d", time.Now().UnixNano())),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		t.Fatalf("创建 Kafka 校验消费者: %v", err)
	}
	defer cl.Close()

	ids := make(map[int64]int)
	deadline := time.Now().Add(60 * time.Second)
	idleSince := time.Now()
	for time.Now().Before(deadline) {
		pollCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		fetches := cl.PollFetches(pollCtx)
		cancel()
		n := 0
		fetches.EachRecord(func(r *kgo.Record) {
			n++
			var ev struct {
				RequestID int64 `json:"request_id"`
			}
			if err := json.Unmarshal(r.Value, &ev); err == nil && ev.RequestID > 0 {
				ids[ev.RequestID]++
			}
		})
		if n > 0 {
			idleSince = time.Now()
		}
		if len(ids) > 0 && time.Since(idleSince) > 3*time.Second {
			break
		}
	}
	if len(ids) == 0 {
		t.Fatalf("Topic %s 中未消费到任何 flashsale 请求消息", topic)
	}
	return ids
}

// lifecycleAssertNoRedelivery 以同一消费组重新加入：位移若已提交，不应再收到消息。
func lifecycleAssertNoRedelivery(t *testing.T, broker, topic, group string) {
	t.Helper()
	assigned := make(chan struct{}, 1)
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(broker, ",")...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.OnPartitionsAssigned(func(context.Context, *kgo.Client, map[string][]int32) {
			select {
			case assigned <- struct{}{}:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("创建位移校验消费者: %v", err)
	}
	defer cl.Close()

	joinCtx, cancelJoin := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelJoin()
	select {
	case <-assigned:
	case <-joinCtx.Done():
		t.Fatalf("位移校验消费者未在 15s 内加入消费组 %s", group)
	}

	pollCtx, cancelPoll := context.WithTimeout(context.Background(), 3*time.Second)
	fetches := cl.PollFetches(pollCtx)
	cancelPoll()
	if err := fetches.Err(); err != nil && pollCtx.Err() == nil {
		t.Fatalf("位移校验拉取失败: %v", err)
	}
	if n := fetches.NumRecords(); n != 0 {
		t.Fatalf("位移未提交：同组新消费者重新收到 %d 条消息", n)
	}
}

// lifecycleEventFields 解析结构化日志行中的 JSON 字段（glog 前缀 + {trace} + JSON）。
func lifecycleEventFields(t *testing.T, line string) map[string]any {
	t.Helper()
	idx := strings.Index(line, `{"`)
	if idx < 0 {
		t.Fatalf("日志行中没有结构化 JSON: %s", line)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(line[idx:]), &fields); err != nil {
		t.Fatalf("解析结构化日志失败: %v（%s）", err, line)
	}
	return fields
}

// lifecycleDuplicateOrderKeys 返回重复成功订单的 (user_id,idempotency_key)。
func lifecycleDuplicateOrderKeys(t *testing.T, activityID int64) []string {
	t.Helper()
	rows, err := g.DB().GetAll(context.Background(),
		`SELECT user_id, idempotency_key, COUNT(*) AS c
		   FROM flash_sale_orders WHERE activity_id = ?
		  GROUP BY user_id, idempotency_key HAVING c > 1`, activityID)
	if err != nil {
		t.Fatalf("检查重复订单: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("user=%d key=%s count=%d",
			r["user_id"].Int64(), r["idempotency_key"].String(), r["c"].Int()))
	}
	return out
}
