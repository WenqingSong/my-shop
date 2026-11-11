// Package spec 实现商品规格（规格组/规格值）业务逻辑。
//
// 规格组与规格值为商品级主数据：不承担库存与价格事实，也不定义全局规格字典。
// 组合指纹（skus.spec_signature）由 SKU 域在生成组合时按同一算法计算，
// 本包只维护规格定义本身。
package spec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/spec/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxNameLen 规格组名最大字符数（与 product_specs.name VARCHAR(32) 一致）。
	maxNameLen = 32
	// maxValueLen 规格值最大字符数（与 product_spec_values.value VARCHAR(32) 一致）。
	maxValueLen = 32
)

type sSpec struct{}

func init() {
	service.RegisterSpec(New())
}

// New 创建并返回商品规格服务实现。
func New() *sSpec {
	return &sSpec{}
}

// specGroup 是 product_specs 表的一条记录。
type specGroup struct {
	Id        int64 `json:"id"`
	ProductId int64 `json:"product_id"`
	Name      string
	Sort      int
}

// specValue 是 product_spec_values 表的一条记录。
type specValue struct {
	Id     int64 `json:"id"`
	SpecId int64 `json:"spec_id"`
	Value  string
	Sort   int
}

// ListByProduct 查询商品全部规格组与规格值，按 sort、id 升序（同 sort 保持稳定）。
func (s *sSpec) ListByProduct(ctx context.Context, productID int64) ([]*v1.SpecGroup, error) {
	var groups []*specGroup
	if err := g.DB().Model("product_specs").Ctx(ctx).
		Where("product_id", productID).
		Order("sort", "id").
		Scan(&groups); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品规格组: %w", err))
	}

	result := make([]*v1.SpecGroup, 0, len(groups))
	if len(groups) == 0 {
		return result, nil
	}

	ids := make([]int64, 0, len(groups))
	for _, gp := range groups {
		ids = append(ids, gp.Id)
	}
	var values []*specValue
	if err := g.DB().Model("product_spec_values").Ctx(ctx).
		WhereIn("spec_id", ids).
		Order("sort", "id").
		Scan(&values); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品规格值: %w", err))
	}
	byGroup := make(map[int64][]*v1.SpecValue, len(groups))
	for _, v := range values {
		byGroup[v.SpecId] = append(byGroup[v.SpecId], toSpecValue(v))
	}
	for _, gp := range groups {
		vals := byGroup[gp.Id]
		if vals == nil {
			vals = []*v1.SpecValue{}
		}
		result = append(result, &v1.SpecGroup{
			Id:        gp.Id,
			ProductId: gp.ProductId,
			Name:      gp.Name,
			Sort:      gp.Sort,
			Values:    vals,
		})
	}
	return result, nil
}

