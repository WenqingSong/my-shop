// Package flashsale 实现「秒杀核心闭环」V1 业务逻辑：
// 管理员创建/更新秒杀活动（绑定 SKU、秒杀价、秒杀库存、起止时间），登录用户在活动时间窗内以秒杀价下单。
// 以「一人一单 + 请求幂等键 + 条件库存扣减」在单一 MySQL 上保证：库存 ≥ 0、成功订单数 ≤ 初始库存、
// 一人一单、失败不建单、重复请求不重复扣库存。秒杀订单「下单即成交」，无支付/取消/退款状态机。
package flashsale

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// 活动状态（DB TINYINT）。
	statusEnabled  = 1
	statusDisabled = 0

	// maxNameLen 活动名最大字符数（trim 后）。
	maxNameLen = 64
	// maxPrice 秒杀价上限（整数分，= ¥999,999.99）。
	maxPrice = 99_999_999
	// maxOrderNoRetry 秒杀订单号撞号重试次数（uk_flash_order_no 兜底，撞号概率极低）。
	maxOrderNoRetry = 3
)

type sFlashSale struct{}

func init() {
	service.RegisterFlashSale(New())
}

// New 创建并返回秒杀服务实现。
func New() *sFlashSale {
	return &sFlashSale{}
}

// activityRow 是 flash_sale_activities 表的一条记录。
type activityRow struct {
	Id        int64       `json:"id"`
	Name      string      `json:"name"`
	Status    int         `json:"status"`
	StartTime *gtime.Time `json:"start_time"`
	EndTime   *gtime.Time `json:"end_time"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// activitySkuRow 是 flash_sale_activity_skus 表的一条记录。
type activitySkuRow struct {
	Id         int64 `json:"id"`
	ActivityId int64 `json:"activity_id"`
	SkuId      int64 `json:"sku_id"`
	FlashPrice int64 `json:"flash_price"`
	TotalStock int64 `json:"total_stock"`
	Sold       int64 `json:"sold"`
}

// orderRow 是 flash_sale_orders 表的一条记录。
type orderRow struct {
	Id               int64       `json:"id"`
	OrderNo          string      `json:"order_no"`
	UserId           int64       `json:"user_id"`
	ActivityId       int64       `json:"activity_id"`
	SkuId            int64       `json:"sku_id"`
	ProductId        int64       `json:"product_id"`
	SkuName          string      `json:"sku_name"`
	ProductName      string      `json:"product_name"`
	ProductMainImage string      `json:"product_main_image"`
	FlashPrice       int64       `json:"flash_price"`
	Quantity         int64       `json:"quantity"`
	IdempotencyKey   string      `json:"idempotency_key"`
	RequestHash      string      `json:"request_hash"`
	CreatedAt        *gtime.Time `json:"created_at"`
	UpdatedAt        *gtime.Time `json:"updated_at"`
}

// bindingInput 是校验后的 SKU 绑定输入（秒杀价已规范化为整数分）。
type bindingInput struct {
	SkuId      int64
	FlashPrice int64
	TotalStock int64
}

// skuSnapshot 是下单时校验 SKU/商品可用性后捕获的快照。
type skuSnapshot struct {
	ProductId        int64
	SkuName          string
	ProductName      string
	ProductMainImage string
}

// duplicateKeyErr 表示秒杀订单写入命中唯一约束（携带键名），由 CreateOrder 分流幂等/撞号/一人一单。
type duplicateKeyErr struct{ key string }

func (e *duplicateKeyErr) Error() string { return "秒杀订单唯一约束冲突: " + e.key }

// CreateActivity 创建秒杀活动：校验参数后单事务写入活动与 SKU 绑定（秒杀价/库存/起止时间）。
func (s *sFlashSale) CreateActivity(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	start, end, err := validateTimes(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	bindings, err := s.validateBindings(ctx, req.Skus)
	if err != nil {
		return nil, err
	}

	var activityID int64
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		id, e := tx.Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
			"name":       name,
			"status":     statusEnabled,
			"start_time": start,
			"end_time":   end,
		}).InsertAndGetId()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入秒杀活动: %w", e))
		}
		for _, b := range bindings {
			if _, e := tx.Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
				"activity_id": id,
				"sku_id":      b.SkuId,
				"flash_price": b.FlashPrice,
				"total_stock": b.TotalStock,
				"sold":        0,
			}).Insert(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入秒杀绑定: %w", e))
			}
		}
		activityID = id
		return nil
	})
	if err != nil {
		return nil, err
	}

	a, err := s.loadActivity(ctx, activityID)
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Activity: *a}, nil
}

// UpdateActivity 更新秒杀活动：改名称/状态/时间/秒杀价/库存（未提交字段保持不变）。
// SKU 绑定集合只能在 Create 时确定：提交 skus 时仅更新已存在绑定的秒杀价/库存，
// 不允许新增或删除绑定（Contract 未授权），sku_id 必须已绑定该活动。
func (s *sFlashSale) UpdateActivity(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeFlashSaleActivityNotFound)
	}

	name := old.Name
	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	status := old.Status
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	start := old.StartTime
	if req.StartTime != nil {
		start = req.StartTime
	}
	end := old.EndTime
	if req.EndTime != nil {
		end = req.EndTime
	}
	if start == nil || end == nil || end.Unix() <= start.Unix() {
		return nil, codes.New(codes.CodeFlashSaleInvalidArgument)
	}

	var bindings []bindingInput
	syncSkus := false
	if req.Skus != nil {
		if bindings, err = s.validateBindings(ctx, req.Skus); err != nil {
			return nil, err
		}
		syncSkus = true
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := tx.Model("flash_sale_activities").Ctx(ctx).Where("id", req.Id).Data(g.Map{
			"name":       name,
			"status":     status,
			"start_time": start,
			"end_time":   end,
		}).Update(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀活动: %w", e))
		}
		if syncSkus {
			if e := s.updateBindings(ctx, tx, req.Id, bindings); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	a, err := s.loadActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateRes{Activity: *a}, nil
}

// CreateOrder 秒杀下单：校验参数与 SKU/商品可用性后，单事务「时间窗校验 → 条件扣秒杀库存 → 创建订单」。
// 幂等键命中（uk_flash_idempotency）回滚扣减后读回既有订单；一人一单命中（uk_flash_one_per_user）回滚并返回 12004。
func (s *sFlashSale) CreateOrder(ctx context.Context, userID, activityID int64, req *v1.CreateOrderReq) (*v1.CreateOrderRes, error) {
	skuID := req.SkuId
	if skuID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	// SKU/商品可用性校验 + 快照（事务外，与普通订单 resolveLine 一致；服务端定价不信任客户端）。
	snap, err := s.resolveSku(ctx, skuID)
	if err != nil {
		return nil, err
	}

	hash := requestHash(activityID, skuID)

	var createdID int64
	for attempt := 0; attempt < maxOrderNoRetry; attempt++ {
		orderNo := generateOrderNo()
		createdID, err = s.insertOrder(ctx, userID, activityID, skuID, snap, idempotencyKey, hash, orderNo)
		if err == nil {
			break
		}
		var dk *duplicateKeyErr
		if errors.As(err, &dk) {
			switch {
			case strings.Contains(dk.key, "uk_flash_idempotency"):
				return s.handleIdempotency(ctx, userID, idempotencyKey, hash)
			case strings.Contains(dk.key, "uk_flash_one_per_user"):
				return nil, codes.New(codes.CodeFlashSaleAlreadyPurchased)
			case strings.Contains(dk.key, "uk_flash_order_no"):
				continue // 撞号，重新生成订单号重试
			}
		}
		return nil, err
	}
	if createdID == 0 {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成秒杀订单号重试失败"))
	}

	o, err := s.loadOrder(ctx, userID, createdID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建秒杀订单后未找到记录"))
	}
	return &v1.CreateOrderRes{FlashOrder: *o}, nil
}

// resolveSku 校验 SKU 存在且 enabled、商品 on_shelf，并捕获下单快照（名称/主图）。
// 返回 5001（SKU 不存在）、4001（商品不存在）、12006（SKU 禁用或商品下架）。
func (s *sFlashSale) resolveSku(ctx context.Context, skuID int64) (*skuSnapshot, error) {
	sku, err := service.Sku().GetByID(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	if sku.Status != skuv1.StatusEnabled {
		return nil, codes.New(codes.CodeFlashSaleSkuUnavailable)
	}
	product, err := service.Product().GetByID(ctx, sku.ProductId)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}
	if product.Status != productv1.StatusOnShelf {
		return nil, codes.New(codes.CodeFlashSaleSkuUnavailable)
	}
	return &skuSnapshot{
		ProductId:        sku.ProductId,
		SkuName:          sku.Name,
		ProductName:      product.Name,
		ProductMainImage: product.MainImage,
	}, nil
}

// insertOrder 在单事务内完成：活动存在性与状态校验 → 时间窗校验（MySQL NOW()）→ 锁定并读取
// 秒杀绑定（秒杀价快照）→ 条件扣减秒杀库存（防超卖）→ 插入秒杀订单（幂等/一人一单唯一约束兜底）。
func (s *sFlashSale) insertOrder(
	ctx context.Context,
	userID, activityID, skuID int64,
	snap *skuSnapshot,
	idempotencyKey, hash, orderNo string,
) (int64, error) {
	var orderID int64
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 1. 活动存在性 + 状态：不存在或已下架 → 12001。
		activity, e := loadActivityInTx(ctx, tx, activityID)
		if e != nil {
			return e
		}
		if activity == nil || activity.Status != statusEnabled {
			return codes.New(codes.CodeFlashSaleActivityNotFound)
		}

		// 2. 时间窗：同一事务内用 MySQL NOW() 判定（左闭右开 start_time <= NOW() < end_time），
		//    避免 Go 进程与 MySQL 时区漂移导致的边界误判。
		inWindow, e := isInTimeWindow(ctx, tx, activityID)
		if e != nil {
			return e
		}
		if !inWindow {
			return codes.New(codes.CodeFlashSaleNotInTimeWindow)
		}

		// 3. 锁定并读取活动 SKU 绑定：秒杀价在此重读并快照（非客户端提交价、非普通 SKU 价）。
		binding, e := loadBindingForUpdate(ctx, tx, activityID, skuID)
		if e != nil {
			return e
		}
		if binding == nil {
			return codes.New(codes.CodeInvalidArgument)
		}

		// 4. 条件扣减秒杀库存：UPDATE ... SET sold = sold + 1 WHERE sold < total_stock，
		//    RowsAffected 判定，防负库存、防超卖（并发下单由行锁串行化）。
		result, e := tx.Model("flash_sale_activity_skus").Ctx(ctx).
			Where("id", binding.Id).
			Where("sold < total_stock").
			Data(g.Map{"sold": gdb.Raw("sold + 1")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("扣减秒杀库存: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeFlashSaleStockInsufficient)
		}

		// 5. 插入秒杀订单：命中唯一约束时回滚整个事务（含上述扣减）并返回 duplicateKeyErr 供上层分流。
		id, e := tx.Model("flash_sale_orders").Ctx(ctx).Data(g.Map{
			"order_no":           orderNo,
			"user_id":            userID,
			"activity_id":        activityID,
			"sku_id":             skuID,
			"product_id":         snap.ProductId,
			"sku_name":           snap.SkuName,
			"product_name":       snap.ProductName,
			"product_main_image": snap.ProductMainImage,
			"flash_price":        binding.FlashPrice,
			"quantity":           1,
			"idempotency_key":    idempotencyKey,
			"request_hash":       hash,
		}).InsertAndGetId()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				return &duplicateKeyErr{key: key}
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入秒杀订单: %w", e))
		}
		orderID = id
		return nil
	})
	if err != nil {
		return 0, err
	}
	return orderID, nil
}

// handleIdempotency 读回既有秒杀订单：同请求指纹返回既有订单（幂等成功），否则 12005。
func (s *sFlashSale) handleIdempotency(ctx context.Context, userID int64, key, hash string) (*v1.CreateOrderRes, error) {
	row, err := s.findByIdempotencyKey(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("幂等键命中但未找到既有秒杀订单"))
	}
	if row.RequestHash != hash {
		return nil, codes.New(codes.CodeFlashSaleIdempotencyConflict)
	}
	return &v1.CreateOrderRes{FlashOrder: *toOrder(row)}, nil
}

// updateBindings 仅更新活动已存在 SKU 绑定的秒杀价/库存，不新增、不删除绑定。
// SKU 绑定集合只能在 Create 时确定（Contract 未授权 Update 增删绑定）：
// 请求中的 sku_id 必须已绑定该活动，否则返回 12007；新库存必须 ≥ 已售（sold），
// 保证更新后 remaining = total_stock - sold 恒 ≥ 0（避免重置 sold 导致超卖）。
func (s *sFlashSale) updateBindings(ctx context.Context, tx gdb.TX, activityID int64, inputs []bindingInput) error {
	for _, in := range inputs {
		// 锁定绑定行读取 sold，保证与后续更新在同一行锁下一致（与下单扣减的行锁串行化）。
		var rows []*activitySkuRow
		if err := tx.Model("flash_sale_activity_skus").Ctx(ctx).
			Where("activity_id", activityID).
			Where("sku_id", in.SkuId).
			LockUpdate().
			Scan(&rows); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀绑定: %w", err))
		}
		if len(rows) == 0 {
			// 该 SKU 尚未绑定此活动，Update 不允许新增绑定。
			return codes.New(codes.CodeFlashSaleInvalidArgument)
		}
		b := rows[0]
		if in.TotalStock < b.Sold {
			return codes.New(codes.CodeFlashSaleInvalidArgument)
		}
		if _, e := tx.Model("flash_sale_activity_skus").Ctx(ctx).Where("id", b.Id).Data(g.Map{
			"flash_price": in.FlashPrice,
			"total_stock": in.TotalStock,
		}).Update(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀绑定: %w", e))
		}
	}
	return nil
}

// validateBindings 校验 SKU 绑定列表：非空、SKU 存在、无重复、秒杀价 >0 且 ≤ 上限、库存为正。
func (s *sFlashSale) validateBindings(ctx context.Context, inputs []*v1.ActivitySkuInput) ([]bindingInput, error) {
	if len(inputs) == 0 {
		return nil, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	seen := make(map[int64]bool, len(inputs))
	out := make([]bindingInput, 0, len(inputs))
	for _, in := range inputs {
		if in == nil || in.SkuId <= 0 {
			return nil, codes.New(codes.CodeFlashSaleInvalidArgument)
		}
		if seen[in.SkuId] {
			return nil, codes.New(codes.CodeFlashSaleInvalidArgument)
		}
		seen[in.SkuId] = true

		price, err := parseFlashPrice(in.FlashPrice)
		if err != nil {
			return nil, err
		}
		if in.TotalStock <= 0 {
			return nil, codes.New(codes.CodeFlashSaleInvalidArgument)
		}
		sku, err := service.Sku().GetByID(ctx, in.SkuId)
		if err != nil {
			return nil, err
		}
		if sku == nil {
			return nil, codes.New(codes.CodeSkuNotFound)
		}
		out = append(out, bindingInput{SkuId: in.SkuId, FlashPrice: price, TotalStock: in.TotalStock})
	}
	return out, nil
}

// findActivity 按 id 查询活动行，不存在返回 nil。
func (s *sFlashSale) findActivity(ctx context.Context, id int64) (*activityRow, error) {
	var rows []*activityRow
	if err := g.DB().Model("flash_sale_activities").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀活动: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadActivity 加载完整活动（含 SKU 绑定列表）。
func (s *sFlashSale) loadActivity(ctx context.Context, activityID int64) (*v1.Activity, error) {
	row, err := s.findActivity(ctx, activityID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeFlashSaleActivityNotFound)
	}
	var skuRows []*activitySkuRow
	if err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Where("activity_id", activityID).Order("id").Scan(&skuRows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀绑定: %w", err))
	}
	return toActivity(row, skuRows), nil
}

// findByIdempotencyKey 按 user_id + idempotency_key 查询秒杀订单行，未命中返回 nil。
func (s *sFlashSale) findByIdempotencyKey(ctx context.Context, userID int64, key string) (*orderRow, error) {
	var rows []*orderRow
	if err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("user_id", userID).Where("idempotency_key", key).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按幂等键查询秒杀订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadOrder 按 id + user_id 加载秒杀订单，未命中返回 nil。
func (s *sFlashSale) loadOrder(ctx context.Context, userID, orderID int64) (*v1.FlashOrder, error) {
	var rows []*orderRow
	if err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("id", orderID).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀订单: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return toOrder(rows[0]), nil
}

// loadActivityInTx 在事务内按 id 查询活动行，不存在返回 nil。
func loadActivityInTx(ctx context.Context, tx gdb.TX, activityID int64) (*activityRow, error) {
	var rows []*activityRow
	if err := tx.Model("flash_sale_activities").Ctx(ctx).Where("id", activityID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀活动: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// isInTimeWindow 在事务内以 MySQL NOW() 判定活动是否处于时间窗（左闭右开）。
func isInTimeWindow(ctx context.Context, tx gdb.TX, activityID int64) (bool, error) {
	n, err := tx.Model("flash_sale_activities").Ctx(ctx).
		Where("id", activityID).
		Where("start_time <= NOW()").
		Where("end_time > NOW()").
		Count()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("判定秒杀时间窗: %w", err))
	}
	return n > 0, nil
}

// loadBindingForUpdate 在事务内锁定并读取活动 SKU 绑定，未命中返回 nil。
// FOR UPDATE 使秒杀价快照与后续条件扣减在同一行锁下保持一致。
func loadBindingForUpdate(ctx context.Context, tx gdb.TX, activityID, skuID int64) (*activitySkuRow, error) {
	var rows []*activitySkuRow
	if err := tx.Model("flash_sale_activity_skus").Ctx(ctx).
		Where("activity_id", activityID).
		Where("sku_id", skuID).
		LockUpdate().
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀绑定: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// toActivity 将活动行与绑定行转换为对外结构。
func toActivity(r *activityRow, skuRows []*activitySkuRow) *v1.Activity {
	skus := make([]*v1.ActivitySku, 0, len(skuRows))
	for _, b := range skuRows {
		skus = append(skus, &v1.ActivitySku{
			Id:         b.Id,
			SkuId:      b.SkuId,
			FlashPrice: b.FlashPrice,
			TotalStock: b.TotalStock,
			Sold:       b.Sold,
		})
	}
	return &v1.Activity{
		Id:        r.Id,
		Name:      r.Name,
		Status:    statusToString(r.Status),
		StartTime: r.StartTime,
		EndTime:   r.EndTime,
		Skus:      skus,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// toOrder 将秒杀订单行转换为对外结构。
func toOrder(r *orderRow) *v1.FlashOrder {
	return &v1.FlashOrder{
		Id:               r.Id,
		OrderNo:          r.OrderNo,
		UserId:           r.UserId,
		ActivityId:       r.ActivityId,
		SkuId:            r.SkuId,
		ProductId:        r.ProductId,
		SkuName:          r.SkuName,
		ProductName:      r.ProductName,
		ProductMainImage: r.ProductMainImage,
		FlashPrice:       r.FlashPrice,
		Quantity:         r.Quantity,
		IdempotencyKey:   r.IdempotencyKey,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	if s == statusEnabled {
		return v1.StatusEnabled
	}
	return v1.StatusDisabled
}

// parseStatus 将 API 字符串状态枚举解析为 DB TINYINT，非法值返回 12007。
func parseStatus(status string) (int, error) {
	switch status {
	case v1.StatusEnabled:
		return statusEnabled, nil
	case v1.StatusDisabled:
		return statusDisabled, nil
	default:
		return 0, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
}

// validateName trim 后校验非空与长度，返回规范化活动名。
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	return name, nil
}

// validateTimes 校验起止时间非空且结束严格晚于开始。
func validateTimes(start, end *gtime.Time) (*gtime.Time, *gtime.Time, error) {
	if start == nil || end == nil {
		return nil, nil, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	if end.Unix() <= start.Unix() {
		return nil, nil, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	return start, end, nil
}

// parseFlashPrice 校验秒杀价为合法整数分（正整数、不超上限）。
func parseFlashPrice(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	if v <= 0 || v > maxPrice {
		return 0, codes.New(codes.CodeFlashSaleInvalidArgument)
	}
	return v, nil
}

// requestHash 计算请求指纹：sha256hex(activity_id|sku_id)。
func requestHash(activityID, skuID int64) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(activityID, 10) + "|" + strconv.FormatInt(skuID, 10)))
	return hex.EncodeToString(sum[:])
}

// generateOrderNo 生成秒杀订单号：FS 前缀 + 毫秒时间戳 + 随机段（12 hex），uk_flash_order_no 兜底撞号重试。
func generateOrderNo() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "FS" + strconv.FormatInt(time.Now().UnixMilli(), 10) + hex.EncodeToString(b[:])
}

// duplicateKeyName 从错误链中提取 MySQL 唯一约束冲突（1062）的键名，非重复键错误返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}
