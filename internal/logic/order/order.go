// Package order 实现「普通订单核心闭环」业务逻辑：创建（购物车/直接购买）、服务端定价与快照、
// 唯一订单号、幂等去重、状态机推进、取消/退款恢复库存、超时自动取消与支付 Mock（幂等）。
package order

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	addressv1 "cnb.cool/go-cloud-devops/my-shop/api/address/v1"
	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logic/pricing"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 订单状态流水枚举（order_status_logs，见 docs/design/order.md §2.4）。
const (
	changeTypeCreate        = "create"
	changeTypePay           = "pay"
	changeTypeCancel        = "cancel"
	changeTypeTimeoutCancel = "timeout_cancel"
	changeTypeShip          = "ship"
	changeTypeReceive       = "receive"
	changeTypeComplete      = "complete"
	changeTypeRefund        = "refund"
)

// 状态流水操作者类型（1=user、2=admin、3=system）。
const (
	operatorTypeUser   = 1
	operatorTypeAdmin  = 2
	operatorTypeSystem = 3
)

// defaultPayTimeoutSeconds 是 order.pay_timeout 的代码默认值（秒）。
const defaultPayTimeoutSeconds int64 = 900

// 订单状态（DB TINYINT，见 docs/design/order.md 状态机）。
const (
	statusPendingPayment = 10
	statusPaid           = 20
	statusShipped        = 30
	statusReceived       = 40
	statusCompleted      = 50
	statusCancelled      = 60
	statusRefunded       = 70
)

// 取消原因（DB TINYINT）。
const (
	cancelReasonUser    = 1
	cancelReasonTimeout = 2
)

const (
	// maxOrderQuantity 单行项目数量上限（与购物车业务上限一致，防 INT UNSIGNED 溢出）。
	maxOrderQuantity = 999
	// maxOrderNoRetry 订单号撞号重试次数（uk_order_no 兜底，撞号概率极低）。
	maxOrderNoRetry = 3

	// memberDiscountBaseBp 是会员折扣率的万分比基数（10000 = 原价、不打折）。
	memberDiscountBaseBp int64 = 10000
	// maxSafeAmountForRate 是参与比例计算的安全金额上限：original × 10000 不会溢出 int64。
	maxSafeAmountForRate int64 = math.MaxInt64 / memberDiscountBaseBp
)

type sOrder struct{}

func init() {
	service.RegisterOrder(New())
}

// New 创建并返回订单服务实现。
func New() *sOrder {
	return &sOrder{}
}

// orderRow 是 orders 表的一条记录。
type orderRow struct {
	Id                   int64       `json:"id"`
	OrderNo              string      `json:"order_no"`
	UserId               int64       `json:"user_id"`
	Status               int         `json:"status"`
	TotalAmount          int64       `json:"total_amount"`
	OriginalAmount       int64       `json:"original_amount"`
	DiscountAmount       int64       `json:"discount_amount"`
	MemberDiscountAmount int64       `json:"member_discount_amount"`
	ShippingFee          int64       `json:"shipping_fee"`
	PayType              string      `json:"pay_type"`
	UserCouponId         *int64      `json:"user_coupon_id"`
	IdempotencyKey       string      `json:"idempotency_key"`
	RequestHash          string      `json:"request_hash"`
	RecipientName        string      `json:"recipient_name"`
	Phone                string      `json:"phone"`
	Province             string      `json:"province"`
	City                 string      `json:"city"`
	District             string      `json:"district"`
	Detail               string      `json:"detail"`
	AddressId            *int64      `json:"address_id"`
	ExpireAt             *gtime.Time `json:"expire_at"`
	CancelReason         *int        `json:"cancel_reason"`
	PaidAt               *gtime.Time `json:"paid_at"`
	ShippedAt            *gtime.Time `json:"shipped_at"`
	ReceivedAt           *gtime.Time `json:"received_at"`
	CompletedAt          *gtime.Time `json:"completed_at"`
	CancelledAt          *gtime.Time `json:"cancelled_at"`
	RefundedAt           *gtime.Time `json:"refunded_at"`
	CreatedAt            *gtime.Time `json:"created_at"`
	UpdatedAt            *gtime.Time `json:"updated_at"`
}

// orderItemRow 是 order_items 表的一条记录。
type orderItemRow struct {
	Id               int64  `json:"id"`
	OrderId          int64  `json:"order_id"`
	SkuId            int64  `json:"sku_id"`
	SkuCode          string `json:"sku_code"`
	ProductId        int64  `json:"product_id"`
	SkuName          string `json:"sku_name"`
	SpecText         string `json:"spec_text"`
	ProductName      string `json:"product_name"`
	ProductMainImage string `json:"product_main_image"`
	Price            int64  `json:"price"`
	OriginalPrice    int64  `json:"original_price"`
	Quantity         int64  `json:"quantity"`
}

// lineItem 是下单时解析出的行项目（含服务端成交价与商品/SKU/规格快照）。
type lineItem struct {
	SkuID            int64
	SkuCode          string
	Quantity         int64
	SkuName          string
	SpecText         string
	ProductID        int64
	ProductName      string
	ProductMainImage string
	Price            int64
	OriginalPrice    int64
	// FreightTemplateID 是商品绑定的运费模板 id（nil=未绑定，零运费兼容）。
	FreightTemplateID *int64
}

// orderAmounts 是订单六槽位金额（整数分），一律由 pricing.Quote 产出。
type orderAmounts struct {
	OriginalAmount int64
	CouponDiscount int64
	MemberDiscount int64
	PointsDiscount int64
	ShippingFee    int64
	PayableAmount  int64
}

// duplicateKeyErr 表示订单写入命中唯一约束（携带键名），由 Create 分流幂等/撞号重试。
type duplicateKeyErr struct{ key string }

func (e *duplicateKeyErr) Error() string { return "订单唯一约束冲突: " + e.key }

