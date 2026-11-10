package order

// B1-04-08 订单链路集成测试（真实 MySQL）：会员折扣算价与快照、支付成功发放成长值/计提佣金、
// 退款成功冲回佣金/扣减成长值。
//
// 这里验证的是「跨模块事务链」本身：Create / Pay / Refund.Apply→Review→Execute 都是真实实现，
// 只有商品/地址/用户等前置数据是直接写库的夹具。不使用 Mock 替换事务与数据库约束。

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	distributionv1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	memberv1 "cnb.cool/go-cloud-devops/my-shop/api/member/v1"
	orderv1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	refundv1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"

	// 本测试位于 logic/order 包内，无法空白导入聚合包 internal/logic（会形成导入环），
	// 因此逐个导入被调用服务的实现包以触发其 init 注册。
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/address"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/cart"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/coupon"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/distribution"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/inventory"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/member"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/product"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/refund"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/sku"
)

// chainSeq 生成进程内唯一幂等键片段。
var chainSeq atomic.Int64

// setupChainTest 建立隔离环境并返回一个用户、一个可用 SKU 与一个收货地址。
func setupChainTest(t *testing.T) (userID, skuID, addressID int64) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", "test-secret-0123456789-0123456789-0123456789")
	t.Setenv("ADMIN_SUPER_PASSWORD", "test-admin-password-123")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cleanChainTables(t, ctx)
	t.Cleanup(func() { cleanChainTables(t, context.Background()) })

	userID = insertChainUser(t, "chain-user")
	skuID = insertChainSku(t, 10000)
	addressID = insertChainAddress(t, userID)
	return userID, skuID, addressID
}

