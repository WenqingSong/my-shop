package e2e

// B1-05-02：电商业务主链 E2E。
//
// 全链路使用真实 HTTP 路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis：
// 注册 → 登录 → 商品浏览 → 加入购物车 → 订单算价 → 创建订单 → 钱包余额支付 → 商家发货
// → 用户确认收货 → 商品评价。
//
// 金额一律为整数分，全部来自已冻结契约：
//   - 商品金额 = skus.price × quantity（服务端权威价格）；
//   - 优惠券抵扣 = 券模板减免额（满减门槛校验）；
//   - 会员折扣 = floor(原价 × (10000 - rate_bp) / 10000)，夹紧到「原价 - 券」；
//   - 积分抵扣当前恒 0（未实现，不发明比例）；
//   - 运费 = 首件费 + ceil((数量-首件数)/续件数) × 续件费；
//   - 应付 = 原价 - 券 - 会员折扣 - 积分 + 运费。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
)

// b10502Quote 是算价预览响应。
type b10502Quote struct {
	OriginalAmount int64 `json:"original_amount"`
	CouponDiscount int64 `json:"coupon_discount"`
	MemberDiscount int64 `json:"member_discount"`
	PointsDiscount int64 `json:"points_discount"`
	ShippingFee    int64 `json:"shipping_fee"`
	PayableAmount  int64 `json:"payable_amount"`
	Items          []struct {
		SkuId    int64  `json:"sku_id"`
		SkuCode  string `json:"sku_code"`
		Price    int64  `json:"price"`
		Quantity int64  `json:"quantity"`
		Amount   int64  `json:"amount"`
	} `json:"items"`
}

// b10502ProductDetail 是前台商品详情中本测试关心的字段。
type b10502ProductDetail struct {
	Id     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Skus   []struct {
		Id            int64  `json:"id"`
		SkuCode       string `json:"sku_code"`
		Price         int64  `json:"price"`
		OriginalPrice int64  `json:"original_price"`
		Status        string `json:"status"`
	} `json:"skus"`
}

// b10502CartList 是购物车列表响应。
type b10502CartList struct {
	Items []struct {
		Id            int64 `json:"id"`
		SkuId         int64 `json:"sku_id"`
		Quantity      int64 `json:"quantity"`
		Selected      bool  `json:"selected"`
		PriceSnapshot int64 `json:"price_snapshot"`
		Available     bool  `json:"available"`
	} `json:"items"`
}

// b10502Wallet 是钱包余额响应。
type b10502Wallet struct {
	Balance int64 `json:"balance"`
}

// b10502WalletTxList 是钱包流水响应。
type b10502WalletTxList struct {
	Items []struct {
		TxType        string `json:"tx_type"`
		Direction     string `json:"direction"`
		Amount        int64  `json:"amount"`
		BalanceBefore int64  `json:"balance_before"`
		BalanceAfter  int64  `json:"balance_after"`
	} `json:"items"`
	Total int `json:"total"`
}

// b10502ReviewList 是商品公开评价列表。
type b10502ReviewList struct {
	Total     int     `json:"total"`
	AvgRating float64 `json:"avg_rating"`
	Items     []struct {
		Id        int64  `json:"id"`
		ProductId int64  `json:"product_id"`
		SkuId     int64  `json:"sku_id"`
		UserId    int64  `json:"user_id"`
		Rating    int    `json:"rating"`
		Content   string `json:"content"`
	} `json:"items"`
}

