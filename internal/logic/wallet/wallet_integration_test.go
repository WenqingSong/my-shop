package wallet

// 钱包资金一致性集成测试（真实 MySQL）。
//
// 安全护栏：只允许在专用测试库上运行（DATABASE_DEFAULT_NAME 必须以 `_test` 结尾），
// 否则整个用例 t.Skip。绝不在 my_shop / my_shop_b10304_verify 等共享库上执行清库动作；
// 本文件只删除「本用例自己创建的 user_id」名下数据，不做全表 DELETE / DROP TABLE。
//
// 运行方式：
//
//	DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
//	  go test -race -count=1 ./internal/logic/wallet/

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

var (
	migrateOnce  sync.Once
	migrateErr   error
	dbConfigOnce sync.Once
	dbConfigErr  error
)

// configureTestDB 按 DATABASE_DEFAULT_* 显式装配 gdb 默认分组。
//
// 生产由 boot.applyDatabaseConfig 装配；本测试包不启动完整 app（钱包不依赖 Redis/HTTP），
// 因此在这里复刻同样的显式装配，避免依赖 GoFrame 对 "database.default.pass" 的环境变量
// 自动映射（项目已明确该映射不可靠）。
func configureTestDB() error {
	dbConfigOnce.Do(func() {
		name := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_NAME"))
		pass := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_PASS"))
		if pass == "" {
			dbConfigErr = errors.New("DATABASE_DEFAULT_PASS 未设置（数据库密码无内置默认值）")
			return
		}
		host := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_HOST"))
		if host == "" {
			host = "127.0.0.1"
		}
		port := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_PORT"))
		if port == "" {
			port = "3306"
		}
		user := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_USER"))
		if user == "" {
			user = "root"
		}
		dbConfigErr = gdb.SetConfigGroup(gdb.DefaultGroupName, gdb.ConfigGroup{gdb.ConfigNode{
			Type:     "mysql",
			Host:     host,
			Port:     port,
			User:     user,
			Pass:     pass,
			Name:     name,
			Charset:  "utf8mb4",
			Protocol: "tcp",
		}})
	})
	return dbConfigErr
}

// requireTestDB 校验测试库隔离并确保 schema 已迁移。
func requireTestDB(t *testing.T) {
	t.Helper()
	name := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_NAME"))
	if !strings.HasSuffix(name, "_test") {
		t.Skipf("资金集成测试需要专用测试库（DATABASE_DEFAULT_NAME 以 _test 结尾，当前 %q）；拒绝在共享库上运行", name)
	}
	if err := configureTestDB(); err != nil {
		t.Skipf("跳过资金集成测试：%v", err)
	}
	migrateOnce.Do(func() {
		migrateErr = migrations.Up(context.Background())
	})
	if migrateErr != nil {
		t.Fatalf("迁移测试库失败: %v", migrateErr)
	}
	if _, err := g.DB().Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("连接测试库失败（检查 DATABASE_DEFAULT_* 与 MySQL 可用性）: %v", err)
	}
}

// newTestUser 返回一个本用例独占的 user_id，并注册清理。
func newTestUser(t *testing.T) int64 {
	t.Helper()
	userID := 700000000000000000 + time.Now().UnixNano()%1000000000000
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			"DELETE FROM wallet_transactions WHERE user_id = ?",
			"DELETE FROM wallet_accounts WHERE user_id = ?",
			"DELETE FROM recharge_orders WHERE user_id = ?",
		} {
			_, _ = g.DB().Exec(ctx, sql, userID)
		}
	})
	return userID
}

// walletBalance 读取账户余额（账户不存在视为 0）。
func walletBalance(t *testing.T, userID int64) int64 {
	t.Helper()
	value, err := g.DB().Model("wallet_accounts").Ctx(context.Background()).Where("user_id", userID).Value("balance")
	if err != nil {
		t.Fatalf("查询余额: %v", err)
	}
	if value == nil || value.IsNil() {
		return 0
	}
	return value.Int64()
}

