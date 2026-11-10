package legacymigrate

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// 历史订单状态事件 → 目标状态（10..70）。
// remove_order 是用户「删除订单列表记录」，不是取消；迁移为不改变状态的审计流水。
var legacyOrderEventStatus = map[string]int{
	"yshop_create_order": statusPendingPayment,
	"pay_success":        statusPaid,
	"delivery_goods":     statusShipped,
	"user_take_delivery": statusReceived,
	"order_eval":         statusCompleted,
}

// 目标订单状态常量（与 docs/design/order.md 一致）。
const (
	statusPendingPayment = 10
	statusPaid           = 20
	statusShipped        = 30
	statusReceived       = 40
	statusCompleted      = 50
	statusCancelled      = 60
	statusRefunded       = 70
)

// 订单状态流水 change_type（语义分类，不参与状态机判定）。
const (
	changeCreate = "create"
	changePay    = "pay"
	changeShip   = "ship"
	changeRcv    = "receive"
	changeDone   = "complete"
	changeDelete = "delete"
)

// orderDomain 迁移订单、订单明细与订单状态流水。
type orderDomain struct{}

func (orderDomain) name() string { return "orders" }

type legacyOrder struct {
	ID             int64
	OrderNo        string
	UID            int64
	RealName       string
	UserPhone      string
	UserAddress    string
	FreightPrice   string
	TotalPrice     string
	TotalPostage   string
	PayPrice       string
	PayPostage     string
	DeductionPrice string
	CouponID       int64
	CouponPrice    string
	Paid           int
	PayTime        sql.NullTime
	PayType        string
	CreateTime     sql.NullTime
	Status         int
	RefundStatus   int
	DeliveryName   string
	DeliverySN     string
	DeliveryType   string
	IsDel          int
}

type legacyOrderItem struct {
	ID         int64
	OID        int64
	ProductID  int64
	CartInfo   string
	CartUnique string
}

type legacyOrderStatusLog struct {
	ID            int64
	OID           int64
	ChangeType    string
	ChangeMessage string
	ChangeTime    time.Time
}

type legacyCartInfo struct {
	CartNum   int         `json:"cartNum"`
	TruePrice json.Number `json:"truePrice"`
	Product   struct {
		StoreName string `json:"storeName"`
		Image     string `json:"image"`
		AttrInfo  struct {
			Unique string      `json:"unique"`
			SKU    string      `json:"sku"`
			Price  json.Number `json:"price"`
		} `json:"attrInfo"`
	} `json:"productInfo"`
}