// cleanChainTables 清理顺序遵循外键依赖（子表在前）。
func cleanChainTables(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, table := range []string{
		"distribution_commissions", "user_referrals",
		"member_growth_logs", "user_memberships", "member_levels",
		"refund_audits", "refund_requests",
		"order_items", "orders",
		"addresses", "inventory_logs", "inventories", "cart_items",
		"skus", "product_images", "products", "categories", "users",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
}

// insertChainUser 插入一个前台用户。
func insertChainUser(t *testing.T, username string) int64 {
	t.Helper()
	id, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username": username, "password_hash": "x", "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// insertChainSku 插入分类+商品+SKU+库存，返回 SKU id（价格单位：整数分）。
func insertChainSku(t *testing.T, price int64) int64 {
	t.Helper()
	ctx := context.Background()
	categoryID, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{"parent_id": 0, "name": "链路分类"}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	productID, err := g.DB().Model("products").Ctx(ctx).Data(g.Map{
		"name": "链路商品", "category_id": categoryID, "price": price, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	skuID, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"product_id": productID, "name": "默认", "price": price, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert sku: %v", err)
	}
	if _, err := g.DB().Model("inventories").Ctx(ctx).Data(g.Map{
		"sku_id": skuID, "quantity": 1000,
	}).Insert(); err != nil {
		t.Fatalf("insert inventory: %v", err)
	}
	return skuID
}

// insertChainAddress 插入一个默认收货地址。
func insertChainAddress(t *testing.T, userID int64) int64 {
	t.Helper()
	id, err := g.DB().Model("addresses").Ctx(context.Background()).Data(g.Map{
		"user_id":        userID,
		"recipient_name": "王五",
		"phone":          "13700000000",
		"province":       "省",
		"city":           "市",
		"district":       "区",
		"detail":         "链路地址",
		"is_default":     1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert address: %v", err)
	}
	return id
}

// createMemberLevel 创建会员等级配置。
func createMemberLevel(t *testing.T, levelNo int, name string, threshold int64, rateBp int) {
	t.Helper()
	if _, err := service.Member().AdminCreateLevel(context.Background(), &memberv1.AdminCreateLevelReq{
		LevelNo:         levelNo,
		Name:            name,
		GrowthThreshold: threshold,
		DiscountRateBp:  rateBp,
		Status:          1,
	}); err != nil {
		t.Fatalf("create member level %d: %v", levelNo, err)
	}
}

// TestOrderChainMemberDiscountGrowthAndCommission 覆盖完整链路：
// 下单算会员折扣 → 支付发成长值/计佣 → 重复支付幂等 → 退款冲回佣金/扣减成长值（不降级）。
func TestOrderChainMemberDiscountGrowthAndCommission(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	// 青铜 9.8 折（门槛 0），白银 9 折（门槛 10000 = 100 元）。
	createMemberLevel(t, 1, "青铜", 0, 9800)
	createMemberLevel(t, 2, "白银", 10000, 9000)

	// 推荐人：买家绑定到 referrer，作为佣金受益人。
	referrerID := insertChainUser(t, "chain-referrer")
	if _, err := service.Distribution().Bind(ctx, userID, &distributionv1.BindReq{ReferrerUserId: referrerID}); err != nil {
		t.Fatalf("绑定推荐人: %v", err)
	}

	// 下单：单价 10000 × 2 = 20000 商品金额；无券；会员折扣 = floor(20000 × 200/10000) = 400。
	created, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceDirect,
		AddressId:      addressID,
		IdempotencyKey: fmt.Sprintf("chain-%d", chainSeq.Add(1)),
		SkuId:          skuID,
		Quantity:       2,
	})
	if err != nil {
		t.Fatalf("创建订单: %v", err)
	}
	if created.OriginalAmount != 20000 {
		t.Fatalf("商品金额期望 20000，实际 %d", created.OriginalAmount)
	}
	if created.MemberDiscountAmount != 400 {
		t.Fatalf("会员折扣期望 400，实际 %d", created.MemberDiscountAmount)
	}
	if created.DiscountAmount != 0 {
		t.Fatalf("券优惠应为 0（不占用券语义），实际 %d", created.DiscountAmount)
	}
	if created.TotalAmount != 19600 {
		t.Fatalf("应付金额期望 19600 = 20000-0-400，实际 %d", created.TotalAmount)
	}

	// 支付：成长值 +19600（跨过白银门槛），佣金基数 = 19600 → 佣金 floor(19600×500/10000) = 980。
	if _, err := service.Order().Pay(ctx, userID, created.Id); err != nil {
		t.Fatalf("支付订单: %v", err)
	}

	membership, err := service.Member().My(ctx, userID)
	if err != nil {
		t.Fatalf("查询会员信息: %v", err)
	}
	if membership.GrowthValue != 19600 {
		t.Fatalf("成长值期望 19600（实付金额口径），实际 %d", membership.GrowthValue)
	}
	if membership.LevelNo == nil || *membership.LevelNo != 2 {
		t.Fatalf("19600 成长值应升到白银 level_no=2，实际 %v", derefInt(membership.LevelNo))
	}

	var commission struct {
		Id                int64 `json:"id"`
		BaseAmount        int64 `json:"base_amount"`
		RateBp            int   `json:"rate_bp"`
		CommissionAmount  int64 `json:"commission_amount"`
		Status            int   `json:"status"`
		BeneficiaryUserId int64 `json:"beneficiary_user_id"`
	}
	if err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Where("order_id", created.Id).Scan(&commission); err != nil {
		t.Fatalf("读取佣金: %v", err)
	}
	if commission.Id == 0 {
		t.Fatal("支付成功后应计提佣金")
	}
	if commission.BaseAmount != 19600 || commission.RateBp != 500 || commission.CommissionAmount != 980 || commission.Status != 10 {
		t.Fatalf("佣金快照不符: %+v", commission)
	}
	if commission.BeneficiaryUserId != referrerID {
		t.Fatalf("佣金受益人期望 %d，实际 %d", referrerID, commission.BeneficiaryUserId)
	}

	// 重复支付：幂等，不重复发成长值/计佣。
	if _, err := service.Order().Pay(ctx, userID, created.Id); err != nil {
		t.Fatalf("重复支付应幂等: %v", err)
	}
	membership, err = service.Member().My(ctx, userID)
	if err != nil {
		t.Fatalf("查询会员信息: %v", err)
	}
	if membership.GrowthValue != 19600 {
		t.Fatalf("重复支付后成长值应仍为 19600，实际 %d", membership.GrowthValue)
	}
	if cnt, _ := g.DB().Model("distribution_commissions").Ctx(ctx).Where("order_id", created.Id).Count(); cnt != 1 {
		t.Fatalf("重复支付后佣金应仍为 1 条，实际 %d", cnt)
	}

	// 退款成功：冲回佣金 + 扣减成长值，等级不降。
	// 管理员 id 必须从库中查询（全量测试并行执行时 admins 自增 id 不保证为 1）。
	adminID := firstAdminID(t)
	applied, err := service.Refund().Apply(ctx, userID, created.Id, "链路退款")
	if err != nil {
		t.Fatalf("发起退款: %v", err)
	}
	if _, err := service.Refund().Review(ctx, adminID, &refundv1.ReviewReq{
		Id: applied.Refund.Id, Result: refundv1.ReviewResultApprove, Reason: "同意",
	}); err != nil {
		t.Fatalf("审核退款: %v", err)
	}
	if _, err := service.Refund().Execute(ctx, adminID, &refundv1.ExecuteReq{
		Id: applied.Refund.Id, Result: refundv1.ExecuteResultSuccess, Reason: "退款成功",
	}); err != nil {
		t.Fatalf("执行退款: %v", err)
	}

	var reversed struct {
		Status         int    `json:"status"`
		ReversedAmount int64  `json:"reversed_amount"`
		RefundId       *int64 `json:"refund_id"`
		ReversalReason string `json:"reversal_reason"`
	}
	if err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Where("order_id", created.Id).Scan(&reversed); err != nil {
		t.Fatalf("读取冲回后佣金: %v", err)
	}
	if reversed.Status != 30 || reversed.ReversedAmount != 980 {
		t.Fatalf("退款后佣金应全额冲回（30/980），实际 %+v", reversed)
	}
	if reversed.RefundId == nil || *reversed.RefundId != applied.Refund.Id || reversed.ReversalReason != "refund" {
		t.Fatalf("冲回溯源字段不符: %+v", reversed)
	}

	membership, err = service.Member().My(ctx, userID)
	if err != nil {
		t.Fatalf("查询会员信息: %v", err)
	}
	if membership.GrowthValue != 0 {
		t.Fatalf("退款后成长值期望 0，实际 %d", membership.GrowthValue)
	}
	if membership.LevelNo == nil || *membership.LevelNo != 2 {
		t.Fatalf("退款不应降级，期望仍为 level_no=2，实际 %v", derefInt(membership.LevelNo))
	}
}

// TestResolveMemberDiscountBoundaries 覆盖折扣计算的取整与订单金额边界（含券已抵满的情形）。
//
// 这里直接调用算价辅助函数，因为「券抵满商品金额」需要构造满额券 + 领取 + 下单三段夹具，
// 而边界语义完全由该函数的夹紧规则决定；主链路的金额守恒已由上一个测试端到端覆盖。
func TestResolveMemberDiscountBoundaries(t *testing.T) {
	userID, _, _ := setupChainTest(t)
	ctx := context.Background()

	// 5 折等级。
	createMemberLevel(t, 1, "半价", 0, 5000)

	// 无券：floor(10000 × 5000 / 10000) = 5000。
	d, err := resolveMemberDiscount(ctx, userID, 10000, 0)
	if err != nil {
		t.Fatalf("resolveMemberDiscount: %v", err)
	}
	if d != 5000 {
		t.Fatalf("5 折等级折扣期望 5000，实际 %d", d)
	}

	// 券已抵 8000：折扣夹紧到剩余 2000（不得让应付为负）。
	d, err = resolveMemberDiscount(ctx, userID, 10000, 8000)
	if err != nil {
		t.Fatalf("resolveMemberDiscount: %v", err)
	}
	if d != 2000 {
		t.Fatalf("券后剩余 2000 时折扣应夹紧为 2000，实际 %d", d)
	}

	// 券已抵满：折扣必须为 0，应付金额不得为负。
	d, err = resolveMemberDiscount(ctx, userID, 10000, 10000)
	if err != nil {
		t.Fatalf("resolveMemberDiscount: %v", err)
	}
	if d != 0 {
		t.Fatalf("券抵满时会员折扣必须为 0，实际 %d", d)
	}

	// 商品金额为 0：无折扣、无副作用。
	d, err = resolveMemberDiscount(ctx, userID, 0, 0)
	if err != nil || d != 0 {
		t.Fatalf("商品金额为 0 时折扣应为 0，实际 %d/%v", d, err)
	}

	// 取整向下：9999 × 5000 / 10000 = 4999.5 → 4999。
	d, err = resolveMemberDiscount(ctx, userID, 9999, 0)
	if err != nil {
		t.Fatalf("resolveMemberDiscount: %v", err)
	}
	if d != 4999 {
		t.Fatalf("向下取整期望 4999，实际 %d", d)
	}

	// 停用唯一等级后，新用户没有可用等级权益：不打折。
	if _, err := g.DB().Model("member_levels").Ctx(ctx).
		Where("level_no", 1).
		Data(g.Map{"status": 0}).
		Update(); err != nil {
		t.Fatalf("停用等级: %v", err)
	}
	other := insertChainUser(t, "chain-no-level")
	d, err = resolveMemberDiscount(ctx, other, 10000, 0)
	if err != nil {
		t.Fatalf("resolveMemberDiscount(无等级): %v", err)
	}
	if d != 0 {
		t.Fatalf("无可用等级时折扣应为 0，实际 %d", d)
	}
}

// derefInt 便于在失败信息中打印 *int 的实际值。
func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// firstAdminID 返回当前库中第一个管理员 id（退款审核/执行需要真实管理员用户名快照）。
func firstAdminID(t *testing.T) int64 {
	t.Helper()
	var rows []*struct {
		Id int64 `json:"id"`
	}
	if err := g.DB().Model("admins").Ctx(context.Background()).Order("id ASC").Limit(1).Scan(&rows); err != nil {
		t.Fatalf("查询管理员: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("测试环境缺少管理员（boot.Bootstrap 应创建超级管理员）")
	}
	return rows[0].Id
}
