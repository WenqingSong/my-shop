package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// Category 分类结构，同时用于树形节点与单分类详情。
type Category struct {
	Id        int64       `json:"id" dc:"分类 id"`
	ParentId  int64       `json:"parent_id" dc:"父分类 id，0 表示顶级"`
	Name      string      `json:"name" dc:"分类名"`
	Sort      int         `json:"sort" dc:"同级排序值"`
	Status    int         `json:"status" dc:"1 启用 / 0 禁用"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"更新时间"`
	Children  []*Category `json:"children,omitempty" dc:"子分类（树形返回时填充）"`
}

// ListReq 树形分类列表请求。
type ListReq struct {
	g.Meta `path:"/api/v1/categories" method:"get" tags:"分类" summary:"树形分类列表（仅启用项）"`
}

// ListRes 树形分类列表响应。
type ListRes struct {
	Items []*Category `json:"items" dc:"顶级分类树"`
}

// DetailReq 分类详情请求。
type DetailReq struct {
	g.Meta `path:"/api/v1/categories/:id" method:"get" tags:"分类" summary:"分类详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"分类 id"`
}

// DetailRes 分类详情响应。
type DetailRes struct {
	Category
}

// CreateReq 创建分类请求。
type CreateReq struct {
	g.Meta   `path:"/admin/v1/categories" method:"post" tags:"分类" summary:"创建分类"`
	ParentId int64  `json:"parent_id" dc:"父分类 id，0 表示顶级"`
	Name     string `json:"name" v:"required" dc:"分类名，非空且不超过 64 字符"`
	Sort     int    `json:"sort" dc:"同级排序值，默认 0"`
	Status   *int   `json:"status" dc:"1 启用 / 0 禁用，默认 1"`
}

// CreateRes 创建分类响应。
type CreateRes struct {
	Id int64 `json:"id" dc:"新分类 id"`
}

// UpdateReq 更新分类请求（仅提交需要变更的字段）。
type UpdateReq struct {
	g.Meta   `path:"/admin/v1/categories/:id" method:"put" tags:"分类" summary:"更新分类"`
	Id       int64   `json:"id" in:"path" v:"required" dc:"分类 id"`
	ParentId *int64  `json:"parent_id" dc:"父分类 id，0 表示顶级"`
	Name     *string `json:"name" dc:"分类名，非空且不超过 64 字符"`
	Sort     *int    `json:"sort" dc:"同级排序值"`
	Status   *int    `json:"status" dc:"1 启用 / 0 禁用"`
}

// UpdateRes 更新分类响应。
type UpdateRes struct {
	Category
}

// DeleteReq 删除分类请求。
type DeleteReq struct {
	g.Meta `path:"/admin/v1/categories/:id" method:"delete" tags:"分类" summary:"删除分类"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"分类 id"`
}

// DeleteRes 删除分类响应。
type DeleteRes struct{}