func (orderDomain) run(ctx context.Context, dc *domainContext) error {
	orders, err := loadLegacyOrders(ctx, dc.source)
	if err != nil {
		return err
	}
	items, err := loadLegacyOrderItems(ctx, dc.source)
	if err != nil {
		return err
	}
	logs, err := loadLegacyOrderLogs(ctx, dc.source)
	if err != nil {
		return err
	}
	skuIndex, err := loadLegacySKUIndex(ctx, dc.source)
	if err != nil {
		return err
	}

	itemsByOrder := make(map[int64][]*legacyOrderItem)
	for _, it := range items {
		itemsByOrder[it.OID] = append(itemsByOrder[it.OID], it)
	}
	logsByOrder := make(map[int64][]*legacyOrderStatusLog)
	for _, l := range logs {
		logsByOrder[l.OID] = append(logsByOrder[l.OID], l)
	}

	orderStat := dc.stat("orders")
	itemStat := dc.stat("order_items")
	logStat := dc.stat("order_status_logs")
	orderStat.SourceTotal = len(orders)
	itemStat.SourceTotal = len(items)
	logStat.SourceTotal = len(logs)
	itemStat.Eligible = len(items)
	logStat.Eligible = len(logs)

	payTimeout := 900
	if v, err := config.Int(ctx, config.KeyOrderPayTimeout); err == nil && v > 0 {
		payTimeout = v
	}

	for _, o := range orders {
		orderStat.Eligible++
		if _, ok := dc.mapped(entityOrder, o.ID); ok {
			dc.skipAlready("orders")
			itemStat.Skipped += len(itemsByOrder[o.ID])
			logStat.Skipped += len(logsByOrder[o.ID])
			continue
		}
		userNewID, ok := dc.mapped(entityUser, o.UID)
		if !ok {
			dc.skip("orders", o.ID, fmt.Sprintf("引用未迁移用户 uid=%d", o.UID))
			itemStat.Skipped += len(itemsByOrder[o.ID])
			logStat.Skipped += len(logsByOrder[o.ID])
			continue
		}

		timeline := buildOrderTimeline(o, logsByOrder[o.ID], dc)
		amounts, err := mapOrderAmounts(o)
		if err != nil {
			dc.skip("orders", o.ID, err.Error())
			itemStat.Skipped += len(itemsByOrder[o.ID])
			logStat.Skipped += len(logsByOrder[o.ID])
			continue
		}
		recipient := splitLegacyAddress(o.UserAddress)

		itemTargets, itemErrs := mapOrderItems(itemsByOrder[o.ID], dc, skuIndex)
		if len(itemErrs) > 0 {
			// 明细不完整会破坏订单历史；宁可整单跳过并报告，也不写入残缺订单。
			for _, msg := range itemErrs {
				dc.warn("order_items", "订单 old_id=%d: %s", o.ID, msg)
			}
			dc.skip("orders", o.ID, "订单明细无法完整映射，整单跳过")
			itemStat.Skipped += len(itemsByOrder[o.ID])
			logStat.Skipped += len(logsByOrder[o.ID])
			continue
		}

		if dc.dryRun {
			orderStat.Migrated++
			itemStat.Migrated += len(itemTargets)
			logStat.Migrated += len(timeline.logs)
			dc.plan(entityOrder, o.ID)
			for _, it := range itemTargets {
				dc.plan(entityOrderItem, it.OldID)
			}
			continue
		}

		if err := writeOrder(ctx, dc, o, userNewID, amounts, recipient, timeline, itemTargets, payTimeout); err != nil {
			if isDuplicateKeyError(err) {
				dc.skip("orders", o.ID, "订单号或幂等键与目标库冲突")
				continue
			}
			dc.fail("orders", "订单 old_id=%d 迁移失败（已回滚）: %v", o.ID, err)
			itemStat.Failed += len(itemsByOrder[o.ID])
			continue
		}
		orderStat.Migrated++
		itemStat.Migrated += len(itemTargets)
		logStat.Migrated += len(timeline.logs)
	}
	return nil
}

// orderAmounts 是订单金额（全部整数分）。
type orderAmounts struct {
	Total    int64
	Original int64
	Discount int64
	Shipping int64
}

func mapOrderAmounts(o *legacyOrder) (orderAmounts, error) {
	total, err := YuanToCents(o.PayPrice)
	if err != nil {
		return orderAmounts{}, fmt.Errorf("实付金额非法: %w", err)
	}
	original, err := YuanToCents(o.TotalPrice)
	if err != nil {
		return orderAmounts{}, fmt.Errorf("商品总额非法: %w", err)
	}
	coupon, err := YuanToCents(o.CouponPrice)
	if err != nil {
		return orderAmounts{}, fmt.Errorf("优惠金额非法: %w", err)
	}
	deduction, err := YuanToCents(o.DeductionPrice)
	if err != nil {
		return orderAmounts{}, fmt.Errorf("抵扣金额非法: %w", err)
	}
	shipping, err := YuanToCents(o.TotalPostage)
	if err != nil {
		return orderAmounts{}, fmt.Errorf("运费金额非法: %w", err)
	}
	return orderAmounts{
		Total:    total,
		Original: original,
		Discount: coupon + deduction,
		Shipping: shipping,
	}, nil
}

// orderTimeline 是按流水重建后的最终状态、时间点与待写入状态流水。
type orderTimeline struct {
	Status      int
	PaidAt      any
	ShippedAt   any
	ReceivedAt  any
	CompletedAt any
	logs        []logTarget
}

type logTarget struct {
	From       int
	To         int
	ChangeType string
	Remark     string
	CreatedAt  time.Time
}

