package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/review/v1"
)

// IReview 定义商品评价服务。
type IReview interface {
	// Create 提交评价：服务端校验购买资格（订单项归属 + 订单已完成），
	// 从 Principal.UserID 与订单项推导归属，唯一约束兜底「每个订单项最多一条」。
	Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error)
	// ListProduct 商品公开评价列表与汇总（仅 published，实时聚合 avg/count）。
	ListProduct(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error)
	// MyList 查询本人全部评价（含全部状态，分页）。
	MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error)
	// Update 修改本人 published 评价；非本人或不存在统一 10001。
	Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 删除本人评价（published → deleted）；非本人或不存在统一 10001。
	Delete(ctx context.Context, userID, id int64) (*v1.DeleteRes, error)
	// TakeDown 下架违规评价（published → taken_down）；不存在或已非 published 统一 10001。
	TakeDown(ctx context.Context, id int64) (*v1.TakeDownRes, error)
}

var localReview IReview

// Review 返回商品评价服务实现。
func Review() IReview {
	if localReview == nil {
		panic("implement not found for interface IReview, forgot register?")
	}
	return localReview
}

// RegisterReview 注册商品评价服务实现。
func RegisterReview(s IReview) {
	localReview = s
}
