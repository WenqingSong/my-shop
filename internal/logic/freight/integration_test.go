package freight

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
	"cnb.cool/go-cloud-devops/my-shop/internal/testdb"
)

// setupIntegrationDB 连接隔离测试库并把 GoFrame 全局 DB 指向它。
//
// 这是硬性安全护栏（白名单统一为 internal/testdb）：历史验证库
// my_shop_b10304_verify 与生产/开发库一律拒绝，防止误连后执行 DELETE/CASCADE。
//
// 未显式设置白名单内的隔离库时跳过（而不是失败），保证无数据库环境下的
// go test ./... 仍然可运行；跳过的用例必须在交付报告中记为 NOT RUN。
func setupIntegrationDB(t *testing.T) {
	t.Helper()

	name := os.Getenv("DATABASE_DEFAULT_NAME")
	if !testdb.IsAllowed(name) {
		t.Skipf("跳过数据库集成测试：DATABASE_DEFAULT_NAME=%q，要求隔离库 %v", name, testdb.Allowed())
	}

	host := envOrDefault("DATABASE_DEFAULT_HOST", "127.0.0.1")
	port := envOrDefault("DATABASE_DEFAULT_PORT", "3306")
	user := envOrDefault("DATABASE_DEFAULT_USER", "root")
	pass := os.Getenv("DATABASE_DEFAULT_PASS")

	gdb.SetConfig(gdb.Config{
		"default": gdb.ConfigGroup{
			gdb.ConfigNode{
				Type:    "mysql",
				Host:    host,
				Port:    port,
				User:    user,
				Pass:    pass,
				Name:    name,
				Charset: "utf8mb4",
			},
		},
	})

	ctx := context.Background()
	if err := g.DB().PingMaster(); err != nil {
		t.Fatalf("连接隔离测试库 %s 失败: %v", name, err)
	}

	if _, err := g.DB().GetValue(ctx, "SELECT COUNT(*) FROM schema_migrations"); err != nil {
		// schema_migrations 不存在说明迁移未执行，直接报错避免"看似通过"。
		t.Fatalf("隔离测试库未执行迁移（schema_migrations 不可读）: %v", err)
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true",
		user, pass, host, port, name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开隔离测试库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("隔离测试库 ping 失败: %v", err)
	}
	var dbName string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&dbName); err != nil {
		t.Fatalf("读取当前库名失败: %v", err)
	}
	if !testdb.IsAllowed(dbName) {
		t.Fatalf("当前连接库为 %q，拒绝在非隔离库执行集成测试（允许 %v）", dbName, testdb.Allowed())
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// uniqueSuffix 生成测试数据后缀，避免多次运行/并行运行时名称冲突。
func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// insertRegion 插入一个地区节点并返回 id。
func insertRegion(t *testing.T, ctx context.Context, parentID int64, level int, name string) int64 {
	t.Helper()
	id, err := g.DB().Model("regions").Ctx(ctx).Data(g.Map{
		"parent_id": parentID,
		"level":     level,
		"name":      name,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入地区失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Model("regions").Ctx(context.Background()).Where("id", id).Delete()
	})
	return id
}

// insertTemplate 插入一个运费模板并返回 id。
func insertTemplate(t *testing.T, ctx context.Context, name string, isFree int, freeThreshold int64) int64 {
	t.Helper()
	id, err := g.DB().Model("freight_templates").Ctx(ctx).Data(g.Map{
		"name":           name,
		"is_free":        isFree,
		"free_threshold": freeThreshold,
		"status":         templateStatusEnabled,
		"remark":         "",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入运费模板失败: %v", err)
	}
	t.Cleanup(func() {
		// 删除模板会级联删除规则，测试自身不依赖跨用例残留。
		_, _ = g.DB().Model("freight_templates").Ctx(context.Background()).Where("id", id).Delete()
	})
	return id
}

// insertRule 插入一条区域规则并返回 id。
func insertRule(t *testing.T, ctx context.Context, templateID, regionID int64, firstQty, firstFee, addQty, addFee, threshold int64) int64 {
	t.Helper()
	id, err := g.DB().Model("freight_template_rules").Ctx(ctx).Data(g.Map{
		"template_id":         templateID,
		"region_id":           regionID,
		"first_quantity":      firstQty,
		"first_fee":           firstFee,
		"additional_quantity": addQty,
		"additional_fee":      addFee,
		"free_threshold":      threshold,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入运费区域规则失败: %v", err)
	}
	return id
}

func TestIntegrationQuoteByPieceWithRegionFallback(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	province := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	city := insertRegion(t, ctx, province, 2, "市_"+suffix)
	district := insertRegion(t, ctx, city, 3, "区_"+suffix)

	templateID := insertTemplate(t, ctx, "模板_"+suffix, 0, 0)
	// 只在市级配置规则：区级未配置时必须回退到市级，而不是判定「无规则」。
	insertRule(t, ctx, templateID, city, 1, 500, 2, 100, 0)

	s := New()
	res, err := s.Quote(ctx, &service.FreightQuoteReq{
		RegionID: district,
		Items: []service.FreightItem{
			{FreightTemplateID: &templateID, Quantity: 3, Amount: 1000},
		},
	})
	if err != nil {
		t.Fatalf("运费计算失败: %v", err)
	}
	// 首件 500 + ceil((3-1)/2)=1 个续件 * 100 = 600
	if res.ShippingFee != 600 {
		t.Fatalf("运费 = %d，期望 600（区级回退到市级规则）", res.ShippingFee)
	}
}

func TestIntegrationQuoteTemplateDisabled(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	region := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	templateID := insertTemplate(t, ctx, "停用模板_"+suffix, 0, 0)
	insertRule(t, ctx, templateID, region, 1, 500, 1, 100, 0)
	if _, err := g.DB().Model("freight_templates").Ctx(ctx).
		Where("id", templateID).Data(g.Map{"status": templateStatusDisabled}).Update(); err != nil {
		t.Fatalf("停用模板失败: %v", err)
	}

	s := New()
	_, err := s.Quote(ctx, &service.FreightQuoteReq{
		RegionID: region,
		Items: []service.FreightItem{
			{FreightTemplateID: &templateID, Quantity: 1, Amount: 1000},
		},
	})
	if err == nil {
		t.Fatal("停用模板必须明确报错，不得静默按 0 运费")
	}
	if got := codes.FromError(err); got != codes.CodeFreightTemplateDisabled {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodeFreightTemplateDisabled)
	}
}

func TestIntegrationQuoteMissingRegionRule(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	region := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	otherRegion := insertRegion(t, ctx, 0, 1, "其他省_"+suffix)
	templateID := insertTemplate(t, ctx, "无规则模板_"+suffix, 0, 0)
	insertRule(t, ctx, templateID, otherRegion, 1, 500, 1, 100, 0)

	s := New()
	_, err := s.Quote(ctx, &service.FreightQuoteReq{
		RegionID: region,
		Items: []service.FreightItem{
			{FreightTemplateID: &templateID, Quantity: 1, Amount: 1000},
		},
	})
	if err == nil {
		t.Fatal("无匹配区域规则必须明确报错，不得静默返回免费运费")
	}
	if got := codes.FromError(err); got != codes.CodeFreightRegionRuleMissing {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodeFreightRegionRuleMissing)
	}
}

func TestIntegrationQuoteUnboundTemplateIsFree(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	s := New()
	// 历史商品未绑定模板：不要求地区有效，直接零运费。
	res, err := s.Quote(ctx, &service.FreightQuoteReq{
		RegionID: 0,
		Items: []service.FreightItem{
			{FreightTemplateID: nil, Quantity: 5, Amount: 9999},
		},
	})
	if err != nil {
		t.Fatalf("未绑定模板应保持零运费兼容，实际错误: %v", err)
	}
	if res.ShippingFee != 0 {
		t.Fatalf("运费 = %d，期望 0", res.ShippingFee)
	}
}

func TestIntegrationQuoteFreeShippingThreshold(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	region := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	templateID := insertTemplate(t, ctx, "免邮模板_"+suffix, 0, 5000)
	insertRule(t, ctx, templateID, region, 1, 500, 1, 100, 0)

	s := New()
	res, err := s.Quote(ctx, &service.FreightQuoteReq{
		RegionID: region,
		Items: []service.FreightItem{
			{FreightTemplateID: &templateID, Quantity: 3, Amount: 5000},
		},
	})
	if err != nil {
		t.Fatalf("运费计算失败: %v", err)
	}
	if res.ShippingFee != 0 {
		t.Fatalf("达到免邮阈值时运费应为 0，实际 %d", res.ShippingFee)
	}
}

func TestIntegrationRegionRuleUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	region := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	templateID := insertTemplate(t, ctx, "唯一约束模板_"+suffix, 0, 0)
	insertRule(t, ctx, templateID, region, 1, 500, 1, 100, 0)

	_, err := g.DB().Model("freight_template_rules").Ctx(ctx).Data(g.Map{
		"template_id":         templateID,
		"region_id":           region,
		"first_quantity":      1,
		"first_fee":           600,
		"additional_quantity": 1,
		"additional_fee":      200,
		"free_threshold":      0,
	}).Insert()
	if err == nil {
		t.Fatal("同一模板同一地区重复配置必须被 uk_template_region 拒绝")
	}
	if !isDuplicateKeyError(err) {
		t.Fatalf("期望 1062 唯一约束冲突，实际: %v", err)
	}
}