// setWalletBalance 直接设置余额（仅用于构造测试初始状态，绕过业务写路径）。
func setWalletBalance(t *testing.T, userID, balance int64) {
	t.Helper()
	if _, err := g.DB().Exec(context.Background(),
		"INSERT INTO wallet_accounts (user_id, balance) VALUES (?, ?) ON DUPLICATE KEY UPDATE balance = ?",
		userID, balance, balance,
	); err != nil {
		t.Fatalf("构造初始余额: %v", err)
	}
}

// countRows 统计某表指定用户的行数。
func countRows(t *testing.T, table string, userID int64) int {
	t.Helper()
	n, err := g.DB().Model(table).Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("统计 %s: %v", table, err)
	}
	return n
}

// assertFundsConsistent 校验「账户余额 = 期初余额 + Σ入账流水 - Σ出账流水」。
//
// 期初余额由测试通过 setWalletBalance 直接构造（真实业务中余额只能由流水变更），
// 因此这里把期初值作为基线，校验资金变动是否与流水净额完全一致。
func assertFundsConsistent(t *testing.T, userID, openingBalance int64) {
	t.Helper()
	record, err := g.DB().Model("wallet_transactions").Ctx(context.Background()).
		Where("user_id", userID).
		Fields("COALESCE(SUM(CASE WHEN direction = 1 THEN amount ELSE 0 END), 0) AS credit_total, " +
			"COALESCE(SUM(CASE WHEN direction = 2 THEN amount ELSE 0 END), 0) AS debit_total").
		One()
	if err != nil {
		t.Fatalf("汇总流水: %v", err)
	}
	creditTotal := record["credit_total"].Int64()
	debitTotal := record["debit_total"].Int64()
	balance := walletBalance(t, userID)
	if balance != openingBalance+creditTotal-debitTotal {
		t.Fatalf("账实不符: balance=%d, 期初=%d, Σ入账=%d, Σ出账=%d", balance, openingBalance, creditTotal, debitTotal)
	}
	if balance < 0 {
		t.Fatalf("余额不得为负: %d", balance)
	}
}

// wantCode 断言错误码。
func wantCode(t *testing.T, err error, want codes.Code, scene string) {
	t.Helper()
	if got := codes.FromError(err); got != want {
		t.Fatalf("%s: code=%d, want %d（err=%v）", scene, got, want, err)
	}
}

// enableMockRecharge 打开 Mock 充值开关（仅测试进程内环境变量，默认关闭）。
func enableMockRecharge(t *testing.T) {
	t.Helper()
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "true")
}

// recharge 发起一次 Mock 充值。
func recharge(t *testing.T, userID, amount int64, key string) (*v1.RechargeRes, error) {
	t.Helper()
	return New().Recharge(context.Background(), userID, &v1.RechargeReq{Amount: amount, IdempotencyKey: key})
}

// TestRechargeNormalAndIdempotent 覆盖场景一/二：正常充值、重复充值只入账一次。
func TestRechargeNormalAndIdempotent(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	userID := newTestUser(t)
	ctx := context.Background()

	account, err := New().Account(ctx, userID)
	if err != nil || account.Balance != 0 {
		t.Fatalf("初始余额应为 0: %+v, %v", account, err)
	}

	first, err := recharge(t, userID, 10000, "recharge-key-1")
	if err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if first.Status != v1.RechargeStatusSucceeded || first.Amount != 10000 || first.PayType != mockPayType {
		t.Fatalf("充值结果不符: %+v", first)
	}
	if first.PaidAt == nil {
		t.Fatal("成功充值单必须有 paid_at")
	}
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("充值后余额 = %d, want 10000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 1 {
		t.Fatalf("充值应产生 1 条流水，实际 %d", n)
	}
	if n := countRows(t, "recharge_orders", userID); n != 1 {
		t.Fatalf("充值应产生 1 条充值单，实际 %d", n)
	}

	// 同一请求重复提交：幂等成功，不重复入账。
	second, err := recharge(t, userID, 10000, "recharge-key-1")
	if err != nil {
		t.Fatalf("重复充值应幂等成功: %v", err)
	}
	if second.Id != first.Id {
		t.Fatalf("重复充值应命中同一充值单: %d != %d", second.Id, first.Id)
	}
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("重复充值后余额 = %d, want 10000（不得重复入账）", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 1 {
		t.Fatalf("重复充值后流水应仍为 1 条，实际 %d", n)
	}
	if n := countRows(t, "recharge_orders", userID); n != 1 {
		t.Fatalf("重复充值后充值单应仍为 1 条，实际 %d", n)
	}

	// 同键不同金额：必须拒绝，不得当作成功。
	_, err = recharge(t, userID, 9999, "recharge-key-1")
	wantCode(t, err, codes.CodeWalletIdempotencyConflict, "同键不同金额")
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("幂等冲突不得改变余额: %d", got)
	}

	assertFundsConsistent(t, userID, 0)
}