// Create 创建订单：服务端定价 → 六槽位计价预览 → 单事务「重读权威数据复核金额 + 写订单 + 订单项
// + 扣库存 + 预占券 + 清已购购物车条目 + 状态流水」。
//
// 预览金额不是最终可信金额：事务内会重新锁定读取 skus 权威价格、重算券/会员/运费，并以复核结果落库。
// 幂等键命中（uk_user_idempotency）时回滚并读回既有订单：同请求指纹返回既有订单、否则 9006。
func (s *sOrder) Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error) {
	source := strings.TrimSpace(req.Source)
	if source != v1.SourceCart && source != v1.SourceDirect {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.AddressId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.CouponId < 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	address, err := service.Address().Detail(ctx, userID, req.AddressId)
	if err != nil {
		return nil, err
	}

	lines, err := s.buildLines(ctx, source, userID, req)
	if err != nil {
		// 购物车下单成功后，服务端会在同一事务内删除已购条目：用同一幂等键重放时
		// buildLines 只能得到 9004，无法再重算 request_hash。按 INV-002（同键同内容
		// 返回既有订单），这里回读既有订单，避免把已成功的下单误报为"购物车无勾选商品"。
		if source == v1.SourceCart && codes.FromError(err) == codes.CodeOrderCartEmpty {
			replay, replayErr := s.replayConsumedCartOrder(ctx, userID, idempotencyKey, req)
			if replayErr != nil {
				return nil, replayErr
			}
			if replay != nil {
				return replay, nil
			}
		}
		return nil, err
	}

	// 预检：提前暴露券不可用/运费无法计算等问题；最终金额由事务内复核结果决定。
	if _, err := s.computeAmounts(ctx, userID, lines, req.CouponId, address.RegionId); err != nil {
		return nil, err
	}

	// 幂等指纹覆盖来源、地址快照（地址内容可能被修改）、券与行项目，避免「同键不同请求」被误判幂等。
	hash := requestHash(source, address.Address, req.CouponId, lines)

	var createdID int64
	for attempt := 0; attempt < maxOrderNoRetry; attempt++ {
		orderNo := generateOrderNo()
		createdID, err = s.insertOrder(ctx, userID, source, req.AddressId, idempotencyKey, hash,
			req.CouponId, orderNo, &address.Address, lines)
		if err == nil {
			break
		}
		var dk *duplicateKeyErr
		if errors.As(err, &dk) {
			switch {
			case strings.Contains(dk.key, "uk_user_idempotency"):
				return s.handleIdempotency(ctx, userID, idempotencyKey, hash)
			case strings.Contains(dk.key, "uk_order_no"):
				continue // 撞号，重新生成订单号重试
			}
		}
		return nil, err
	}
	if createdID == 0 {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成订单号重试失败"))
	}

	o, err := s.loadOwnedOrder(ctx, userID, createdID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建订单后未找到记录"))
	}
	logx.Info(ctx, "order_created", "下单成功", logx.Fields{
		"user_id":      userID,
		"order_id":     o.Id,
		"order_no":     o.OrderNo,
		"total_amount": o.TotalAmount,
	})
	return &v1.CreateRes{Order: *o}, nil
}

// Quote 订单算价预览（只读）：按服务端权威价格、当前会员折扣、券与运费计算六槽位金额。
//
// 边界：不占用/不核销券、不写订单、不扣库存；返回金额仅用于前端展示，
// 创建订单时会在事务内重新读取权威数据并复核（预览金额不等于最终可信金额）。
func (s *sOrder) Quote(ctx context.Context, userID int64, req *v1.QuoteReq) (*v1.QuoteRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	source := strings.TrimSpace(req.Source)
	if source != v1.SourceCart && source != v1.SourceDirect {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.AddressId <= 0 || req.CouponId < 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	address, err := service.Address().Detail(ctx, userID, req.AddressId)
	if err != nil {
		return nil, err
	}
	lines, err := s.buildLines(ctx, source, userID, &v1.CreateReq{
		Source:   source,
		SkuCode:  req.SkuCode,
		SkuId:    req.SkuId,
		Quantity: req.Quantity,
	})
	if err != nil {
		return nil, err
	}
	amounts, err := s.computeAmounts(ctx, userID, lines, req.CouponId, address.RegionId)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.QuoteItem, 0, len(lines))
	for _, l := range lines {
		items = append(items, &v1.QuoteItem{
			SkuId:     l.SkuID,
			SkuCode:   l.SkuCode,
			ProductId: l.ProductID,
			Name:      l.SkuName,
			SpecText:  l.SpecText,
			Price:     l.Price,
			Quantity:  l.Quantity,
			Amount:    l.Price * l.Quantity,
		})
	}
	return &v1.QuoteRes{
		OriginalAmount: amounts.OriginalAmount,
		CouponDiscount: amounts.CouponDiscount,
		MemberDiscount: amounts.MemberDiscount,
		PointsDiscount: amounts.PointsDiscount,
		ShippingFee:    amounts.ShippingFee,
		PayableAmount:  amounts.PayableAmount,
		Items:          items,
	}, nil
}

// computeAmounts 计算六槽位金额：商品金额 → 券 → 会员折扣 → 积分（恒 0）→ 运费。
//
// 金额公式只在 pricing.Quote 中实现，本方法只负责收集各项输入；
// 任一输入非法（如券不可用、地区缺失导致运费无法计算）都会整体失败，不做静默降级。
func (s *sOrder) computeAmounts(ctx context.Context, userID int64, lines []lineItem, couponID int64, regionID *int64) (*orderAmounts, error) {
	original := int64(0)
	for _, l := range lines {
		original += l.Price * l.Quantity
	}

	couponDiscount := int64(0)
	if couponID > 0 {
		d, err := service.Coupon().GetDiscount(ctx, userID, couponID, original)
		if err != nil {
			return nil, err
		}
		couponDiscount = d
	}

	memberDiscount, err := resolveMemberDiscount(ctx, userID, original, couponDiscount)
	if err != nil {
		return nil, err
	}

	shipping, err := s.freightFee(ctx, lines, regionID)
	if err != nil {
		return nil, err
	}

	breakdown, err := pricing.Quote(ctx, &pricing.QuoteRequest{
		OriginalAmount: original,
		CouponDiscount: couponDiscount,
		MemberDiscount: memberDiscount,
		PointsDiscount: 0,
		ShippingFee:    shipping,
	})
	if err != nil {
		return nil, err
	}
	return &orderAmounts{
		OriginalAmount: breakdown.OriginalAmount,
		CouponDiscount: breakdown.CouponDiscount,
		MemberDiscount: breakdown.MemberDiscount,
		PointsDiscount: breakdown.PointsDiscount,
		ShippingFee:    breakdown.ShippingFee,
		PayableAmount:  breakdown.PayableAmount,
	}, nil
}

// freightFee 调用运费模块计算整单运费。
//
// 未绑定运费模板的商品按零运费兼容；一旦有商品绑定模板，收货地址必须是结构化地区
// （region_id 非空且存在），否则运费模块返回 21008，而不是静默按 0 处理。
func (s *sOrder) freightFee(ctx context.Context, lines []lineItem, regionID *int64) (int64, error) {
	items := make([]service.FreightItem, 0, len(lines))
	needRegion := false
	for _, l := range lines {
		items = append(items, service.FreightItem{
			FreightTemplateID: l.FreightTemplateID,
			Quantity:          l.Quantity,
			Amount:            l.Price * l.Quantity,
		})
		if l.FreightTemplateID != nil {
			needRegion = true
		}
	}
	var region int64
	if regionID != nil {
		region = *regionID
	} else if needRegion {
		return 0, codes.New(codes.CodeFreightRegionNotFound)
	}
	res, err := service.Freight().Quote(ctx, &service.FreightQuoteReq{RegionID: region, Items: items})
	if err != nil {
		return 0, err
	}
	if res == nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("运费计算返回空结果"))
	}
	return res.ShippingFee, nil
}

// resolveMemberDiscount 计算会员等级折扣金额（整数分，向下取整）。
//
// 公式与边界（docs/design/pricing.md §5）：
//
//	raw = floor(original_amount × (10000 - rate_bp) / 10000)
//	member_discount_amount = min(raw, original_amount - coupon_discount)
//
// 第二条夹紧保证「各项优惠累计 ≤ 商品金额」：会员折扣不会把应付金额压成负数，
// 也不会突破订单金额边界（total_amount ≥ 0 恒成立）。
// 无等级权益（rate_bp = 10000）或原始金额为 0 时返回 0，不产生任何会员副作用。
func resolveMemberDiscount(ctx context.Context, userID, originalAmount, couponDiscount int64) (int64, error) {
	if originalAmount <= 0 {
		return 0, nil
	}
	if originalAmount > maxSafeAmountForRate {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("订单商品金额 %d 超出会员折扣计算范围", originalAmount))
	}
	remaining := originalAmount - couponDiscount
	if remaining <= 0 {
		return 0, nil
	}

	rateBp, err := service.Member().DiscountRateBp(ctx, userID)
	if err != nil {
		return 0, err
	}
	if rateBp <= 0 || int64(rateBp) >= memberDiscountBaseBp {
		return 0, nil
	}
	discount := originalAmount * (memberDiscountBaseBp - int64(rateBp)) / memberDiscountBaseBp
	if discount > remaining {
		discount = remaining
	}
	if discount < 0 {
		discount = 0
	}
	return discount, nil
}

