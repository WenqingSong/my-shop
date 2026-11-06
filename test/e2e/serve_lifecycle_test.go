//go:build serve_lifecycle

// B4.1 进程级验收：
//
//	真实 serve 子进程（Kafka 模式）→ 真实 HTTP 秒杀请求 → 操作系统 SIGTERM
//	→ 有序关闭 → 进程自行退出 → 同配置重启 → 请求收敛与数据一致性核对
//
// 与 B4 的进程内 E2E 的差别：这里验证的是操作系统信号、真实进程退出码、
// 真实 serve 启动装配（含七牛启动校验、Kafka Runtime 装配、HTTP 监听）。
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/termination"
)

const (
	// 预热请求用于确认 Publisher/Consumer 在 SIGTERM 前已真实运行。
	lifecycleWarmupUserID = int64(970000)
	// 突发请求的独立用户数与目标受理数（秒杀一人一单，必须一人一请求）。
	lifecycleBurstUsers     = 60
	lifecycleTargetAccepted = 36
	// 进程退出上限 = HTTP drain(10s) + runtime shutdown(30s) + 安全余量(15s)。
	// preStop 由 Kubernetes 执行，本测试直接发送 SIGTERM，因此不包含 pre_stop_delay。
	lifecycleMaxExitWait = termination.DefaultHTTPDrainTimeout +
		termination.DefaultRuntimeShutdownTimeout + termination.DefaultSafetyMargin
	// flash_sale.shutdown_timeout 的验收上限。
	lifecycleShutdownBudget = termination.DefaultRuntimeShutdownTimeout
	// HTTP drain 单段预算上限。
	lifecycleDrainBudget = termination.DefaultHTTPDrainTimeout
)

