package concurrency

// 4.4 砍价助力并发测试。
//
// 被测实现：internal/logic/bargain（真实 MySQL 事务）：
//   - Start：单语句 INSERT ... SELECT 快照活动价格；active_key 生成列 + UNIQUE 保证
//     「同一用户同一活动至多一个进行中实例」；
//   - Help：SELECT ... FOR UPDATE 锁实例 → 全量条件 UPDATE（status/次数/底价/未过期）
//     → 插入助力记录（uk_instance_helper 兜底）→ 达底价置成功；任一步失败整体回滚。
//
// 本文件验证的业务不变量：
//   BG-1 同一用户同一活动只能有一个进行中实例（active_key 唯一）；
//   BG-2 同一助手对同一实例只能成功助力一次；
//   BG-3 累计砍价金额不得突破底价（current_price ≥ floor_price，永不为负）；
//   BG-4 助力记录与价格推进一致：每条 price_after = price_before - cut_amount，
//        且相邻记录首尾相接，Σcut_amount == start_price - current_price；
//   BG-5 终态（成功）后不再接受助力，且状态只推进一次。
//
// 注意：测试代码从不写入 active_key（STORED 生成列），只在 SELECT 中读取校验。

import (
	"context"
	"fmt"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// bgActivity 建立启用且时间窗内的砍价活动，返回活动 id。
func bgActivity(t *testing.T, productID, skuID, startPrice, floorPrice, helpAmount, maxHelp int64) int64 {
	t.Helper()
	activityID, err := g.DB().Model("bargain_activities").Ctx(context.Background()).Data(g.Map{
		"product_id":     productID,
		"sku_id":         skuID,
		"name":           "砍价并发-" + uniqueSuffix(),
		"start_price":    startPrice,
		"floor_price":    floorPrice,
		"help_amount":    helpAmount,
		"max_help_count": maxHelp,
		"start_time":     mysqlNowOffset(-60),
		"end_time":       mysqlNowOffset(120),
		"status":         1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入砍价活动失败: %v", err)
	}
	return activityID
}

// bargainInstanceRow 是 bargain_instances 的一致性快照（active_key 只读）。
type bargainInstanceRow struct {
	Id           int64  `json:"id"`
	UserId       int64  `json:"user_id"`
	ActivityId   int64  `json:"activity_id"`
	StartPrice   int64  `json:"start_price"`
	FloorPrice   int64  `json:"floor_price"`
	CurrentPrice int64  `json:"current_price"`
	HelpedCount  int64  `json:"helped_count"`
	MaxHelpCount int64  `json:"max_help_count"`
	Status       int    `json:"status"`
	ActiveKey    string `json:"active_key"`
}

// loadBargainInstance 读取砍价实例快照。
func loadBargainInstance(t *testing.T, instanceID int64) bargainInstanceRow {
	t.Helper()
	var rows []*bargainInstanceRow
	if err := g.DB().Model("bargain_instances").Ctx(context.Background()).
		Fields("id", "user_id", "activity_id", "start_price", "floor_price", "current_price",
			"helped_count", "max_help_count", "status", "active_key").
		Where("id", instanceID).Scan(&rows); err != nil {
		t.Fatalf("查询砍价实例失败: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("砍价实例 %d 不存在", instanceID)
	}
	return *rows[0]
}

// bargainHelpRow 是 bargain_help_records 的一致性快照。
type bargainHelpRow struct {
	Id           int64 `json:"id"`
	HelperUserId int64 `json:"helper_user_id"`
	CutAmount    int64 `json:"cut_amount"`
	PriceBefore  int64 `json:"price_before"`
	PriceAfter   int64 `json:"price_after"`
}

// loadBargainHelps 读取实例的全部助力记录（id 升序）。
func loadBargainHelps(t *testing.T, instanceID int64) []*bargainHelpRow {
	t.Helper()
	var rows []*bargainHelpRow
	if err := g.DB().Model("bargain_help_records").Ctx(context.Background()).
		Fields("id", "helper_user_id", "cut_amount", "price_before", "price_after").
		Where("instance_id", instanceID).Order("id ASC").Scan(&rows); err != nil {
		t.Fatalf("查询助力记录失败: %v", err)
	}
	return rows
}

// assertBargainHelpChain 校验助力记录与价格推进自洽，返回累计砍减金额。
func assertBargainHelpChain(t *testing.T, rows []*bargainHelpRow, startPrice, floorPrice int64) int64 {
	t.Helper()
	var totalCut int64
	prevAfter := startPrice
	seenHelper := map[int64]bool{}
	for _, r := range rows {
		if r.CutAmount <= 0 {
			t.Fatalf("助力记录 %d 的 cut_amount=%d 必须为正", r.Id, r.CutAmount)
		}
		if seenHelper[r.HelperUserId] {
			t.Fatalf("助手 %d 在同一实例上出现了多条助力记录", r.HelperUserId)
		}
		seenHelper[r.HelperUserId] = true
		if r.PriceBefore != prevAfter {
			t.Fatalf("助力记录 %d 断链：price_before=%d，上一条 price_after=%d", r.Id, r.PriceBefore, prevAfter)
		}
		if r.PriceAfter != r.PriceBefore-r.CutAmount {
			t.Fatalf("助力记录 %d 金额不自洽：%d - %d != %d",
				r.Id, r.PriceBefore, r.CutAmount, r.PriceAfter)
		}
		if r.PriceAfter < floorPrice {
			t.Fatalf("助力记录 %d 砍穿底价：price_after=%d < floor_price=%d", r.Id, r.PriceAfter, floorPrice)
		}
		if r.PriceAfter < 0 {
			t.Fatalf("助力记录 %d 出现负价格: %d", r.Id, r.PriceAfter)
		}
		prevAfter = r.PriceAfter
		totalCut += r.CutAmount
	}
	return totalCut
}

// TestBargainConcurrentStartSingleActiveInstance 覆盖 BG-1：
// 同一用户对同一活动并发发起 8 次砍价，只有 1 次成功，DB 只留 1 个进行中实例，
// 且 active_key（生成列）= "activity_id:user_id" 由数据库生成。
func TestBargainConcurrentStartSingleActiveInstance(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	f := seedCatalog(t, "bg-start", 10000, 0)
	activityID := bgActivity(t, f.ProductID, f.SkuID, 10000, 1000, 500, 10)
	userID := newTestUser(t, "bg-start-owner")

	const attempts = 8
	errs := make([]error, attempts)
	instanceIDs := make([]int64, attempts)
	runConcurrently(attempts, func(i int) {
		res, err := service.Bargain().Start(ctx, userID, activityID)
		errs[i] = err
		if err == nil && res != nil {
			instanceIDs[i] = res.Id
		}
	})

	var (
		successes  int
		instanceID int64
	)
	for i, err := range errs {
		if err == nil {
			successes++
			if instanceIDs[i] <= 0 {
				t.Fatalf("第 %d 次成功发起但未返回实例 id", i)
			}
			instanceID = instanceIDs[i]
			continue
		}
		if code := codes.FromError(err); code != codes.CodeBargainInstanceExists {
			t.Fatalf("第 %d 次发起砍价非预期错误码 %d: %v", i, code, err)
		}
	}
	if successes != 1 {
		t.Fatalf("并发发起成功 %d 次，期望恰好 1 次（active_key 唯一约束失效）", successes)
	}

	n, err := g.DB().Model("bargain_instances").Ctx(ctx).
		Where("user_id", userID).Where("activity_id", activityID).Where("status", 10).Count()
	if err != nil {
		t.Fatalf("统计进行中实例失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("进行中实例=%d，期望 1", n)
	}

	inst := loadBargainInstance(t, instanceID)
	wantKey := fmt.Sprintf("%d:%d", activityID, userID)
	if inst.ActiveKey != wantKey {
		t.Fatalf("active_key=%q，期望 %q（生成列语义不符）", inst.ActiveKey, wantKey)
	}
	if inst.CurrentPrice != 10000 || inst.HelpedCount != 0 || inst.Status != 10 {
		t.Fatalf("新实例初始状态异常: price=%d helped=%d status=%d", inst.CurrentPrice, inst.HelpedCount, inst.Status)
	}
}

// TestBargainConcurrentHelpOncePerHelper 覆盖 BG-2：
// 同一助手对同一实例并发助力 10 次，只成功 1 次，价格只砍一次，助力记录只 1 条。
func TestBargainConcurrentHelpOncePerHelper(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		startPrice = int64(10000)
		floorPrice = int64(1000)
		helpAmount = int64(500)
		attempts   = 10
	)
	f := seedCatalog(t, "bg-dup", 12000, 0)
	activityID := bgActivity(t, f.ProductID, f.SkuID, startPrice, floorPrice, helpAmount, 10)

	ownerID := newTestUser(t, "bg-dup-owner")
	ownerRes, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价失败: %v", err)
	}
	instanceID := ownerRes.Id
	helperID := newTestUser(t, "bg-dup-helper")

	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		_, e := service.Bargain().Help(ctx, helperID, instanceID)
		errs[i] = e
	})

	var successes int
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if code := codes.FromError(err); code != codes.CodeBargainHelpDuplicated {
			t.Fatalf("第 %d 次重复助力非预期错误码 %d: %v", i, code, err)
		}
	}
	if successes != 1 {
		t.Fatalf("同一助手助力成功 %d 次，期望恰好 1 次（重复计算砍价金额）", successes)
	}

	inst := loadBargainInstance(t, instanceID)
	if inst.HelpedCount != 1 {
		t.Fatalf("helped_count=%d，期望 1", inst.HelpedCount)
	}
	if want := startPrice - helpAmount; inst.CurrentPrice != want {
		t.Fatalf("current_price=%d，期望 %d（只应砍一次）", inst.CurrentPrice, want)
	}
	rows := loadBargainHelps(t, instanceID)
	if len(rows) != 1 {
		t.Fatalf("助力记录=%d 条，期望 1 条", len(rows))
	}
	if total := assertBargainHelpChain(t, rows, startPrice, floorPrice); total != helpAmount {
		t.Fatalf("累计砍减=%d，期望 %d", total, helpAmount)
	}
}

