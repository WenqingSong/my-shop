package flashsale

// B1-05-03 秒杀回归测试（只新增测试，不修改任何秒杀生产内核）。
//
// 覆盖范围（与任务 5.1–5.5 一一对应）：
//   5.1 Redis Lua 准入闸门：正常准入 / 库存不足 / 活动不存在 / 状态不允许 / 不在时间窗 /
//       SKU 未绑定 / 重复购买 / 幂等命中与冲突 / 并发准入；
//   5.2 秒杀库存售罄：库存 0 拒绝、并发不超卖、售罄标记与 MySQL 状态收敛；
//   5.3 重复购买拦截：同用户重复抢购、同幂等键重复提交、并发请求、消费者重复投递；
//   5.4 Redis 故障 fail-closed：闸门执行失败 → 1005/503，不降级 MySQL、不落请求、不预扣；
//   5.5 Kafka 消费幂等：同一 Request 重复消费只产生一次业务结果。
//
// 说明：闸门用真实 Redis 执行真实 Lua（g.Redis().Do EVAL），售罄/落单链路走真实 MySQL；
// 不 mock Redis 也不 mock 数据库，避免把「缓存里算对了」误当成「业务上正确」。

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// regressionActivity 建立启用且时间窗内的秒杀活动（-60min ~ +60min）并绑定给定 SKU。
func regressionActivity(t *testing.T, skuID, totalStock int64) int64 {
	t.Helper()
	ctx := context.Background()
	now := consumeMySQLNow(t)
	activityID, err := g.DB().Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
		"name":       "regression-" + uniqueRegressionSuffix(),
		"status":     1,
		"start_time": now.Add(-time.Hour).Format("2006-01-02 15:04:05"),
		"end_time":   now.Add(time.Hour).Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入秒杀活动失败: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
		"activity_id": activityID, "sku_id": skuID, "flash_price": 1000,
		"total_stock": totalStock, "sold": 0,
	}).Insert(); err != nil {
		t.Fatalf("插入秒杀绑定失败: %v", err)
	}
	return activityID
}

// regressionFutureActivity 建立「未开始」的活动（+60min ~ +120min），用于闸门时间窗拒绝。
func regressionFutureActivity(t *testing.T, skuID, totalStock int64) int64 {
	t.Helper()
	ctx := context.Background()
	now := consumeMySQLNow(t)
	activityID, err := g.DB().Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
		"name":       "regression-future-" + uniqueRegressionSuffix(),
		"status":     1,
		"start_time": now.Add(time.Hour).Format("2006-01-02 15:04:05"),
		"end_time":   now.Add(2 * time.Hour).Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入未开始活动失败: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
		"activity_id": activityID, "sku_id": skuID, "flash_price": 1000,
		"total_stock": totalStock, "sold": 0,
	}).Insert(); err != nil {
		t.Fatalf("插入未开始活动绑定失败: %v", err)
	}
	return activityID
}

// uniqueRegressionSuffix 生成活动名后缀，避免同一轮测试内多个活动重名。
func uniqueRegressionSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// regressionGate 直接执行生产 Lua 闸门（不经过限流/熔断），返回闸门结论。
func regressionGate(t *testing.T, s *sFlashSale, activityID, skuID, userID int64, key string) (gateResult, error) {
	t.Helper()
	return s.runGate(context.Background(), activityID, skuID, userID, key, requestHash(activityID, skuID))
}

// regressionRedisInt 读取 Redis 整数值（不存在返回 -1）。
func regressionRedisInt(t *testing.T, key string) int64 {
	t.Helper()
	v, err := g.Redis().Get(context.Background(), key)
	if err != nil {
		t.Fatalf("读取 Redis key %s 失败: %v", key, err)
	}
	if v.IsNil() || v.IsEmpty() {
		return -1
	}
	return v.Int64()
}

// regressionExists 判断 Redis key 是否存在。
func regressionExists(t *testing.T, key string) bool {
	t.Helper()
	n, err := g.Redis().Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("探测 Redis key %s 失败: %v", key, err)
	}
	return n > 0
}

