package main

import "strings"

// enumDef 描述一个枚举字段的实际取值。
// 只登记经 DTO 常量/码表核实过的取值，不做业务推断。
type enumDef struct {
	Values []any
	Labels [][2]string // [取值, 业务含义]，保持声明顺序
	Note   string
}

func strEnum(values ...string) enumDef {
	d := enumDef{}
	for _, v := range values {
		d.Values = append(d.Values, v)
	}
	return d
}

func intEnum(values ...int) enumDef {
	d := enumDef{}
	for _, v := range values {
		d.Values = append(d.Values, v)
	}
	return d
}

// withLabels 为枚举补充"数值/取值 -> 业务含义"的对照。
func (d enumDef) withLabels(pairs ...string) enumDef {
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Labels = append(d.Labels, [2]string{pairs[i], pairs[i+1]})
	}
	return d
}

var orderStatusLabels = []string{
	"pending_payment", "待支付（DB 10）",
	"paid", "已支付（DB 20）",
	"shipped", "已发货（DB 30）",
	"received", "已收货（DB 40，实际不可观测）",
	"completed", "已完成（DB 50，终态）",
	"cancelled", "已取消（DB 60，终态）",
	"refunded", "已退款（DB 70，终态）",
}

var refundStatusLabels = []string{
	"applying", "申请中（DB 10）",
	"pending_refund", "待退款（DB 20）",
	"refund_failed", "退款失败（DB 30）",
	"review_rejected", "审核拒绝（DB 40，终态）",
	"revoked", "已撤销（DB 50，终态）",
	"refund_succeeded", "退款成功（DB 60，终态）",
	"closed", "已关闭（DB 70，终态）",
}

