// Package concurrency 是 B1-05-03 的并发/事务一致性测试包。
//
// 设计边界（重要）：
//   - 只做测试，不重新实现任何业务逻辑：所有写路径都调用 internal/logic 下真实服务
//     （库存条件扣减、钱包行锁 + 条件更新、拼团人数条件更新、砍价行锁 + 生成列、
//     优惠券原子闸门），数据库为真实 MySQL 与真实事务。
//   - 不使用 Mock 替代数据库：断言的是一条条真实落库的行与业务不变量。
//   - 不使用纯内存模拟冒充并发验证。
//
// 数据安全：
//   - 只在 DATABASE_DEFAULT_NAME 以 `_test` 结尾的独立测试库上运行，否则 Skip；
//     `my_shop` / `my_shop_b10304_verify` 等共享库永远不满足该条件。
//   - 启动时校验 SELECT DATABASE() 与 DATABASE_DEFAULT_NAME 一致，避免「环境变量写 A、
//     实际连到 B」的静默错连。
//   - 清表只发生在上述已校验的独立库内，不触达任何历史迁移验证库。
package concurrency

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic" // 触发各业务服务 init 注册
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// testDBPrefix 是本任务独立测试库的命名前缀（由 scripts/test-race.sh 创建与销毁）。
	testDBPrefix = "my_shop_b10503"
	// testDBName 是 scripts/test-race.sh 为本阶段准备的库名。
	testDBName = testDBPrefix + "_test"

	// 测试用 JWT 密钥与超管密码：仅用于满足 Bootstrap 的启动前置校验，不对应任何真实环境。
	testJWTSecret = "b10503-concurrency-secret-0123456789-0123456789"
	testAdminPass = "b10503-concurrency-admin-pass"
)

var (
	// setupOnce 保证迁移 + Bootstrap 在同一个测试二进制内只执行一次。
	setupOnce sync.Once
	setupErr  error
)

// resetTables 是本包用到的全部业务表，按外键依赖「子表先删、父表后删」排序。
// 删除只发生在本包独占的独立测试库（见 requireConcurrencyDB 的三重校验）。
var resetTables = []string{
	"group_buy_participants", "group_buy_teams", "group_buy_activities",
	"bargain_help_records", "bargain_instances", "bargain_activities",
	"user_coupons", "coupon_template_scopes", "coupon_batches", "coupon_templates",
	"wallet_transactions", "recharge_orders", "wallet_accounts",
	"order_items", "order_status_logs", "orders",
	"inventory_logs", "inventories",
	"addresses", "users",
	"skus", "product_images", "products", "categories",
}

// requireConcurrencyDB 建立并发测试环境：校验库隔离 → 一次性迁移与 Bootstrap → 清空本域表。
// 每调用一次都会重置数据，使各用例互不影响（同一包内测试串行执行）。
func requireConcurrencyDB(t *testing.T) {
	t.Helper()
	name := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_NAME"))
	if name == "" {
		t.Skip("并发测试需要独立测试库：请设置 DATABASE_DEFAULT_NAME=" + testDBName)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Skipf("并发测试拒绝在非测试库运行：DATABASE_DEFAULT_NAME=%q 必须以 _test 结尾", name)
	}
	if name != testDBName {
		t.Skipf("并发测试需要专属库 %q，当前 %q（避免与其它任务共享测试库）", testDBName, name)
	}

	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPass)

	ctx := context.Background()
	setupOnce.Do(func() {
		if err := migrations.Up(ctx); err != nil {
			setupErr = fmt.Errorf("迁移独立测试库失败: %w", err)
			return
		}
		if err := boot.Bootstrap(ctx); err != nil {
			setupErr = fmt.Errorf("装配测试库/Redis 失败: %w", err)
			return
		}
		actual, err := g.DB().GetValue(ctx, "SELECT DATABASE()")
		if err != nil {
			setupErr = fmt.Errorf("查询当前数据库失败: %w", err)
			return
		}
		if got := strings.TrimSpace(actual.String()); got != name {
			setupErr = fmt.Errorf("连接库与配置不一致: SELECT DATABASE()=%q, DATABASE_DEFAULT_NAME=%q", got, name)
		}
	})
	if setupErr != nil {
		t.Fatalf("%v", setupErr)
	}
	resetDomainTables(t)
}

