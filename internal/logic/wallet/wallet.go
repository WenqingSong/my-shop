// Package wallet 实现钱包业务：账户余额查询、资金流水分页查询、Mock 充值，
// 以及供订单/退款模块在既有事务内复用的余额扣减与退款入账能力。
//
// 资金一致性约束（与 docs/design/wallet.md 一致）：
//   - 余额只经条件 UPDATE 变更：扣减 `balance = balance - ? WHERE id = ? AND balance >= ?`，
//     入账 `balance = balance + ? WHERE id = ? AND balance <= ?`（上限为 INT UNSIGNED 可表示范围）；
//   - 每次余额变更必须写一条 wallet_transactions（记录 before/after），余额与流水同事务；
//   - 幂等键确定性且全局唯一：recharge:<recharge_order_id> / order_pay:<order_id> / refund:<refund_id>；
//   - 同一用户的资金操作先锁 wallet_accounts 行，把并发资金操作串行化，避免「先查余额再判断」竞态。
package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 资金流水常量（与 20261110000006_wallet.up.sql 的字段语义一一对应）。
const (
	directionCredit = 1 // 入账
	directionDebit  = 2 // 出账

	txTypeRecharge    = v1.TxTypeRecharge
	txTypeOrderPay    = v1.TxTypeOrderPay
	txTypeOrderRefund = v1.TxTypeOrderRefund

	bizTypeRecharge = "recharge"
	bizTypeOrder    = "order"
	bizTypeRefund   = "refund"

	// mockPayType 是 Mock 充值订单的支付方式标记（不接入真实渠道）。
	mockPayType = "mock"

	// rechargeOrderNoPrefix 是充值单号前缀；单号由 user_id + idempotency_key 确定性派生，
	// 长度固定 32（VARCHAR(32)），保证「同一用户同一幂等键 → 同一充值单」。
	rechargeOrderNoPrefix = "RC"

	// rechargeOrderNoHashLen 是确定性单号中哈希前缀的长度（32 - len("RC")）。
	rechargeOrderNoHashLen = 30

	// maxWalletBalance 是 wallet_accounts.balance（INT UNSIGNED）可表示的余额上限。
	// 入账前显式校验，避免依赖 MySQL 无符号溢出/严格模式行为。
	maxWalletBalance int64 = 4294967295

	// maxIdempotencyKeyLen 与 wallet_transactions.idempotency_key（VARCHAR(64)）对齐。
	maxIdempotencyKeyLen = 64

	// 分页默认值与上限（与项目其它分页接口一致）。
	defaultPage = 1
	defaultSize = 20
	maxPageSize = 100
)

// 充值单状态（DB TINYINT）。
const (
	rechargeStatusPending   = 10
	rechargeStatusSucceeded = 20
	rechargeStatusCancelled = 30
	rechargeStatusFailed    = 40
)

type sWallet struct{}

func init() {
	service.RegisterWallet(New())
}

// New 创建并返回钱包服务实现。
func New() *sWallet {
	return &sWallet{}
}

// Account 查询本人钱包余额；账户不存在时安全初始化（余额 0），不写流水。
func (s *sWallet) Account(ctx context.Context, userID int64) (*v1.AccountRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	accountID, err := s.ensureAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	record, err := g.DB().Model("wallet_accounts").Ctx(ctx).
		Fields("balance", "updated_at").
		Where("id", accountID).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeWalletAccountUnavailable, fmt.Errorf("查询钱包账户: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, codes.New(codes.CodeWalletAccountUnavailable)
	}
	res := &v1.AccountRes{Balance: record["balance"].Int64()}
	if updatedAt := record["updated_at"].GTime(); updatedAt != nil {
		res.UpdatedAt = updatedAt
	}
	return res, nil
}