func TestServeProcessLifecycle(t *testing.T) {
	env := lifecycleRequireEnv(t)
	repoRoot := lifecycleRepoRoot(t)
	bin := buildLifecycleServeBinary(t, repoRoot)
	mock := newLifecycleQiniuMock(t)
	runID := fmt.Sprintf("%d", time.Now().UnixNano())

	// 父进程只用于准备数据与断言；它必须与子进程指向同一隔离库/Redis DB。
	t.Setenv("DATABASE_DEFAULT_NAME", env.DBName)
	t.Setenv("DATABASE_DEFAULT_PASS", env.DBPass)
	t.Setenv("REDIS_DEFAULT_DB", env.RedisDB)
	t.Setenv("AUTH_JWT_SECRET", env.JWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", env.AdminPass)
	t.Setenv("STORAGE_LOCAL_ROOT", env.StorageDir)

	t.Logf("隔离资源: MySQL=%s RedisDB=%s Kafka broker=%s topic=%s dlq=%s group=%s",
		env.DBName, env.RedisDB, env.Broker, env.Topic, env.DLQTopic, env.Group)

	lifecycleSetupIsolatedDB(t)

	// 数据准备：1 条预热 + 60 条突发请求，库存 61 保证不会因库存不足被拒。
	totalStock := int64(1 + lifecycleBurstUsers)
	skuID := flashSetupSku(t, "b41-serve-lifecycle", 5000, 1)
	now := flashMySQLNow(t)
	activityID := flashInsertActivity(t, "b41-serve-lifecycle", 1, now.Add(-time.Hour), now.Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, totalStock)
	flashSyncCache(t, activityID)

	warmup := lifecycleSubmission{Key: "b41-warmup", UserID: lifecycleWarmupUserID, Token: flashMintUserToken(t, lifecycleWarmupUserID)}
	burst := make([]lifecycleSubmission, 0, lifecycleBurstUsers)
	for i := 0; i < lifecycleBurstUsers; i++ {
		userID := int64(971000 + i)
		burst = append(burst, lifecycleSubmission{
			Key:    fmt.Sprintf("b41-burst-%03d", i),
			UserID: userID,
			Token:  flashMintUserToken(t, userID),
		})
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", env.ServerPort)
	submit := func(s lifecycleSubmission) (isoResult, error) {
		return isoRequest(base, "POST", fmt.Sprintf("/flash-sales/%d/orders", activityID), map[string]any{
			"sku_id": skuID, "idempotency_key": s.Key,
		}, isoAuthHeader(s.Token))
	}

	// ---------- 阶段 1：真实 serve 启动 ----------
	p1 := startLifecycleServeProcess(t, bin, repoRoot, env.childEnv(mock, runID))
	p1.waitHealth(base, 90*time.Second)
	started := p1.waitLogEvent("flashsale_runtime_started", 30*time.Second)
	if !strings.Contains(started.Line, `"mode":"kafka"`) {
		t.Fatalf("Kafka Runtime 未按 kafka 模式装配: %s", started.Line)
	}
	// ---------- 启动日志：终止预算契约 ----------
	budgetEvent := p1.waitLogEvent("termination_budget", 30*time.Second)
	budgetFields := lifecycleEventFields(t, budgetEvent.Line)
	frozenBudget := map[string]string{
		"pre_stop_delay":              "5s",
		"http_drain_timeout":          "10s",
		"runtime_shutdown_timeout":    "30s",
		"required_termination_budget": "45s",
		"termination_grace_period":    "60s",
		"safety_margin":               "15s",
	}
	for key, want := range frozenBudget {
		if got := fmt.Sprint(budgetFields[key]); got != want {
			t.Fatalf("终止预算日志 %s = %q，期望 %q；日志: %s", key, got, want, budgetEvent.Line)
		}
	}
	t.Logf("PASS 终止预算启动日志：termination_grace_period=60s，必需预算=45s，安全余量=15s（preStop 5s + drain 10s + runtime 30s）")
	if mock.BucketInfoCalls() == 0 {
		t.Fatalf("serve 启动未执行七牛 bucket 可用性校验（Mock 未收到 /v2/bucketInfo）")
	}
	if got := mock.LastBucket(); got == "" || !strings.Contains(got, "lifecycle-bucket-") {
		t.Fatalf("七牛校验的 bucket 与配置不一致: %q", got)
	}
	t.Logf("PASS 真实 serve 启动：HTTP /health 可用，Kafka Runtime mode=kafka，七牛真实校验请求 %d 次（bucket=%s）",
		mock.BucketInfoCalls(), mock.LastBucket())

	// ---------- 预热：证明 Publisher + Consumer 已真实工作 ----------
	warm, warmErr := submit(warmup)
	if warmErr != nil || warm.Status != 200 || warm.Code != 0 {
		t.Fatalf("预热秒杀请求未被受理: err=%v status=%d code=%d msg=%q", warmErr, warm.Status, warm.Code, warm.Message)
	}
	lifecycleWaitRequestTerminal(t, activityID, warmup.Key, 60*time.Second)
	warmRow := lifecycleLoadRequests(t, activityID)[warmup.Key]
	if warmRow.Status != 1 || !warmRow.Published {
		t.Fatalf("预热请求未完成发布与落单: %+v", warmRow)
	}
	t.Logf("PASS Kafka 链路预热：warmup request_id=%d 已发布并落单", warmRow.Id)

	// ---------- 在途流量 + 进程级 SIGTERM ----------
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu       sync.Mutex
		accepted = make(map[string]lifecycleSubmission, lifecycleBurstUsers)
		rejected = make(map[string]string, lifecycleBurstUsers)
		wg       sync.WaitGroup
	)
	jobs := make(chan lifecycleSubmission)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				res, err := submit(s)
				mu.Lock()
				if err == nil && res.Status == 200 && res.Code == 0 {
					accepted[s.Key] = s
				} else {
					rejected[s.Key] = fmt.Sprintf("err=%v status=%d code=%d", err, res.Status, res.Code)
				}
				mu.Unlock()
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, s := range burst {
			select {
			case <-ctx.Done():
				return
			case jobs <- s:
			}
		}
	}()

	if err := lifecycleWaitAccepted(&mu, accepted, lifecycleTargetAccepted, 30*time.Second); err != nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("突发请求未达到目标受理数: %v（accepted=%d rejected=%d）", err, len(accepted), len(rejected))
	}
	queued, terminal, published := lifecycleRequestStats(t, activityID)
	if queued == 0 || published >= queued+terminal {
		t.Fatalf("SIGTERM 前未形成在途积压: queued=%d terminal=%d published=%d", queued, terminal, published)
	}
	mu.Lock()
	acceptedAtSignal := len(accepted)
	mu.Unlock()
	t.Logf("SIGTERM 前状态：HTTP 已受理 %d 条，queued=%d terminal=%d published=%d", acceptedAtSignal, queued, terminal, published)

	p1.sendSIGTERM()
	// 保留少量在途提交：SIGTERM 已送达时可能仍有 HTTP 请求正在处理。
	time.Sleep(200 * time.Millisecond)
	cancel()
	if !lifecycleWaitGroup(&wg, 10*time.Second) {
		t.Fatalf("HTTP 提交协程未在 10s 内结束")
	}

	exitCode, elapsed := p1.waitExit(lifecycleMaxExitWait)
	if exitCode != 0 {
		t.Fatalf("serve 进程退出码=%d（期望 0）；日志尾部:\n%s", exitCode, lifecycleTail(p1.Logs(), 60))
	}
	if elapsed > lifecycleMaxExitWait {
		t.Fatalf("进程退出耗时 %s 超出上限 %s", elapsed, lifecycleMaxExitWait)
	}
	// 无残留：进程必须真正退出，且不得留下同二进制的子进程。
	if p1.Alive() {
		t.Fatalf("serve 进程退出后仍存活（pid=%d）", p1.cmd.Process.Pid)
	}
	lifecycleAssertNoResidualBinary(t, bin)
	mu.Lock()
	httpAcceptedCount := len(accepted)
	mu.Unlock()

	// 关闭过程证据：SIGTERM → HTTP drain（有界）→ Publisher → Consumer → Producer → Scanner。
	logs := p1.Logs()
	events := lifecycleParseEvents(logs)
	drainIdx := lifecycleEventIndex(events, "http_server_drain_begin")
	if drainIdx < 0 {
		t.Fatalf("日志中缺少操作系统信号接收证据 http_server_drain_begin；日志尾部:\n%s", lifecycleTail(logs, 60))
	}
	if !strings.Contains(events[drainIdx].Line, `"signal":"terminated"`) {
		t.Fatalf("HTTP drain 未记录 SIGTERM 信号: %s", events[drainIdx].Line)
	}
	order := []string{
		"http_server_drain_begin",
		"http_server_stopped",
		"flashsale_runtime_shutdown_begin",
		"flashsale_publisher_stopped",
		"flashsale_consumer_stopped",
		"flashsale_producer_closed",
		"flashsale_scanners_stopped",
		"flashsale_runtime_shutdown_complete",
	}
	lastIdx := -1
	for _, name := range order {
		idx := lifecycleEventIndex(events, name)
		if idx < 0 {
			t.Fatalf("关闭日志缺少事件 %q；日志尾部:\n%s", name, lifecycleTail(logs, 80))
		}
		if idx <= lastIdx {
			t.Fatalf("关闭事件顺序错误：%q 未发生在上一事件之后", name)
		}
		lastIdx = idx
	}
	httpIdx := lifecycleEventIndex(events, "http_server_stopped")
	// HTTP drain 必须在冻结预算内结束（有界 drain），而不是无限等待在途请求。
	httpStoppedFields := lifecycleEventFields(t, events[httpIdx].Line)
	if timedOut, ok := httpStoppedFields["timed_out"].(bool); !ok || timedOut {
		t.Fatalf("HTTP drain 未在预算内完成: %s", events[httpIdx].Line)
	}
	if ms, ok := httpStoppedFields["elapsed_ms"].(float64); ok && time.Duration(ms)*time.Millisecond > lifecycleDrainBudget {
		t.Fatalf("HTTP drain 耗时 %dms 超出预算 %s", int64(ms), lifecycleDrainBudget)
	}
	if beginIdx := lifecycleEventIndex(events, "flashsale_runtime_shutdown_begin"); beginIdx < httpIdx {
		t.Fatalf("runtime 关闭早于 HTTP Server 退出（http=%d begin=%d）", httpIdx, beginIdx)
	}
	// 关键回归：Publisher/Consumer 的实际退出必须晚于 HTTP drain。
	// 若把 signal.NotifyContext 的 ctx 作为 runtime 父 ctx，两者会在 SIGTERM 瞬间被并发取消，
	// 这两个事件就会早于 http_server_stopped —— 正是 B4.1 修复的缺陷。
	for _, name := range []string{"flashsale_publisher_loop_exited", "flashsale_consumer_supervisor_exited"} {
		idx := lifecycleEventIndex(events, name)
		if idx < 0 {
			t.Fatalf("缺少组件退出事件 %q；日志尾部:\n%s", name, lifecycleTail(logs, 80))
		}
		if idx < httpIdx {
			t.Fatalf("组件 %q 在 HTTP drain 前被取消（组件=%d http=%d），关闭顺序未生效", name, idx, httpIdx)
		}
	}
	if exitIdx, stopIdx := lifecycleEventIndex(events, "flashsale_publisher_loop_exited"),
		lifecycleEventIndex(events, "flashsale_publisher_stopped"); exitIdx > stopIdx {
		t.Fatalf("Publisher 退出日志顺序异常（exit=%d stopped=%d）", exitIdx, stopIdx)
	}
	if exitIdx, stopIdx := lifecycleEventIndex(events, "flashsale_consumer_supervisor_exited"),
		lifecycleEventIndex(events, "flashsale_consumer_stopped"); exitIdx > stopIdx {
		t.Fatalf("Consumer 退出日志顺序异常（exit=%d stopped=%d）", exitIdx, stopIdx)
	}
	completeLine := events[lifecycleEventIndex(events, "flashsale_runtime_shutdown_complete")].Line
	if !strings.Contains(completeLine, `"within_budget":true`) || !strings.Contains(completeLine, `"errors":0`) {
		t.Fatalf("关闭未在统一预算内成功完成: %s", completeLine)
	}
	if ms, ok := lifecycleEventFields(t, completeLine)["elapsed_ms"].(float64); ok {
		if time.Duration(ms)*time.Millisecond > lifecycleShutdownBudget {
			t.Fatalf("runtime 关闭耗时 %dms 超出 shutdown_timeout %s", int64(ms), lifecycleShutdownBudget)
		}
	}
	for _, e := range events {
		if (e.Name == "flashsale_publisher_stopped" || e.Name == "flashsale_consumer_stopped" || e.Name == "flashsale_scanners_stopped") &&
			strings.Contains(e.Line, `"timed_out":true`) {
			t.Fatalf("组件关闭超时: %s", e.Line)
		}
	}
	t.Logf("PASS 进程级 SIGTERM：pid=%d 在 %s 内以退出码 0 自行退出；关闭顺序 %s",
		p1.cmd.Process.Pid, elapsed.Round(time.Millisecond), strings.Join(order, " → "))

	postQueued, postTerminal, postPublished := lifecycleRequestStats(t, activityID)
	t.Logf("进程退出时数据快照：http_accepted=%d queued=%d terminal=%d published=%d", httpAcceptedCount, postQueued, postTerminal, postPublished)
	if postQueued == 0 || postPublished >= postQueued+postTerminal {
		t.Fatalf("退出时不存在「已受理但未完成」的请求，未能验证在途关闭: queued=%d terminal=%d published=%d",
			postQueued, postTerminal, postPublished)
	}

	// 以 MySQL 请求行为「服务端已受理」的权威集合：
	// SIGTERM 期间客户端可能收不到响应，但请求已提交，最终仍必须收敛。
	rowsAfterExit := lifecycleLoadRequests(t, activityID)
	for key := range accepted {
		if _, ok := rowsAfterExit[key]; !ok {
			t.Fatalf("HTTP 已受理但 MySQL 缺少请求行: %s", key)
		}
	}
	serverAccepted := make([]string, 0, len(burst))
	for _, s := range burst {
		if _, ok := rowsAfterExit[s.Key]; ok {
			serverAccepted = append(serverAccepted, s.Key)
		}
	}
	if len(serverAccepted) < lifecycleTargetAccepted {
		t.Fatalf("服务端实际受理 %d 条，低于目标 %d", len(serverAccepted), lifecycleTargetAccepted)
	}
	t.Logf("受理口径核对：HTTP 200 = %d 条，服务端已提交 = %d 条（差额含 SIGTERM 期间响应未送达的请求）",
		httpAcceptedCount, len(serverAccepted))

	// ---------- 阶段 2：同配置重启并等待收敛 ----------
	p2 := startLifecycleServeProcess(t, bin, repoRoot, env.childEnv(mock, runID))
	p2.waitHealth(base, 90*time.Second)
	p2.waitLogEvent("flashsale_runtime_started", 30*time.Second)
	if mock.BucketInfoCalls() < 2 {
		t.Fatalf("重启未再次执行七牛真实校验（Mock 调用 %d 次）", mock.BucketInfoCalls())
	}
	t.Logf("PASS 同配置重启：HTTP /health 可用，Kafka Runtime 重新装配（七牛校验累计 %d 次）", mock.BucketInfoCalls())

	mustConverge := append([]string{warmup.Key}, serverAccepted...)
	convergeStarted := time.Now()
	deadline := convergeStarted.Add(180 * time.Second)
	for {
		rows := lifecycleLoadRequests(t, activityID)
		notConverged := make([]string, 0, 4)
		for _, key := range mustConverge {
			row, ok := rows[key]
			if !ok {
				notConverged = append(notConverged, key+"(missing)")
				continue
			}
			if row.Status == 0 || !row.Published || row.Partition == nil || row.Offset == nil {
				notConverged = append(notConverged, fmt.Sprintf("%s(status=%d published=%t)", key, row.Status, row.Published))
			}
		}
		if len(notConverged) == 0 {
			break
		}
		if time.Now().After(deadline) {
			q, term, pub := lifecycleRequestStats(t, activityID)
			t.Fatalf("重启后请求未收敛：queued=%d terminal=%d published=%d，未收敛样例=%v；日志尾部:\n%s",
				q, term, pub, notConverged[:min(len(notConverged), 5)], lifecycleTail(p2.Logs(), 60))
		}
		time.Sleep(250 * time.Millisecond)
	}
	// 若关闭时 Publisher 正好处于发布中，已领取记录会保留最长 60s 租约，
	// 重启后需等租约到期才可重新发布；耗时包含这一段恢复窗口。
	t.Logf("PASS 重启收敛：%d 条服务端已受理请求全部到达终态并写入发布事实（耗时 %s）",
		len(serverAccepted), time.Since(convergeStarted).Round(time.Millisecond))

	// ---------- 数据一致性核对 ----------
	rows := lifecycleLoadRequests(t, activityID)
	for _, key := range mustConverge {
		row := rows[key]
		switch row.Status {
		case 1:
			if row.OrderID == nil {
				t.Fatalf("请求 %s 成功但缺少 flash_order_id", key)
			}
		case 2, 3:
			t.Fatalf("请求 %s 进入业务/技术失败终态（stock=%d）：status=%d", key, totalStock, row.Status)
		default:
			t.Fatalf("请求 %s 未进入终态: status=%d", key, row.Status)
		}
	}
	orders := lifecycleCountOrders(t, activityID)
	successRows := lifecycleCountSuccess(t, activityID)
	sold := lifecycleSold(t, activityID, skuID)
	if duplicates := lifecycleDuplicateOrderKeys(t, activityID); len(duplicates) > 0 {
		t.Fatalf("存在重复订单: %v", duplicates)
	}
	if orders != successRows {
		t.Fatalf("订单数 %d 与成功请求数 %d 不一致", orders, successRows)
	}
	if sold != int64(successRows) {
		t.Fatalf("sold=%d 与实际成功订单数 %d 不一致", sold, successRows)
	}
	if sold > totalStock {
		t.Fatalf("库存超卖：sold=%d total_stock=%d", sold, totalStock)
	}
	if successRows != 1+len(serverAccepted) {
		t.Fatalf("成功订单数 %d != 预热 1 + 服务端受理 %d；可能存在漏单", successRows, len(serverAccepted))
	}

	// Kafka 中必须真的存在每个已受理请求的消息（不只是 MySQL 标记）。
	kafkaIDs := lifecycleCollectKafkaRequestIDs(t, env.Broker, env.Topic)
	for _, key := range mustConverge {
		row := rows[key]
		if kafkaIDs[row.Id] == 0 {
			t.Fatalf("请求 %s（id=%d）在 Kafka Topic %s 中没有消息", key, row.Id, env.Topic)
		}
	}
	t.Logf("PASS 数据一致性：http_accepted=%d server_accepted=%d success=%d orders=%d sold=%d total_stock=%d duplicate_orders=0 kafka_request_ids=%d",
		httpAcceptedCount, len(serverAccepted), successRows, orders, sold, totalStock, len(kafkaIDs))

	// ---------- 阶段 2 关闭 + Offset 已提交验证 ----------
	// 收敛判定基于 MySQL 终态，而 Offset 提交紧随处理之后：留出短暂稳定窗口，
	// 避免把「业务已落库、位移尚未提交」的 at-least-once 窗口误判为未提交。
	time.Sleep(time.Second)
	p2.sendSIGTERM()
	if code, elapsed2 := p2.waitExit(lifecycleMaxExitWait); code != 0 {
		t.Fatalf("重启进程退出码=%d（期望 0），耗时 %s", code, elapsed2)
	}
	lifecycleAssertNoResidualBinary(t, bin)
	lifecycleAssertNoRedelivery(t, env.Broker, env.Topic, env.Group)
	t.Logf("PASS 消费位移已提交：同组新消费者未重新收到任何消息")

	t.Logf("B4.1 进程级验收通过：serve 启动=%s，SIGTERM 退出耗时=%s，重启收敛完成",
		started.At.Format(time.RFC3339Nano), elapsed.Round(time.Millisecond))
}

