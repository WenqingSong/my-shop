package product

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// 商品状态（DB 存 TINYINT）。
	statusDraft    = 0
	statusOnShelf  = 1
	statusOffShelf = 2

	// maxNameLen 商品名最大字符数（trim 后）。
	maxNameLen = 128
	// maxBrandLen 品牌最大字符数。
	maxBrandLen = 64
	// maxImageURLLen 主图 URL 最大字符数。
	maxImageURLLen = 512
	// maxPrice 价格上限（整数分，= ¥999,999.99）。
	maxPrice = 99_999_999

	// 分页默认值与上限。
	defaultPage = 1
	defaultSize = 20
	maxSize     = 100

	// 默认排序字段（非法字段回落）。
	defaultSortField = "created_at"
)

// sortColumns 排序字段白名单：前端字段 → 数据库列，禁止拼接前端字段进 SQL。
var sortColumns = map[string]string{
	"id":         "id",
	"price":      "price",
	"created_at": "created_at",
	"updated_at": "updated_at",
}

type sProduct struct{}

func init() {
	service.RegisterProduct(New())
}

// New 创建并返回商品 SPU 服务实现。
func New() *sProduct {
	return &sProduct{}
}

// product 是 products 表的一条记录。
type product struct {
	Id         int64       `json:"id"`
	Name       string      `json:"name"`
	Brand      string      `json:"brand"`
	CategoryId int64       `json:"category_id"`
	Price      int64       `json:"price"`
	MainImage  string      `json:"main_image"`
	Detail     *string     `json:"detail"`
	Status     int         `json:"status"`
	CreatedAt  *gtime.Time `json:"created_at"`
	UpdatedAt  *gtime.Time `json:"updated_at"`
}

// productImage 是 product_images 表的一条记录。
type productImage struct {
	ProductId int64 `json:"product_id"`
	Url       string
	Sort      int
}

// List 前台列表：仅 on_shelf。
func (s *sProduct) List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error) {
	return s.queryList(ctx, true, req.Page, req.Size, req.CategoryId, req.Keyword, req.Sort, req.Order)
}

// AdminList 后台列表：全部状态。
func (s *sProduct) AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error) {
	res, err := s.queryList(ctx, false, req.Page, req.Size, req.CategoryId, req.Keyword, req.Sort, req.Order)
	if err != nil {
		return nil, err
	}
	return &v1.AdminListRes{Items: res.Items, Total: res.Total, Page: res.Page, Size: res.Size}, nil
}

// Detail 前台详情：仅 on_shelf 可见。
func (s *sProduct) Detail(ctx context.Context, id int64) (*v1.DetailRes, error) {
	p, err := s.load(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return &v1.DetailRes{Product: *p}, nil
}

// AdminDetail 后台详情：全部状态。
func (s *sProduct) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	p, err := s.load(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return &v1.AdminDetailRes{Product: *p}, nil
}

// Create 创建商品：校验后事务写入 products 与 product_images，status 强制 draft。
func (s *sProduct) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	brand, err := validateBrand(req.Brand)
	if err != nil {
		return nil, err
	}
	mainImage, err := validateMainImage(req.MainImage)
	if err != nil {
		return nil, err
	}
	price, err := parsePrice(req.Price)
	if err != nil {
		return nil, err
	}
	if err := s.validateCategory(ctx, req.CategoryId); err != nil {
		return nil, err
	}
	images, err := validateImages(req.Images)
	if err != nil {
		return nil, err
	}

	var id int64
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		pid, e := tx.Model("products").Ctx(ctx).Data(g.Map{
			"name":        name,
			"brand":       brand,
			"category_id": req.CategoryId,
			"price":       price,
			"main_image":  mainImage,
			"detail":      req.Detail,
			"status":      statusDraft,
		}).InsertAndGetId()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入商品: %w", e))
		}
		if e := insertImages(ctx, tx, pid, images); e != nil {
			return e
		}
		id = pid
		return nil
	})
	if err != nil {
		return nil, err
	}

	p, err := s.load(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Product: *p}, nil
}

