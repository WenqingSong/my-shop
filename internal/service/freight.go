package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/freight/v1"
)

// FreightItem 是运费计算的一行商品输入。
//
// FreightTemplateID 为 nil 表示商品未绑定运费模板（历史商品），按既有零运费兼容；
// Amount 为该行商品原始金额（整数分），是满额免邮的比较基数。
type FreightItem struct {
	FreightTemplateID *int64
	Quantity          int64
	Amount            int64
}

// FreightQuoteReq 是运费计算请求。
//
// RegionID 来自收货地址的结构化地区（regions.id），不接受地区名称，
// 避免「按名称模糊匹配」在不同层级重名时产生错误归属。
type FreightQuoteReq struct {
	RegionID int64
	Items    []FreightItem
}

// FreightQuoteRes 是运费计算结果（整数分）。
type FreightQuoteRes struct {
	ShippingFee int64
}

// IFreight 定义运费模板管理（后台）与运费计算（服务端唯一入口）能力。
type IFreight interface {
	// AdminCreate 创建运费模板（后台）。
	AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error)
	// AdminList 查询全部运费模板（后台，按 id 倒序）。
	AdminList(ctx context.Context) (*v1.AdminListRes, error)
	// AdminDetail 查询模板详情（含区域规则）；不存在返回 20001。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// AdminUpdate 更新模板基础信息；不存在 20001，重名 20002。
	AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error)
	// AdminUpdateStatus 启用/停用模板；不存在 20001，非法状态 20003。
	AdminUpdateStatus(ctx context.Context, req *v1.AdminUpdateStatusReq) (*v1.AdminUpdateStatusRes, error)
	// AdminListRules 查询模板的区域规则；模板不存在 20001。
	AdminListRules(ctx context.Context, id int64) (*v1.AdminListRulesRes, error)
	// AdminReplaceRules 单事务整体替换模板的区域规则；重复区域 20006，地区不存在 20008。
	AdminReplaceRules(ctx context.Context, req *v1.AdminReplaceRulesReq) (*v1.AdminReplaceRulesRes, error)

	// ValidateTemplateForBinding 校验运费模板可被商品绑定：必须存在且启用。
	// 不存在返回 21001，已停用返回 21004；商品管理侧写 freight_template_id 前必须调用。
	ValidateTemplateForBinding(ctx context.Context, id int64) error

	// Quote 计算一笔订单的运费（服务端唯一入口，只读）。
	//
	// 语义：
	//   - 商品未绑定模板（FreightTemplateID=nil）→ 该行运费 0（历史兼容）；
	//   - 已绑定模板但模板不存在 → 20001，模板停用 → 20004；
	//   - 模板启用但当前地区链无匹配规则 → 20009，绝不静默按 0 处理；
	//   - 模板级包邮或满额免邮命中 → 该模板组运费 0；
	//   - 多个不同模板的商品各自计费后求和。
	Quote(ctx context.Context, req *FreightQuoteReq) (*FreightQuoteRes, error)
}

var localFreight IFreight

// Freight 返回运费服务实现。
func Freight() IFreight {
	if localFreight == nil {
		panic("implement not found for interface IFreight, forgot register?")
	}
	return localFreight
}

// RegisterFreight 注册运费服务实现。
func RegisterFreight(s IFreight) {
	localFreight = s
}
