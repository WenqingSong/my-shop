// Package order 实现「普通订单核心闭环」业务逻辑：创建（购物车/直接购买）、服务端定价与快照、
// 唯一订单号、幂等去重、状态机推进、取消/退款恢复库存、超时自动取消与支付 Mock（幂等）。
package order

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/os/gtime"

	addressv1 "cnb.cool/go-cloud-devops/my-shop/api/address/v1"
	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 订单状态（DB TINYINT，见 docs/design/order.md 状态机）。
const (
	statusPendingPayment = 10
	statusPaid           = 20
	statusShipped        = 30
	statusReceived       = 40
	statusCompleted      = 50
	statusCancelled      = 60
	statusRefunded       = 70
)

// 取消原因（DB TINYINT）。
const (
	cancelReasonUser    = 1
	cancelReasonTimeout = 2
)

const (
	// maxOrderQuantity 单行项目数量上限（与购物车业务上限一致，防 INT UNSIGNED 溢出）。
	maxOrderQuantity = 999
	// maxOrderNoRetry 订单号撞号重试次数（uk_order_no 兜底，撞号概率极低）。
	maxOrderNoRetry = 3
)

type sOrder struct{}

func init() {
	service.RegisterOrder(New())
}

// New 创建并返回订单服务实现。
func New() *sOrder {
	return &sOrder{}
}

// orderRow 是 orders 表的一条记录。
type orderRow struct {
	Id             int64       `json:"id"`
	OrderNo        string      `json:"order_no"`
	UserId         int64       `json:"user_id"`
	Status         int         `json:"status"`
	TotalAmount    int64       `json:"total_amount"`
	IdempotencyKey string      `json:"idempotency_key"`
	RequestHash    string      `json:"request_hash"`
	RecipientName  string      `json:"recipient_name"`
	Phone          string      `json:"phone"`
	Province       string      `json:"province"`
	City           string      `json:"city"`
	District       string      `json:"district"`
	Detail         string      `json:"detail"`
	AddressId      *int64      `json:"address_id"`
	ExpireAt       *gtime.Time `json:"expire_at"`
	CancelReason   *int        `json:"cancel_reason"`
	PaidAt         *gtime.Time `json:"paid_at"`
	ShippedAt      *gtime.Time `json:"shipped_at"`
	ReceivedAt     *gtime.Time `json:"received_at"`
	CompletedAt    *gtime.Time `json:"completed_at"`
	CancelledAt    *gtime.Time `json:"cancelled_at"`
	RefundedAt     *gtime.Time `json:"refunded_at"`
	CreatedAt      *gtime.Time `json:"created_at"`
	UpdatedAt      *gtime.Time `json:"updated_at"`
}

// orderItemRow 是 order_items 表的一条记录。
type orderItemRow struct {
	Id               int64  `json:"id"`
	OrderId          int64  `json:"order_id"`
	SkuId            int64  `json:"sku_id"`
	ProductId        int64  `json:"product_id"`
	SkuName          string `json:"sku_name"`
	ProductName      string `json:"product_name"`
	ProductMainImage string `json:"product_main_image"`
	Price            int64  `json:"price"`
	Quantity         int64  `json:"quantity"`
}

// lineItem 是下单时解析出的行项目（含成交价与商品/SKU 快照）。
type lineItem struct {
	SkuID            int64
	Quantity         int64
	SkuName          string
	ProductID        int64
	ProductName      string
	ProductMainImage string
	Price            int64
}

// duplicateKeyErr 表示订单写入命中唯一约束（携带键名），由 Create 分流幂等/撞号重试。
type duplicateKeyErr struct{ key string }

func (e *duplicateKeyErr) Error() string { return "订单唯一约束冲突: " + e.key }

