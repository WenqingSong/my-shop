// Package pricing 是全项目唯一的订单计价入口。
//
// 设计目标：订单、积分、会员折扣、拼团与砍价等所有涉及「订单金额如何得出」的业务，
// 必须围绕本入口扩展，禁止在各自模块内复制一套金额公式。
//
// 明细槽位固定有序（顺序即扣减顺序，不得重排）：
//
//	OriginalAmount 商品原始金额
//	  ↓ CouponDiscount 优惠券折扣
//	  ↓ MemberDiscount 会员折扣（本批次不产生业务规则，恒 0）
//	  ↓ PointsDiscount 积分抵扣（本批次不产生业务规则，恒 0）
//	  + ShippingFee    运费
//	= PayableAmount  应付金额
//
// 金额一律为整数分（int64），禁止 float64/decimal 参与计算。
// 不变量：PayableAmount = OriginalAmount - CouponDiscount - MemberDiscount - PointsDiscount + ShippingFee，
// 且各项非负、优惠累计不超过商品金额、应付不为负、全程无整数溢出。
package pricing

import (
	"context"
	"math"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// MaxTotalAmount 是单笔订单应付金额上限（整数分），与 orders 表 INT UNSIGNED 列可容纳区间一致。
// 超过该值写入数据库必然失败，因此在计价阶段就拒绝，避免事务提交时才发现。
const MaxTotalAmount int64 = math.MaxUint32

// Breakdown 是计价明细。字段顺序即计算顺序，JSON 字段名对外稳定。
type Breakdown struct {
	OriginalAmount int64 `json:"original_amount" dc:"商品原始金额（整数分）"`
	CouponDiscount int64 `json:"coupon_discount" dc:"优惠券折扣（整数分）"`
	MemberDiscount int64 `json:"member_discount" dc:"会员折扣（整数分，本批次恒 0）"`
	PointsDiscount int64 `json:"points_discount" dc:"积分抵扣（整数分，本批次恒 0）"`
	ShippingFee    int64 `json:"shipping_fee" dc:"运费（整数分）"`
	PayableAmount  int64 `json:"payable_amount" dc:"应付金额（整数分）"`
}

// QuoteRequest 是计价输入。
//
// OriginalAmount 由调用方按服务端权威价格汇总（订单场景为下单时重读的 skus.price × quantity），
// CouponDiscount 由优惠券模块在事务内校验并计算后传入，ShippingFee 由运费模块计算后传入。
// MemberDiscount/PointsDiscount 为后续任务预留：本批次没有产生这两个值的业务规则，
// 调用方必须传 0；槽位保留是为了让 B1-04-08（会员折扣）与 B1-04-05（积分）在同一入口扩展，
// 而不是各自新增第二套公式。
type QuoteRequest struct {
	OriginalAmount int64
	CouponDiscount int64
	MemberDiscount int64
	PointsDiscount int64
	ShippingFee    int64
}

// Quote 计算计价明细，是唯一允许产生订单金额公式的入口。
//
// ctx 当前不参与计算；保留以便后续扩展需要读取权威配置（如积分换算比例、会员等级折扣）时
// 不改变调用签名，避免调用方在大范围改造中漂移。
func Quote(ctx context.Context, req *QuoteRequest) (*Breakdown, error) {
	_ = ctx
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	// 1. 所有输入必须非负：负金额一律视为非法输入，不做「自动纠正」。
	if req.OriginalAmount < 0 || req.CouponDiscount < 0 || req.MemberDiscount < 0 ||
		req.PointsDiscount < 0 || req.ShippingFee < 0 {
		return nil, codes.New(codes.CodePricingInvalidAmount)
	}

	// 2. 逐项扣减：任一项超过当前剩余金额即拒绝，保证累计优惠不超过商品金额，
	//    同时天然避免「多个折扣相加溢出」与中途出现负数。
	remaining := req.OriginalAmount
	discounts := []int64{req.CouponDiscount, req.MemberDiscount, req.PointsDiscount}
	for _, discount := range discounts {
		if discount > remaining {
			return nil, codes.New(codes.CodePricingDiscountExceeds)
		}
		remaining -= discount
	}

	// 3. 加运费前做溢出防护；应付金额还需落在数据库可写区间内。
	if req.ShippingFee > math.MaxInt64-remaining {
		return nil, codes.New(codes.CodePricingAmountOverflow)
	}
	payable := remaining + req.ShippingFee
	if payable > MaxTotalAmount {
		return nil, codes.New(codes.CodePricingAmountOverflow)
	}

	return &Breakdown{
		OriginalAmount: req.OriginalAmount,
		CouponDiscount: req.CouponDiscount,
		MemberDiscount: req.MemberDiscount,
		PointsDiscount: req.PointsDiscount,
		ShippingFee:    req.ShippingFee,
		PayableAmount:  payable,
	}, nil
}

// Conserved 校验明细是否满足金额守恒（供测试与调用方断言使用）。
func (b *Breakdown) Conserved() bool {
	if b == nil {
		return false
	}
	if b.OriginalAmount < 0 || b.CouponDiscount < 0 || b.MemberDiscount < 0 ||
		b.PointsDiscount < 0 || b.ShippingFee < 0 || b.PayableAmount < 0 {
		return false
	}
	// 用减法累计避免加法溢出；折算结果必须与 PayableAmount 一致。
	remaining := b.OriginalAmount
	for _, discount := range []int64{b.CouponDiscount, b.MemberDiscount, b.PointsDiscount} {
		if discount > remaining {
			return false
		}
		remaining -= discount
	}
	if b.ShippingFee > math.MaxInt64-remaining {
		return false
	}
	return remaining+b.ShippingFee == b.PayableAmount
}