// TestRechargeDisabledByDefault 覆盖安全开关：默认关闭时充值被拒绝且无副作用。
func TestRechargeDisabledByDefault(t *testing.T) {
	requireTestDB(t)
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "false")
	userID := newTestUser(t)

	_, err := recharge(t, userID, 10000, "recharge-key-disabled")
	wantCode(t, err, codes.CodeWalletMockRechargeDisabled, "Mock 充值关闭")
	if n := countRows(t, "recharge_orders", userID); n != 0 {
		t.Fatalf("开关关闭不得创建充值单，实际 %d", n)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("开关关闭不得产生流水，实际 %d", n)
	}
}

// TestRechargeAmountValidation 覆盖充值金额非法（0/负数/超上限）。
func TestRechargeAmountValidation(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	userID := newTestUser(t)

	for _, amount := range []int64{0, -1, v1.MaxRechargeAmount + 1} {
		_, err := recharge(t, userID, amount, fmt.Sprintf("bad-amount-%d", amount))
		wantCode(t, err, codes.CodeWalletInvalidRechargeAmount, fmt.Sprintf("金额 %d", amount))
	}
	if n := countRows(t, "recharge_orders", userID); n != 0 {
		t.Fatalf("非法金额不得创建充值单，实际 %d", n)
	}
}

// TestRechargeOverflowRollback 覆盖故障回滚：充值单已创建但余额入账失败时整体回滚。
func TestRechargeOverflowRollback(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, maxWalletBalance-100)

	_, err := recharge(t, userID, 10000, "overflow-key")
	wantCode(t, err, codes.CodeWalletAmountOverflow, "余额溢出")
	if got := walletBalance(t, userID); got != maxWalletBalance-100 {
		t.Fatalf("溢出失败不得改变余额: %d", got)
	}
	if n := countRows(t, "recharge_orders", userID); n != 0 {
		t.Fatalf("溢出失败时充值单必须随事务回滚，实际残留 %d 条", n)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("溢出失败不得留下流水，实际 %d", n)
	}
}

// TestConcurrentRechargeSameKey 覆盖并发重复充值：多请求同键只入账一次。
func TestConcurrentRechargeSameKey(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	userID := newTestUser(t)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = recharge(t, userID, 5000, "concurrent-recharge-key")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发同键充值第 %d 个请求失败: %v", i, err)
		}
	}
	if got := walletBalance(t, userID); got != 5000 {
		t.Fatalf("并发同键充值后余额 = %d, want 5000（只能入账一次）", got)
	}
	if n := countRows(t, "recharge_orders", userID); n != 1 {
		t.Fatalf("并发同键充值后充值单应只有 1 条，实际 %d", n)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 1 {
		t.Fatalf("并发同键充值后流水应只有 1 条，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 0)
}

// TestConcurrentAccountInit 覆盖账户初始化并发：同一用户只允许一个账户。
func TestConcurrentAccountInit(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = New().Account(context.Background(), userID)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发初始化账户第 %d 个失败: %v", i, err)
		}
	}
	if n := countRows(t, "wallet_accounts", userID); n != 1 {
		t.Fatalf("同一用户应只有 1 个钱包账户，实际 %d", n)
	}
}