// buildOrderTimeline 以 store_order_status.change_type 流水重建最终状态。
//
// 规则（依据实际数据，不直接信任 store_order.status）：
//   - create 固定为起点 10；其余事件按 change_time,id 升序；
//   - 状态只允许单调前进（10→20→30→40→50），重复/回退事件只记流水不降级；
//   - remove_order 是「用户删除订单记录」，不改变状态，写 change_type=delete；
//   - 未知事件保留原 change_type，from=to=当前状态并记警告。
func buildOrderTimeline(o *legacyOrder, logs []*legacyOrderStatusLog, dc *domainContext) orderTimeline {
	tl := orderTimeline{Status: statusPendingPayment}
	current := 0

	// create 事件优先（源数据存在 pay 流水时间早于 create 流水时间的情况）。
	var createLogs, otherLogs []*legacyOrderStatusLog
	for _, l := range logs {
		if l.ChangeType == "yshop_create_order" {
			createLogs = append(createLogs, l)
		} else {
			otherLogs = append(otherLogs, l)
		}
	}
	sort.Slice(createLogs, func(i, j int) bool {
		if createLogs[i].ChangeTime.Equal(createLogs[j].ChangeTime) {
			return createLogs[i].ID < createLogs[j].ID
		}
		return createLogs[i].ChangeTime.Before(createLogs[j].ChangeTime)
	})
	sort.Slice(otherLogs, func(i, j int) bool {
		if otherLogs[i].ChangeTime.Equal(otherLogs[j].ChangeTime) {
			return otherLogs[i].ID < otherLogs[j].ID
		}
		return otherLogs[i].ChangeTime.Before(otherLogs[j].ChangeTime)
	})

	if len(createLogs) == 0 {
		dc.warn("order_status_logs", "订单 old_id=%d 缺少下单流水，status 以 10 为起点", o.ID)
	} else {
		first := createLogs[0]
		tl.logs = append(tl.logs, logTarget{From: 0, To: statusPendingPayment, ChangeType: changeCreate, Remark: first.ChangeMessage, CreatedAt: first.ChangeTime})
		current = statusPendingPayment
		// 重复 create 流水只保留审计，不推进状态。
		for _, extra := range createLogs[1:] {
			tl.logs = append(tl.logs, logTarget{From: current, To: current, ChangeType: changeCreate, Remark: extra.ChangeMessage, CreatedAt: extra.ChangeTime})
		}
	}

	if current == 0 {
		current = statusPendingPayment
		tl.Status = statusPendingPayment
	}
	if o.CreateTime.Valid && len(createLogs) > 0 && o.CreateTime.Time.Sub(createLogs[0].ChangeTime) > time.Hour {
		dc.warn("order_status_logs", "订单 old_id=%d 的下单流水时间明显早于订单创建时间", o.ID)
	}

	for _, l := range otherLogs {
		target, known := legacyOrderEventStatus[l.ChangeType]
		from := current
		to := current
		changeType := l.ChangeType
		switch l.ChangeType {
		case "remove_order":
			// 用户删除订单列表记录：不改变订单状态，仅保留审计流水。
			known = true
			changeType = changeDelete
		case "cancel_order":
			target, known = statusCancelled, true
			changeType = "cancel"
		case "refund_success":
			target, known = statusRefunded, true
			changeType = "refund"
		}
		if known && target > current {
			to = target
			current = target
		} else if !known {
			dc.warn("order_status_logs", "订单 old_id=%d 存在未知流水 %q，按无状态变更归档", o.ID, l.ChangeType)
		}
		if len([]rune(changeType)) > 32 {
			changeType = truncateRunes(changeType, 32)
		}
		tl.logs = append(tl.logs, logTarget{From: from, To: to, ChangeType: changeType, Remark: l.ChangeMessage, CreatedAt: l.ChangeTime})

		switch l.ChangeType {
		case "pay_success":
			if tl.PaidAt == nil {
				tl.PaidAt = l.ChangeTime
			}
		case "delivery_goods":
			if tl.ShippedAt == nil {
				tl.ShippedAt = l.ChangeTime
			}
		case "user_take_delivery":
			if tl.ReceivedAt == nil {
				tl.ReceivedAt = l.ChangeTime
			}
		case "order_eval":
			if tl.CompletedAt == nil {
				tl.CompletedAt = l.ChangeTime
			}
		}
	}
	tl.Status = current

	if o.Paid == 1 {
		if o.PayTime.Valid {
			tl.PaidAt = o.PayTime.Time
		}
	}
	if o.Paid == 1 && tl.PaidAt == nil {
		dc.warn("order_status_logs", "订单 old_id=%d paid=1 但缺少支付时间/支付流水", o.ID)
	}
	return tl
}

