package bargain_test

// 本文件是砍价业务的真实 MySQL 集成测试：不使用 Mock，并发不变量由数据库行锁与唯一索引证明。
//
// 环境要求：一个与其它任务隔离的 MySQL 8.0 实例（默认 127.0.0.1:13307）。
// 可经 BARGAIN_TEST_MYSQL_{HOST,PORT,USER,PASS,NAME} 覆盖；默认库名 my_shop_bargain_test。
// 测试自行执行 migrations.Up 建表，并在每个用例前后清空砍价/商品/用户相关表。
//
// 运行：go test ./internal/logic/bargain/ -run TestBargain -v

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/bargain/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	defaultTestMySQLHost = "127.0.0.1"
	defaultTestMySQLPort = "13307"
	defaultTestMySQLUser = "root"
	defaultTestMySQLPass = "bargain-test-pass"
	defaultTestMySQLName = "my_shop_bargain_test"
)

// cleanupTables 按外键依赖顺序清空本任务涉及的表（先子后父）。
var cleanupTables = []string{
	"bargain_help_records", "bargain_instances", "bargain_activities",
	"skus", "product_images", "products", "categories", "users",
}

// setupBargainDB 连接隔离 MySQL、执行 migration、注册 gdb 配置并清空业务表。
func setupBargainDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	host := envOr("BARGAIN_TEST_MYSQL_HOST", defaultTestMySQLHost)
	port := envOr("BARGAIN_TEST_MYSQL_PORT", defaultTestMySQLPort)
	user := envOr("BARGAIN_TEST_MYSQL_USER", defaultTestMySQLUser)
	pass := envOr("BARGAIN_TEST_MYSQL_PASS", defaultTestMySQLPass)
	name := envOr("BARGAIN_TEST_MYSQL_NAME", defaultTestMySQLName)

	t.Setenv("DATABASE_DEFAULT_TYPE", "mysql")
	t.Setenv("DATABASE_DEFAULT_HOST", host)
	t.Setenv("DATABASE_DEFAULT_PORT", port)
	t.Setenv("DATABASE_DEFAULT_USER", user)
	t.Setenv("DATABASE_DEFAULT_PASS", pass)
	t.Setenv("DATABASE_DEFAULT_NAME", name)
	t.Setenv("DATABASE_DEFAULT_CHARSET", "utf8mb4")

	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up（需要隔离 MySQL %s:%s/%s）: %v", host, port, name, err)
	}

	node := gdb.ConfigNode{
		Type:     "mysql",
		Host:     host,
		Port:     port,
		User:     user,
		Pass:     pass,
		Name:     name,
		Charset:  "utf8mb4",
		Protocol: "tcp",
		// 会话时区与 Go 进程本地时区一致，避免 NOW() 与 Go 侧时间比较漂移。
		Extra: localTimeZoneExtra(),
	}
	if err := gdb.SetConfigGroup(gdb.DefaultGroupName, gdb.ConfigGroup{node}); err != nil {
		t.Fatalf("注册 gdb 配置: %v", err)
	}

	truncateAll(t)
	t.Cleanup(func() {
		truncateAll(t)
		_ = g.DB().Close(context.Background())
	})
}