// TestDebitForOrderSuccessAndIdempotent 覆盖场景三与支付幂等。
func TestDebitForOrderSuccessAndIdempotent(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 10000)
	ctx := context.Background()

	var first *service.WalletFundResult
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 1001, Amount: 6000})
		first = result
		return e
	})
	if err != nil {
		t.Fatalf("余额支付失败: %v", err)
	}
	if first.BalanceBefore != 10000 || first.BalanceAfter != 4000 || first.AlreadyApplied {
		t.Fatalf("扣款结果不符: %+v", first)
	}
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("支付后余额 = %d, want 4000", got)
	}

	// 重复支付：同一订单只扣一次，返回幂等结果。
	var replay *service.WalletFundResult
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 1001, Amount: 6000})
		replay = result
		return e
	})
	if err != nil {
		t.Fatalf("重复支付应幂等成功: %v", err)
	}
	if !replay.AlreadyApplied || replay.TransactionID != first.TransactionID {
		t.Fatalf("重复支付应复用既有流水: %+v", replay)
	}
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("重复支付后余额 = %d, want 4000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 1 {
		t.Fatalf("同一订单应只有 1 条扣款流水，实际 %d", n)
	}

	// 同一订单以不同金额再次扣款：必须拒绝（幂等键不可复用于不同金额）。
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 1001, Amount: 1})
		return e
	})
	wantCode(t, err, codes.CodeWalletDuplicatePayment, "同订单不同金额")
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("幂等冲突不得改变余额: %d", got)
	}
	assertFundsConsistent(t, userID, 10000)
}

// TestDebitInsufficientBalance 覆盖场景四：余额不足不扣款、不留流水、余额不变。
func TestDebitInsufficientBalance(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 3000)
	ctx := context.Background()

	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 2001, Amount: 6000})
		return e
	})
	wantCode(t, err, codes.CodeWalletInsufficientBalance, "余额不足")
	if got := walletBalance(t, userID); got != 3000 {
		t.Fatalf("余额不足不得改变余额: %d", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("余额不足不得产生扣款流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 3000)
}

// TestDebitExactBalanceBoundary 覆盖扣减边界：余额恰好等于订单金额时扣到 0，不越界。
func TestDebitExactBalanceBoundary(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 6000)
	ctx := context.Background()

	var result *service.WalletFundResult
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 2501, Amount: 6000})
		result = r
		return e
	}); err != nil {
		t.Fatalf("余额恰好足额时应支付成功: %v", err)
	}
	if result.BalanceAfter != 0 {
		t.Fatalf("扣到 0 时余额应为 0，实际 %d", result.BalanceAfter)
	}
	if got := walletBalance(t, userID); got != 0 {
		t.Fatalf("余额应为 0，实际 %d", got)
	}
	// 再扣 1 分必须失败（余额不得为负）。
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 2502, Amount: 1})
		return e
	})
	wantCode(t, err, codes.CodeWalletInsufficientBalance, "余额为 0 时再扣款")
	assertFundsConsistent(t, userID, 6000)
}

// TestCreditRejectsInvalidAmount 覆盖退款金额校验：0/负数直接拒绝且无副作用。
func TestCreditRejectsInvalidAmount(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 4000)
	ctx := context.Background()

	for _, amount := range []int64{0, -1} {
		err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
			_, e := New().CreditForRefundInTx(ctx, tx, service.WalletCreditInput{
				UserID: userID, RefundID: 5501, OrderID: 4501, Amount: amount,
			})
			return e
		})
		wantCode(t, err, codes.CodeInvalidArgument, fmt.Sprintf("退款金额 %d", amount))
	}
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("非法退款金额不得改变余额: %d", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("非法退款金额不得产生流水，实际 %d", n)
	}
}

