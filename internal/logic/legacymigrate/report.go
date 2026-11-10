package legacymigrate

import (
	"fmt"
	"io"
	"strings"
)

// SkipRecord 记录一条未迁移数据及原因（AC-012：逐条失败/跳过原因，真实不编造）。
type SkipRecord struct {
	Domain string // 数据域（categories/products/users/orders/...）
	Entity string // 同 Domain，保留旧字段名兼容既有测试
	OldID  int64
	Reason string
}

// DomainStat 是单个数据域的真实统计（数量全部来自源库查询与实际写入结果）。
//
// 口径：
//   - SourceTotal：源表中该域的全部行数（不做任何过滤）；
//   - Eligible：通过基本筛选、具备迁移资格的行数（例如有效用户、被订单引用的软删商品）；
//   - Migrated：本次实际写入（Dry Run 为「将写入」）的行数；
//   - Skipped：未写入行数（含已迁移幂等跳过、源数据不满足条件、引用缺失）；
//   - Failed：事务级失败行数（已回滚）。
type DomainStat struct {
	Domain      string
	SourceTotal int
	Eligible    int
	Migrated    int
	Skipped     int
	Failed      int
}

// Result 是一次迁移执行的真实统计结果。
type Result struct {
	DryRun bool

	// Domains 按执行顺序保存各数据域统计。
	Domains []*DomainStat
	index   map[string]*DomainStat

	// 以下字段为 B1-03 旧版报告字段，保留以兼容既有调用方/测试；
	// 由 finalize 从 Domains 同步，不作为事实来源。
	CategoriesMigrated  int
	CategoriesSkipped   int
	ProductsMigrated    int
	ProductsSkipped     int
	SkusMigrated        int
	SkusSkipped         int
	InventoriesMigrated int

	// Skipped 逐条记录未迁移数据及原因（成功数量不逐条记录，避免报告冗余）。
	Skipped []SkipRecord
	// Warnings 记录「迁移完成但发生降级」的说明（如父分类缺失 parent_id 置 0）。
	Warnings []string
	// Failures 记录事务级失败（整批回滚）及原因。
	Failures []string
}

// domain 返回数据域统计对象（不存在则按首次出现顺序创建）。
func (r *Result) domain(name string) *DomainStat {
	if r.index == nil {
		r.index = make(map[string]*DomainStat)
	}
	if s, ok := r.index[name]; ok {
		return s
	}
	s := &DomainStat{Domain: name}
	r.index[name] = s
	r.Domains = append(r.Domains, s)
	return s
}

// addSkip 追加一条未迁移记录（计数由调用方通过 DomainStat 维护）。
func (r *Result) addSkip(domain string, oldID int64, reason string) {
	r.Skipped = append(r.Skipped, SkipRecord{Domain: domain, Entity: domain, OldID: oldID, Reason: reason})
}

// addWarning 追加一条降级说明（不改变成功/跳过计数）。
func (r *Result) addWarning(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// addFailure 追加一条事务级失败记录。
func (r *Result) addFailure(format string, args ...any) {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
}

// finalize 把通用数据域统计同步到旧版字段，保证既有调用方兼容。
func (r *Result) finalize() {
	r.CategoriesMigrated = r.domain("categories").Migrated
	r.CategoriesSkipped = r.domain("categories").Skipped
	r.ProductsMigrated = r.domain("products").Migrated
	r.ProductsSkipped = r.domain("products").Skipped
	r.SkusMigrated = r.domain("skus").Migrated
	r.SkusSkipped = r.domain("skus").Skipped
	r.InventoriesMigrated = r.domain("inventories").Migrated
}

// domainLabel 返回报告中的中文数据域名。
func domainLabel(name string) string {
	switch name {
	case "categories":
		return "商品分类"
	case "products":
		return "商品"
	case "product_specs":
		return "商品规格组"
	case "product_spec_values":
		return "商品规格值"
	case "skus":
		return "SKU"
	case "sku_spec_values":
		return "SKU规格关联"
	case "inventories":
		return "库存"
	case "regions":
		return "地区字典"
	case "users":
		return "用户"
	case "addresses":
		return "用户地址"
	case "orders":
		return "订单"
	case "order_items":
		return "订单明细"
	case "order_status_logs":
		return "订单状态流水"
	case "coupon_templates":
		return "优惠券模板"
	case "coupon_batches":
		return "优惠券批次"
	case "coupon_scopes":
		return "优惠券适用范围"
	case "user_coupons":
		return "用户优惠券"
	case "reviews":
		return "商品评价"
	case "favorites":
		return "用户收藏"
	case "wallets":
		return "钱包账户"
	case "wallet_transactions":
		return "钱包期初流水"
	case "points_accounts":
		return "积分账户"
	case "points_logs":
		return "积分期初流水"
	default:
		return name
	}
}

// WriteReport 把统计结果以可读文本写出（供 CLI 与验收脚本消费）。
func (r *Result) WriteReport(w io.Writer) {
	mode := "import"
	if r.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(w, "=== 历史数据迁移报告（%s） ===\n", mode)
	fmt.Fprintf(w, "%-16s %10s %10s %10s %10s %10s\n", "数据域", "源总量", "符合条件", "成功", "跳过", "失败")
	for _, s := range r.Domains {
		fmt.Fprintf(w, "%-16s %10d %10d %10d %10d %10d\n",
			domainLabel(s.Domain), s.SourceTotal, s.Eligible, s.Migrated, s.Skipped, s.Failed)
	}

	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\n-- 跳过明细（共 %d 条） --\n", len(r.Skipped))
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  [%s] old_id=%d: %s\n", s.Domain, s.OldID, s.Reason)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "\n-- 降级说明（共 %d 条，数据仍已迁移） --\n", len(r.Warnings))
		for _, s := range r.Warnings {
			fmt.Fprintf(w, "  %s\n", s)
		}
	}
	if len(r.Failures) > 0 {
		fmt.Fprintf(w, "\n-- 失败明细（共 %d 条，已整批回滚） --\n", len(r.Failures))
		for _, f := range r.Failures {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	if r.DryRun {
		fmt.Fprintf(w, "\n（Dry Run：以上为将执行的变更与校验结果，目标库未写入。）\n")
	}
}

// String 返回报告的字符串形式（便于日志/测试）。
func (r *Result) String() string {
	var b strings.Builder
	r.WriteReport(&b)
	return b.String()
}
