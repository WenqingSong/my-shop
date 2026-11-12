package legacymigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// legacyCoupon 是 store_coupon（模板）参与迁移的字段。
type legacyCoupon struct {
	ID         int64
	Title      string
	Price      string
	MinPrice   string
	CouponTime int
	Status     int
	ProductIDs string
	Type       int
	IsDel      int
	CreateTime sql.NullTime
}

// legacyCouponBatch 是 store_coupon_issue（发行批次）参与迁移的字段。
type legacyCouponBatch struct {
	ID          int64
	TemplateID  int64
	StartTime   sql.NullTime
	EndTime     sql.NullTime
	TotalCount  int64
	RemainCount int64
	IsPermanent int
	Status      int
	IsDel       int
	CreateTime  sql.NullTime
}

// legacyIssueUser 是 store_coupon_issue_user（领取流水）。
type legacyIssueUser struct {
	ID         int64
	UID        int64
	BatchID    int64
	CreateTime sql.NullTime
}

// legacyUserCoupon 是 store_coupon_user（用户券实例，含权益快照）。
type legacyUserCoupon struct {
	ID         int64
	TemplateID int64
	UID        int64
	Title      string
	Price      string
	MinPrice   string
	CreateTime sql.NullTime
	EndTime    sql.NullTime
	UseTime    sql.NullTime
	Status     int
	IsFail     int
	IsDel      int
}

// couponDomain 迁移优惠券模板、发行批次、适用范围与用户券。
// 批次先于用户券写入；用户券先于订单写入（订单需要 user_coupon_id 映射）。
type couponDomain struct{}

func (couponDomain) name() string { return "coupons" }

func (couponDomain) run(ctx context.Context, dc *domainContext) error {
	templates, err := loadLegacyCoupons(ctx, dc.source)
	if err != nil {
		return err
	}
	batches, err := loadLegacyCouponBatches(ctx, dc.source)
	if err != nil {
		return err
	}

	batchesByTemplate := make(map[int64][]*legacyCouponBatch)
	for _, b := range batches {
		batchesByTemplate[b.TemplateID] = append(batchesByTemplate[b.TemplateID], b)
	}

	if err := migrateCouponTemplates(ctx, dc, templates, batchesByTemplate); err != nil {
		return err
	}
	if err := migrateCouponBatches(ctx, dc, batches, templates); err != nil {
		return err
	}
	if err := migrateCouponScopes(ctx, dc, templates); err != nil {
		return err
	}
	return migrateUserCoupons(ctx, dc)
}

