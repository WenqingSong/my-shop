//go:build serve_lifecycle

// T2-03.1 进程级验收：HTTP Drain 超时 → 强制关闭 → Runtime Shutdown 顺序控制。
//
// 真实 serve 子进程 + 真实 HTTP 请求 + 真实 SIGTERM：
//   - 测试构建注入一个阻塞 30s 且忽略请求取消的 handler（生产二进制不存在该路由）；
//   - SIGTERM 后验证 10s drain 预算生效（9s 优雅窗口 + 1s 强制关闭窗口）；
//   - 验证超时后 listener 与连接被强制关闭、客户端请求以错误返回；
//   - 验证 http_server_stopped 仅在强制关闭并确认 Server 停止后输出；
//   - 验证 Runtime Shutdown 在 Server 停止后才开始，进程在 60s 预算内以退出码 0 结束。
package e2e

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/termination"
)

const (
	// drainTimeoutBudget 是冻结契约中的 HTTP drain 总预算。
	drainTimeoutBudget = termination.DefaultHTTPDrainTimeout
	// drainForceWindow 是预算内预留给强制关闭与停止确认的窗口。
	drainForceWindow = time.Second
	// drainMaxExitWait 证明进程没有等待阻塞 30s 的 handler。
	drainMaxExitWait = 15 * time.Second
)

