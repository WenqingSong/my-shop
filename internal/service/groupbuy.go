package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/groupbuy/v1"
)

// IGroupBuy 定义拼团服务：活动管理、开团/参团、团查询、超时与运营关闭。
//
// 关键约束（见 docs/design/marketing.md）：
//   - 拼团不拥有库存与支付：拼团订单复用 IOrder 的事务内下单与取消能力；
//   - 团人数以 group_buy_teams.joined_count 为容量事实，条件更新 + 唯一键兜底；
//   - 团状态终态为 success/failed/expired，只允许从 pending 迁入且只迁入一次。
type IGroupBuy interface {
	// ListActivities 前台活动列表：仅启用且处于 [start_time, end_time) 的活动。
	ListActivities(ctx context.Context, req *v1.ListActivitiesReq) (*v1.ListActivitiesRes, error)
	// ActivityDetail 前台活动详情；不存在返回 21001。
	ActivityDetail(ctx context.Context, id int64) (*v1.ActivityDetailRes, error)
	// ListTeams 查询指定活动进行中（pending 且未到期）的团。
	ListTeams(ctx context.Context, req *v1.ListTeamsReq) (*v1.ListTeamsRes, error)
	// CreateTeam 发起拼团：单事务写团 + 拼团订单 + 参与记录；返回团详情。
	CreateTeam(ctx context.Context, userID int64, req *v1.CreateTeamReq) (*v1.CreateTeamRes, error)
	// JoinTeam 参与已有团：容量条件更新 + 订单 + 参与记录 + 成团推进同事务。
	JoinTeam(ctx context.Context, userID int64, req *v1.JoinTeamReq) (*v1.JoinTeamRes, error)
	// TeamDetail 查询团详情（含成员与当前用户参与情况）；不存在返回 21005。
	TeamDetail(ctx context.Context, userID, teamID int64) (*v1.TeamDetailRes, error)
	// MyList 查询本人拼团记录（按团 id 倒序，支持团状态过滤）。
	MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error)

	// AdminCreate 创建活动（校验商品/SKU/价格/人数/时间窗）。
	AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error)
	// AdminUpdate 修改活动（商品与 SKU 不可变；已开团使用快照不受影响）。
	AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error)
	// AdminStatus 启用/停用活动。
	AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (*v1.AdminStatusRes, error)
	// AdminList 后台活动分页列表。
	AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error)
	// AdminDetail 后台活动详情。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// AdminListTeams 后台团分页列表。
	AdminListTeams(ctx context.Context, req *v1.AdminListTeamsReq) (*v1.AdminListTeamsRes, error)
	// AdminTeamDetail 后台团详情。
	AdminTeamDetail(ctx context.Context, id int64) (*v1.AdminTeamDetailRes, error)
	// AdminCloseTeam 运营关闭团（pending→failed）：同事务取消该团未支付订单并恢复库存。
	// 已支付订单不伪造退款，保留 paid 供用户走现有退款流程；重复关闭返回 21009。
	AdminCloseTeam(ctx context.Context, adminID, teamID int64) (*v1.AdminCloseTeamRes, error)

	// ExpireTeams 扫描并推进过期团（pending 且 expire_at <= NOW()）为 expired，
	// 同时取消未支付订单恢复库存；返回本轮实际推进的团数量。供后台扫描器复用。
	ExpireTeams(ctx context.Context, limit int) (int, error)
}

var localGroupBuy IGroupBuy

// GroupBuy 返回拼团服务实现。
func GroupBuy() IGroupBuy {
	if localGroupBuy == nil {
		panic("implement not found for interface IGroupBuy, forgot register?")
	}
	return localGroupBuy
}

// RegisterGroupBuy 注册拼团服务实现。
func RegisterGroupBuy(s IGroupBuy) {
	localGroupBuy = s
}
