// Package v1 定义钱包（Wallet）用户端 API 契约：余额查询、资金流水分页查询与 Mock 充值。
//
// 边界：任何请求都不接受 user_id/account_id；归属一律取自已认证 Principal.UserID。
// 金额为整数分（int64），出参不暴露内部数据库字段（账户 id、幂等键等）。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 资金流水方向（出参字符串枚举；DB TINYINT：1=入账、2=出账）。
const (
	DirectionCredit = "credit" // 入账（1，balance_after = balance_before + amount）
	DirectionDebit  = "debit"  // 出账（2，balance_after = balance_before - amount）
)

// 充值单状态（出参字符串枚举；DB TINYINT 10/20/30/40）。
const (
	RechargeStatusPending   = "pending"   // 待支付（10）
	RechargeStatusSucceeded = "succeeded" // 支付成功（20，唯一入账状态）
	RechargeStatusCancelled = "cancelled" // 已取消（30）
	RechargeStatusFailed    = "failed"    // 支付失败（40）
)

// 资金流水业务类型（tx_type），与 DB 的 wallet_transactions.tx_type 取值一致。
const (
	TxTypeRecharge    = "recharge"     // Mock 充值
	TxTypeOrderPay    = "order_pay"    // 余额支付订单
	TxTypeOrderRefund = "order_refund" // 退款退回余额
)

// 单笔充值金额上限（整数分，1_000_000 分 = ¥10,000）。
// 上限不是业务计价规则，而是「Mock 充值凭空造钱」场景下的风险收敛：既限制单次可造金额，
// 也保证 balance_before + amount 不会逼近 INT UNSIGNED 上限。
const MaxRechargeAmount int64 = 1_000_000

// AccountReq 查询本人钱包余额。
type AccountReq struct {
	g.Meta `path:"/wallet" method:"get" tags:"钱包" summary:"查询钱包余额"`
}

// AccountRes 钱包余额响应（不含内部账户 id 与归属用户 id）。
type AccountRes struct {
	Balance   int64       `json:"balance" dc:"可用余额（整数分）"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"账户记录最后更新时间（初始化或余额变更触发）"`
}

// Transaction 资金流水（append-only，只读）。
type Transaction struct {
	Id            int64       `json:"id" dc:"流水 id"`
	TxType        string      `json:"tx_type" dc:"交易类型：recharge/order_pay/order_refund"`
	Direction     string      `json:"direction" dc:"交易方向：credit=入账、debit=出账"`
	Amount        int64       `json:"amount" dc:"交易金额（整数分，恒为正数）"`
	BalanceBefore int64       `json:"balance_before" dc:"交易前余额（整数分）"`
	BalanceAfter  int64       `json:"balance_after" dc:"交易后余额（整数分）"`
	BizType       string      `json:"biz_type" dc:"业务类型：recharge/order/refund"`
	BizId         *int64      `json:"biz_id" dc:"业务关联 id（充值单/订单/退款申请 id，可空）"`
	Remark        string      `json:"remark" dc:"备注"`
	CreatedAt     *gtime.Time `json:"created_at" dc:"创建时间"`
}

// TransactionListReq 查询本人资金流水请求（按创建时间倒序）。
type TransactionListReq struct {
	g.Meta `path:"/wallet/transactions" method:"get" tags:"钱包" summary:"查询钱包资金流水"`
	Page   *int   `json:"page" dc:"页码，默认 1；<1 返回 400"`
	Size   *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
	TxType string `json:"tx_type" dc:"按交易类型筛选：recharge/order_pay/order_refund；缺省为不过滤"`
}

// TransactionListRes 资金流水分页响应。
type TransactionListRes struct {
	Items []*Transaction `json:"items" dc:"资金流水列表（按 id 倒序，仅本人）"`
	Total int            `json:"total" dc:"总记录数"`
	Page  int            `json:"page" dc:"当前页码"`
	Size  int            `json:"size" dc:"每页数量"`
}

// RechargeReq Mock 充值请求（仅登录用户，作用于 Principal.UserID）。
//
// IdempotencyKey 是充值幂等键：同一用户重复提交同一键只会产生一笔充值单、只入账一次；
// 同键不同金额返回 21008（幂等冲突），不会被当作成功。
type RechargeReq struct {
	g.Meta         `path:"/wallet/recharges" method:"post" tags:"钱包" summary:"Mock 充值"`
	Amount         int64  `json:"amount" v:"required" dc:"充值金额（整数分，1 ~ 1000000）"`
	IdempotencyKey string `json:"idempotency_key" v:"required" dc:"充值幂等键（同键同金额幂等，同键不同金额 21008）"`
}

// RechargeOrder Mock 充值订单（出参）。
type RechargeOrder struct {
	Id        int64       `json:"id" dc:"充值单 id"`
	OrderNo   string      `json:"order_no" dc:"充值单号（全局唯一）"`
	Amount    int64       `json:"amount" dc:"充值金额（整数分）"`
	Status    string      `json:"status" dc:"状态：pending/succeeded/cancelled/failed"`
	PayType   string      `json:"pay_type" dc:"支付方式（Mock 固定 mock；未支付为空串）"`
	PaidAt    *gtime.Time `json:"paid_at" dc:"支付完成时间（仅 succeeded 有值）"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
}

// RechargeRes Mock 充值响应。
type RechargeRes struct {
	RechargeOrder
}