// Update 更新商品：普通更新不能改变 status（合法 status 忽略、非法值拒绝 4006）；
// images 提供则全量替换、未提供则保留。
func (s *sProduct) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	// 普通 update 提交 status：合法值被忽略（满足 AC-005），非法值拒绝。
	if req.Status != nil {
		if !isValidStatusString(*req.Status) {
			return nil, codes.New(codes.CodeProductInvalidStatus)
		}
	}

	name := old.Name
	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	brand := old.Brand
	if req.Brand != nil {
		if brand, err = validateBrand(*req.Brand); err != nil {
			return nil, err
		}
	}
	mainImage := old.MainImage
	if req.MainImage != nil {
		if mainImage, err = validateMainImage(*req.MainImage); err != nil {
			return nil, err
		}
	}
	detail := productDetail(old)
	if req.Detail != nil {
		detail = *req.Detail
	}
	categoryID := old.CategoryId
	if req.CategoryId != nil {
		if err := s.validateCategory(ctx, *req.CategoryId); err != nil {
			return nil, err
		}
		categoryID = *req.CategoryId
	}
	price := old.Price
	if req.Price != nil {
		if price, err = parsePrice(*req.Price); err != nil {
			return nil, err
		}
	}

	var (
		images         []string
		needReplaceImg bool
	)
	if req.Images != nil {
		if images, err = validateImages(*req.Images); err != nil {
			return nil, err
		}
		needReplaceImg = true
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := tx.Model("products").Ctx(ctx).Where("id", req.Id).Data(g.Map{
			"name":        name,
			"brand":       brand,
			"category_id": categoryID,
			"price":       price,
			"main_image":  mainImage,
			"detail":      detail,
		}).Update(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新商品: %w", e))
		}
		if needReplaceImg {
			if e := replaceProductImages(ctx, tx, req.Id, images); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	p, err := s.load(ctx, req.Id, false)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateRes{Product: *p}, nil
}

// OnShelf 上架：合法迁移 draft→on_shelf、off_shelf→on_shelf。
func (s *sProduct) OnShelf(ctx context.Context, id int64) (*v1.OnShelfRes, error) {
	p, err := s.transition(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return &v1.OnShelfRes{Product: *p}, nil
}

// OffShelf 下架：合法迁移 on_shelf→off_shelf。
func (s *sProduct) OffShelf(ctx context.Context, id int64) (*v1.OffShelfRes, error) {
	p, err := s.transition(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return &v1.OffShelfRes{Product: *p}, nil
}

// CountByCategory 统计分类下商品数量（供分类删除保护）。
func (s *sProduct) CountByCategory(ctx context.Context, categoryID int64) (int64, error) {
	n, err := g.DB().Model("products").Ctx(ctx).Where("category_id", categoryID).Count()
	if err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计分类下商品: %w", err))
	}
	return int64(n), nil
}

// transition 执行状态迁移：先 SELECT 判定存在（404），再条件 UPDATE + RowsAffected 判定（409），
// 保证并发重复上下架最多一次成功。上架前重新校验分类（存在 + 叶子 + enabled）。
func (s *sProduct) transition(ctx context.Context, id int64, onShelf bool) (*v1.Product, error) {
	old, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	// 上架前校验分类最新有效性（含「已禁用/已变非叶子」的再次上架拒绝）。
	if onShelf {
		if err := s.validateCategory(ctx, old.CategoryId); err != nil {
			return nil, err
		}
	}

	var affected int64
	if onShelf {
		result, e := g.DB().Model("products").Ctx(ctx).
			Where("id", id).
			WhereIn("status", []int{statusDraft, statusOffShelf}).
			Data(g.Map{"status": statusOnShelf}).Update()
		if e != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新商品状态: %w", e))
		}
		affected, _ = result.RowsAffected()
	} else {
		result, e := g.DB().Model("products").Ctx(ctx).
			Where("id", id).
			Where("status", statusOnShelf).
			Data(g.Map{"status": statusOffShelf}).Update()
		if e != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新商品状态: %w", e))
		}
		affected, _ = result.RowsAffected()
	}
	if affected == 0 {
		return nil, codes.New(codes.CodeProductInvalidStatusTransition)
	}

	return s.load(ctx, id, false)
}