// Create 创建订单：服务端定价 → 组装快照 → 单事务「写订单 + 写订单项 + 扣库存（+ 清已购购物车条目）」。
// 幂等键命中（uk_user_idempotency）时回滚并读回既有订单：同请求指纹返回既有订单、否则 9006。
func (s *sOrder) Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error) {
	source := strings.TrimSpace(req.Source)
	if source != v1.SourceCart && source != v1.SourceDirect {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.AddressId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	address, err := service.Address().Detail(ctx, userID, req.AddressId)
	if err != nil {
		return nil, err
	}

	lines, err := s.buildLines(ctx, source, userID, req)
	if err != nil {
		return nil, err
	}

	totalAmount := int64(0)
	for _, l := range lines {
		totalAmount += l.Price * l.Quantity
	}

	hash := requestHash(source, req.AddressId, lines)

	var createdID int64
	for attempt := 0; attempt < maxOrderNoRetry; attempt++ {
		orderNo := generateOrderNo()
		createdID, err = s.insertOrder(ctx, userID, source, req.AddressId, idempotencyKey, hash, totalAmount, orderNo, &address.Address, lines)
		if err == nil {
			break
		}
		var dk *duplicateKeyErr
		if errors.As(err, &dk) {
			switch {
			case strings.Contains(dk.key, "uk_user_idempotency"):
				return s.handleIdempotency(ctx, userID, idempotencyKey, hash)
			case strings.Contains(dk.key, "uk_order_no"):
				continue // 撞号，重新生成订单号重试
			}
		}
		return nil, err
	}
	if createdID == 0 {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成订单号重试失败"))
	}

	o, err := s.loadOwnedOrder(ctx, userID, createdID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建订单后未找到记录"))
	}
	return &v1.CreateRes{Order: *o}, nil
}

// buildLines 依据来源组装行项目：直接购买取单个 SKU；购物车购买取全部勾选项。
func (s *sOrder) buildLines(ctx context.Context, source string, userID int64, req *v1.CreateReq) ([]lineItem, error) {
	switch source {
	case v1.SourceDirect:
		if req.SkuId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		if req.Quantity < 1 || req.Quantity > maxOrderQuantity {
			return nil, codes.New(codes.CodeOrderInvalidQuantity)
		}
		line, err := s.resolveLine(ctx, req.SkuId, req.Quantity)
		if err != nil {
			return nil, err
		}
		return []lineItem{line}, nil
	case v1.SourceCart:
		cart, err := service.Cart().List(ctx, userID)
		if err != nil {
			return nil, err
		}
		lines := make([]lineItem, 0, len(cart.Items))
		for _, item := range cart.Items {
			if !item.Selected {
				continue
			}
			line, err := s.resolveLine(ctx, item.SkuId, item.Quantity)
			if err != nil {
				return nil, err
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return nil, codes.New(codes.CodeOrderCartEmpty)
		}
		return lines, nil
	default:
		return nil, codes.New(codes.CodeInvalidArgument)
	}
}

// resolveLine 服务端定价：重读 skus.price 作为成交价，校验 SKU enabled 且商品 on_shelf，
// 同时快照商品/SKU 名称与主图。不信任客户端提交价、也不信任购物车快照。
func (s *sOrder) resolveLine(ctx context.Context, skuID, qty int64) (lineItem, error) {
	sku, err := service.Sku().GetByID(ctx, skuID)
	if err != nil {
		return lineItem{}, err
	}
	if sku == nil {
		return lineItem{}, codes.New(codes.CodeSkuNotFound)
	}
	if sku.Status != skuv1.StatusEnabled {
		return lineItem{}, codes.New(codes.CodeOrderSkuUnavailable)
	}
	product, err := service.Product().GetByID(ctx, sku.ProductId)
	if err != nil {
		return lineItem{}, err
	}
	if product == nil || product.Status != productv1.StatusOnShelf {
		return lineItem{}, codes.New(codes.CodeOrderSkuUnavailable)
	}
	return lineItem{
		SkuID:            skuID,
		Quantity:         qty,
		SkuName:          sku.Name,
		ProductID:        sku.ProductId,
		ProductName:      product.Name,
		ProductMainImage: product.MainImage,
		Price:            sku.Price,
	}, nil
}

// insertOrder 在单事务内写订单 + 订单项 + 扣库存（购物车模式另删已购条目），返回订单 id。
// 订单 INSERT 命中唯一约束时返回 duplicateKeyErr 供上层分流。
func (s *sOrder) insertOrder(
	ctx context.Context,
	userID int64,
	source string,
	addressID int64,
	idempotencyKey, hash string,
	totalAmount int64,
	orderNo string,
	address *addressv1.Address,
	lines []lineItem,
) (int64, error) {
	var orderID int64
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		id, e := tx.Model("orders").Ctx(ctx).Data(g.Map{
			"order_no":        orderNo,
			"user_id":         userID,
			"status":          statusPendingPayment,
			"total_amount":    totalAmount,
			"idempotency_key": idempotencyKey,
			"request_hash":    hash,
			"recipient_name":  address.RecipientName,
			"phone":           address.Phone,
			"province":        address.Province,
			"city":            address.City,
			"district":        address.District,
			"detail":          address.Detail,
			"address_id":      addressID,
			// 支付截止时间用 MySQL NOW() 计算，避免 Go 进程与 MySQL 的时区不一致导致过期判定偏移。
			"expire_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(payTimeout(ctx), 10) + " SECOND)"),
		}).InsertAndGetId()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				return &duplicateKeyErr{key: key}
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入订单: %w", e))
		}

		for _, l := range lines {
			if _, e := tx.Model("order_items").Ctx(ctx).Data(g.Map{
				"order_id":           id,
				"sku_id":             l.SkuID,
				"product_id":         l.ProductID,
				"sku_name":           l.SkuName,
				"product_name":       l.ProductName,
				"product_main_image": l.ProductMainImage,
				"price":              l.Price,
				"quantity":           l.Quantity,
			}).Insert(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入订单项: %w", e))
			}
		}

		for _, l := range lines {
			if _, e := service.Inventory().DeductInTx(ctx, tx, l.SkuID, l.Quantity, nil); e != nil {
				return e
			}
		}

		if source == v1.SourceCart {
			skuIDs := make([]int64, 0, len(lines))
			for _, l := range lines {
				skuIDs = append(skuIDs, l.SkuID)
			}
			if _, e := tx.Model("cart_items").Ctx(ctx).
				Where("user_id", userID).
				WhereIn("sku_id", skuIDs).
				Delete(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("清理已购购物车条目: %w", e))
			}
		}

		orderID = id
		return nil
	})
	if err != nil {
		return 0, err
	}
	return orderID, nil
}