// buildLines 依据来源组装行项目：直接购买取单个 SKU（sku_code 优先，兼容 sku_id）；
// 购物车购买取全部勾选项。单价一律由 resolveLine 从数据库重新读取。
func (s *sOrder) buildLines(ctx context.Context, source string, userID int64, req *v1.CreateReq) ([]lineItem, error) {
	switch source {
	case v1.SourceDirect:
		if req.SkuId <= 0 && strings.TrimSpace(req.SkuCode) == "" {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		if req.Quantity < 1 || req.Quantity > maxOrderQuantity {
			return nil, codes.New(codes.CodeOrderInvalidQuantity)
		}
		skuID := req.SkuId
		if code := strings.TrimSpace(req.SkuCode); code != "" {
			sku, err := service.Sku().GetByCode(ctx, code)
			if err != nil {
				return nil, err
			}
			if sku == nil {
				return nil, codes.New(codes.CodeSkuNotFound)
			}
			// 同时提交 sku_code 与 sku_id 时必须指向同一 SKU，避免客户端拼接两种标识绕过校验。
			if skuID > 0 && skuID != sku.Id {
				return nil, codes.New(codes.CodeOrderSkuUnavailable)
			}
			skuID = sku.Id
		}
		line, err := s.resolveLine(ctx, skuID, req.Quantity)
		if err != nil {
			return nil, err
		}
		if err := verifyClientPrice(req.Price, line.Price); err != nil {
			return nil, err
		}
		return []lineItem{line}, nil
	case v1.SourceCart:
		// 购物车下单不接受客户端单价：多行订单没有单一「客户端单价」语义，
		// 任何 price 字段都视为不可信输入直接拒绝（定价只取 skus.price）。
		if req.Price != nil {
			return nil, codes.New(codes.CodeOrderPriceMismatch)
		}
		cart, err := service.Cart().List(ctx, userID)
		if err != nil {
			return nil, err
		}
		lines := make([]lineItem, 0, len(cart.Items))
		for _, item := range cart.Items {
			if !item.Selected {
				continue
			}
			line, err := s.resolveLine(ctx, item.SkuId, item.Quantity)
			if err != nil {
				return nil, err
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return nil, codes.New(codes.CodeOrderCartEmpty)
		}
		return lines, nil
	default:
		return nil, codes.New(codes.CodeInvalidArgument)
	}
}

// resolveLine 服务端定价：重读 skus.price 作为成交价，校验 SKU enabled 且商品 on_shelf，
// 同时快照 SKU 编码、规格文本、原价与商品/SKU 名称、主图。不信任客户端提交价、也不信任购物车快照。
func (s *sOrder) resolveLine(ctx context.Context, skuID, qty int64) (lineItem, error) {
	sku, err := service.Sku().GetByID(ctx, skuID)
	if err != nil {
		return lineItem{}, err
	}
	if sku == nil {
		return lineItem{}, codes.New(codes.CodeSkuNotFound)
	}
	if sku.Status != skuv1.StatusEnabled {
		return lineItem{}, codes.New(codes.CodeOrderSkuUnavailable)
	}
	product, err := service.Product().GetByID(ctx, sku.ProductId)
	if err != nil {
		return lineItem{}, err
	}
	if product == nil || product.Status != productv1.StatusOnShelf {
		return lineItem{}, codes.New(codes.CodeOrderSkuUnavailable)
	}
	return lineItem{
		SkuID:             skuID,
		SkuCode:           sku.SkuCode,
		Quantity:          qty,
		SkuName:           sku.Name,
		SpecText:          skuSpecText(sku.SpecValues),
		ProductID:         sku.ProductId,
		ProductName:       product.Name,
		ProductMainImage:  product.MainImage,
		Price:             sku.Price,
		OriginalPrice:     sku.OriginalPrice,
		FreightTemplateID: product.FreightTemplateId,
	}, nil
}

// verifyClientPrice 校验客户端提交单价：未提交视为不参与；提交值必须等于服务端权威售价，
// 否则拒绝（9007）。该值永不参与金额计算，只用于识别伪造/篡改价格的请求。
func verifyClientPrice(n *json.Number, serverPrice int64) error {
	if n == nil {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v < 0 || v != serverPrice {
		return codes.New(codes.CodeOrderPriceMismatch)
	}
	return nil
}

// skuSpecText 将 SKU 规格取值按展示顺序拼为订单快照文本（如「红色,S」），无规格为空串。
func skuSpecText(values []*skuv1.SkuSpecValue) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		if value := strings.TrimSpace(v.Value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, ",")
}

// insertOrder 在单事务内完成「重读权威数据 → 复核六槽位金额 → 写订单 + 订单项 → 扣库存
// → 预占券 → 清理购物车 → 写创建状态流水」，返回订单 id。
//
// A7/A9：事务内重新读取 skus（FOR UPDATE）与 products，券/会员/运费全部重算，落库金额以复核结果为准；
// 订单状态推进与 order_status_logs 在同一事务内，任一步失败整体回滚。
// 订单 INSERT 命中唯一约束时返回 duplicateKeyErr 供上层分流；预占失败（券不可用）返回 19007 并整体回滚。
func (s *sOrder) insertOrder(
	ctx context.Context,
	userID int64,
	source string,
	addressID int64,
	idempotencyKey, hash string,
	couponID int64,
	orderNo string,
	address *addressv1.Address,
	lines []lineItem,
) (int64, error) {
	var orderID int64
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		authoritative, e := s.reloadLinesInTx(ctx, tx, lines)
		if e != nil {
			return e
		}
		amounts, e := s.computeAmounts(ctx, userID, authoritative, couponID, address.RegionId)
		if e != nil {
			return e
		}

		data := g.Map{
			"order_no":               orderNo,
			"user_id":                userID,
			"status":                 statusPendingPayment,
			"total_amount":           amounts.PayableAmount,
			"original_amount":        amounts.OriginalAmount,
			"discount_amount":        amounts.CouponDiscount,
			"member_discount_amount": amounts.MemberDiscount,
			"shipping_fee":           amounts.ShippingFee,
			"idempotency_key":        idempotencyKey,
			"request_hash":           hash,
			"recipient_name":         address.RecipientName,
			"phone":                  address.Phone,
			"province":               address.Province,
			"city":                   address.City,
			"district":               address.District,
			"detail":                 address.Detail,
			"address_id":             addressID,
			// 支付截止时间用 MySQL NOW() 计算，避免 Go 进程与 MySQL 的时区不一致导致过期判定偏移。
			"expire_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(payTimeout(ctx), 10) + " SECOND)"),
		}
		if couponID > 0 {
			data["user_coupon_id"] = couponID
		}
		id, e := tx.Model("orders").Ctx(ctx).Data(data).InsertAndGetId()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				return &duplicateKeyErr{key: key}
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入订单: %w", e))
		}

		for _, l := range authoritative {
			if _, e := tx.Model("order_items").Ctx(ctx).Data(g.Map{
				"order_id":           id,
				"sku_id":             l.SkuID,
				"sku_code":           l.SkuCode,
				"product_id":         l.ProductID,
				"sku_name":           l.SkuName,
				"spec_text":          l.SpecText,
				"product_name":       l.ProductName,
				"product_main_image": l.ProductMainImage,
				"price":              l.Price,
				"original_price":     l.OriginalPrice,
				"quantity":           l.Quantity,
			}).Insert(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入订单项: %w", e))
			}
		}

		for _, l := range authoritative {
			if _, e := service.Inventory().DeductInTx(ctx, tx, l.SkuID, l.Quantity, nil); e != nil {
				return e
			}
		}

		// 预占券：与订单创建同事务原子完成（available→locked），失败整体回滚（不残留「已预占」）。
		if couponID > 0 {
			if e := service.Coupon().LockInTx(ctx, tx, userID, couponID, id); e != nil {
				return e
			}
		}

		if source == v1.SourceCart {
			skuIDs := make([]int64, 0, len(authoritative))
			for _, l := range authoritative {
				skuIDs = append(skuIDs, l.SkuID)
			}
			if _, e := tx.Model("cart_items").Ctx(ctx).
				Where("user_id", userID).
				WhereIn("sku_id", skuIDs).
				Delete(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("清理已购购物车条目: %w", e))
			}
		}

		// 状态流水：创建事件（0 → 10）与订单写入同事务。
		if e := insertStatusLogInTx(ctx, tx, id, 0, statusPendingPayment, changeTypeCreate, operatorTypeUser, userID, ""); e != nil {
			return e
		}

		orderID = id
		return nil
	})
	if err != nil {
		return 0, err
	}
	return orderID, nil
}