// Transactions 分页查询本人资金流水（按 id 倒序，可按 tx_type 过滤）。
//
// 账户不存在时返回空列表而不是错误：只读查询不产生副作用，也不因「未充值过」暴露 404。
func (s *sWallet) Transactions(ctx context.Context, userID int64, req *v1.TransactionListReq) (*v1.TransactionListRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	page, size, err := normalizePaging(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	txType := strings.TrimSpace(req.TxType)
	if txType != "" && !validTxType(txType) {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	query := g.DB().Model("wallet_transactions").Ctx(ctx).Where("user_id", userID)
	if txType != "" {
		query = query.Where("tx_type", txType)
	}
	total, err := query.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计钱包流水: %w", err))
	}
	res := &v1.TransactionListRes{Items: make([]*v1.Transaction, 0, size), Total: total, Page: page, Size: size}
	if total == 0 {
		return res, nil
	}

	records, err := query.Clone().
		Fields("id", "tx_type", "direction", "amount", "balance_before", "balance_after", "biz_type", "biz_id", "remark", "created_at").
		OrderDesc("id").
		Page(page, size).
		All()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询钱包流水: %w", err))
	}
	for _, record := range records {
		item := &v1.Transaction{
			Id:            record["id"].Int64(),
			TxType:        record["tx_type"].String(),
			Direction:     directionText(record["direction"].Int()),
			Amount:        record["amount"].Int64(),
			BalanceBefore: record["balance_before"].Int64(),
			BalanceAfter:  record["balance_after"].Int64(),
			BizType:       record["biz_type"].String(),
			Remark:        record["remark"].String(),
			CreatedAt:     record["created_at"].GTime(),
		}
		if !record["biz_id"].IsNil() && record["biz_id"].Int64() > 0 {
			bizID := record["biz_id"].Int64()
			item.BizId = &bizID
		}
		res.Items = append(res.Items, item)
	}
	return res, nil
}

// Recharge 执行 Mock 充值：充值单 + 钱包余额 + 资金流水同一事务提交。
//
// 幂等：充值单号由 (user_id, idempotency_key) 确定性派生，重复请求命中 uk_order_no 后回读既有单：
//   - 金额一致且已成功 → 幂等成功（不重复入账），返回既有结果；
//   - 金额不同 → 21008；
//   - 状态非成功 → 21005（不重复推进状态）。
func (s *sWallet) Recharge(ctx context.Context, userID int64, req *v1.RechargeReq) (*v1.RechargeRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	// 安全开关：Mock 充值凭空增加资金，必须显式启用；默认关闭（生产严格模式禁止启用）。
	enabled, err := config.Bool(ctx, config.KeyWalletMockRechargeEnabled)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取 Mock 充值开关: %w", err))
	}
	if !enabled {
		return nil, codes.New(codes.CodeWalletMockRechargeDisabled)
	}
	if err := validateRechargeAmount(req.Amount); err != nil {
		return nil, err
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" || utf8.RuneCountInString(idempotencyKey) > maxIdempotencyKeyLen {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	orderNo := rechargeOrderNo(userID, idempotencyKey)
	var (
		rechargeID int64
		balance    int64
		applied    bool
	)
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 1) 先锁账户行：同一用户的资金操作串行化，后续幂等判定读到的是已提交版本。
		accountID, before, e := lockAccountInTx(ctx, tx, userID)
		if e != nil {
			return e
		}
		// 2) 幂等判定（确定性单号）：已存在则按金额/状态决定幂等成功或冲突。
		existing, e := findRechargeOrderInTx(ctx, tx, orderNo)
		if e != nil {
			return e
		}
		if existing != nil {
			return s.replayRechargeInTx(ctx, tx, existing, userID, req.Amount, &rechargeID, &balance)
		}
		applied = true
		// 3) 创建充值单（待支付），撞唯一键说明并发同键请求已先行插入，交由外层回读。
		id, e := tx.Model("recharge_orders").Ctx(ctx).Data(g.Map{
			"order_no": orderNo,
			"user_id":  userID,
			"amount":   req.Amount,
			"status":   rechargeStatusPending,
			"pay_type": "",
		}).InsertAndGetId()
		if e != nil {
			if duplicateKeyName(e) != "" {
				return errRechargeOrderExists
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建充值单: %w", e))
		}
		// 4) 余额入账（条件 UPDATE，带溢出上限保护）。
		if before > maxWalletBalance-req.Amount {
			return codes.New(codes.CodeWalletAmountOverflow)
		}
		after := before + req.Amount
		if e := updateBalanceInTx(ctx, tx, accountID, req.Amount, "balance <= ?", maxWalletBalance-req.Amount); e != nil {
			return e
		}
		// 5) 写资金流水（幂等键 recharge:<id>）。
		if _, e := insertTransactionInTx(ctx, tx, transactionRecord{
			AccountID:      accountID,
			UserID:         userID,
			TxType:         txTypeRecharge,
			Direction:      directionCredit,
			Amount:         req.Amount,
			BalanceBefore:  before,
			BalanceAfter:   after,
			BizType:        bizTypeRecharge,
			BizID:          id,
			IdempotencyKey: idempotencyKeyOfRecharge(id),
			Remark:         "Mock 充值",
		}, codes.CodeWalletIdempotencyConflict); e != nil {
			return e
		}
		// 6) 充值单状态 10→20：条件更新 + RowsAffected 闸门，保证「入账一次且只有一次」。
		result, e := tx.Model("recharge_orders").Ctx(ctx).
			Where("id", id).
			Where("status", rechargeStatusPending).
			Data(g.Map{
				"status":   rechargeStatusSucceeded,
				"pay_type": mockPayType,
				"paid_at":  gdb.Raw("NOW()"),
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进充值单状态: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeWalletRechargeStatusNotAllowed)
		}
		rechargeID = id
		balance = after
		return nil
	})
	if errors.Is(err, errRechargeOrderExists) {
		// 并发同键请求：以提交后的既有单为准回读，不重复入账。
		existing, e := s.loadRechargeOrder(ctx, userID, orderNo)
		if e != nil {
			return nil, e
		}
		if existing == nil {
			return nil, codes.New(codes.CodeWalletRechargeStatusNotAllowed)
		}
		return s.idempotentRechargeResult(ctx, existing, userID, req.Amount)
	}
	if err != nil {
		return nil, err
	}

	row, err := s.loadRechargeOrderByID(ctx, rechargeID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeWalletRechargeOrderNotFound)
	}
	if applied {
		logx.Info(ctx, "wallet_recharge_succeeded", "Mock 充值成功", logx.Fields{
			"user_id":     userID,
			"recharge_id": rechargeID,
			"amount":      req.Amount,
			"balance":     balance,
		})
	}
	return &v1.RechargeRes{RechargeOrder: *row}, nil
}

