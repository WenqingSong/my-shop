package order

// B1-04 统一集成链路测试（真实 MySQL）：
//   - 订单算价预览（/orders/quote）与创建：运费、会员折扣、六槽位守恒；
//   - 事务内重读权威价格并复核（预览金额不等于最终可信金额）；
//   - 钱包余额支付：余额不足回滚、重复支付不重复扣款、支付流水与状态流水；
//   - 退款事务：钱包入账 + 分销佣金冲回 + 会员成长值扣减 + 订单 20→70 原子一致。
//
// 不 Mock 数据库与事务：所有不变量都在真实 MySQL 上验证。

import (
	"context"
	"fmt"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	orderv1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	refundv1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// insertRegion 插入一条地区并返回 id（level 1=省、2=市、3=区）。
func insertRegion(t *testing.T, parentID int64, name string, level int) int64 {
	t.Helper()
	id, err := g.DB().Model("regions").Ctx(context.Background()).Data(g.Map{
		"parent_id": parentID, "name": name, "level": level,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert region: %v", err)
	}
	return id
}

// bindProductFreightTemplate 把 SKU 所属商品绑定到运费模板。
func bindProductFreightTemplate(t *testing.T, skuID, templateID int64) {
	t.Helper()
	cnt, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", skuID).Count()
	if err != nil || cnt != 1 {
		t.Fatalf("读取 sku: %v", err)
	}
	var row struct {
		ProductId int64 `json:"product_id"`
	}
	if err := g.DB().Model("skus").Ctx(context.Background()).Fields("product_id").Where("id", skuID).Scan(&row); err != nil {
		t.Fatalf("读取 sku 商品: %v", err)
	}
	if _, err := g.DB().Model("products").Ctx(context.Background()).
		Where("id", row.ProductId).Data(g.Map{"freight_template_id": templateID}).Update(); err != nil {
		t.Fatalf("绑定运费模板: %v", err)
	}
}

// bindAddressRegion 给地址写入结构化地区 id。
func bindAddressRegion(t *testing.T, addressID, regionID int64) {
	t.Helper()
	if _, err := g.DB().Model("addresses").Ctx(context.Background()).
		Where("id", addressID).Data(g.Map{"region_id": regionID}).Update(); err != nil {
		t.Fatalf("绑定地址地区: %v", err)
	}
}

// setSKUPrice 修改 SKU 售价（模拟下单前的改价）。
func setSKUPrice(t *testing.T, skuID, price int64) {
	t.Helper()
	if _, err := g.DB().Model("skus").Ctx(context.Background()).
		Where("id", skuID).Data(g.Map{"price": price}).Update(); err != nil {
		t.Fatalf("修改 SKU 价格: %v", err)
	}
}

// TestOrderCreateRepricesAuthoritativePriceInTx 覆盖 A7：预览后改价，创建订单必须按事务内权威价复核。
func TestOrderCreateRepricesAuthoritativePriceInTx(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	quote, err := service.Order().Quote(ctx, userID, &orderv1.QuoteReq{
		Source: orderv1.SourceDirect, AddressId: addressID, SkuId: skuID, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("算价预览: %v", err)
	}
	if quote.OriginalAmount != 20000 || quote.PayableAmount != 20000 || quote.ShippingFee != 0 {
		t.Fatalf("预览金额不符: %+v", quote)
	}

	// 预览后改价：最终订单必须使用 12000 而不是预览的 10000。
	setSKUPrice(t, skuID, 12000)

	created, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceDirect,
		AddressId:      addressID,
		IdempotencyKey: fmt.Sprintf("reprice-%d", chainSeq.Add(1)),
		SkuId:          skuID,
		Quantity:       2,
	})
	if err != nil {
		t.Fatalf("创建订单: %v", err)
	}
	if created.OriginalAmount != 24000 || created.TotalAmount != 24000 {
		t.Fatalf("订单必须按事务内权威价 24000 落库，实际 original=%d total=%d",
			created.OriginalAmount, created.TotalAmount)
	}
	if len(created.Items) != 1 || created.Items[0].Price != 12000 {
		t.Fatalf("订单项单价必须为权威价 12000，实际 %+v", created.Items)
	}

	// 状态流水：创建事件 0→10。
	var logs []*struct {
		FromStatus int    `json:"from_status"`
		ToStatus   int    `json:"to_status"`
		ChangeType string `json:"change_type"`
	}
	if err := g.DB().Model("order_status_logs").Ctx(ctx).
		Where("order_id", created.Id).Scan(&logs); err != nil {
		t.Fatalf("读取状态流水: %v", err)
	}
	if len(logs) != 1 || logs[0].FromStatus != 0 || logs[0].ToStatus != 10 || logs[0].ChangeType != "create" {
		t.Fatalf("创建状态流水不符: %+v", logs)
	}
}

// TestOrderQuoteFreightAndMemberDiscount 覆盖统一计价六槽位：会员折扣 + 区域运费 + 守恒。
func TestOrderQuoteFreightAndMemberDiscount(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	// 地区链：省 → 市 → 区；地址指向区，运费规则配置在省。
	provinceID := insertRegion(t, 0, "测试省", 1)
	cityID := insertRegion(t, provinceID, "测试市", 2)
	districtID := insertRegion(t, cityID, "测试区", 3)
	bindAddressRegion(t, addressID, districtID)

	templateID, err := g.DB().Model("freight_templates").Ctx(ctx).Data(g.Map{
		"name": "链路运费模板", "is_free": 0, "free_threshold": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert freight template: %v", err)
	}
	if _, err := g.DB().Model("freight_template_rules").Ctx(ctx).Data(g.Map{
		"template_id": templateID, "region_id": provinceID,
		"first_quantity": 1, "first_fee": 800,
		"additional_quantity": 1, "additional_fee": 100,
	}).Insert(); err != nil {
		t.Fatalf("insert freight rule: %v", err)
	}
	bindProductFreightTemplate(t, skuID, templateID)

	// 会员等级：9 折（9000bp），并给用户建档到该等级。
	createMemberLevel(t, 1, "链路会员", 0, 9000)
	var level struct {
		Id int64 `json:"id"`
	}
	if err := g.DB().Model("member_levels").Ctx(ctx).Fields("id").Where("level_no", 1).Scan(&level); err != nil {
		t.Fatalf("读取会员等级: %v", err)
	}
	if _, err := g.DB().Model("user_memberships").Ctx(ctx).Data(g.Map{
		"user_id": userID, "level_id": level.Id, "growth_value": 0,
	}).Insert(); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	quote, err := service.Order().Quote(ctx, userID, &orderv1.QuoteReq{
		Source: orderv1.SourceDirect, AddressId: addressID, SkuId: skuID, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("算价预览: %v", err)
	}
	// 商品 20000，会员折扣 floor(20000×1000/10000)=2000；
	// 运费：首件 800 + 续件 1×100 = 900 → 应付 20000-2000+900 = 18900。
	if quote.OriginalAmount != 20000 || quote.MemberDiscount != 2000 ||
		quote.CouponDiscount != 0 || quote.PointsDiscount != 0 {
		t.Fatalf("算价槽位不符: %+v", quote)
	}
	if quote.ShippingFee != 900 || quote.PayableAmount != 18900 {
		t.Fatalf("运费/应付不符: %+v", quote)
	}

	created, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceDirect,
		AddressId:      addressID,
		IdempotencyKey: fmt.Sprintf("freight-%d", chainSeq.Add(1)),
		SkuId:          skuID,
		Quantity:       2,
	})
	if err != nil {
		t.Fatalf("创建订单: %v", err)
	}
	if created.OriginalAmount != 20000 || created.MemberDiscountAmount != 2000 ||
		created.ShippingFee != 900 || created.TotalAmount != 18900 {
		t.Fatalf("订单金额快照不符: %+v", created.Order)
	}
	if created.TotalAmount != created.OriginalAmount-created.DiscountAmount-created.MemberDiscountAmount+created.ShippingFee {
		t.Fatalf("金额守恒被破坏: %+v", created.Order)
	}
}

// TestOrderWalletPayAndRefundAtomicity 覆盖钱包支付与退款事务链：
// 余额不足回滚、重复支付不重复扣款、退款只入账一次、成长值扣减、状态流水。
func TestOrderWalletPayAndRefundAtomicity(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	createMemberLevel(t, 1, "退款会员", 0, 10000)
	var level struct {
		Id int64 `json:"id"`
	}
	if err := g.DB().Model("member_levels").Ctx(ctx).Fields("id").Where("level_no", 1).Scan(&level); err != nil {
		t.Fatalf("读取会员等级: %v", err)
	}
	if _, err := g.DB().Model("user_memberships").Ctx(ctx).Data(g.Map{
		"user_id": userID, "level_id": level.Id, "growth_value": 0,
	}).Insert(); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	created, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceDirect,
		AddressId:      addressID,
		IdempotencyKey: fmt.Sprintf("wallet-%d", chainSeq.Add(1)),
		SkuId:          skuID,
		Quantity:       1,
	})
	if err != nil {
		t.Fatalf("创建订单: %v", err)
	}
	if created.TotalAmount != 10000 {
		t.Fatalf("订单应付期望 10000，实际 %d", created.TotalAmount)
	}

	// 余额不足：22002，订单保持待支付，余额不变。
	if _, err := g.DB().Model("wallet_accounts").Ctx(ctx).Data(g.Map{
		"user_id": userID, "balance": 5000,
	}).Insert(); err != nil {
		t.Fatalf("初始化钱包: %v", err)
	}
	if _, err := service.Order().Pay(ctx, userID, created.Id, orderv1.PayTypeBalance); codes.FromError(err) != codes.CodeWalletInsufficientBalance {
		t.Fatalf("余额不足期望 22002，实际 %v", err)
	}
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := g.DB().Model("wallet_accounts").Ctx(ctx).Fields("balance").Where("user_id", userID).Scan(&account); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if account.Balance != 5000 {
		t.Fatalf("余额不足时不应扣款，实际 %d", account.Balance)
	}
	paid, err := service.Order().Detail(ctx, userID, created.Id)
	if err != nil {
		t.Fatalf("读取订单: %v", err)
	}
	if paid.Status != orderv1.StatusPendingPayment {
		t.Fatalf("余额不足时订单应保持待支付，实际 %s", paid.Status)
	}

	// 充足够余额后支付成功。
	if _, err := g.DB().Model("wallet_accounts").Ctx(ctx).
		Where("user_id", userID).Data(g.Map{"balance": 20000}).Update(); err != nil {
		t.Fatalf("调整余额: %v", err)
	}
	if _, err := service.Order().Pay(ctx, userID, created.Id, orderv1.PayTypeBalance); err != nil {
		t.Fatalf("余额支付: %v", err)
	}
	if err := g.DB().Model("wallet_accounts").Ctx(ctx).Fields("balance").Where("user_id", userID).Scan(&account); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if account.Balance != 10000 {
		t.Fatalf("支付后余额期望 10000，实际 %d", account.Balance)
	}
	paid, err = service.Order().Detail(ctx, userID, created.Id)
	if err != nil {
		t.Fatalf("读取订单: %v", err)
	}
	if paid.Status != orderv1.StatusPaid || paid.PayType != orderv1.PayTypeBalance || paid.PayType == "" {
		t.Fatalf("支付后订单不符: %+v", paid.Order)
	}

	// 重复支付：幂等，不重复扣款。
	if _, err := service.Order().Pay(ctx, userID, created.Id, orderv1.PayTypeBalance); err != nil {
		t.Fatalf("重复支付应幂等: %v", err)
	}
	if err := g.DB().Model("wallet_accounts").Ctx(ctx).Fields("balance").Where("user_id", userID).Scan(&account); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if account.Balance != 10000 {
		t.Fatalf("重复支付不应重复扣款，实际余额 %d", account.Balance)
	}
	if cnt, _ := g.DB().Model("wallet_transactions").Ctx(ctx).
		Where("idempotency_key", fmt.Sprintf("order_pay:%d", created.Id)).Count(); cnt != 1 {
		t.Fatalf("同一订单只能有一条扣款流水，实际 %d", cnt)
	}

	// 支付成功发放成长值 10000。
	membership, err := service.Member().My(ctx, userID)
	if err != nil {
		t.Fatalf("读取会员: %v", err)
	}
	if membership.GrowthValue != 10000 {
		t.Fatalf("成长值期望 10000，实际 %d", membership.GrowthValue)
	}

	// 申请 → 审核 → 执行退款：钱包退回 10000、成长值扣回 0、订单 70。
	// 审核/执行需要真实管理员 id（不同环境下自增起点不同，不能硬编码）。
	var admin struct {
		Id int64 `json:"id"`
	}
	if err := g.DB().Model("admins").Ctx(ctx).Fields("id").Order("id ASC").Limit(1).Scan(&admin); err != nil {
		t.Fatalf("读取管理员: %v", err)
	}
	if admin.Id <= 0 {
		t.Fatal("测试环境缺少管理员账号")
	}
	applied, err := service.Refund().Apply(ctx, userID, created.Id, "链路退款")
	if err != nil {
		t.Fatalf("申请退款: %v", err)
	}
	if _, err := service.Refund().Review(ctx, admin.Id, &refundv1.ReviewReq{
		Id: applied.Id, Result: refundv1.ReviewResultApprove, Reason: "同意",
	}); err != nil {
		t.Fatalf("审核退款: %v", err)
	}
	if _, err := service.Refund().Execute(ctx, admin.Id, &refundv1.ExecuteReq{
		Id: applied.Id, Result: refundv1.ExecuteResultSuccess, Reason: "已退款",
	}); err != nil {
		t.Fatalf("执行退款: %v", err)
	}

	if err := g.DB().Model("wallet_accounts").Ctx(ctx).Fields("balance").Where("user_id", userID).Scan(&account); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if account.Balance != 20000 {
		t.Fatalf("退款后余额期望 20000，实际 %d", account.Balance)
	}
	membership, err = service.Member().My(ctx, userID)
	if err != nil {
		t.Fatalf("读取会员: %v", err)
	}
	if membership.GrowthValue != 0 {
		t.Fatalf("退款后成长值期望 0，实际 %d", membership.GrowthValue)
	}
	refunded, err := service.Order().Detail(ctx, userID, created.Id)
	if err != nil {
		t.Fatalf("读取订单: %v", err)
	}
	if refunded.Status != orderv1.StatusRefunded {
		t.Fatalf("退款后订单状态期望 refunded，实际 %s", refunded.Status)
	}

	// 重复执行退款：幂等，不重复入账、不重复扣成长值。
	if _, err := service.Refund().Execute(ctx, admin.Id, &refundv1.ExecuteReq{
		Id: applied.Id, Result: refundv1.ExecuteResultSuccess, Reason: "重复执行",
	}); err != nil {
		t.Fatalf("重复执行退款应幂等: %v", err)
	}
	if err := g.DB().Model("wallet_accounts").Ctx(ctx).Fields("balance").Where("user_id", userID).Scan(&account); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if account.Balance != 20000 {
		t.Fatalf("重复退款不应重复入账，实际余额 %d", account.Balance)
	}
	if cnt, _ := g.DB().Model("wallet_transactions").Ctx(ctx).
		Where("idempotency_key", fmt.Sprintf("refund:%d", applied.Id)).Count(); cnt != 1 {
		t.Fatalf("同一退款单只能有一条入账流水，实际 %d", cnt)
	}

	// 状态流水：create / pay / refund 三条。
	var logs []*struct {
		ChangeType string `json:"change_type"`
		FromStatus int    `json:"from_status"`
		ToStatus   int    `json:"to_status"`
	}
	if err := g.DB().Model("order_status_logs").Ctx(ctx).
		Where("order_id", created.Id).Order("id ASC").Scan(&logs); err != nil {
		t.Fatalf("读取状态流水: %v", err)
	}
	want := []struct {
		change string
		from   int
		to     int
	}{
		{"create", 0, 10},
		{"pay", 10, 20},
		{"refund", 20, 70},
	}
	if len(logs) != len(want) {
		t.Fatalf("状态流水条数期望 %d，实际 %d: %+v", len(want), len(logs), logs)
	}
	for i, w := range want {
		if logs[i].ChangeType != w.change || logs[i].FromStatus != w.from || logs[i].ToStatus != w.to {
			t.Fatalf("第 %d 条状态流水不符: %+v", i, logs[i])
		}
	}
}