// localTimeZoneExtra 按 Go 进程本地时区生成 MySQL 会话 time_zone DSN 参数（与 boot 的实现同形）。
func localTimeZoneExtra() string {
	_, offset := time.Now().Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	tz := fmt.Sprintf("%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
	return "time_zone=%27" + url.QueryEscape(tz) + "%27"
}

func truncateAll(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, table := range cleanupTables {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("清空 %s: %v", table, err)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// createProductSku 创建「分类 → 商品 → SKU」最小目录数据，返回商品/SKU id。
func createProductSku(t *testing.T, price int64) (productID, skuID int64) {
	t.Helper()
	ctx := context.Background()

	categoryID, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{
		"name": fmt.Sprintf("砍价分类-%d", time.Now().UnixNano()), "parent_id": 0, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建分类: %v", err)
	}
	productID, err = g.DB().Model("products").Ctx(ctx).Data(g.Map{
		"name":        fmt.Sprintf("砍价商品-%d", time.Now().UnixNano()),
		"category_id": categoryID,
		"price":       price,
		"main_image":  "https://example.test/bargain.png",
		"status":      1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建商品: %v", err)
	}
	skuID, err = g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"product_id": productID,
		"name":       "默认规格",
		"price":      price,
		"status":     1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建 SKU: %v", err)
	}
	return productID, skuID
}

// userSeq 为测试用户提供进程内唯一序号（users.username 上限 24 字符）。
var userSeq atomic.Int64

// createUsers 创建 n 个用户（username 唯一），返回 id 列表。
func createUsers(t *testing.T, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		id, err := g.DB().Model("users").Ctx(ctx).Data(g.Map{
			"username":      fmt.Sprintf("bgu%d_%d", userSeq.Add(1), i),
			"password_hash": "$2a$10$abcdefghijklmnopqrstuv",
			"nickname":      fmt.Sprintf("助力好友%d", i),
		}).InsertAndGetId()
		if err != nil {
			t.Fatalf("创建用户: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// insertActivity 直接写活动行（绕过后台校验），用于构造停用/已结束等边界窗口。
// startOffset/endOffset 为相对数据库 NOW() 的分钟数。
func insertActivity(t *testing.T, productID, skuID, startPrice, floorPrice, helpAmount, maxHelpCount, startOffset, endOffset, status int64) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := g.DB().Model("bargain_activities").Ctx(ctx).Data(g.Map{
		"product_id":     productID,
		"sku_id":         skuID,
		"name":           "集成测试砍价活动",
		"start_price":    startPrice,
		"floor_price":    floorPrice,
		"help_amount":    helpAmount,
		"max_help_count": maxHelpCount,
		"start_time":     gdb.Raw(fmt.Sprintf("DATE_ADD(NOW(), INTERVAL %d MINUTE)", startOffset)),
		"end_time":       gdb.Raw(fmt.Sprintf("DATE_ADD(NOW(), INTERVAL %d MINUTE)", endOffset)),
		"status":         status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建砍价活动: %v", err)
	}
	return id
}

// createRunningActivity 创建一个当前处于时间窗内的启用活动。
func createRunningActivity(t *testing.T, startPrice, floorPrice, helpAmount, maxHelpCount int64) (productID, skuID, activityID int64) {
	t.Helper()
	productID, skuID = createProductSku(t, startPrice)
	activityID = insertActivity(t, productID, skuID, startPrice, floorPrice, helpAmount, maxHelpCount, -60, 120, 1)
	return productID, skuID, activityID
}

// instanceSnapshot 是实例的关键列快照，直接读库断言（不经过 API 序列化）。
type instanceSnapshot struct {
	Id           int64       `json:"id"`
	ActivityId   int64       `json:"activity_id"`
	UserId       int64       `json:"user_id"`
	StartPrice   int64       `json:"start_price"`
	FloorPrice   int64       `json:"floor_price"`
	CurrentPrice int64       `json:"current_price"`
	HelpedCount  int64       `json:"helped_count"`
	MaxHelpCount int64       `json:"max_help_count"`
	Status       int         `json:"status"`
	CompletedAt  *gtime.Time `json:"completed_at"`
	OrderId      *int64      `json:"order_id"`
	ActiveKey    *string     `json:"active_key"`
}

func loadInstance(t *testing.T, id int64) instanceSnapshot {
	t.Helper()
	var row instanceSnapshot
	if err := g.DB().Model("bargain_instances").Ctx(context.Background()).Where("id", id).Scan(&row); err != nil {
		t.Fatalf("读取砍价实例 %d: %v", id, err)
	}
	if row.Id == 0 {
		t.Fatalf("砍价实例 %d 不存在", id)
	}
	return row
}

func countRows(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	m := g.DB().Model(table).Ctx(context.Background())
	if where != "" {
		m = m.Where(where, args...)
	}
	n, err := m.Count()
	if err != nil {
		t.Fatalf("统计 %s: %v", table, err)
	}
	return n
}

// requireCode 断言错误码；err 为 nil 时直接失败。
func requireCode(t *testing.T, err error, want codes.Code, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望错误码 %d，实际成功", op, want)
	}
	if got := codes.FromError(err); got != want {
		t.Fatalf("%s: 错误码 = %d, want %d（err=%v）", op, got, want, err)
	}
}

// runConcurrently 并发执行 n 次 fn，返回每次的错误（按索引）。
func runConcurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = fn(idx)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// TestBargainAdminCreate 覆盖活动创建的后台校验：商品/SKU 不存在或不属于同一商品一律 21002。
func TestBargainAdminCreate(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	productID, skuID := createProductSku(t, 10000)
	otherProductID, otherSkuID := createProductSku(t, 20000)

	validReq := func() *v1.AdminCreateReq {
		return &v1.AdminCreateReq{
			Name:         "秋季砍价活动",
			ProductId:    productID,
			SkuId:        skuID,
			StartPrice:   10000,
			FloorPrice:   9000,
			HelpAmount:   100,
			MaxHelpCount: 20,
			StartTime:    gtime.NewFromTime(time.Now().Add(-time.Hour)),
			EndTime:      gtime.NewFromTime(time.Now().Add(24 * time.Hour)),
		}
	}

	t.Run("商品不存在", func(t *testing.T) {
		req := validReq()
		req.ProductId = 999999
		_, err := service.Bargain().AdminCreate(ctx, req)
		requireCode(t, err, codes.CodeBargainActivityInvalid, "创建活动-商品不存在")
	})

	t.Run("SKU 不属于该商品", func(t *testing.T) {
		req := validReq()
		req.SkuId = otherSkuID
		_, err := service.Bargain().AdminCreate(ctx, req)
		requireCode(t, err, codes.CodeBargainActivityInvalid, "创建活动-SKU 归属不符")
	})

	t.Run("非法价格配置", func(t *testing.T) {
		req := validReq()
		req.FloorPrice = req.StartPrice
		_, err := service.Bargain().AdminCreate(ctx, req)
		requireCode(t, err, codes.CodeBargainActivityInvalid, "创建活动-底价等于原价")
	})

	t.Run("创建成功并落库", func(t *testing.T) {
		res, err := service.Bargain().AdminCreate(ctx, validReq())
		if err != nil {
			t.Fatalf("创建活动: %v", err)
		}
		if res.Id <= 0 || res.Status != v1.ActivityStatusEnabled {
			t.Fatalf("创建结果异常: %+v", res.Activity)
		}
		if res.ProductName == "" || res.SkuName == "" {
			t.Fatalf("活动应带出商品/SKU 展示名: %+v", res.Activity)
		}
		if n := countRows(t, "bargain_activities", "id = ?", res.Id); n != 1 {
			t.Fatalf("活动落库行数 = %d, want 1", n)
		}
	})

	t.Run("停用后前台列表不再返回", func(t *testing.T) {
		res, err := service.Bargain().AdminCreate(ctx, validReq())
		if err != nil {
			t.Fatalf("创建活动: %v", err)
		}
		if _, err := service.Bargain().AdminStatus(ctx, &v1.AdminStatusReq{Id: res.Id, Status: v1.ActivityStatusDisabled}); err != nil {
			t.Fatalf("停用活动: %v", err)
		}
		list, err := service.Bargain().List(ctx, &v1.ListReq{})
		if err != nil {
			t.Fatalf("查询可参与活动: %v", err)
		}
		for _, item := range list.Items {
			if item.Id == res.Id {
				t.Fatalf("停用活动 %d 不应出现在可参与列表", res.Id)
			}
		}
		detail, err := service.Bargain().Detail(ctx, res.Id)
		if err != nil {
			t.Fatalf("活动详情: %v", err)
		}
		if detail.IsJoinable {
			t.Fatalf("停用活动不应 is_joinable")
		}
		// 切换商品/SKU 的编辑请求被拒绝。
		_, err = service.Bargain().AdminUpdate(ctx, &v1.AdminUpdateReq{
			Id: res.Id, Name: "改名", ProductId: otherProductID, SkuId: otherSkuID,
			StartPrice: 10000, FloorPrice: 9000, HelpAmount: 100, MaxHelpCount: 20,
			StartTime: gtime.NewFromTime(time.Now().Add(-time.Hour)),
			EndTime:   gtime.NewFromTime(time.Now().Add(24 * time.Hour)),
		})
		requireCode(t, err, codes.CodeBargainActivityInvalid, "编辑活动-改绑商品")
	})
}

// TestBargainStart 覆盖发起砍价：快照正确、重复发起 21005、活动不可参与/不存在分别 21003/21001。
func TestBargainStart(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	productID, skuID, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	users := createUsers(t, 1)
	userID := users[0]

	res, err := service.Bargain().Start(ctx, userID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}
	if res.Status != v1.StatusInProgress || res.CurrentPrice != 10000 || res.RemainingAmount != 1000 ||
		res.HelpedCount != 0 || res.MaxHelpCount != 20 || res.StartPrice != 10000 || res.FloorPrice != 9000 {
		t.Fatalf("实例快照不符合活动配置: %+v", res.Instance)
	}
	if res.ProductId != productID || res.SkuId != skuID || res.ActivityName == "" {
		t.Fatalf("实例应带出活动/商品展示字段: %+v", res.Instance)
	}
	if res.OrderId != nil {
		t.Fatalf("本期不创建订单，order_id 应为空: %+v", res.Instance.OrderId)
	}

	row := loadInstance(t, res.Id)
	if row.ActiveKey == nil || *row.ActiveKey != fmt.Sprintf("%d:%d", activityID, userID) {
		t.Fatalf("active_key = %v, want %s", row.ActiveKey, fmt.Sprintf("%d:%d", activityID, userID))
	}

	_, err = service.Bargain().Start(ctx, userID, activityID)
	requireCode(t, err, codes.CodeBargainInstanceExists, "重复发起砍价")

	_, err = service.Bargain().Start(ctx, userID, 999999)
	requireCode(t, err, codes.CodeBargainActivityNotFound, "发起不存在活动")

	disabledID := insertActivity(t, productID, skuID, 10000, 9000, 100, 20, -60, 120, 0)
	_, err = service.Bargain().Start(ctx, userID, disabledID)
	requireCode(t, err, codes.CodeBargainActivityUnavailable, "发起停用活动")

	endedID := insertActivity(t, productID, skuID, 10000, 9000, 100, 20, -120, -60, 1)
	_, err = service.Bargain().Start(ctx, userID, endedID)
	requireCode(t, err, codes.CodeBargainActivityUnavailable, "发起已结束活动")

	notStartedID := insertActivity(t, productID, skuID, 10000, 9000, 100, 20, 60, 120, 1)
	_, err = service.Bargain().Start(ctx, userID, notStartedID)
	requireCode(t, err, codes.CodeBargainActivityUnavailable, "发起未开始活动")
}

// TestBargainStartConcurrentSingleActive 覆盖并发重复发起：uk_active_instance 保证只成功一次。
func TestBargainStartConcurrentSingleActive(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	_, _, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	userID := createUsers(t, 1)[0]

	const goroutines = 8
	errs := runConcurrently(goroutines, func(int) error {
		_, err := service.Bargain().Start(ctx, userID, activityID)
		return err
	})

	success, duplicated := 0, 0
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		if codes.FromError(err) == codes.CodeBargainInstanceExists {
			duplicated++
			continue
		}
		t.Fatalf("并发发起出现非预期错误: %v", err)
	}
	if success != 1 || duplicated != goroutines-1 {
		t.Fatalf("并发发起 success=%d duplicated=%d, want 1/%d", success, duplicated, goroutines-1)
	}
	if n := countRows(t, "bargain_instances", "activity_id = ? AND user_id = ?", activityID, userID); n != 1 {
		t.Fatalf("进行中实例数 = %d, want 1", n)
	}
}

// TestBargainHelpConcurrentFloor 覆盖并发助力：价格永不突破底价，成功次数与助力次数严格收敛。
func TestBargainHelpConcurrentFloor(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	// 10000 → 9500，每次 100：恰好 5 次助力成功，第 6 次起不可助力。
	_, _, activityID := createRunningActivity(t, 10000, 9500, 100, 200)
	ownerID := createUsers(t, 1)[0]
	helpers := createUsers(t, 20)

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}
	instanceID := started.Id

	errs := runConcurrently(len(helpers), func(i int) error {
		_, err := service.Bargain().Help(ctx, helpers[i], instanceID)
		return err
	})

	success, unavailable := 0, 0
	for _, e := range errs {
		if e == nil {
			success++
			continue
		}
		if codes.FromError(e) == codes.CodeBargainHelpUnavailable {
			unavailable++
			continue
		}
		t.Fatalf("并发助力出现非预期错误: %v", e)
	}
	if success != 5 || unavailable != len(helpers)-5 {
		t.Fatalf("并发助力 success=%d unavailable=%d, want 5/%d", success, unavailable, len(helpers)-5)
	}

	row := loadInstance(t, instanceID)
	if row.CurrentPrice != 9500 {
		t.Fatalf("最终价格 = %d, want 9500（不得低于底价）", row.CurrentPrice)
	}
	if row.HelpedCount != 5 {
		t.Fatalf("助力次数 = %d, want 5", row.HelpedCount)
	}
	if row.Status != 20 || row.CompletedAt == nil {
		t.Fatalf("达到底价应置成功且写 completed_at: status=%d completed_at=%v", row.Status, row.CompletedAt)
	}

	records, err := service.Bargain().Helps(ctx, instanceID)
	if err != nil {
		t.Fatalf("查询助力记录: %v", err)
	}
	if len(records.Items) != 5 {
		t.Fatalf("助力记录数 = %d, want 5", len(records.Items))
	}
	var cutSum int64
	for _, rec := range records.Items {
		if rec.PriceAfter != rec.PriceBefore-rec.CutAmount {
			t.Fatalf("记录 %d 金额不自洽: before=%d cut=%d after=%d", rec.Id, rec.PriceBefore, rec.CutAmount, rec.PriceAfter)
		}
		if rec.PriceAfter < 9500 || rec.CutAmount <= 0 {
			t.Fatalf("记录 %d 突破价格下限: after=%d cut=%d", rec.Id, rec.PriceAfter, rec.CutAmount)
		}
		cutSum += rec.CutAmount
	}
	if cutSum != 10000-row.CurrentPrice {
		t.Fatalf("助力记录累计砍减 = %d, want %d（与实例价格一致）", cutSum, 10000-row.CurrentPrice)
	}
}

// TestBargainHelpDuplicateAndSelf 覆盖重复助力（21008）与自助拒绝（21006），且均无副作用。
func TestBargainHelpDuplicateAndSelf(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	_, _, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	users := createUsers(t, 2)
	ownerID, helperID := users[0], users[1]

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}

	_, err = service.Bargain().Help(ctx, ownerID, started.Id)
	requireCode(t, err, codes.CodeBargainSelfHelpForbidden, "给自己助力")
	if n := countRows(t, "bargain_help_records", "instance_id = ?", started.Id); n != 0 {
		t.Fatalf("自助被拒后不应产生助力记录，实际 %d", n)
	}

	helpRes, err := service.Bargain().Help(ctx, helperID, started.Id)
	if err != nil {
		t.Fatalf("好友助力: %v", err)
	}
	if helpRes.Help == nil || helpRes.Help.CutAmount != 100 || helpRes.CurrentPrice != 9900 {
		t.Fatalf("助力结果异常: instance=%+v help=%+v", helpRes.Instance, helpRes.Help)
	}
	if helpRes.Help.HelperNickname == "" {
		t.Fatalf("助力记录应带出助力者昵称: %+v", helpRes.Help)
	}

	_, err = service.Bargain().Help(ctx, helperID, started.Id)
	requireCode(t, err, codes.CodeBargainHelpDuplicated, "重复助力")

	row := loadInstance(t, started.Id)
	if row.CurrentPrice != 9900 || row.HelpedCount != 1 {
		t.Fatalf("重复助力被拒后价格/次数被改动: price=%d helped=%d", row.CurrentPrice, row.HelpedCount)
	}
	if n := countRows(t, "bargain_help_records", "instance_id = ?", started.Id); n != 1 {
		t.Fatalf("助力记录数 = %d, want 1", n)
	}
}

// TestBargainHelpRollbackOnDuplicateRecord 证明「助力记录撞唯一键 → 已推进的价格与次数整体回滚」。
func TestBargainHelpRollbackOnDuplicateRecord(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	_, _, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	users := createUsers(t, 2)
	ownerID, helperID := users[0], users[1]

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}

	// 绕过服务直接插入一条助力记录，模拟「唯一键已存在」的并发终态。
	if _, err := g.DB().Model("bargain_help_records").Ctx(ctx).Data(g.Map{
		"instance_id":    started.Id,
		"helper_user_id": helperID,
		"cut_amount":     100,
		"price_before":   10000,
		"price_after":    9900,
	}).Insert(); err != nil {
		t.Fatalf("预置助力记录: %v", err)
	}

	_, err = service.Bargain().Help(ctx, helperID, started.Id)
	requireCode(t, err, codes.CodeBargainHelpDuplicated, "撞唯一键助力")

	row := loadInstance(t, started.Id)
	if row.CurrentPrice != 10000 || row.HelpedCount != 0 {
		t.Fatalf("事务未回滚：price=%d helped=%d, want 10000/0", row.CurrentPrice, row.HelpedCount)
	}
	if n := countRows(t, "bargain_help_records", "instance_id = ?", started.Id); n != 1 {
		t.Fatalf("助力记录数 = %d, want 1（预置那条）", n)
	}
}

// TestBargainExpire 覆盖过期懒推进的幂等性与终态拒绝：过期后不可助力、不可放弃。
func TestBargainExpire(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	_, _, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	users := createUsers(t, 2)
	ownerID, helperID := users[0], users[1]

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}
	// 把截止时间推到过去，模拟活动结束（实例 expire_at 是创建时对 end_time 的快照）。
	if _, err := g.DB().Exec(ctx, "UPDATE bargain_instances SET expire_at = DATE_SUB(NOW(), INTERVAL 1 MINUTE) WHERE id = ?", started.Id); err != nil {
		t.Fatalf("调整实例截止时间: %v", err)
	}

	detail, err := service.Bargain().InstanceDetail(ctx, started.Id)
	if err != nil {
		t.Fatalf("查询实例详情: %v", err)
	}
	if detail.Status != v1.StatusExpired || detail.CompletedAt != nil {
		t.Fatalf("过期实例 status=%q completed_at=%v, want expired/nil", detail.Status, detail.CompletedAt)
	}
	// 重复读取不产生副作用。
	if _, err := service.Bargain().InstanceDetail(ctx, started.Id); err != nil {
		t.Fatalf("重复查询过期实例: %v", err)
	}
	row := loadInstance(t, started.Id)
	if row.Status != 40 || row.CompletedAt != nil || row.ActiveKey != nil {
		t.Fatalf("过期推进结果异常: status=%d completed=%v active_key=%v", row.Status, row.CompletedAt, row.ActiveKey)
	}

	_, err = service.Bargain().Help(ctx, helperID, started.Id)
	requireCode(t, err, codes.CodeBargainHelpUnavailable, "过期实例助力")
	if n := countRows(t, "bargain_help_records", "instance_id = ?", started.Id); n != 0 {
		t.Fatalf("过期实例不应产生助力记录，实际 %d", n)
	}

	_, err = service.Bargain().Abandon(ctx, ownerID, started.Id)
	requireCode(t, err, codes.CodeBargainInvalidStatusTransition, "放弃已过期实例")

	mine, err := service.Bargain().MyInstances(ctx, ownerID, &v1.MyListReq{})
	if err != nil {
		t.Fatalf("查询我的砍价记录: %v", err)
	}
	if len(mine.Items) != 1 || mine.Items[0].Status != v1.StatusExpired {
		t.Fatalf("我的砍价记录应显示过期: %+v", mine.Items)
	}

	// 过期释放 active_key，允许对同一活动重新发起。
	restarted, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("过期后重新发起: %v", err)
	}
	if restarted.Id == started.Id {
		t.Fatalf("重新发起应生成新实例")
	}
}

