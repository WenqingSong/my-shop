// Package recommendation 实现「推荐位」业务逻辑：推荐位 CRUD + 推荐商品管理 + 前台公开查询。
// 事实来源为单一 MySQL（recommend_positions 主数据 + recommend_items 从属关系）；
// 商品可售性在前台查询时经 JOIN products.status 判定（products 为只读引用，不修改商品模块行为）。
// 无 Redis 写、无 MQ、无异步、无跨系统一致性。
package recommendation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/recommendation/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	statusEnabled  = 1 // 推荐位启用（公开，前台可查询）
	statusDisabled = 0 // 推荐位禁用（临时下线，前台不返回，后台可见）

	productStatusOnShelf = 1 // products.status=1 表示 on_shelf（前台仅返回可售商品）

	maxNameLen = 64 // 推荐位名称最大字符数（trim 后）
)

// codePattern 校验推荐位 code 格式：[a-z0-9][a-z0-9-]{0,63}（小写字母/数字/连字符，1~64）。
var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type sRecommendation struct{}

func init() {
	service.RegisterRecommendation(New())
}

// New 创建并返回推荐位服务实现。
func New() *sRecommendation {
	return &sRecommendation{}
}

// positionRow 是 recommend_positions 表的一条记录。
type positionRow struct {
	Id        int64       `json:"id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Status    int         `json:"status"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// itemRow 是 recommend_items 表的一条记录。
type itemRow struct {
	Id         int64       `json:"id"`
	PositionId int64       `json:"position_id"`
	ProductId  int64       `json:"product_id"`
	Sort       int         `json:"sort"`
	CreatedAt  *gtime.Time `json:"created_at"`
	UpdatedAt  *gtime.Time `json:"updated_at"`
}

// snapshotRow 是前台查询 JOIN products 后的商品快照行。
type snapshotRow struct {
	ProductId int64  `json:"product_id"`
	Name      string `json:"name"`
	MainImage string `json:"main_image"`
	Price     int64  `json:"price"`
	Sort      int    `json:"sort"`
}

// Frontend 返回单个推荐位的可售商品快照（仅启用位 + on_shelf 商品，按 sort,id 升序）。
// 推荐位不存在或禁用时返回空结果（不区分两者，避免向公开接口泄露内部状态）。
func (s *sRecommendation) Frontend(ctx context.Context, code string) (*v1.FrontendRes, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return &v1.FrontendRes{Code: "", Name: "", Items: []*v1.ItemSnapshot{}}, nil
	}

	var posRows []*positionRow
	if err := g.DB().Model("recommend_positions").Ctx(ctx).
		Where("code", code).
		Where("status", statusEnabled).
		Scan(&posRows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐位: %w", err))
	}
	if len(posRows) == 0 {
		return &v1.FrontendRes{Code: "", Name: "", Items: []*v1.ItemSnapshot{}}, nil
	}
	pos := posRows[0]

	var rows []*snapshotRow
	if err := g.DB().Model("recommend_items ri").Ctx(ctx).
		InnerJoin("products p", "p.id = ri.product_id").
		Where("ri.position_id", pos.Id).
		Where("p.status", productStatusOnShelf).
		Fields("ri.product_id AS product_id, p.name AS name, p.main_image AS main_image, p.price AS price, ri.sort AS sort").
		Order("ri.sort", "ri.id").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐商品快照: %w", err))
	}

	items := make([]*v1.ItemSnapshot, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.ItemSnapshot{
			ProductId: r.ProductId,
			Name:      r.Name,
			MainImage: r.MainImage,
			Price:     r.Price,
			Sort:      r.Sort,
		})
	}
	return &v1.FrontendRes{Code: pos.Code, Name: pos.Name, Items: items}, nil
}

// AdminList 返回后台全部推荐位列表（全部状态，按 id 升序）。
func (s *sRecommendation) AdminList(ctx context.Context) (*v1.AdminListRes, error) {
	var rows []*positionRow
	if err := g.DB().Model("recommend_positions").Ctx(ctx).Order("id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐位列表: %w", err))
	}
	items := make([]*v1.Position, 0, len(rows))
	for _, r := range rows {
		items = append(items, toPosition(r))
	}
	return &v1.AdminListRes{Items: items}, nil
}