// resetDomainTables 清空本包用到的表；表名固定写死，不接受外部输入拼接。
func resetDomainTables(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, table := range resetTables {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("清理测试表 %s 失败（库隔离异常？）: %v", table, err)
		}
	}
}

// runConcurrently 用「关闭 channel 作为并发屏障」尽量让 n 个 goroutine 同时开始，
// 并等待全部结束。fn 内部只允许使用只读共享状态与各自的独立输入，避免测试自身引入竞态。
func runConcurrently(n int, fn func(i int)) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
}

// catalogFixture 是一次测试用到的商品域主键（全部由本用例自建）。
type catalogFixture struct {
	CategoryID int64
	ProductID  int64
	SkuID      int64
}

// uniqueSeq 保证同一纳秒内连续申请也得到不同后缀（并发建数据时避免唯一键冲突）。
var uniqueSeq atomic.Uint64

// uniqueSuffix 生成短且唯一的名称后缀（15 字符，适配 users.username VARCHAR(24)）。
func uniqueSuffix() string {
	return fmt.Sprintf("%09d%06d", time.Now().UnixNano()%1_000_000_000, uniqueSeq.Add(1)%1_000_000)
}

// shortPrefix 截断前缀，保证「前缀 + 分隔符 + 15 位后缀」不超过 24 字符。
func shortPrefix(prefix string) string {
	if len(prefix) > 6 {
		return prefix[:6]
	}
	return prefix
}

// containsFold 判断 s 是否包含 substr（忽略大小写），用于校验 information_schema 的列属性文本。
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToUpper(s), strings.ToUpper(substr))
}

// seedCatalog 建立一个「分类 → 商品（上架）→ SKU（启用）」的最小可售商品域。
// stock > 0 时通过真实库存服务 Increase 初始化库存（同时产生一条「增加」流水）。
func seedCatalog(t *testing.T, prefix string, price, stock int64) catalogFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uniqueSuffix()

	categoryID, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{
		"parent_id": 0, "name": prefix + "-cat-" + suffix, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入分类失败: %v", err)
	}
	productID, err := g.DB().Model("products").Ctx(ctx).Data(g.Map{
		"name": prefix + "-prod-" + suffix, "brand": "", "category_id": categoryID,
		"price": price, "main_image": "http://img.example/b10503.png", "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入商品失败: %v", err)
	}
	skuID, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"sku_code":       "SKU-B10503-" + suffix,
		"product_id":     productID,
		"name":           prefix + "-sku-" + suffix,
		"price":          price,
		"original_price": price,
		"status":         1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入 SKU 失败: %v", err)
	}

	f := catalogFixture{CategoryID: categoryID, ProductID: productID, SkuID: skuID}
	if stock > 0 {
		if _, err := service.Inventory().Increase(ctx, skuID, stock, nil); err != nil {
			t.Fatalf("初始化库存失败: %v", err)
		}
	}
	return f
}

// newTestUser 建立一个测试用户（只用于满足地址等外键，不参与登录）。
func newTestUser(t *testing.T, prefix string) int64 {
	t.Helper()
	userID, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username":      shortPrefix(prefix) + "-" + uniqueSuffix(),
		"password_hash": "$2a$10$b10503concurrencytestpasswordhashplaceholder000000",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入测试用户失败: %v", err)
	}
	return userID
}

// newTestAddress 为指定用户建立默认收货地址（拼团下单需要真实地址快照）。
func newTestAddress(t *testing.T, userID int64) int64 {
	t.Helper()
	addressID, err := g.DB().Model("addresses").Ctx(context.Background()).Data(g.Map{
		"user_id":        userID,
		"recipient_name": "并发测试",
		"phone":          "13800138000",
		"province":       "广东省",
		"city":           "深圳市",
		"district":       "南山区",
		"detail":         "科技园 B1-05-03 号",
		"is_default":     1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入测试地址失败: %v", err)
	}
	return addressID
}