// TestFlashSaleRegressionLuaGateMatrix 覆盖 5.1 的确定性闸门分支：
// 正常准入 → 幂等命中 → 一人一单 → 另一用户准入 → SKU 未绑定 → 活动不存在 → 未开始 → 已停用。
func TestFlashSaleRegressionLuaGateMatrix(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reg-gate")
	unboundSkuID := consumeTestSku(t, "reg-gate-unbound")
	activityID := regressionActivity(t, skuID, 10)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 10 {
		t.Fatalf("预热后 remaining=%d，期望 10", got)
	}

	// 1) 正常准入：预扣 1 件并写入一人一单/幂等标记。
	if gate, err := regressionGate(t, s, activityID, skuID, 850001, "k-normal"); err != nil || gate != gatePassed {
		t.Fatalf("正常准入 gate=%s err=%v，期望 GATE_PASSED", gate, err)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 9 {
		t.Fatalf("准入后 remaining=%d，期望 9", got)
	}
	if !regressionExists(t, flashSaleBoughtKey(activityID, skuID, 850001)) {
		t.Fatalf("GATE_PASSED 必须原子写入一人一单标记")
	}
	if !regressionExists(t, flashSaleIdemKey(850001, "k-normal")) {
		t.Fatalf("GATE_PASSED 必须原子写入幂等标记")
	}

	// 2) 同一用户同一幂等键重试：幂等命中（不重复预扣）。
	if gate, err := regressionGate(t, s, activityID, skuID, 850001, "k-normal"); err != nil || gate != gateIdempotentHit {
		t.Fatalf("幂等重试 gate=%s err=%v，期望 IDEMPOTENT_HIT", gate, err)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 9 {
		t.Fatalf("幂等重试后 remaining=%d，期望 9（不得重复预扣）", got)
	}

	// 3) 同一用户换幂等键：一人一单拦截。
	if gate, err := regressionGate(t, s, activityID, skuID, 850001, "k-other"); err != nil || gate != gateAlreadyPurchased {
		t.Fatalf("同用户换键 gate=%s err=%v，期望 ALREADY_PURCHASED", gate, err)
	}

	// 4) 另一用户可正常准入（库存 9 → 8）。
	if gate, err := regressionGate(t, s, activityID, skuID, 850002, "k-user2"); err != nil || gate != gatePassed {
		t.Fatalf("第二用户准入 gate=%s err=%v，期望 GATE_PASSED", gate, err)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 8 {
		t.Fatalf("第二用户准入后 remaining=%d，期望 8", got)
	}

	// 5) SKU 未绑定：活动已预热但没有该 SKU 的库存 key。
	if gate, err := regressionGate(t, s, activityID, unboundSkuID, 850003, "k-unbound"); err != nil || gate != gateSkuNotBound {
		t.Fatalf("未绑定 SKU gate=%s err=%v，期望 SKU_NOT_BOUND", gate, err)
	}

	// 6) 活动不存在（Redis 无任何活动 key）。
	if gate, err := regressionGate(t, s, activityID+999999, skuID, 850004, "k-missing"); err != nil || gate != gateNotFound {
		t.Fatalf("不存在活动 gate=%s err=%v，期望 NOT_FOUND", gate, err)
	}

	// 7) 活动未开始：状态启用但时间窗在未来。
	futureID := regressionFutureActivity(t, skuID, 10)
	if err := s.SyncCache(ctx, futureID); err != nil {
		t.Fatalf("预热未开始活动失败: %v", err)
	}
	if gate, err := regressionGate(t, s, futureID, skuID, 850005, "k-future"); err != nil || gate != gateNotInWindow {
		t.Fatalf("未开始活动 gate=%s err=%v，期望 NOT_IN_WINDOW", gate, err)
	}

	// 8) 活动已停用：同步缓存后置空值标记，闸门快速失败。
	if _, err := g.DB().Model("flash_sale_activities").Ctx(ctx).
		Where("id", activityID).Data(g.Map{"status": 0}).Update(); err != nil {
		t.Fatalf("停用活动失败: %v", err)
	}
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("停用后同步缓存失败: %v", err)
	}
	if gate, err := regressionGate(t, s, activityID, skuID, 850006, "k-disabled"); err != nil || gate != gateNotFound {
		t.Fatalf("停用活动 gate=%s err=%v，期望 NOT_FOUND", gate, err)
	}
}

// TestFlashSaleRegressionLuaGateSoldOut 覆盖 5.1/5.2 的售罄分支：
// 库存为 0 时闸门拒绝并写入售罄标记，后续请求走标记快速失败；MySQL 库存不受影响。
func TestFlashSaleRegressionLuaGateSoldOut(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reg-soldout")
	activityID := regressionActivity(t, skuID, 5)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}
	// 直接把 remaining 置 0，模拟「库存已预热耗尽」。
	if _, err := g.Redis().Set(ctx, flashSaleStockKey(activityID, skuID), "0"); err != nil {
		t.Fatalf("构造售罄状态失败: %v", err)
	}

	if gate, err := regressionGate(t, s, activityID, skuID, 851001, "k-soldout-1"); err != nil || gate != gateSoldOut {
		t.Fatalf("库存 0 gate=%s err=%v，期望 SOLD_OUT", gate, err)
	}
	if !regressionExists(t, flashSaleSoldoutKey(activityID, skuID)) {
		t.Fatalf("库存 0 拒绝后必须写入售罄标记")
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 0 {
		t.Fatalf("售罄后 remaining=%d，期望 0（不得变为负数）", got)
	}
	// 售罄标记生效：另一个用户也快速失败。
	if gate, err := regressionGate(t, s, activityID, skuID, 851002, "k-soldout-2"); err != nil || gate != gateSoldOut {
		t.Fatalf("售罄标记后 gate=%s err=%v，期望 SOLD_OUT", gate, err)
	}
	// 闸门只读 MySQL 权威库存做预热，不在 Redis 侧改 DB：sold 仍为 0。
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 0 {
		t.Fatalf("闸门拒绝不应扣减 MySQL sold，实际 sold=%d", sold)
	}
}

// TestFlashSaleRegressionLuaGateConcurrentNoOversell 覆盖 5.2 的并发不超卖：
// 库存 5、40 个用户并发抢购，恰好 5 个 GATE_PASSED，其余 SOLD_OUT，remaining 归零不为负。
func TestFlashSaleRegressionLuaGateConcurrentNoOversell(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	const (
		totalStock = 5
		users      = 40
	)
	skuID := consumeTestSku(t, "reg-race")
	activityID := regressionActivity(t, skuID, totalStock)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}

	start := make(chan struct{})
	gates := make([]gateResult, users)
	errs := make([]error, users)
	var wg sync.WaitGroup
	wg.Add(users)
	for i := 0; i < users; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			gates[i], errs[i] = s.runGate(ctx, activityID, skuID, int64(860000+i),
				"k-race-"+strconv.Itoa(i), requestHash(activityID, skuID))
		}(i)
	}
	close(start)
	wg.Wait()

	var passed, soldOut int
	for i := range gates {
		if errs[i] != nil {
			t.Fatalf("第 %d 个并发闸门执行失败: %v", i, errs[i])
		}
		switch gates[i] {
		case gatePassed:
			passed++
		case gateSoldOut:
			soldOut++
		default:
			t.Fatalf("第 %d 个并发请求返回未知结论 %s", i, gates[i])
		}
	}
	if passed != totalStock {
		t.Fatalf("并发准入 %d 次，期望恰好 %d 次（超卖或少卖）", passed, totalStock)
	}
	if soldOut != users-totalStock {
		t.Fatalf("售罄拒绝 %d 次，期望 %d 次", soldOut, users-totalStock)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 0 {
		t.Fatalf("并发后 remaining=%d，期望 0（不得为负）", got)
	}
	if !regressionExists(t, flashSaleSoldoutKey(activityID, skuID)) {
		t.Fatalf("售罄后必须写入售罄标记")
	}
}