// AdminDetail 返回单个推荐位完整字段与推荐商品关系；不存在返回 15001（404）。
func (s *sRecommendation) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeRecommendPositionNotFound)
	}
	itemRows, err := s.findItems(ctx, id)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Item, 0, len(itemRows))
	for _, r := range itemRows {
		items = append(items, toItem(r))
	}
	return &v1.AdminDetailRes{Position: *toPosition(row), Items: items}, nil
}

// Create 校验并持久化新推荐位：单条 INSERT；code 冲突（1062 on uk_code）→ 15002（409）。
func (s *sRecommendation) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	code, err := validateCode(req.Code)
	if err != nil {
		return nil, err
	}
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	status := statusEnabled
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}

	id, err := g.DB().Model("recommend_positions").Ctx(ctx).Data(g.Map{
		"code":   code,
		"name":   name,
		"status": status,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeRecommendPositionCodeExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐位: %w", err))
	}

	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐位后未找到记录"))
	}
	return &v1.CreateRes{Position: *toPosition(row)}, nil
}

// Update 按提交字段更新推荐位（name/status，code 不可变）；不存在返回 15001。
// 存在性由更新前的 findOne 判断，不依据 RowsAffected（避免把「值无变化的幂等更新」误判为不存在）。
func (s *sRecommendation) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeRecommendPositionNotFound)
	}

	data := g.Map{}
	if req.Name != nil {
		name, err := validateName(*req.Name)
		if err != nil {
			return nil, err
		}
		data["name"] = name
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		data["status"] = *req.Status
	}

	// 未提交任何字段：视为幂等成功，返回当前值。
	if len(data) == 0 {
		return &v1.UpdateRes{Position: *toPosition(old)}, nil
	}

	if _, err := g.DB().Model("recommend_positions").Ctx(ctx).Where("id", req.Id).Data(data).Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新推荐位: %w", err))
	}

	row, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeRecommendPositionNotFound)
	}
	return &v1.UpdateRes{Position: *toPosition(row)}, nil
}

// Delete 物理删除推荐位：核对 RowsAffected（=0 → 15001）；级联删除推荐商品关系由 FK CASCADE 保证。
func (s *sRecommendation) Delete(ctx context.Context, id int64) error {
	result, err := g.DB().Model("recommend_positions").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除推荐位: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeRecommendPositionNotFound)
	}
	return nil
}

// AddItem 向推荐位添加商品：仅校验「商品存在」（draft/on_shelf/off_shelf 均可加入），
// 不存在复用 4001；重复（1062 on uk_position_product）→ 15005；推荐位不存在 → 15001。
func (s *sRecommendation) AddItem(ctx context.Context, req *v1.AddItemReq) (*v1.AddItemRes, error) {
	if req.ProductId <= 0 {
		return nil, codes.New(codes.CodeRecommendInvalidInput)
	}
	pos, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if pos == nil {
		return nil, codes.New(codes.CodeRecommendPositionNotFound)
	}
	ok, err := service.Product().Exists(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	sort := 0
	if req.Sort != nil {
		sort = *req.Sort
	}

	id, err := g.DB().Model("recommend_items").Ctx(ctx).Data(g.Map{
		"position_id": req.Id,
		"product_id":  req.ProductId,
		"sort":        sort,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeRecommendItemDuplicate)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐商品关系: %w", err))
	}

	row, err := s.findItemByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐商品关系后未找到记录"))
	}
	return &v1.AddItemRes{Item: *toItem(row)}, nil
}

// RemoveItem 移除推荐商品关系：条件删除 + 核对 RowsAffected（=0 → 15004）。
func (s *sRecommendation) RemoveItem(ctx context.Context, req *v1.RemoveItemReq) error {
	if req.ProductId <= 0 {
		return codes.New(codes.CodeRecommendInvalidInput)
	}
	result, err := g.DB().Model("recommend_items").Ctx(ctx).
		Where("position_id", req.Id).
		Where("product_id", req.ProductId).
		Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("移除推荐商品关系: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeRecommendItemNotFound)
	}
	return nil
}

