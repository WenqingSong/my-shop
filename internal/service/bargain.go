package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/bargain/v1"
)

// IBargain 定义砍价服务：活动管理（后台）、活动浏览（公开）、发起砍价、好友助力与实例查询。
//
// 全部写操作的事实来源为单一 MySQL：活动/实例/助力记录均在事务内以条件更新 + 唯一约束保证一致性，
// 不引入 Redis、MQ 或分布式锁（见 docs/design/marketing.md §3）。
type IBargain interface {
	// AdminCreate 创建砍价活动（校验商品/SKU/价格/时间/助力参数）。
	AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error)
	// AdminUpdate 编辑砍价活动（product_id/sku_id 不可变更）。
	AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error)
	// AdminStatus 启用/停用活动（幂等，不影响已创建实例）。
	AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (*v1.AdminStatusRes, error)
	// AdminList 后台活动分页列表（可按状态/商品筛选）。
	AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error)
	// AdminDetail 后台活动详情（任意状态可见）。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// List 前台可参与活动分页列表（启用且在时间窗内）。
	List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error)
	// Detail 前台活动详情（公开，含当前是否可参与）。
	Detail(ctx context.Context, id int64) (*v1.DetailRes, error)
	// Start 发起砍价：单语句 INSERT ... SELECT 快照活动价格，uk_active_instance 保证一人一活动至多一个进行中实例。
	Start(ctx context.Context, userID, activityID int64) (*v1.StartRes, error)
	// MyInstances 查询当前用户的砍价实例（读取前懒推进已过期实例）。
	MyInstances(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error)
	// InstanceDetail 查询实例详情（登录用户均可查看，便于好友打开分享链接）。
	InstanceDetail(ctx context.Context, id int64) (*v1.InstanceDetailRes, error)
	// Help 好友助力：单事务内「锁实例行 → 校验 → 推进价格 → 写助力记录 → 达底价则置成功」。
	Help(ctx context.Context, helperID, instanceID int64) (*v1.HelpRes, error)
	// Helps 查询实例的助力记录（按 id 倒序）。
	Helps(ctx context.Context, instanceID int64) (*v1.HelpsRes, error)
	// Abandon 发起者放弃砍价（进行中 → 失败），释放 active_key 允许重新发起。
	Abandon(ctx context.Context, userID, instanceID int64) (*v1.AbandonRes, error)
}

var localBargain IBargain

// Bargain 返回砍价服务实现。
func Bargain() IBargain {
	if localBargain == nil {
		panic("implement not found for interface IBargain, forgot register?")
	}
	return localBargain
}

// RegisterBargain 注册砍价服务实现。
func RegisterBargain(s IBargain) {
	localBargain = s
}