// replayRechargeInTx 处理「确定性单号已存在」的幂等路径（事务内）。
func (s *sWallet) replayRechargeInTx(
	ctx context.Context,
	tx gdb.TX,
	existing *rechargeRow,
	userID, amount int64,
	rechargeID, balance *int64,
) error {
	if existing.UserId != userID {
		// 不同用户不可能共享同一派生单号；按「不存在」处理，避免泄露他人充值单。
		return codes.New(codes.CodeWalletRechargeOrderNotFound)
	}
	if existing.Amount != amount {
		return codes.New(codes.CodeWalletIdempotencyConflict)
	}
	if existing.Status != rechargeStatusSucceeded {
		return codes.New(codes.CodeWalletRechargeStatusNotAllowed)
	}
	// 回读既有充值流水，确认「已入账一次」并返回其余额快照。
	record, e := tx.Model("wallet_transactions").Ctx(ctx).
		Fields("id", "balance_before", "balance_after", "amount").
		Where("idempotency_key", idempotencyKeyOfRecharge(existing.Id)).
		One()
	if e != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("回读充值流水: %w", e))
	}
	if record == nil || record.IsEmpty() {
		// 充值单成功却没有流水，说明数据已被外部破坏；拒绝把冲突当成功。
		return codes.New(codes.CodeWalletAccountUnavailable)
	}
	*rechargeID = existing.Id
	*balance = record["balance_after"].Int64()
	return nil
}

// idempotentRechargeResult 在并发撞键后按既有充值单构造幂等响应。
func (s *sWallet) idempotentRechargeResult(ctx context.Context, existing *rechargeRow, userID, amount int64) (*v1.RechargeRes, error) {
	if existing.UserId != userID {
		return nil, codes.New(codes.CodeWalletRechargeOrderNotFound)
	}
	if existing.Amount != amount {
		return nil, codes.New(codes.CodeWalletIdempotencyConflict)
	}
	if existing.Status != rechargeStatusSucceeded {
		return nil, codes.New(codes.CodeWalletRechargeStatusNotAllowed)
	}
	row, err := s.loadRechargeOrderByID(ctx, existing.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeWalletRechargeOrderNotFound)
	}
	return &v1.RechargeRes{RechargeOrder: *row}, nil
}

// ensureAccount 保证当前用户存在钱包账户（不存在则初始化余额 0），返回账户 id。
//
// 使用 INSERT ... ON DUPLICATE KEY UPDATE 由 uk_user_id 兜底并发创建，不依赖「先查再插」。
func (s *sWallet) ensureAccount(ctx context.Context, userID int64) (int64, error) {
	if _, err := g.DB().Exec(ctx,
		"INSERT INTO wallet_accounts (user_id, balance) VALUES (?, 0) ON DUPLICATE KEY UPDATE user_id = user_id",
		userID,
	); err != nil {
		return 0, codes.Wrap(codes.CodeWalletAccountUnavailable, fmt.Errorf("初始化钱包账户: %w", err))
	}
	value, err := g.DB().Model("wallet_accounts").Ctx(ctx).Where("user_id", userID).Value("id")
	if err != nil {
		return 0, codes.Wrap(codes.CodeWalletAccountUnavailable, fmt.Errorf("查询钱包账户: %w", err))
	}
	if value == nil || value.IsNil() || value.Int64() <= 0 {
		return 0, codes.New(codes.CodeWalletAccountUnavailable)
	}
	return value.Int64(), nil
}

