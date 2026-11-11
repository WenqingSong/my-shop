package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/spec/v1"
)

// ISpec 定义商品规格（规格组/规格值）服务。
//
// 规格为商品级定义，写入前必须校验商品归属：规格组必须指向存在的商品，
// 规格值只能写入其所属规格组，跨商品/跨规格组写入一律拒绝。
type ISpec interface {
	// ListByProduct 查询商品全部规格组与规格值（按 sort、id 升序）。
	ListByProduct(ctx context.Context, productID int64) ([]*v1.SpecGroup, error)
	// CreateGroup 创建规格组（商品必须存在，同商品组名唯一）。
	CreateGroup(ctx context.Context, req *v1.CreateGroupReq) (*v1.CreateGroupRes, error)
	// UpdateGroup 更新规格组名称/排序（商品归属不可变）。
	UpdateGroup(ctx context.Context, req *v1.UpdateGroupReq) (*v1.UpdateGroupRes, error)
	// DeleteGroup 删除规格组（级联删除规格值；被 SKU 引用的值会阻止删除）。
	DeleteGroup(ctx context.Context, id int64) error
	// CreateValue 在指定规格组下创建规格值（同组值唯一）。
	CreateValue(ctx context.Context, req *v1.CreateValueReq) (*v1.CreateValueRes, error)
	// UpdateValue 更新规格值文本/排序（规格组归属不可变）。
	UpdateValue(ctx context.Context, req *v1.UpdateValueReq) (*v1.UpdateValueRes, error)
	// DeleteValue 删除规格值（被 SKU 引用时拒绝）。
	DeleteValue(ctx context.Context, id int64) error
}

var localSpec ISpec

// Spec 返回商品规格服务实现。
func Spec() ISpec {
	if localSpec == nil {
		panic("implement not found for interface ISpec, forgot register?")
	}
	return localSpec
}

// RegisterSpec 注册商品规格服务实现。
func RegisterSpec(s ISpec) {
	localSpec = s
}