// handleIdempotency 读回既有订单：同请求指纹返回既有订单（幂等成功），否则 9006。
func (s *sOrder) handleIdempotency(ctx context.Context, userID int64, key, hash string) (*v1.CreateRes, error) {
	row, err := s.findByIdempotencyKey(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("幂等键命中但未找到既有订单"))
	}
	if row.RequestHash != hash {
		return nil, codes.New(codes.CodeOrderIdempotencyConflict)
	}
	o, err := s.toOrder(ctx, row)
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Order: *o}, nil
}

// List 查询本人订单列表（按 id 倒序，含订单项）。
func (s *sOrder) List(ctx context.Context, userID int64) (*v1.ListRes, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("user_id", userID).Order("id DESC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单列表: %w", err))
	}
	items := make([]*v1.Order, 0, len(rows))
	for _, r := range rows {
		o, err := s.toOrder(ctx, r)
		if err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return &v1.ListRes{Items: items}, nil
}

// Detail 查询本人订单详情；不存在或非本人统一 9001。待支付且已过期时先懒取消再返回。
func (s *sOrder) Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	if row.Status == statusPendingPayment {
		if _, err := s.cancelExpired(ctx, id); err != nil {
			return nil, err
		}
	}
	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.DetailRes{Order: *o}, nil
}

// Pay Mock 支付：仅待支付→已支付合法，幂等；已过期订单先懒取消。
func (s *sOrder) Pay(ctx context.Context, userID, id int64) (*v1.PayRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	if row.Status == statusPendingPayment {
		// 懒取消：待支付且已过期（expire_at < NOW()，SQL 判定，避免 Go↔MySQL 时区漂移）。
		cancelled, e := s.cancelExpired(ctx, id)
		if e != nil {
			return nil, e
		}
		if cancelled {
			return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
		}
	}

	switch row.Status {
	case statusPaid:
		// 重复支付：幂等成功，不重复改状态。
		o, err := s.toOrder(ctx, row)
		if err != nil {
			return nil, err
		}
		return &v1.PayRes{Order: *o}, nil
	case statusPendingPayment:
		result, e := g.DB().Model("orders").Ctx(ctx).
			Where("id", id).
			Where("user_id", userID).
			Where("status", statusPendingPayment).
			Data(g.Map{"status": statusPaid, "paid_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("支付订单: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		o, err := s.loadOwnedOrder(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if o == nil {
			return nil, codes.New(codes.CodeOrderNotFound)
		}
		return &v1.PayRes{Order: *o}, nil
	default:
		return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
	}
}

// Cancel 取消本人待支付订单：条件更新 + 恢复库存，原子；非待支付返回 9002。
func (s *sOrder) Cancel(ctx context.Context, userID, id int64) (*v1.CancelRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	var changed bool
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var e error
		changed, e = s.cancelInTx(ctx, tx, id, cancelReasonUser, false)
		return e
	})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
	}

	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.CancelRes{Order: *o}, nil
}

