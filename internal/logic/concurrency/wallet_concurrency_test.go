package concurrency

// 4.2 钱包余额扣减并发测试。
//
// 被测实现：internal/logic/wallet 的 DebitForOrderInTx（真实 MySQL 事务）：
//  1) SELECT ... FOR UPDATE 锁 wallet_accounts 行，把同一用户的资金操作串行化；
//  2) 幂等键 order_pay:<order_id> 查重（同键不同金额必须冲突，不得当成功）；
//  3) 条件更新 balance = balance - ? WHERE balance >= ?，0 行即余额不足；
//  4) 余额变更与 wallet_transactions 流水同事务提交。
//
// 本文件验证的业务不变量：
//   WA-1 不允许负余额、不允许超额扣款；
//   WA-2 同一幂等请求只扣一次（并发同订单号 20 次 → 1 条流水、1 次余额变动）；
//   WA-3 账户余额与流水一致：balance == 初始 - Σ流水金额，且最后一条流水 after 等于余额；
//   WA-4 流水严格串行自洽：每条的 balance_before 接续上一条 balance_after；
//   WA-5 事务失败（调用方回滚）不留余额变动、不留流水、不留幂等键残留。

import (
	"context"
	"errors"
	"testing"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// walletTxRow 是 wallet_transactions 的一条记录（校验账户与流水一致性所需列）。
type walletTxRow struct {
	Id             int64  `json:"id"`
	UserId         int64  `json:"user_id"`
	TxType         string `json:"tx_type"`
	Direction      int    `json:"direction"`
	Amount         int64  `json:"amount"`
	BalanceBefore  int64  `json:"balance_before"`
	BalanceAfter   int64  `json:"balance_after"`
	IdempotencyKey string `json:"idempotency_key"`
}

// setWalletBalanceDirect 直接构造初始余额（仅用于准备测试前置状态，不绕过被测扣款路径）。
func setWalletBalanceDirect(t *testing.T, userID, balance int64) {
	t.Helper()
	if _, err := g.DB().Exec(context.Background(),
		"INSERT INTO wallet_accounts (user_id, balance) VALUES (?, ?) ON DUPLICATE KEY UPDATE balance = ?",
		userID, balance, balance,
	); err != nil {
		t.Fatalf("构造初始余额失败: %v", err)
	}
}

// walletBalanceOf 读取账户余额（账户不存在视为 0）。
func walletBalanceOf(t *testing.T, userID int64) int64 {
	t.Helper()
	value, err := g.DB().Model("wallet_accounts").Ctx(context.Background()).
		Where("user_id", userID).Value("balance")
	if err != nil {
		t.Fatalf("查询余额失败: %v", err)
	}
	if value == nil || value.IsNil() {
		return 0
	}
	return value.Int64()
}

// loadWalletLedger 读取指定用户的资金流水（id 升序）。
func loadWalletLedger(t *testing.T, userID int64) []*walletTxRow {
	t.Helper()
	var rows []*walletTxRow
	if err := g.DB().Model("wallet_transactions").Ctx(context.Background()).
		Fields("id", "user_id", "tx_type", "direction", "amount", "balance_before", "balance_after", "idempotency_key").
		Where("user_id", userID).Order("id ASC").Scan(&rows); err != nil {
		t.Fatalf("查询钱包流水失败: %v", err)
	}
	return rows
}

// debitForOrder 走真实的「调用方事务 + DebitForOrderInTx」，与订单支付路径一致。
func debitForOrder(ctx context.Context, userID, orderID, amount int64) (*service.WalletFundResult, error) {
	var (
		res *service.WalletFundResult
	)
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, e := service.Wallet().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{
			UserID:  userID,
			OrderID: orderID,
			Amount:  amount,
		})
		if e != nil {
			return e
		}
		res = r
		return nil
	})
	return res, err
}