// TestFlashSaleRegressionLuaGateDuplicateUser 覆盖 5.1/5.3 的并发重复购买：
//   - 同一用户、同一幂等键并发 20 次：1 次准入 + 19 次幂等命中，只预扣 1 件；
//   - 同一用户、不同幂等键并发 20 次：1 次准入 + 19 次一人一单拦截，只预扣 1 件。
func TestFlashSaleRegressionLuaGateDuplicateUser(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	const attempts = 20

	// 场景一：同一幂等键。
	skuID := consumeTestSku(t, "reg-idem")
	activityID := regressionActivity(t, skuID, 10)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}
	const userID = int64(870001)
	gates := make([]gateResult, attempts)
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			gates[i], errs[i] = regressionGate(t, s, activityID, skuID, userID, "k-same-key")
		}(i)
	}
	wg.Wait()

	var passed, hit int
	for i := range gates {
		if errs[i] != nil {
			t.Fatalf("同键并发第 %d 次执行失败: %v", i, errs[i])
		}
		switch gates[i] {
		case gatePassed:
			passed++
		case gateIdempotentHit:
			hit++
		default:
			t.Fatalf("同键并发第 %d 次返回 %s，期望 GATE_PASSED/IDEMPOTENT_HIT", i, gates[i])
		}
	}
	if passed != 1 || hit != attempts-1 {
		t.Fatalf("同键并发：准入 %d 次、幂等命中 %d 次，期望 1/%d", passed, hit, attempts-1)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 9 {
		t.Fatalf("同键并发后 remaining=%d，期望 9（只预扣 1 件）", got)
	}

	// 场景二：同一用户不同幂等键。
	skuID2 := consumeTestSku(t, "reg-user-dup")
	activityID2 := regressionActivity(t, skuID2, 10)
	if err := s.SyncCache(ctx, activityID2); err != nil {
		t.Fatalf("预热第二个活动失败: %v", err)
	}
	gates2 := make([]gateResult, attempts)
	errs2 := make([]error, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			gates2[i], errs2[i] = regressionGate(t, s, activityID2, skuID2, int64(870002),
				"k-diff-"+strconv.Itoa(i))
		}(i)
	}
	wg.Wait()

	passed, already := 0, 0
	for i := range gates2 {
		if errs2[i] != nil {
			t.Fatalf("不同键并发第 %d 次执行失败: %v", i, errs2[i])
		}
		switch gates2[i] {
		case gatePassed:
			passed++
		case gateAlreadyPurchased:
			already++
		default:
			t.Fatalf("不同键并发第 %d 次返回 %s，期望 GATE_PASSED/ALREADY_PURCHASED", i, gates2[i])
		}
	}
	if passed != 1 || already != attempts-1 {
		t.Fatalf("不同键并发：准入 %d 次、已购拦截 %d 次，期望 1/%d", passed, already, attempts-1)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID2, skuID2)); got != 9 {
		t.Fatalf("不同键并发后 remaining=%d，期望 9（只预扣 1 件）", got)
	}
}