// rechargeRow 是 recharge_orders 表的一行（仅内部使用，不对外暴露）。
type rechargeRow struct {
	Id        int64
	OrderNo   string
	UserId    int64
	Amount    int64
	Status    int
	PayType   string
	PaidAt    *gtime.Time
	CreatedAt *gtime.Time
}

// findRechargeOrderInTx 事务内按单号锁定并读取充值单；不存在返回 nil。
func findRechargeOrderInTx(ctx context.Context, tx gdb.TX, orderNo string) (*rechargeRow, error) {
	record, err := tx.Model("recharge_orders").Ctx(ctx).
		Fields("id", "order_no", "user_id", "amount", "status", "pay_type", "paid_at", "created_at").
		Where("order_no", orderNo).
		LockUpdate().
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定充值单: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return rechargeRowFrom(record), nil
}

// loadRechargeOrder 按 (user_id, order_no) 读取充值单（归属不符视为不存在）。
func (s *sWallet) loadRechargeOrder(ctx context.Context, userID int64, orderNo string) (*rechargeRow, error) {
	record, err := g.DB().Model("recharge_orders").Ctx(ctx).
		Fields("id", "order_no", "user_id", "amount", "status", "pay_type", "paid_at", "created_at").
		Where("order_no", orderNo).
		Where("user_id", userID).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询充值单: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return rechargeRowFrom(record), nil
}

// loadRechargeOrderByID 按 id 读取充值单并转换为 API 出参。
func (s *sWallet) loadRechargeOrderByID(ctx context.Context, id int64) (*v1.RechargeOrder, error) {
	record, err := g.DB().Model("recharge_orders").Ctx(ctx).
		Fields("id", "order_no", "user_id", "amount", "status", "pay_type", "paid_at", "created_at").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询充值单: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	row := rechargeRowFrom(record)
	return &v1.RechargeOrder{
		Id:        row.Id,
		OrderNo:   row.OrderNo,
		Amount:    row.Amount,
		Status:    rechargeStatusText(row.Status),
		PayType:   row.PayType,
		PaidAt:    row.PaidAt,
		CreatedAt: row.CreatedAt,
	}, nil
}

// rechargeRowFrom 把数据库行映射为内部结构。
func rechargeRowFrom(record gdb.Record) *rechargeRow {
	return &rechargeRow{
		Id:        record["id"].Int64(),
		OrderNo:   record["order_no"].String(),
		UserId:    record["user_id"].Int64(),
		Amount:    record["amount"].Int64(),
		Status:    record["status"].Int(),
		PayType:   record["pay_type"].String(),
		PaidAt:    record["paid_at"].GTime(),
		CreatedAt: record["created_at"].GTime(),
	}
}

// rechargeOrderNo 由 (userID, idempotencyKey) 确定性派生充值单号。
// 单号长度固定 32，落在 recharge_orders.order_no VARCHAR(32) 内且满足 uk_order_no。
func rechargeOrderNo(userID int64, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", userID, idempotencyKey)))
	return rechargeOrderNoPrefix + hex.EncodeToString(sum[:])[:rechargeOrderNoHashLen]
}

// normalizePaging 校验并归一化分页参数（越界返回 400，而不是静默截断）。
func normalizePaging(page, size *int) (int, int, error) {
	p, s := defaultPage, defaultSize
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	if size != nil {
		if *size < 1 || *size > maxPageSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		s = *size
	}
	return p, s, nil
}

// validTxType 判断交易类型是否属于已登记的流水类型。
func validTxType(txType string) bool {
	switch txType {
	case txTypeRecharge, txTypeOrderPay, txTypeOrderRefund:
		return true
	default:
		return false
	}
}

// directionText 把 TINYINT 方向映射为出参枚举。
func directionText(direction int) string {
	if direction == directionDebit {
		return v1.DirectionDebit
	}
	return v1.DirectionCredit
}

// rechargeStatusText 把 TINYINT 充值状态映射为出参枚举。
func rechargeStatusText(status int) string {
	switch status {
	case rechargeStatusSucceeded:
		return v1.RechargeStatusSucceeded
	case rechargeStatusCancelled:
		return v1.RechargeStatusCancelled
	case rechargeStatusFailed:
		return v1.RechargeStatusFailed
	default:
		return v1.RechargeStatusPending
	}
}

// duplicateKeyName 从 MySQL 错误中提取唯一键名；非 1062 返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		return ""
	}
	return mysqlErr.Message
}