// assertWalletLedgerChain 校验流水自洽（方向/金额/前后余额），返回实际扣减总额。
func assertWalletLedgerChain(t *testing.T, rows []*walletTxRow) int64 {
	t.Helper()
	var totalDebit int64
	prevAfter := int64(-1)
	for _, r := range rows {
		if r.Direction != 2 {
			t.Fatalf("流水 %d 方向=%d，期望 2（出账）", r.Id, r.Direction)
		}
		if r.TxType != "order_pay" {
			t.Fatalf("流水 %d tx_type=%q，期望 order_pay", r.Id, r.TxType)
		}
		if r.Amount <= 0 {
			t.Fatalf("流水 %d 金额=%d，必须为正", r.Id, r.Amount)
		}
		if r.BalanceAfter != r.BalanceBefore-r.Amount {
			t.Fatalf("流水 %d 前后余额不自洽：%d - %d != %d",
				r.Id, r.BalanceBefore, r.Amount, r.BalanceAfter)
		}
		if r.BalanceAfter < 0 {
			t.Fatalf("流水 %d 扣出负余额: %d", r.Id, r.BalanceAfter)
		}
		if prevAfter >= 0 && r.BalanceBefore != prevAfter {
			t.Fatalf("流水 %d 断链：balance_before=%d，上一条 balance_after=%d",
				r.Id, r.BalanceBefore, prevAfter)
		}
		prevAfter = r.BalanceAfter
		totalDebit += r.Amount
	}
	return totalDebit
}

// TestWalletConcurrentDebitNoNegativeBalance 覆盖 WA-1/WA-3/WA-4：
// 余额 1000，40 个 goroutine 同时各扣 30（订单号互不相同）。
// 成功次数上限 33；余额 = 1000 - 30×成功数，且流水与余额严格一致。
func TestWalletConcurrentDebitNoNegativeBalance(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialBalance = 1000
		amount         = 30
		attempts       = 40
	)
	userID := newTestUser(t, "wallet-overspend")
	setWalletBalanceDirect(t, userID, initialBalance)

	errs := make([]error, attempts)
	applied := make([]bool, attempts)
	runConcurrently(attempts, func(i int) {
		res, err := debitForOrder(ctx, userID, 900000+int64(i), amount)
		errs[i] = err
		applied[i] = res != nil && !res.AlreadyApplied
	})

	var successes, insufficient int
	for i, err := range errs {
		switch {
		case err == nil:
			successes++
			if !applied[i] {
				t.Fatalf("第 %d 个请求返回成功但未实际扣款", i)
			}
		case codes.FromError(err) == codes.CodeWalletInsufficientBalance:
			insufficient++
		default:
			t.Fatalf("第 %d 个请求出现非预期错误: %v", i, err)
		}
	}
	if wantMax := initialBalance / amount; successes > wantMax {
		t.Fatalf("成功扣款 %d 次，超过余额可承载的 %d 次（超额扣款）", successes, wantMax)
	}
	if successes+insufficient != attempts {
		t.Fatalf("成功 %d + 余额不足 %d != 总请求 %d", successes, insufficient, attempts)
	}

	balance := walletBalanceOf(t, userID)
	wantBalance := int64(initialBalance - successes*amount)
	if balance != wantBalance {
		t.Fatalf("最终余额=%d，期望 %d", balance, wantBalance)
	}
	if balance < 0 {
		t.Fatalf("余额出现负数: %d", balance)
	}

	rows := loadWalletLedger(t, userID)
	if len(rows) != successes {
		t.Fatalf("流水条数=%d，期望 %d（失败请求写入了错误流水）", len(rows), successes)
	}
	totalDebit := assertWalletLedgerChain(t, rows)
	if totalDebit != int64(initialBalance)-balance {
		t.Fatalf("流水扣减合计=%d，与余额差 %d 不一致", totalDebit, int64(initialBalance)-balance)
	}
	if len(rows) > 0 && rows[len(rows)-1].BalanceAfter != balance {
		t.Fatalf("最后一条流水 after=%d 与账户余额 %d 不一致", rows[len(rows)-1].BalanceAfter, balance)
	}
}

// TestWalletConcurrentSameOrderSingleDebit 覆盖 WA-2：
// 同一订单（同一幂等键）20 个并发扣款请求，只有 1 次真实扣款，其余为幂等回放。
func TestWalletConcurrentSameOrderSingleDebit(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialBalance = 500
		amount         = 100
		attempts       = 20
		orderID        = int64(777001)
	)
	userID := newTestUser(t, "wallet-idem")
	setWalletBalanceDirect(t, userID, initialBalance)

	results := make([]*service.WalletFundResult, attempts)
	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		res, err := debitForOrder(ctx, userID, orderID, amount)
		results[i] = res
		errs[i] = err
	})

	var appliedCount, replayCount int
	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个同订单请求失败（幂等键应回放而非报错）: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("第 %d 个请求返回空结果", i)
		}
		if results[i].AlreadyApplied {
			replayCount++
			continue
		}
		appliedCount++
	}
	if appliedCount != 1 {
		t.Fatalf("实际扣款 %d 次，期望恰好 1 次（重复扣款）", appliedCount)
	}
	if replayCount != attempts-1 {
		t.Fatalf("幂等回放 %d 次，期望 %d 次", replayCount, attempts-1)
	}

	if balance := walletBalanceOf(t, userID); balance != initialBalance-amount {
		t.Fatalf("最终余额=%d，期望 %d", balance, initialBalance-amount)
	}
	rows := loadWalletLedger(t, userID)
	if len(rows) != 1 {
		t.Fatalf("同一订单产生了 %d 条流水，期望 1 条", len(rows))
	}
	if rows[0].Amount != amount || rows[0].BalanceAfter != initialBalance-amount {
		t.Fatalf("流水金额/余额异常: amount=%d after=%d", rows[0].Amount, rows[0].BalanceAfter)
	}
}

