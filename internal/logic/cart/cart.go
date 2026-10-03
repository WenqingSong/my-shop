// Package cart 实现购物车业务逻辑：添加（含原子累加）、改数量、勾选、删除与列表。
package cart

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/cart/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxQuantity 购物车条目数量业务上限。
	maxQuantity = 999

	// DB 层状态值（联查解释用）：SKU enabled=1、商品 on_shelf=1。
	dbSkuEnabled     = 1
	dbProductOnShelf = 1

	// unavailableReason 不可购原因枚举。
	reasonSkuDeleted = "sku_deleted"
	reasonDisabled   = "disabled"
	reasonOffShelf   = "off_shelf"
)

type sCart struct{}

func init() {
	service.RegisterCart(New())
}

// New 创建并返回购物车服务实现。
func New() *sCart {
	return &sCart{}
}

// cartItemRow 是购物车条目联查结果（LEFT JOIN skus/products/inventories）。
// SKU 被删除时，skus/products/inventories 侧字段为 NULL（对应指针字段为 nil）。
type cartItemRow struct {
	Id               int64       `orm:"id"`
	SkuId            int64       `orm:"sku_id"`
	Quantity         int64       `orm:"quantity"`
	Selected         int         `orm:"selected"`
	PriceSnapshot    int64       `orm:"price_snapshot"`
	CreatedAt        *gtime.Time `orm:"created_at"`
	UpdatedAt        *gtime.Time `orm:"updated_at"`
	ProductId        *int64      `orm:"product_id"`
	SkuName          *string     `orm:"sku_name"`
	SkuPrice         *int64      `orm:"sku_price"`
	SkuStatus        *int64      `orm:"sku_status"`
	ProductName      *string     `orm:"product_name"`
	ProductMainImage *string     `orm:"product_main_image"`
	ProductStatus    *int64      `orm:"product_status"`
	Stock            *int64      `orm:"stock"`
}

// List 列出当前用户全部条目（按 id 升序），并实时计算异常状态标识。
func (s *sCart) List(ctx context.Context, userID int64) (*v1.ListRes, error) {
	var rows []*cartItemRow
	if err := s.cartItemQuery(ctx).Where("ci.user_id", userID).Order("ci.id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询购物车条目: %w", err))
	}
	items := make([]*v1.CartItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toCartItem(r))
	}
	return &v1.ListRes{Items: items}, nil
}

// Add 添加 SKU：校验数量（1..999）与 SKU 可购（存在/enabled/商品 on_shelf），
// 取 skus.price 快照后原子写入：重复添加在数据库层原子累加，累加后超上限拒绝。
func (s *sCart) Add(ctx context.Context, userID int64, req *v1.AddReq) (*v1.AddRes, error) {
	qty, err := normalizeAddQuantity(req.Quantity)
	if err != nil {
		return nil, err
	}
	sku, err := service.Sku().GetByID(ctx, req.SkuId)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	if sku.Status != skuv1.StatusEnabled {
		return nil, codes.New(codes.CodeCartSkuUnavailable)
	}
	product, err := service.Product().GetByID(ctx, sku.ProductId)
	if err != nil {
		return nil, err
	}
	if product == nil || product.Status != productv1.StatusOnShelf {
		return nil, codes.New(codes.CodeCartSkuUnavailable)
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 原子 upsert：首次插入建新条目，重复则累加数量。单语句完成，避免无锁「先查再写」丢更新。
		if _, e := tx.Ctx(ctx).Exec(
			"INSERT INTO cart_items (user_id, sku_id, quantity, price_snapshot, selected) VALUES (?, ?, ?, ?, 1) ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)",
			userID, req.SkuId, qty, sku.Price,
		); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入购物车条目: %w", e))
		}
		// 事务内读回最新数量，校验不超过上限；超限整体回滚（拒绝且不静默截断）。
		row, e := tx.Model("cart_items").Ctx(ctx).
			Fields("quantity").
			Where("user_id", userID).
			Where("sku_id", req.SkuId).
			One()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取购物车数量: %w", e))
		}
		if row == nil || row.IsEmpty() {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入购物车条目后未找到记录"))
		}
		if cur := row["quantity"].Int64(); cur > maxQuantity {
			return codes.New(codes.CodeCartInvalidQuantity)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	item, err := s.findItemBySku(ctx, userID, req.SkuId)
	if err != nil {
		return nil, err
	}
	return &v1.AddRes{CartItem: *item}, nil
}

// UpdateQuantity 修改数量（正整数且 ≤ 999）；按 id AND user_id 定位，未命中 404。
func (s *sCart) UpdateQuantity(ctx context.Context, userID, itemID, quantity int64) (*v1.UpdateQuantityRes, error) {
	if quantity < 1 || quantity > maxQuantity {
		return nil, codes.New(codes.CodeCartInvalidQuantity)
	}
	result, err := g.DB().Model("cart_items").Ctx(ctx).
		Where("id", itemID).
		Where("user_id", userID).
		Data(g.Map{"quantity": quantity}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改购物车数量: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeCartItemNotFound)
	}
	item, err := s.findItemByID(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateQuantityRes{CartItem: *item}, nil
}

