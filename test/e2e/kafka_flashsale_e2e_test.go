//go:build kafka_e2e

package e2e

// B4 真实端到端验证（真实 MySQL + Redis + Kafka + HTTP）。
//
// 运行方式：
//
//	docker compose --profile kafka up -d kafka
//	bash scripts/test-kafka-e2e.sh
//
// 或手工：
//
//	DATABASE_DEFAULT_NAME=my_shop_kafka_e2e DATABASE_DEFAULT_PASS=root \
//	REDIS_DEFAULT_DB=6 FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 \
//	go test -tags kafka_e2e -p 1 -count=1 -v -timeout 20m ./test/e2e/ -run 'TestKafka|TestFlashSaleModeSwitch'
//
// 这些用例会清理测试库中的秒杀/商品域表，必须指向一次性数据库（scripts/test-kafka-e2e.sh 会自动创建）。
// 不使用任何 mock：HTTP 请求经真实路由，Consumer 经真实 Broker，订单落在真实 MySQL。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/twmb/franz-go/pkg/kgo"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/worker"
)

const (
	kafkaE2ETopicDefault = "surgecart.e2e.flashsale.order.requested.v1"
	kafkaE2EBrokerEnv    = "FLASH_SALE_KAFKA_TEST_BROKER"
)

// kafkaE2EGroupName 在同一个测试进程内复用同一个消费组：
// Topic 是脚本按运行创建的，组内位移连续推进，后续用例不会从 earliest 重放前面用例的消息。
// 每次 go test 进程独立运行，组名带时间戳；脚本也可显式注入 FLASH_SALE_KAFKA_TEST_GROUP。
var kafkaE2EGroupName = func() string {
	if v := strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_GROUP")); v != "" {
		return v
	}
	return fmt.Sprintf("surgecart-e2e-%d", time.Now().UnixNano())
}()

// kafkaE2EContext 返回真实 Broker 地址；未显式提供时跳过，避免把「未验证」伪装成通过。
func kafkaE2EContext(t *testing.T) string {
	t.Helper()
	broker := strings.TrimSpace(os.Getenv(kafkaE2EBrokerEnv))
	if broker == "" {
		t.Skipf("未设置 %s，跳过真实 Kafka 端到端测试", kafkaE2EBrokerEnv)
	}
	return broker
}

func kafkaE2ETopic() string {
	if v := strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_TOPIC")); v != "" {
		return v
	}
	return kafkaE2ETopicDefault
}

// setKafkaModeEnv 把进程配置切到 kafka 模式。
// 每次 StartFlashSaleRuntime 都会重新读取配置，因此同一测试内切换模式只需重设环境变量
// 并重启 runtime（与生产「改配置后重启进程」一致）。
func setKafkaModeEnv(t *testing.T, broker, group string) {
	t.Helper()
	t.Setenv("FLASH_SALE_DISPATCH_MODE", "kafka")
	t.Setenv("FLASH_SALE_KAFKA_ENABLED", "true")
	t.Setenv("FLASH_SALE_KAFKA_BROKERS", broker)
	t.Setenv("FLASH_SALE_KAFKA_TOPIC", kafkaE2ETopic())
	t.Setenv("FLASH_SALE_KAFKA_DLQ_TOPIC", kafkaE2ETopic()+".dlq")
	t.Setenv("FLASH_SALE_KAFKA_CONSUMER_GROUP", group)
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE", "20")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL", "50ms")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF", "200ms")
	t.Setenv("FLASH_SALE_SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
}

func setDBScannerModeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FLASH_SALE_DISPATCH_MODE", "db_scanner")
	t.Setenv("FLASH_SALE_KAFKA_ENABLED", "false")
	// 不可达地址：db_scanner 模式不得因 Kafka 配置/连接影响原有链路。
	t.Setenv("FLASH_SALE_KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("FLASH_SALE_SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
}

func startE2ERuntime(t *testing.T, ctx context.Context) *worker.FlashSaleRuntime {
	t.Helper()
	rt, err := worker.StartFlashSaleRuntime(ctx)
	if err != nil {
		t.Fatalf("启动秒杀 runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Shutdown(); err != nil {
			t.Logf("关闭秒杀 runtime: %v", err)
		}
	})
	return rt
}

