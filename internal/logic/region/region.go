// Package region 实现地区字典（省/市/区三级）的只读业务逻辑。
// 地区数据由历史数据迁移一次性导入（3938 条），本模块不提供任何写入能力。
package region

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/region/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 地区层级：1=省、2=市、3=区/县（与 regions.level 一致）。
const (
	levelProvince = 1
	levelCity     = 2
	levelDistrict = 3
)

type sRegion struct{}

func init() {
	service.RegisterRegion(New())
}

// New 创建并返回地区字典服务实现。
func New() *sRegion {
	return &sRegion{}
}

// List 查询 parentID 的直接子级，按 id 升序（顺序稳定）。parentID=0 返回省级列表。
// 负数视为非法参数（1001）；不存在的正数父级返回空列表（合法查询无子级）。
func (s *sRegion) List(ctx context.Context, parentID int64) (*v1.ListRes, error) {
	if parentID < 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	records, err := queryRegions(ctx, g.DB(), "parent_id = ?", parentID)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Region, 0, len(records))
	for _, r := range records {
		items = append(items, toRegion(r))
	}
	return &v1.ListRes{Items: items}, nil
}

// Tree 返回省/市/区三级树：一次全量查询后在内存按 parent_id 建树，避免逐节点查询（N+1）。
// 数据不完整（孤儿节点/引用缺失）视为字典事实被破坏，返回 1000 而非输出残缺树。
func (s *sRegion) Tree(ctx context.Context) (*v1.TreeRes, error) {
	records, err := queryRegions(ctx, g.DB(), "1 = 1")
	if err != nil {
		return nil, err
	}

	// nodes 按 id 索引；roots 收集省级节点。records 已按 id 升序，故同级顺序天然稳定。
	nodes := make(map[int64]*v1.TreeNode, len(records))
	for _, r := range records {
		nodes[r.Id] = &v1.TreeNode{
			Region:   *toRegion(r),
			Children: make([]*v1.TreeNode, 0),
		}
	}

	roots := make([]*v1.TreeNode, 0, 34)
	seen := make(map[int64]bool, len(records))
	orphans := 0
	for _, r := range records {
		if seen[r.Id] {
			// 主键保证不会重复；保留防御性判断，避免未来查询变更导致重复节点进入响应。
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典存在重复节点 id=%d", r.Id))
		}
		seen[r.Id] = true

		node := nodes[r.Id]
		if r.ParentId == 0 {
			if r.Level != levelProvince {
				return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典层级非法 id=%d level=%d parent_id=0", r.Id, r.Level))
			}
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[r.ParentId]
		if !ok {
			orphans++
			continue
		}
		if parent.Level != r.Level-1 {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典父子层级不一致 id=%d level=%d parent_level=%d", r.Id, r.Level, parent.Level))
		}
		parent.Children = append(parent.Children, node)
	}
	if orphans > 0 {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典存在 %d 个孤儿节点", orphans))
	}

	total := 0
	var count func(n *v1.TreeNode)
	count = func(n *v1.TreeNode) {
		total++
		for _, c := range n.Children {
			count(c)
		}
	}
	for _, root := range roots {
		count(root)
	}
	if total != len(records) {
		// 存在未被任何根覆盖的节点（环或断裂链路），不输出残缺树。
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典树覆盖不完整: total=%d records=%d", total, len(records)))
	}
	glog.Debugf(ctx, "地区树构建完成: nodes=%d roots=%d", total, len(roots))
	return &v1.TreeRes{Items: roots, Total: total}, nil
}

// Detail 查询单个地区及其完整三级链路，链上不存在的层级为 nil。
// 不存在返回 1001（地区字典为公开只读数据，不引入独立「不存在」错误码）。
func (s *sRegion) Detail(ctx context.Context, id int64) (*v1.DetailRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	rec, err := findRegion(ctx, g.DB(), id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	res := &v1.DetailRes{Region: *toRegion(rec)}
	current := rec
	for current.Level > levelProvince {
		parent, err := findRegion(ctx, g.DB(), current.ParentId)
		if err != nil {
			return nil, err
		}
		if parent == nil || parent.Level != current.Level-1 {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("地区字典链路断裂 id=%d parent_id=%d", current.Id, current.ParentId))
		}
		switch parent.Level {
		case levelProvince:
			res.Province = toRegion(parent)
		case levelCity:
			res.City = toRegion(parent)
		}
		current = parent
	}
	if rec.Level == levelProvince {
		res.Province = toRegion(rec)
	}
	if rec.Level == levelDistrict {
		res.District = toRegion(rec)
	}
	return res, nil
}

// ResolveDistrict 校验 regionID 为存在的区/县级节点且省-市-区链路完整，返回权威链路。
// 单次 JOIN 查询完成，避免多次往返；不满足条件统一返回 1001（调用方据此返回参数错误）。
func (s *sRegion) ResolveDistrict(ctx context.Context, regionID int64) (*service.RegionChain, error) {
	if regionID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	query := `SELECT d.id AS district_id, d.parent_id AS district_pid, d.level AS district_level, d.name AS district_name,
       c.id AS city_id, c.parent_id AS city_pid, c.level AS city_level, c.name AS city_name,
       p.id AS province_id, p.parent_id AS province_pid, p.level AS province_level, p.name AS province_name
FROM regions d
JOIN regions c ON c.id = d.parent_id
JOIN regions p ON p.id = c.parent_id
WHERE d.id = ? AND d.level = 3 AND c.level = 2 AND p.level = 1 AND p.parent_id = 0`
	record, err := g.DB().Ctx(ctx).Raw(query, regionID).One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验地区链路: %w", err))
	}
	if record.IsEmpty() {
		// 不存在、非区县层级、父子链断裂统一按非法参数处理。
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	return &service.RegionChain{
		Province: &v1.Region{
			Id:       record["province_id"].Int64(),
			ParentId: record["province_pid"].Int64(),
			Level:    record["province_level"].Int(),
			Name:     record["province_name"].String(),
		},
		City: &v1.Region{
			Id:       record["city_id"].Int64(),
			ParentId: record["city_pid"].Int64(),
			Level:    record["city_level"].Int(),
			Name:     record["city_name"].String(),
		},
		District: &v1.Region{
			Id:       record["district_id"].Int64(),
			ParentId: record["district_pid"].Int64(),
			Level:    record["district_level"].Int(),
			Name:     record["district_name"].String(),
		},
	}, nil
}

// regionRecord 是 regions 表的一条记录。
type regionRecord struct {
	Id       int64  `json:"id"`
	ParentId int64  `json:"parent_id"`
	Level    int    `json:"level"`
	Name     string `json:"name"`
}

// queryRegions 按条件查询地区记录，统一按 id 升序保证响应顺序稳定。
func queryRegions(ctx context.Context, db gdb.DB, where string, args ...any) ([]*regionRecord, error) {
	var records []*regionRecord
	if err := db.Model("regions").Ctx(ctx).Where(where, args...).Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询地区: %w", err))
	}
	return records, nil
}

// findRegion 按 id 查询单个地区，不存在返回 nil。
func findRegion(ctx context.Context, db gdb.DB, id int64) (*regionRecord, error) {
	var records []*regionRecord
	if err := db.Model("regions").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询地区: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// toRegion 将内部记录转换为对外结构。
func toRegion(r *regionRecord) *v1.Region {
	return &v1.Region{Id: r.Id, ParentId: r.ParentId, Level: r.Level, Name: r.Name}
}