// CreateGroup 创建规格组：商品必须存在（否则 4001），组名 trim 后 1..32 字符，
// 同商品重名返回 5011（DB 唯一约束兜底并发）。
func (s *sSpec) CreateGroup(ctx context.Context, req *v1.CreateGroupReq) (*v1.CreateGroupRes, error) {
	if req.ProductId <= 0 {
		return nil, codes.New(codes.CodeProductNotFound)
	}
	name, err := validateText(req.Name, maxNameLen)
	if err != nil {
		return nil, err
	}
	exists, err := service.Product().Exists(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	id, err := g.DB().Model("product_specs").Ctx(ctx).Data(g.Map{
		"product_id": req.ProductId,
		"name":       name,
		"sort":       req.Sort,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSpecNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入规格组: %w", err))
	}

	group, err := s.loadGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.CreateGroupRes{SpecGroup: *group}, nil
}

// UpdateGroup 更新规格组：不存在返回 5010（404），同商品重名返回 5011，商品归属不可变。
func (s *sSpec) UpdateGroup(ctx context.Context, req *v1.UpdateGroupReq) (*v1.UpdateGroupRes, error) {
	old, err := s.findGroup(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}

	name := old.Name
	if req.Name != nil {
		if name, err = validateText(*req.Name, maxNameLen); err != nil {
			return nil, err
		}
	}
	sort := old.Sort
	if req.Sort != nil {
		sort = *req.Sort
	}

	if _, err := g.DB().Model("product_specs").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{"name": name, "sort": sort}).
		Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSpecNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新规格组: %w", err))
	}

	group, err := s.loadGroup(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateGroupRes{SpecGroup: *group}, nil
}

// DeleteGroup 删除规格组：不存在返回 5010；其规格值被 SKU 引用时由 RESTRICT 兜底返回 5013。
func (s *sSpec) DeleteGroup(ctx context.Context, id int64) error {
	group, err := s.findGroup(ctx, id)
	if err != nil {
		return err
	}
	if group == nil {
		return codes.New(codes.CodeSpecNotFound)
	}
	if _, err := g.DB().Model("product_specs").Ctx(ctx).Where("id", id).Delete(); err != nil {
		if isForeignKeyError(err) {
			return codes.New(codes.CodeSpecValueInUse)
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除规格组: %w", err))
	}
	return nil
}

// CreateValue 在指定规格组下创建规格值：规格组必须存在（否则 5010），同组值唯一（5012）。
func (s *sSpec) CreateValue(ctx context.Context, req *v1.CreateValueReq) (*v1.CreateValueRes, error) {
	group, err := s.findGroup(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}
	value, err := validateText(req.Value, maxValueLen)
	if err != nil {
		return nil, err
	}

	id, err := g.DB().Model("product_spec_values").Ctx(ctx).Data(g.Map{
		"spec_id": group.Id,
		"value":   value,
		"sort":    req.Sort,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSpecValueExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入规格值: %w", err))
	}

	rec, err := s.findValue(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}
	return &v1.CreateValueRes{SpecValue: *toSpecValue(rec)}, nil
}

// UpdateValue 更新规格值：不存在返回 5010，同组重名返回 5012，规格组归属不可变。
func (s *sSpec) UpdateValue(ctx context.Context, req *v1.UpdateValueReq) (*v1.UpdateValueRes, error) {
	old, err := s.findValue(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}

	value := old.Value
	if req.Value != nil {
		if value, err = validateText(*req.Value, maxValueLen); err != nil {
			return nil, err
		}
	}
	sort := old.Sort
	if req.Sort != nil {
		sort = *req.Sort
	}

	if _, err := g.DB().Model("product_spec_values").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{"value": value, "sort": sort}).
		Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSpecValueExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新规格值: %w", err))
	}

	rec, err := s.findValue(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}
	return &v1.UpdateValueRes{SpecValue: *toSpecValue(rec)}, nil
}

// DeleteValue 删除规格值：不存在返回 5010；被 SKU 引用时由 RESTRICT 兜底返回 5013。
func (s *sSpec) DeleteValue(ctx context.Context, id int64) error {
	rec, err := s.findValue(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return codes.New(codes.CodeSpecNotFound)
	}
	if _, err := g.DB().Model("product_spec_values").Ctx(ctx).Where("id", id).Delete(); err != nil {
		if isForeignKeyError(err) {
			return codes.New(codes.CodeSpecValueInUse)
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除规格值: %w", err))
	}
	return nil
}

// loadGroup 按 id 加载规格组（含规格值），不存在返回 5010。
func (s *sSpec) loadGroup(ctx context.Context, id int64) (*v1.SpecGroup, error) {
	group, err := s.findGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, codes.New(codes.CodeSpecNotFound)
	}
	var values []*specValue
	if err := g.DB().Model("product_spec_values").Ctx(ctx).
		Where("spec_id", id).
		Order("sort", "id").
		Scan(&values); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询规格组规格值: %w", err))
	}
	items := make([]*v1.SpecValue, 0, len(values))
	for _, v := range values {
		items = append(items, toSpecValue(v))
	}
	return &v1.SpecGroup{
		Id:        group.Id,
		ProductId: group.ProductId,
		Name:      group.Name,
		Sort:      group.Sort,
		Values:    items,
	}, nil
}

// findGroup 按 id 查询规格组，不存在返回 nil。
func (s *sSpec) findGroup(ctx context.Context, id int64) (*specGroup, error) {
	var records []*specGroup
	if err := g.DB().Model("product_specs").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询规格组: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// findValue 按 id 查询规格值，不存在返回 nil。
func (s *sSpec) findValue(ctx context.Context, id int64) (*specValue, error) {
	var records []*specValue
	if err := g.DB().Model("product_spec_values").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询规格值: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// toSpecValue 将内部记录转换为对外结构。
func toSpecValue(r *specValue) *v1.SpecValue {
	return &v1.SpecValue{
		Id:     r.Id,
		SpecId: r.SpecId,
		Value:  r.Value,
		Sort:   r.Sort,
	}
}

// validateText trim 后校验非空与字符数上限，返回规范化文本。
func validateText(text string, maxLen int) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(text) > maxLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return text, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// isForeignKeyError 判断是否为 MySQL 外键约束失败（1451，ON DELETE RESTRICT）。
func isForeignKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1451
}