// TestBargainAbandonAndRestart 覆盖放弃砍价：仅本人、仅进行中、终态不可重复推进。
func TestBargainAbandonAndRestart(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	_, _, activityID := createRunningActivity(t, 10000, 9000, 100, 20)
	users := createUsers(t, 2)
	ownerID, otherID := users[0], users[1]

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}

	_, err = service.Bargain().Abandon(ctx, otherID, started.Id)
	requireCode(t, err, codes.CodeBargainInstanceNotFound, "非本人放弃砍价")
	if row := loadInstance(t, started.Id); row.Status != 10 {
		t.Fatalf("越权放弃不应改变状态: status=%d", row.Status)
	}

	abandoned, err := service.Bargain().Abandon(ctx, ownerID, started.Id)
	if err != nil {
		t.Fatalf("放弃砍价: %v", err)
	}
	if abandoned.Status != v1.StatusFailed {
		t.Fatalf("放弃后状态 = %q, want failed", abandoned.Status)
	}

	_, err = service.Bargain().Abandon(ctx, ownerID, started.Id)
	requireCode(t, err, codes.CodeBargainInvalidStatusTransition, "重复放弃")

	// 失败释放 active_key：同一活动可重新发起，历史实例保留。
	restarted, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("放弃后重新发起: %v", err)
	}
	if restarted.Id == started.Id {
		t.Fatalf("重新发起应生成新实例")
	}
	if n := countRows(t, "bargain_instances", "activity_id = ? AND user_id = ?", activityID, ownerID); n != 2 {
		t.Fatalf("历史实例应保留，实际 %d 条", n)
	}
}