func TestServeHTTPDrainTimeoutForceClose(t *testing.T) {
	env := lifecycleRequireEnv(t)
	repoRoot := lifecycleRepoRoot(t)
	bin := buildLifecycleServeBinary(t, repoRoot)
	mock := newLifecycleQiniuMock(t)
	runID := fmt.Sprintf("%d", time.Now().UnixNano())

	t.Setenv("DATABASE_DEFAULT_NAME", env.DBName)
	t.Setenv("DATABASE_DEFAULT_PASS", env.DBPass)
	t.Setenv("REDIS_DEFAULT_DB", env.RedisDB)
	t.Setenv("AUTH_JWT_SECRET", env.JWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", env.AdminPass)
	t.Setenv("STORAGE_LOCAL_ROOT", env.StorageDir)

	lifecycleSetupIsolatedDB(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", env.ServerPort)
	p := startLifecycleServeProcess(t, bin, repoRoot, env.childEnv(mock, runID))
	p.waitHealth(base, 90*time.Second)
	budgetEvent := p.waitLogEvent("termination_budget", 30*time.Second)
	budgetFields := lifecycleEventFields(t, budgetEvent.Line)
	if got := fmt.Sprint(budgetFields["http_drain_timeout"]); got != "10s" {
		t.Fatalf("启动预算日志 http_drain_timeout = %q，期望 10s", got)
	}

	// ---------- 发起真实阻塞请求（handler 忽略取消，阻塞 30s） ----------
	type clientOutcome struct {
		err error
		at  time.Time
	}
	outcome := make(chan clientOutcome, 1)
	client := &http.Client{Timeout: 40 * time.Second}
	go func() {
		resp, err := client.Get(base + "/__lifecycle/block")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			outcome <- clientOutcome{
				err: fmt.Errorf("阻塞 handler 在 drain 超时后仍返回响应 status=%d body=%q", resp.StatusCode, string(body)),
				at:  time.Now(),
			}
			return
		}
		outcome <- clientOutcome{err: err, at: time.Now()}
	}()
	p.waitLogEvent("lifecycle_block_handler_entered", 20*time.Second)

	// ---------- 真实 SIGTERM，验证 10s drain 预算 ----------
	p.sendSIGTERM()
	signalAt := p.signalAt

	var got clientOutcome
	select {
	case got = <-outcome:
	case <-time.After(20 * time.Second):
		t.Fatalf("阻塞请求未在 20s 内因强制关闭返回；日志尾部:\n%s", lifecycleTail(p.Logs(), 60))
	}
	if got.err == nil {
		t.Fatal("阻塞请求不应在强制关闭后成功返回")
	}
	requestElapsed := got.at.Sub(signalAt)
	if requestElapsed < drainTimeoutBudget-drainForceWindow {
		t.Fatalf("连接在 %s 被关闭，早于优雅 drain 窗口（%s）", requestElapsed, drainTimeoutBudget-drainForceWindow)
	}
	if requestElapsed > drainTimeoutBudget+drainForceWindow {
		t.Fatalf("连接在 %s 才被关闭，强制关闭未在 drain 预算内生效", requestElapsed)
	}
	t.Logf("PASS 强制关闭：阻塞请求在 SIGTERM 后 %s 以错误返回（%v）", requestElapsed.Round(time.Millisecond), got.err)

	exitCode, exitElapsed := p.waitExit(termination.DefaultTerminationGracePeriod)
	if exitCode != 0 {
		t.Fatalf("serve 进程退出码=%d（期望 0）；日志尾部:\n%s", exitCode, lifecycleTail(p.Logs(), 60))
	}
	if exitElapsed > drainMaxExitWait {
		t.Fatalf("进程退出耗时 %s，说明关闭流程等待了阻塞 handler，未受 drain 预算约束", exitElapsed)
	}
	lifecycleAssertNoResidualBinary(t, bin)
	t.Logf("PASS 进程退出：退出码 0，SIGTERM→退出耗时 %s（未等待 30s handler）", exitElapsed.Round(time.Millisecond))

	// ---------- 超时路径事件与顺序 ----------
	logs := p.Logs()
	events := lifecycleParseEvents(logs)
	order := []string{
		"lifecycle_block_handler_entered",
		"http_server_drain_begin",
		"http_server_drain_timeout",
		"http_server_force_closed",
		"http_server_stopped",
		"flashsale_runtime_shutdown_begin",
		"flashsale_publisher_loop_exited",
		"flashsale_publisher_stopped",
		"flashsale_consumer_supervisor_exited",
		"flashsale_consumer_stopped",
		"flashsale_producer_closed",
		"flashsale_scanners_stopped",
		"flashsale_runtime_shutdown_complete",
	}
	lastIdx := -1
	index := map[string]int{}
	for _, name := range order {
		idx := lifecycleEventIndex(events, name)
		if idx < 0 {
			t.Fatalf("关闭日志缺少事件 %q；日志尾部:\n%s", name, lifecycleTail(logs, 80))
		}
		if idx <= lastIdx {
			t.Fatalf("关闭事件顺序错误：%q 未发生在上一事件之后", name)
		}
		lastIdx = idx
		index[name] = idx
	}

	timeoutFields := lifecycleEventFields(t, events[index["http_server_drain_timeout"]].Line)
	if ms, ok := timeoutFields["elapsed_ms"].(float64); ok {
		elapsed := time.Duration(ms) * time.Millisecond
		if elapsed < drainTimeoutBudget-drainForceWindow-time.Second || elapsed > drainTimeoutBudget {
			t.Fatalf("drain 超时发生在 %s，未落在优雅窗口末尾（预算 %s）", elapsed, drainTimeoutBudget)
		}
	} else {
		t.Fatalf("http_server_drain_timeout 缺少 elapsed_ms: %s", events[index["http_server_drain_timeout"]].Line)
	}

	forceFields := lifecycleEventFields(t, events[index["http_server_force_closed"]].Line)
	forced, ok := forceFields["forced_connections"].(float64)
	if !ok || forced < 1 {
		t.Fatalf("强制关闭未记录任何连接: %s", events[index["http_server_force_closed"]].Line)
	}
	if active, ok := forceFields["active_connections"].(float64); !ok || active != 0 {
		t.Fatalf("强制关闭后仍有活跃连接: %s", events[index["http_server_force_closed"]].Line)
	}

	stoppedLine := events[index["http_server_stopped"]].Line
	stoppedFields := lifecycleEventFields(t, stoppedLine)
	if timedOut, ok := stoppedFields["timed_out"].(bool); !ok || !timedOut {
		t.Fatalf("超时路径 http_server_stopped 必须标记 timed_out=true: %s", stoppedLine)
	}
	if ms, ok := stoppedFields["elapsed_ms"].(float64); !ok || time.Duration(ms)*time.Millisecond > drainTimeoutBudget {
		t.Fatalf("HTTP drain 总耗时超出预算 %s: %s", drainTimeoutBudget, stoppedLine)
	}

	completeLine := events[index["flashsale_runtime_shutdown_complete"]].Line
	if !strings.Contains(completeLine, `"within_budget":true`) || !strings.Contains(completeLine, `"errors":0`) {
		t.Fatalf("Runtime Shutdown 未在预算内成功完成: %s", completeLine)
	}
	t.Logf("PASS 事件顺序：drain_begin → drain_timeout → force_closed → stopped(timed_out=true) → runtime_shutdown → complete")
	t.Logf("超时路径证据：%s", stoppedLine)
}