// 数据库状态取值（服务层实体用字符串枚举，事务内复核直接读 DB 整数列）。
const (
	dbSkuEnabled     = 1
	dbProductOnShelf = 1
)

// skuSnapshotRow 是事务内复核用的 SKU 权威行。
type skuSnapshotRow struct {
	Id            int64  `json:"id"`
	SkuCode       string `json:"sku_code"`
	ProductId     int64  `json:"product_id"`
	Name          string `json:"name"`
	Price         int64  `json:"price"`
	OriginalPrice int64  `json:"original_price"`
	Status        int    `json:"status"`
}

// productSnapshotRow 是事务内复核用的商品权威行。
type productSnapshotRow struct {
	Id                int64  `json:"id"`
	Name              string `json:"name"`
	MainImage         string `json:"main_image"`
	Status            int    `json:"status"`
	FreightTemplateId *int64 `json:"freight_template_id"`
}

// reloadLinesInTx 在事务内重新读取权威数据并覆盖行项目快照。
//
// skus 行加 FOR UPDATE 锁，防止「复核价格 → 写入订单」之间被改价；
// products 只读（价格不在商品表参与订单金额），但状态、名称、主图与运费模板必须取当前值。
func (s *sOrder) reloadLinesInTx(ctx context.Context, tx gdb.TX, lines []lineItem) ([]lineItem, error) {
	if len(lines) == 0 {
		return nil, codes.New(codes.CodeOrderCartEmpty)
	}
	skuIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		skuIDs = append(skuIDs, l.SkuID)
	}
	var skuRows []*skuSnapshotRow
	if err := tx.Model("skus").Ctx(ctx).
		Fields("id", "sku_code", "product_id", "name", "price", "original_price", "status").
		WhereIn("id", skuIDs).Order("id ASC").LockUpdate().Scan(&skuRows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("复核 SKU 权威价格: %w", err))
	}
	skuByID := make(map[int64]*skuSnapshotRow, len(skuRows))
	productIDs := make([]int64, 0, len(skuRows))
	for _, row := range skuRows {
		skuByID[row.Id] = row
		productIDs = append(productIDs, row.ProductId)
	}
	var productRows []*productSnapshotRow
	if err := tx.Model("products").Ctx(ctx).
		Fields("id", "name", "main_image", "status", "freight_template_id").
		WhereIn("id", productIDs).Scan(&productRows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("复核商品状态: %w", err))
	}
	productByID := make(map[int64]*productSnapshotRow, len(productRows))
	for _, row := range productRows {
		productByID[row.Id] = row
	}

	out := make([]lineItem, 0, len(lines))
	for _, l := range lines {
		sku, ok := skuByID[l.SkuID]
		if !ok {
			return nil, codes.New(codes.CodeSkuNotFound)
		}
		if sku.Status != dbSkuEnabled {
			return nil, codes.New(codes.CodeOrderSkuUnavailable)
		}
		product, ok := productByID[sku.ProductId]
		if !ok || product.Status != dbProductOnShelf {
			return nil, codes.New(codes.CodeOrderSkuUnavailable)
		}
		updated := l
		updated.SkuCode = sku.SkuCode
		updated.SkuName = sku.Name
		updated.Price = sku.Price
		updated.OriginalPrice = sku.OriginalPrice
		updated.ProductID = sku.ProductId
		updated.ProductName = product.Name
		updated.ProductMainImage = product.MainImage
		updated.FreightTemplateID = product.FreightTemplateId
		out = append(out, updated)
	}
	return out, nil
}

// insertStatusLogInTx 在调用方事务内追加一条订单状态流水（append-only）。
func insertStatusLogInTx(ctx context.Context, tx gdb.TX, orderID int64, from, to int, changeType string, operatorType int, operatorID int64, remark string) error {
	if orderID <= 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("订单状态流水 order_id 非法: %d", orderID))
	}
	if _, err := tx.Model("order_status_logs").Ctx(ctx).Data(g.Map{
		"order_id":      orderID,
		"from_status":   from,
		"to_status":     to,
		"change_type":   changeType,
		"operator_type": operatorType,
		"operator_id":   operatorID,
		"remark":        remark,
	}).Insert(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入订单状态流水: %w", err))
	}
	return nil
}

// handleIdempotency 读回既有订单：同请求指纹返回既有订单（幂等成功），否则 9006。
// replayConsumedCartOrder 处理「购物车来源下单成功后再用同一幂等键重放」的场景。
//
// 幂等键命中且请求的地址/优惠券与既有订单一致 → 返回既有订单（不重复下单）；
// 键不存在 → 返回 (nil, nil)，调用方保留原本的 9004；
// 键存在但地址/券不同 → 9006（同键不同内容）。
//
// 说明：购物车行项目已被下单事务删除，无法重算完整 request_hash，
// 因此这里用订单快照中的 address_id / user_coupon_id 做一致性校验。
func (s *sOrder) replayConsumedCartOrder(
	ctx context.Context, userID int64, key string, req *v1.CreateReq,
) (*v1.CreateRes, error) {
	row, err := s.findByIdempotencyKey(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}

	storedAddressID := int64(0)
	if row.AddressId != nil {
		storedAddressID = *row.AddressId
	}
	storedCouponID := int64(0)
	if row.UserCouponId != nil {
		storedCouponID = *row.UserCouponId
	}
	if storedAddressID != req.AddressId || storedCouponID != req.CouponId {
		return nil, codes.New(codes.CodeOrderIdempotencyConflict)
	}

	o, err := s.toOrder(ctx, row)
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Order: *o}, nil
}

func (s *sOrder) handleIdempotency(ctx context.Context, userID int64, key, hash string) (*v1.CreateRes, error) {
	row, err := s.findByIdempotencyKey(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("幂等键命中但未找到既有订单"))
	}
	if row.RequestHash != hash {
		return nil, codes.New(codes.CodeOrderIdempotencyConflict)
	}
	o, err := s.toOrder(ctx, row)
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Order: *o}, nil
}

// List 查询本人订单列表（按 id 倒序，含订单项）。
func (s *sOrder) List(ctx context.Context, userID int64) (*v1.ListRes, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("user_id", userID).Order("id DESC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单列表: %w", err))
	}
	items := make([]*v1.Order, 0, len(rows))
	for _, r := range rows {
		o, err := s.toOrder(ctx, r)
		if err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return &v1.ListRes{Items: items}, nil
}

// Detail 查询本人订单详情；不存在或非本人统一 9001。待支付且已过期时先懒取消再返回。
func (s *sOrder) Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	if row.Status == statusPendingPayment {
		if _, err := s.cancelExpired(ctx, id); err != nil {
			return nil, err
		}
	}
	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.DetailRes{Order: *o}, nil
}