// UpdateSelected 勾选/取消勾选（持久化 selected）；按 id AND user_id 定位，未命中 404。
func (s *sCart) UpdateSelected(ctx context.Context, userID, itemID int64, selected bool) (*v1.UpdateSelectedRes, error) {
	val := 0
	if selected {
		val = 1
	}
	result, err := g.DB().Model("cart_items").Ctx(ctx).
		Where("id", itemID).
		Where("user_id", userID).
		Data(g.Map{"selected": val}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改购物车勾选状态: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeCartItemNotFound)
	}
	item, err := s.findItemByID(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateSelectedRes{CartItem: *item}, nil
}

// Delete 物理删除条目；按 id AND user_id 定位，未命中（不存在/已删除/属他人）统一 404。
func (s *sCart) Delete(ctx context.Context, userID, itemID int64) error {
	result, err := g.DB().Model("cart_items").Ctx(ctx).
		Where("id", itemID).
		Where("user_id", userID).
		Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除购物车条目: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeCartItemNotFound)
	}
	return nil
}

// cartItemQuery 构造购物车条目联查（LEFT JOIN skus/products/inventories），
// 容忍 SKU 缺失：SKU 被删除后条目保留为悬空引用，由 toCartItem 标识为 sku_deleted。
func (s *sCart) cartItemQuery(ctx context.Context) *gdb.Model {
	return g.DB().Ctx(ctx).Model("cart_items ci").
		Fields(
			"ci.id", "ci.sku_id", "ci.quantity", "ci.selected", "ci.price_snapshot",
			"ci.created_at", "ci.updated_at",
			"s.product_id", "s.name AS sku_name", "s.price AS sku_price", "s.status AS sku_status",
			"p.name AS product_name", "p.main_image AS product_main_image", "p.status AS product_status",
			"i.quantity AS stock",
		).
		LeftJoin("skus s", "s.id = ci.sku_id").
		LeftJoin("products p", "p.id = s.product_id").
		LeftJoin("inventories i", "i.sku_id = ci.sku_id")
}

// findItemByID 按条目 id + 用户过滤联查条目，未命中返回 404。
func (s *sCart) findItemByID(ctx context.Context, userID, itemID int64) (*v1.CartItem, error) {
	var rows []*cartItemRow
	if err := s.cartItemQuery(ctx).Where("ci.user_id", userID).Where("ci.id", itemID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询购物车条目: %w", err))
	}
	if len(rows) == 0 {
		return nil, codes.New(codes.CodeCartItemNotFound)
	}
	return s.toCartItem(rows[0]), nil
}

// findItemBySku 按 sku_id + 用户过滤联查条目，未命中返回 404。
func (s *sCart) findItemBySku(ctx context.Context, userID, skuID int64) (*v1.CartItem, error) {
	var rows []*cartItemRow
	if err := s.cartItemQuery(ctx).Where("ci.user_id", userID).Where("ci.sku_id", skuID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 sku_id 查询购物车条目: %w", err))
	}
	if len(rows) == 0 {
		return nil, codes.New(codes.CodeCartItemNotFound)
	}
	return s.toCartItem(rows[0]), nil
}

// toCartItem 将联查行转换为对外条目并动态计算异常标识。
func (s *sCart) toCartItem(r *cartItemRow) *v1.CartItem {
	item := &v1.CartItem{
		Id:            r.Id,
		SkuId:         r.SkuId,
		Quantity:      r.Quantity,
		Selected:      r.Selected == 1,
		PriceSnapshot: r.PriceSnapshot,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
	if r.SkuName != nil {
		item.SkuName = *r.SkuName
	}
	if r.ProductName != nil {
		item.ProductName = *r.ProductName
	}
	if r.ProductMainImage != nil {
		item.ProductMainImage = *r.ProductMainImage
	}
	item.ProductId = r.ProductId
	item.CurrentPrice = r.SkuPrice
	if r.SkuPrice != nil && *r.SkuPrice != r.PriceSnapshot {
		item.PriceChanged = true
	}

	// 可购性：SKU 存在 → enabled → 商品 on_shelf 依次判定。
	switch {
	case r.SkuStatus == nil:
		item.Available = false
		item.UnavailableReason = reasonSkuDeleted
	case *r.SkuStatus != dbSkuEnabled:
		item.Available = false
		item.UnavailableReason = reasonDisabled
	case r.ProductStatus == nil || *r.ProductStatus != dbProductOnShelf:
		item.Available = false
		item.UnavailableReason = reasonOffShelf
	default:
		item.Available = true
		item.UnavailableReason = ""
	}

	// 库存：SKU 存在时无库存记录视为 0；SKU 删除时为 null。
	if r.SkuStatus != nil {
		stock := int64(0)
		if r.Stock != nil {
			stock = *r.Stock
		}
		item.Stock = &stock
		if item.Available && item.Quantity > stock {
			item.Insufficient = true
		}
	}
	return item
}

// normalizeAddQuantity 解析添加数量：缺省为 1，必须为正整数且 ≤ 999。
func normalizeAddQuantity(n *json.Number) (int64, error) {
	if n == nil {
		return 1, nil
	}
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeCartInvalidQuantity)
	}
	if v < 1 || v > maxQuantity {
		return 0, codes.New(codes.CodeCartInvalidQuantity)
	}
	return v, nil
}
