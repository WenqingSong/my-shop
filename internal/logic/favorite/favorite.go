// Package favorite 实现「商品收藏」业务逻辑：添加（商品存在且在售校验 + 唯一约束兜底并发去重）、
// 取消（按 product_id 幂等）、本人列表（实时联查商品并标识可用性）、是否已收藏。
package favorite

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/favorite/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// dbProductOnShelf 是 products.status 的在售值（1），用于实时标识收藏商品的可用性。
	dbProductOnShelf = 1

	// 不可用原因枚举。
	reasonOffShelf       = "off_shelf"
	reasonProductDeleted = "product_deleted"

	// 分页默认值与上限。
	defaultPage = 1
	defaultSize = 20
	maxSize     = 100
)

type sFavorite struct{}

func init() {
	service.RegisterFavorite(New())
}

// New 创建并返回商品收藏服务实现。
func New() *sFavorite {
	return &sFavorite{}
}

// favoriteRow 是收藏条目联查结果（LEFT JOIN products）。
// 商品被删除时，products 侧字段为 NULL（对应指针字段为 nil）。
type favoriteRow struct {
	Id               int64       `orm:"id"`
	ProductId        int64       `orm:"product_id"`
	CreatedAt        *gtime.Time `orm:"created_at"`
	ProductName      *string     `orm:"product_name"`
	ProductMainImage *string     `orm:"product_main_image"`
	ProductPrice     *int64      `orm:"product_price"`
	ProductStatus    *int64      `orm:"product_status"`
}

// Add 添加收藏：校验 product_id 为正、商品存在且在售，随后单条 INSERT；
// 命中 uk_user_product（1062）视为幂等成功（no-op），不产生第二条记录。
func (s *sFavorite) Add(ctx context.Context, userID int64, req *v1.AddReq) (*v1.AddRes, error) {
	if req.ProductId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	product, err := service.Product().GetByID(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}
	if product.Status != productv1.StatusOnShelf {
		return nil, codes.New(codes.CodeFavoriteProductUnavailable)
	}

	if _, err := g.DB().Model("favorites").Ctx(ctx).Data(g.Map{
		"user_id":    userID,
		"product_id": req.ProductId,
	}).Insert(); err != nil {
		if !isDuplicateKeyError(err) {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入收藏: %w", err))
		}
		// 重复收藏：唯一约束兜底，幂等成功，继续读取既有条目返回。
	}

	item, err := s.findByUserProduct(ctx, userID, req.ProductId)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入收藏后未找到记录"))
	}
	return &v1.AddRes{FavoriteItem: *item}, nil
}

// Remove 取消收藏：按 product_id AND user_id 物理删除；
// RowsAffected=0（未收藏/不存在）视为幂等成功（no-op），不报错、不区分越权。
func (s *sFavorite) Remove(ctx context.Context, userID, productID int64) error {
	if _, err := g.DB().Model("favorites").Ctx(ctx).
		Where("product_id", productID).
		Where("user_id", userID).
		Delete(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消收藏: %w", err))
	}
	return nil
}

// List 查询本人收藏列表：WHERE user_id 过滤，按收藏时间倒序分页，
// LEFT JOIN products 实时联查商品信息并动态标识可用性（悬空引用不报错）。
func (s *sFavorite) List(ctx context.Context, userID int64, req *v1.ListReq) (*v1.ListRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	total, err := g.DB().Model("favorites").Ctx(ctx).Where("user_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计收藏列表: %w", err))
	}

	var rows []*favoriteRow
	if err := s.favoriteQuery(ctx).Where("f.user_id", userID).Order("f.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询收藏列表: %w", err))
	}

	items := make([]*v1.FavoriteItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toFavoriteItem(r))
	}
	return &v1.ListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// Check 查询本人对指定商品是否已收藏（供商品详情展示收藏态）。
func (s *sFavorite) Check(ctx context.Context, userID, productID int64) (*v1.CheckRes, error) {
	if productID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("favorites").Ctx(ctx).
		Where("user_id", userID).
		Where("product_id", productID).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询收藏状态: %w", err))
	}
	return &v1.CheckRes{Favorited: n > 0}, nil
}

// favoriteQuery 构造收藏条目联查（LEFT JOIN products），容忍商品缺失：
// 商品被删除后条目保留为悬空引用，由 toFavoriteItem 标识为 product_deleted。
func (s *sFavorite) favoriteQuery(ctx context.Context) *gdb.Model {
	return g.DB().Ctx(ctx).Model("favorites f").
		Fields(
			"f.id", "f.product_id", "f.created_at",
			"p.name AS product_name", "p.main_image AS product_main_image",
			"p.price AS product_price", "p.status AS product_status",
		).
		LeftJoin("products p", "p.id = f.product_id")
}

// findByUserProduct 按 user_id + product_id 联查收藏条目，未命中返回 nil。
func (s *sFavorite) findByUserProduct(ctx context.Context, userID, productID int64) (*v1.FavoriteItem, error) {
	var rows []*favoriteRow
	if err := s.favoriteQuery(ctx).
		Where("f.user_id", userID).
		Where("f.product_id", productID).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询收藏条目: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return s.toFavoriteItem(rows[0]), nil
}

// toFavoriteItem 将联查行转换为对外条目并动态计算可用性。
func (s *sFavorite) toFavoriteItem(r *favoriteRow) *v1.FavoriteItem {
	item := &v1.FavoriteItem{
		Id:           r.Id,
		ProductId:    r.ProductId,
		CreatedAt:    r.CreatedAt,
		ProductPrice: r.ProductPrice,
	}
	if r.ProductName != nil {
		item.ProductName = *r.ProductName
	}
	if r.ProductMainImage != nil {
		item.ProductMainImage = *r.ProductMainImage
	}

	// 可用性：商品存在且 on_shelf → 可用；被删除 → product_deleted；其余（draft/off_shelf）→ off_shelf。
	switch {
	case r.ProductStatus == nil:
		item.Available = false
		item.UnavailableReason = reasonProductDeleted
	case *r.ProductStatus != dbProductOnShelf:
		item.Available = false
		item.UnavailableReason = reasonOffShelf
	default:
		item.Available = true
		item.UnavailableReason = ""
	}
	return item
}

// normalizePage 归一化分页参数：page/size 下限 1，size 上限 maxSize。
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = defaultPage
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发收藏同一 (user_id, product_id) 时，uk_user_product 唯一约束是最终兜底。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
