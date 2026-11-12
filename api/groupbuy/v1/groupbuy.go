// Package v1 定义拼团（Group Buy）公开 API 契约：用户端活动浏览、开团/参团、团详情与我的拼团；
// 后台活动管理、团查询与运营关闭。
//
// 状态枚举：API 出参为字符串，DB 存 TINYINT（见 docs/design/marketing.md）。
// 拼团价与参与订单均由服务端派生，本包不接收客户端价格或人数。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 拼团活动状态（DB TINYINT：0=停用、1=启用）。
const (
	ActivityStatusDisabled = "disabled" // 停用（0）
	ActivityStatusEnabled  = "enabled"  // 启用（1）
)

// 团状态（DB TINYINT：10/20/30/40）。
const (
	TeamStatusPending = "pending" // 拼团中（10）
	TeamStatusSuccess = "success" // 已成团（20，终态）
	TeamStatusFailed  = "failed"  // 已失败（30，终态）
	TeamStatusExpired = "expired" // 已过期（40，终态）
)

// 参与记录状态（DB TINYINT：10=有效、20=已退出）。
const (
	ParticipantStatusActive = "active" // 有效（10）
	ParticipantStatusQuit   = "quit"   // 已退出（20）
)

// Activity 拼团活动（查询与写接口复用）。
type Activity struct {
	Id             int64       `json:"id" dc:"拼团活动 id"`
	ProductId      int64       `json:"product_id" dc:"商品 id（FK products.id）"`
	SkuId          int64       `json:"sku_id" dc:"参与拼团的 SKU id（FK skus.id）"`
	Name           string      `json:"name" dc:"活动名称"`
	GroupPrice     int64       `json:"group_price" dc:"拼团价（整数分）"`
	GroupSize      int64       `json:"group_size" dc:"成团人数（含团长，≥2）"`
	TeamTtlMinutes int64       `json:"team_ttl_minutes" dc:"开团后有效分钟数"`
	StartTime      *gtime.Time `json:"start_time" dc:"活动开始时间"`
	EndTime        *gtime.Time `json:"end_time" dc:"活动结束时间"`
	Status         string      `json:"status" dc:"状态：enabled/disabled"`
	CreatedAt      *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt      *gtime.Time `json:"updated_at" dc:"更新时间"`
	// 以下是商品/SKU 的实时只读摘要（不落活动表，供前端展示）。
	SkuName          string `json:"sku_name" dc:"SKU 名称（当前值）"`
	SkuPrice         int64  `json:"sku_price" dc:"SKU 当前原价（整数分，当前值）"`
	ProductName      string `json:"product_name" dc:"商品名称（当前值）"`
	ProductMainImage string `json:"product_main_image" dc:"商品主图（当前值）"`
	OngoingTeams     int64  `json:"ongoing_teams" dc:"进行中的团数量"`
}