// TestBargainConcurrentHelpersPriceNeverBelowFloor 覆盖 BG-3/BG-4/BG-5：
// 20 个不同助手并发助力，每次名义砍 300，底价 1000：
// 价格依次 2000→1700→1400→1100→1000 后置成功，后续助力一律拒绝；
// 最终价格恰好等于底价，助力记录与价格推进严格一致，状态成功只推进一次。
func TestBargainConcurrentHelpersPriceNeverBelowFloor(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		startPrice = int64(2000)
		floorPrice = int64(1000)
		helpAmount = int64(300)
		helpers    = 20
	)
	f := seedCatalog(t, "bg-floor", 3000, 0)
	activityID := bgActivity(t, f.ProductID, f.SkuID, startPrice, floorPrice, helpAmount, 50)

	ownerID := newTestUser(t, "bg-floor-owner")
	ownerRes, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价失败: %v", err)
	}
	instanceID := ownerRes.Id

	helperIDs := make([]int64, helpers)
	for i := 0; i < helpers; i++ {
		helperIDs[i] = newTestUser(t, "bg-floor-helper")
	}
	errs := make([]error, helpers)
	runConcurrently(helpers, func(i int) {
		_, e := service.Bargain().Help(ctx, helperIDs[i], instanceID)
		errs[i] = e
	})

	var successes int
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if code := codes.FromError(err); code != codes.CodeBargainHelpUnavailable {
			t.Fatalf("第 %d 个助力请求非预期错误码 %d: %v", i, code, err)
		}
	}
	// (2000-1000)/300 = 3 次整砍 + 1 次补足差额 = 4 次成功。
	if successes != 4 {
		t.Fatalf("成功助力 %d 次，期望 4 次（底价前恰好用尽可砍差额）", successes)
	}

	inst := loadBargainInstance(t, instanceID)
	if inst.CurrentPrice != floorPrice {
		t.Fatalf("最终价格=%d，期望恰好底价 %d（砍穿底价或少砍）", inst.CurrentPrice, floorPrice)
	}
	if inst.CurrentPrice < 0 {
		t.Fatalf("价格出现负数: %d", inst.CurrentPrice)
	}
	if inst.Status != 20 {
		t.Fatalf("实例状态=%d，期望 20（砍价成功）", inst.Status)
	}
	if inst.HelpedCount != 4 {
		t.Fatalf("helped_count=%d，期望 4", inst.HelpedCount)
	}
	if inst.ActiveKey != "" {
		t.Fatalf("成功实例的 active_key 应为 NULL，实际 %q（应释放唯一名额）", inst.ActiveKey)
	}

	rows := loadBargainHelps(t, instanceID)
	if len(rows) != 4 {
		t.Fatalf("助力记录=%d 条，期望 4 条", len(rows))
	}
	if total := assertBargainHelpChain(t, rows, startPrice, floorPrice); total != startPrice-floorPrice {
		t.Fatalf("累计砍减=%d，期望 %d（记录与价格不一致）", total, startPrice-floorPrice)
	}
}