// itemTarget 是待写入 order_items 的行快照。
type itemTarget struct {
	OldID        int64
	SKUID        int64
	ProductID    int64
	SKUName      string
	SpecText     string
	ProductName  string
	ProductImage string
	Price        int64
	Quantity     int64
}

// legacySKUIndex 用于把订单快照中的 SKU 唯一码（或商品+规格名）定位回历史 SKU。
type legacySKUIndex struct {
	byUnique map[string]int64
	byName   map[string]int64
}

func loadLegacySKUIndex(ctx context.Context, source *sql.DB) (*legacySKUIndex, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, product_id, sku, `unique` FROM store_product_attr_value")
	if err != nil {
		return nil, fmt.Errorf("读取 SKU 索引: %w", err)
	}
	defer rows.Close()

	idx := &legacySKUIndex{byUnique: make(map[string]int64), byName: make(map[string]int64)}
	for rows.Next() {
		var id, productID int64
		var name, unique string
		if err := rows.Scan(&id, &productID, &name, &unique); err != nil {
			return nil, fmt.Errorf("扫描 SKU 索引: %w", err)
		}
		if unique != "" {
			idx.byUnique[unique] = id
		}
		idx.byName[fmt.Sprintf("%d|%s", productID, strings.TrimSpace(name))] = id
	}
	return idx, rows.Err()
}

func mapOrderItems(items []*legacyOrderItem, dc *domainContext, idx *legacySKUIndex) ([]itemTarget, []string) {
	var targets []itemTarget
	var warnings []string
	for _, it := range items {
		var cart legacyCartInfo
		dec := json.NewDecoder(strings.NewReader(it.CartInfo))
		dec.UseNumber()
		if err := dec.Decode(&cart); err != nil {
			warnings = append(warnings, fmt.Sprintf("明细 old_id=%d JSON 解析失败: %v", it.ID, err))
			continue
		}
		productNewID, ok := dc.mapped(entityProduct, it.ProductID)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("明细 old_id=%d 商品 product_id=%d 未迁移", it.ID, it.ProductID))
			continue
		}
		unique := strings.TrimSpace(cart.Product.AttrInfo.Unique)
		skuName := strings.TrimSpace(cart.Product.AttrInfo.SKU)
		oldSKUID := int64(0)
		if unique != "" {
			oldSKUID = idx.byUnique[unique]
		}
		if oldSKUID == 0 && skuName != "" {
			oldSKUID = idx.byName[fmt.Sprintf("%d|%s", it.ProductID, skuName)]
		}
		skuNewID, ok := dc.mapped(entitySKU, oldSKUID)
		if !ok || oldSKUID == 0 {
			warnings = append(warnings, fmt.Sprintf("明细 old_id=%d 无法定位 SKU（unique=%q sku=%q）", it.ID, unique, skuName))
			continue
		}
		if skuName == "" {
			skuName = defaultSKUName
		}
		price, err := YuanToCents(cart.TruePrice.String())
		if err != nil || price == 0 {
			price, err = YuanToCents(cart.Product.AttrInfo.Price.String())
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("明细 old_id=%d 金额非法: %v", it.ID, err))
				continue
			}
		}
		qty := int64(cart.CartNum)
		if qty <= 0 {
			warnings = append(warnings, fmt.Sprintf("明细 old_id=%d 数量非法: %d", it.ID, cart.CartNum))
			continue
		}
		productName := strings.TrimSpace(cart.Product.StoreName)
		if productName == "" {
			productName = fmt.Sprintf("legacy_product_%d", it.ProductID)
		}
		targets = append(targets, itemTarget{
			OldID:        it.ID,
			SKUID:        skuNewID,
			ProductID:    productNewID,
			SKUName:      truncateRunes(skuName, 128),
			SpecText:     truncateRunes(skuName, 128),
			ProductName:  truncateRunes(productName, 128),
			ProductImage: truncateRunes(strings.TrimSpace(cart.Product.Image), 512),
			Price:        price,
			Quantity:     qty,
		})
	}
	return targets, warnings
}

// legacyRecipient 是订单收货地址快照。
type legacyRecipient struct {
	Name     string
	Phone    string
	Province string
	City     string
	District string
	Detail   string
}