func migrateCouponTemplates(ctx context.Context, dc *domainContext, templates []*legacyCoupon, batches map[int64][]*legacyCouponBatch) error {
	stat := dc.stat("coupon_templates")
	stat.SourceTotal = len(templates)

	issuedCount := make(map[int64]int)
	rows, err := dc.source.QueryContext(ctx, "SELECT cid, COUNT(*) FROM store_coupon_user WHERE is_del = 0 GROUP BY cid")
	if err != nil {
		return fmt.Errorf("统计用户券: %w", err)
	}
	for rows.Next() {
		var cid int64
		var n int
		if err := rows.Scan(&cid, &n); err != nil {
			rows.Close()
			return fmt.Errorf("扫描用户券统计: %w", err)
		}
		issuedCount[cid] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历用户券统计: %w", err)
	}

	for _, c := range templates {
		if c.IsDel == 1 {
			dc.skip("coupon_templates", c.ID, "源优惠券模板已软删除")
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityCouponTemplate, c.ID); ok {
			dc.skipAlready("coupon_templates")
			continue
		}

		price, err := YuanToCents(c.Price)
		if err != nil {
			dc.skip("coupon_templates", c.ID, "券面额非法: "+err.Error())
			continue
		}
		minPrice, err := YuanToCents(c.MinPrice)
		if err != nil {
			dc.skip("coupon_templates", c.ID, "使用门槛非法: "+err.Error())
			continue
		}
		createdAt := timeOrNow(c.CreateTime)
		useStart := createdAt
		useEnd := createdAt.AddDate(0, 0, maxInt(c.CouponTime, 1))
		claimStart, claimEnd := couponClaimWindow(c, batches[c.ID], createdAt, useEnd)

		totalQuantity := int64(0)
		for _, b := range batches[c.ID] {
			if b.TotalCount > 0 {
				totalQuantity += b.TotalCount
			}
		}
		if totalQuantity <= 0 {
			totalQuantity = int64(maxInt(issuedCount[c.ID], 1))
		}
		status := 0
		if c.Status == 1 {
			status = 1
		}

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityCouponTemplate, c.ID)
			continue
		}

		tx, err := dc.target.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO coupon_templates
				(name, threshold_amount, discount_amount, discount_type, discount_rate_bp,
				 total_quantity, per_user_limit, issued_quantity, claim_start_time, claim_end_time,
				 use_start_time, use_end_time, status, created_at)
			 VALUES (?, ?, ?, 1, NULL, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
			truncateRunes(c.Title, 64), minPrice, price, totalQuantity, issuedCount[c.ID],
			claimStart, claimEnd, useStart, useEnd, status, createdAt)
		if err != nil {
			tx.Rollback()
			dc.fail("coupon_templates", "模板 old_id=%d 写入失败: %v", c.ID, err)
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			dc.fail("coupon_templates", "读取模板新 ID old_id=%d 失败: %v", c.ID, err)
			continue
		}
		if err := dc.record(ctx, tx, entityCouponTemplate, c.ID, newID); err != nil {
			tx.Rollback()
			dc.fail("coupon_templates", "模板 old_id=%d 写映射失败: %v", c.ID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			dc.fail("coupon_templates", "模板 old_id=%d 提交失败: %v", c.ID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

func migrateCouponBatches(ctx context.Context, dc *domainContext, batches []*legacyCouponBatch, templates []*legacyCoupon) error {
	stat := dc.stat("coupon_batches")
	stat.SourceTotal = len(batches)
	templateByID := make(map[int64]*legacyCoupon, len(templates))
	for _, t := range templates {
		templateByID[t.ID] = t
	}

	for _, b := range batches {
		tpl, ok := templateByID[b.TemplateID]
		if !ok {
			dc.skip("coupon_batches", b.ID, fmt.Sprintf("引用不存在的模板 cid=%d", b.TemplateID))
			continue
		}
		templateNewID, ok := dc.mapped(entityCouponTemplate, b.TemplateID)
		if !ok {
			dc.skip("coupon_batches", b.ID, fmt.Sprintf("模板 cid=%d 未迁移", b.TemplateID))
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityCouponBatch, b.ID); ok {
			dc.skipAlready("coupon_batches")
			continue
		}

		createdAt := timeOrNow(b.CreateTime)
		claimStart := createdAt
		if b.StartTime.Valid {
			claimStart = b.StartTime.Time
		}
		claimEnd := claimStart.AddDate(0, 0, maxInt(tpl.CouponTime, 1))
		if b.EndTime.Valid {
			claimEnd = b.EndTime.Time
		}
		total := b.TotalCount
		if total < 0 {
			total = 0
		}
		issued := total - b.RemainCount
		if issued < 0 {
			issued = 0
		}
		if issued > total {
			issued = total
		}
		status := 0
		if b.Status == 1 && b.IsDel == 0 {
			status = 1
		}

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityCouponBatch, b.ID)
			continue
		}

		tx, err := dc.target.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO coupon_batches
				(template_id, total_quantity, issued_quantity, claim_start_time, claim_end_time, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			templateNewID, total, issued, claimStart, claimEnd, status, createdAt)
		if err != nil {
			tx.Rollback()
			dc.fail("coupon_batches", "批次 old_id=%d 写入失败: %v", b.ID, err)
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			dc.fail("coupon_batches", "读取批次新 ID old_id=%d 失败: %v", b.ID, err)
			continue
		}
		if err := dc.record(ctx, tx, entityCouponBatch, b.ID, newID); err != nil {
			tx.Rollback()
			dc.fail("coupon_batches", "批次 old_id=%d 写映射失败: %v", b.ID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			dc.fail("coupon_batches", "批次 old_id=%d 提交失败: %v", b.ID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

func migrateCouponScopes(ctx context.Context, dc *domainContext, templates []*legacyCoupon) error {
	stat := dc.stat("coupon_scopes")
	for _, t := range templates {
		if t.Type != 1 || strings.TrimSpace(t.ProductIDs) == "" {
			continue
		}
		templateNewID, ok := dc.mapped(entityCouponTemplate, t.ID)
		if !ok {
			continue
		}
		for _, raw := range strings.Split(t.ProductIDs, ",") {
			stat.SourceTotal++
			oldProductID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				dc.skip("coupon_scopes", t.ID, fmt.Sprintf("商品范围 %q 非法", raw))
				continue
			}
			productNewID, ok := dc.mapped(entityProduct, oldProductID)
			if !ok {
				dc.skip("coupon_scopes", t.ID, fmt.Sprintf("商品范围引用未迁移商品 product_id=%d", oldProductID))
				continue
			}
			stat.Eligible++
			if dc.dryRun {
				stat.Migrated++
				continue
			}
			_, err = dc.target.ExecContext(ctx,
				"INSERT INTO coupon_template_scopes (template_id, product_id) VALUES (?, ?)",
				templateNewID, productNewID)
			if err != nil {
				if isDuplicateKeyError(err) {
					dc.skipAlready("coupon_scopes")
					continue
				}
				dc.fail("coupon_scopes", "模板 old_id=%d 商品范围写入失败: %v", t.ID, err)
				continue
			}
			stat.Migrated++
		}
	}
	return nil
}

func migrateUserCoupons(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("user_coupons")
	rows, err := dc.source.QueryContext(ctx,
		"SELECT id, cid, uid, coupon_title, coupon_price, use_min_price, create_time, end_time, use_time, status, is_fail, is_del FROM store_coupon_user ORDER BY uid, cid, create_time, id")
	if err != nil {
		return fmt.Errorf("读取 store_coupon_user: %w", err)
	}
	defer rows.Close()

	var coupons []*legacyUserCoupon
	for rows.Next() {
		var c legacyUserCoupon
		var title, price, minPrice sql.NullString
		var endTime, useTimeT sql.NullTime
		if err := rows.Scan(&c.ID, &c.TemplateID, &c.UID, &title, &price, &minPrice,
			&c.CreateTime, &endTime, &useTimeT, &c.Status, &c.IsFail, &c.IsDel); err != nil {
			return fmt.Errorf("扫描 store_coupon_user: %w", err)
		}
		c.Title = title.String
		c.Price = price.String
		c.MinPrice = minPrice.String
		c.EndTime = endTime
		c.UseTime = useTimeT
		coupons = append(coupons, &c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 store_coupon_user: %w", err)
	}
	stat.SourceTotal = len(coupons)

	batchIndex, err := loadIssueUserIndex(ctx, dc.source)
	if err != nil {
		return err
	}

	claimNo := make(map[string]int)
	for _, c := range coupons {
		if c.IsDel == 1 {
			stat.Skipped++
			continue
		}
		key := fmt.Sprintf("%d|%d", c.UID, c.TemplateID)
		claimNo[key]++
		myClaimNo := claimNo[key]

		if _, ok := dc.mapped(entityUser, c.UID); !ok {
			dc.skip("user_coupons", c.ID, fmt.Sprintf("引用未迁移用户 uid=%d", c.UID))
			continue
		}
		if _, ok := dc.mapped(entityCouponTemplate, c.TemplateID); !ok {
			dc.skip("user_coupons", c.ID, fmt.Sprintf("引用未迁移模板 cid=%d", c.TemplateID))
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityUserCoupon, c.ID); ok {
			dc.skipAlready("user_coupons")
			continue
		}

		userNewID, _ := dc.mapped(entityUser, c.UID)
		templateNewID, _ := dc.mapped(entityCouponTemplate, c.TemplateID)
		batchOldID := batchIndex.find(c.UID, c.TemplateID, c.CreateTime)
		var batchNewID any
		if batchOldID > 0 {
			if id, ok := dc.mapped(entityCouponBatch, batchOldID); ok {
				batchNewID = id
			}
		}

		price, err := YuanToCents(c.Price)
		if err != nil {
			dc.skip("user_coupons", c.ID, "券面额快照非法: "+err.Error())
			continue
		}
		minPrice, err := YuanToCents(c.MinPrice)
		if err != nil {
			dc.skip("user_coupons", c.ID, "使用门槛快照非法: "+err.Error())
			continue
		}
		status := mapUserCouponStatus(c.Status, c.IsFail)
		receivedAt := timeOrNow(c.CreateTime)
		endTime := timeOrNow(c.EndTime)
		var usedAt any
		if c.UseTime.Valid {
			usedAt = c.UseTime.Time
		}

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityUserCoupon, c.ID)
			continue
		}

		tx, err := dc.target.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO user_coupons
				(user_id, template_id, batch_id, claim_no, status,
				 coupon_title, coupon_price, use_min_price, end_time,
				 received_at, locked_at, used_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			userNewID, templateNewID, batchNewID, myClaimNo, status,
			truncateRunes(c.Title, 64), price, minPrice, endTime,
			receivedAt, usedAt, receivedAt)
		if err != nil {
			tx.Rollback()
			dc.fail("user_coupons", "用户券 old_id=%d 写入失败: %v", c.ID, err)
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			dc.fail("user_coupons", "读取用户券新 ID old_id=%d 失败: %v", c.ID, err)
			continue
		}
		if err := dc.record(ctx, tx, entityUserCoupon, c.ID, newID); err != nil {
			tx.Rollback()
			dc.fail("user_coupons", "用户券 old_id=%d 写映射失败: %v", c.ID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			dc.fail("user_coupons", "用户券 old_id=%d 提交失败: %v", c.ID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

// issueUserIndex 支持按 (uid, 模板, 领取时间) 定位发行批次（用户券表本身无 batch_id）。
type issueUserIndex struct {
	entries map[string]int64
	all     map[string][]issueUserEntry
}

type issueUserEntry struct {
	batchID    int64
	createTime time.Time
}

func loadIssueUserIndex(ctx context.Context, source *sql.DB) (*issueUserIndex, error) {
	rows, err := source.QueryContext(ctx,
		`SELECT iu.uid, i.cid, iu.issue_coupon_id, iu.create_time
		 FROM store_coupon_issue_user iu
		 JOIN store_coupon_issue i ON i.id = iu.issue_coupon_id
		 WHERE iu.is_del = 0`)
	if err != nil {
		return nil, fmt.Errorf("读取 store_coupon_issue_user: %w", err)
	}
	defer rows.Close()

	idx := &issueUserIndex{entries: make(map[string]int64), all: make(map[string][]issueUserEntry)}
	for rows.Next() {
		var uid, cid, batchID int64
		var created sql.NullTime
		if err := rows.Scan(&uid, &cid, &batchID, &created); err != nil {
			return nil, fmt.Errorf("扫描 store_coupon_issue_user: %w", err)
		}
		if !created.Valid {
			continue
		}
		key := issueKey(uid, cid)
		idx.entries[fmt.Sprintf("%s|%s", key, created.Time.Format("2006-01-02 15:04:05"))] = batchID
		idx.all[key] = append(idx.all[key], issueUserEntry{batchID: batchID, createTime: created.Time})
	}
	return idx, rows.Err()
}

func (idx *issueUserIndex) find(uid, templateID int64, created sql.NullTime) int64 {
	if !created.Valid {
		return 0
	}
	key := issueKey(uid, templateID)
	exact := fmt.Sprintf("%s|%s", key, created.Time.Format("2006-01-02 15:04:05"))
	if id, ok := idx.entries[exact]; ok {
		return id
	}
	// 回退：同用户同模板下领取时间不晚于用户券创建时间的最近批次。
	var best int64
	var bestTime time.Time
	for _, e := range idx.all[key] {
		if e.createTime.After(created.Time) {
			continue
		}
		if best == 0 || e.createTime.After(bestTime) {
			best = e.batchID
			bestTime = e.createTime
		}
	}
	return best
}

func issueKey(uid, templateID int64) string {
	return fmt.Sprintf("%d|%d", uid, templateID)
}

// mapUserCouponStatus 映射用户券状态：0 未使用→10 可用、1 已使用→30 已使用、2 已过期→10（由 end_time 判定）。
// is_fail=1 视为不可用，映射为 30（不新增目标状态），并保留原因为数据异常。
func mapUserCouponStatus(status, isFail int) int {
	if isFail == 1 {
		return 30
	}
	switch status {
	case 0:
		return 10
	case 1:
		return 30
	case 2:
		return 10
	default:
		return 10
	}
}

// couponClaimWindow 计算模板领取窗口：优先取批次最早开始/最晚结束，否则回退模板创建时间与券有效期。
func couponClaimWindow(t *legacyCoupon, batches []*legacyCouponBatch, createdAt, useEnd time.Time) (time.Time, time.Time) {
	start, end := createdAt, useEnd
	first := true
	for _, b := range batches {
		if b.StartTime.Valid && (first || b.StartTime.Time.Before(start)) {
			start = b.StartTime.Time
			first = false
		}
		if b.EndTime.Valid && b.EndTime.Time.After(end) {
			end = b.EndTime.Time
		}
	}
	if !end.After(start) {
		end = start.AddDate(0, 0, maxInt(t.CouponTime, 1))
	}
	return start, end
}

// loadLegacyCoupons 读取 store_coupon 全量行。
func loadLegacyCoupons(ctx context.Context, source *sql.DB) ([]*legacyCoupon, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, title, coupon_price, use_min_price, coupon_time, status, product_id, type, is_del, create_time FROM store_coupon ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_coupon: %w", err)
	}
	defer rows.Close()

	var out []*legacyCoupon
	for rows.Next() {
		var c legacyCoupon
		var price, minPrice, productIDs sql.NullString
		var couponType sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Title, &price, &minPrice, &c.CouponTime, &c.Status,
			&productIDs, &couponType, &c.IsDel, &c.CreateTime); err != nil {
			return nil, fmt.Errorf("扫描 store_coupon: %w", err)
		}
		c.Price = price.String
		c.MinPrice = minPrice.String
		c.ProductIDs = productIDs.String
		c.Type = int(couponType.Int64)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// loadLegacyCouponBatches 读取 store_coupon_issue 全量行（含 is_del=1，历史用户券仍引用其批次）。
func loadLegacyCouponBatches(ctx context.Context, source *sql.DB) ([]*legacyCouponBatch, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, cid, start_time, end_time, total_count, remain_count, is_permanent, status, is_del, create_time FROM store_coupon_issue ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_coupon_issue: %w", err)
	}
	defer rows.Close()

	var out []*legacyCouponBatch
	for rows.Next() {
		var b legacyCouponBatch
		var templateID, total, remain sql.NullInt64
		if err := rows.Scan(&b.ID, &templateID, &b.StartTime, &b.EndTime, &total, &remain,
			&b.IsPermanent, &b.Status, &b.IsDel, &b.CreateTime); err != nil {
			return nil, fmt.Errorf("扫描 store_coupon_issue: %w", err)
		}
		b.TemplateID = templateID.Int64
		b.TotalCount = total.Int64
		b.RemainCount = remain.Int64
		out = append(out, &b)
	}
	return out, rows.Err()
}

// reviewDomain 迁移商品评价。
type reviewDomain struct{}

func (reviewDomain) name() string { return "reviews" }

func (reviewDomain) run(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("reviews")
	rows, err := dc.source.QueryContext(ctx,
		"SELECT id, uid, oid, product_id, product_score, comment, pics, is_del, create_time FROM store_product_reply ORDER BY id")
	if err != nil {
		return fmt.Errorf("读取 store_product_reply: %w", err)
	}
	defer rows.Close()

	type legacyReview struct {
		ID        int64
		UID       int64
		OID       int64
		ProductID int64
		Score     int
		Comment   string
		Pics      string
		IsDel     int
		CreateAt  sql.NullTime
	}
	var reviews []*legacyReview
	for rows.Next() {
		var r legacyReview
		var comment, pics sql.NullString
		if err := rows.Scan(&r.ID, &r.UID, &r.OID, &r.ProductID, &r.Score, &comment, &pics, &r.IsDel, &r.CreateAt); err != nil {
			return fmt.Errorf("扫描 store_product_reply: %w", err)
		}
		r.Comment = comment.String
		r.Pics = pics.String
		reviews = append(reviews, &r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 store_product_reply: %w", err)
	}
	stat.SourceTotal = len(reviews)

	for _, r := range reviews {
		if r.IsDel == 1 {
			stat.Skipped++
			continue
		}
		userNewID, ok := dc.mapped(entityUser, r.UID)
		if !ok {
			dc.skip("reviews", r.ID, fmt.Sprintf("引用未迁移用户 uid=%d", r.UID))
			continue
		}
		productNewID, ok := dc.mapped(entityProduct, r.ProductID)
		if !ok {
			dc.skip("reviews", r.ID, fmt.Sprintf("引用未迁移商品 product_id=%d", r.ProductID))
			continue
		}
		orderNewID, ok := dc.mapped(entityOrder, r.OID)
		if !ok {
			dc.skip("reviews", r.ID, fmt.Sprintf("引用未迁移订单 oid=%d", r.OID))
			continue
		}
		itemOldID, err := findOrderItemOldID(ctx, dc.source, r.OID, r.ProductID)
		if err != nil {
			return err
		}
		if itemOldID == 0 {
			dc.skip("reviews", r.ID, "无法定位对应订单明细")
			continue
		}
		itemNewID, ok := dc.mapped(entityOrderItem, itemOldID)
		if !ok {
			dc.skip("reviews", r.ID, fmt.Sprintf("订单明细 old_id=%d 未迁移", itemOldID))
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityReview, r.ID); ok {
			dc.skipAlready("reviews")
			continue
		}

		var skuID int64
		if dc.dryRun {
			// Dry Run 目标库没有订单明细行；引用完整性已由 itemNewID 映射校验。
			skuID = plannedID
		} else if err := dc.target.QueryRowContext(ctx, "SELECT sku_id FROM order_items WHERE id = ?", itemNewID).Scan(&skuID); err != nil {
			dc.skip("reviews", r.ID, "读取订单明细 SKU 失败: "+err.Error())
			continue
		}
		imageURLs, err := encodeImageURLs(r.Pics)
		if err != nil {
			dc.skip("reviews", r.ID, "评价图片 JSON 编码失败: "+err.Error())
			continue
		}
		createdAt := timeOrNow(r.CreateAt)
		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityReview, r.ID)
			continue
		}

		tx, err := dc.target.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO reviews (user_id, order_item_id, product_id, sku_id, rating, content, image_urls, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			userNewID, itemNewID, productNewID, skuID, clampRating(r.Score), truncateRunes(r.Comment, 500), imageURLs, createdAt)
		if err != nil {
			tx.Rollback()
			if isDuplicateKeyError(err) {
				dc.skip("reviews", r.ID, "该订单明细已存在评价（uk_order_item 冲突）")
				continue
			}
			dc.fail("reviews", "评价 old_id=%d 写入失败: %v", r.ID, err)
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			dc.fail("reviews", "读取评价新 ID old_id=%d 失败: %v", r.ID, err)
			continue
		}
		if err := dc.record(ctx, tx, entityReview, r.ID, newID); err != nil {
			tx.Rollback()
			dc.fail("reviews", "评价 old_id=%d 写映射失败: %v", r.ID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			dc.fail("reviews", "评价 old_id=%d 提交失败: %v", r.ID, err)
			continue
		}
		_ = orderNewID
		stat.Migrated++
	}
	return nil
}

// findOrderItemOldID 通过 (oid, product_id) 定位订单明细旧 ID；源数据中同一订单同一商品至多一行。
func findOrderItemOldID(ctx context.Context, source *sql.DB, oid, productID int64) (int64, error) {
	var id int64
	err := source.QueryRowContext(ctx,
		"SELECT id FROM store_order_cart_info WHERE oid = ? AND product_id = ? ORDER BY id LIMIT 1",
		oid, productID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("查询订单明细 oid=%d product_id=%d: %w", oid, productID, err)
	}
	return id, nil
}

func encodeImageURLs(raw string) (any, error) {
	parts := SplitSliderImages(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(parts)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func clampRating(v int) int {
	if v < 1 {
		return 1
	}
	if v > 5 {
		return 5
	}
	return v
}

// favoriteDomain 迁移用户收藏（仅 type=collect 且 is_del=0）。
type favoriteDomain struct{}

func (favoriteDomain) name() string { return "favorites" }

func (favoriteDomain) run(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("favorites")
	var sourceTotal int
	if err := dc.source.QueryRowContext(ctx, "SELECT COUNT(*) FROM store_product_relation").Scan(&sourceTotal); err != nil {
		return fmt.Errorf("统计 store_product_relation: %w", err)
	}
	stat.SourceTotal = sourceTotal

	rows, err := dc.source.QueryContext(ctx,
		"SELECT id, uid, product_id, create_time FROM store_product_relation WHERE type = 'collect' AND is_del = 0 ORDER BY id")
	if err != nil {
		return fmt.Errorf("读取 store_product_relation: %w", err)
	}
	defer rows.Close()

	type legacyFavorite struct {
		ID        int64
		UID       int64
		ProductID int64
		CreateAt  sql.NullTime
	}
	var favorites []*legacyFavorite
	for rows.Next() {
		var f legacyFavorite
		if err := rows.Scan(&f.ID, &f.UID, &f.ProductID, &f.CreateAt); err != nil {
			return fmt.Errorf("扫描 store_product_relation: %w", err)
		}
		favorites = append(favorites, &f)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 store_product_relation: %w", err)
	}
	// foot（足迹）与 is_del=1（取消收藏）不进入 favorites，计入跳过口径。
	stat.Skipped += sourceTotal - len(favorites)

	seen := make(map[string]bool)
	for _, f := range favorites {
		userNewID, ok := dc.mapped(entityUser, f.UID)
		if !ok {
			dc.skip("favorites", f.ID, fmt.Sprintf("引用未迁移用户 uid=%d", f.UID))
			continue
		}
		productNewID, ok := dc.mapped(entityProduct, f.ProductID)
		if !ok {
			dc.skip("favorites", f.ID, fmt.Sprintf("引用未迁移商品 product_id=%d", f.ProductID))
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityFavorite, f.ID); ok {
			dc.skipAlready("favorites")
			continue
		}
		key := fmt.Sprintf("%d|%d", userNewID, productNewID)
		if dc.dryRun && userNewID == plannedID {
			key = fmt.Sprintf("%d|%d", f.UID, f.ProductID)
		}
		if seen[key] {
			dc.skip("favorites", f.ID, "同一用户同一商品重复收藏")
			continue
		}
		seen[key] = true

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityFavorite, f.ID)
			continue
		}
		tx, err := dc.target.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"INSERT INTO favorites (user_id, product_id, created_at) VALUES (?, ?, COALESCE(?, CURRENT_TIMESTAMP))",
			userNewID, productNewID, nullableTime(f.CreateAt))
		if err != nil {
			tx.Rollback()
			if isDuplicateKeyError(err) {
				dc.skip("favorites", f.ID, "同一用户同一商品已存在收藏")
				continue
			}
			dc.fail("favorites", "收藏 old_id=%d 写入失败: %v", f.ID, err)
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			dc.fail("favorites", "读取收藏新 ID old_id=%d 失败: %v", f.ID, err)
			continue
		}
		if err := dc.record(ctx, tx, entityFavorite, f.ID, newID); err != nil {
			tx.Rollback()
			dc.fail("favorites", "收藏 old_id=%d 写映射失败: %v", f.ID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			dc.fail("favorites", "收藏 old_id=%d 提交失败: %v", f.ID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

func timeOrNow(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Now()
}

func nullableTime(t sql.NullTime) any {
	if t.Valid {
		return t.Time
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