// TestRollbackWhenTransactionInsertFails 覆盖「余额更新成功但流水插入失败」的回滚。
//
// 故障注入方式：在事务内先完成余额扣减，再用一个「已存在的幂等键」写流水。
// 唯一索引冲突使 insertTransactionInTx 返回 21008；调用方返回错误后整个事务回滚，
// 已扣减的余额必须恢复，证明「余额与流水同事务」不是口头约定。
func TestRollbackWhenTransactionInsertFails(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 10000)
	ctx := context.Background()

	// 先产生一条真实扣款流水（order_pay:7001），作为后续冲突源。
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 7001, Amount: 1000})
		return e
	}); err != nil {
		t.Fatalf("准备既有流水失败: %v", err)
	}
	if got := walletBalance(t, userID); got != 9000 {
		t.Fatalf("准备后余额 = %d, want 9000", got)
	}

	var insertErr error
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		accountID, before, e := lockAccountInTx(ctx, tx, userID)
		if e != nil {
			return e
		}
		if e := updateBalanceInTx(ctx, tx, accountID, -1000, "balance >= ?", 1000); e != nil {
			return e
		}
		_, insertErr = insertTransactionInTx(ctx, tx, transactionRecord{
			AccountID:      accountID,
			UserID:         userID,
			TxType:         txTypeOrderPay,
			Direction:      directionDebit,
			Amount:         1000,
			BalanceBefore:  before,
			BalanceAfter:   before - 1000,
			BizType:        bizTypeOrder,
			BizID:          7002,
			IdempotencyKey: idempotencyKeyOfOrderPay(7001), // 与既有流水冲突
			Remark:         "故障注入",
		}, codes.CodeWalletDuplicatePayment)
		return insertErr
	})
	wantCode(t, insertErr, codes.CodeWalletDuplicatePayment, "流水插入冲突")
	if err == nil {
		t.Fatal("流水插入失败时事务必须返回错误")
	}
	if got := walletBalance(t, userID); got != 9000 {
		t.Fatalf("流水插入失败必须回滚余额扣减，余额 = %d, want 9000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 1 {
		t.Fatalf("回滚后只应保留既有 1 条流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 10000)
}

// TestDebitRollbackWhenOrderAdvanceFails 覆盖「钱包扣款成功但订单状态推进失败」的回滚。
//
// 用 0 行的订单状态条件更新模拟订单模块的失败路径：调用方返回错误 → 整个事务回滚，
// 扣款与流水一并撤销，不出现「钱扣了订单未支付」。
func TestDebitRollbackWhenOrderAdvanceFails(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 10000)
	ctx := context.Background()

	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 3001, Amount: 6000}); e != nil {
			return e
		}
		result, e := tx.Ctx(ctx).Exec("UPDATE orders SET status = 20 WHERE id = ? AND status = 10", 0)
		if e != nil {
			return e
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return errors.New("订单状态推进失败")
		}
		return nil
	})
	if err == nil {
		t.Fatal("订单状态推进失败时应返回错误")
	}
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("订单推进失败必须回滚扣款，余额 = %d, want 10000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("回滚后不得残留扣款流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 10000)
}

