package pricing

import (
	"context"
	"math"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

func TestQuoteWithoutDiscount(t *testing.T) {
	got, err := Quote(context.Background(), &QuoteRequest{OriginalAmount: 10000})
	if err != nil {
		t.Fatalf("无优惠计价失败: %v", err)
	}
	want := Breakdown{
		OriginalAmount: 10000,
		PayableAmount:  10000,
	}
	if *got != want {
		t.Fatalf("明细 = %+v，期望 %+v", *got, want)
	}
	if !got.Conserved() {
		t.Fatal("金额守恒校验失败")
	}
}

func TestQuoteWithCouponOnly(t *testing.T) {
	got, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		CouponDiscount: 1000,
	})
	if err != nil {
		t.Fatalf("券折扣计价失败: %v", err)
	}
	if got.CouponDiscount != 1000 {
		t.Fatalf("券折扣 = %d，期望 1000", got.CouponDiscount)
	}
	if got.PayableAmount != 9000 {
		t.Fatalf("应付 = %d，期望 9000", got.PayableAmount)
	}
	if !got.Conserved() {
		t.Fatal("金额守恒校验失败")
	}
}

func TestQuoteWithShippingOnly(t *testing.T) {
	got, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		ShippingFee:    500,
	})
	if err != nil {
		t.Fatalf("运费计价失败: %v", err)
	}
	if got.PayableAmount != 10500 {
		t.Fatalf("应付 = %d，期望 10500（运费不可被折扣抵扣）", got.PayableAmount)
	}
	if !got.Conserved() {
		t.Fatal("金额守恒校验失败")
	}
}

func TestQuoteWithAllSlots(t *testing.T) {
	// 本批次不产生会员/积分业务值，但槽位参与计算以验证后续扩展不会破坏守恒。
	got, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		CouponDiscount: 1000,
		MemberDiscount: 500,
		PointsDiscount: 300,
		ShippingFee:    500,
	})
	if err != nil {
		t.Fatalf("多槽位计价失败: %v", err)
	}
	// 10000 - 1000 - 500 - 300 + 500 = 8700
	if got.PayableAmount != 8700 {
		t.Fatalf("应付 = %d，期望 8700", got.PayableAmount)
	}
	if !got.Conserved() {
		t.Fatal("金额守恒校验失败")
	}
}

func TestQuoteDiscountEqualsOriginal(t *testing.T) {
	got, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		CouponDiscount: 10000,
	})
	if err != nil {
		t.Fatalf("折扣等于商品金额应允许（应付为 0）: %v", err)
	}
	if got.PayableAmount != 0 {
		t.Fatalf("应付 = %d，期望 0", got.PayableAmount)
	}
}

func TestQuoteDiscountExceedsOriginal(t *testing.T) {
	_, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		CouponDiscount: 10001,
	})
	if err == nil {
		t.Fatal("期望折扣超过商品金额被拒绝，实际成功")
	}
	if got := codes.FromError(err); got != codes.CodePricingDiscountExceeds {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingDiscountExceeds)
	}
}

func TestQuoteCumulativeDiscountExceedsOriginal(t *testing.T) {
	// 单看每项都不超，但累计超过：必须同样拒绝。
	_, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: 10000,
		CouponDiscount: 6000,
		MemberDiscount: 3000,
		PointsDiscount: 2000,
	})
	if err == nil {
		t.Fatal("期望累计优惠超过商品金额被拒绝，实际成功")
	}
	if got := codes.FromError(err); got != codes.CodePricingDiscountExceeds {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingDiscountExceeds)
	}
}

func TestQuoteNegativeAmounts(t *testing.T) {
	cases := []struct {
		name string
		req  QuoteRequest
	}{
		{"商品金额为负", QuoteRequest{OriginalAmount: -1}},
		{"券折扣为负", QuoteRequest{OriginalAmount: 100, CouponDiscount: -1}},
		{"会员折扣为负", QuoteRequest{OriginalAmount: 100, MemberDiscount: -1}},
		{"积分抵扣为负", QuoteRequest{OriginalAmount: 100, PointsDiscount: -1}},
		{"运费为负", QuoteRequest{OriginalAmount: 100, ShippingFee: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			_, err := Quote(context.Background(), &req)
			if err == nil {
				t.Fatal("期望负金额被拒绝，实际成功")
			}
			if got := codes.FromError(err); got != codes.CodePricingInvalidAmount {
				t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingInvalidAmount)
			}
		})
	}
}

