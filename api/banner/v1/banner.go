// Package v1 定义轮播图（Banner）V1 的公开 API 契约。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// BannerItem 公开轮播图条目（仅 status=1）。
type BannerItem struct {
	Id       int64  `json:"id" dc:"轮播图 id"`
	Title    string `json:"title" dc:"标题"`
	ImageUrl string `json:"image_url" dc:"图片地址（可直接访问）"`
	LinkUrl  string `json:"link_url" dc:"跳转目标，无跳转时为空"`
	Sort     int    `json:"sort" dc:"展示顺序"`
}

// Banner 完整轮播图（后台列表/详情/写接口响应）。
type Banner struct {
	Id        int64       `json:"id" dc:"轮播图 id"`
	Title     string      `json:"title" dc:"标题"`
	ImageUrl  string      `json:"image_url" dc:"图片地址"`
	LinkUrl   string      `json:"link_url" dc:"跳转目标，无跳转时为空"`
	Sort      int         `json:"sort" dc:"展示顺序"`
	Status    int         `json:"status" dc:"1 启用 / 0 禁用"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ListReq 公开轮播图列表请求（无需 token）。
type ListReq struct {
	g.Meta `path:"/banners" method:"get" tags:"轮播图" summary:"公开轮播图列表（仅启用项）"`
}

// ListRes 公开轮播图列表响应（仅启用项，按 sort 升序、同值按 id 升序）。
type ListRes struct {
	Items []*BannerItem `json:"items" dc:"轮播图列表"`
}

// AdminListReq 后台轮播图列表请求（AdminAuth，全部状态）。
type AdminListReq struct {
	g.Meta `path:"/admin/banners" method:"get" tags:"轮播图" summary:"后台轮播图列表（全部状态）"`
}

// AdminListRes 后台轮播图列表响应（全部状态，按 sort 升序、同值按 id 升序）。
type AdminListRes struct {
	Items []*Banner `json:"items" dc:"轮播图列表"`
}

// AdminDetailReq 后台轮播图详情请求（AdminAuth，全部状态）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/banners/:id" method:"get" tags:"轮播图" summary:"后台轮播图详情"`
	Id     int64 `json:"id" in:"path" dc:"轮播图 id"`
}

// AdminDetailRes 后台轮播图详情响应。
type AdminDetailRes struct {
	Banner
}

// CreateReq 创建轮播图请求（AdminAuth + RequirePermission("banner:create")）。
type CreateReq struct {
	g.Meta   `path:"/admin/banners" method:"post" tags:"轮播图" summary:"创建轮播图"`
	Title    string `json:"title" dc:"标题（必填，trim 后非空且 ≤64 字符）"`
	ImageUrl string `json:"image_url" dc:"图片地址（必填，≤255 字符）"`
	LinkUrl  string `json:"link_url" dc:"跳转目标（可选，≤512 字符，空表示无跳转）"`
	Sort     int    `json:"sort" dc:"展示顺序，默认 0"`
	Status   *int   `json:"status" dc:"1 启用 / 0 禁用，默认 1"`
}

// CreateRes 创建轮播图响应。
type CreateRes struct {
	Banner
}

// UpdateReq 更新轮播图请求（AdminAuth + RequirePermission("banner:update")，仅提交需变更字段）。
type UpdateReq struct {
	g.Meta   `path:"/admin/banners/:id" method:"put" tags:"轮播图" summary:"更新轮播图"`
	Id       int64   `json:"id" in:"path" dc:"轮播图 id"`
	Title    *string `json:"title" dc:"标题（trim 后非空且 ≤64 字符）"`
	ImageUrl *string `json:"image_url" dc:"图片地址（≤255 字符）"`
	LinkUrl  *string `json:"link_url" dc:"跳转目标（≤512 字符，空表示清空跳转）"`
	Sort     *int    `json:"sort" dc:"展示顺序"`
	Status   *int    `json:"status" dc:"1 启用 / 0 禁用"`
}

// UpdateRes 更新轮播图响应。
type UpdateRes struct {
	Banner
}

// DeleteReq 删除轮播图请求（AdminAuth + RequirePermission("banner:delete")）。
type DeleteReq struct {
	g.Meta `path:"/admin/banners/:id" method:"delete" tags:"轮播图" summary:"删除轮播图"`
	Id     int64 `json:"id" in:"path" dc:"轮播图 id"`
}

// DeleteRes 删除轮播图响应。
type DeleteRes struct{}