// TestMemberLevelDiscountRateUsedByPricing 校验会员折扣只来自等级配置（无档案按原价）。
func TestMemberLevelDiscountRateUsedByPricing(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	// 未建档：不打折。
	quote, err := service.Order().Quote(ctx, userID, &orderv1.QuoteReq{
		Source: orderv1.SourceDirect, AddressId: addressID, SkuId: skuID, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("算价: %v", err)
	}
	if quote.MemberDiscount != 0 || quote.PayableAmount != 10000 {
		t.Fatalf("未建档用户不应有会员折扣: %+v", quote)
	}

	// 建档到 8.8 折：10000 × 1200 / 10000 = 1200。
	createMemberLevel(t, 1, "八八折", 0, 8800)
	var level struct {
		Id int64 `json:"id"`
	}
	if err := g.DB().Model("member_levels").Ctx(ctx).Fields("id").Where("level_no", 1).Scan(&level); err != nil {
		t.Fatalf("读取等级: %v", err)
	}
	if _, err := g.DB().Model("user_memberships").Ctx(ctx).Data(g.Map{
		"user_id": userID, "level_id": level.Id, "growth_value": 0,
	}).Insert(); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	quote, err = service.Order().Quote(ctx, userID, &orderv1.QuoteReq{
		Source: orderv1.SourceDirect, AddressId: addressID, SkuId: skuID, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("算价: %v", err)
	}
	if quote.MemberDiscount != 1200 || quote.PayableAmount != 8800 {
		t.Fatalf("会员折扣期望 1200/8800，实际 %+v", quote)
	}
}