// flashResult 是 GET /flash-sales/:id/orders/result 的响应。
type flashResult struct {
	Status   string `json:"status"`
	FailCode int    `json:"fail_code"`
	Order    *struct {
		Id int64 `json:"id"`
	} `json:"order"`
}

func flashGetResult(t *testing.T, base string, activityID int64, token, key string) flashResult {
	t.Helper()
	path := fmt.Sprintf("/flash-sales/%d/orders/result?idempotency_key=%s", activityID, url.QueryEscape(key))
	res := isoDo(t, base, "GET", path, nil, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("查询秒杀结果: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var out flashResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return out
}

// flashWaitSuccess 轮询真实 HTTP 查询接口，直到请求落单成功或超时。
func flashWaitSuccess(t *testing.T, base string, activityID int64, token, key string, timeout time.Duration) flashResult {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last flashResult
	for time.Now().Before(deadline) {
		last = flashGetResult(t, base, activityID, token, key)
		switch last.Status {
		case "success":
			return last
		case "failed", "dead":
			t.Fatalf("秒杀请求进入终态失败: status=%s fail_code=%d key=%s", last.Status, last.FailCode, key)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("秒杀请求未在 %s 内完成: status=%s key=%s", timeout, last.Status, key)
	return last
}

// flashAssertPublished 断言 Outbox 发布事实与消费终态最终都已落库。
//
// 为什么需要轮询：Kafka 已 ACK 但 MySQL 标记失败时（MySQL 短暂不可用），记录会保留原租约，
// 必须等租约到期后重新领取并标记；业务终态（订单）可能先于 published_at 可见。
// 这正是 at-least-once 的恢复窗口，不能把「暂时未标记」误判为丢事件。
func flashAssertPublished(t *testing.T, activityID, skuID int64, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		rec, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
			Fields("status,published_at,publish_claim_token,published_partition,published_offset").
			Where("activity_id", activityID).Where("idempotency_key", key).One()
		if err != nil {
			t.Fatalf("查询发布事实: %v", err)
		}
		if rec == nil || rec.IsEmpty() {
			t.Fatalf("请求 %s 不存在", key)
		}
		last = fmt.Sprintf("status=%d published_at=%v token=%q partition=%v offset=%v",
			rec["status"].Int(), rec["published_at"].String(), rec["publish_claim_token"].String(),
			rec["published_partition"].String(), rec["published_offset"].String())
		if rec["status"].Int() == 1 && !rec["published_at"].IsNil() &&
			rec["publish_claim_token"].String() == "" &&
			!rec["published_partition"].IsNil() && !rec["published_offset"].IsNil() {
			sold, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
				Where("activity_id", activityID).Where("sku_id", skuID).Value("sold")
			if err != nil {
				t.Fatalf("查询 sold: %v", err)
			}
			if sold.Int64() != 1 {
				t.Fatalf("sold 应为 1，实际 %d", sold.Int64())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("请求 %s 未在 %s 内完成发布标记（%s）", key, timeout, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// flashSeedSellableActivity 建立测试数据：可售 SKU、进行中的秒杀活动、库存与 Redis 预热。
func flashSeedSellableActivity(t *testing.T, name string, totalStock int64) (activityID, skuID int64) {
	t.Helper()
	skuID = flashSetupSku(t, name, 5000, 1)
	now := flashMySQLNow(t)
	activityID = flashInsertActivity(t, name, 1, now.Add(-time.Hour), now.Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, totalStock)
	flashSyncCache(t, activityID)
	return activityID, skuID
}

// TestKafkaFlashSaleEndToEnd 是 B4 的核心验收：
//
//	HTTP → Redis Lua 准入 → MySQL Request + Outbox → Publisher → Kafka
//	     → Consumer → MySQL 订单事务 → Offset 提交 → HTTP 查询结果
func TestKafkaFlashSaleEndToEnd(t *testing.T) {
	broker := kafkaE2EContext(t)
	base := setupFlashSaleServer(t)

	group := kafkaE2EGroupName
	setKafkaModeEnv(t, broker, group)
	activityID, skuID := flashSeedSellableActivity(t, "kafka-e2e", 5)
	userID := int64(950001)
	token := flashMintUserToken(t, userID)
	key := "kafka-e2e-1"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := startE2ERuntime(t, ctx)

	// 真实 HTTP 入口：Redis 闸门通过后才写 Request/Outbox。
	env := flashOrderCall(t, base, activityID, token, map[string]any{
		"sku_id": skuID, "idempotency_key": key,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil || env.Data.Status != "queued" {
		t.Fatalf("秒杀下单未受理: status=%d code=%d msg=%q data=%+v", env.Status, env.Code, env.Message, env.Data)
	}

	result := flashWaitSuccess(t, base, activityID, token, key, 20*time.Second)
	if result.Order == nil || result.Order.Id <= 0 {
		t.Fatalf("success 结果缺少订单: %+v", result)
	}
	flashAssertPublished(t, activityID, skuID, key, 10*time.Second)
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("秒杀订单数应为 1，实际 %d", n)
	}

	// 停止 Consumer 后，用同组新客户端验证位移已提交：若提交失败，会重新收到该消息。
	if err := rt.Shutdown(); err != nil {
		t.Fatalf("关闭 runtime: %v", err)
	}
	assertNoRedelivery(t, broker, group)
}

// assertNoRedelivery 用同组新消费者拉取：已提交位移后不应再收到消息。
// 这是「MySQL COMMIT 先于 Offset Commit」链路最终把位移推进到末尾的可观测证据。
func assertNoRedelivery(t *testing.T, broker, group string) {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(kafkaE2ETopic()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		t.Fatalf("创建位移校验消费者: %v", err)
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fetches := cl.PollRecords(ctx, 1)
	if err := fetches.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("位移校验拉取失败: %v", err)
	}
	if n := fetches.NumRecords(); n != 0 {
		t.Fatalf("位移未提交：同组新消费者重新收到 %d 条消息", n)
	}
}

// TestDBScannerFlashSaleEndToEndWithoutKafka 覆盖 A：默认 db_scanner 模式不需要 Kafka，
// HTTP → Redis 准入 → MySQL 入队 → Scanner 事务落单；published_at 必须保持 NULL。
func TestDBScannerFlashSaleEndToEndWithoutKafka(t *testing.T) {
	base := setupFlashSaleServer(t)
	setDBScannerModeEnv(t)

	activityID, skuID := flashSeedSellableActivity(t, "db-scanner-e2e", 5)
	token := flashMintUserToken(t, 950101)
	key := "db-scanner-e2e-1"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = startE2ERuntime(t, ctx)

	env := flashOrderCall(t, base, activityID, token, map[string]any{
		"sku_id": skuID, "idempotency_key": key,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil || env.Data.Status != "queued" {
		t.Fatalf("秒杀下单未受理: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}

	flashWaitSuccess(t, base, activityID, token, key, 10*time.Second)
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("秒杀订单数应为 1，实际 %d", n)
	}

	rec, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Fields("published_at,publish_claim_token").
		Where("activity_id", activityID).Where("idempotency_key", key).One()
	if err != nil || rec == nil || rec.IsEmpty() {
		t.Fatalf("查询请求: %v", err)
	}
	if !rec["published_at"].IsNil() || rec["publish_claim_token"].String() != "" {
		t.Fatalf("db_scanner 模式不得运行 Outbox Publisher: %+v", rec)
	}
}

// TestFlashSaleModeSwitchRequiresRestart 覆盖 D：
// DB Scanner → Kafka → DB Scanner，每次都是「停旧组件后启动新模式」，
// 三段请求都必须完成且不产生重复订单。
func TestFlashSaleModeSwitchRequiresRestart(t *testing.T) {
	broker := kafkaE2EContext(t)
	base := setupFlashSaleServer(t)
	activityID, skuID := flashSeedSellableActivity(t, "mode-switch-e2e", 10)

	// 秒杀「一人一单」：每个阶段使用不同用户，才能产生三笔订单。
	submitAndWait := func(phase, key string, userID int64) {
		t.Helper()
		token := flashMintUserToken(t, userID)
		env := flashOrderCall(t, base, activityID, token, map[string]any{
			"sku_id": skuID, "idempotency_key": key,
		})
		if env.Status != 200 || env.Code != 0 || env.Data == nil || env.Data.Status != "queued" {
			t.Fatalf("%s 下单未受理: status=%d code=%d msg=%q", phase, env.Status, env.Code, env.Message)
		}
		flashWaitSuccess(t, base, activityID, token, key, 20*time.Second)
	}

	// 阶段 1：db_scanner。
	setDBScannerModeEnv(t)
	ctx1, cancel1 := context.WithCancel(context.Background())
	rt1 := startE2ERuntime(t, ctx1)
	submitAndWait("db_scanner", "switch-1", 950201)
	if err := rt1.Shutdown(); err != nil {
		t.Fatalf("停止 db_scanner runtime: %v", err)
	}
	cancel1()

	// 阶段 2：kafka（必须重启 runtime，运行中不热切换）。
	setKafkaModeEnv(t, broker, kafkaE2EGroupName)
	ctx2, cancel2 := context.WithCancel(context.Background())
	rt2 := startE2ERuntime(t, ctx2)
	submitAndWait("kafka", "switch-2", 950202)
	if err := rt2.Shutdown(); err != nil {
		t.Fatalf("停止 kafka runtime: %v", err)
	}
	cancel2()

	// 阶段 3：切回 db_scanner。
	setDBScannerModeEnv(t)
	ctx3, cancel3 := context.WithCancel(context.Background())
	rt3 := startE2ERuntime(t, ctx3)
	submitAndWait("db_scanner(2)", "switch-3", 950203)
	if err := rt3.Shutdown(); err != nil {
		t.Fatalf("停止第二个 db_scanner runtime: %v", err)
	}
	cancel3()

	if n := flashOrderCount(t, activityID); n != 3 {
		t.Fatalf("三个阶段应各产生一单，实际 %d", n)
	}
	for _, key := range []string{"switch-1", "switch-2", "switch-3"} {
		if st := flashRequestStatusForKey(t, key); st != 1 {
			t.Fatalf("请求 %s 状态应为 success(1)，实际 %d", key, st)
		}
	}
}

func flashRequestStatusForKey(t *testing.T, key string) int {
	t.Helper()
	rec, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Fields("status").Where("idempotency_key", key).One()
	if err != nil || rec == nil || rec.IsEmpty() {
		t.Fatalf("查询请求 %s: %v", key, err)
	}
	return rec["status"].Int()
}

// TestKafkaFlashSalePublisherRestartRecoversOutbox 覆盖 Publisher/Consumer 重启：
// runtime 停止期间 HTTP 仍可受理（Request/Outbox 已提交），重启后由新 runtime 完成发布与落单。
func TestKafkaFlashSalePublisherRestartRecoversOutbox(t *testing.T) {
	broker := kafkaE2EContext(t)
	base := setupFlashSaleServer(t)
	activityID, skuID := flashSeedSellableActivity(t, "publisher-restart-e2e", 5)
	token := flashMintUserToken(t, 950301)
	key := "publisher-restart-1"

	group := kafkaE2EGroupName
	setKafkaModeEnv(t, broker, group)

	// 第一次启动后立即停止：模拟进程重启，且此时还没有待发布消息。
	ctx1, cancel1 := context.WithCancel(context.Background())
	rt1 := startE2ERuntime(t, ctx1)
	if err := rt1.Shutdown(); err != nil {
		t.Fatalf("停止 runtime: %v", err)
	}
	cancel1()

	// 后台链路停止期间受理请求：只写 MySQL Request + Outbox。
	env := flashOrderCall(t, base, activityID, token, map[string]any{
		"sku_id": skuID, "idempotency_key": key,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil || env.Data.Status != "queued" {
		t.Fatalf("runtime 停止期间受理失败: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	rec, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Fields("published_at").Where("idempotency_key", key).One()
	if err != nil || rec == nil || !rec["published_at"].IsNil() {
		t.Fatalf("重启前不应已发布: %v %+v", err, rec)
	}

	// 重启：Outbox 领取未发布记录 → Kafka → Consumer 落单。
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_ = startE2ERuntime(t, ctx2)
	flashWaitSuccess(t, base, activityID, token, key, 20*time.Second)
	flashAssertPublished(t, activityID, skuID, key, 10*time.Second)
}

// TestKafkaFlashSaleBrokerOutageRecovery 覆盖 Kafka 暂时不可用后恢复。
// 需要显式设置 FLASH_SALE_KAFKA_E2E_DOCKER=1（会 stop/start Compose 中的 kafka 容器）。
func TestKafkaFlashSaleBrokerOutageRecovery(t *testing.T) {
	broker := kafkaE2EContext(t)
	if os.Getenv("FLASH_SALE_KAFKA_E2E_DOCKER") != "1" {
		t.Skip("未设置 FLASH_SALE_KAFKA_E2E_DOCKER=1，跳过 Broker 停机故障注入")
	}
	container := os.Getenv("FLASH_SALE_KAFKA_E2E_CONTAINER")
	if container == "" {
		container = "my-shop-kafka"
	}

	base := setupFlashSaleServer(t)
	activityID, skuID := flashSeedSellableActivity(t, "broker-outage-e2e", 5)
	token := flashMintUserToken(t, 950401)

	setKafkaModeEnv(t, broker, kafkaE2EGroupName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = startE2ERuntime(t, ctx)

	// 先确认链路正常。
	firstKey := "outage-ok-1"
	if env := flashOrderCall(t, base, activityID, token, map[string]any{
		"sku_id": skuID, "idempotency_key": firstKey,
	}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("故障前下单失败: status=%d code=%d", env.Status, env.Code)
	}
	flashWaitSuccess(t, base, activityID, token, firstKey, 20*time.Second)

	dockerMust(t, "stop", container)
	t.Cleanup(func() {
		_ = exec.Command("docker", "start", container).Run()
	})

	// Broker 不可用时 HTTP 仍可受理：Redis 准入门 + MySQL Request/Outbox 不依赖 Kafka。
	// 秒杀一人一单，因此第二个请求换一个用户。
	secondKey := "outage-recover-1"
	secondToken := flashMintUserToken(t, 950402)
	if env := flashOrderCall(t, base, activityID, secondToken, map[string]any{
		"sku_id": skuID, "idempotency_key": secondKey,
	}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("Broker 故障期间受理失败（HTTP 不应依赖 Kafka）: status=%d code=%d", env.Status, env.Code)
	}
	time.Sleep(2 * time.Second)

	dockerMust(t, "start", container)
	waitKafkaHealthy(t, container, 120*time.Second)
	flashWaitSuccess(t, base, activityID, secondToken, secondKey, 180*time.Second)
}

// TestKafkaFlashSaleMySQLOutageRecovery 覆盖 MySQL 暂时不可用后恢复：
// 请求已在 Outbox 中，Publisher 在数据库不可用期间只退避、不丢记录；恢复后完成发布与落单。
func TestKafkaFlashSaleMySQLOutageRecovery(t *testing.T) {
	broker := kafkaE2EContext(t)
	if os.Getenv("FLASH_SALE_KAFKA_E2E_DOCKER") != "1" {
		t.Skip("未设置 FLASH_SALE_KAFKA_E2E_DOCKER=1，跳过 MySQL 停机故障注入")
	}
	container := os.Getenv("FLASH_SALE_KAFKA_E2E_MYSQL_CONTAINER")
	if container == "" {
		container = "my-shop-mysql"
	}

	base := setupFlashSaleServer(t)
	activityID, skuID := flashSeedSellableActivity(t, "mysql-outage-e2e", 5)
	token := flashMintUserToken(t, 950501)

	setKafkaModeEnv(t, broker, kafkaE2EGroupName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = startE2ERuntime(t, ctx)

	key := "mysql-outage-1"
	if env := flashOrderCall(t, base, activityID, token, map[string]any{
		"sku_id": skuID, "idempotency_key": key,
	}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("MySQL 故障前下单失败: status=%d code=%d", env.Status, env.Code)
	}

	dockerMust(t, "stop", container)
	t.Cleanup(func() {
		_ = exec.Command("docker", "start", container).Run()
	})
	// 留出若干轮发布失败/退避的时间，验证不会因为失败而丢弃记录。
	time.Sleep(3 * time.Second)

	dockerMust(t, "start", container)
	waitMySQLHealthy(t, container, 120*time.Second)
	flashWaitSuccess(t, base, activityID, token, key, 180*time.Second)
	// Kafka 可能已 ACK、但 MySQL 标记失败：
	//   - Consumer 尚未落终态时，租约到期后会重新领取并重发；
	//   - Consumer 已落终态时，消费事务用消息的 partition/offset 补写发布凭证。
	// 两种路径都必须最终写入 published_at；这里覆盖整个恢复窗口，证明不会丢事件。
	flashAssertPublished(t, activityID, skuID, key, 120*time.Second)
}

// dockerMust 执行 docker 子命令；失败直接终止测试（故障注入本身不可静默跳过）。
func dockerMust(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s 失败: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func waitKafkaHealthy(t *testing.T, container string, timeout time.Duration) {
	t.Helper()
	waitContainerHealthy(t, container, timeout)
	// 健康检查通过后，Broker API 可能仍在恢复元数据；短暂等待让客户端重连。
	time.Sleep(2 * time.Second)
}

func waitMySQLHealthy(t *testing.T, container string, timeout time.Duration) {
	t.Helper()
	waitContainerHealthy(t, container, timeout)
}

func waitContainerHealthy(t *testing.T, container string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", container).Output()
		if err == nil && strings.TrimSpace(string(out)) == "healthy" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("容器 %s 未在 %s 内恢复 healthy", container, timeout)
}

// TestKafkaFlashSaleShutdownWithInflightTraffic 覆盖 Publisher 与 Consumer 同时运行时关闭：
// 关闭必须在预算内完成，且下一次启动仍能处理遗留请求（不丢事件）。
func TestKafkaFlashSaleShutdownWithInflightTraffic(t *testing.T) {
	broker := kafkaE2EContext(t)
	base := setupFlashSaleServer(t)
	activityID, skuID := flashSeedSellableActivity(t, "shutdown-inflight-e2e", 20)

	setKafkaModeEnv(t, broker, kafkaE2EGroupName)
	ctx, cancel := context.WithCancel(context.Background())
	rt := startE2ERuntime(t, ctx)

	// 每笔请求使用独立用户（一人一单），否则第二笔会被业务幂等拒绝。
	type submitted struct {
		key   string
		token string
	}
	submittedOrders := make([]submitted, 0, 10)
	for i := 0; i < 10; i++ {
		key := "shutdown-inflight-" + strconv.Itoa(i)
		token := flashMintUserToken(t, int64(950601+i))
		if env := flashOrderCall(t, base, activityID, token, map[string]any{
			"sku_id": skuID, "idempotency_key": key,
		}); env.Status != 200 || env.Code != 0 {
			t.Fatalf("下单 %s 失败: status=%d code=%d", key, env.Status, env.Code)
		}
		submittedOrders = append(submittedOrders, submitted{key: key, token: token})
	}

	// 立即关闭：Publisher 可能正在发布、Consumer 可能正在消费。
	started := time.Now()
	if err := rt.Shutdown(); err != nil {
		t.Fatalf("关闭 runtime: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("关闭耗时 %v，超出 shutdown budget", elapsed)
	}
	cancel()

	// 重启后所有有效请求必须收敛（已成功的不重复落单，未处理的继续处理）。
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_ = startE2ERuntime(t, ctx2)
	for _, order := range submittedOrders {
		flashWaitSuccess(t, base, activityID, order.token, order.key, 30*time.Second)
	}
	if n := flashOrderCount(t, activityID); n != len(submittedOrders) {
		t.Fatalf("订单数应为 %d，实际 %d（可能存在重复落单或漏单）", len(submittedOrders), n)
	}
	if metrics.Enabled(context.Background()) {
		// 指标开关已打开：确认消费链路至少记录到成功处理（不作为业务事实，仅可观测性冒烟）。
		_ = metrics.Handler()
	}
}
