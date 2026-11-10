package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/region/v1"
)

// RegionChain 一个区/县级地区及其完整的省-市-区链路。
// 供地址模块在写入 region_id 时取字典权威名称，避免信任客户端提交的地区文本。
type RegionChain struct {
	Province *v1.Region
	City     *v1.Region
	District *v1.Region
}

// IRegion 定义地区字典只读服务（省/市/区三级）。
// 地区数据由历史数据迁移一次性导入，本模块不提供任何写入能力。
type IRegion interface {
	// List 查询 parentID 的直接子级；parentID=0 返回省级列表。按 id 升序，顺序稳定。
	List(ctx context.Context, parentID int64) (*v1.ListRes, error)
	// Tree 返回省/市/区三级树（一次性查询构建，无 N+1）。
	Tree(ctx context.Context) (*v1.TreeRes, error)
	// Detail 查询单个地区及其层级链；不存在返回 INVALID_ARGUMENT（400）。
	Detail(ctx context.Context, id int64) (*v1.DetailRes, error)
	// ResolveDistrict 校验 regionID 是存在的区/县级（level=3）节点且父子链完整，
	// 返回省/市/区链路；不满足（不存在、层级不对、链路断裂）返回 INVALID_ARGUMENT（400）。
	ResolveDistrict(ctx context.Context, regionID int64) (*RegionChain, error)
}

var localRegion IRegion

// Region 返回地区字典服务实现。
func Region() IRegion {
	if localRegion == nil {
		panic("implement not found for interface IRegion, forgot register?")
	}
	return localRegion
}

// RegisterRegion 注册地区字典服务实现。
func RegisterRegion(s IRegion) {
	localRegion = s
}