// UpdateSort 按给定商品顺序调整排序：product_ids 的顺序即目标顺序（sort=下标）。
// 推荐位不存在 → 15001；列表非法（空/重复/非法 id）→ 15003；含未加入该推荐位的商品 → 15004；
// 未覆盖全部已加入商品（缺漏）→ 15006。
func (s *sRecommendation) UpdateSort(ctx context.Context, req *v1.UpdateSortReq) (*v1.UpdateSortRes, error) {
	pos, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if pos == nil {
		return nil, codes.New(codes.CodeRecommendPositionNotFound)
	}
	if len(req.ProductIds) == 0 {
		return nil, codes.New(codes.CodeRecommendInvalidInput)
	}
	seen := make(map[int64]bool, len(req.ProductIds))
	for _, pid := range req.ProductIds {
		if pid <= 0 {
			return nil, codes.New(codes.CodeRecommendInvalidInput)
		}
		if seen[pid] {
			return nil, codes.New(codes.CodeRecommendInvalidInput)
		}
		seen[pid] = true
	}

	// 先加载现有关系，校验每个 product_id 均已加入该推荐位（避免依赖 RowsAffected 判断存在性，
	// 否则「sort 无变化」的幂等更新会被误判为关系不存在）。
	existing, err := s.findItems(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	existingSet := make(map[int64]bool, len(existing))
	for _, r := range existing {
		existingSet[r.ProductId] = true
	}
	for _, pid := range req.ProductIds {
		if !existingSet[pid] {
			return nil, codes.New(codes.CodeRecommendItemNotFound)
		}
	}
	// 校验列表覆盖全部已加入商品：缺漏任一已加入商品 → 15006，避免部分更新导致排序语义不完整。
	for _, r := range existing {
		if !seen[r.ProductId] {
			return nil, codes.New(codes.CodeRecommendItemSortMismatch)
		}
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		for i, pid := range req.ProductIds {
			if _, e := tx.Model("recommend_items").Ctx(ctx).
				Where("position_id", req.Id).
				Where("product_id", pid).
				Data(g.Map{"sort": i}).
				Update(); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新推荐商品排序: %w", e))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	itemRows, err := s.findItems(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Item, 0, len(itemRows))
	for _, r := range itemRows {
		items = append(items, toItem(r))
	}
	return &v1.UpdateSortRes{Items: items}, nil
}

// findOne 按 id 查询单条推荐位，未命中返回 nil。
func (s *sRecommendation) findOne(ctx context.Context, id int64) (*positionRow, error) {
	var rows []*positionRow
	if err := g.DB().Model("recommend_positions").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询推荐位: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findItems 查询推荐位全部推荐商品关系（按 sort,id 升序）。
func (s *sRecommendation) findItems(ctx context.Context, positionID int64) ([]*itemRow, error) {
	var rows []*itemRow
	if err := g.DB().Model("recommend_items").Ctx(ctx).Where("position_id", positionID).Order("sort", "id").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐商品关系: %w", err))
	}
	return rows, nil
}

// findItemByID 按 id 查询单条推荐商品关系，未命中返回 nil。
func (s *sRecommendation) findItemByID(ctx context.Context, id int64) (*itemRow, error) {
	var rows []*itemRow
	if err := g.DB().Model("recommend_items").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询推荐商品关系: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// toPosition 将推荐位行转换为对外结构。
func toPosition(r *positionRow) *v1.Position {
	return &v1.Position{
		Id:        r.Id,
		Code:      r.Code,
		Name:      r.Name,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// toItem 将推荐商品关系行转换为对外结构。
func toItem(r *itemRow) *v1.Item {
	return &v1.Item{
		Id:         r.Id,
		PositionId: r.PositionId,
		ProductId:  r.ProductId,
		Sort:       r.Sort,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// validateCode trim 后校验 code 格式（[a-z0-9][a-z0-9-]{0,63}），返回规范化 code。
func validateCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	if !codePattern.MatchString(code) {
		return "", codes.New(codes.CodeRecommendInvalidInput)
	}
	return code, nil
}

// validateName trim 后校验非空与长度，返回规范化名称。
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeRecommendInvalidInput)
	}
	return name, nil
}

// validateStatus 校验状态枚举值（1 启用 / 0 禁用）。
func validateStatus(status int) error {
	if status != statusEnabled && status != statusDisabled {
		return codes.New(codes.CodeRecommendInvalidInput)
	}
	return nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发写入同一唯一键（uk_code / uk_position_product）时，DB 唯一约束是最终兜底。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