// enumRegistry 以 <Schema>.<json 字段名> 为键登记枚举。
var enumRegistry = map[string]enumDef{
	// 商品 / SKU
	"Product.status": strEnum("draft", "on_shelf", "off_shelf").
		withLabels("draft", "草稿", "on_shelf", "已上架", "off_shelf", "已下架"),
	"Sku.status": strEnum("enabled", "disabled").
		withLabels("enabled", "启用", "disabled", "禁用"),
	"SkuCreateRequest.status": strEnum("enabled", "disabled").
		withLabels("enabled", "启用", "disabled", "禁用"),
	"SkuUpdateRequest.status": strEnum("enabled", "disabled").
		withLabels("enabled", "启用", "disabled", "禁用"),

	// 订单
	"Order.status": strEnum(
		"pending_payment", "paid", "shipped", "received", "completed", "cancelled", "refunded").
		withLabels(orderStatusLabels...),
	"OrderCreateRequest.source": strEnum("cart", "direct").
		withLabels("cart", "购物车勾选下单", "direct", "单 SKU 直接购买"),
	"Order.cancel_reason": intEnum(1, 2).
		withLabels("1", "用户取消", "2", "超时未支付自动取消"),
	"OrderAdminListRequest.status": strEnum(
		"pending_payment", "paid", "shipped", "received", "completed", "cancelled", "refunded").
		withLabels(orderStatusLabels...),
	"OrderStatsItem.status": strEnum(
		"pending_payment", "paid", "shipped", "received", "completed", "cancelled", "refunded").
		withLabels(orderStatusLabels...),
	"AdminOrderListItem.status": strEnum(
		"pending_payment", "paid", "shipped", "received", "completed", "cancelled", "refunded").
		withLabels(orderStatusLabels...),

	// 退款
	"Refund.status": strEnum(
		"applying", "pending_refund", "refund_failed", "review_rejected", "revoked", "refund_succeeded", "closed").
		withLabels(refundStatusLabels...),
	"RefundAudit.action": strEnum(
		"apply", "review_approve", "review_reject", "execute_success", "execute_failure", "close", "revoke"),
	"RefundAudit.operator_type": intEnum(1, 2).
		withLabels("1", "用户", "2", "管理员"),
	"RefundAudit.before_status": intEnum(0, 10, 20, 30, 40, 50, 60, 70).
		withLabels("0", "无前置状态（apply）", "10", "申请中", "20", "待退款", "30", "退款失败",
			"40", "审核拒绝", "50", "已撤销", "60", "退款成功", "70", "已关闭"),
	"RefundAudit.after_status": intEnum(10, 20, 30, 40, 50, 60, 70).
		withLabels("10", "申请中", "20", "待退款", "30", "退款失败",
			"40", "审核拒绝", "50", "已撤销", "60", "退款成功", "70", "已关闭"),
	"RefundAdminListRequest.status": strEnum(
		"applying", "pending_refund", "refund_failed", "review_rejected", "revoked", "refund_succeeded", "closed").
		withLabels(refundStatusLabels...),
	"RefundReviewRequest.result": strEnum("approve", "reject").
		withLabels("approve", "通过", "reject", "拒绝"),
	"RefundExecuteRequest.result": strEnum("success", "failure").
		withLabels("success", "退款成功", "failure", "退款失败"),

	// 优惠券
	"UserCoupon.status": strEnum("available", "locked", "used").
		withLabels("available", "可用（DB 10）", "locked", "已预占（DB 20）", "used", "已使用（DB 30）"),
	"CouponTemplate.status": intEnum(0, 1).
		withLabels("0", "已停止新领取", "1", "正常可领取"),

	// 秒杀
	"FlashSaleActivity.status": strEnum("enabled", "disabled").
		withLabels("enabled", "启用", "disabled", "禁用"),
	"FlashSaleCreateOrderResponse.status": strEnum("queued").
		withLabels("queued", "已受理/排队中"),
	"FlashSaleGetOrderResultResponse.status": strEnum("queued", "success", "failed", "dead").
		withLabels("queued", "排队中", "success", "落单成功", "failed", "业务失败终态", "dead", "技术失败待修复"),
	"FlashSaleRepairRequestRequest.target_status": strEnum("queued").
		withLabels("queued", "重新排队"),
	"FlashSaleRepairRequestResponse.status": strEnum("queued").
		withLabels("queued", "重新排队"),
	"FlashSaleRequestAudit.before_status": intEnum(0, 1, 2, 3).
		withLabels("0", "queued", "1", "success", "2", "failed", "3", "dead"),
	"FlashSaleRequestAudit.after_status": intEnum(0, 1, 2, 3).
		withLabels("0", "queued", "1", "success", "2", "failed", "3", "dead"),

	// 评价 / 库存
	"Review.status": strEnum("published", "taken_down", "deleted").
		withLabels("published", "公开（DB 1）", "taken_down", "已下架（DB 2）", "deleted", "已删除（DB 3）"),
	"InventoryLog.change_type": strEnum("increase", "deduct").
		withLabels("increase", "增加", "deduct", "扣减"),

	// 内容 / 组织 / 用户
	"Category.status":              intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"CategoryCreateRequest.status": intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"CategoryUpdateRequest.status": intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"Banner.status":                intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"BannerCreateRequest.status":   intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"BannerUpdateRequest.status":   intEnum(0, 1).withLabels("0", "禁用", "1", "启用"),
	"RecommendPosition.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),
	"RecommendationCreateRequest.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),
	"RecommendationUpdateRequest.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),
	"UserGetUserStatusResponse.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),
	"UserUpdateUserStatusRequest.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),
	"AdminUpdateAdminStatusRequest.status": intEnum(0, 1).
		withLabels("0", "禁用", "1", "启用"),

	// 购物车 / 收藏可用性
	"CartItem.unavailable_reason": strEnum("", "sku_deleted", "disabled", "off_shelf").
		withLabels("", "可购", "sku_deleted", "SKU 已删除", "disabled", "SKU 已禁用", "off_shelf", "商品已下架"),
	"FavoriteItem.unavailable_reason": strEnum("", "off_shelf", "product_deleted").
		withLabels("", "可购", "off_shelf", "商品已下架", "product_deleted", "商品已删除"),

	// 大屏
	"DashboardStatusItem.status": intEnum(10, 20, 30, 40, 50, 60, 70).
		withLabels("10", "待支付", "20", "已支付", "30", "已发货", "40", "已收货",
			"50", "已完成", "60", "已取消", "70", "已退款"),
}

// moneyFields 是经逐一核实的金额字段（整数分）。
// 命名规则只做辅助，最终以该集合 + 后缀规则为准，避免把 sales/count 误标为金额。
var moneyExactFields = map[string]bool{
	"price":              true,
	"amount":             true,
	"total_amount":       true,
	"original_amount":    true,
	"discount_amount":    true,
	"threshold_amount":   true,
	"today_order_amount": true,
	"flash_price":        true,
	"price_snapshot":     true,
	"product_price":      true,
}

// isMoneyField 判断字段是否为"整数分"金额。
func isMoneyField(f *Field) bool {
	if f.Type == nil || f.Type.Pointer {
		return false
	}
	if f.Type.Kind != KindScalar {
		return false
	}
	switch f.Type.Scalar {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
	default:
		return false
	}
	name := f.JSONName
	if moneyExactFields[name] {
		return true
	}
	return strings.HasSuffix(name, "_amount") || strings.HasSuffix(name, "_price")
}
