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
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// 预热请求用于确认 Publisher/Consumer 在 SIGTERM 前已真实运行。
	lifecycleWarmupUserID = int64(970000)
	// 突发请求的独立用户数与目标受理数（秒杀一人一单，必须一人一请求）。
	lifecycleBurstUsers     = 60
	lifecycleTargetAccepted = 36
	// 进程退出上限 = server.graceful_timeout(10s) + flash_sale.shutdown_timeout(30s) + 余量。
	lifecycleMaxExitWait = 45 * time.Second
	// flash_sale.shutdown_timeout 的验收上限。
	lifecycleShutdownBudget = 30 * time.Second
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
	mu.Lock()
	httpAcceptedCount := len(accepted)
	mu.Unlock()

	// 关闭过程证据：信号 → HTTP drain → Publisher → Consumer → Producer → Scanner。
	logs := p1.Logs()
	events := lifecycleParseEvents(logs)
	if !strings.Contains(logs, "server gracefully shutting down by signal: terminated") {
		t.Fatalf("日志中缺少操作系统信号接收证据（GoFrame signal shutdown 日志）；日志尾部:\n%s", lifecycleTail(logs, 60))
	}
	order := []string{
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
	if httpIdx < 0 {
		t.Fatalf("缺少 http_server_stopped 事件")
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
	lifecycleAssertNoRedelivery(t, env.Broker, env.Topic, env.Group)
	t.Logf("PASS 消费位移已提交：同组新消费者未重新收到任何消息")

	t.Logf("B4.1 进程级验收通过：serve 启动=%s，SIGTERM 退出耗时=%s，重启收敛完成",
		started.At.Format(time.RFC3339Nano), elapsed.Round(time.Millisecond))
}