// Pay 支付：仅待支付→已支付合法，幂等；已过期订单先懒取消。
//
// payType=mock（默认）只推进状态；payType=balance 在「订单 10→20」同一事务内调用钱包扣款原语，
// 余额不足返回 22002 且订单仍为待支付；重复支付走幂等快路径，不重复扣款。
func (s *sOrder) Pay(ctx context.Context, userID, id int64, payType string) (*v1.PayRes, error) {
	payType = strings.TrimSpace(payType)
	if payType == "" {
		payType = v1.PayTypeMock
	}
	if payType != v1.PayTypeMock && payType != v1.PayTypeBalance {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	if row.Status == statusPendingPayment {
		// 懒取消：待支付且已过期（expire_at < NOW()，SQL 判定，避免 Go↔MySQL 时区漂移）。
		cancelled, e := s.cancelExpired(ctx, id)
		if e != nil {
			return nil, e
		}
		if cancelled {
			return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
		}
	}

	switch row.Status {
	case statusPaid:
		// 重复支付：幂等成功，不重复改状态。
		o, err := s.toOrder(ctx, row)
		if err != nil {
			return nil, err
		}
		return &v1.PayRes{Order: *o}, nil
	case statusPendingPayment:
		// 事务：订单 10→20（条件更新）+ 钱包扣款（余额渠道）+ 券核销 + 会员成长值 + 一级分销佣金
		// + 状态流水，全部原子完成；任一步失败整体回滚（订单保持待支付，可重试）。
		err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
			data := g.Map{
				"status":   statusPaid,
				"paid_at":  gdb.Raw("NOW()"),
				"pay_type": payType,
			}
			result, e := tx.Model("orders").Ctx(ctx).
				Where("id", id).
				Where("user_id", userID).
				Where("status", statusPendingPayment).
				Data(data).
				Update()
			if e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("支付订单: %w", e))
			}
			if affected, _ := result.RowsAffected(); affected == 0 {
				return codes.New(codes.CodeOrderInvalidStatusTransition)
			}
			// 钱包余额支付：与订单状态推进同事务，余额不足（22002）时整体回滚。
			if payType == v1.PayTypeBalance && row.TotalAmount > 0 {
				fund, e := service.Wallet().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{
					UserID:  userID,
					OrderID: id,
					Amount:  row.TotalAmount,
				})
				if e != nil {
					return e
				}
				if fund != nil && fund.TransactionID > 0 {
					if _, e := tx.Model("orders").Ctx(ctx).
						Where("id", id).
						Data(g.Map{"transaction_no": fmt.Sprintf("wallet:%d", fund.TransactionID)}).
						Update(); e != nil {
						return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入支付流水号: %w", e))
					}
				}
			}
			if row.UserCouponId != nil {
				if e := service.Coupon().MarkUsedInTx(ctx, tx, *row.UserCouponId); e != nil {
					return e
				}
			}
			// 会员成长值：成长值流水幂等键 growth:order_reward:<order_id>，重复支付不会重复发放。
			if e := service.Member().AccrueForOrderInTx(ctx, tx, id); e != nil {
				return e
			}
			// 一级分销计佣：与「订单 10→20」同事务提交。买家无直接推荐人或未配置比例时 no-op；
			// 重复支付走上方幂等快路径不进本事务，uk_order_id 再兜底一次；计佣失败整体回滚，
			// 不产生「订单已支付但佣金缺失/错账」的中间态（见 docs/design/distribution.md §3.2）。
			if e := service.Distribution().AccrueOrderCommissionInTx(ctx, tx, id); e != nil {
				return e
			}
			if e := insertStatusLogInTx(ctx, tx, id, statusPendingPayment, statusPaid, changeTypePay, operatorTypeUser, userID, payType); e != nil {
				return e
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		o, err := s.loadOwnedOrder(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if o == nil {
			return nil, codes.New(codes.CodeOrderNotFound)
		}
		logx.Info(ctx, "order_paid", "支付成功", logx.Fields{
			"user_id":  userID,
			"order_id": o.Id,
			"order_no": o.OrderNo,
		})
		return &v1.PayRes{Order: *o}, nil
	default:
		return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
	}
}

// Cancel 取消本人待支付订单：条件更新 + 恢复库存，原子；非待支付返回 9002。
func (s *sOrder) Cancel(ctx context.Context, userID, id int64) (*v1.CancelRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	var changed bool
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var e error
		changed, e = s.cancelInTx(ctx, tx, id, cancelReasonUser, false, userID)
		return e
	})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
	}

	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.CancelRes{Order: *o}, nil
}

