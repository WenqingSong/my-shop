package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
)

// WalletDebitInput 是「余额支付订单」的资金扣减入参。
//
// 幂等键固定为 order_pay:<OrderID>（与 docs/design/wallet.md §4 一致），不接受调用方自定义，
// 避免同一订单出现多个可扣款口径。
type WalletDebitInput struct {
	// UserID 是订单归属用户，由调用方从已校验的订单行派生，不来自客户端。
	UserID int64
	// OrderID 是业务订单 id（幂等键与 biz_id 均以它为准）。
	OrderID int64
	// Amount 是应付金额（整数分，必须为正）。
	Amount int64
}

// WalletCreditInput 是「退款退回余额」的资金入账入参。
//
// 幂等键固定为 refund:<RefundID>。
type WalletCreditInput struct {
	// UserID 是退款单归属用户，由调用方从已校验的退款单派生。
	UserID int64
	// RefundID 是退款申请 id（幂等键与 biz_id 均以它为准）。
	RefundID int64
	// OrderID 是原订单 id（仅写 remark 追溯，不参与幂等判定）。
	OrderID int64
	// Amount 是实际退款金额（整数分，必须为正）。
	Amount int64
}

// WalletFundResult 是一次钱包资金操作的结果。
type WalletFundResult struct {
	// TransactionID 是本次（或既有）流水的 id。
	TransactionID int64
	// BalanceBefore / BalanceAfter 是流水记录的余额快照。
	BalanceBefore int64
	BalanceAfter  int64
	// Amount 是本次（或既有）流水的金额。
	Amount int64
	// AlreadyApplied 为 true 表示幂等键已存在且业务要素一致：本次未再次变动资金，
	// 返回的是既有流水的余额快照（调用方可据此判定「已处理」而不重复推进业务）。
	AlreadyApplied bool
}

// IWallet 定义钱包服务：账户查询、流水分页查询、Mock 充值，
// 以及供订单/退款模块在既有 MySQL 事务内复用的扣款与入账能力。
//
// 事务约束：DebitForOrderInTx / CreditForRefundInTx 必须在调用方事务内执行，
// 自身不开事务、不跨事务；失败一律返回错误由调用方回滚整个事务。
type IWallet interface {
	// Account 查询本人钱包余额；账户不存在时按需安全初始化（余额 0），不产生流水。
	Account(ctx context.Context, userID int64) (*v1.AccountRes, error)
	// Transactions 分页查询本人资金流水（按 id 倒序，可按 tx_type 过滤）。
	Transactions(ctx context.Context, userID int64, req *v1.TransactionListReq) (*v1.TransactionListRes, error)
	// Recharge 执行 Mock 充值：充值单 + 余额 + 流水同事务，按 idempotency_key 幂等。
	Recharge(ctx context.Context, userID int64, req *v1.RechargeReq) (*v1.RechargeRes, error)
	// DebitForOrderInTx 在既有事务内扣减订单应付金额并写扣款流水（幂等键 order_pay:<order_id>）。
	// 余额不足返回 21002 且不改变余额；幂等重复调用返回 AlreadyApplied=true 且不重复扣款。
	DebitForOrderInTx(ctx context.Context, tx gdb.TX, in WalletDebitInput) (*WalletFundResult, error)
	// CreditForRefundInTx 在既有事务内退回退款金额并写入账流水（幂等键 refund:<refund_id>）。
	// 幂等重复调用返回 AlreadyApplied=true 且不重复入账。
	CreditForRefundInTx(ctx context.Context, tx gdb.TX, in WalletCreditInput) (*WalletFundResult, error)
}

var localWallet IWallet

// Wallet 返回钱包服务实现。
func Wallet() IWallet {
	if localWallet == nil {
		panic("implement not found for interface IWallet, forgot register?")
	}
	return localWallet
}

// RegisterWallet 注册钱包服务实现。
func RegisterWallet(s IWallet) {
	localWallet = s
}
