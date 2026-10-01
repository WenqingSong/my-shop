package v1

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 库存变更类型枚举（API 出参为字符串枚举；DB 存 TINYINT 1/2）。
const (
	ChangeTypeIncrease = "increase"
	ChangeTypeDeduct   = "deduct"
)

// Inventory 库存结构，查询与写接口复用。
type Inventory struct {
	SkuId     int64       `json:"sku_id" dc:"SKU id"`
	Quantity  int64       `json:"quantity" dc:"当前库存数量（无记录时为 0）"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间（无记录时为 null）"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"更新时间（无记录时为 null）"`
}

// Log 库存变更流水记录。
type Log struct {
	Id              int64       `json:"id" dc:"流水 id"`
	SkuId           int64       `json:"sku_id" dc:"SKU id"`
	ChangeType      string      `json:"change_type" dc:"变更类型：increase/deduct"`
	ChangeQty       int64       `json:"change_qty" dc:"变更量（正数）"`
	BeforeQty       int64       `json:"before_qty" dc:"变更前库存"`
	AfterQty        int64       `json:"after_qty" dc:"变更后库存"`
	OperatorAdminId *int64      `json:"operator_admin_id" dc:"操作者管理员 id（可空）"`
	Reason          string      `json:"reason" dc:"变更原因"`
	CreatedAt       *gtime.Time `json:"created_at" dc:"变更时间"`
}

// GetReq 查询库存请求。
type GetReq struct {
	g.Meta `path:"/admin/inventories/:sku_id" method:"get" tags:"库存" summary:"查询库存"`
	SkuId  int64 `json:"sku_id" in:"path" v:"required" dc:"SKU id"`
}

// GetRes 查询库存响应（无记录 = 0）。
type GetRes struct {
	Inventory
}

// IncreaseReq 增加库存请求（兼作首次初始化）。
type IncreaseReq struct {
	g.Meta   `path:"/admin/inventories/:sku_id/increase" method:"post" tags:"库存" summary:"增加库存"`
	SkuId    int64       `json:"sku_id" in:"path" v:"required" dc:"SKU id"`
	Quantity json.Number `json:"quantity" dc:"增加数量（正整数）"`
}

// IncreaseRes 增加库存响应。
type IncreaseRes struct {
	Inventory
}

// DeductReq 条件扣减库存请求（库存充足才成功）。
type DeductReq struct {
	g.Meta   `path:"/admin/inventories/:sku_id/deduct" method:"post" tags:"库存" summary:"扣减库存"`
	SkuId    int64       `json:"sku_id" in:"path" v:"required" dc:"SKU id"`
	Quantity json.Number `json:"quantity" dc:"扣减数量（正整数）"`
}

// DeductRes 扣减库存响应。
type DeductRes struct {
	Inventory
}

// ListLogsReq 查询库存流水请求。
type ListLogsReq struct {
	g.Meta `path:"/admin/inventories/:sku_id/logs" method:"get" tags:"库存" summary:"查询库存流水"`
	SkuId  int64 `json:"sku_id" in:"path" v:"required" dc:"SKU id"`
}

// ListLogsRes 库存流水响应（id 倒序）。
type ListLogsRes struct {
	Items []*Log `json:"items" dc:"流水列表（id 倒序）"`
}