// TestFlashSaleRegressionFailClosedOnRedisFault 覆盖 5.4（硬性回归）：
// Redis 闸门 Lua 执行失败时必须 fail-closed：返回 1005/503，不落请求、不建单、不预扣、
// 不降级为直接 MySQL 抢购；Redis 恢复后同一活动可正常受理。
func TestFlashSaleRegressionFailClosedOnRedisFault(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reg-failclosed")
	activityID := regressionActivity(t, skuID, 10)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}
	remainingBefore := regressionRedisInt(t, flashSaleStockKey(activityID, skuID))
	if remainingBefore != 10 {
		t.Fatalf("预热后 remaining=%d，期望 10", remainingBefore)
	}

	// 注入 Redis 闸门故障：把活动 Hash 改成 string，Lua 的 HGETALL 返回 WRONGTYPE。
	if _, err := g.Redis().Do(ctx, "SET", flashSaleActivityKey(activityID), "not-a-hash"); err != nil {
		t.Fatalf("注入 Redis 故障失败: %v", err)
	}

	for i, key := range []string{"k-failclosed-1", "k-failclosed-2"} {
		res, err := s.CreateOrder(ctx, int64(880001+i), activityID, &v1.CreateOrderReq{
			SkuId: skuID, IdempotencyKey: key,
		})
		if res != nil {
			t.Fatalf("第 %d 次 fail-closed 仍返回成功结果: %+v", i, res)
		}
		if code := codes.FromError(err); code != codes.CodeServiceUnavailable {
			t.Fatalf("第 %d 次 fail-closed 错误码=%d，期望 %d: %v",
				i, code, codes.CodeServiceUnavailable, err)
		}
	}

	// 关键断言：没有降级写 MySQL、没有预扣、没有排队。
	if n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); err != nil || n != 0 {
		t.Fatalf("fail-closed 产生了 %d 条请求，期望 0（err=%v）", n, err)
	}
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != 0 {
		t.Fatalf("fail-closed 产生了 %d 个订单，期望 0（err=%v）", n, err)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 0 {
		t.Fatalf("fail-closed 扣减了 MySQL 库存，sold=%d", sold)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != remainingBefore {
		t.Fatalf("fail-closed 改变了 Redis 预扣 remaining=%d，期望仍为 %d", got, remainingBefore)
	}

	// 恢复 Redis 后必须能正常受理（fail-closed 不是永久降级）。
	if _, err := g.Redis().Del(ctx, flashSaleActivityKey(activityID)); err != nil {
		t.Fatalf("清除故障注入失败: %v", err)
	}
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("恢复预热失败: %v", err)
	}
	res, err := s.CreateOrder(ctx, 880009, activityID, &v1.CreateOrderReq{
		SkuId: skuID, IdempotencyKey: "k-recovered",
	})
	if err != nil {
		t.Fatalf("Redis 恢复后仍无法受理: %v", err)
	}
	if res.Status != requestStatusString(requestStatusQueued) {
		t.Fatalf("恢复后受理状态=%q，期望 queued", res.Status)
	}
	if n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); err != nil || n != 1 {
		t.Fatalf("恢复后请求数=%d，期望 1（err=%v）", n, err)
	}
}

