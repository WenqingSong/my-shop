package legacymigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// 迁移实体类型（写入 legacy_id_map.entity_type）。
// 新增取值只扩展数据，不改变表结构（entity_type VARCHAR(16)）。
const (
	entityCategory       = "category"
	entityProduct        = "product"
	entitySKU            = "sku"
	entityUser           = "user"
	entityAddress        = "address"
	entityOrder          = "order"
	entityOrderItem      = "order_item"
	entityCouponTemplate = "coupon_template"
	entityCouponBatch    = "coupon_batch"
	entityUserCoupon     = "user_coupon"
	entityReview         = "review"
	entityFavorite       = "favorite"
)

// plannedID 是 Dry Run 阶段占位新 ID：真实主键恒为正数，负值只存在于内存映射中，
// 用于让后续迁移域在「不写库」的前提下继续完成引用校验。
const plannedID int64 = -1

// domainContext 是一次迁移执行内共享的源/目标连接、幂等映射与统计上下文。
// 所有迁移域（region/product/user/order/coupon/wallet...）只通过本结构访问共享状态，
// 避免各域自行读取 legacy_id_map 或重复实现幂等判断。
type domainContext struct {
	source *sql.DB
	target *sql.DB
	dryRun bool
	ids    *idMap
	result *Result
}

// domainRunner 是一个可独立实现、独立测试的迁移域。
type domainRunner interface {
	// name 返回数据域名（用于错误与报告；与 DomainStat.Domain 一致）。
	name() string
	// run 执行该域的迁移；dryRun=true 时只校验与统计，禁止写目标库。
	run(ctx context.Context, dc *domainContext) error
}

// stat 返回数据域统计对象（不存在则创建）。
func (dc *domainContext) stat(domain string) *DomainStat {
	return dc.result.domain(domain)
}

// mapped 查询旧 ID 对应的新 ID；Dry Run 阶段返回 plannedID。
func (dc *domainContext) mapped(entity string, oldID int64) (int64, bool) {
	return dc.ids.get(entity, oldID)
}

// required 要求旧 ID 已映射（已迁移或 Dry Run 已规划），否则返回错误。
func (dc *domainContext) required(entity string, oldID int64) (int64, error) {
	newID, ok := dc.ids.get(entity, oldID)
	if !ok {
		return 0, fmt.Errorf("缺少 %s 旧 ID=%d 的迁移映射", entity, oldID)
	}
	return newID, nil
}

// plan 在 Dry Run 阶段登记「将迁移」占位映射，供后续域做引用校验。
func (dc *domainContext) plan(entity string, oldID int64) {
	if dc.dryRun {
		dc.ids.put(entity, oldID, plannedID)
	}
}

// record 在目标库事务内写入 legacy_id_map 并同步更新内存映射（业务行与映射同事务，INV-006）。
func (dc *domainContext) record(ctx context.Context, tx *sql.Tx, entity string, oldID, newID int64) error {
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO legacy_id_map (source, entity_type, old_id, new_id) VALUES (?, ?, ?, ?)",
		SourceName, entity, oldID, newID); err != nil {
		return fmt.Errorf("写入映射 %s old_id=%d: %w", entity, oldID, err)
	}
	dc.ids.put(entity, oldID, newID)
	return nil
}

// skip 记录一条未迁移数据及原因，并累计所属数据域跳过数。
// 调用方必须在统计 source/eligible 之后调用，保证跳过数与源数据可解释。
func (dc *domainContext) skip(domain string, oldID int64, reason string) {
	dc.stat(domain).Skipped++
	dc.result.addSkip(domain, oldID, reason)
}

// skipAlready 记录「已迁移，本次跳过」，不写逐条明细（重复执行时避免报告膨胀）。
func (dc *domainContext) skipAlready(domain string) {
	dc.stat(domain).Skipped++
}

// warn 记录一次降级/数据异常说明（数据仍迁移成功）。
func (dc *domainContext) warn(domain, format string, args ...any) {
	dc.result.addWarning("["+domain+"] "+format, args...)
}

// fail 记录一次事务级失败（该批已回滚）。
func (dc *domainContext) fail(domain, format string, args ...any) {
	dc.stat(domain).Failed++
	dc.result.addFailure("["+domain+"] "+format, args...)
}