// b10502Suffix 返回进程内唯一的测试数据后缀，避免与历史运行残留的唯一键冲突。
func b10502Suffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// b10502InsertRegion 写入一条地区字典（自增 id），返回 id。
func b10502InsertRegion(t *testing.T, parentID int64, level int, name string) int64 {
	t.Helper()
	id, err := g.DB().Model("regions").Ctx(context.Background()).Data(g.Map{
		"parent_id": parentID, "level": level, "name": name,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert region %q: %v", name, err)
	}
	return id
}

// b10502InsertFreightTemplate 写入运费模板与一条区域规则，返回模板 id。
func b10502InsertFreightTemplate(t *testing.T, name string, regionID, firstFee, additionalFee int64) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := g.DB().Model("freight_templates").Ctx(ctx).Data(g.Map{
		"name": name, "is_free": 0, "free_threshold": 0, "status": 1, "remark": "",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert freight template: %v", err)
	}
	if _, err := g.DB().Model("freight_template_rules").Ctx(ctx).Data(g.Map{
		"template_id": id, "region_id": regionID,
		"first_quantity": 1, "first_fee": firstFee,
		"additional_quantity": 2, "additional_fee": additionalFee,
		"free_threshold": 0,
	}).Insert(); err != nil {
		t.Fatalf("insert freight rule: %v", err)
	}
	return id
}

// b10502InsertProduct 写入上架商品（可绑定运费模板），返回商品 id。
func b10502InsertProduct(t *testing.T, categoryID int64, name string, freightTemplateID *int64) int64 {
	t.Helper()
	data := g.Map{
		"name": name, "brand": "", "category_id": categoryID,
		"price": 2000, "main_image": "http://img.example/b10502.png", "status": 1,
	}
	if freightTemplateID != nil {
		data["freight_template_id"] = *freightTemplateID
	}
	id, err := g.DB().Model("products").Ctx(context.Background()).Data(data).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return id
}

// b10502InsertSku 写入可售 SKU，返回 (skuID, skuCode)。
func b10502InsertSku(t *testing.T, productID int64, code string, price, originalPrice, stock int64) (int64, string) {
	t.Helper()
	ctx := context.Background()
	id, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"sku_code": code, "product_id": productID, "name": "默认",
		"price": price, "original_price": originalPrice, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert sku %s: %v", code, err)
	}
	orderInsertInventory(t, id, stock)
	return id, code
}

// b10502WalletRow 读取用户钱包余额（无记录为 0）。
func b10502WalletRow(t *testing.T, userID int64) (balance int64, exists bool) {
	t.Helper()
	v, err := g.DB().Model("wallet_accounts").Ctx(context.Background()).
		Fields("balance").Where("user_id", userID).Value()
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if v == nil {
		return 0, false
	}
	return v.Int64(), true
}

// b10502WalletTxCount 统计用户指定类型的钱包流水条数。
func b10502WalletTxCount(t *testing.T, userID int64, txType string) int {
	t.Helper()
	n, err := g.DB().Model("wallet_transactions").Ctx(context.Background()).
		Where("user_id", userID).Where("tx_type", txType).Count()
	if err != nil {
		t.Fatalf("count wallet tx: %v", err)
	}
	return n
}

// b10502Decode 把统一响应的 data 解码为具体结构。
func b10502Decode[T any](t *testing.T, res isoResult) T {
	t.Helper()
	var out T
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	return out
}

// b10502OrderFetch 读取订单（含明细）。
func b10502OrderFetch(t *testing.T, base, token string, orderID int64) *v1.Order {
	t.Helper()
	detail := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", orderID), token, nil)
	if detail.Status != 200 || detail.Code != 0 || detail.Data == nil {
		t.Fatalf("查询订单 %d: status=%d code=%d msg=%q", orderID, detail.Status, detail.Code, detail.Message)
	}
	return detail.Data
}

