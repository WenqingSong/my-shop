package sku

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxNameLen SKU 名称最大字符数（trim 后）。
	maxNameLen = 128
	// maxPrice 价格上限（整数分，= ¥999,999.99）。
	maxPrice = 99_999_999

	// statusEnabled / statusDisabled SKU 启用/禁用状态（DB 存 TINYINT）。
	statusEnabled  = 1
	statusDisabled = 0
)

type sSku struct{}

func init() {
	service.RegisterSku(New())
}

// New 创建并返回 SKU 服务实现。
func New() *sSku {
	return &sSku{}
}

// sku 是 skus 表的一条记录。
type sku struct {
	Id        int64       `json:"id"`
	ProductId int64       `json:"product_id"`
	Name      string      `json:"name"`
	Price     int64       `json:"price"`
	Status    int         `json:"status"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// Create 校验后写入 SKU：product_id 必须指向存在商品（否则 4001），name 同商品唯一由
// 复合唯一约束兜底（撞名 5004），price 为整数分，status 默认 enabled。
func (s *sSku) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	price, err := parsePrice(req.Price)
	if err != nil {
		return nil, err
	}
	status := statusEnabled
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}

	exists, err := service.Product().Exists(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	id, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"product_id": req.ProductId,
		"name":       name,
		"price":      price,
		"status":     status,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSkuNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入 SKU: %w", err))
	}

	return s.loadCreateRes(ctx, id)
}

// Update 按提交字段更新 SKU 的 name/price/status（product_id 不可变）；
// 不存在返回 5001（404），撞名返回 5004（409），非法值在写入前拒绝且不改变原值。
func (s *sSku) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}

	name := old.Name
	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	price := old.Price
	if req.Price != nil {
		if price, err = parsePrice(*req.Price); err != nil {
			return nil, err
		}
	}
	status := old.Status
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}

	if _, err := g.DB().Model("skus").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{
			"name":   name,
			"price":  price,
			"status": status,
		}).Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSkuNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新 SKU: %w", err))
	}

	return s.loadUpdateRes(ctx, req.Id)
}

// Delete 物理删除 SKU：不存在/已删除返回 5001（404）；核对 RowsAffected 兜底并发删除。
// 删除不级联、不影响所属商品与同商品其他 SKU。
func (s *sSku) Delete(ctx context.Context, id int64) error {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return codes.New(codes.CodeSkuNotFound)
	}

	result, err := g.DB().Model("skus").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除 SKU: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeSkuNotFound)
	}
	return nil
}

// ListByProduct 按商品查询 SKU（供商品详情组合），按 id 升序；
// onlyEnabled=true 时仅返回 enabled SKU（前台可见性）。
func (s *sSku) ListByProduct(ctx context.Context, productID int64, onlyEnabled bool) ([]*v1.Sku, error) {
	model := g.DB().Model("skus").Ctx(ctx).Where("product_id", productID)
	if onlyEnabled {
		model = model.Where("status", statusEnabled)
	}
	var records []*sku
	if err := model.Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品 SKU: %w", err))
	}
	items := make([]*v1.Sku, 0, len(records))
	for _, r := range records {
		items = append(items, toSku(r))
	}
	return items, nil
}

// findOne 按 id 查询 SKU，不存在返回 nil。
func (s *sSku) findOne(ctx context.Context, id int64) (*sku, error) {
	var records []*sku
	if err := g.DB().Model("skus").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询 SKU: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// loadCreateRes 按 id 重新加载 SKU 并包装为创建响应。
func (s *sSku) loadCreateRes(ctx context.Context, id int64) (*v1.CreateRes, error) {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	return &v1.CreateRes{Sku: *toSku(rec)}, nil
}

// loadUpdateRes 按 id 重新加载 SKU 并包装为更新响应。
func (s *sSku) loadUpdateRes(ctx context.Context, id int64) (*v1.UpdateRes, error) {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	return &v1.UpdateRes{Sku: *toSku(rec)}, nil
}

// toSku 将内部记录转换为对外 SKU 结构。
func toSku(r *sku) *v1.Sku {
	return &v1.Sku{
		Id:        r.Id,
		ProductId: r.ProductId,
		Name:      r.Name,
		Price:     r.Price,
		Status:    statusToString(r.Status),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	if s == statusEnabled {
		return v1.StatusEnabled
	}
	return v1.StatusDisabled
}

// parseStatus 将 API 字符串状态枚举解析为 DB TINYINT，非法值返回 5003。
func parseStatus(status string) (int, error) {
	switch status {
	case v1.StatusEnabled:
		return statusEnabled, nil
	case v1.StatusDisabled:
		return statusDisabled, nil
	default:
		return 0, codes.New(codes.CodeSkuInvalidStatus)
	}
}

// parsePrice 校验价格为合法整数分（整数、非负、不超上限），非法返回 5002。
func parsePrice(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeSkuInvalidPrice)
	}
	if v < 0 || v > maxPrice {
		return 0, codes.New(codes.CodeSkuInvalidPrice)
	}
	return v, nil
}

// validateName trim 后校验非空与长度，返回规范化后的 SKU 名称。
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

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
