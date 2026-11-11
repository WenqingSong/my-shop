package concurrency

// 4.3 拼团参团并发测试。
//
// 被测实现：internal/logic/groupbuy（真实 MySQL 事务）：
//   - 容量闸门：UPDATE group_buy_teams SET joined_count = joined_count + 1
//     WHERE id=? AND status=10 AND joined_count < required_size AND expire_at > NOW()
//   - 唯一约束：uk_team_user(team_id,user_id)、uk_order_id(order_id)、
//     orders.uk_user_idempotency(user_id,idempotency_key)
//   - 成团推进：status=20 WHERE status=10 AND joined_count >= required_size（只命中一次）
//
// 本文件验证的业务不变量：
//   GB-1 不允许超员：joined_count ≤ required_size，参与记录数 == joined_count；
//   GB-2 同一用户不能重复参团：重复请求命中幂等快路径，不新增参与记录/订单；
//   GB-3 同一订单不能重复占名额：订单创建失败（幂等键冲突）时整体回滚，名额不泄漏；
//   GB-4 团状态只允许 10→20 一次，且成团时 closed_at 非空；
//   GB-5 拼团不拥有库存，但下单扣库存与参与记录必须一一对应（不重复扣减）。

import (
	"context"
	"fmt"
	"testing"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	groupbuyv1 "cnb.cool/go-cloud-devops/my-shop/api/groupbuy/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// gbActivity 建立启用且时间窗内的拼团活动，返回活动 id。
// 时间窗用 MySQL NOW() 计算，避免 Go↔MySQL 时区漂移导致活动被判为未开始。
func gbActivity(t *testing.T, productID, skuID, groupPrice, groupSize int64) int64 {
	t.Helper()
	activityID, err := g.DB().Model("group_buy_activities").Ctx(context.Background()).Data(g.Map{
		"product_id":       productID,
		"sku_id":           skuID,
		"name":             "拼团并发-" + uniqueSuffix(),
		"group_price":      groupPrice,
		"group_size":       groupSize,
		"team_ttl_minutes": 30,
		"start_time":       mysqlNowOffset(-60),
		"end_time":         mysqlNowOffset(60),
		"status":           1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入拼团活动失败: %v", err)
	}
	return activityID
}

// mysqlNowOffset 返回「相对数据库 NOW() 偏移 minutes 分钟」的 SQL 表达式。
// 用数据库时间而非 Go 时间，避免 Go↔MySQL 时区漂移导致活动/团被判为不在时间窗内。
func mysqlNowOffset(minutes int) gdb.Raw {
	if minutes < 0 {
		return gdb.Raw(fmt.Sprintf("DATE_SUB(NOW(), INTERVAL %d MINUTE)", -minutes))
	}
	return gdb.Raw(fmt.Sprintf("DATE_ADD(NOW(), INTERVAL %d MINUTE)", minutes))
}

// teamState 是 group_buy_teams 的容量快照。
type teamState struct {
	Id           int64       `json:"id"`
	Status       int         `json:"status"`
	JoinedCount  int64       `json:"joined_count"`
	RequiredSize int64       `json:"required_size"`
	ClosedAt     *gtime.Time `json:"closed_at"`
}

// loadTeamState 读取团状态。
func loadTeamState(t *testing.T, teamID int64) teamState {
	t.Helper()
	var rows []*teamState
	if err := g.DB().Model("group_buy_teams").Ctx(context.Background()).
		Fields("id", "status", "joined_count", "required_size", "closed_at").
		Where("id", teamID).Scan(&rows); err != nil {
		t.Fatalf("查询团状态失败: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("团 %d 不存在", teamID)
	}
	return *rows[0]
}

// countParticipants 统计团的有效参与记录数（status=10）。
func countParticipants(t *testing.T, teamID int64) int {
	t.Helper()
	n, err := g.DB().Model("group_buy_participants").Ctx(context.Background()).
		Where("team_id", teamID).Where("status", 10).Count()
	if err != nil {
		t.Fatalf("统计参与记录失败: %v", err)
	}
	return n
}

// countOrdersByIDs 统计给定订单 id 中真实存在的订单数。
func countOrdersByIDs(t *testing.T, ids []int64) int {
	t.Helper()
	if len(ids) == 0 {
		return 0
	}
	n, err := g.DB().Model("orders").Ctx(context.Background()).WhereIn("id", ids).Count()
	if err != nil {
		t.Fatalf("统计订单失败: %v", err)
	}
	return n
}

// teamOrderIDs 返回团内参与记录关联的全部订单 id。
func teamOrderIDs(t *testing.T, teamID int64) []int64 {
	t.Helper()
	values, err := g.DB().Model("group_buy_participants").Ctx(context.Background()).
		Fields("order_id").Where("team_id", teamID).Array()
	if err != nil {
		t.Fatalf("查询参与订单失败: %v", err)
	}
	ids := make([]int64, 0, len(values))
	seen := map[int64]bool{}
	for _, v := range values {
		id := v.Int64()
		if id <= 0 {
			t.Fatalf("参与记录存在空订单 id（订单与参与记录不一致）")
		}
		if seen[id] {
			t.Fatalf("同一订单 %d 被多条参与记录占用（uk_order_id 失效）", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// TestGroupBuyConcurrentJoinNoOverCapacity 覆盖 GB-1/GB-4/GB-5：
// 团上限 3 人（团长已占 1），10 个不同用户并发抢 2 个剩余名额：
// 只有 2 次成功，团人数恰好 3、成团一次、参与记录与订单一一对应、库存只扣 3 件。
func TestGroupBuyConcurrentJoinNoOverCapacity(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		groupSize    = 3
		initialStock = 50
		joiners      = 10
	)
	f := seedCatalog(t, "gb-capacity", 10000, initialStock)
	activityID := gbActivity(t, f.ProductID, f.SkuID, 8000, groupSize)

	leaderID := newTestUser(t, "gb-leader")
	leaderAddr := newTestAddress(t, leaderID)
	created, err := service.GroupBuy().CreateTeam(ctx, leaderID, &groupbuyv1.CreateTeamReq{
		Id: activityID, AddressId: leaderAddr, IdempotencyKey: "gb-leader-key",
	})
	if err != nil {
		t.Fatalf("开团失败: %v", err)
	}
	teamID := created.Id
	if created.JoinedCount != 1 {
		t.Fatalf("开团后人数=%d，期望 1", created.JoinedCount)
	}

	users := make([]int64, joiners)
	addrs := make([]int64, joiners)
	for i := 0; i < joiners; i++ {
		users[i] = newTestUser(t, "gb-joiner")
		addrs[i] = newTestAddress(t, users[i])
	}

	errs := make([]error, joiners)
	runConcurrently(joiners, func(i int) {
		_, e := service.GroupBuy().JoinTeam(ctx, users[i], &groupbuyv1.JoinTeamReq{
			Id: teamID, AddressId: addrs[i], IdempotencyKey: fmt.Sprintf("gb-join-%d", i),
		})
		errs[i] = e
	})

	var joined int
	for i, err := range errs {
		if err == nil {
			joined++
			continue
		}
		if code := codes.FromError(err); code != codes.CodeGroupBuyTeamNotJoinable {
			t.Fatalf("第 %d 个参团请求非预期错误码 %d: %v", i, code, err)
		}
	}
	if joined != groupSize-1 {
		t.Fatalf("成功参团 %d 人，期望恰好 %d 人（超员或少成团）", joined, groupSize-1)
	}

	state := loadTeamState(t, teamID)
	if state.JoinedCount != groupSize {
		t.Fatalf("最终人数=%d，期望 %d（超员）", state.JoinedCount, groupSize)
	}
	if state.JoinedCount > state.RequiredSize {
		t.Fatalf("joined_count=%d 超过 required_size=%d", state.JoinedCount, state.RequiredSize)
	}
	if state.Status != 20 {
		t.Fatalf("团状态=%d，期望 20（已成团）", state.Status)
	}
	if state.ClosedAt == nil {
		t.Fatalf("成团后 closed_at 为空")
	}

	if n := countParticipants(t, teamID); n != groupSize {
		t.Fatalf("参与记录=%d，期望 %d（人数与记录不一致）", n, groupSize)
	}
	orderIDs := teamOrderIDs(t, teamID)
	if len(orderIDs) != groupSize {
		t.Fatalf("参与订单=%d，期望 %d", len(orderIDs), groupSize)
	}
	if n := countOrdersByIDs(t, orderIDs); n != groupSize {
		t.Fatalf("真实订单=%d，期望 %d（参与记录指向不存在的订单）", n, groupSize)
	}

	// 拼团一人一份：库存只应扣 groupSize 件，失败请求不得扣库存。
	inv, err := service.Inventory().Get(ctx, f.SkuID)
	if err != nil {
		t.Fatalf("查询库存失败: %v", err)
	}
	if want := int64(initialStock - groupSize); inv.Quantity != want {
		t.Fatalf("剩余库存=%d，期望 %d（失败参团造成了多余扣减）", inv.Quantity, want)
	}
}

// TestGroupBuyConcurrentDuplicateJoinSingleParticipant 覆盖 GB-2：
// 同一用户对同一团并发提交 10 次，只允许 1 条参与记录 + 1 个订单，人数只 +1。
func TestGroupBuyConcurrentDuplicateJoinSingleParticipant(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		groupSize    = 5
		initialStock = 50
		attempts     = 10
	)
	f := seedCatalog(t, "gb-dup", 10000, initialStock)
	activityID := gbActivity(t, f.ProductID, f.SkuID, 7000, groupSize)

	leaderID := newTestUser(t, "gb-dup-leader")
	leaderAddr := newTestAddress(t, leaderID)
	created, err := service.GroupBuy().CreateTeam(ctx, leaderID, &groupbuyv1.CreateTeamReq{
		Id: activityID, AddressId: leaderAddr, IdempotencyKey: "gb-dup-leader-key",
	})
	if err != nil {
		t.Fatalf("开团失败: %v", err)
	}
	teamID := created.Id

	userID := newTestUser(t, "gb-dup-joiner")
	addressID := newTestAddress(t, userID)

	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		_, e := service.GroupBuy().JoinTeam(ctx, userID, &groupbuyv1.JoinTeamReq{
			Id: teamID, AddressId: addressID, IdempotencyKey: "gb-dup-same-user",
		})
		errs[i] = e
	})
	for i, err := range errs {
		// 三种正常结果，均不产生第二次参与：
		//   1) nil：命中「已参与」幂等快路径，返回既有团详情；
		//   2) 25007：并发撞 uk_team_user，整体回滚；
		//   3) 9006：并发窗口内第二次下单复用同一幂等键，撞 uk_user_idempotency 后整体回滚。
		// 第 3 种是并发重复提交的已知表现（非幂等成功，但副作用为零），记录在测试报告。
		if err == nil {
			continue
		}
		if code := codes.FromError(err); code != codes.CodeGroupBuyAlreadyJoined &&
			code != codes.CodeOrderIdempotencyConflict {
			t.Fatalf("第 %d 个重复参团请求非预期错误码 %d: %v", i, code, err)
		}
	}

	state := loadTeamState(t, teamID)
	if state.JoinedCount != 2 {
		t.Fatalf("重复参团后人数=%d，期望 2（同一用户占用了多个名额）", state.JoinedCount)
	}
	if n := countParticipants(t, teamID); n != 2 {
		t.Fatalf("参与记录=%d，期望 2（团长 + 该用户）", n)
	}
	orderIDs := teamOrderIDs(t, teamID)
	if len(orderIDs) != 2 {
		t.Fatalf("订单数=%d，期望 2（同一用户被重复下单）", len(orderIDs))
	}
	if inv, err := service.Inventory().Get(ctx, f.SkuID); err != nil {
		t.Fatalf("查询库存失败: %v", err)
	} else if want := int64(initialStock - 2); inv.Quantity != want {
		t.Fatalf("剩余库存=%d，期望 %d（重复参团导致重复扣库存）", inv.Quantity, want)
	}
}

// TestGroupBuyJoinOrderConflictRollsBackCapacity 覆盖 GB-3：
// 同一用户用同一幂等键参团两个不同的团：第二个团的订单创建必然失败（uk_user_idempotency），
// 此时「已占用的名额」必须随事务整体回滚，不得留下参与记录或虚增人数。
func TestGroupBuyJoinOrderConflictRollsBackCapacity(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		groupSize    = 3
		initialStock = 50
	)
	f := seedCatalog(t, "gb-rollback", 10000, initialStock)
	activityID := gbActivity(t, f.ProductID, f.SkuID, 6000, groupSize)

	leaderA := newTestUser(t, "gb-rb-leader-a")
	teamA, err := service.GroupBuy().CreateTeam(ctx, leaderA, &groupbuyv1.CreateTeamReq{
		Id: activityID, AddressId: newTestAddress(t, leaderA), IdempotencyKey: "gb-rb-a-key",
	})
	if err != nil {
		t.Fatalf("开团 A 失败: %v", err)
	}
	leaderB := newTestUser(t, "gb-rb-leader-b")
	teamB, err := service.GroupBuy().CreateTeam(ctx, leaderB, &groupbuyv1.CreateTeamReq{
		Id: activityID, AddressId: newTestAddress(t, leaderB), IdempotencyKey: "gb-rb-b-key",
	})
	if err != nil {
		t.Fatalf("开团 B 失败: %v", err)
	}

	userID := newTestUser(t, "gb-rb-joiner")
	addressID := newTestAddress(t, userID)
	const sharedKey = "gb-rb-shared-key"

	if _, err := service.GroupBuy().JoinTeam(ctx, userID, &groupbuyv1.JoinTeamReq{
		Id: teamA.Id, AddressId: addressID, IdempotencyKey: sharedKey,
	}); err != nil {
		t.Fatalf("首次参团 A 失败: %v", err)
	}
	beforeB := loadTeamState(t, teamB.Id)

	_, err = service.GroupBuy().JoinTeam(ctx, userID, &groupbuyv1.JoinTeamReq{
		Id: teamB.Id, AddressId: addressID, IdempotencyKey: sharedKey,
	})
	if code := codes.FromError(err); code != codes.CodeOrderIdempotencyConflict {
		t.Fatalf("复用幂等键参团 B 的错误码=%d，期望 %d（订单幂等冲突）: %v",
			code, codes.CodeOrderIdempotencyConflict, err)
	}

	afterB := loadTeamState(t, teamB.Id)
	if afterB.JoinedCount != beforeB.JoinedCount {
		t.Fatalf("失败事务虚增了名额：%d → %d", beforeB.JoinedCount, afterB.JoinedCount)
	}
	if afterB.Status != 10 {
		t.Fatalf("失败事务改变了团状态：%d，期望 10（拼团中）", afterB.Status)
	}
	if n := countParticipants(t, teamB.Id); n != 1 {
		t.Fatalf("失败事务留下了参与记录：团 B 参与数=%d，期望 1（仅团长）", n)
	}

	// 团 A 仍成立：人数 2、参与记录 2、订单恰好 2 个（无重复占用）。
	stateA := loadTeamState(t, teamA.Id)
	if stateA.JoinedCount != 2 || stateA.Status != 10 {
		t.Fatalf("团 A 状态异常: joined=%d status=%d，期望 2/10", stateA.JoinedCount, stateA.Status)
	}
	if n := countParticipants(t, teamA.Id); n != 2 {
		t.Fatalf("团 A 参与记录=%d，期望 2", n)
	}
	// 库存应为「团长 A + 用户 + 团长 B」共 3 件（失败的团 B 参团未扣库存）。
	if inv, err := service.Inventory().Get(ctx, f.SkuID); err != nil {
		t.Fatalf("查询库存失败: %v", err)
	} else if want := int64(initialStock - 3); inv.Quantity != want {
		t.Fatalf("剩余库存=%d，期望 %d（失败事务未回滚库存扣减）", inv.Quantity, want)
	}
}