// TestCreditForRefundAndIdempotent 覆盖场景五/六：支付后退款与重复退款。
func TestCreditForRefundAndIdempotent(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 10000)
	ctx := context.Background()

	pay := func() {
		if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
			_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID: userID, OrderID: 4001, Amount: 6000})
			return e
		}); err != nil {
			t.Fatalf("支付失败: %v", err)
		}
	}
	pay()
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("支付后余额 = %d, want 4000", got)
	}

	var credit *service.WalletFundResult
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := New().CreditForRefundInTx(ctx, tx, service.WalletCreditInput{UserID: userID, RefundID: 5001, OrderID: 4001, Amount: 6000})
		credit = result
		return e
	}); err != nil {
		t.Fatalf("退款入账失败: %v", err)
	}
	if credit.BalanceBefore != 4000 || credit.BalanceAfter != 10000 || credit.AlreadyApplied {
		t.Fatalf("退款入账结果不符: %+v", credit)
	}
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("退款后余额 = %d, want 10000", got)
	}

	// 重复退款：只入账一次。
	var replay *service.WalletFundResult
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := New().CreditForRefundInTx(ctx, tx, service.WalletCreditInput{UserID: userID, RefundID: 5001, OrderID: 4001, Amount: 6000})
		replay = result
		return e
	}); err != nil {
		t.Fatalf("重复退款应幂等成功: %v", err)
	}
	if !replay.AlreadyApplied || replay.TransactionID != credit.TransactionID {
		t.Fatalf("重复退款应复用既有流水: %+v", replay)
	}
	if got := walletBalance(t, userID); got != 10000 {
		t.Fatalf("重复退款后余额 = %d, want 10000（不得重复入账）", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 2 {
		t.Fatalf("支付 + 退款应产生 2 条流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 10000)
}

// TestCreditRollbackWhenRefundAdvanceFails 覆盖「退款入账成功但退款状态推进失败」的回滚。
func TestCreditRollbackWhenRefundAdvanceFails(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 4000)
	ctx := context.Background()

	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := New().CreditForRefundInTx(ctx, tx, service.WalletCreditInput{UserID: userID, RefundID: 6001, OrderID: 4101, Amount: 6000}); e != nil {
			return e
		}
		result, e := tx.Ctx(ctx).Exec("UPDATE refund_requests SET status = 60 WHERE id = ? AND status = 20", 0)
		if e != nil {
			return e
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return errors.New("退款状态推进失败")
		}
		return nil
	})
	if err == nil {
		t.Fatal("退款状态推进失败时应返回错误")
	}
	if got := walletBalance(t, userID); got != 4000 {
		t.Fatalf("退款推进失败必须回滚入账，余额 = %d, want 4000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 0 {
		t.Fatalf("回滚后不得残留退款流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 4000)
}

// TestConcurrentDebitNeverNegative 覆盖场景七：并发扣款不得透支、不得重复扣款。
func TestConcurrentDebitNeverNegative(t *testing.T) {
	requireTestDB(t)
	userID := newTestUser(t)
	setWalletBalance(t, userID, 10000)
	ctx := context.Background()

	const workers = 10
	const perDeduction int64 = 3000
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
				_, e := New().DebitForOrderInTx(ctx, tx, service.WalletDebitInput{
					UserID:  userID,
					OrderID: int64(9000 + idx),
					Amount:  perDeduction,
				})
				return e
			})
		}(i)
	}
	wg.Wait()

	success := 0
	for i, err := range errs {
		switch codes.FromError(err) {
		case codes.CodeOK:
			success++
		case codes.CodeWalletInsufficientBalance:
			// 预期：余额不足的并发请求整体回滚，无资金变化。
		default:
			t.Fatalf("并发扣款第 %d 个出现非预期错误: %v", i, err)
		}
	}
	if success != 3 {
		t.Fatalf("10000 分余额按 3000 分并发扣款应成功 3 次，实际 %d", success)
	}
	if got := walletBalance(t, userID); got != 1000 {
		t.Fatalf("并发扣款后余额 = %d, want 1000", got)
	}
	if n := countRows(t, "wallet_transactions", userID); n != 3 {
		t.Fatalf("并发扣款应只有 3 条有效流水，实际 %d", n)
	}
	assertFundsConsistent(t, userID, 10000)
}