// TestBargainHelpAfterSuccess 覆盖成功终态：最后一次助力补足差额并置成功，终态拒绝后续助力。
func TestBargainHelpAfterSuccess(t *testing.T) {
	setupBargainDB(t)
	ctx := context.Background()
	// 10000 → 9900，名义额 500：最后一次只砍 100。
	_, _, activityID := createRunningActivity(t, 10000, 9900, 500, 5)
	users := createUsers(t, 3)
	ownerID, helper1, helper2 := users[0], users[1], users[2]

	started, err := service.Bargain().Start(ctx, ownerID, activityID)
	if err != nil {
		t.Fatalf("发起砍价: %v", err)
	}
	helpRes, err := service.Bargain().Help(ctx, helper1, started.Id)
	if err != nil {
		t.Fatalf("首次助力: %v", err)
	}
	if helpRes.Help.CutAmount != 100 {
		t.Fatalf("最后一次助力应只砍剩余差额 100，实际 %d", helpRes.Help.CutAmount)
	}
	if helpRes.Status != v1.StatusSuccess || helpRes.CurrentPrice != 9900 || helpRes.CompletedAt == nil {
		t.Fatalf("助力后应成功: %+v", helpRes.Instance)
	}

	_, err = service.Bargain().Help(ctx, helper2, started.Id)
	requireCode(t, err, codes.CodeBargainHelpUnavailable, "成功实例继续助力")

	if n := countRows(t, "bargain_help_records", "instance_id = ?", started.Id); n != 1 {
		t.Fatalf("成功实例助力记录数 = %d, want 1", n)
	}

	// 不存在的实例统一 21004。
	_, err = service.Bargain().Help(ctx, helper2, 999999)
	requireCode(t, err, codes.CodeBargainInstanceNotFound, "助力不存在实例")
	_, err = service.Bargain().Helps(ctx, 999999)
	requireCode(t, err, codes.CodeBargainInstanceNotFound, "查询不存在实例的助力记录")
}
