package categories

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/categories/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxLevel 分类最大层级（顶级为 1）。
	maxLevel = 3
	// maxNameLen 分类名最大字符数（trim 后校验）。
	maxNameLen = 64
	// statusEnabled / statusDisabled 分类启用/禁用状态。
	statusEnabled  = 1
	statusDisabled = 0
)

type sCategory struct{}

func init() {
	service.RegisterCategory(New())
}

// New 创建并返回商品分类服务实现。
func New() *sCategory {
	return &sCategory{}
}

// category 是 categories 表的一条记录。
type category struct {
	Id        int64       `json:"id"`
	ParentId  int64       `json:"parent_id"`
	Name      string      `json:"name"`
	Sort      int         `json:"sort"`
	Status    int         `json:"status"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// List 返回仅含启用项的树形分类：一次查全表，内存按 parent_id 组装嵌套树，
// 同级按 sort 升序、sort 相同按 id 升序。禁用父分类时其子树整体不再展示。
func (s *sCategory) List(ctx context.Context) (*v1.ListRes, error) {
	records, err := s.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.ListRes{Items: buildTree(records)}, nil
}

// Detail 返回单个分类完整字段；不存在返回 3001（404）。
func (s *sCategory) Detail(ctx context.Context, id int64) (*v1.DetailRes, error) {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeCategoryNotFound)
	}
	return &v1.DetailRes{Category: toCategory(rec)}, nil
}

// Create 校验并持久化新分类：同级重名仅依赖 DB 复合唯一键 (parent_id, name) 判定。
func (s *sCategory) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
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

	parentOf, err := s.loadParentMap(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateParent(parentOf, req.ParentId, 0); err != nil {
		return nil, err
	}

	id, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{
		"parent_id": req.ParentId,
		"name":      name,
		"sort":      req.Sort,
		"status":    status,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeCategoryNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入分类: %w", err))
	}

	return &v1.CreateRes{Id: id}, nil
}

// Update 按提交字段更新分类：改名/改父/改排序/改状态；改父时校验循环引用与层级上限。
func (s *sCategory) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeCategoryNotFound)
	}

	name := old.Name
	parentID := old.ParentId
	sortVal := old.Sort
	status := old.Status

	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	if req.Sort != nil {
		sortVal = *req.Sort
	}

	parentChanged := false
	if req.ParentId != nil && *req.ParentId != old.ParentId {
		parentChanged = true
		parentID = *req.ParentId
	}
	if parentChanged {
		parentOf, err := s.loadParentMap(ctx)
		if err != nil {
			return nil, err
		}
		if err := validateParent(parentOf, parentID, req.Id); err != nil {
			return nil, err
		}
	}

	if _, err := g.DB().Model("categories").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{
			"parent_id": parentID,
			"name":      name,
			"sort":      sortVal,
			"status":    status,
		}).
		Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeCategoryNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新分类: %w", err))
	}

	updated, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, codes.New(codes.CodeCategoryNotFound)
	}
	return &v1.UpdateRes{Category: toCategory(updated)}, nil
}

// Delete 物理删除无子分类的分类；有子分类返回 3003（409）。
func (s *sCategory) Delete(ctx context.Context, id int64) error {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return codes.New(codes.CodeCategoryNotFound)
	}

	childCount, err := g.DB().Model("categories").Ctx(ctx).Where("parent_id", id).Count()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计子分类: %w", err))
	}
	if childCount > 0 {
		return codes.New(codes.CodeCategoryHasChildren)
	}

	result, err := g.DB().Model("categories").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除分类: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeCategoryNotFound)
	}
	return nil
}

// loadAll 一次查询全表，按 parent_id, sort, id 排序。
func (s *sCategory) loadAll(ctx context.Context) ([]*category, error) {
	var records []*category
	if err := g.DB().Model("categories").Ctx(ctx).
		Order("parent_id", "sort", "id").
		Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询分类列表: %w", err))
	}
	return records, nil
}

// findOne 按 id 查询单条分类，不存在返回 nil。
func (s *sCategory) findOne(ctx context.Context, id int64) (*category, error) {
	var records []*category
	if err := g.DB().Model("categories").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询分类: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// loadParentMap 加载全部分类的 id → parent_id 映射，用于层级与循环引用校验。
func (s *sCategory) loadParentMap(ctx context.Context) (map[int64]int64, error) {
	var records []*category
	if err := g.DB().Model("categories").Ctx(ctx).Fields("id", "parent_id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询分类父关系: %w", err))
	}
	m := make(map[int64]int64, len(records))
	for _, r := range records {
		m[r.Id] = r.ParentId
	}
	return m, nil
}

// buildTree 仅保留「自身及所有祖先均启用」的节点，并按 parent_id 组装嵌套树；
// 每一级按 sort 升序、sort 相同按 id 升序。
func buildTree(records []*category) []*v1.Category {
	byID := make(map[int64]*category, len(records))
	for _, r := range records {
		byID[r.Id] = r
	}

	visible := func(r *category) bool {
		cur := r
		for depth := 0; cur != nil; depth++ {
			if depth > maxLevel {
				return false // 数据异常（环或层级超限），不展示
			}
			if cur.Status != statusEnabled {
				return false
			}
			if cur.ParentId == 0 {
				return true
			}
			cur = byID[cur.ParentId]
		}
		return false
	}

	children := make(map[int64][]*v1.Category)
	roots := make([]*v1.Category, 0)
	for _, r := range records {
		if !visible(r) {
			continue
		}
		node := &v1.Category{
			Id:        r.Id,
			ParentId:  r.ParentId,
			Name:      r.Name,
			Sort:      r.Sort,
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
		if r.ParentId == 0 {
			roots = append(roots, node)
		} else {
			children[r.ParentId] = append(children[r.ParentId], node)
		}
	}

	sortNodes(roots)
	attachChildren(roots, children)
	return roots
}

// attachChildren 递归填充子分类，并对每一级排序。
func attachChildren(nodes []*v1.Category, children map[int64][]*v1.Category) {
	for _, n := range nodes {
		cs, ok := children[n.Id]
		if !ok {
			continue
		}
		sortNodes(cs)
		n.Children = cs
		attachChildren(cs, children)
	}
}

// sortNodes 对同级节点按 sort 升序、sort 相同按 id 升序排序。
func sortNodes(nodes []*v1.Category) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Sort != nodes[j].Sort {
			return nodes[i].Sort < nodes[j].Sort
		}
		return nodes[i].Id < nodes[j].Id
	})
}

// validateParent 校验 parentID 作为目标父分类是否合法：父存在、不指向自身或后代、
// 且移动后（含自身及整棵子树）层级不超过 maxLevel。selfID 为 0 表示创建场景（新节点为叶子）。
func validateParent(parentOf map[int64]int64, parentID, selfID int64) error {
	if parentID == 0 {
		return nil // 顶级分类
	}
	if _, ok := parentOf[parentID]; !ok {
		return codes.New(codes.CodeCategoryInvalidParent) // 父不存在
	}
	parentLevel, err := levelOf(parentOf, parentID)
	if err != nil {
		return codes.New(codes.CodeCategoryInvalidParent) // 父层级异常
	}
	if selfID == 0 {
		// 创建场景：新节点为叶子，新层级 = 父层级 + 1。
		if parentLevel+1 > maxLevel {
			return codes.New(codes.CodeCategoryInvalidParent)
		}
		return nil
	}
	// 更新/改父场景：禁止指向自身或后代，并保证整棵被移动子树的最深后代不超过 maxLevel。
	if isDescendant(parentOf, selfID, parentID) {
		return codes.New(codes.CodeCategoryInvalidParent) // 指向自身或自身后代
	}
	if parentLevel+subtreeDepth(parentOf, selfID) > maxLevel {
		return codes.New(codes.CodeCategoryInvalidParent) // 子树最深后代超过最大层级
	}
	return nil
}

// subtreeDepth 计算以 root 为根的子树的层级深度（root 自身记为 1）。
// parentOf 为全部分类的 id → parent_id 映射；数据异常（环/父缺失）按安全值处理，仅用于层级上限校验。
func subtreeDepth(parentOf map[int64]int64, root int64) int {
	children := make(map[int64][]int64)
	for id, parent := range parentOf {
		if parent != 0 {
			children[parent] = append(children[parent], id)
		}
	}
	var depth func(id int64, visiting map[int64]bool) int
	depth = func(id int64, visiting map[int64]bool) int {
		if visiting[id] {
			return 0 // 环防护：异常数据按 0 处理，避免死循环
		}
		visiting[id] = true
		maxChild := 0
		for _, c := range children[id] {
			if d := depth(c, visiting); d > maxChild {
				maxChild = d
			}
		}
		delete(visiting, id)
		return maxChild + 1
	}
	return depth(root, make(map[int64]bool))
}

// levelOf 计算分类层级（顶级为 1）；父不存在或存在环时返回错误。
func levelOf(parentOf map[int64]int64, id int64) (int, error) {
	if id == 0 {
		return 0, nil
	}
	level := 0
	cur := id
	for cur != 0 {
		level++
		if level > maxLevel {
			return 0, fmt.Errorf("分类层级超过 %d", maxLevel)
		}
		parent, ok := parentOf[cur]
		if !ok {
			return 0, fmt.Errorf("分类 %d 的父分类不存在", cur)
		}
		if parent == cur {
			return 0, fmt.Errorf("分类 %d 存在环", cur)
		}
		cur = parent
	}
	return level, nil
}

// isDescendant 判断 node 是否为 ancestor 的后代（含 node == ancestor）。
func isDescendant(parentOf map[int64]int64, ancestor, node int64) bool {
	cur := node
	for cur != 0 {
		if cur == ancestor {
			return true
		}
		parent, ok := parentOf[cur]
		if !ok || parent == cur {
			return false
		}
		cur = parent
	}
	return false
}

// validateName trim 后校验非空与长度，返回规范化后的分类名。
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

// validateStatus 校验状态枚举值（1 启用 / 0 禁用）。
func validateStatus(status int) error {
	if status != statusEnabled && status != statusDisabled {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

// toCategory 将内部记录转换为对外分类结构。
func toCategory(r *category) v1.Category {
	return v1.Category{
		Id:        r.Id,
		ParentId:  r.ParentId,
		Name:      r.Name,
		Sort:      r.Sort,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}
