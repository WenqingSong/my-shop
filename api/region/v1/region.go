package v1

import "github.com/gogf/gf/v2/frame/g"

// Region 地区字典项（只读）。level：1=省、2=市、3=区/县；省级 parent_id=0。
type Region struct {
	Id       int64  `json:"id" dc:"地区 id（沿用历史 city_id）"`
	ParentId int64  `json:"parent_id" dc:"父级地区 id；省级为 0"`
	Level    int    `json:"level" dc:"层级：1=省、2=市、3=区/县"`
	Name     string `json:"name" dc:"地区名称"`
}

// ListReq 按父级查询直接子级请求。parent_id 缺省或为 0 时返回省级列表。
type ListReq struct {
	g.Meta   `path:"/regions" method:"get" tags:"地区" summary:"查询地区列表"`
	ParentId int64 `json:"parent_id" in:"query" d:"0" dc:"父级地区 id；0 或缺省返回省级列表"`
}

// ListRes 按父级查询直接子级响应（按 id 升序，顺序稳定）。
type ListRes struct {
	Items []*Region `json:"items" dc:"地区列表（按 id 升序）"`
}

// TreeNode 地区树节点。children 为该节点的直接子级；区/县节点 children 为空数组。
type TreeNode struct {
	Region
	Children []*TreeNode `json:"children" dc:"直接子级"`
}

// TreeReq 查询省/市/区三级树请求（只读全量字典）。
type TreeReq struct {
	g.Meta `path:"/regions/tree" method:"get" tags:"地区" summary:"查询地区三级树"`
}

// TreeRes 查询省/市/区三级树响应。total 为树中全部节点数（省份+城市+区县）。
type TreeRes struct {
	Items []*TreeNode `json:"items" dc:"省级节点列表（按 id 升序，子级按 id 升序）"`
	Total int         `json:"total" dc:"树中全部节点数"`
}

// DetailReq 查询单个地区及其完整三级链路请求。
type DetailReq struct {
	g.Meta `path:"/regions/:id" method:"get" tags:"地区" summary:"查询地区详情与层级链"`
	Id     int64 `json:"id" in:"path" v:"required|min:1" dc:"地区 id"`
}

// DetailRes 地区详情：province/city/district 对应 1/2/3 级，链上不存在的层级为 null。
type DetailRes struct {
	Region
	Province *Region `json:"province" dc:"省级地区；level=1 时为自身"`
	City     *Region `json:"city" dc:"市级地区；level>=2 时存在"`
	District *Region `json:"district" dc:"区/县级地区；level=3 时为自身"`
}
