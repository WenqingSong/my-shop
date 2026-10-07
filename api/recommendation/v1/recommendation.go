// Package v1 定义推荐位（Recommendation）V1 的 API 契约：
// 前台公开查询 + 后台推荐位 CRUD 与推荐商品管理。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 推荐位状态（DB TINYINT；API 出参为 int：1=启用、0=禁用）。
const (
	StatusEnabled  = 1
	StatusDisabled = 0
)

// ItemSnapshot 前台推荐商品快照（仅 on_shelf 商品，查询时 JOIN products 实时快照）。
type ItemSnapshot struct {
	ProductId int64  `json:"product_id" dc:"商品 id"`
	Name      string `json:"name" dc:"商品名"`
	MainImage string `json:"main_image" dc:"商品主图"`
	Price     int64  `json:"price" dc:"价格（整数分）"`
	Sort      int    `json:"sort" dc:"展示顺序"`
}

// Item 后台推荐商品关系（完整字段，商品下架后仍保留并可见）。
type Item struct {
	Id         int64       `json:"id" dc:"关系 id"`
	PositionId int64       `json:"position_id" dc:"推荐位 id"`
	ProductId  int64       `json:"product_id" dc:"商品 id（软引用）"`
	Sort       int         `json:"sort" dc:"展示顺序"`
	CreatedAt  *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt  *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// Position 推荐位（后台完整字段）。
type Position struct {
	Id        int64       `json:"id" dc:"推荐位 id"`
	Code      string      `json:"code" dc:"稳定业务标识（创建后不可变）"`
	Name      string      `json:"name" dc:"推荐位名称"`
	Status    int         `json:"status" dc:"1 启用 / 0 禁用"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// FrontendReq 前台推荐位查询请求（无需 token）。
type FrontendReq struct {
	g.Meta `path:"/recommendations/:code" method:"get" tags:"推荐位" summary:"前台推荐位商品列表（仅启用位中的可售商品）"`
	Code   string `json:"code" in:"path" v:"required" dc:"推荐位 code"`
}

// FrontendRes 前台推荐位查询响应（推荐位不存在或禁用时 code/name 为空、items 为空数组）。
type FrontendRes struct {
	Code  string          `json:"code" dc:"推荐位 code（不存在/禁用时为空）"`
	Name  string          `json:"name" dc:"推荐位名称（不存在/禁用时为空）"`
	Items []*ItemSnapshot `json:"items" dc:"可售商品快照列表（按 sort,id 升序）"`
}

// AdminListReq 后台推荐位列表请求（AdminAuth，全部状态）。
type AdminListReq struct {
	g.Meta `path:"/admin/recommend-positions" method:"get" tags:"推荐位" summary:"后台推荐位列表（全部状态）"`
}

// AdminListRes 后台推荐位列表响应（全部状态，按 id 升序）。
type AdminListRes struct {
	Items []*Position `json:"items" dc:"推荐位列表"`
}

// AdminDetailReq 后台推荐位详情请求（AdminAuth，全部状态，含推荐商品）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/recommend-positions/:id" method:"get" tags:"推荐位" summary:"后台推荐位详情（含推荐商品）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"推荐位 id"`
}

// AdminDetailRes 后台推荐位详情响应。
type AdminDetailRes struct {
	Position
	Items []*Item `json:"items" dc:"推荐商品关系列表（按 sort,id 升序）"`
}

// CreateReq 创建推荐位请求（AdminAuth + RequirePermission("recommend:create")）。
type CreateReq struct {
	g.Meta `path:"/admin/recommend-positions" method:"post" tags:"推荐位" summary:"创建推荐位"`
	Code   string `json:"code" dc:"稳定业务标识（[a-z0-9][a-z0-9-]{0,63}，创建后不可变）"`
	Name   string `json:"name" dc:"推荐位名称（trim 后非空且 ≤64 字符）"`
	Status *int   `json:"status" dc:"1 启用 / 0 禁用，默认 1"`
}

// CreateRes 创建推荐位响应。
type CreateRes struct {
	Position
}

// UpdateReq 更新推荐位请求（AdminAuth + RequirePermission("recommend:update")，仅 name/status，不改 code）。
type UpdateReq struct {
	g.Meta `path:"/admin/recommend-positions/:id" method:"put" tags:"推荐位" summary:"更新推荐位（name/status）"`
	Id     int64   `json:"id" in:"path" v:"required" dc:"推荐位 id"`
	Name   *string `json:"name" dc:"推荐位名称（trim 后非空且 ≤64 字符）"`
	Status *int    `json:"status" dc:"1 启用 / 0 禁用"`
}

// UpdateRes 更新推荐位响应。
type UpdateRes struct {
	Position
}

// DeleteReq 删除推荐位请求（AdminAuth + RequirePermission("recommend:delete")，物理删除 + 级联推荐商品）。
type DeleteReq struct {
	g.Meta `path:"/admin/recommend-positions/:id" method:"delete" tags:"推荐位" summary:"删除推荐位（级联删除推荐商品关系）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"推荐位 id"`
}

// DeleteRes 删除推荐位响应。
type DeleteRes struct{}

// AddItemReq 添加推荐商品请求（AdminAuth + RequirePermission("recommend:item")）。
type AddItemReq struct {
	g.Meta    `path:"/admin/recommend-positions/:id/items" method:"post" tags:"推荐位" summary:"添加推荐商品"`
	Id        int64 `json:"id" in:"path" v:"required" dc:"推荐位 id"`
	ProductId int64 `json:"product_id" dc:"商品 id（必须存在）"`
	Sort      *int  `json:"sort" dc:"展示顺序，默认 0"`
}

// AddItemRes 添加推荐商品响应。
type AddItemRes struct {
	Item
}

// RemoveItemReq 移除推荐商品请求（AdminAuth + RequirePermission("recommend:item")）。
type RemoveItemReq struct {
	g.Meta    `path:"/admin/recommend-positions/:id/items/:product_id" method:"delete" tags:"推荐位" summary:"移除推荐商品"`
	Id        int64 `json:"id" in:"path" v:"required" dc:"推荐位 id"`
	ProductId int64 `json:"product_id" in:"path" v:"required" dc:"商品 id"`
}

// RemoveItemRes 移除推荐商品响应。
type RemoveItemRes struct{}

// UpdateSortReq 调整推荐商品排序请求（AdminAuth + RequirePermission("recommend:item")）。
type UpdateSortReq struct {
	g.Meta     `path:"/admin/recommend-positions/:id/items/sort" method:"put" tags:"推荐位" summary:"调整推荐商品排序"`
	Id         int64   `json:"id" in:"path" v:"required" dc:"推荐位 id"`
	ProductIds []int64 `json:"product_ids" dc:"按目标顺序排列的商品 id 列表（非空、无重复，必须恰好覆盖该推荐位全部已加入商品）"`
}

// UpdateSortRes 调整推荐商品排序响应。
type UpdateSortRes struct {
	Items []*Item `json:"items" dc:"排序后的推荐商品关系列表（按 sort,id 升序）"`
}