// TestFlashSaleRegressionSoldOutConvergesWithDB 覆盖 5.2 的售罄收敛：
// 库存 2、3 个用户抢购 → 2 个受理 + 1 个 12003；消费落单后 MySQL sold=2、订单=2，
// Redis remaining=0，缓存与数据库状态一致。
func TestFlashSaleRegressionSoldOutConvergesWithDB(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	const totalStock = 2
	skuID := consumeTestSku(t, "reg-converge")
	activityID := regressionActivity(t, skuID, totalStock)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}

	accepted := 0
	for i := 0; i < totalStock; i++ {
		res, err := s.CreateOrder(ctx, int64(890000+i), activityID, &v1.CreateOrderReq{
			SkuId: skuID, IdempotencyKey: "k-sold-" + strconv.Itoa(i),
		})
		if err != nil {
			t.Fatalf("第 %d 个抢购失败: %v", i, err)
		}
		if res.Status != requestStatusString(requestStatusQueued) {
			t.Fatalf("第 %d 个抢购状态=%q，期望 queued", i, res.Status)
		}
		accepted++
	}
	if accepted != totalStock {
		t.Fatalf("受理数=%d，期望 %d", accepted, totalStock)
	}

	// 售罄后的请求必须被拒绝，且不产生第 3 条请求。
	_, err := s.CreateOrder(ctx, 890099, activityID, &v1.CreateOrderReq{
		SkuId: skuID, IdempotencyKey: "k-sold-extra",
	})
	if code := codes.FromError(err); code != codes.CodeFlashSaleStockInsufficient {
		t.Fatalf("售罄后错误码=%d，期望 %d: %v", code, codes.CodeFlashSaleStockInsufficient, err)
	}
	if n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); err != nil || n != totalStock {
		t.Fatalf("售罄后请求数=%d，期望 %d（err=%v）", n, totalStock, err)
	}
	if !regressionExists(t, flashSaleSoldoutKey(activityID, skuID)) {
		t.Fatalf("售罄后必须写入售罄标记")
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 0 {
		t.Fatalf("售罄后 remaining=%d，期望 0", got)
	}

	// 消费落单：订单数 = 初始库存，sold = 初始库存。
	n, err := s.ConsumeQueued(ctx, 10)
	if err != nil {
		t.Fatalf("消费排队请求失败: %v", err)
	}
	if n != totalStock {
		t.Fatalf("消费处理 %d 条，期望 %d 条", n, totalStock)
	}
	if count, err := g.DB().Model("flash_sale_orders").Ctx(ctx).
		Where("activity_id", activityID).Count(); err != nil || count != totalStock {
		t.Fatalf("落单数=%d，期望 %d（err=%v）", count, totalStock, err)
	}
	_, sold := consumeBindingStock(t, activityID, skuID)
	if sold != totalStock {
		t.Fatalf("MySQL sold=%d，期望 %d", sold, totalStock)
	}
	if got := regressionRedisInt(t, flashSaleStockKey(activityID, skuID)); got != 0 {
		t.Fatalf("消费后 Redis remaining=%d，期望 0（缓存与 DB 不收敛）", got)
	}
}