// Receive 确认收货：已发货→已收货→已完成（同请求同事务原子推进）。
func (s *sOrder) Receive(ctx context.Context, userID, id int64) (*v1.ReceiveRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := tx.Model("orders").Ctx(ctx).
			Where("id", id).
			Where("user_id", userID).
			Where("status", statusShipped).
			Data(g.Map{"status": statusReceived, "received_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("确认收货: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		if _, e := tx.Model("orders").Ctx(ctx).
			Where("id", id).
			Where("status", statusReceived).
			Data(g.Map{"status": statusCompleted, "completed_at": gdb.Raw("NOW()")}).
			Update(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("完成订单: %w", e))
		}
		if e := insertStatusLogInTx(ctx, tx, id, statusShipped, statusReceived, changeTypeReceive, operatorTypeUser, userID, ""); e != nil {
			return e
		}
		if e := insertStatusLogInTx(ctx, tx, id, statusReceived, statusCompleted, changeTypeComplete, operatorTypeUser, userID, ""); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.ReceiveRes{Order: *o}, nil
}

// Ship 发货（仅管理员）：已支付→已发货。
// 事务内锁定订单行，与「发起退款申请」「退款成功」经同一 orders 行锁串行化；
// 存在有效退款申请（status ∈ {10,20,30}）时拒绝发货（退款与发货互斥）。
func (s *sOrder) Ship(ctx context.Context, orderID int64) (*v1.ShipRes, error) {
	row, err := s.findByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var locked []*orderRow
		if e := tx.Model("orders").Ctx(ctx).Where("id", orderID).LockUpdate().Scan(&locked); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定订单: %w", e))
		}
		if len(locked) == 0 {
			return codes.New(codes.CodeOrderNotFound)
		}
		if locked[0].Status != statusPaid {
			return codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		has, e := service.Refund().HasActiveRequestInTx(ctx, tx, orderID)
		if e != nil {
			return e
		}
		if has {
			return codes.New(codes.CodeRefundShipConflict)
		}
		result, e := tx.Model("orders").Ctx(ctx).
			Where("id", orderID).
			Where("status", statusPaid).
			Data(g.Map{"status": statusShipped, "shipped_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("发货: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		if e := insertStatusLogInTx(ctx, tx, orderID, statusPaid, statusShipped, changeTypeShip, operatorTypeAdmin, 0, ""); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	o, err := s.loadByIDOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.ShipRes{Order: *o}, nil
}

// CompleteRefundInTx 在既有事务内完成退款推进：订单 paid(20)→refunded(70) + refunded_at + 恢复库存。
// WHERE status=20 闸门保证「只补偿一次」；0 行返回明确错误供退款模块整体回滚。
// 供退款模块在「执行退款成功」同事务中复用，不复制订单状态迁移与库存恢复业务规则。
func (s *sOrder) CompleteRefundInTx(ctx context.Context, tx gdb.TX, orderID int64) error {
	result, err := tx.Model("orders").Ctx(ctx).
		Where("id", orderID).
		Where("status", statusPaid).
		Data(g.Map{"status": statusRefunded, "refunded_at": gdb.Raw("NOW()")}).
		Update()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("退款推进订单: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return codes.New(codes.CodeOrderInvalidStatusTransition)
	}
	items, err := s.loadItemsInTx(ctx, tx, orderID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := service.Inventory().IncreaseInTx(ctx, tx, it.SkuId, it.Quantity, nil); err != nil {
			return err
		}
	}
	// 状态流水：退款成功推进订单 20→70，与库存恢复同事务。
	if err := insertStatusLogInTx(ctx, tx, orderID, statusPaid, statusRefunded, changeTypeRefund, operatorTypeAdmin, 0, ""); err != nil {
		return err
	}
	return nil
}

// CreateGroupBuyInTx 在既有事务内创建拼团订单（orders + order_items + 扣库存 + 状态流水）。
//
// 金额语义（接入统一计价，不另立第二套公式）：
//   - original_amount 仍为事务内重读的 skus.price × quantity（服务端权威价）；
//   - 拼团价与现价的差额作为优惠额进入 pricing.Quote 的 CouponDiscount 槽位，
//     落库为 orders.discount_amount（既有「原价 - 优惠 = 应付」语义）；
//   - order_items.price 为拼团成交价、original_price 为权威原价，订单快照自足；
//   - 会员折扣不叠加（拼团价即成交价），运费按商品运费模板与收货地区实时计算并计入应付。
//
// 若 SKU 现价已低于拼团价（活动创建后调价），拒绝下单（25004），不产生负优惠。
func (s *sOrder) CreateGroupBuyInTx(ctx context.Context, tx gdb.TX, in *service.GroupBuyOrderInput) (int64, error) {
	if in == nil || in.UserID <= 0 || in.AddressID <= 0 || in.SkuID <= 0 {
		return 0, codes.New(codes.CodeInvalidArgument)
	}
	if in.Quantity < 1 || in.Quantity > maxOrderQuantity {
		return 0, codes.New(codes.CodeOrderInvalidQuantity)
	}
	if in.GroupPrice <= 0 {
		return 0, codes.New(codes.CodeGroupBuyPriceInvalid)
	}
	idempotencyKey := strings.TrimSpace(in.IdempotencyKey)
	if idempotencyKey == "" {
		return 0, codes.New(codes.CodeInvalidArgument)
	}

	address, err := service.Address().Detail(ctx, in.UserID, in.AddressID)
	if err != nil {
		return 0, err
	}

	if in.GroupPrice > 0 {
		// 事务内 FOR UPDATE 重读 SKU/商品，价格以数据库为准。
		authoritative, e := s.reloadLinesInTx(ctx, tx, []lineItem{{SkuID: in.SkuID, Quantity: in.Quantity}})
		if e != nil {
			if c := codes.FromError(e); c == codes.CodeOrderSkuUnavailable || c == codes.CodeSkuNotFound {
				return 0, codes.New(codes.CodeGroupBuySkuUnavailable)
			}
			return 0, e
		}
		line := authoritative[0]
		if in.GroupPrice > line.Price {
			return 0, codes.New(codes.CodeGroupBuyPriceInvalid)
		}
		groupDiscount := (line.Price - in.GroupPrice) * in.Quantity
		freight, e := s.freightFee(ctx, authoritative, address.RegionId)
		if e != nil {
			return 0, e
		}
		quote, e := pricing.Quote(ctx, &pricing.QuoteRequest{
			OriginalAmount: line.Price * in.Quantity,
			CouponDiscount: groupDiscount,
			MemberDiscount: 0,
			PointsDiscount: 0,
			ShippingFee:    freight,
		})
		if e != nil {
			return 0, e
		}
		hash := groupBuyRequestHash(in.SkuID, in.Quantity, in.GroupPrice)

		var orderID int64
		for attempt := 0; attempt < maxOrderNoRetry; attempt++ {
			data := g.Map{
				"order_no":               generateOrderNo(),
				"user_id":                in.UserID,
				"status":                 statusPendingPayment,
				"total_amount":           quote.PayableAmount,
				"original_amount":        quote.OriginalAmount,
				"discount_amount":        quote.CouponDiscount,
				"member_discount_amount": quote.MemberDiscount,
				"shipping_fee":           quote.ShippingFee,
				"idempotency_key":        idempotencyKey,
				"request_hash":           hash,
				"recipient_name":         address.RecipientName,
				"phone":                  address.Phone,
				"province":               address.Province,
				"city":                   address.City,
				"district":               address.District,
				"detail":                 address.Detail,
				"address_id":             in.AddressID,
				// 与普通订单一致：支付截止用 MySQL NOW() 计算，消除 Go↔MySQL 时区漂移。
				"expire_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(payTimeout(ctx), 10) + " SECOND)"),
			}
			id, e := tx.Model("orders").Ctx(ctx).Data(data).InsertAndGetId()
			if e == nil {
				orderID = id
				break
			}
			key := duplicateKeyName(e)
			switch {
			case strings.Contains(key, "uk_user_idempotency"):
				// 同一幂等键已被使用：拼团侧应先命中参与记录，走到这里说明请求内容冲突或跨团复用键。
				return 0, codes.New(codes.CodeOrderIdempotencyConflict)
			case strings.Contains(key, "uk_order_no"):
				continue // 撞号，重生成订单号重试
			default:
				return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入拼团订单: %w", e))
			}
		}
		if orderID == 0 {
			return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成拼团订单号重试失败"))
		}

		// 订单项成交价使用拼团价：订单快照自足，后续活动改价不影响已生成订单。
		if _, e := tx.Model("order_items").Ctx(ctx).Data(g.Map{
			"order_id":           orderID,
			"sku_id":             line.SkuID,
			"sku_code":           line.SkuCode,
			"product_id":         line.ProductID,
			"sku_name":           line.SkuName,
			"spec_text":          line.SpecText,
			"product_name":       line.ProductName,
			"product_main_image": line.ProductMainImage,
			"price":              in.GroupPrice,
			"original_price":     line.OriginalPrice,
			"quantity":           line.Quantity,
		}).Insert(); e != nil {
			return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入拼团订单项: %w", e))
		}

		if _, e := service.Inventory().DeductInTx(ctx, tx, line.SkuID, line.Quantity, nil); e != nil {
			return 0, e
		}
		// 状态流水与订单创建同事务（与普通订单一致）。
		if e := insertStatusLogInTx(ctx, tx, orderID, 0, statusPendingPayment, changeTypeCreate, operatorTypeUser, in.UserID, ""); e != nil {
			return 0, e
		}
		return orderID, nil
	}
	return 0, codes.New(codes.CodeGroupBuyPriceInvalid)
}

// CancelInTx 在既有事务内取消待支付订单（status=10→60 + 恢复库存 + 释放券）。
// 复用 cancelInTx，不复制订单状态迁移与库存恢复规则；返回 false 表示订单已非待支付（幂等无副作用）。
func (s *sOrder) CancelInTx(ctx context.Context, tx gdb.TX, orderID int64) (bool, error) {
	return s.cancelInTx(ctx, tx, orderID, cancelReasonTimeout, false, 0)
}

// groupBuyRequestHash 计算拼团订单请求指纹：sha256hex(group_buy|sku|qty|group_price)。
// 订单本身不落拼团 id，指纹只用于同幂等键的内容一致性判定。
func groupBuyRequestHash(skuID, quantity, groupPrice int64) string {
	raw := "group_buy|" + strconv.FormatInt(skuID, 10) +
		"|" + strconv.FormatInt(quantity, 10) +
		"|" + strconv.FormatInt(groupPrice, 10)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CancelExpired 取消过期的待支付订单（供后台扫描器复用），逐单原子取消，返回本次实际取消数量。
// 单单失败不影响其他订单与后续轮次。
func (s *sOrder) CancelExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).
		Fields("id").
		Where("status", statusPendingPayment).
		Where("expire_at < NOW()").
		Limit(limit).
		Scan(&rows); err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("扫描过期订单: %w", err))
	}
	count := 0
	for _, r := range rows {
		cancelled, err := s.cancelExpired(ctx, r.Id)
		if err != nil {
			logx.Warning(ctx, "order_cancel_expired_failed", "超时取消订单失败", logx.Fields{
				"order_id": r.Id,
				"error":    err.Error(),
			})
			continue
		}
		if cancelled {
			count++
		}
	}
	return count, nil
}

