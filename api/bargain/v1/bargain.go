// Package v1 定义砍价（Bargain）V1 的公开 API 契约：
// 后台活动管理（创建/编辑/启停/列表/详情）、用户侧活动浏览、发起砍价、好友助力、实例与助力记录查询。
//
// 状态约定：活动 `status` 为 TINYINT（1=启用、0=停用）；实例 `status` 在 DB 中为 TINYINT 10/20/30/40，
// 对外统一映射为字符串枚举（in_progress/success/failed/expired），客户端按字符串判断，不依赖数字。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 实例状态枚举（DB TINYINT 10/20/30/40，见 docs/design/marketing.md §3.4）。
const (
	StatusInProgress = "in_progress" // 进行中（10）
	StatusSuccess    = "success"     // 砍价成功（20，终态）
	StatusFailed     = "failed"      // 砍价失败（30，终态）
	StatusExpired    = "expired"     // 已过期（40，终态）
)

// 活动状态（DB TINYINT）。
const (
	ActivityStatusEnabled  = 1 // 启用（可参与）
	ActivityStatusDisabled = 0 // 停用（不可参与，不影响已创建实例）
)

// Activity 砍价活动（后台管理与前台浏览复用）。
type Activity struct {
	Id               int64       `json:"id" dc:"活动 id"`
	ProductId        int64       `json:"product_id" dc:"商品 id（物理 FK）"`
	SkuId            int64       `json:"sku_id" dc:"SKU id（活动价格作用于该 SKU，物理 FK）"`
	Name             string      `json:"name" dc:"活动名称"`
	StartPrice       int64       `json:"start_price" dc:"原始价（整数分）"`
	FloorPrice       int64       `json:"floor_price" dc:"目标价/底价（整数分，< start_price）"`
	HelpAmount       int64       `json:"help_amount" dc:"每次助力的名义砍减额（整数分，最后一次可只砍剩余差额）"`
	MaxHelpCount     int64       `json:"max_help_count" dc:"单个实例最多被助力次数"`
	StartTime        *gtime.Time `json:"start_time" dc:"活动开始时间"`
	EndTime          *gtime.Time `json:"end_time" dc:"活动结束时间"`
	Status           int         `json:"status" dc:"1=启用、0=停用"`
	ProductName      string      `json:"product_name" dc:"商品名（联查展示，非快照）"`
	ProductMainImage string      `json:"product_main_image" dc:"商品主图（联查展示，非快照）"`
	SkuName          string      `json:"sku_name" dc:"SKU 名（联查展示，非快照）"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt        *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// Instance 用户砍价实例。
type Instance struct {
	Id               int64       `json:"id" dc:"实例 id"`
	ActivityId       int64       `json:"activity_id" dc:"活动 id"`
	UserId           int64       `json:"user_id" dc:"发起砍价的用户 id"`
	Status           string      `json:"status" dc:"状态：in_progress/success/failed/expired"`
	StartPrice       int64       `json:"start_price" dc:"原始价快照（整数分）"`
	FloorPrice       int64       `json:"floor_price" dc:"目标价快照（整数分）"`
	CurrentPrice     int64       `json:"current_price" dc:"当前价（整数分，≥ floor_price）"`
	RemainingAmount  int64       `json:"remaining_amount" dc:"距目标价还需砍掉的金额（整数分，= current_price - floor_price）"`
	HelpedCount      int64       `json:"helped_count" dc:"已成功助力次数"`
	MaxHelpCount     int64       `json:"max_help_count" dc:"助力次数上限快照"`
	ExpireAt         *gtime.Time `json:"expire_at" dc:"截止时间快照（= 活动 end_time）"`
	CompletedAt      *gtime.Time `json:"completed_at" dc:"砍价成功时间（失败/过期为空）"`
	OrderId          *int64      `json:"order_id" dc:"关联订单 id（软引用；下单联动未在本期实现，通常为空）"`
	ActivityName     string      `json:"activity_name" dc:"活动名称（联查展示）"`
	ProductId        int64       `json:"product_id" dc:"商品 id"`
	SkuId            int64       `json:"sku_id" dc:"SKU id"`
	ProductName      string      `json:"product_name" dc:"商品名（联查展示）"`
	ProductMainImage string      `json:"product_main_image" dc:"商品主图（联查展示）"`
	SkuName          string      `json:"sku_name" dc:"SKU 名（联查展示）"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"发起时间"`
	UpdatedAt        *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// HelpRecord 助力记录（append-only 的价格变化凭据）。
type HelpRecord struct {
	Id              int64       `json:"id" dc:"助力记录 id"`
	InstanceId      int64       `json:"instance_id" dc:"砍价实例 id"`
	HelperUserId    int64       `json:"helper_user_id" dc:"助力者用户 id"`
	HelperNickname  string      `json:"helper_nickname" dc:"助力者昵称（联查展示，可能为空串）"`
	HelperAvatarUrl string      `json:"helper_avatar_url" dc:"助力者头像（联查展示，可能为空串）"`
	CutAmount       int64       `json:"cut_amount" dc:"本次实际砍减额（整数分，≤ help_amount）"`
	PriceBefore     int64       `json:"price_before" dc:"本次助力前价格（整数分）"`
	PriceAfter      int64       `json:"price_after" dc:"本次助力后价格（整数分，≥ floor_price）"`
	CreatedAt       *gtime.Time `json:"created_at" dc:"助力时间"`
}

// AdminCreateReq 创建砍价活动请求（后台，需 bargain:create）。
type AdminCreateReq struct {
	g.Meta       `path:"/admin/bargain-activities" method:"post" tags:"砍价" summary:"创建砍价活动"`
	Name         string      `json:"name" dc:"活动名称（trim 后 1~64 字符）"`
	ProductId    int64       `json:"product_id" dc:"商品 id（必须存在）"`
	SkuId        int64       `json:"sku_id" dc:"SKU id（必须存在且属于该商品）"`
	StartPrice   int64       `json:"start_price" dc:"原始价（整数分，1~99999999）"`
	FloorPrice   int64       `json:"floor_price" dc:"目标价/底价（整数分，≥1 且 < start_price）"`
	HelpAmount   int64       `json:"help_amount" dc:"每次助力的名义砍减额（整数分，≥1）"`
	MaxHelpCount int64       `json:"max_help_count" dc:"单个实例最多被助力次数（≥1，且需足以砍到 floor_price）"`
	StartTime    *gtime.Time `json:"start_time" dc:"活动开始时间（必须早于 end_time）"`
	EndTime      *gtime.Time `json:"end_time" dc:"活动结束时间（必须晚于当前时间）"`
}

// AdminCreateRes 创建砍价活动响应。
type AdminCreateRes struct {
	Activity
}

// AdminUpdateReq 编辑砍价活动请求（后台，需 bargain:update）。
// product_id/sku_id 不可变更：活动已有实例时改绑商品会让在途砍价指向另一件商品，需另建活动。
type AdminUpdateReq struct {
	g.Meta       `path:"/admin/bargain-activities/:id" method:"put" tags:"砍价" summary:"编辑砍价活动"`
	Id           int64       `json:"id" in:"path" v:"required" dc:"活动 id"`
	Name         string      `json:"name" dc:"活动名称（trim 后 1~64 字符）"`
	ProductId    int64       `json:"product_id" dc:"商品 id（必须与创建时一致）"`
	SkuId        int64       `json:"sku_id" dc:"SKU id（必须与创建时一致）"`
	StartPrice   int64       `json:"start_price" dc:"原始价（整数分）"`
	FloorPrice   int64       `json:"floor_price" dc:"目标价/底价（整数分）"`
	HelpAmount   int64       `json:"help_amount" dc:"每次助力的名义砍减额（整数分）"`
	MaxHelpCount int64       `json:"max_help_count" dc:"单个实例最多被助力次数"`
	StartTime    *gtime.Time `json:"start_time" dc:"活动开始时间"`
	EndTime      *gtime.Time `json:"end_time" dc:"活动结束时间（必须晚于当前时间）"`
}

// AdminUpdateRes 编辑砍价活动响应。
type AdminUpdateRes struct {
	Activity
}

// AdminStatusReq 启用/停用砍价活动请求（后台，需 bargain:update；幂等）。
type AdminStatusReq struct {
	g.Meta `path:"/admin/bargain-activities/:id/status" method:"post" tags:"砍价" summary:"启用或停用砍价活动"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"活动 id"`
	Status int   `json:"status" dc:"目标状态：1=启用、0=停用"`
}

// AdminStatusRes 启用/停用砍价活动响应。
type AdminStatusRes struct {
	Activity
}

// AdminListReq 后台砍价活动分页列表请求（只读，需 bargain:list）。
type AdminListReq struct {
	g.Meta    `path:"/admin/bargain-activities" method:"get" tags:"砍价" summary:"后台砍价活动分页列表"`
	Page      *int   `json:"page" dc:"页码，默认 1；<1 返回 400"`
	Size      *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
	Status    *int   `json:"status" dc:"状态筛选：1=启用、0=停用；省略=全部"`
	ProductId *int64 `json:"product_id" dc:"商品 id 精确筛选；<=0 返回 400"`
}

// AdminListRes 后台砍价活动分页列表响应。
type AdminListRes struct {
	Items []*Activity `json:"items" dc:"活动列表（按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// AdminDetailReq 后台砍价活动详情请求（只读，需 bargain:view）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/bargain-activities/:id" method:"get" tags:"砍价" summary:"后台砍价活动详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"活动 id"`
}