// Receive 确认收货：已发货→已收货→已完成（同请求同事务原子推进）。
func (s *sOrder) Receive(ctx context.Context, userID, id int64) (*v1.ReceiveRes, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := tx.Model("orders").Ctx(ctx).
			Where("id", id).
			Where("user_id", userID).
			Where("status", statusShipped).
			Data(g.Map{"status": statusReceived, "received_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("确认收货: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		if _, e := tx.Model("orders").Ctx(ctx).
			Where("id", id).
			Where("status", statusReceived).
			Data(g.Map{"status": statusCompleted, "completed_at": gdb.Raw("NOW()")}).
			Update(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("完成订单: %w", e))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	o, err := s.loadOwnedOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.ReceiveRes{Order: *o}, nil
}

// Ship 发货（仅管理员）：已支付→已发货。
func (s *sOrder) Ship(ctx context.Context, orderID int64) (*v1.ShipRes, error) {
	row, err := s.findByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	result, err := g.DB().Model("orders").Ctx(ctx).
		Where("id", orderID).
		Where("status", statusPaid).
		Data(g.Map{"status": statusShipped, "shipped_at": gdb.Raw("NOW()")}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("发货: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, codes.New(codes.CodeOrderInvalidStatusTransition)
	}

	o, err := s.loadByIDOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.ShipRes{Order: *o}, nil
}

// Refund 退款（仅管理员）：已支付→已退款，恢复库存（原子，仅补偿一次）。
func (s *sOrder) Refund(ctx context.Context, orderID int64) (*v1.RefundRes, error) {
	row, err := s.findByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := tx.Model("orders").Ctx(ctx).
			Where("id", orderID).
			Where("status", statusPaid).
			Data(g.Map{"status": statusRefunded, "refunded_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("退款: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeOrderInvalidStatusTransition)
		}
		items, e := s.loadItemsInTx(ctx, tx, orderID)
		if e != nil {
			return e
		}
		for _, it := range items {
			if _, e := service.Inventory().IncreaseInTx(ctx, tx, it.SkuId, it.Quantity, nil); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	o, err := s.loadByIDOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.New(codes.CodeOrderNotFound)
	}
	return &v1.RefundRes{Order: *o}, nil
}

// CancelExpired 取消过期的待支付订单（供后台扫描器复用），逐单原子取消，返回本次实际取消数量。
// 单单失败不影响其他订单与后续轮次。
func (s *sOrder) CancelExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).
		Fields("id").
		Where("status", statusPendingPayment).
		Where("expire_at < NOW()").
		Limit(limit).
		Scan(&rows); err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("扫描过期订单: %w", err))
	}
	count := 0
	for _, r := range rows {
		cancelled, err := s.cancelExpired(ctx, r.Id)
		if err != nil {
			glog.Warningf(ctx, "超时取消订单 %d 失败: %v", r.Id, err)
			continue
		}
		if cancelled {
			count++
		}
	}
	return count, nil
}

// cancelExpired 原子取消「待支付且已过期」的订单（含库存恢复）；未过期/已非待支付返回 false。
func (s *sOrder) cancelExpired(ctx context.Context, orderID int64) (bool, error) {
	var changed bool
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var e error
		changed, e = s.cancelInTx(ctx, tx, orderID, cancelReasonTimeout, true)
		return e
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// cancelInTx 在事务内原子取消订单：条件更新 status=10 → 60 + 恢复库存；仅命中者恢复一次库存。
// onlyExpired=true 时额外要求 expire_at < NOW()（SQL 判定，避免 Go↔MySQL 时区漂移）。
func (s *sOrder) cancelInTx(ctx context.Context, tx gdb.TX, orderID int64, reason int, onlyExpired bool) (bool, error) {
	model := tx.Model("orders").Ctx(ctx).
		Where("id", orderID).
		Where("status", statusPendingPayment)
	if onlyExpired {
		model = model.Where("expire_at < NOW()")
	}
	result, err := model.Data(g.Map{
		"status":        statusCancelled,
		"cancel_reason": reason,
		"cancelled_at":  gdb.Raw("NOW()"),
	}).Update()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消订单: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return false, nil
	}
	items, err := s.loadItemsInTx(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	for _, it := range items {
		if _, err := service.Inventory().IncreaseInTx(ctx, tx, it.SkuId, it.Quantity, nil); err != nil {
			return false, err
		}
	}
	return true, nil
}

// findOwned 按 id + user_id 查询订单行，未命中返回 nil（区分不存在与非本人不在此层）。
func (s *sOrder) findOwned(ctx context.Context, userID, id int64) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("id", id).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findByID 按 id 查询订单行（后台无归属过滤），未命中返回 nil。
func (s *sOrder) findByID(ctx context.Context, id int64) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findByIdempotencyKey 按 user_id + idempotency_key 查询订单行，未命中返回 nil。
func (s *sOrder) findByIdempotencyKey(ctx context.Context, userID int64, key string) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("orders").Ctx(ctx).Where("user_id", userID).Where("idempotency_key", key).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按幂等键查询订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadOwnedOrder 按 id + user_id 加载完整订单（含订单项），未命中返回 nil。
func (s *sOrder) loadOwnedOrder(ctx context.Context, userID, id int64) (*v1.Order, error) {
	row, err := s.findOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return s.toOrder(ctx, row)
}

// loadByIDOrder 按 id 加载完整订单（后台，含订单项），未命中返回 nil。
func (s *sOrder) loadByIDOrder(ctx context.Context, id int64) (*v1.Order, error) {
	row, err := s.findByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return s.toOrder(ctx, row)
}

// loadItems 查询订单项（按 id 升序）。
func (s *sOrder) loadItems(ctx context.Context, orderID int64) ([]*v1.OrderItem, error) {
	var rows []*orderItemRow
	if err := g.DB().Model("order_items").Ctx(ctx).Where("order_id", orderID).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单项: %w", err))
	}
	return toOrderItems(rows), nil
}