// TestBargainActiveKeyReleasedAfterAbandon 覆盖 BG-1/BG-5 的生成列语义：
// 放弃后 active_key 变 NULL（允许重新发起），历史实例保留；同一活动同一用户最终
// 只有 1 个 active_key 非空的实例，且该列始终由数据库生成（测试从不写入）。
func TestBargainActiveKeyReleasedAfterAbandon(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	f := seedCatalog(t, "bg-abandon", 8000, 0)
	activityID := bgActivity(t, f.ProductID, f.SkuID, 8000, 800, 400, 10)
	userID := newTestUser(t, "bg-abandon-owner")

	first, err := service.Bargain().Start(ctx, userID, activityID)
	if err != nil {
		t.Fatalf("首次发起砍价失败: %v", err)
	}
	if _, err := service.Bargain().Abandon(ctx, userID, first.Id); err != nil {
		t.Fatalf("放弃砍价失败: %v", err)
	}
	abandoned := loadBargainInstance(t, first.Id)
	if abandoned.Status != 30 {
		t.Fatalf("放弃后状态=%d，期望 30（失败）", abandoned.Status)
	}
	if abandoned.ActiveKey != "" {
		t.Fatalf("放弃后 active_key 应为 NULL，实际 %q", abandoned.ActiveKey)
	}

	second, err := service.Bargain().Start(ctx, userID, activityID)
	if err != nil {
		t.Fatalf("放弃后重新发起失败（active_key 未释放）: %v", err)
	}
	if second.Id == first.Id {
		t.Fatalf("重新发起复用了历史实例 id %d", first.Id)
	}

	activeCount, err := g.DB().Model("bargain_instances").Ctx(ctx).
		Where("user_id", userID).Where("activity_id", activityID).
		WhereNotNull("active_key").Count()
	if err != nil {
		t.Fatalf("统计 active_key 非空实例失败: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active_key 非空实例=%d，期望 1", activeCount)
	}

	// 生成列语义确认：active_key 是 STORED GENERATED，只能由数据库维护。
	extra, err := g.DB().GetValue(ctx,
		"SELECT EXTRA FROM information_schema.columns WHERE table_schema = DATABASE() "+
			"AND table_name = 'bargain_instances' AND column_name = 'active_key'")
	if err != nil {
		t.Fatalf("查询 active_key 列属性失败: %v", err)
	}
	if got := extra.String(); got == "" || !containsFold(got, "STORED GENERATED") {
		t.Fatalf("active_key 列属性=%q，期望包含 STORED GENERATED", got)
	}
}