// Team 团实例（不含成员明细）。
type Team struct {
	Id           int64       `json:"id" dc:"团 id"`
	ActivityId   int64       `json:"activity_id" dc:"拼团活动 id"`
	LeaderUserId int64       `json:"leader_user_id" dc:"团长用户 id"`
	GroupPrice   int64       `json:"group_price" dc:"拼团价快照（整数分，开团时固定）"`
	RequiredSize int64       `json:"required_size" dc:"成团人数快照（含团长）"`
	JoinedCount  int64       `json:"joined_count" dc:"当前有效参与人数（含团长）"`
	Status       string      `json:"status" dc:"状态：pending/success/failed/expired"`
	ExpireAt     *gtime.Time `json:"expire_at" dc:"团截止时间"`
	ClosedAt     *gtime.Time `json:"closed_at" dc:"进入终态的时间（可空）"`
	CreatedAt    *gtime.Time `json:"created_at" dc:"开团时间"`
	UpdatedAt    *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// Participant 参与记录（对外不暴露成员订单 id，避免泄露他人订单）。
type Participant struct {
	Id        int64       `json:"id" dc:"参与记录 id"`
	TeamId    int64       `json:"team_id" dc:"团 id"`
	UserId    int64       `json:"user_id" dc:"参与用户 id"`
	Username  string      `json:"username" dc:"参与用户名（实时只读摘要）"`
	Status    string      `json:"status" dc:"状态：active/quit"`
	CreatedAt *gtime.Time `json:"created_at" dc:"参团时间"`
}

// TeamDetail 团详情：团 + 活动摘要 + 成员 + 当前用户的参与记录。
type TeamDetail struct {
	Team
	ActivityName     string         `json:"activity_name" dc:"活动名称（当前值）"`
	ProductId        int64          `json:"product_id" dc:"商品 id"`
	SkuId            int64          `json:"sku_id" dc:"SKU id"`
	SkuName          string         `json:"sku_name" dc:"SKU 名称（当前值）"`
	ProductName      string         `json:"product_name" dc:"商品名称（当前值）"`
	ProductMainImage string         `json:"product_main_image" dc:"商品主图（当前值）"`
	Participants     []*Participant `json:"participants" dc:"参与成员（按 id 升序）"`
	MyParticipant    *Participant   `json:"my_participant" dc:"当前登录用户的参与记录；未参与为 null"`
	MyOrderId        *int64         `json:"my_order_id" dc:"当前登录用户的参团订单 id；未参与为 null"`
}

// MyTeamItem 我的拼团记录项。
type MyTeamItem struct {
	Team
	ActivityName string      `json:"activity_name" dc:"活动名称（当前值）"`
	ProductName  string      `json:"product_name" dc:"商品名称（当前值）"`
	SkuName      string      `json:"sku_name" dc:"SKU 名称（当前值）"`
	OrderId      *int64      `json:"order_id" dc:"本人参团订单 id"`
	JoinedAt     *gtime.Time `json:"joined_at" dc:"本人参团时间"`
	IsLeader     bool        `json:"is_leader" dc:"是否团长"`
}

// ListActivitiesReq 前台拼团活动列表请求（仅启用且在时间窗内的活动）。
type ListActivitiesReq struct {
	g.Meta `path:"/group-buys" method:"get" tags:"拼团" summary:"拼团活动列表"`
	Page   *int `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size   *int `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
}

// ListActivitiesRes 前台拼团活动分页列表响应。
type ListActivitiesRes struct {
	Items []*Activity `json:"items" dc:"拼团活动列表（按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// ActivityDetailReq 前台拼团活动详情请求。
type ActivityDetailReq struct {
	g.Meta `path:"/group-buys/:id" method:"get" tags:"拼团" summary:"拼团活动详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
}

// ActivityDetailRes 前台拼团活动详情响应。
type ActivityDetailRes struct {
	Activity
}

// ListTeamsReq 查询指定活动进行中的团列表请求。
type ListTeamsReq struct {
	g.Meta `path:"/group-buys/:id/teams" method:"get" tags:"拼团" summary:"进行中的拼团列表"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
	Page   *int  `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size   *int  `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
}

// ListTeamsRes 进行中的拼团列表响应。
type ListTeamsRes struct {
	Items []*Team `json:"items" dc:"进行中的团列表（按 id 倒序）"`
	Total int     `json:"total" dc:"总记录数"`
	Page  int     `json:"page" dc:"当前页码"`
	Size  int     `json:"size" dc:"每页数量"`
}

// CreateTeamReq 发起拼团请求（团长开团 + 同事务创建拼团订单）。
type CreateTeamReq struct {
	g.Meta         `path:"/group-buys/:id/teams" method:"post" tags:"拼团" summary:"发起拼团（开团并下单）"`
	Id             int64  `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
	AddressId      int64  `json:"address_id" v:"required" dc:"收货地址 id（必须属于当前用户）"`
	IdempotencyKey string `json:"idempotency_key" v:"required" dc:"下单幂等键（同键不同内容 9006）"`
}

// CreateTeamRes 发起拼团响应（含团详情与本次订单 id）。
type CreateTeamRes struct {
	TeamDetail
}

// JoinTeamReq 参与已有拼团请求（同事务创建拼团订单）。
type JoinTeamReq struct {
	g.Meta         `path:"/group-buy-teams/:id/join" method:"post" tags:"拼团" summary:"参与拼团"`
	Id             int64  `json:"id" in:"path" v:"required" dc:"团 id"`
	AddressId      int64  `json:"address_id" v:"required" dc:"收货地址 id（必须属于当前用户）"`
	IdempotencyKey string `json:"idempotency_key" v:"required" dc:"下单幂等键（同键不同内容 9006）"`
}

// JoinTeamRes 参与拼团响应（含团详情与本次订单 id）。
type JoinTeamRes struct {
	TeamDetail
}

// TeamDetailReq 查询团详情请求。
type TeamDetailReq struct {
	g.Meta `path:"/group-buy-teams/:id" method:"get" tags:"拼团" summary:"拼团详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"团 id"`
}

// TeamDetailRes 团详情响应。
type TeamDetailRes struct {
	TeamDetail
}

// MyListReq 查询本人拼团记录请求。
type MyListReq struct {
	g.Meta `path:"/my/group-buys" method:"get" tags:"拼团" summary:"我的拼团记录"`
	Page   *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size   *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	Status string `json:"status" dc:"按团状态过滤：pending/success/failed/expired；空表示全部"`
}

// MyListRes 本人拼团记录响应。
type MyListRes struct {
	Items []*MyTeamItem `json:"items" dc:"我的拼团记录（按团 id 倒序）"`
	Total int           `json:"total" dc:"总记录数"`
	Page  int           `json:"page" dc:"当前页码"`
	Size  int           `json:"size" dc:"每页数量"`
}

// ---- 后台 ----

// AdminCreateReq 创建拼团活动请求（后台，持 group_buy:create）。
type AdminCreateReq struct {
	g.Meta         `path:"/admin/group-buys" method:"post" tags:"拼团" summary:"创建拼团活动"`
	Name           string      `json:"name" v:"required" dc:"活动名称（1-64 字符）"`
	ProductId      int64       `json:"product_id" v:"required" dc:"商品 id"`
	SkuId          int64       `json:"sku_id" v:"required" dc:"SKU id（必须属于该商品）"`
	GroupPrice     int64       `json:"group_price" v:"required" dc:"拼团价（整数分，>0 且 ≤ SKU 现价）"`
	GroupSize      int64       `json:"group_size" v:"required" dc:"成团人数（含团长，≥2）"`
	TeamTtlMinutes int64       `json:"team_ttl_minutes" v:"required" dc:"开团有效分钟数（1-10080）"`
	StartTime      *gtime.Time `json:"start_time" v:"required" dc:"活动开始时间（RFC3339）"`
	EndTime        *gtime.Time `json:"end_time" v:"required" dc:"活动结束时间（RFC3339，晚于开始时间）"`
}

// AdminCreateRes 创建拼团活动响应。
type AdminCreateRes struct {
	Activity
}

// AdminUpdateReq 修改拼团活动请求（后台，持 group_buy:update；商品与 SKU 不可变）。
type AdminUpdateReq struct {
	g.Meta         `path:"/admin/group-buys/:id" method:"put" tags:"拼团" summary:"修改拼团活动"`
	Id             int64       `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
	Name           string      `json:"name" v:"required" dc:"活动名称（1-64 字符）"`
	GroupPrice     int64       `json:"group_price" v:"required" dc:"拼团价（整数分，>0 且 ≤ SKU 现价）"`
	GroupSize      int64       `json:"group_size" v:"required" dc:"成团人数（含团长，≥2）"`
	TeamTtlMinutes int64       `json:"team_ttl_minutes" v:"required" dc:"开团有效分钟数（1-10080）"`
	StartTime      *gtime.Time `json:"start_time" v:"required" dc:"活动开始时间（RFC3339）"`
	EndTime        *gtime.Time `json:"end_time" v:"required" dc:"活动结束时间（RFC3339）"`
}

// AdminUpdateRes 修改拼团活动响应。
type AdminUpdateRes struct {
	Activity
}

// AdminStatusReq 启用/停用拼团活动请求（后台，持 group_buy:update）。
type AdminStatusReq struct {
	g.Meta `path:"/admin/group-buys/:id/status" method:"post" tags:"拼团" summary:"启用/停用拼团活动"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
	Status string `json:"status" v:"required" dc:"目标状态：enabled/disabled"`
}

// AdminStatusRes 启用/停用拼团活动响应。
type AdminStatusRes struct {
	Activity
}

// AdminListReq 后台拼团活动分页列表请求（持 group_buy:list）。
type AdminListReq struct {
	g.Meta    `path:"/admin/group-buys" method:"get" tags:"拼团" summary:"后台拼团活动列表"`
	Page      *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size      *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	Status    string `json:"status" dc:"状态过滤：enabled/disabled；空表示全部"`
	ProductId *int64 `json:"product_id" dc:"商品 id 精确过滤；<=0 返回 400"`
	SkuId     *int64 `json:"sku_id" dc:"SKU id 精确过滤；<=0 返回 400"`
}

// AdminListRes 后台拼团活动分页列表响应。
type AdminListRes struct {
	Items []*Activity `json:"items" dc:"拼团活动列表（按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// AdminDetailReq 后台拼团活动详情请求（持 group_buy:view）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/group-buys/:id" method:"get" tags:"拼团" summary:"后台拼团活动详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"拼团活动 id"`
}

// AdminDetailRes 后台拼团活动详情响应。
type AdminDetailRes struct {
	Activity
}

// AdminListTeamsReq 后台团列表请求（持 group_buy:list）。
type AdminListTeamsReq struct {
	g.Meta     `path:"/admin/group-buy-teams" method:"get" tags:"拼团" summary:"后台拼团列表"`
	Page       *int   `json:"page" dc:"页码，默认 1；<1 或非整数返回 400"`
	Size       *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界或非整数返回 400"`
	ActivityId *int64 `json:"activity_id" dc:"活动 id 精确过滤；<=0 返回 400"`
	Status     string `json:"status" dc:"团状态过滤：pending/success/failed/expired；空表示全部"`
	LeaderId   *int64 `json:"leader_user_id" dc:"团长 user id 精确过滤；<=0 返回 400"`
}

// AdminListTeamsRes 后台团列表响应。
type AdminListTeamsRes struct {
	Items []*Team `json:"items" dc:"团列表（按 id 倒序）"`
	Total int     `json:"total" dc:"总记录数"`
	Page  int     `json:"page" dc:"当前页码"`
	Size  int     `json:"size" dc:"每页数量"`
}

// AdminTeamDetailReq 后台团详情请求（持 group_buy:view）。
type AdminTeamDetailReq struct {
	g.Meta `path:"/admin/group-buy-teams/:id" method:"get" tags:"拼团" summary:"后台拼团详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"团 id"`
}

// AdminTeamDetailRes 后台团详情响应。
type AdminTeamDetailRes struct {
	TeamDetail
}

// AdminCloseTeamReq 运营关闭团请求（持 group_buy:close；仅拼团中可关闭）。
type AdminCloseTeamReq struct {
	g.Meta `path:"/admin/group-buy-teams/:id/close" method:"post" tags:"拼团" summary:"关闭拼团（判定失败）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"团 id"`
}

// AdminCloseTeamRes 关闭团响应。
type AdminCloseTeamRes struct {
	TeamDetail
}
