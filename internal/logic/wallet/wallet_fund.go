package wallet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// errRechargeOrderExists 是「确定性充值单号已存在」的内部哨兵错误。
// 只在事务内部用于跳出并交由外层回读，不会暴露给 API 调用方。
var errRechargeOrderExists = errors.New("wallet: recharge order already exists")

// transactionRecord 是一条待写入的资金流水。
type transactionRecord struct {
	AccountID      int64
	UserID         int64
	TxType         string
	Direction      int
	Amount         int64
	BalanceBefore  int64
	BalanceAfter   int64
	BizType        string
	BizID          int64
	IdempotencyKey string
	Remark         string
}

// existingTransaction 是已存在的资金流水（用于幂等回放）。
type existingTransaction struct {
	Id            int64
	UserId        int64
	TxType        string
	Direction     int
	Amount        int64
	BalanceBefore int64
	BalanceAfter  int64
	BizType       string
	BizID         int64
}

// DebitForOrderInTx 在调用方事务内扣减订单应付金额并写扣款流水。
//
// 调用方（订单模块）必须把「订单 10→20」与本次调用放在同一事务：任一步失败整体回滚，
// 不会出现「钱包扣了但订单未支付」或反之。本方法不自开事务。
func (s *sWallet) DebitForOrderInTx(ctx context.Context, tx gdb.TX, in service.WalletDebitInput) (*service.WalletFundResult, error) {
	if in.UserID <= 0 || in.OrderID <= 0 || in.Amount <= 0 || in.Amount > maxWalletBalance {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	key := idempotencyKeyOfOrderPay(in.OrderID)

	// 1) 先锁账户行：同一用户的并发资金操作串行化，「查余额 + 判断 + 扣减」在锁内完成。
	accountID, balance, err := lockAccountInTx(ctx, tx, in.UserID)
	if err != nil {
		return nil, err
	}
	// 2) 幂等判定：同一订单只扣一次；同键不同金额必须拒绝，而不是当作成功。
	existing, err := findTransactionInTx(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return replayFundResult(existing, in.UserID, in.Amount, directionDebit, txTypeOrderPay, codes.CodeWalletDuplicatePayment)
	}
	if balance < in.Amount {
		return nil, codes.New(codes.CodeWalletInsufficientBalance)
	}
	after := balance - in.Amount
	// 3) 数据库层面原子保护：带余额条件的 UPDATE，0 行即余额不足。
	if err := updateBalanceInTx(ctx, tx, accountID, -in.Amount, "balance >= ?", in.Amount); err != nil {
		return nil, err
	}
	// 4) 写扣款流水（余额变更与流水同事务）。
	id, err := insertTransactionInTx(ctx, tx, transactionRecord{
		AccountID:      accountID,
		UserID:         in.UserID,
		TxType:         txTypeOrderPay,
		Direction:      directionDebit,
		Amount:         in.Amount,
		BalanceBefore:  balance,
		BalanceAfter:   after,
		BizType:        bizTypeOrder,
		BizID:          in.OrderID,
		IdempotencyKey: key,
		Remark:         "订单余额支付",
	}, codes.CodeWalletDuplicatePayment)
	if err != nil {
		return nil, err
	}
	return &service.WalletFundResult{
		TransactionID: id,
		BalanceBefore: balance,
		BalanceAfter:  after,
		Amount:        in.Amount,
	}, nil
}

// CreditForRefundInTx 在调用方事务内退回退款金额并写入账流水。
//
// 调用方（退款模块）必须把「订单 20→70 + 库存恢复 + 退款状态→60」与本次调用放在同一事务：
// 任一步失败整体回滚，不会出现「退款成功但资金未退回」或「资金退回但退款未成功」。
func (s *sWallet) CreditForRefundInTx(ctx context.Context, tx gdb.TX, in service.WalletCreditInput) (*service.WalletFundResult, error) {
	if in.UserID <= 0 || in.RefundID <= 0 || in.Amount <= 0 || in.Amount > maxWalletBalance {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	key := idempotencyKeyOfRefund(in.RefundID)

	accountID, balance, err := lockAccountInTx(ctx, tx, in.UserID)
	if err != nil {
		return nil, err
	}
	existing, err := findTransactionInTx(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return replayFundResult(existing, in.UserID, in.Amount, directionCredit, txTypeOrderRefund, codes.CodeWalletRefundCreditFailed)
	}
	if balance > maxWalletBalance-in.Amount {
		return nil, codes.New(codes.CodeWalletAmountOverflow)
	}
	after := balance + in.Amount
	if err := updateBalanceInTx(ctx, tx, accountID, in.Amount, "balance <= ?", maxWalletBalance-in.Amount); err != nil {
		return nil, err
	}
	remark := "订单退款退回余额"
	if in.OrderID > 0 {
		remark = fmt.Sprintf("订单 %d 退款退回余额", in.OrderID)
	}
	id, err := insertTransactionInTx(ctx, tx, transactionRecord{
		AccountID:      accountID,
		UserID:         in.UserID,
		TxType:         txTypeOrderRefund,
		Direction:      directionCredit,
		Amount:         in.Amount,
		BalanceBefore:  balance,
		BalanceAfter:   after,
		BizType:        bizTypeRefund,
		BizID:          in.RefundID,
		IdempotencyKey: key,
		Remark:         remark,
	}, codes.CodeWalletRefundCreditFailed)
	if err != nil {
		return nil, err
	}
	return &service.WalletFundResult{
		TransactionID: id,
		BalanceBefore: balance,
		BalanceAfter:  after,
		Amount:        in.Amount,
	}, nil
}

// lockAccountInTx 在事务内保证账户存在并锁读余额（SELECT ... FOR UPDATE）。
// 锁账户行是所有钱包资金操作的统一入口，保证同一用户的资金操作串行。
func lockAccountInTx(ctx context.Context, tx gdb.TX, userID int64) (int64, int64, error) {
	// 先初始化（不存在则插入余额 0；已存在则仅取行锁），并发创建由 uk_user_id 兜底。
	if _, err := tx.Ctx(ctx).Exec(
		"INSERT INTO wallet_accounts (user_id, balance) VALUES (?, 0) ON DUPLICATE KEY UPDATE user_id = user_id",
		userID,
	); err != nil {
		return 0, 0, codes.Wrap(codes.CodeWalletAccountUnavailable, fmt.Errorf("初始化钱包账户: %w", err))
	}
	record, err := tx.Model("wallet_accounts").Ctx(ctx).
		Fields("id", "balance").
		Where("user_id", userID).
		LockUpdate().
		One()
	if err != nil {
		return 0, 0, codes.Wrap(codes.CodeWalletAccountUnavailable, fmt.Errorf("锁定钱包账户: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return 0, 0, codes.New(codes.CodeWalletAccountUnavailable)
	}
	accountID := record["id"].Int64()
	balance := record["balance"].Int64()
	if accountID <= 0 {
		return 0, 0, codes.New(codes.CodeWalletAccountUnavailable)
	}
	return accountID, balance, nil
}

// updateBalanceInTx 执行带条件的原子余额变更（delta 为负表示扣减），0 行按场景返回业务错误。
//
// guard 与 guardArg 由调用方给出：扣减用 `balance >= ?`，入账用 `balance <= ?`。
// 条件更新保证「余额不足/溢出」由数据库判定，不依赖应用层先读后写。
func updateBalanceInTx(ctx context.Context, tx gdb.TX, accountID, delta int64, guard string, guardArg int64) error {
	data := g.Map{"balance": gdb.Raw("balance + " + strconv.FormatInt(delta, 10))}
	result, err := tx.Model("wallet_accounts").Ctx(ctx).
		Where("id", accountID).
		Where(guard, guardArg).
		Data(data).
		Update()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("变更钱包余额: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if delta < 0 {
			return codes.New(codes.CodeWalletInsufficientBalance)
		}
		return codes.New(codes.CodeWalletAmountOverflow)
	}
	return nil
}

// insertTransactionInTx 写入一条资金流水；唯一键冲突一律视为冲突错误（不得当作成功）。
//
// conflictCode 由调用方给出业务语义更精确的错误码（订单重复支付 21006 / 退款入账失败 21007 /
// 通用幂等冲突 21008），避免把所有唯一键冲突都笼统归为同一种错误。
func insertTransactionInTx(ctx context.Context, tx gdb.TX, rec transactionRecord, conflictCode codes.Code) (int64, error) {
	data := g.Map{
		"account_id":      rec.AccountID,
		"user_id":         rec.UserID,
		"tx_type":         rec.TxType,
		"direction":       rec.Direction,
		"amount":          rec.Amount,
		"balance_before":  rec.BalanceBefore,
		"balance_after":   rec.BalanceAfter,
		"biz_type":        rec.BizType,
		"idempotency_key": rec.IdempotencyKey,
		"remark":          rec.Remark,
	}
	if rec.BizID > 0 {
		data["biz_id"] = rec.BizID
	}
	id, err := tx.Model("wallet_transactions").Ctx(ctx).Data(data).InsertAndGetId()
	if err != nil {
		if duplicateKeyName(err) != "" {
			return 0, codes.Wrap(conflictCode, fmt.Errorf("资金流水幂等键冲突: %s", rec.IdempotencyKey))
		}
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入钱包流水: %w", err))
	}
	return id, nil
}

// findTransactionInTx 事务内按幂等键当前读（FOR UPDATE）已存在的流水；不存在返回 nil。
func findTransactionInTx(ctx context.Context, tx gdb.TX, idempotencyKey string) (*existingTransaction, error) {
	record, err := tx.Model("wallet_transactions").Ctx(ctx).
		Fields("id", "user_id", "tx_type", "direction", "amount", "balance_before", "balance_after", "biz_type", "biz_id").
		Where("idempotency_key", idempotencyKey).
		LockUpdate().
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取资金流水: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &existingTransaction{
		Id:            record["id"].Int64(),
		UserId:        record["user_id"].Int64(),
		TxType:        record["tx_type"].String(),
		Direction:     record["direction"].Int(),
		Amount:        record["amount"].Int64(),
		BalanceBefore: record["balance_before"].Int64(),
		BalanceAfter:  record["balance_after"].Int64(),
		BizType:       record["biz_type"].String(),
		BizID:         record["biz_id"].Int64(),
	}, nil
}

// replayFundResult 校验既有流水与本次请求的业务要素一致后返回幂等结果。
// 任何一项不一致（用户/方向/类型/金额）都返回 conflictCode，避免把「同键不同请求」当成功。
func replayFundResult(existing *existingTransaction, userID, amount int64, direction int, txType string, conflictCode codes.Code) (*service.WalletFundResult, error) {
	if existing.UserId != userID || existing.Direction != direction || existing.TxType != txType || existing.Amount != amount {
		return nil, codes.New(conflictCode)
	}
	return &service.WalletFundResult{
		TransactionID:  existing.Id,
		BalanceBefore:  existing.BalanceBefore,
		BalanceAfter:   existing.BalanceAfter,
		Amount:         existing.Amount,
		AlreadyApplied: true,
	}, nil
}

// validateRechargeAmount 校验充值金额：正整数且不超过单笔上限。
func validateRechargeAmount(amount int64) error {
	if amount <= 0 || amount > v1.MaxRechargeAmount {
		return codes.New(codes.CodeWalletInvalidRechargeAmount)
	}
	return nil
}

// idempotencyKeyOfRecharge 返回充值入账的确定性幂等键。
func idempotencyKeyOfRecharge(rechargeOrderID int64) string {
	return "recharge:" + strconv.FormatInt(rechargeOrderID, 10)
}

// idempotencyKeyOfOrderPay 返回订单余额支付的确定性幂等键。
func idempotencyKeyOfOrderPay(orderID int64) string {
	return "order_pay:" + strconv.FormatInt(orderID, 10)
}

// idempotencyKeyOfRefund 返回退款入账的确定性幂等键。
func idempotencyKeyOfRefund(refundID int64) string {
	return "refund:" + strconv.FormatInt(refundID, 10)
}