// queryList 构建列表查询：分页/分类筛选/关键词（LIKE 转义）/排序白名单。
func (s *sProduct) queryList(
	ctx context.Context,
	onlyOnShelf bool,
	page, size int,
	categoryID *int64,
	keyword, sortField, order string,
) (*v1.ListRes, error) {
	page, size = normalizePage(page, size)

	model := g.DB().Model("products").Ctx(ctx)
	if onlyOnShelf {
		model = model.Where("status", statusOnShelf)
	}
	if categoryID != nil {
		model = model.Where("category_id", *categoryID)
	}
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		pattern := "%" + escapeLikeKeyword(keyword) + "%"
		model = model.Where(`(name LIKE ? ESCAPE '\\' OR brand LIKE ? ESCAPE '\\')`, pattern, pattern)
	}

	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计商品: %w", err))
	}

	col, ok := sortColumns[sortField]
	if !ok {
		col = defaultSortField
	}
	direction := "DESC"
	if strings.EqualFold(order, "asc") {
		direction = "ASC"
	}

	var records []*product
	if err := model.Order(col+" "+direction).Order("id DESC").Page(page, size).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品列表: %w", err))
	}

	items, err := s.toProducts(ctx, records)
	if err != nil {
		return nil, err
	}
	return &v1.ListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// load 按 id 加载商品（含图片），onlyOnShelf=true 时强制 on_shelf，否则 404。