// TestFlashSaleRegressionDuplicatePurchaseSingleOrder 覆盖 5.3：
// 同一用户第二次用新幂等键抢购被 12004 拦截；消费后只有 1 个订单；
// 再次用原幂等键提交命中原请求状态（不重复落单）。
func TestFlashSaleRegressionDuplicatePurchaseSingleOrder(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reg-dup")
	activityID := regressionActivity(t, skuID, 5)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("预热活动缓存失败: %v", err)
	}
	const userID = int64(895001)

	if _, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{
		SkuId: skuID, IdempotencyKey: "k-first",
	}); err != nil {
		t.Fatalf("首次抢购失败: %v", err)
	}

	_, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{
		SkuId: skuID, IdempotencyKey: "k-second",
	})
	if code := codes.FromError(err); code != codes.CodeFlashSaleAlreadyPurchased {
		t.Fatalf("同一用户换键抢购错误码=%d，期望 %d: %v", code, codes.CodeFlashSaleAlreadyPurchased, err)
	}
	if n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Where("user_id", userID).Count(); err != nil || n != 1 {
		t.Fatalf("同一用户请求数=%d，期望 1（err=%v）", n, err)
	}

	if n, err := s.ConsumeQueued(ctx, 10); err != nil || n != 1 {
		t.Fatalf("消费 n=%d err=%v，期望处理 1 条", n, err)
	}
	if count, err := g.DB().Model("flash_sale_orders").Ctx(ctx).
		Where("user_id", userID).Where("activity_id", activityID).Count(); err != nil || count != 1 {
		t.Fatalf("同一用户订单数=%d，期望 1（重复有效订单）", count)
	}

	// 原幂等键重复提交：命中既有请求（success），不再产生新请求/新订单。
	res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{
		SkuId: skuID, IdempotencyKey: "k-first",
	})
	if err != nil {
		t.Fatalf("原幂等键重复提交失败: %v", err)
	}
	if res.Status != requestStatusString(requestStatusSuccess) {
		t.Fatalf("原幂等键重复提交状态=%q，期望 success", res.Status)
	}
	if n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Where("user_id", userID).Count(); err != nil || n != 1 {
		t.Fatalf("幂等重复提交后请求数=%d，期望 1（err=%v）", n, err)
	}
	if count, err := g.DB().Model("flash_sale_orders").Ctx(ctx).
		Where("user_id", userID).Count(); err != nil || count != 1 {
		t.Fatalf("幂等重复提交后订单数=%d，期望 1", count)
	}
}

// TestFlashSaleRegressionKafkaRedeliveryConsumesOnce 覆盖 5.5：
// 直接驱动 Kafka 消费入口 ConsumeRequest（与 B3 Consumer 调用的同一业务核心），
// 同一 Request 重复投递：第一次 success 落单，第二次 duplicate 安全跳过，
// 订单数与 MySQL 扣减都只发生一次。
func TestFlashSaleRegressionKafkaRedeliveryConsumesOnce(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reg-kafka-idem")
	activityID := regressionActivity(t, skuID, 10)

	const (
		userID = int64(897001)
		key    = "k-kafka-redeliver"
	)
	hash := requestHash(activityID, skuID)
	requestID, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         userID,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": key,
		"request_hash":    hash,
		"status":          requestStatusQueued,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入排队请求失败: %v", err)
	}

	in := ConsumeRequest{
		RequestID:      requestID,
		UserID:         userID,
		ActivityID:     activityID,
		SkuID:          skuID,
		IdempotencyKey: key,
		RequestHash:    hash,
	}
	first, err := s.ConsumeRequest(ctx, in)
	if err != nil {
		t.Fatalf("首次消费失败: %v", err)
	}
	if first.Outcome != ConsumeOutcomeSuccess || first.OrderID <= 0 {
		t.Fatalf("首次消费 outcome=%s order=%d，期望 success 且已落单", first.Outcome, first.OrderID)
	}

	second, err := s.ConsumeRequest(ctx, in)
	if err != nil {
		t.Fatalf("重复消费不应返回技术错误: %v", err)
	}
	if second.Outcome != ConsumeOutcomeDuplicate {
		t.Fatalf("重复消费 outcome=%s，期望 duplicate", second.Outcome)
	}
	if second.OrderID != 0 {
		t.Fatalf("重复消费返回了订单 id=%d，期望 0（不得重复落单）", second.OrderID)
	}

	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).
		Where("activity_id", activityID).Count(); err != nil || n != 1 {
		t.Fatalf("重复投递后订单数=%d，期望 1（err=%v）", n, err)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("重复投递后 sold=%d，期望 1（重复扣减）", sold)
	}
	req := consumeRequestRow(t, userID, key)
	if req.Status != requestStatusSuccess {
		t.Fatalf("请求状态=%d，期望 success", req.Status)
	}
}