// TestServeKafkaOutageKeepsHTTPAvailable 是 T2-02-D 的进程级验收：
//
//	真实 serve 子进程（Kafka 模式）→ 停止真实 Kafka Broker
//	→ 子进程不退出、/health 与 /ready 仍为 200、consumer_joined 归 0
//	→ HTTP 仍受理请求并写入 MySQL Outbox（积压指标反映真实数量）
//	→ 恢复 Kafka → Consumer 重新加入、积压收敛、请求进入正确终态且不重复。
//
// 为什么必须用真实子进程而不是进程内测试：本用例要证明的是「Kafka 故障不会让进程退出」，
// 以及健康检查/指标在真实 serve 装配下（含 Qiniu 启动校验、终止预算、Runtime 装配）成立。
//
// 该用例放在文件末尾：它会在运行期 stop/start Kafka 容器，必须在其他进程级用例之后执行。
func TestServeKafkaOutageKeepsHTTPAvailable(t *testing.T) {
	env := lifecycleRequireEnv(t)
	repoRoot := lifecycleRepoRoot(t)
	bin := buildLifecycleServeBinary(t, repoRoot)
	mock := newLifecycleQiniuMock(t)
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	kafkaContainer := strings.TrimSpace(os.Getenv("B4_E2E_KAFKA_CONTAINER"))
	if kafkaContainer == "" {
		kafkaContainer = "my-shop-kafka"
	}

	// 父进程只用于准备数据与断言；必须与子进程指向同一隔离库/Redis DB。
	t.Setenv("DATABASE_DEFAULT_NAME", env.DBName)
	t.Setenv("DATABASE_DEFAULT_PASS", env.DBPass)
	t.Setenv("REDIS_DEFAULT_DB", env.RedisDB)
	t.Setenv("AUTH_JWT_SECRET", env.JWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", env.AdminPass)
	t.Setenv("STORAGE_LOCAL_ROOT", env.StorageDir)

	t.Logf("隔离资源: MySQL=%s RedisDB=%s broker=%s topic=%s group=%s kafka_container=%s",
		env.DBName, env.RedisDB, env.Broker, env.Topic, env.Group, kafkaContainer)

	lifecycleSetupIsolatedDB(t)

	// 库存 5：只提交 1 条请求，避免库存因素干扰「故障期间受理」的判定。
	skuID := flashSetupSku(t, "t202-broker-outage", 5000, 1)
	now := flashMySQLNow(t)
	activityID := flashInsertActivity(t, "t202-broker-outage", 1, now.Add(-time.Hour), now.Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 5)
	flashSyncCache(t, activityID)

	base := fmt.Sprintf("http://127.0.0.1:%d", env.ServerPort)
	p := startLifecycleServeProcess(t, bin, repoRoot, env.childEnv(mock, runID))

	// 若断言失败，尽最大努力把 Kafka 拉回来，避免后续用例受污染。
	restoreKafka := func() { _ = exec.Command("docker", "start", kafkaContainer).Run() }
	t.Cleanup(restoreKafka)

	// ---------- 阶段 1：正常运行 ----------
	p.waitHealth(base, 90*time.Second)
	started := p.waitLogEvent("flashsale_runtime_started", 30*time.Second)
	if !strings.Contains(started.Line, `"mode":"kafka"`) {
		t.Fatalf("Runtime 未按 kafka 模式装配: %s", started.Line)
	}
	assertHealthAndReady(t, base, 200, 200)
	waitMetricValue(t, base, metricConsumerJoined, 1, 120*time.Second)
	waitMetricValue(t, base, metricOutboxBacklogPending, 0, 60*time.Second)
	joinedEvent := p.waitLogEvent("flashsale_consumer_joined", 30*time.Second)
	if joinedEvent.Name != "flashsale_consumer_joined" {
		t.Fatalf("首次加入消费组必须输出 flashsale_consumer_joined 日志，实际 %q", joinedEvent.Name)
	}
	if fields := lifecycleEventFields(t, joinedEvent.Line); fmt.Sprint(fields["consumer_group"]) != env.Group {
		t.Fatalf("加入日志的 consumer_group=%v，期望 %q", fields["consumer_group"], env.Group)
	}
	t.Logf("PASS 正常状态：/health 200、/ready 200、consumer_joined=1、outbox_backlog_pending=0")

	// ---------- 阶段 2：真实 Kafka 停机 ----------
	lifecycleDockerMust(t, "stop", kafkaContainer)
	waitMetricValue(t, base, metricConsumerJoined, 0, 150*time.Second)
	t.Logf("PASS Kafka 停机：consumer_joined 归 0（进程 pid=%d 仍存活）", p.cmd.Process.Pid)

	// 跨多个 readiness 缓存窗口（默认 3s）重复验证：Kafka 故障绝不能摘流 HTTP。
	for i := 0; i < 3; i++ {
		if !p.Alive() {
			t.Fatalf("Kafka 故障期间 serve 子进程退出；日志尾部:\n%s", lifecycleTail(p.Logs(), 60))
		}
		assertHealthAndReady(t, base, 200, 200)
		time.Sleep(2 * time.Second)
	}
	if v, ok := metricValue(scrapeMetrics(t, base), metricConsumerJoined); !ok || v != 0 {
		t.Fatalf("Kafka 停机期间 consumer_joined=(%v,%v) want (true,0)", v, ok)
	}
	t.Logf("PASS Kafka 故障期间 HTTP 可用：连续 3 个 readiness 窗口 /health=200 /ready=200")

	// ---------- 阶段 3：故障期间受理请求 ----------
	userID := int64(971500) // 位于 lifecycleSetupIsolatedDB 清理区间内，可重复运行
	token := flashMintUserToken(t, userID)
	key := "t202-outage-1"
	res, err := isoRequest(base, "POST", fmt.Sprintf("/flash-sales/%d/orders", activityID), map[string]any{
		"sku_id": skuID, "idempotency_key": key,
	}, isoAuthHeader(token))
	if err != nil {
		t.Fatalf("Kafka 故障期间下单请求失败: %v", err)
	}
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("Kafka 故障期间下单被拒（HTTP 不应依赖 Kafka）: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}

	// Outbox 待发布数量必须反映真实积压（这是「请求已可靠落库」的可观测证据）。
	waitMetricValue(t, base, metricOutboxBacklogPending, 1, 90*time.Second)
	row := lifecycleLoadRequests(t, activityID)[key]
	if row.Status != 0 || row.Published {
		t.Fatalf("Kafka 故障期间请求状态异常（应 queued 且未发布）: %+v", row)
	}
	t.Logf("PASS Kafka 故障期间受理：request_id=%d status=queued published=false backlog=1", row.Id)

	// ---------- 阶段 4：恢复 Kafka ----------
	lifecycleDockerMust(t, "start", kafkaContainer)
	lifecycleWaitKafkaHealthy(t, kafkaContainer, 150*time.Second)

	waitMetricValue(t, base, metricConsumerJoined, 1, 180*time.Second)
	lifecycleWaitRequestTerminal(t, activityID, key, 180*time.Second)
	waitMetricValue(t, base, metricOutboxBacklogPending, 0, 150*time.Second)
	t.Logf("PASS Kafka 恢复：consumer_joined=1、请求已发布并落单、backlog 收敛为 0")

	// ---------- 阶段 5：数据一致性 ----------
	orders := lifecycleCountOrders(t, activityID)
	success := lifecycleCountSuccess(t, activityID)
	sold := lifecycleSold(t, activityID, skuID)
	if duplicates := lifecycleDuplicateOrderKeys(t, activityID); len(duplicates) > 0 {
		t.Fatalf("存在重复订单: %v", duplicates)
	}
	if orders != 1 || success != 1 || sold != 1 {
		t.Fatalf("一致性核对失败：orders=%d success=%d sold=%d（期望均为 1）", orders, success, sold)
	}
	if !p.Alive() {
		t.Fatalf("恢复阶段 serve 子进程已退出；日志尾部:\n%s", lifecycleTail(p.Logs(), 60))
	}
	t.Logf("PASS 数据一致性：orders=%d success=%d sold=%d duplicate_orders=0", orders, success, sold)

	// ---------- 阶段 6：SIGTERM 正常退出（证明新增采样 goroutine 不影响关闭） ----------
	p.sendSIGTERM()
	if code, elapsed := p.waitExit(lifecycleMaxExitWait); code != 0 {
		t.Fatalf("退出码=%d（期望 0），耗时 %s", code, elapsed)
	}
	lifecycleAssertNoResidualBinary(t, bin)
	t.Logf("PASS 关闭：SIGTERM 后进程正常退出（退出码 0）")
}

// lifecycleDockerMust 执行 docker 子命令；故障注入本身不可静默跳过。
func lifecycleDockerMust(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s 失败: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// lifecycleWaitKafkaHealthy 等待 Kafka 容器 healthy，并留出 Broker 元数据恢复时间。
func lifecycleWaitKafkaHealthy(t *testing.T, container string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", container).Output()
		if err == nil && strings.TrimSpace(string(out)) == "healthy" {
			time.Sleep(2 * time.Second)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("Kafka 容器 %s 未在 %s 内恢复 healthy", container, timeout)
}
