package wallet

import (
	"strings"
	"testing"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestValidateRechargeAmount 覆盖充值金额校验：非正数与超过单笔上限必须拒绝。
func TestValidateRechargeAmount(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		want   codes.Code
	}{
		{"zero", 0, codes.CodeWalletInvalidRechargeAmount},
		{"negative", -1, codes.CodeWalletInvalidRechargeAmount},
		{"one cent", 1, codes.CodeOK},
		{"upper bound", v1.MaxRechargeAmount, codes.CodeOK},
		{"over upper bound", v1.MaxRechargeAmount + 1, codes.CodeWalletInvalidRechargeAmount},
		{"int64 max", 1<<63 - 1, codes.CodeWalletInvalidRechargeAmount},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRechargeAmount(c.amount)
			if got := codes.FromError(err); got != c.want {
				t.Fatalf("amount=%d: code=%d, want %d", c.amount, got, c.want)
			}
		})
	}
}

// TestRechargeOrderNoDeterministic 覆盖充值单号派生：同 (user, key) 稳定、长度 32、跨用户/跨键不同。
func TestRechargeOrderNoDeterministic(t *testing.T) {
	first := rechargeOrderNo(1001, "req-0001")
	second := rechargeOrderNo(1001, "req-0001")
	if first != second {
		t.Fatalf("同键单号应稳定: %q != %q", first, second)
	}
	if len(first) != 32 {
		t.Fatalf("单号长度应为 32（VARCHAR(32)），实际 %d: %q", len(first), first)
	}
	if !strings.HasPrefix(first, rechargeOrderNoPrefix) {
		t.Fatalf("单号前缀应为 %q，实际 %q", rechargeOrderNoPrefix, first)
	}
	if other := rechargeOrderNo(1002, "req-0001"); other == first {
		t.Fatalf("不同用户不应共享单号: %q", other)
	}
	if other := rechargeOrderNo(1001, "req-0002"); other == first {
		t.Fatalf("不同幂等键不应共享单号: %q", other)
	}
}

// TestIdempotencyKeys 覆盖三类确定性幂等键的既有约定。
func TestIdempotencyKeys(t *testing.T) {
	if got := idempotencyKeyOfRecharge(12); got != "recharge:12" {
		t.Fatalf("充值幂等键 = %q", got)
	}
	if got := idempotencyKeyOfOrderPay(34); got != "order_pay:34" {
		t.Fatalf("支付幂等键 = %q", got)
	}
	if got := idempotencyKeyOfRefund(56); got != "refund:56" {
		t.Fatalf("退款幂等键 = %q", got)
	}
}

// TestNormalizePaging 覆盖分页参数校验：默认值、边界与越界拒绝。
func TestNormalizePaging(t *testing.T) {
	page, size, err := normalizePaging(nil, nil)
	if err != nil || page != defaultPage || size != defaultSize {
		t.Fatalf("默认分页 = (%d,%d,%v)", page, size, err)
	}
	zero, hundred := 3, maxPageSize
	if page, size, err = normalizePaging(&zero, &hundred); err != nil || page != 3 || size != 100 {
		t.Fatalf("合法分页 = (%d,%d,%v)", page, size, err)
	}
	badPage, badSize, tooLarge := 0, 0, maxPageSize+1
	for _, c := range []struct {
		page, size *int
	}{
		{&badPage, nil},
		{nil, &badSize},
		{nil, &tooLarge},
	} {
		if _, _, err := normalizePaging(c.page, c.size); codes.FromError(err) != codes.CodeInvalidArgument {
			t.Fatalf("越界分页应返回 1001，实际 %v", err)
		}
	}
}

// TestReplayFundResult 覆盖幂等回放：要素一致才复用，任一不一致必须拒绝。
func TestReplayFundResult(t *testing.T) {
	existing := &existingTransaction{
		Id: 9, UserId: 7, TxType: txTypeOrderPay, Direction: directionDebit,
		Amount: 6000, BalanceBefore: 10000, BalanceAfter: 4000,
	}
	result, err := replayFundResult(existing, 7, 6000, directionDebit, txTypeOrderPay, codes.CodeWalletDuplicatePayment)
	if err != nil {
		t.Fatalf("要素一致应幂等成功: %v", err)
	}
	if !result.AlreadyApplied || result.BalanceAfter != 4000 || result.TransactionID != 9 {
		t.Fatalf("幂等结果不符: %+v", result)
	}

	cases := []struct {
		name      string
		userID    int64
		amount    int64
		direction int
		txType    string
	}{
		{"different amount", 7, 5999, directionDebit, txTypeOrderPay},
		{"different user", 8, 6000, directionDebit, txTypeOrderPay},
		{"different direction", 7, 6000, directionCredit, txTypeOrderPay},
		{"different tx type", 7, 6000, directionDebit, txTypeOrderRefund},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := replayFundResult(existing, c.userID, c.amount, c.direction, c.txType, codes.CodeWalletDuplicatePayment)
			if codes.FromError(err) != codes.CodeWalletDuplicatePayment {
				t.Fatalf("应返回 22006，实际 %v", err)
			}
		})
	}
}

// TestDirectionAndStatusText 覆盖方向/状态枚举映射。
func TestDirectionAndStatusText(t *testing.T) {
	if directionText(directionCredit) != v1.DirectionCredit || directionText(directionDebit) != v1.DirectionDebit {
		t.Fatalf("方向映射错误")
	}
	pairs := map[int]string{
		rechargeStatusPending:   v1.RechargeStatusPending,
		rechargeStatusSucceeded: v1.RechargeStatusSucceeded,
		rechargeStatusCancelled: v1.RechargeStatusCancelled,
		rechargeStatusFailed:    v1.RechargeStatusFailed,
	}
	for status, want := range pairs {
		if got := rechargeStatusText(status); got != want {
			t.Fatalf("状态 %d 映射为 %q，期望 %q", status, got, want)
		}
	}
}

// TestValidTxType 覆盖流水类型白名单：未知类型必须拒绝（防止 SQL 语义漂移）。
func TestValidTxType(t *testing.T) {
	for _, valid := range []string{txTypeRecharge, txTypeOrderPay, txTypeOrderRefund} {
		if !validTxType(valid) {
			t.Fatalf("%q 应为合法类型", valid)
		}
	}
	for _, invalid := range []string{"", "manual_adjust", "ORDER_PAY", "order_pay "} {
		if validTxType(invalid) {
			t.Fatalf("%q 不应为合法类型", invalid)
		}
	}
}

// TestMaxWalletBalanceMatchesUnsignedInt 固化「余额上限 = INT UNSIGNED 上限」的事实，防止误改。
func TestMaxWalletBalanceMatchesUnsignedInt(t *testing.T) {
	if maxWalletBalance != 4294967295 {
		t.Fatalf("余额上限应为 INT UNSIGNED 最大值，实际 %d", maxWalletBalance)
	}
}