// TestTransactionsPaginationAndIsolation 覆盖流水查询：分页、类型过滤与用户隔离。
func TestTransactionsPaginationAndIsolation(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	owner := newTestUser(t)
	other := newTestUser(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := recharge(t, owner, 1000, fmt.Sprintf("page-key-%d", i)); err != nil {
			t.Fatalf("准备流水失败: %v", err)
		}
	}

	page, size := 1, 2
	res, err := New().Transactions(ctx, owner, &v1.TransactionListReq{Page: &page, Size: &size})
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if res.Total != 3 || len(res.Items) != 2 || res.Items[0].Id < res.Items[1].Id {
		t.Fatalf("分页/倒序不符: total=%d len=%d", res.Total, len(res.Items))
	}
	if res.Items[0].TxType != v1.TxTypeRecharge || res.Items[0].Direction != v1.DirectionCredit {
		t.Fatalf("流水字段不符: %+v", res.Items[0])
	}
	if res.Items[0].BizId == nil || *res.Items[0].BizId <= 0 {
		t.Fatalf("充值流水应带 biz_id: %+v", res.Items[0])
	}

	filtered, err := New().Transactions(ctx, owner, &v1.TransactionListReq{TxType: v1.TxTypeOrderPay})
	if err != nil || filtered.Total != 0 {
		t.Fatalf("按类型过滤应无数据: total=%d, err=%v", filtered.Total, err)
	}

	otherRes, err := New().Transactions(ctx, other, &v1.TransactionListReq{})
	if err != nil || otherRes.Total != 0 {
		t.Fatalf("不得看到他人流水: total=%d, err=%v", otherRes.Total, err)
	}

	zero, tooLarge := 0, maxPageSize+1
	if _, err := New().Transactions(ctx, owner, &v1.TransactionListReq{Page: &zero}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("page=0 应返回 1001，实际 %v", err)
	}
	if _, err := New().Transactions(ctx, owner, &v1.TransactionListReq{Size: &tooLarge}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("size 超上限应返回 1001，实际 %v", err)
	}
	if _, err := New().Transactions(ctx, owner, &v1.TransactionListReq{TxType: "unknown"}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("未知 tx_type 应返回 1001，实际 %v", err)
	}
}

// TestRechargeFlowIsRepeatableAcrossTransactions 覆盖「充值单 → 余额 → 流水」的可核对性。
func TestRechargeFlowIsRepeatableAcrossTransactions(t *testing.T) {
	requireTestDB(t)
	enableMockRecharge(t)
	userID := newTestUser(t)

	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("flow-key-%d", i)
		if _, err := recharge(t, userID, int64(1000*(i+1)), key); err != nil {
			t.Fatalf("第 %d 次充值失败: %v", i, err)
		}
	}
	if got := walletBalance(t, userID); got != 6000 {
		t.Fatalf("三次充值后余额 = %d, want 6000", got)
	}

	records, err := g.DB().Model("wallet_transactions").Ctx(context.Background()).
		Where("user_id", userID).
		Fields("id", "idempotency_key", "biz_type", "biz_id", "balance_before", "balance_after", "amount", "direction").
		OrderAsc("id").All()
	if err != nil {
		t.Fatalf("读取流水: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("应有 3 条流水，实际 %d", len(records))
	}
	expectedBefore := int64(0)
	for _, record := range records {
		if record["biz_type"].String() != bizTypeRecharge {
			t.Fatalf("充值流水 biz_type 应为 recharge: %v", record)
		}
		bizID := record["biz_id"].Int64()
		if want := idempotencyKeyOfRecharge(bizID); record["idempotency_key"].String() != want {
			t.Fatalf("幂等键应为 %q，实际 %q", want, record["idempotency_key"].String())
		}
		if record["direction"].Int() != directionCredit {
			t.Fatalf("充值方向应为入账: %v", record)
		}
		if got := record["balance_before"].Int64(); got != expectedBefore {
			t.Fatalf("balance_before 应为 %d，实际 %d", expectedBefore, got)
		}
		expectedBefore = record["balance_after"].Int64()
		if expectedBefore != record["balance_before"].Int64()+record["amount"].Int64() {
			t.Fatalf("流水余额不闭合: %v", record)
		}
	}
	if expectedBefore != 6000 {
		t.Fatalf("最后一条流水余额应为 6000，实际 %d", expectedBefore)
	}
	assertFundsConsistent(t, userID, 0)
}