// loadItemsInTx 在事务内查询订单项（供取消/退款恢复库存使用）。
func (s *sOrder) loadItemsInTx(ctx context.Context, tx gdb.TX, orderID int64) ([]*orderItemRow, error) {
	var rows []*orderItemRow
	if err := tx.Model("order_items").Ctx(ctx).Where("order_id", orderID).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单项: %w", err))
	}
	return rows, nil
}

// toOrder 将订单行（含订单项）转换为对外结构。
func (s *sOrder) toOrder(ctx context.Context, r *orderRow) (*v1.Order, error) {
	items, err := s.loadItems(ctx, r.Id)
	if err != nil {
		return nil, err
	}
	return &v1.Order{
		Id:             r.Id,
		OrderNo:        r.OrderNo,
		UserId:         r.UserId,
		Status:         statusToString(r.Status),
		TotalAmount:    r.TotalAmount,
		IdempotencyKey: r.IdempotencyKey,
		RecipientName:  r.RecipientName,
		Phone:          r.Phone,
		Province:       r.Province,
		City:           r.City,
		District:       r.District,
		Detail:         r.Detail,
		AddressId:      r.AddressId,
		ExpireAt:       r.ExpireAt,
		CancelReason:   r.CancelReason,
		PaidAt:         r.PaidAt,
		ShippedAt:      r.ShippedAt,
		ReceivedAt:     r.ReceivedAt,
		CompletedAt:    r.CompletedAt,
		CancelledAt:    r.CancelledAt,
		RefundedAt:     r.RefundedAt,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		Items:          items,
	}, nil
}

// toOrderItems 将订单项行转换为对外结构。
func toOrderItems(rows []*orderItemRow) []*v1.OrderItem {
	items := make([]*v1.OrderItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.OrderItem{
			Id:               r.Id,
			SkuId:            r.SkuId,
			ProductId:        r.ProductId,
			SkuName:          r.SkuName,
			ProductName:      r.ProductName,
			ProductMainImage: r.ProductMainImage,
			Price:            r.Price,
			Quantity:         r.Quantity,
		})
	}
	return items
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	switch s {
	case statusPaid:
		return v1.StatusPaid
	case statusShipped:
		return v1.StatusShipped
	case statusReceived:
		return v1.StatusReceived
	case statusCompleted:
		return v1.StatusCompleted
	case statusCancelled:
		return v1.StatusCancelled
	case statusRefunded:
		return v1.StatusRefunded
	default:
		return v1.StatusPendingPayment
	}
}

// requestHash 计算请求指纹：sha256hex(source|address_id|sku_id:qty...（按 sku_id 升序）)。
func requestHash(source string, addressID int64, lines []lineItem) string {
	sorted := make([]lineItem, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SkuID < sorted[j].SkuID })

	var b strings.Builder
	b.WriteString(source)
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(addressID, 10))
	for _, l := range sorted {
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(l.SkuID, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(l.Quantity, 10))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// generateOrderNo 生成订单号：毫秒时间戳 + 随机段（12 hex），uk_order_no 兜底撞号重试。
func generateOrderNo() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + hex.EncodeToString(b[:])
}

// payTimeout 返回支付超时时长（秒，默认 900），不硬编码。
func payTimeout(ctx context.Context) int64 {
	v := g.Cfg().MustGet(ctx, "order.pay_timeout", 900).Int64()
	if v <= 0 {
		return 900
	}
	return v
}

// duplicateKeyName 从错误链中提取 MySQL 唯一约束冲突（1062）的键名，非重复键错误返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}