// AdminDetailRes 后台砍价活动详情响应。
type AdminDetailRes struct {
	Activity
}

// ListReq 前台可参与砍价活动列表请求（公开，无需 token）。
type ListReq struct {
	g.Meta    `path:"/bargain-activities" method:"get" tags:"砍价" summary:"查询可参与的砍价活动"`
	Page      *int   `json:"page" dc:"页码，默认 1；<1 返回 400"`
	Size      *int   `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
	ProductId *int64 `json:"product_id" dc:"商品 id 精确筛选；<=0 返回 400"`
}

// ListRes 前台可参与砍价活动列表响应。
type ListRes struct {
	Items []*Activity `json:"items" dc:"可参与活动列表（启用且在时间窗内，按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// DetailReq 前台砍价活动详情请求（公开，无需 token）。
type DetailReq struct {
	g.Meta `path:"/bargain-activities/:id" method:"get" tags:"砍价" summary:"查询砍价活动详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"活动 id"`
}

// DetailRes 前台砍价活动详情响应。
type DetailRes struct {
	Activity
	IsJoinable bool `json:"is_joinable" dc:"当前是否可发起砍价（启用且在 [start_time, end_time) 内）"`
}

// StartReq 发起砍价请求（前台登录用户，作用于 Principal.UserID）。
type StartReq struct {
	g.Meta     `path:"/bargain-activities/:id/instances" method:"post" tags:"砍价" summary:"发起砍价"`
	ActivityId int64 `json:"id" in:"path" v:"required" dc:"活动 id"`
}

// StartRes 发起砍价响应。
type StartRes struct {
	Instance
}

// MyListReq 查询我的砍价记录请求（前台登录用户）。
type MyListReq struct {
	g.Meta `path:"/my/bargain-instances" method:"get" tags:"砍价" summary:"查询我的砍价记录"`
	Page   *int `json:"page" dc:"页码，默认 1；<1 返回 400"`
	Size   *int `json:"size" dc:"每页数量，默认 20、上限 100；越界返回 400"`
}

// MyListRes 查询我的砍价记录响应。
type MyListRes struct {
	Items []*Instance `json:"items" dc:"我的砍价实例（按 id 倒序）"`
	Total int         `json:"total" dc:"总记录数"`
	Page  int         `json:"page" dc:"当前页码"`
	Size  int         `json:"size" dc:"每页数量"`
}

// InstanceDetailReq 查询砍价实例详情请求（前台登录用户；助力者也需要查看分享的实例）。
type InstanceDetailReq struct {
	g.Meta `path:"/bargain-instances/:id" method:"get" tags:"砍价" summary:"查询砍价实例详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"实例 id"`
}

// InstanceDetailRes 查询砍价实例详情响应。
type InstanceDetailRes struct {
	Instance
}

// HelpReq 好友助力请求（前台登录用户，作用于 Principal.UserID）。
type HelpReq struct {
	g.Meta `path:"/bargain-instances/:id/help" method:"post" tags:"砍价" summary:"好友助力砍价"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"实例 id"`
}

// HelpRes 好友助力响应：返回推进后的实例与本次助力记录。
type HelpRes struct {
	Instance
	Help *HelpRecord `json:"help" dc:"本次助力记录"`
}

// HelpsReq 查询砍价实例助力记录请求（前台登录用户）。
type HelpsReq struct {
	g.Meta `path:"/bargain-instances/:id/helps" method:"get" tags:"砍价" summary:"查询砍价助力记录"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"实例 id"`
}

// HelpsRes 查询砍价助力记录响应。
type HelpsRes struct {
	Items []*HelpRecord `json:"items" dc:"助力记录（按 id 倒序）"`
}

// AbandonReq 放弃砍价请求（前台登录用户，仅实例发起者、仅进行中）。
type AbandonReq struct {
	g.Meta `path:"/bargain-instances/:id/abandon" method:"post" tags:"砍价" summary:"放弃砍价"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"实例 id"`
}

// AbandonRes 放弃砍价响应。
type AbandonRes struct {
	Instance
}