func TestQuoteNilRequest(t *testing.T) {
	if _, err := Quote(context.Background(), nil); err == nil {
		t.Fatal("nil 请求应被拒绝")
	} else if got := codes.FromError(err); got != codes.CodeInvalidArgument {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodeInvalidArgument)
	}
}

func TestQuoteOverflow(t *testing.T) {
	_, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: math.MaxInt64,
		ShippingFee:    1,
	})
	if err == nil {
		t.Fatal("期望整数溢出被拒绝，实际成功")
	}
	if got := codes.FromError(err); got != codes.CodePricingAmountOverflow {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingAmountOverflow)
	}
}

func TestQuoteExceedsDatabaseColumnRange(t *testing.T) {
	// 商品金额本身在 int64 内，但应付超过 INT UNSIGNED 可写区间时必须提前拒绝。
	_, err := Quote(context.Background(), &QuoteRequest{
		OriginalAmount: int64(math.MaxUint32) + 1,
	})
	if err == nil {
		t.Fatal("期望超出数据库列区间被拒绝，实际成功")
	}
	if got := codes.FromError(err); got != codes.CodePricingAmountOverflow {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingAmountOverflow)
	}
}

func TestQuoteBoundary(t *testing.T) {
	// 应付恰好等于 INT UNSIGNED 上限时应允许。
	got, err := Quote(context.Background(), &QuoteRequest{OriginalAmount: int64(math.MaxUint32)})
	if err != nil {
		t.Fatalf("边界值应允许: %v", err)
	}
	if got.PayableAmount != int64(math.MaxUint32) {
		t.Fatalf("应付 = %d，期望 %d", got.PayableAmount, int64(math.MaxUint32))
	}
}

func TestQuoteZero(t *testing.T) {
	got, err := Quote(context.Background(), &QuoteRequest{OriginalAmount: 0})
	if err != nil {
		t.Fatalf("零金额计价应允许: %v", err)
	}
	if got.PayableAmount != 0 {
		t.Fatalf("应付 = %d，期望 0", got.PayableAmount)
	}
}

func TestConservedRejectsTamperedBreakdown(t *testing.T) {
	valid := &Breakdown{OriginalAmount: 100, CouponDiscount: 10, ShippingFee: 5, PayableAmount: 95}
	if !valid.Conserved() {
		t.Fatal("合法明细应通过守恒校验")
	}

	tampered := &Breakdown{OriginalAmount: 100, CouponDiscount: 10, ShippingFee: 5, PayableAmount: 94}
	if tampered.Conserved() {
		t.Fatal("应付金额被篡改时守恒校验必须失败")
	}

	negative := &Breakdown{OriginalAmount: 100, PayableAmount: -1}
	if negative.Conserved() {
		t.Fatal("负数应付必须被守恒校验拒绝")
	}

	// 折扣合计超过商品金额时即便应付为 0 也必须拒绝：否则负数中间余额会被运费抹平而掩盖错误。
	overDiscount := &Breakdown{OriginalAmount: 100, CouponDiscount: 60, MemberDiscount: 50, PayableAmount: 0}
	if overDiscount.Conserved() {
		t.Fatal("折扣合计超过商品金额必须被守恒校验拒绝")
	}

	// 运费与剩余金额相加溢出 int64 时必须拒绝，不能回绕成合法的应付金额。
	overflow := &Breakdown{OriginalAmount: math.MaxInt64, ShippingFee: 1, PayableAmount: 0}
	if overflow.Conserved() {
		t.Fatal("运费导致 int64 溢出必须被守恒校验拒绝")
	}

	if (*Breakdown)(nil).Conserved() {
		t.Fatal("nil 明细不应通过守恒校验")
	}
}