// TestB10502MainChainE2E 覆盖「注册 → 浏览 → 加购 → 算价 → 下单 → 余额支付 → 发货 → 收货 → 评价」。
func TestB10502MainChainE2E(t *testing.T) {
	ctx := context.Background()
	base := setupOrderServer(t)
	suffix := b10502Suffix()

	// ---- 夹具：地区链 + 运费模板 + 商品 / SKU / 库存 ----
	provinceID := b10502InsertRegion(t, 0, 1, "B10502省-"+suffix)
	cityID := b10502InsertRegion(t, provinceID, 2, "B10502市-"+suffix)
	districtID := b10502InsertRegion(t, cityID, 3, "B10502区-"+suffix)
	freightTemplateID := b10502InsertFreightTemplate(t, "B10502运费模板-"+suffix, provinceID, 500, 100)

	categoryID := orderInsertCategory(t, "B10502主链分类-"+suffix)
	productID := b10502InsertProduct(t, categoryID, "B10502主链商品-"+suffix, &freightTemplateID)
	skuID, skuCode := b10502InsertSku(t, productID, "SKU-B10502-CHAIN-"+suffix, 2000, 2500, 10)

	// ---- 1. 用户注册 ----
	username := "chainbuyer" + suffix
	password := "chainpass123"
	reg := isoDo(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if reg.Status != 200 || reg.Code != 0 {
		t.Fatalf("注册失败: status=%d code=%d msg=%q", reg.Status, reg.Code, reg.Message)
	}
	regData := b10502Decode[struct {
		Id       int64  `json:"id"`
		Username string `json:"username"`
	}](t, reg)
	if regData.Id <= 0 || regData.Username != username {
		t.Fatalf("注册返回不符: %+v", regData)
	}
	var dbUser struct {
		Username     string
		PasswordHash string
		Status       int
	}
	if err := g.DB().Model("users").Ctx(ctx).Where("id", regData.Id).
		Fields("username, password_hash, status").Scan(&dbUser); err != nil {
		t.Fatalf("查询注册用户: %v", err)
	}
	if dbUser.Username != username || dbUser.PasswordHash == "" || dbUser.Status != 1 {
		t.Fatalf("用户记录不符: %+v", dbUser)
	}

	// ---- 2. 登录认证 ----
	login := isoDo(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("登录失败: status=%d code=%d msg=%q", login.Status, login.Code, login.Message)
	}
	loginData := b10502Decode[struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}](t, login)
	if loginData.AccessToken == "" || loginData.TokenType != "Bearer" {
		t.Fatalf("登录返回不符: %+v", loginData)
	}
	token := loginData.AccessToken
	userID, err := parseUserID(t, isoClaims(t, token).Subject)
	if err != nil {
		t.Fatalf("解析用户 id: %v", err)
	}
	if userID != regData.Id {
		t.Fatalf("登录身份 %d 与注册用户 %d 不一致", userID, regData.Id)
	}
	me := isoDo(t, base, "GET", "/me", nil, isoAuthHeader(token))
	if me.Status != 200 || me.Code != 0 {
		t.Fatalf("GET /me: status=%d code=%d", me.Status, me.Code)
	}

	// 会员等级：9000bp = 9 折，成长值门槛唯一以避开历史数据。
	levelNo := 800000 + time.Now().UnixNano()%100000
	levelID, err := g.DB().Model("member_levels").Ctx(ctx).Data(g.Map{
		"level_no": levelNo, "name": "B10502会员等级-" + suffix,
		"growth_threshold": levelNo, "discount_rate_bp": 9000, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert member level: %v", err)
	}
	if _, err := g.DB().Model("user_memberships").Ctx(ctx).Data(g.Map{
		"user_id": userID, "level_id": levelID, "growth_value": levelNo,
	}).Insert(); err != nil {
		t.Fatalf("insert user membership: %v", err)
	}

	// ---- 3. 商品浏览（公开路由） ----
	product := isoDo(t, base, "GET", fmt.Sprintf("/products/%d", productID), nil, nil)
	if product.Status != 200 || product.Code != 0 {
		t.Fatalf("商品详情: status=%d code=%d msg=%q", product.Status, product.Code, product.Message)
	}
	productData := b10502Decode[b10502ProductDetail](t, product)
	if productData.Id != productID || productData.Status != "on_shelf" {
		t.Fatalf("商品详情不符: %+v", productData)
	}
	if len(productData.Skus) != 1 || productData.Skus[0].SkuCode != skuCode ||
		productData.Skus[0].Price != 2000 || productData.Skus[0].OriginalPrice != 2500 ||
		productData.Skus[0].Status != "enabled" {
		t.Fatalf("商品 SKU 信息不符: %+v", productData.Skus)
	}

	// ---- 4. 收货地址（结构化地区） ----
	addr := isoDo(t, base, "POST", "/addresses", map[string]any{
		"recipient_name": "张三", "phone": "13800138000",
		"region_id": districtID, "detail": "科技园1号", "is_default": true,
	}, isoAuthHeader(token))
	if addr.Status != 200 || addr.Code != 0 {
		t.Fatalf("创建地址: status=%d code=%d msg=%q", addr.Status, addr.Code, addr.Message)
	}
	addrData := b10502Decode[struct {
		Id       int64  `json:"id"`
		Province string `json:"province"`
		City     string `json:"city"`
		District string `json:"district"`
		RegionId *int64 `json:"region_id"`
	}](t, addr)
	if addrData.RegionId == nil || *addrData.RegionId != districtID {
		t.Fatalf("地址结构化地区不符: %+v", addrData)
	}
	if addrData.Province == "" || addrData.City == "" || addrData.District == "" {
		t.Fatalf("结构化地区未填充省市区文本: %+v", addrData)
	}
	// 出参不暴露 user_id，归属以数据库事实核对。
	addrOwner, err := g.DB().Model("addresses").Ctx(ctx).Fields("user_id").Where("id", addrData.Id).Value()
	if err != nil || addrOwner == nil || addrOwner.Int64() != userID {
		t.Fatalf("地址归属不符: user_id=%v err=%v", addrOwner, err)
	}

	// ---- 5. 优惠券领取（真实领取路由） ----
	now := time.Now()
	couponTemplateID := couponInsertTemplate(t, "B10502满减券-"+suffix, 3000, 300, 10,
		now.Add(-time.Hour), now.Add(24*time.Hour), now.Add(-time.Hour), now.Add(48*time.Hour), 1)
	claim := couponClaimViaAPI(t, base, token, couponTemplateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("领取优惠券: status=%d code=%d msg=%q", claim.Status, claim.Code, claim.Message)
	}
	couponID := b10502Decode[struct {
		Id     int64  `json:"id"`
		Status string `json:"status"`
	}](t, claim).Id
	if couponID <= 0 {
		t.Fatalf("领取优惠券未返回券 id: %+v", claim.Data)
	}

	// ---- 6. 加入购物车 ----
	add := isoDo(t, base, "POST", "/cart/items", map[string]any{"sku_id": skuID, "quantity": 3}, isoAuthHeader(token))
	if add.Status != 200 || add.Code != 0 {
		t.Fatalf("加入购物车: status=%d code=%d msg=%q", add.Status, add.Code, add.Message)
	}
	cart := isoDo(t, base, "GET", "/cart", nil, isoAuthHeader(token))
	cartData := b10502Decode[b10502CartList](t, cart)
	if cart.Status != 200 || cart.Code != 0 || len(cartData.Items) != 1 {
		t.Fatalf("购物车列表: status=%d code=%d items=%d", cart.Status, cart.Code, len(cartData.Items))
	}
	if cartData.Items[0].SkuId != skuID || cartData.Items[0].Quantity != 3 ||
		cartData.Items[0].PriceSnapshot != 2000 || !cartData.Items[0].Available {
		t.Fatalf("购物车条目不符: %+v", cartData.Items[0])
	}
	v, err := g.DB().Model("cart_items").Ctx(ctx).Fields("user_id").Where("sku_id", skuID).Value()
	if err != nil || v == nil {
		t.Fatalf("查询购物车归属: value=%v err=%v", v, err)
	}
	if v.Int64() != userID {
		t.Fatalf("购物车归属 %d != 当前用户 %d", v.Int64(), userID)
	}

	// ---- 7. 订单算价（六槽位） ----
	quoteRes := isoDo(t, base, "POST", "/orders/quote", map[string]any{
		"source": "cart", "address_id": addrData.Id, "coupon_id": couponID,
	}, isoAuthHeader(token))
	if quoteRes.Status != 200 || quoteRes.Code != 0 {
		t.Fatalf("算价失败: status=%d code=%d msg=%q", quoteRes.Status, quoteRes.Code, quoteRes.Message)
	}
	quote := b10502Decode[b10502Quote](t, quoteRes)
	// 原价 2000×3=6000；券减 300；会员 9 折 → floor(6000×1000/10000)=600；
	// 运费首件 500 + 续件 1×100 = 600；应付 5700。
	if quote.OriginalAmount != 6000 || quote.CouponDiscount != 300 || quote.MemberDiscount != 600 ||
		quote.PointsDiscount != 0 || quote.ShippingFee != 600 || quote.PayableAmount != 5700 {
		t.Fatalf("算价六槽位不符: %+v", quote)
	}
	if len(quote.Items) != 1 || quote.Items[0].SkuId != skuID || quote.Items[0].SkuCode != skuCode ||
		quote.Items[0].Price != 2000 || quote.Items[0].Quantity != 3 || quote.Items[0].Amount != 6000 {
		t.Fatalf("算价明细不符: %+v", quote.Items)
	}
	if quote.PayableAmount != quote.OriginalAmount-quote.CouponDiscount-quote.MemberDiscount-quote.PointsDiscount+quote.ShippingFee {
		t.Fatal("算价不满足金额守恒")
	}

	// ---- 8. 创建订单 ----
	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "cart", "address_id": addrData.Id,
		"idempotency_key": "B10502-CHAIN-" + suffix + "-1", "coupon_id": couponID,
	})
	if created.Status != 200 || created.Code != 0 || created.Data == nil {
		t.Fatalf("创建订单: status=%d code=%d msg=%q", created.Status, created.Code, created.Message)
	}
	order := created.Data
	orderID := order.Id
	if order.Status != v1.StatusPendingPayment {
		t.Fatalf("订单初始状态期望 %s，实际 %s", v1.StatusPendingPayment, order.Status)
	}
	if order.OriginalAmount != 6000 || order.DiscountAmount != 300 || order.MemberDiscountAmount != 600 ||
		order.ShippingFee != 600 || order.TotalAmount != 5700 {
		t.Fatalf("订单金额与算价不一致: %+v", order)
	}
	if len(order.Items) != 1 {
		t.Fatalf("订单明细行数期望 1，实际 %d", len(order.Items))
	}
	item := order.Items[0]
	if item.SkuId != skuID || item.SkuCode != skuCode || item.Price != 2000 ||
		item.OriginalPrice != 2500 || item.Quantity != 3 || item.ProductName != "B10502主链商品-"+suffix {
		t.Fatalf("订单明细快照不符: %+v", item)
	}
	if got := orderStock(t, skuID); got != 7 {
		t.Fatalf("下单后库存期望 7，实际 %d", got)
	}
	if n, err := g.DB().Model("cart_items").Ctx(ctx).Where("user_id", userID).Count(); err != nil || n != 0 {
		t.Fatalf("下单后购物车应清空，实际 %d err=%v", n, err)
	}
	if status := couponStatus(t, couponID); status != 20 {
		t.Fatalf("下单后优惠券应为 locked(20)，实际 %d", status)
	}

	var logs []struct {
		FromStatus int
		ToStatus   int
		ChangeType string
	}
	if err := g.DB().Model("order_status_logs").Ctx(ctx).Where("order_id", orderID).
		Fields("from_status, to_status, change_type").OrderAsc("id").Scan(&logs); err != nil {
		t.Fatalf("查询订单状态流水: %v", err)
	}
	if len(logs) != 1 || logs[0].ToStatus != 10 || logs[0].ChangeType != "create" {
		t.Fatalf("创建订单应写 1 条 create 流水，实际 %+v", logs)
	}

	// ---- 9. 钱包充值 + 余额支付 ----
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "true")
	const rechargeAmount int64 = 10000
	recharge := isoDo(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": rechargeAmount, "idempotency_key": "B10502-RECHARGE-" + suffix,
	}, isoAuthHeader(token))
	if recharge.Status != 200 || recharge.Code != 0 {
		t.Fatalf("Mock 充值: status=%d code=%d msg=%q", recharge.Status, recharge.Code, recharge.Message)
	}
	wallet := isoDo(t, base, "GET", "/wallet", nil, isoAuthHeader(token))
	walletData := b10502Decode[b10502Wallet](t, wallet)
	if walletData.Balance != rechargeAmount {
		t.Fatalf("充值后余额期望 %d，实际 %d", rechargeAmount, walletData.Balance)
	}

	paid := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, map[string]any{
		"pay_type": v1.PayTypeBalance,
	})
	if paid.Status != 200 || paid.Code != 0 || paid.Data.Status != v1.StatusPaid {
		t.Fatalf("余额支付: status=%d code=%d order_status=%s msg=%q", paid.Status, paid.Code, paid.Data.Status, paid.Message)
	}
	if paid.Data.PayType != v1.PayTypeBalance || paid.Data.PaidAt == nil {
		t.Fatalf("支付后订单字段不符: pay_type=%s paid_at=%v", paid.Data.PayType, paid.Data.PaidAt)
	}
	if balance, _ := b10502WalletRow(t, userID); balance != rechargeAmount-5700 {
		t.Fatalf("支付后余额期望 %d，实际 %d", rechargeAmount-5700, balance)
	}
	if status := couponStatus(t, couponID); status != 30 {
		t.Fatalf("支付后优惠券应为 used(30)，实际 %d", status)
	}
	if n := b10502WalletTxCount(t, userID, "order_pay"); n != 1 {
		t.Fatalf("订单支付流水期望 1 条，实际 %d", n)
	}
	var payTx struct {
		Amount        int64
		Direction     int
		BalanceBefore int64
		BalanceAfter  int64
	}
	if err := g.DB().Model("wallet_transactions").Ctx(ctx).
		Where("user_id", userID).Where("tx_type", "order_pay").
		Fields("amount, direction, balance_before, balance_after").Scan(&payTx); err != nil {
		t.Fatalf("查询支付流水: %v", err)
	}
	if payTx.Amount != 5700 || payTx.Direction != 2 || payTx.BalanceBefore != 10000 || payTx.BalanceAfter != 4300 {
		t.Fatalf("支付流水不符: %+v", payTx)
	}

	// 重复支付：语义幂等，不产生第二次扣款。
	paidAgain := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, map[string]any{
		"pay_type": v1.PayTypeBalance,
	})
	if paidAgain.Status != 200 || paidAgain.Code != 0 || paidAgain.Data.Status != v1.StatusPaid {
		t.Fatalf("重复支付应幂等: status=%d code=%d", paidAgain.Status, paidAgain.Code)
	}
	if balance, _ := b10502WalletRow(t, userID); balance != rechargeAmount-5700 {
		t.Fatalf("重复支付不得重复扣款，实际余额 %d", balance)
	}
	if n := b10502WalletTxCount(t, userID, "order_pay"); n != 1 {
		t.Fatalf("重复支付不得新增流水，实际 %d 条", n)
	}

	// ---- 10. 商家发货 ----
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	ship := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{})
	if ship.Status != 200 || ship.Code != 0 || ship.Data.Status != v1.StatusShipped || ship.Data.ShippedAt == nil {
		t.Fatalf("发货: status=%d code=%d order_status=%v", ship.Status, ship.Code, ship.Data)
	}

	// 非所有者不能确认收货。
	otherName := "chainother" + suffix
	if r := isoDo(t, base, "POST", "/register", map[string]any{"username": otherName, "password": "otherpass123"}, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("注册第二用户: status=%d code=%d", r.Status, r.Code)
	}
	otherLogin := isoDo(t, base, "POST", "/login", map[string]any{"username": otherName, "password": "otherpass123"}, nil)
	otherToken := b10502Decode[struct {
		AccessToken string `json:"access_token"`
	}](t, otherLogin).AccessToken
	otherReceive := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", orderID), otherToken, nil)
	if otherReceive.Status != 404 {
		t.Fatalf("非订单所有者确认收货应 404，实际 status=%d code=%d", otherReceive.Status, otherReceive.Code)
	}
	if got := orderStatus(t, orderID); got != 30 {
		t.Fatalf("越权确认收货不得改变订单状态，实际 %d", got)
	}

	// ---- 11. 用户确认收货 ----
	received := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", orderID), token, nil)
	if received.Status != 200 || received.Code != 0 || received.Data.Status != v1.StatusCompleted {
		t.Fatalf("确认收货: status=%d code=%d order_status=%v", received.Status, received.Code, received.Data)
	}
	if received.Data.ReceivedAt == nil || received.Data.CompletedAt == nil {
		t.Fatalf("确认收货后时间戳缺失: %+v", received.Data)
	}
	again := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", orderID), token, nil)
	if again.Status != 409 || again.Code != 9002 {
		t.Fatalf("重复确认收货应 409/9002，实际 status=%d code=%d", again.Status, again.Code)
	}

	// ---- 12. 商品评价 ----
	orderItemID := item.Id
	review := isoDo(t, base, "POST", "/reviews", map[string]any{
		"order_item_id": orderItemID, "rating": 5, "content": "B10502 主链评价",
	}, isoAuthHeader(token))
	if review.Status != 200 || review.Code != 0 {
		t.Fatalf("提交评价: status=%d code=%d msg=%q", review.Status, review.Code, review.Message)
	}
	reviewData := b10502Decode[struct {
		Id          int64 `json:"id"`
		UserId      int64 `json:"user_id"`
		OrderItemId int64 `json:"order_item_id"`
		ProductId   int64 `json:"product_id"`
		SkuId       int64 `json:"sku_id"`
		Rating      int   `json:"rating"`
	}](t, review)
	if reviewData.UserId != userID || reviewData.OrderItemId != orderItemID ||
		reviewData.ProductId != productID || reviewData.SkuId != skuID || reviewData.Rating != 5 {
		t.Fatalf("评价关联不符: %+v", reviewData)
	}
	dup := isoDo(t, base, "POST", "/reviews", map[string]any{
		"order_item_id": orderItemID, "rating": 5, "content": "重复评价",
	}, isoAuthHeader(token))
	if dup.Status != 409 || dup.Code != 10003 {
		t.Fatalf("重复评价应 409/10003，实际 status=%d code=%d", dup.Status, dup.Code)
	}
	public := isoDo(t, base, "GET", fmt.Sprintf("/products/%d/reviews", productID), nil, nil)
	publicData := b10502Decode[b10502ReviewList](t, public)
	if public.Status != 200 || publicData.Total != 1 || len(publicData.Items) != 1 ||
		publicData.Items[0].UserId != userID || publicData.AvgRating != 5 {
		t.Fatalf("公开评价列表不符: %+v", publicData)
	}

	// ---- 13. 订单状态流水（全程 5 条） ----
	logs = nil
	if err := g.DB().Model("order_status_logs").Ctx(ctx).Where("order_id", orderID).
		Fields("from_status, to_status, change_type").OrderAsc("id").Scan(&logs); err != nil {
		t.Fatalf("查询订单状态流水: %v", err)
	}
	if len(logs) != 5 {
		t.Fatalf("订单状态流水期望 5 条（create/pay/ship/receive/complete），实际 %d: %+v", len(logs), logs)
	}
	var toStatuses []int
	for _, l := range logs {
		toStatuses = append(toStatuses, l.ToStatus)
	}
	if toStatuses[0] != 10 || toStatuses[1] != 20 || toStatuses[2] != 30 || toStatuses[3] != 40 || toStatuses[4] != 50 {
		t.Fatalf("订单状态流水序列不符: %v", toStatuses)
	}
}