// TestWalletConcurrentMixedAmountsLedgerConsistent 覆盖 WA-1/WA-3/WA-4：
// 不同金额（100/200/300）与相同金额混合并发，余额与流水必须完全对账。
func TestWalletConcurrentMixedAmountsLedgerConsistent(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialBalance = 3000
		attempts       = 30
	)
	userID := newTestUser(t, "wallet-mixed")
	setWalletBalanceDirect(t, userID, initialBalance)

	amounts := make([]int64, attempts)
	for i := range amounts {
		amounts[i] = int64(100 * (i%3 + 1))
	}

	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		_, err := debitForOrder(ctx, userID, 910000+int64(i), amounts[i])
		errs[i] = err
	})

	var wantSpend int64
	for i, err := range errs {
		if err == nil {
			wantSpend += amounts[i]
			continue
		}
		if codes.FromError(err) != codes.CodeWalletInsufficientBalance {
			t.Fatalf("第 %d 个请求非预期错误: %v", i, err)
		}
	}

	balance := walletBalanceOf(t, userID)
	if want := int64(initialBalance) - wantSpend; balance != want {
		t.Fatalf("最终余额=%d，期望 %d", balance, want)
	}
	if balance < 0 {
		t.Fatalf("余额出现负数: %d", balance)
	}

	rows := loadWalletLedger(t, userID)
	if total := assertWalletLedgerChain(t, rows); total != wantSpend {
		t.Fatalf("流水合计=%d，期望 %d", total, wantSpend)
	}
	if len(rows) > 0 && rows[len(rows)-1].BalanceAfter != balance {
		t.Fatalf("最后一条流水 after=%d 与余额 %d 不一致", rows[len(rows)-1].BalanceAfter, balance)
	}
}

// TestWalletDebitRollbackLeavesNoTrace 覆盖 WA-5：
// 调用方事务在扣款成功后回滚（模拟「钱包扣了但订单未支付」的失败事务），
// 余额、流水、幂等键必须全部回滚；随后同一订单仍可正常扣款一次。
func TestWalletDebitRollbackLeavesNoTrace(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialBalance = 500
		amount         = 200
		orderID        = int64(778001)
	)
	userID := newTestUser(t, "wallet-rollback")
	setWalletBalanceDirect(t, userID, initialBalance)

	injected := errors.New("simulated order failure after wallet debit")
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := service.Wallet().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{
			UserID: userID, OrderID: orderID, Amount: amount,
		}); e != nil {
			return e
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("期望注入错误原样返回，实际 %v", err)
	}

	if balance := walletBalanceOf(t, userID); balance != initialBalance {
		t.Fatalf("回滚后余额=%d，期望 %d（事务未整体回滚）", balance, initialBalance)
	}
	if rows := loadWalletLedger(t, userID); len(rows) != 0 {
		t.Fatalf("回滚后残留 %d 条流水，期望 0", len(rows))
	}

	// 幂等键也必须随事务回滚：同一订单再次扣款应真实生效（而不是被孤儿流水判为已支付）。
	res, err := debitForOrder(ctx, userID, orderID, amount)
	if err != nil {
		t.Fatalf("回滚后重新扣款失败: %v", err)
	}
	if res.AlreadyApplied {
		t.Fatalf("回滚后重新扣款被误判为幂等回放（残留幂等键）")
	}
	if balance := walletBalanceOf(t, userID); balance != initialBalance-amount {
		t.Fatalf("重新扣款后余额=%d，期望 %d", balance, initialBalance-amount)
	}
	if rows := loadWalletLedger(t, userID); len(rows) != 1 {
		t.Fatalf("重新扣款后流水条数=%d，期望 1", len(rows))
	}
}
