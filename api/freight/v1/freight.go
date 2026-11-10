// Package v1 定义运费模板（Freight）V1 的公开 API 契约。
//
// 本批次只提供后台管理能力（模板 CRUD、启停、区域规则管理）；
// 运费计算由 internal/logic/freight 的计价入口在服务端完成，前端不直接调用计算接口。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 模板状态枚举（DB TINYINT）。
const (
	TemplateStatusEnabled  = 1 // 启用
	TemplateStatusDisabled = 0 // 停用
)

// 计费方式：本批次只实现按件计费。
const (
	ChargeTypeByPiece = "by_piece" // 按件计费（首件 + 续件）
)

// Template 运费模板（后台列表/详情/写接口响应）。
type Template struct {
	Id            int64       `json:"id" dc:"模板 id"`
	Name          string      `json:"name" dc:"模板名称（全局唯一）"`
	IsFree        int         `json:"is_free" dc:"1=模板级无条件包邮（跳过区域规则）；0=按区域规则计费"`
	FreeThreshold int64       `json:"free_threshold" dc:"满额免邮阈值（整数分，0=不启用）"`
	Status        int         `json:"status" dc:"1 启用 / 0 停用；停用模板不可用于计价"`
	Remark        string      `json:"remark" dc:"备注"`
	CreatedAt     *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt     *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// Rule 区域运费规则（按件计费）。
type Rule struct {
	Id                 int64       `json:"id" dc:"规则 id"`
	TemplateId         int64       `json:"template_id" dc:"所属模板 id"`
	RegionId           int64       `json:"region_id" dc:"地区 id（regions.id，省/市/区任一层级）"`
	FirstQuantity      int64       `json:"first_quantity" dc:"首件数量（>=1）"`
	FirstFee           int64       `json:"first_fee" dc:"首件费用（整数分）"`
	AdditionalQuantity int64       `json:"additional_quantity" dc:"续件数量（>=1）"`
	AdditionalFee      int64       `json:"additional_fee" dc:"续件费用（整数分）"`
	FreeThreshold      int64       `json:"free_threshold" dc:"该区域满额免邮阈值（整数分，0=继承模板）"`
	CreatedAt          *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt          *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// RuleInput 区域规则写入入参（整体替换时使用）。
type RuleInput struct {
	RegionId           int64 `json:"region_id" v:"required" dc:"地区 id（必须存在于 regions）"`
	FirstQuantity      int64 `json:"first_quantity" v:"required" dc:"首件数量，>=1"`
	FirstFee           int64 `json:"first_fee" dc:"首件费用（整数分，>=0）"`
	AdditionalQuantity int64 `json:"additional_quantity" v:"required" dc:"续件数量，>=1"`
	AdditionalFee      int64 `json:"additional_fee" dc:"续件费用（整数分，>=0）"`
	FreeThreshold      int64 `json:"free_threshold" dc:"该区域满额免邮阈值（整数分，0=继承模板）"`
}

// AdminCreateReq 创建运费模板（后台，持 freight:create）。
type AdminCreateReq struct {
	g.Meta        `path:"/admin/freight-templates" method:"post" tags:"运费" summary:"创建运费模板"`
	Name          string `json:"name" v:"required" dc:"模板名称，1-64 字符，全局唯一"`
	IsFree        *int   `json:"is_free" dc:"1=无条件包邮；0 或缺省=按区域规则计费"`
	FreeThreshold int64  `json:"free_threshold" dc:"满额免邮阈值（整数分，0=不启用）"`
	Remark        string `json:"remark" dc:"备注，<=255 字符"`
}

// AdminCreateRes 创建运费模板响应。
type AdminCreateRes struct {
	Template
}

// AdminListReq 查询运费模板列表（后台，持 freight:list）。
type AdminListReq struct {
	g.Meta `path:"/admin/freight-templates" method:"get" tags:"运费" summary:"运费模板列表"`
}

// AdminListRes 运费模板列表响应（按 id 倒序）。
type AdminListRes struct {
	Items []*Template `json:"items" dc:"模板列表"`
}

// AdminDetailReq 查询运费模板详情（后台，持 freight:view）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/freight-templates/:id" method:"get" tags:"运费" summary:"运费模板详情（含区域规则）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"模板 id"`
}

// AdminDetailRes 运费模板详情响应。
type AdminDetailRes struct {
	Template
	Rules []*Rule `json:"rules" dc:"区域规则列表（按 region_id 升序）"`
}

// AdminUpdateReq 更新运费模板基础信息（后台，持 freight:update）。
// 区域规则不在此接口更新，见 AdminReplaceRulesReq。
type AdminUpdateReq struct {
	g.Meta        `path:"/admin/freight-templates/:id" method:"put" tags:"运费" summary:"更新运费模板"`
	Id            int64  `json:"id" in:"path" v:"required" dc:"模板 id"`
	Name          string `json:"name" v:"required" dc:"模板名称，1-64 字符，全局唯一"`
	IsFree        *int   `json:"is_free" dc:"1=无条件包邮；0=按区域规则计费；缺省保持原值"`
	FreeThreshold int64  `json:"free_threshold" dc:"满额免邮阈值（整数分，0=不启用）"`
	Remark        string `json:"remark" dc:"备注，<=255 字符"`
}

// AdminUpdateRes 更新运费模板响应。
type AdminUpdateRes struct {
	Template
}

// AdminUpdateStatusReq 启用/停用运费模板（后台，持 freight:update）。
type AdminUpdateStatusReq struct {
	g.Meta `path:"/admin/freight-templates/:id/status" method:"put" tags:"运费" summary:"启用或停用运费模板"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"模板 id"`
	Status *int  `json:"status" v:"required" dc:"1=启用、0=停用"`
}

// AdminUpdateStatusRes 启用/停用响应。
type AdminUpdateStatusRes struct {
	Template
}

// AdminListRulesReq 查询模板的区域规则（后台，持 freight:view）。
type AdminListRulesReq struct {
	g.Meta `path:"/admin/freight-templates/:id/rules" method:"get" tags:"运费" summary:"查询运费区域规则"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"模板 id"`
}

// AdminListRulesRes 区域规则列表响应。
type AdminListRulesRes struct {
	Items []*Rule `json:"items" dc:"区域规则列表"`
}

// AdminReplaceRulesReq 整体替换模板的区域规则（后台，持 freight:update）。
// 采用整体替换而非逐条增删，保证「同一模板同一区域至多一条规则」的语义在单事务内成立。
type AdminReplaceRulesReq struct {
	g.Meta `path:"/admin/freight-templates/:id/rules" method:"put" tags:"运费" summary:"整体替换运费区域规则"`
	Id     int64        `json:"id" in:"path" v:"required" dc:"模板 id"`
	Rules  []*RuleInput `json:"rules" dc:"区域规则全量列表；空数组表示清空全部规则"`
}

// AdminReplaceRulesRes 整体替换响应。
type AdminReplaceRulesRes struct {
	Items []*Rule `json:"items" dc:"替换后的区域规则列表"`
}
