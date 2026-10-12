package legacymigrate

import (
	"fmt"
	"io"
	"strings"
)

// SkipRecord 记录一条被跳过的数据及原因（AC-012：逐条失败/跳过原因，真实不编造）。
type SkipRecord struct {
	Entity string // category / product / sku
	OldID  int64
	Reason string
}

// Result 是一次迁移执行的真实统计结果（AC-012）。
type Result struct {
	DryRun bool

	CategoriesMigrated  int
	CategoriesSkipped   int
	ProductsMigrated    int
	ProductsSkipped     int
	SkusMigrated        int
	SkusSkipped         int
	InventoriesMigrated int

	// Skipped 逐条记录被跳过数据及原因（成功数量不逐条记录，避免报告冗余）。
	Skipped []SkipRecord
	// Failures 记录事务级失败（整批回滚）及原因。
	Failures []string
}

// addSkip 追加一条跳过记录并累计对应实体计数。
func (r *Result) addSkip(entity string, oldID int64, reason string) {
	r.Skipped = append(r.Skipped, SkipRecord{Entity: entity, OldID: oldID, Reason: reason})
	switch entity {
	case "category":
		r.CategoriesSkipped++
	case "product":
		r.ProductsSkipped++
	case "sku":
		r.SkusSkipped++
	}
}

// addFailure 追加一条事务级失败记录。
func (r *Result) addFailure(format string, args ...any) {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
}

// WriteReport 把统计结果以可读文本写出（供 CLI 与验收脚本消费）。
func (r *Result) WriteReport(w io.Writer) {
	mode := "import"
	if r.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(w, "=== 历史数据迁移报告（%s） ===\n", mode)
	fmt.Fprintf(w, "分类：成功 %d / 跳过 %d\n", r.CategoriesMigrated, r.CategoriesSkipped)
	fmt.Fprintf(w, "商品：成功 %d / 跳过 %d\n", r.ProductsMigrated, r.ProductsSkipped)
	fmt.Fprintf(w, "SKU：成功 %d / 跳过 %d\n", r.SkusMigrated, r.SkusSkipped)
	fmt.Fprintf(w, "库存：成功 %d\n", r.InventoriesMigrated)

	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\n-- 跳过明细（共 %d 条） --\n", len(r.Skipped))
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  [%s] old_id=%d: %s\n", s.Entity, s.OldID, s.Reason)
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