// TestB10502WalletBalanceInsufficient 覆盖「余额不足拒绝支付且无任何副作用」。
func TestB10502WalletBalanceInsufficient(t *testing.T) {
	base := setupOrderServer(t)
	suffix := b10502Suffix()

	categoryID := orderInsertCategory(t, "B10502余额不足分类-"+suffix)
	productID := b10502InsertProduct(t, categoryID, "B10502余额不足商品-"+suffix, nil)
	skuID, _ := b10502InsertSku(t, productID, "SKU-B10502-POOR-"+suffix, 2000, 2000, 5)

	username := "chainpoor" + suffix
	if r := isoDo(t, base, "POST", "/register", map[string]any{"username": username, "password": "poorpass123"}, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("注册: status=%d code=%d", r.Status, r.Code)
	}
	login := isoDo(t, base, "POST", "/login", map[string]any{"username": username, "password": "poorpass123"}, nil)
	token := b10502Decode[struct {
		AccessToken string `json:"access_token"`
	}](t, login).AccessToken
	userID, _ := parseUserID(t, isoClaims(t, token).Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID,
		"idempotency_key": "B10502-POOR-" + suffix, "sku_id": skuID, "quantity": 1,
	})
	if created.Status != 200 || created.Code != 0 || created.Data.TotalAmount != 2000 {
		t.Fatalf("创建订单: status=%d code=%d msg=%q", created.Status, created.Code, created.Message)
	}
	orderID := created.Data.Id

	if balance, exists := b10502WalletRow(t, userID); exists && balance != 0 {
		t.Fatalf("余额不足用例要求初始余额为 0，实际 %d", balance)
	}
	pay := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, map[string]any{
		"pay_type": v1.PayTypeBalance,
	})
	if pay.Status != 409 || pay.Code != 22002 {
		t.Fatalf("余额不足应 409/22002，实际 status=%d code=%d msg=%q", pay.Status, pay.Code, pay.Message)
	}
	if got := orderStatus(t, orderID); got != 10 {
		t.Fatalf("余额不足不得改变订单状态，实际 %d", got)
	}
	if n := b10502WalletTxCount(t, userID, "order_pay"); n != 0 {
		t.Fatalf("余额不足不得产生支付流水，实际 %d 条", n)
	}
	if balance, _ := b10502WalletRow(t, userID); balance != 0 {
		t.Fatalf("余额不足不得改变余额，实际 %d", balance)
	}

	// 充值足够金额后可正常支付，覆盖「拒绝不产生副作用、可恢复」。
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "true")
	if r := isoDo(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": 2000, "idempotency_key": "B10502-POOR-RECHARGE-" + suffix,
	}, isoAuthHeader(token)); r.Status != 200 || r.Code != 0 {
		t.Fatalf("充值: status=%d code=%d", r.Status, r.Code)
	}
	ok := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, map[string]any{
		"pay_type": v1.PayTypeBalance,
	})
	if ok.Status != 200 || ok.Code != 0 || ok.Data.Status != v1.StatusPaid {
		t.Fatalf("充值后支付: status=%d code=%d", ok.Status, ok.Code)
	}
	if balance, _ := b10502WalletRow(t, userID); balance != 0 {
		t.Fatalf("支付后余额应为 0，实际 %d", balance)
	}
}