func (s *sProduct) load(ctx context.Context, id int64, onlyOnShelf bool) (*v1.Product, error) {
	model := g.DB().Model("products").Ctx(ctx).Where("id", id)
	if onlyOnShelf {
		model = model.Where("status", statusOnShelf)
	}
	var records []*product
	if err := model.Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品: %w", err))
	}
	if len(records) == 0 {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	images, err := s.loadImages(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	return s.toProduct(records[0], images[id]), nil
}

// findOne 按 id 查询商品，不存在返回 nil。
func (s *sProduct) findOne(ctx context.Context, id int64) (*product, error) {
	var records []*product
	if err := g.DB().Model("products").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询商品: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// toProducts 批量转换为对外结构并批量加载图片（避免 N+1）。
func (s *sProduct) toProducts(ctx context.Context, records []*product) ([]*v1.Product, error) {
	ids := make([]int64, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.Id)
	}
	images, err := s.loadImages(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Product, 0, len(records))
	for _, r := range records {
		items = append(items, s.toProduct(r, images[r.Id]))
	}
	return items, nil
}

// toProduct 将内部记录转换为对外商品结构。
func (s *sProduct) toProduct(r *product, images []string) *v1.Product {
	if images == nil {
		images = []string{}
	}
	return &v1.Product{
		Id:         r.Id,
		Name:       r.Name,
		Brand:      r.Brand,
		CategoryId: r.CategoryId,
		Price:      r.Price,
		MainImage:  r.MainImage,
		Detail:     productDetail(r),
		Status:     statusToString(r.Status),
		Images:     images,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// loadImages 批量加载商品图片（按 sort 升序、sort 相同按 id 升序）。
func (s *sProduct) loadImages(ctx context.Context, productIDs []int64) (map[int64][]string, error) {
	result := make(map[int64][]string, len(productIDs))
	if len(productIDs) == 0 {
		return result, nil
	}
	var imgs []*productImage
	if err := g.DB().Model("product_images").Ctx(ctx).
		WhereIn("product_id", productIDs).
		Order("product_id", "sort", "id").
		Scan(&imgs); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品图片: %w", err))
	}
	for _, img := range imgs {
		result[img.ProductId] = append(result[img.ProductId], img.Url)
	}
	return result, nil
}

// validateCategory 校验分类（存在 + 叶子 + enabled），任一不满足即拒绝且无写入。
func (s *sProduct) validateCategory(ctx context.Context, categoryID int64) error {
	exists, err := service.Category().Exists(ctx, categoryID)
	if err != nil {
		return err
	}
	if !exists {
		return codes.New(codes.CodeProductInvalidCategory)
	}
	hasChildren, err := service.Category().HasChildren(ctx, categoryID)
	if err != nil {
		return err
	}
	if hasChildren {
		return codes.New(codes.CodeProductCategoryNotLeaf)
	}
	enabled, err := service.Category().IsEnabled(ctx, categoryID)
	if err != nil {
		return err
	}
	if !enabled {
		return codes.New(codes.CodeProductCategoryDisabled)
	}
	return nil
}

// insertImages 在事务内写入商品图片（sort 为下标）。
func insertImages(ctx context.Context, tx gdb.TX, productID int64, images []string) error {
	for i, url := range images {
		if _, err := tx.Model("product_images").Ctx(ctx).Data(g.Map{
			"product_id": productID,
			"url":        url,
			"sort":       i,
		}).Insert(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入商品图片: %w", err))
		}
	}
	return nil
}

// replaceProductImages 在事务内全量替换商品图片：先删除旧行，再按新列表重插。
func replaceProductImages(ctx context.Context, tx gdb.TX, productID int64, images []string) error {
	if _, err := tx.Model("product_images").Ctx(ctx).Where("product_id", productID).Delete(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除旧商品图片: %w", err))
	}
	return insertImages(ctx, tx, productID, images)
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

// escapeLikeKeyword 转义 LIKE 特殊字符（\ % _），使 keyword 按普通文本匹配。
func escapeLikeKeyword(kw string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(kw)
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	switch s {
	case statusOnShelf:
		return v1.StatusOnShelf
	case statusOffShelf:
		return v1.StatusOffShelf
	default:
		return v1.StatusDraft
	}
}

// isValidStatusString 判断是否为合法状态字符串枚举。
func isValidStatusString(s string) bool {
	switch s {
	case v1.StatusDraft, v1.StatusOnShelf, v1.StatusOffShelf:
		return true
	default:
		return false
	}
}

// productDetail 返回商品详情文本，DB 为 NULL 时返回空字符串。
func productDetail(r *product) string {
	if r.Detail == nil {
		return ""
	}
	return *r.Detail
}

// parsePrice 校验价格为合法整数分（整数、非负、不超上限）。
func parsePrice(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeProductInvalidPrice)
	}
	if v < 0 || v > maxPrice {
		return 0, codes.New(codes.CodeProductInvalidPrice)
	}
	return v, nil
}

// validateName trim 后校验非空与长度，返回规范化后的商品名。
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return name, nil
}

// validateBrand trim 后校验品牌长度。
func validateBrand(brand string) (string, error) {
	brand = strings.TrimSpace(brand)
	if utf8.RuneCountInString(brand) > maxBrandLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return brand, nil
}

// validateMainImage trim 后校验主图 URL 长度。
func validateMainImage(mainImage string) (string, error) {
	mainImage = strings.TrimSpace(mainImage)
	if utf8.RuneCountInString(mainImage) > maxImageURLLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return mainImage, nil
}

// validateImages 校验图片 URL 非空（trim 后），返回规范化列表。
func validateImages(images []string) ([]string, error) {
	result := make([]string, 0, len(images))
	for _, url := range images {
		url = strings.TrimSpace(url)
		if url == "" {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		result = append(result, url)
	}
	return result, nil
}