// cancelExpired 原子取消「待支付且已过期」的订单（含库存恢复）；未过期/已非待支付返回 false。
func (s *sOrder) cancelExpired(ctx context.Context, orderID int64) (bool, error) {
	var changed bool
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var e error
		changed, e = s.cancelInTx(ctx, tx, orderID, cancelReasonTimeout, true, 0)
		return e
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// cancelInTx 在事务内原子取消订单：条件更新 status=10 → 60 + 恢复库存；仅命中者恢复一次库存。
// onlyExpired=true 时额外要求 expire_at < NOW()（SQL 判定，避免 Go↔MySQL 时区漂移）。
func (s *sOrder) cancelInTx(ctx context.Context, tx gdb.TX, orderID int64, reason int, onlyExpired bool, operatorID int64) (bool, error) {
	model := tx.Model("orders").Ctx(ctx).
		Where("id", orderID).
		Where("status", statusPendingPayment)
	if onlyExpired {
		model = model.Where("expire_at < NOW()")
	}
	result, err := model.Data(g.Map{
		"status":        statusCancelled,
		"cancel_reason": reason,
		"cancelled_at":  gdb.Raw("NOW()"),
	}).Update()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消订单: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return false, nil
	}
	items, err := s.loadItemsInTx(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	for _, it := range items {
		if _, err := service.Inventory().IncreaseInTx(ctx, tx, it.SkuId, it.Quantity, nil); err != nil {
			return false, err
		}
	}
	// 释放占用的券（locked→available，清空 order_id/locked_at）；订单未占券时为 no-op。
	if err := service.Coupon().ReleaseInTx(ctx, tx, orderID); err != nil {
		return false, err
	}
	// 状态流水：取消事件与状态变更、库存恢复、券释放同事务。
	changeType := changeTypeCancel
	operatorType := operatorTypeUser
	if reason == cancelReasonTimeout {
		changeType = changeTypeTimeoutCancel
		operatorType = operatorTypeSystem
		operatorID = 0
	}
	if err := insertStatusLogInTx(ctx, tx, orderID, statusPendingPayment, statusCancelled, changeType, operatorType, operatorID, ""); err != nil {
		return false, err
	}
	return true, nil
}

// findOwned 按 id + user_id 查询订单行，未命中返回 nil（区分不存在与非本人不在此层）。
func (s *sOrder) findOwned(ctx context.Context, userID, id int64) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("id", id).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findByID 按 id 查询订单行（后台无归属过滤），未命中返回 nil。
func (s *sOrder) findByID(ctx context.Context, id int64) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findByIdempotencyKey 按 user_id + idempotency_key 查询订单行，未命中返回 nil。
func (s *sOrder) findByIdempotencyKey(ctx context.Context, userID int64, key string) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("user_id", userID).Where("idempotency_key", key).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按幂等键查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadOwnedOrder 按 id + user_id 加载完整订单（含订单项），未命中返回 nil。
func (s *sOrder) loadOwnedOrder(ctx context.Context, userID, id int64) (*v1.Order, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return s.toOrder(ctx, row)
}

// loadByIDOrder 按 id 加载完整订单（后台，含订单项），未命中返回 nil。
func (s *sOrder) loadByIDOrder(ctx context.Context, id int64) (*v1.Order, error) {
	row, err := s.findByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return s.toOrder(ctx, row)
}

// loadItems 查询订单项（按 id 升序）。
func (s *sOrder) loadItems(ctx context.Context, orderID int64) ([]*v1.OrderItem, error) {
	var rows []*orderItemRow
	if err := g.DB().Model("order_items").Ctx(ctx).Where("order_id", orderID).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单项: %w", err))
	}
	return toOrderItems(rows), nil
}

// loadItemsInTx 在事务内查询订单项（供取消/退款恢复库存使用）。
func (s *sOrder) loadItemsInTx(ctx context.Context, tx gdb.TX, orderID int64) ([]*orderItemRow, error) {
	var rows []*orderItemRow
	if err := tx.Model("order_items").Ctx(ctx).Where("order_id", orderID).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单项: %w", err))
	}
	return rows, nil
}

// toOrder 将订单行（含订单项）转换为对外结构。
func (s *sOrder) toOrder(ctx context.Context, r *orderRow) (*v1.Order, error) {
	items, err := s.loadItems(ctx, r.Id)
	if err != nil {
		return nil, err
	}
	return &v1.Order{
		Id:                   r.Id,
		OrderNo:              r.OrderNo,
		UserId:               r.UserId,
		Status:               statusToString(r.Status),
		TotalAmount:          r.TotalAmount,
		OriginalAmount:       r.OriginalAmount,
		DiscountAmount:       r.DiscountAmount,
		MemberDiscountAmount: r.MemberDiscountAmount,
		ShippingFee:          r.ShippingFee,
		PayType:              r.PayType,
		UserCouponId:         r.UserCouponId,
		IdempotencyKey:       r.IdempotencyKey,
		RecipientName:        r.RecipientName,
		Phone:                r.Phone,
		Province:             r.Province,
		City:                 r.City,
		District:             r.District,
		Detail:               r.Detail,
		AddressId:            r.AddressId,
		ExpireAt:             r.ExpireAt,
		CancelReason:         r.CancelReason,
		PaidAt:               r.PaidAt,
		ShippedAt:            r.ShippedAt,
		ReceivedAt:           r.ReceivedAt,
		CompletedAt:          r.CompletedAt,
		CancelledAt:          r.CancelledAt,
		RefundedAt:           r.RefundedAt,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		Items:                items,
	}, nil
}

// toOrderItems 将订单项行转换为对外结构。
func toOrderItems(rows []*orderItemRow) []*v1.OrderItem {
	items := make([]*v1.OrderItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.OrderItem{
			Id:               r.Id,
			SkuId:            r.SkuId,
			SkuCode:          r.SkuCode,
			ProductId:        r.ProductId,
			SkuName:          r.SkuName,
			SpecText:         r.SpecText,
			ProductName:      r.ProductName,
			ProductMainImage: r.ProductMainImage,
			Price:            r.Price,
			OriginalPrice:    r.OriginalPrice,
			Quantity:         r.Quantity,
		})
	}
	return items
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	switch s {
	case statusPaid:
		return v1.StatusPaid
	case statusShipped:
		return v1.StatusShipped
	case statusReceived:
		return v1.StatusReceived
	case statusCompleted:
		return v1.StatusCompleted
	case statusCancelled:
		return v1.StatusCancelled
	case statusRefunded:
		return v1.StatusRefunded
	default:
		return v1.StatusPendingPayment
	}
}

// requestHash 计算请求指纹：sha256hex(source|address_id|coupon_id|sku_id:qty...（按 sku_id 升序）)。
// coupon_id 缺省为 0（无券），使同一幂等键下「是否用券/用哪张券」参与内容一致性判定（INV-C10）。
func requestHash(source string, address addressv1.Address, couponID int64, lines []lineItem) string {
	sorted := make([]lineItem, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SkuID < sorted[j].SkuID })

	var b strings.Builder
	b.WriteString(source)
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(address.Id, 10))
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(couponID, 10))
	// 地址内容参与指纹：同一 address_id 被修改后，同幂等键再次提交必须识别为「不同请求」（9006）。
	b.WriteByte('|')
	b.WriteString(address.RecipientName)
	b.WriteByte('|')
	b.WriteString(address.Phone)
	b.WriteByte('|')
	b.WriteString(address.Province)
	b.WriteByte('|')
	b.WriteString(address.City)
	b.WriteByte('|')
	b.WriteString(address.District)
	b.WriteByte('|')
	b.WriteString(address.Detail)
	if address.RegionId != nil {
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(*address.RegionId, 10))
	}
	for _, l := range sorted {
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(l.SkuID, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(l.Quantity, 10))
		// 成交价与规格参与指纹：SKU 改价/换规格后，同键同商品也视为不同下单请求。
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(l.Price, 10))
		b.WriteByte(':')
		b.WriteString(l.SkuCode)
		b.WriteByte(':')
		b.WriteString(l.SpecText)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// generateOrderNo 生成订单号：毫秒时间戳 + 随机段（12 hex），uk_order_no 兜底撞号重试。
func generateOrderNo() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + hex.EncodeToString(b[:])
}

// payTimeout 返回支付超时时长（秒，默认 900），不硬编码。
//
// 配置非法时记录 Error 级日志并按代码默认值处理：该值在创建订单的 SQL 片段中内联使用，
// 调用签名不携带 error；启动期的 config.Validate 已把非法配置拦成启动失败。
func payTimeout(ctx context.Context) int64 {
	seconds, err := config.Int64(ctx, config.KeyOrderPayTimeout)
	if err != nil {
		logx.Error(ctx, "order_config_read_failed", "读取订单支付超时失败，按代码默认值处理", logx.Fields{
			"config_key": string(config.KeyOrderPayTimeout),
			"error":      err.Error(),
		})
		return defaultPayTimeoutSeconds
	}
	if seconds <= 0 {
		return defaultPayTimeoutSeconds
	}
	return seconds
}