func TestIntegrationTemplateDeleteCascadesRules(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	suffix := uniqueSuffix()
	region := insertRegion(t, ctx, 0, 1, "省_"+suffix)
	templateID := insertTemplate(t, ctx, "级联模板_"+suffix, 0, 0)
	insertRule(t, ctx, templateID, region, 1, 500, 1, 100, 0)

	if _, err := g.DB().Model("freight_templates").Ctx(ctx).
		Where("id", templateID).Delete(); err != nil {
		t.Fatalf("删除模板失败: %v", err)
	}
	count, err := g.DB().Model("freight_template_rules").Ctx(ctx).
		Where("template_id", templateID).Count()
	if err != nil {
		t.Fatalf("统计规则失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("删除模板后规则残留 %d 条，期望 0（ON DELETE CASCADE）", count)
	}
}

func TestIntegrationProductsFreightTemplateColumnNullable(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	value, err := g.DB().GetValue(ctx, `
		SELECT IS_NULLABLE FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'products' AND COLUMN_NAME = 'freight_template_id'`)
	if err != nil {
		t.Fatalf("查询列元数据失败: %v", err)
	}
	if strings.ToUpper(value.String()) != "YES" {
		t.Fatalf("products.freight_template_id 必须可空（历史商品兼容），实际 IS_NULLABLE=%s", value.String())
	}
}

func TestIntegrationSchemaVersion(t *testing.T) {
	ctx := context.Background()
	setupIntegrationDB(t)

	// 期望值为内嵌迁移的最新版本，而不是硬编码版本号：后续任务新增迁移后，
	// 本用例应继续校验「隔离库已迁移到最新」，而不是因版本号推进而误报失败。
	want := int64(migrations.LatestVersion())
	value, err := g.DB().GetValue(ctx, "SELECT MAX(version) FROM schema_migrations")
	if err != nil {
		t.Fatalf("查询迁移版本失败: %v", err)
	}
	if value.Int64() != want {
		t.Fatalf("隔离库迁移版本 = %d，期望 %d（内嵌迁移最新版本）", value.Int64(), want)
	}
	dirty, err := g.DB().GetValue(ctx, "SELECT dirty FROM schema_migrations WHERE version = ?", want)
	if err != nil {
		t.Fatalf("查询 dirty 失败: %v", err)
	}
	if dirty.Int() != 0 {
		t.Fatalf("隔离库 migration dirty=%d，期望 0", dirty.Int())
	}
}