// splitLegacyAddress 把 "省 市 区 详细地址" 拆为四段；不足四段时整体作为 detail，避免凭空编造。
func splitLegacyAddress(raw string) legacyRecipient {
	fields := strings.Fields(strings.TrimSpace(raw))
	r := legacyRecipient{}
	switch {
	case len(fields) >= 4:
		r.Province = fields[0]
		r.City = fields[1]
		r.District = fields[2]
		r.Detail = strings.Join(fields[3:], " ")
	case len(fields) == 3:
		r.Province = fields[0]
		r.City = fields[1]
		r.Detail = fields[2]
	case len(fields) == 2:
		r.Province = fields[0]
		r.Detail = fields[1]
	case len(fields) == 1:
		r.Detail = fields[0]
	}
	return r
}

func writeOrder(ctx context.Context, dc *domainContext, o *legacyOrder, userNewID int64, amounts orderAmounts, recipient legacyRecipient, tl orderTimeline, items []itemTarget, payTimeoutSeconds int) error {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	createdAt := timeOrNow(o.CreateTime)
	expireAt := createdAt.Add(time.Duration(payTimeoutSeconds) * time.Second)

	var userCouponID any
	if o.CouponID > 0 {
		if id, ok := dc.mapped(entityUserCoupon, o.CouponID); ok {
			userCouponID = id
		}
	}
	var paidAt, shippedAt, receivedAt, completedAt any
	paidAt = tl.PaidAt
	shippedAt = tl.ShippedAt
	receivedAt = tl.ReceivedAt
	completedAt = tl.CompletedAt

	res, err := tx.ExecContext(ctx,
		`INSERT INTO orders
			(order_no, user_id, status, total_amount, original_amount, discount_amount, shipping_fee,
			 user_coupon_id, pay_type, transaction_no, idempotency_key, request_hash,
			 recipient_name, phone, province, city, district, detail, address_id, expire_at,
			 paid_at, shipped_at, received_at, completed_at, delivery_type, delivery_name, delivery_sn, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		truncateRunes(o.OrderNo, 32), userNewID, tl.Status, amounts.Total, amounts.Original, amounts.Discount, amounts.Shipping,
		userCouponID, mapLegacyPayType(o.PayType, o.Paid, tl.Status), fmt.Sprintf("legacy_order:%d", o.ID), legacyOrderHash(o.OrderNo),
		truncateRunes(o.RealName, 32), truncateRunes(o.UserPhone, 20),
		truncateRunes(recipient.Province, 32), truncateRunes(recipient.City, 32), truncateRunes(recipient.District, 32), truncateRunes(recipient.Detail, 255),
		expireAt, paidAt, shippedAt, receivedAt, completedAt,
		truncateRunes(o.DeliveryType, 32), truncateRunes(o.DeliveryName, 64), truncateRunes(o.DeliverySN, 64), createdAt)
	if err != nil {
		return fmt.Errorf("插入 orders: %w", err)
	}
	orderNewID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取订单新 ID: %w", err)
	}
	if err := dc.record(ctx, tx, entityOrder, o.ID, orderNewID); err != nil {
		return err
	}

	for _, it := range items {
		itemRes, err := tx.ExecContext(ctx,
			`INSERT INTO order_items
				(order_id, sku_id, product_id, sku_name, spec_text, product_name, product_main_image, price, quantity, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			orderNewID, it.SKUID, it.ProductID, it.SKUName, it.SpecText, it.ProductName, it.ProductImage, it.Price, it.Quantity, createdAt)
		if err != nil {
			return fmt.Errorf("插入 order_items old_id=%d: %w", it.OldID, err)
		}
		itemNewID, err := itemRes.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取订单明细新 ID old_id=%d: %w", it.OldID, err)
		}
		if err := dc.record(ctx, tx, entityOrderItem, it.OldID, itemNewID); err != nil {
			return err
		}
	}

	for _, l := range tl.logs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO order_status_logs
				(order_id, from_status, to_status, change_type, operator_type, operator_id, remark, created_at)
			 VALUES (?, ?, ?, ?, 1, ?, ?, ?)`,
			orderNewID, l.From, l.To, l.ChangeType, userNewID, truncateRunes(l.Remark, 255), l.CreatedAt); err != nil {
			return fmt.Errorf("插入 order_status_logs: %w", err)
		}
	}

	// 回填用户券与订单的双向关联（历史 store_order.coupon_id 指向 store_coupon_user.id）。
	if userCouponID != nil {
		if _, err := tx.ExecContext(ctx,
			"UPDATE user_coupons SET order_id = ? WHERE id = ?", orderNewID, userCouponID); err != nil {
			return fmt.Errorf("回填 user_coupons.order_id: %w", err)
		}
	}
	return tx.Commit()
}

// mapLegacyPayType 映射历史支付方式：yue（余额）→ balance；
// 最终仍为待支付(10)的订单保留空串（目标语义：空串=未支付/未选择）。
func mapLegacyPayType(raw string, paid, finalStatus int) string {
	if finalStatus == statusPendingPayment && paid != 1 {
		return ""
	}
	s := strings.TrimSpace(strings.ToLower(raw))
	switch s {
	case "":
		if paid == 1 {
			return "balance"
		}
		return ""
	case "yue":
		return "balance"
	default:
		return truncateRunes(s, 16)
	}
}

// legacyOrderHash 生成目标 orders.request_hash 的确定性占位值（历史订单无原请求体）；
// 不参与业务判定，仅满足 NOT NULL 与审计可追溯。
func legacyOrderHash(orderNo string) string {
	sum := md5.Sum([]byte("legacy:" + orderNo))
	return hex.EncodeToString(sum[:])
}

func loadLegacyOrders(ctx context.Context, source *sql.DB) ([]*legacyOrder, error) {
	rows, err := source.QueryContext(ctx,
		`SELECT id, order_id, uid, real_name, user_phone, user_address, freight_price, total_price,
			total_postage, pay_price, pay_postage, deduction_price, coupon_id, coupon_price, paid,
			pay_time, pay_type, create_time, status, refund_status, delivery_name, delivery_sn, delivery_type, is_del
		 FROM store_order ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("读取 store_order: %w", err)
	}
	defer rows.Close()

	var out []*legacyOrder
	for rows.Next() {
		var o legacyOrder
		var freight, total, postage, pay, payPostage, deduction, coupon sql.NullString
		var realName, phone, address, payType, deliveryName, deliverySN, deliveryType sql.NullString
		var couponID sql.NullInt64
		if err := rows.Scan(&o.ID, &o.OrderNo, &o.UID, &realName, &phone, &address, &freight, &total,
			&postage, &pay, &payPostage, &deduction, &couponID, &coupon, &o.Paid,
			&o.PayTime, &payType, &o.CreateTime, &o.Status, &o.RefundStatus,
			&deliveryName, &deliverySN, &deliveryType, &o.IsDel); err != nil {
			return nil, fmt.Errorf("扫描 store_order: %w", err)
		}
		o.RealName = realName.String
		o.UserPhone = phone.String
		o.UserAddress = address.String
		o.FreightPrice = freight.String
		o.TotalPrice = total.String
		o.TotalPostage = postage.String
		o.PayPrice = pay.String
		o.PayPostage = payPostage.String
		o.DeductionPrice = deduction.String
		o.CouponID = couponID.Int64
		o.CouponPrice = coupon.String
		o.PayType = payType.String
		o.DeliveryName = deliveryName.String
		o.DeliverySN = deliverySN.String
		o.DeliveryType = deliveryType.String
		out = append(out, &o)
	}
	return out, rows.Err()
}

func loadLegacyOrderItems(ctx context.Context, source *sql.DB) ([]*legacyOrderItem, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, oid, product_id, cart_info, `unique` FROM store_order_cart_info ORDER BY oid, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_order_cart_info: %w", err)
	}
	defer rows.Close()

	var out []*legacyOrderItem
	for rows.Next() {
		var it legacyOrderItem
		if err := rows.Scan(&it.ID, &it.OID, &it.ProductID, &it.CartInfo, &it.CartUnique); err != nil {
			return nil, fmt.Errorf("扫描 store_order_cart_info: %w", err)
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

func loadLegacyOrderLogs(ctx context.Context, source *sql.DB) ([]*legacyOrderStatusLog, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, oid, change_type, change_message, change_time FROM store_order_status ORDER BY oid, change_time, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_order_status: %w", err)
	}
	defer rows.Close()

	var out []*legacyOrderStatusLog
	for rows.Next() {
		var l legacyOrderStatusLog
		if err := rows.Scan(&l.ID, &l.OID, &l.ChangeType, &l.ChangeMessage, &l.ChangeTime); err != nil {
			return nil, fmt.Errorf("扫描 store_order_status: %w", err)
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}