// duplicateKeyName 从错误链中提取 MySQL 唯一约束冲突（1062）的键名，非重复键错误返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}

// 后台订单列表的排序字段白名单（防止任意 SQL 排序字段注入）。
var adminOrderSortColumns = map[string]string{
	"id":         "id",
	"created_at": "created_at",
}

// 后台订单列表分页与排序默认值/上限。
const (
	adminDefaultPage  = 1
	adminDefaultSize  = 20
	adminMaxSize      = 100
	adminDefaultSort  = "id"
	adminDefaultOrder = "desc"
)

// adminStatusOrder 是统计结果使用的全状态顺序（DB TINYINT 升序，覆盖 7 态）。
var adminStatusOrder = []int{
	statusPendingPayment, statusPaid, statusShipped, statusReceived,
	statusCompleted, statusCancelled, statusRefunded,
}

// AdminList 后台分页查询全部普通订单（轻量，不含明细），支持组合筛选与稳定排序。
// 与用户端 List 不同：无归属过滤、MySQL 侧分页（LIMIT/OFFSET）、不逐单加载明细（无 N+1）。
func (s *sOrder) AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error) {
	page, size, err := s.adminPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	sortCol, direction, err := s.adminSortParams(req.Sort, req.Order)
	if err != nil {
		return nil, err
	}

	model := g.DB().Model("orders").Ctx(ctx)
	if orderNo := strings.TrimSpace(req.OrderNo); orderNo != "" {
		model = model.Where("order_no", orderNo)
	}
	if req.UserId != nil {
		if *req.UserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("user_id", *req.UserId)
	}
	if status := strings.TrimSpace(req.Status); status != "" {
		st, e := statusFromString(status)
		if e != nil {
			return nil, e
		}
		model = model.Where("status", st)
	}
	startUnix, endUnix, err := s.adminTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	if startUnix != nil {
		model = model.Where("created_at >= FROM_UNIXTIME(?)", *startUnix)
	}
	if endUnix != nil {
		model = model.Where("created_at < FROM_UNIXTIME(?)", *endUnix)
	}

	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计订单: %w", err))
	}

	// 列表保持轻量：仅取列表所需 6 字段，避免把 idempotency_key/地址快照等一并取出。
	orderBy := sortCol + " " + direction
	if sortCol != "id" {
		orderBy += ", id " + direction // id 作稳定 tiebreak，避免翻页重复/遗漏。
	}
	var rows []*orderRow
	if err := model.Fields("id", "order_no", "user_id", "status", "total_amount", "created_at").
		Order(orderBy).Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单列表: %w", err))
	}

	items := make([]*v1.AdminOrderListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.AdminOrderListItem{
			Id:          r.Id,
			OrderNo:     r.OrderNo,
			UserId:      r.UserId,
			Status:      statusToString(r.Status),
			TotalAmount: r.TotalAmount,
			CreatedAt:   r.CreatedAt,
		})
	}
	return &v1.AdminListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// AdminStats 后台按 status 聚合全部普通订单数量，覆盖完整 7 态（无数据为 0），
// total 等于各状态数量之和；仅作用于 orders 表，天然不混入秒杀订单。
func (s *sOrder) AdminStats(ctx context.Context) (*v1.AdminOrderStatsRes, error) {
	var rows []struct {
		Status int   `json:"status"`
		Count  int64 `json:"count"`
	}
	if err := g.DB().Model("orders").Ctx(ctx).
		Fields("status", "COUNT(*) AS count").
		Group("status").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计订单状态: %w", err))
	}

	counts := make(map[int]int64, len(rows))
	var total int64
	for _, r := range rows {
		counts[r.Status] = r.Count
		total += r.Count
	}

	stats := make([]*v1.AdminOrderStatsItem, 0, len(adminStatusOrder))
	for _, st := range adminStatusOrder {
		stats = append(stats, &v1.AdminOrderStatsItem{
			Status: statusToString(st),
			Count:  counts[st],
		})
	}
	return &v1.AdminOrderStatsRes{Total: total, Stats: stats}, nil
}

// AdminDetail 后台按 id 查询任意普通订单详情（含明细与历史快照），不存在返回 9001。
// 复用 findByID/loadItems/toOrder，无用户端 findOwned 归属过滤，也不触发懒取消（纯只读）。
func (s *sOrder) AdminDetail(ctx context.Context, id int64) (*v1.AdminOrderDetailRes, error) {
	o, err := s.loadByIDOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.AdminOrderDetailRes{Order: *o}, nil
}

// adminPageParams 校验分页参数：缺失取默认值，page<1 或 size<1/size>100 返回 400（拒绝而非钳制）。
func (s *sOrder) adminPageParams(page, size *int) (int, int, error) {
	p := adminDefaultPage
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	sz := adminDefaultSize
	if size != nil {
		if *size < 1 || *size > adminMaxSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		sz = *size
	}
	return p, sz, nil
}

// adminSortParams 校验排序参数：sort 白名单、order 取 asc/desc；非法返回 400。
func (s *sOrder) adminSortParams(sortField, order string) (string, string, error) {
	col := adminDefaultSort
	if f := strings.TrimSpace(sortField); f != "" {
		if _, ok := adminOrderSortColumns[f]; !ok {
			return "", "", codes.New(codes.CodeInvalidArgument)
		}
		col = f
	}
	direction := "DESC"
	if d := strings.TrimSpace(order); d != "" {
		switch strings.ToLower(d) {
		case "asc":
			direction = "ASC"
		case "desc":
			direction = "DESC"
		default:
			return "", "", codes.New(codes.CodeInvalidArgument)
		}
	}
	return col, direction, nil
}

// adminTimeRange 解析时间范围：RFC3339（必须含时区偏移）→ Unix 秒；非法或 start>=end 返回 400。
func (s *sOrder) adminTimeRange(start, end string) (*int64, *int64, error) {
	var startUnix, endUnix *int64
	if s0 := strings.TrimSpace(start); s0 != "" {
		ts, err := parseRFC3339ToUnix(s0)
		if err != nil {
			return nil, nil, codes.New(codes.CodeInvalidArgument)
		}
		startUnix = &ts
	}
	if e0 := strings.TrimSpace(end); e0 != "" {
		ts, err := parseRFC3339ToUnix(e0)
		if err != nil {
			return nil, nil, codes.New(codes.CodeInvalidArgument)
		}
		endUnix = &ts
	}
	if startUnix != nil && endUnix != nil && *startUnix >= *endUnix {
		return nil, nil, codes.New(codes.CodeInvalidArgument)
	}
	return startUnix, endUnix, nil
}

// parseRFC3339ToUnix 将 RFC3339（含时区偏移）字符串解析为绝对时刻的 Unix 秒。
// 缺偏移或非法格式返回错误，由上层映射为 400。
func parseRFC3339ToUnix(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// statusFromString 将 API 字符串枚举映射回 DB TINYINT（statusToString 的反函数）。
// 未知状态返回错误，由上层映射为 400。
func statusFromString(s string) (int, error) {
	switch s {
	case v1.StatusPendingPayment:
		return statusPendingPayment, nil
	case v1.StatusPaid:
		return statusPaid, nil
	case v1.StatusShipped:
		return statusShipped, nil
	case v1.StatusReceived:
		return statusReceived, nil
	case v1.StatusCompleted:
		return statusCompleted, nil
	case v1.StatusCancelled:
		return statusCancelled, nil
	case v1.StatusRefunded:
		return statusRefunded, nil
	default:
		return 0, codes.New(codes.CodeInvalidArgument)
	}
}
