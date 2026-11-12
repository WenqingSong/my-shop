// Package groupbuy 实现拼团（Group Buy）业务：活动管理、开团/参团、成团推进、
// 超时过期与运营关闭。数据库结构由 migration 20261110000011_group_buy 提供，
// 本包只在其三张表上实现业务，不新增活动库存、不改订单金额语义、不触碰秒杀与库存表。
//
// 核心机制（详见 docs/design/marketing.md）：
//   - 团人数以 group_buy_teams.joined_count 为容量事实，参团用「条件更新 + RowsAffected」闸门，
//     不以「先查后写」或事后 COUNT(*) 判定名额；
//   - 成团/失败/过期均为 pending(10) → 终态(20/30/40) 的条件更新，只有命中者推进，保证只发生一次；
//   - 拼团订单复用 IOrder 的事务内下单（CreateGroupBuyInTx）与取消（CancelInTx），
//     团容量更新、订单创建、参与记录在同一事务内提交，不产生「有订单无参与」或「有参与无订单」。
package groupbuy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/groupbuy/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 活动状态（DB TINYINT，见 docs/design/marketing.md §2.1）。
const (
	activityStatusDisabled = 0
	activityStatusEnabled  = 1
)

// 团状态（DB TINYINT，见 docs/design/marketing.md §2.4）。终态 = {20,30,40}。
const (
	teamStatusPending = 10
	teamStatusSuccess = 20
	teamStatusFailed  = 30
	teamStatusExpired = 40
)

// 参与记录状态（DB TINYINT，见 docs/design/marketing.md §2.3）。
const (
	participantStatusActive = 10
)

const (
	// maxNameLen 活动名称最大长度（与 DDL VARCHAR(64) 一致）。
	maxNameLen = 64
	// minGroupSize 成团人数下限：至少 2 人（含团长）才构成拼团。
	minGroupSize = 2
	// maxGroupSize 成团人数上限：防止 int 溢出与异常活动配置。
	maxGroupSize = 1000
	// minTeamTTLMinutes / maxTeamTTLMinutes 团有效期上下限（1 分钟 ~ 7 天）。
	minTeamTTLMinutes = 1
	maxTeamTTLMinutes = 10080
	// maxPrice 拼团价上限（整数分，= ¥999,999.99），与秒杀价上限一致。
	maxPrice = 99_999_999
	// participantQuantity 拼团一人一份：每个参与记录对应数量固定为 1。
	participantQuantity = 1
)

// 分页默认值与上限（与退款/订单后台一致）。
const (
	defaultPage = 1
	defaultSize = 20
	maxSize     = 100
)

type sGroupBuy struct{}

func init() {
	service.RegisterGroupBuy(New())
}

// New 创建并返回拼团服务实现。
func New() *sGroupBuy {
	return &sGroupBuy{}
}

// activityRow 是 group_buy_activities 表的一条记录。
type activityRow struct {
	Id             int64       `json:"id"`
	ProductId      int64       `json:"product_id"`
	SkuId          int64       `json:"sku_id"`
	Name           string      `json:"name"`
	GroupPrice     int64       `json:"group_price"`
	GroupSize      int64       `json:"group_size"`
	TeamTtlMinutes int64       `json:"team_ttl_minutes"`
	StartTime      *gtime.Time `json:"start_time"`
	EndTime        *gtime.Time `json:"end_time"`
	Status         int         `json:"status"`
	CreatedAt      *gtime.Time `json:"created_at"`
	UpdatedAt      *gtime.Time `json:"updated_at"`
}

// teamRow 是 group_buy_teams 表的一条记录。
type teamRow struct {
	Id           int64       `json:"id"`
	ActivityId   int64       `json:"activity_id"`
	LeaderUserId int64       `json:"leader_user_id"`
	GroupPrice   int64       `json:"group_price"`
	RequiredSize int64       `json:"required_size"`
	JoinedCount  int64       `json:"joined_count"`
	Status       int         `json:"status"`
	ExpireAt     *gtime.Time `json:"expire_at"`
	ClosedAt     *gtime.Time `json:"closed_at"`
	CreatedAt    *gtime.Time `json:"created_at"`
	UpdatedAt    *gtime.Time `json:"updated_at"`
}

// participantRow 是 group_buy_participants 表的一条记录。
type participantRow struct {
	Id        int64       `json:"id"`
	TeamId    int64       `json:"team_id"`
	UserId    int64       `json:"user_id"`
	OrderId   *int64      `json:"order_id"`
	Status    int         `json:"status"`
	CreatedAt *gtime.Time `json:"created_at"`
}

// activityMeta 是活动关联商品/SKU 的实时只读摘要（不落活动表）。
type activityMeta struct {
	SkuName          string
	SkuPrice         int64
	ProductName      string
	ProductMainImage string
	OngoingTeams     int64
}

// skuSummary 是 SKU 的只读摘要（拼团展示用，不写入活动表）。
type skuSummary struct {
	Id        int64  `json:"id"`
	ProductId int64  `json:"product_id"`
	Name      string `json:"name"`
	Price     int64  `json:"price"`
}

// productSummary 是商品的只读摘要（拼团展示用，不写入活动表）。
type productSummary struct {
	Id        int64  `json:"id"`
	Name      string `json:"name"`
	MainImage string `json:"main_image"`
}

// activityTeamCount 是「活动 → 进行中团数量」的聚合行。
type activityTeamCount struct {
	ActivityId int64 `json:"activity_id"`
	Total      int64 `json:"total"`
}

// userNameRow 是用户名的只读行（参与成员展示）。
type userNameRow struct {
	Id       int64  `json:"id"`
	Username string `json:"username"`
}

// teamMeta 是团关联的活动/SKU/商品摘要（用于团详情）。
type teamMeta struct {
	ActivityName     string
	ProductId        int64
	SkuId            int64
	SkuName          string
	ProductName      string
	ProductMainImage string
}

// myTeamRow 是「我的拼团」联表查询结果。
type myTeamRow struct {
	TeamId       int64       `json:"team_id"`
	ActivityId   int64       `json:"activity_id"`
	LeaderId     int64       `json:"leader_user_id"`
	GroupPrice   int64       `json:"group_price"`
	RequiredSize int64       `json:"required_size"`
	JoinedCount  int64       `json:"joined_count"`
	Status       int         `json:"status"`
	ExpireAt     *gtime.Time `json:"expire_at"`
	ClosedAt     *gtime.Time `json:"closed_at"`
	TeamCreated  *gtime.Time `json:"team_created_at"`
	OrderId      *int64      `json:"order_id"`
	JoinedAt     *gtime.Time `json:"joined_at"`
}

// ---------- 前台：活动 ----------

// ListActivities 前台活动列表：仅启用且处于 [start_time, end_time) 的活动。
func (s *sGroupBuy) ListActivities(ctx context.Context, req *v1.ListActivitiesReq) (*v1.ListActivitiesRes, error) {
	page, size, err := parsePage(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("group_buy_activities").Ctx(ctx).
		Where("status", activityStatusEnabled).
		Where("start_time <= NOW()").
		Where("end_time > NOW()")
	total, err := model.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计拼团活动: %w", err))
	}
	var rows []*activityRow
	if err := model.Order("id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团活动: %w", err))
	}
	metas, err := s.loadActivityMetas(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Activity, 0, len(rows))
	for _, r := range rows {
		items = append(items, toActivity(r, metas[r.Id]))
	}
	return &v1.ListActivitiesRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// ActivityDetail 前台活动详情；不存在返回 21001。已结束/停用活动仍可查看（历史团与参团记录需可追溯）。
func (s *sGroupBuy) ActivityDetail(ctx context.Context, id int64) (*v1.ActivityDetailRes, error) {
	row, err := s.findActivity(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeGroupBuyNotFound)
	}
	metas, err := s.loadActivityMetas(ctx, []*activityRow{row})
	if err != nil {
		return nil, err
	}
	return &v1.ActivityDetailRes{Activity: *toActivity(row, metas[row.Id])}, nil
}

// ListTeams 查询指定活动进行中（pending 且未到期）的团。
func (s *sGroupBuy) ListTeams(ctx context.Context, req *v1.ListTeamsReq) (*v1.ListTeamsRes, error) {
	if req == nil || req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	page, size, err := parsePage(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	activity, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if activity == nil {
		return nil, codes.New(codes.CodeGroupBuyNotFound)
	}
	model := g.DB().Model("group_buy_teams").Ctx(ctx).
		Where("activity_id", req.Id).
		Where("status", teamStatusPending).
		Where("expire_at > NOW()")
	total, err := model.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计进行中拼团: %w", err))
	}
	var rows []*teamRow
	if err := model.Order("id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询进行中拼团: %w", err))
	}
	items := make([]*v1.Team, 0, len(rows))
	for _, r := range rows {
		items = append(items, toTeam(r))
	}
	return &v1.ListTeamsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// ---------- 前台：开团/参团 ----------

// CreateTeam 发起拼团：单事务「写团 + 拼团订单 + 参与记录」。
// 活动在事务内 FOR UPDATE 读取，确保快照（价格/人数/有效期）与活动启停、改价串行化。
func (s *sGroupBuy) CreateTeam(ctx context.Context, userID int64, req *v1.CreateTeamReq) (*v1.CreateTeamRes, error) {
	if req == nil || req.Id <= 0 || req.AddressId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	var teamID int64
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// FOR UPDATE 锁定活动行：开团快照与后台改价/启停串行化。
		// 有效性用 MySQL NOW() 判定（不依赖 Go 与 MySQL 的时区一致性）。
		var activities []*activityRow
		if e := tx.Model("group_buy_activities").Ctx(ctx).
			Where("id", req.Id).LockUpdate().Scan(&activities); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定拼团活动: %w", e))
		}
		if len(activities) == 0 {
			return codes.New(codes.CodeGroupBuyNotFound)
		}
		open, e := activityOpenInTx(ctx, tx, req.Id)
		if e != nil {
			return e
		}
		if !open {
			return codes.New(codes.CodeGroupBuyActivityInactive)
		}
		activity := activities[0]

		id, e := tx.Model("group_buy_teams").Ctx(ctx).Data(g.Map{
			"activity_id":    activity.Id,
			"leader_user_id": userID,
			"group_price":    activity.GroupPrice,
			"required_size":  activity.GroupSize,
			"joined_count":   1,
			"status":         teamStatusPending,
			// 有效期与价格/人数同为开团快照；用 MySQL NOW() 避免 Go↔MySQL 时区漂移。
			"expire_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(activity.TeamTtlMinutes, 10) + " MINUTE)"),
		}).InsertAndGetId()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入拼团团实例: %w", e))
		}

		orderID, e := service.Order().CreateGroupBuyInTx(ctx, tx, &service.GroupBuyOrderInput{
			UserID:         userID,
			AddressID:      req.AddressId,
			SkuID:          activity.SkuId,
			Quantity:       participantQuantity,
			GroupPrice:     activity.GroupPrice,
			IdempotencyKey: idempotencyKey,
		})
		if e != nil {
			return e
		}

		if _, e := tx.Model("group_buy_participants").Ctx(ctx).Data(g.Map{
			"team_id":  id,
			"user_id":  userID,
			"order_id": orderID,
			"status":   participantStatusActive,
		}).Insert(); e != nil {
			if key := duplicateKeyName(e); key != "" {
				return codes.New(codes.CodeGroupBuyAlreadyJoined)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入团长参与记录: %w", e))
		}

		teamID = id
		return nil
	})
	if err != nil {
		return nil, err
	}

	detail, err := s.loadTeamDetail(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	logx.Info(ctx, "group_buy_team_created", "拼团开团成功", logx.Fields{
		"user_id":  userID,
		"team_id":  teamID,
		"order_id": detail.MyOrderId,
	})
	return &v1.CreateTeamRes{TeamDetail: *detail}, nil
}

// JoinTeam 参与已有团。
//
// 并发与幂等：
//  1. 事务前先按 (team_id,user_id) 读参与记录，命中即返回当前团详情（常规重复请求幂等，不重复下单）；
//  2. 事务内用「条件更新 joined_count + 1 WHERE status=10 AND joined_count<required_size AND expire_at>NOW()」
//     作为容量闸门；0 行即满/终态/过期，拒绝参团；
//  3. 订单与参与记录在同一事务写入；并发重复请求由 uk_team_user 兜底（1062 → 21007，整体回滚，计数回滚）；
//  4. 达到 required_size 时同事务条件更新 status=20，只有命中者推进，保证「只成团一次」。
func (s *sGroupBuy) JoinTeam(ctx context.Context, userID int64, req *v1.JoinTeamReq) (*v1.JoinTeamRes, error) {
	if req == nil || req.Id <= 0 || req.AddressId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	// 快速幂等路径：已参与直接返回当前团详情（不重复扣库存/建单）。
	existing, err := s.findParticipant(ctx, req.Id, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		detail, err := s.loadTeamDetail(ctx, userID, req.Id)
		if err != nil {
			return nil, err
		}
		if detail == nil {
			return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
		}
		return &v1.JoinTeamRes{TeamDetail: *detail}, nil
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 先读团仅用于区分「不存在」与「不可参与」并解析活动 SKU；
		// 名额是否可用完全由下面的条件更新决定，读结果不作为并发闸门。
		var teams []*teamRow
		if e := tx.Model("group_buy_teams").Ctx(ctx).Where("id", req.Id).Scan(&teams); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取拼团团实例: %w", e))
		}
		if len(teams) == 0 {
			return codes.New(codes.CodeGroupBuyTeamNotFound)
		}

		result, e := tx.Model("group_buy_teams").Ctx(ctx).
			Where("id", req.Id).
			Where("status", teamStatusPending).
			Where("joined_count < required_size").
			Where("expire_at > NOW()").
			Data(g.Map{"joined_count": gdb.Raw("joined_count + 1")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("占用拼团名额: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeGroupBuyTeamNotJoinable)
		}

		// 条件更新已持有该团行锁，此处的 joined_count 是本次占用后的权威值。
		if e := tx.Model("group_buy_teams").Ctx(ctx).Where("id", req.Id).Scan(&teams); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取拼团团实例: %w", e))
		}
		team := teams[0]

		activity, e := findActivityInTx(ctx, tx, team.ActivityId)
		if e != nil {
			return e
		}
		if activity == nil {
			return codes.New(codes.CodeGroupBuyNotFound)
		}
		// 已开的团不受活动停用/结束影响（INV-GB07）；成交价与人数使用团快照。
		if activity.SkuId <= 0 {
			return codes.New(codes.CodeGroupBuySkuUnavailable)
		}

		orderID, e := service.Order().CreateGroupBuyInTx(ctx, tx, &service.GroupBuyOrderInput{
			UserID:         userID,
			AddressID:      req.AddressId,
			SkuID:          activity.SkuId,
			Quantity:       participantQuantity,
			GroupPrice:     team.GroupPrice,
			IdempotencyKey: idempotencyKey,
		})
		if e != nil {
			return e
		}

		if _, e := tx.Model("group_buy_participants").Ctx(ctx).Data(g.Map{
			"team_id":  req.Id,
			"user_id":  userID,
			"order_id": orderID,
			"status":   participantStatusActive,
		}).Insert(); e != nil {
			if key := duplicateKeyName(e); key != "" {
				return codes.New(codes.CodeGroupBuyAlreadyJoined)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入拼团参与记录: %w", e))
		}

		// 成团推进：与本次参团同事务；条件更新保证并发下只有一次成功。
		if team.JoinedCount >= team.RequiredSize {
			result, e := tx.Model("group_buy_teams").Ctx(ctx).
				Where("id", req.Id).
				Where("status", teamStatusPending).
				Where("joined_count >= required_size").
				Data(g.Map{"status": teamStatusSuccess, "closed_at": gdb.Raw("NOW()")}).
				Update()
			if e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进拼团成团: %w", e))
			}
			if affected, _ := result.RowsAffected(); affected > 0 {
				logx.Info(ctx, "group_buy_team_success", "拼团成团", logx.Fields{
					"team_id":      team.Id,
					"joined_count": team.JoinedCount,
					"required":     team.RequiredSize,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	detail, err := s.loadTeamDetail(ctx, userID, req.Id)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	logx.Info(ctx, "group_buy_team_joined", "参与拼团成功", logx.Fields{
		"user_id":  userID,
		"team_id":  req.Id,
		"status":   detail.Status,
		"order_id": detail.MyOrderId,
	})
	return &v1.JoinTeamRes{TeamDetail: *detail}, nil
}

// TeamDetail 查询团详情；不存在返回 21005。
func (s *sGroupBuy) TeamDetail(ctx context.Context, userID, teamID int64) (*v1.TeamDetailRes, error) {
	detail, err := s.loadTeamDetail(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	return &v1.TeamDetailRes{TeamDetail: *detail}, nil
}

// MyList 查询本人拼团记录（按团 id 倒序，支持团状态过滤）。
func (s *sGroupBuy) MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error) {
	var statusFilter *int
	if req != nil && strings.TrimSpace(req.Status) != "" {
		status, err := parseTeamStatus(req.Status)
		if err != nil {
			return nil, err
		}
		statusFilter = &status
	}
	page, size := defaultPage, defaultSize
	if req != nil {
		var err error
		page, size, err = parsePage(req.Page, req.Size)
		if err != nil {
			return nil, err
		}
	}

	base := g.DB().Model("group_buy_participants p").Ctx(ctx).
		InnerJoin("group_buy_teams t", "t.id = p.team_id").
		Where("p.user_id", userID)
	if statusFilter != nil {
		base = base.Where("t.status", *statusFilter)
	}
	total, err := base.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计我的拼团: %w", err))
	}

	fields := "t.id AS team_id, t.activity_id, t.leader_user_id, t.group_price, t.required_size, " +
		"t.joined_count, t.status, t.expire_at, t.closed_at, t.created_at AS team_created_at, " +
		"p.order_id, p.created_at AS joined_at"
	var rows []*myTeamRow
	if err := base.Fields(fields).Order("t.id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询我的拼团: %w", err))
	}

	activityIDs := make([]int64, 0, len(rows))
	for _, r := range rows {
		activityIDs = append(activityIDs, r.ActivityId)
	}
	activities, err := s.findActivitiesByIDs(ctx, activityIDs)
	if err != nil {
		return nil, err
	}
	activityRows := make([]*activityRow, 0, len(activities))
	for _, a := range activities {
		activityRows = append(activityRows, a)
	}
	metas, err := s.loadActivityMetas(ctx, activityRows)
	if err != nil {
		return nil, err
	}

	items := make([]*v1.MyTeamItem, 0, len(rows))
	for _, r := range rows {
		item := &v1.MyTeamItem{
			Team: v1.Team{
				Id:           r.TeamId,
				ActivityId:   r.ActivityId,
				LeaderUserId: r.LeaderId,
				GroupPrice:   r.GroupPrice,
				RequiredSize: r.RequiredSize,
				JoinedCount:  r.JoinedCount,
				Status:       teamStatusToString(r.Status),
				ExpireAt:     r.ExpireAt,
				ClosedAt:     r.ClosedAt,
				CreatedAt:    r.TeamCreated,
			},
			OrderId:  r.OrderId,
			JoinedAt: r.JoinedAt,
			IsLeader: r.LeaderId == userID,
		}
		if a := activities[r.ActivityId]; a != nil {
			item.ActivityName = a.Name
			if m := metas[a.Id]; m != nil {
				item.ProductName = m.ProductName
				item.SkuName = m.SkuName
			}
		}
		items = append(items, item)
	}
	return &v1.MyListRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// ---------- 后台：活动管理 ----------

// AdminCreate 创建拼团活动：校验商品/SKU/价格/人数/有效期/时间窗后写入，初始为启用。
func (s *sGroupBuy) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	name, err := s.validateActivityInput(ctx, req.ProductId, req.SkuId, req.Name, req.GroupPrice,
		req.GroupSize, req.TeamTtlMinutes, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	startValue, err := activityTimeValue(ctx, req.StartTime)
	if err != nil {
		return nil, err
	}
	endValue, err := activityTimeValue(ctx, req.EndTime)
	if err != nil {
		return nil, err
	}
	id, err := g.DB().Model("group_buy_activities").Ctx(ctx).Data(g.Map{
		"product_id":       req.ProductId,
		"sku_id":           req.SkuId,
		"name":             name,
		"group_price":      req.GroupPrice,
		"group_size":       req.GroupSize,
		"team_ttl_minutes": req.TeamTtlMinutes,
		"start_time":       startValue,
		"end_time":         endValue,
		"status":           activityStatusEnabled,
	}).InsertAndGetId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入拼团活动: %w", err))
	}
	detail, err := s.ActivityDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	logx.Info(ctx, "group_buy_activity_created", "拼团活动已创建", logx.Fields{
		"activity_id": id,
		"product_id":  req.ProductId,
		"sku_id":      req.SkuId,
	})
	return &v1.AdminCreateRes{Activity: detail.Activity}, nil
}

// AdminUpdate 修改拼团活动；product_id/sku_id 不可变，已开的团使用快照不受影响。
func (s *sGroupBuy) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error) {
	if req == nil || req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	current, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, codes.New(codes.CodeGroupBuyNotFound)
	}
	name, err := s.validateActivityInput(ctx, current.ProductId, current.SkuId, req.Name, req.GroupPrice,
		req.GroupSize, req.TeamTtlMinutes, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	startValue, err := activityTimeValue(ctx, req.StartTime)
	if err != nil {
		return nil, err
	}
	endValue, err := activityTimeValue(ctx, req.EndTime)
	if err != nil {
		return nil, err
	}
	if _, err := g.DB().Model("group_buy_activities").Ctx(ctx).Where("id", req.Id).Data(g.Map{
		"name":             name,
		"group_price":      req.GroupPrice,
		"group_size":       req.GroupSize,
		"team_ttl_minutes": req.TeamTtlMinutes,
		"start_time":       startValue,
		"end_time":         endValue,
	}).Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新拼团活动: %w", err))
	}
	detail, err := s.ActivityDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	logx.Info(ctx, "group_buy_activity_updated", "拼团活动已更新", logx.Fields{"activity_id": req.Id})
	return &v1.AdminUpdateRes{Activity: detail.Activity}, nil
}

// AdminStatus 启用/停用活动；停用不影响已开的团（团使用自身快照）。
func (s *sGroupBuy) AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (*v1.AdminStatusRes, error) {
	if req == nil || req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	status, err := parseActivityStatus(req.Status)
	if err != nil {
		return nil, err
	}
	current, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, codes.New(codes.CodeGroupBuyNotFound)
	}
	if _, err := g.DB().Model("group_buy_activities").Ctx(ctx).
		Where("id", req.Id).Data(g.Map{"status": status}).Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新拼团活动状态: %w", err))
	}
	detail, err := s.ActivityDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	logx.Info(ctx, "group_buy_activity_status", "拼团活动状态已变更", logx.Fields{
		"activity_id": req.Id,
		"status":      req.Status,
	})
	return &v1.AdminStatusRes{Activity: detail.Activity}, nil
}

// AdminList 后台活动分页列表（可查看全部状态）。
func (s *sGroupBuy) AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error) {
	page, size, err := parsePage(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("group_buy_activities").Ctx(ctx)
	if req.Status != "" {
		status, err := parseActivityStatus(req.Status)
		if err != nil {
			return nil, err
		}
		model = model.Where("status", status)
	}
	if req.ProductId != nil {
		if *req.ProductId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("product_id", *req.ProductId)
	}
	if req.SkuId != nil {
		if *req.SkuId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("sku_id", *req.SkuId)
	}
	total, err := model.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计拼团活动: %w", err))
	}
	var rows []*activityRow
	if err := model.Order("id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团活动: %w", err))
	}
	metas, err := s.loadActivityMetas(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Activity, 0, len(rows))
	for _, r := range rows {
		items = append(items, toActivity(r, metas[r.Id]))
	}
	return &v1.AdminListRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminDetail 后台活动详情。
func (s *sGroupBuy) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	detail, err := s.ActivityDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.AdminDetailRes{Activity: detail.Activity}, nil
}

// AdminListTeams 后台团分页列表。
func (s *sGroupBuy) AdminListTeams(ctx context.Context, req *v1.AdminListTeamsReq) (*v1.AdminListTeamsRes, error) {
	page, size, err := parsePage(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("group_buy_teams").Ctx(ctx)
	if req.ActivityId != nil {
		if *req.ActivityId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("activity_id", *req.ActivityId)
	}
	if req.Status != "" {
		status, err := parseTeamStatus(req.Status)
		if err != nil {
			return nil, err
		}
		model = model.Where("status", status)
	}
	if req.LeaderId != nil {
		if *req.LeaderId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("leader_user_id", *req.LeaderId)
	}
	total, err := model.Clone().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计拼团: %w", err))
	}
	var rows []*teamRow
	if err := model.Order("id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团: %w", err))
	}
	items := make([]*v1.Team, 0, len(rows))
	for _, r := range rows {
		items = append(items, toTeam(r))
	}
	return &v1.AdminListTeamsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminTeamDetail 后台团详情（userID=0 表示不计算「我的参与」）。
func (s *sGroupBuy) AdminTeamDetail(ctx context.Context, id int64) (*v1.AdminTeamDetailRes, error) {
	detail, err := s.loadTeamDetail(ctx, 0, id)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	return &v1.AdminTeamDetailRes{TeamDetail: *detail}, nil
}

// AdminCloseTeam 运营关闭团（pending→failed），同事务取消该团未支付订单并恢复库存。
//
// 失败补偿边界：已支付订单不伪造退款，保持 paid 供用户走现有退款流程（/orders/:id/refund）；
// 自动退款依赖退款模块的「系统发起」能力，当前不存在，见 docs/design/refund.md。
func (s *sGroupBuy) AdminCloseTeam(ctx context.Context, adminID, teamID int64) (*v1.AdminCloseTeamRes, error) {
	if teamID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	// 先区分「团不存在」与「状态不允许关闭」，再进入条件更新；并发下仍以条件更新结果为准。
	var teams []*teamRow
	if err := g.DB().Model("group_buy_teams").Ctx(ctx).Where("id", teamID).Scan(&teams); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团: %w", err))
	}
	if len(teams) == 0 {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	cancelled, err := s.terminateTeam(ctx, teamID, teamStatusFailed, false)
	if err != nil {
		return nil, err
	}
	if cancelled < 0 {
		return nil, codes.New(codes.CodeGroupBuyTeamNotClosable)
	}
	detail, err := s.loadTeamDetail(ctx, 0, teamID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, codes.New(codes.CodeGroupBuyTeamNotFound)
	}
	logx.Info(ctx, "group_buy_team_closed", "拼团已被运营关闭", logx.Fields{
		"team_id":           teamID,
		"admin_id":          adminID,
		"cancelled_orders":  cancelled,
		"final_team_status": detail.Status,
	})
	return &v1.AdminCloseTeamRes{TeamDetail: *detail}, nil
}

// ExpireTeams 扫描并推进过期团（pending 且 expire_at <= NOW()）为 expired，
// 同时取消未支付订单恢复库存；返回本轮实际推进的团数量。
//
// 并发安全：团状态推进为条件更新（status=10 AND expire_at<=NOW()），与参团的
// 「status=10 AND expire_at>NOW()」闸门互斥；多实例扫描只有命中者处理订单与库存。
func (s *sGroupBuy) ExpireTeams(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*teamRow
	if err := g.DB().Model("group_buy_teams").Ctx(ctx).
		Fields("id").
		Where("status", teamStatusPending).
		Where("expire_at <= NOW()").
		Limit(limit).
		Scan(&rows); err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("扫描过期拼团: %w", err))
	}
	count := 0
	for _, r := range rows {
		cancelled, err := s.terminateTeam(ctx, r.Id, teamStatusExpired, true)
		if err != nil {
			logx.Warning(ctx, "group_buy_expire_failed", "拼团过期处理失败", logx.Fields{
				"team_id": r.Id,
				"error":   err.Error(),
			})
			continue
		}
		if cancelled < 0 {
			continue // 条件更新未命中：已被成团/关闭/其他实例处理
		}
		count++
	}
	return count, nil
}

// ---------- 内部：状态推进与补偿 ----------

// terminateTeam 在单事务内把 pending 团推进到指定终态，并取消该团未支付订单。
// onlyExpired=true 时额外要求 expire_at <= NOW()（与参团闸门互斥）。
// 返回实际取消的订单数；返回 -1 表示条件更新未命中（团已非 pending 或不满足过期条件）。
func (s *sGroupBuy) terminateTeam(ctx context.Context, teamID int64, target int, onlyExpired bool) (int, error) {
	cancelled := 0
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		model := tx.Model("group_buy_teams").Ctx(ctx).
			Where("id", teamID).
			Where("status", teamStatusPending)
		if onlyExpired {
			model = model.Where("expire_at <= NOW()")
		}
		result, e := model.Data(g.Map{
			"status":    target,
			"closed_at": gdb.Raw("NOW()"),
		}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进拼团终态: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			cancelled = -1
			return nil
		}

		var participants []*participantRow
		if e := tx.Model("group_buy_participants").Ctx(ctx).
			Where("team_id", teamID).
			Where("status", participantStatusActive).
			Scan(&participants); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团参与记录: %w", e))
		}
		for _, p := range participants {
			if p.OrderId == nil {
				continue
			}
			// 未支付订单：条件取消 + 恢复库存；已支付/已取消订单返回 false 且无副作用。
			changed, e := service.Order().CancelInTx(ctx, tx, *p.OrderId)
			if e != nil {
				return e
			}
			if changed {
				cancelled++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return cancelled, nil
}

// ---------- 内部：查询辅助 ----------

// findActivity 按 id 查询活动，不存在返回 nil。
func (s *sGroupBuy) findActivity(ctx context.Context, id int64) (*activityRow, error) {
	var rows []*activityRow
	if err := g.DB().Model("group_buy_activities").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团活动: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findActivityInTx 在事务内按 id 查询活动，不存在返回 nil。
func findActivityInTx(ctx context.Context, tx gdb.TX, id int64) (*activityRow, error) {
	var rows []*activityRow
	if err := tx.Model("group_buy_activities").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团活动: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findActivitiesByIDs 批量查询活动，返回 id→行 的映射（缺失的 id 不出现在结果中）。
func (s *sGroupBuy) findActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]*activityRow, error) {
	result := make(map[int64]*activityRow, len(ids))
	unique := uniquePositive(ids)
	if len(unique) == 0 {
		return result, nil
	}
	var rows []*activityRow
	if err := g.DB().Model("group_buy_activities").Ctx(ctx).WhereIn("id", unique).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("批量查询拼团活动: %w", err))
	}
	for _, r := range rows {
		result[r.Id] = r
	}
	return result, nil
}

// findParticipant 按 (team_id,user_id) 查询参与记录，不存在返回 nil。
func (s *sGroupBuy) findParticipant(ctx context.Context, teamID, userID int64) (*participantRow, error) {
	var rows []*participantRow
	if err := g.DB().Model("group_buy_participants").Ctx(ctx).
		Where("team_id", teamID).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团参与记录: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadTeamDetail 组装团详情（团 + 活动/SKU/商品摘要 + 成员 + 当前用户参与情况）。
// userID<=0 表示不填「我的参与」。团不存在返回 (nil,nil)。
func (s *sGroupBuy) loadTeamDetail(ctx context.Context, userID, teamID int64) (*v1.TeamDetail, error) {
	var teams []*teamRow
	if err := g.DB().Model("group_buy_teams").Ctx(ctx).Where("id", teamID).Scan(&teams); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团: %w", err))
	}
	if len(teams) == 0 {
		return nil, nil
	}
	team := teams[0]

	meta, err := s.loadTeamMeta(ctx, team.ActivityId)
	if err != nil {
		return nil, err
	}

	var participants []*participantRow
	if err := g.DB().Model("group_buy_participants").Ctx(ctx).
		Where("team_id", teamID).Order("id").Scan(&participants); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团成员: %w", err))
	}
	userIDs := make([]int64, 0, len(participants))
	for _, p := range participants {
		userIDs = append(userIDs, p.UserId)
	}
	usernames, err := s.loadUsernames(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	detail := &v1.TeamDetail{
		Team:             *toTeam(team),
		ActivityName:     meta.ActivityName,
		ProductId:        meta.ProductId,
		SkuId:            meta.SkuId,
		SkuName:          meta.SkuName,
		ProductName:      meta.ProductName,
		ProductMainImage: meta.ProductMainImage,
		Participants:     make([]*v1.Participant, 0, len(participants)),
	}
	for _, p := range participants {
		item := &v1.Participant{
			Id:        p.Id,
			TeamId:    p.TeamId,
			UserId:    p.UserId,
			Username:  usernames[p.UserId],
			Status:    participantStatusToString(p.Status),
			CreatedAt: p.CreatedAt,
		}
		detail.Participants = append(detail.Participants, item)
		if userID > 0 && p.UserId == userID {
			detail.MyParticipant = item
			detail.MyOrderId = p.OrderId
		}
	}
	return detail, nil
}

// loadTeamMeta 读取团关联的活动与商品/SKU 摘要。
func (s *sGroupBuy) loadTeamMeta(ctx context.Context, activityID int64) (*teamMeta, error) {
	meta := &teamMeta{}
	activity, err := s.findActivity(ctx, activityID)
	if err != nil {
		return nil, err
	}
	if activity == nil {
		return meta, nil
	}
	meta.ActivityName = activity.Name
	meta.ProductId = activity.ProductId
	meta.SkuId = activity.SkuId

	var skus []*skuSummary
	if err := g.DB().Model("skus").Ctx(ctx).Fields("id", "name").
		Where("id", activity.SkuId).Scan(&skus); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团 SKU: %w", err))
	}
	if len(skus) > 0 {
		meta.SkuName = skus[0].Name
	}
	var products []*productSummary
	if err := g.DB().Model("products").Ctx(ctx).Fields("id", "name", "main_image").
		Where("id", activity.ProductId).Scan(&products); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团商品: %w", err))
	}
	if len(products) > 0 {
		meta.ProductName = products[0].Name
		meta.ProductMainImage = products[0].MainImage
	}
	return meta, nil
}

// loadActivityMetas 批量加载活动摘要（SKU/商品名称与价格、进行中团数量），避免 N+1。
func (s *sGroupBuy) loadActivityMetas(ctx context.Context, rows []*activityRow) (map[int64]*activityMeta, error) {
	result := make(map[int64]*activityMeta, len(rows))
	if len(rows) == 0 {
		return result, nil
	}
	skuIDs := make([]int64, 0, len(rows))
	productIDs := make([]int64, 0, len(rows))
	activityIDs := make([]int64, 0, len(rows))
	for _, r := range rows {
		skuIDs = append(skuIDs, r.SkuId)
		productIDs = append(productIDs, r.ProductId)
		activityIDs = append(activityIDs, r.Id)
		result[r.Id] = &activityMeta{}
	}

	var skus []*skuSummary
	if err := g.DB().Model("skus").Ctx(ctx).Fields("id", "product_id", "name", "price").
		WhereIn("id", uniquePositive(skuIDs)).Scan(&skus); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团 SKU 摘要: %w", err))
	}
	skuByID := make(map[int64]int64, len(skus))
	for _, r := range skus {
		skuByID[r.Id] = r.ProductId
	}
	var products []*productSummary
	if err := g.DB().Model("products").Ctx(ctx).Fields("id", "name", "main_image").
		WhereIn("id", uniquePositive(productIDs)).Scan(&products); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团商品摘要: %w", err))
	}
	productByID := make(map[int64]*productSummary, len(products))
	for _, r := range products {
		productByID[r.Id] = r
	}

	var counts []*activityTeamCount
	if err := g.DB().Model("group_buy_teams").Ctx(ctx).
		Fields("activity_id", "COUNT(*) AS total").
		WhereIn("activity_id", uniquePositive(activityIDs)).
		Where("status", teamStatusPending).
		Where("expire_at > NOW()").
		Group("activity_id").
		Scan(&counts); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计进行中拼团: %w", err))
	}
	countByActivity := make(map[int64]int64, len(counts))
	for _, c := range counts {
		countByActivity[c.ActivityId] = c.Total
	}

	for _, row := range rows {
		meta := result[row.Id]
		meta.OngoingTeams = countByActivity[row.Id]
		for _, sku := range skus {
			if sku.Id == row.SkuId {
				meta.SkuName = sku.Name
				meta.SkuPrice = sku.Price
				break
			}
		}
		if p := productByID[skuByID[row.SkuId]]; p != nil {
			meta.ProductName = p.Name
			meta.ProductMainImage = p.MainImage
		} else if p := productByID[row.ProductId]; p != nil {
			meta.ProductName = p.Name
			meta.ProductMainImage = p.MainImage
		}
	}
	return result, nil
}

// loadUsernames 批量查询用户名（参与成员展示），返回 id→username；缺失为空串。
func (s *sGroupBuy) loadUsernames(ctx context.Context, ids []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(ids))
	unique := uniquePositive(ids)
	if len(unique) == 0 {
		return result, nil
	}
	var rows []*userNameRow
	if err := g.DB().Model("users").Ctx(ctx).Fields("id", "username").WhereIn("id", unique).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询拼团成员用户名: %w", err))
	}
	for _, r := range rows {
		result[r.Id] = r.Username
	}
	return result, nil
}

// ---------- 内部：校验 ----------

// validateActivityInput 校验活动字段与商品/SKU 可售性，返回规范化后的活动名称。
func (s *sGroupBuy) validateActivityInput(
	ctx context.Context,
	productID, skuID int64,
	name string,
	groupPrice, groupSize, ttlMinutes int64,
	startTime, endTime *gtime.Time,
) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len([]rune(trimmed)) > maxNameLen {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	if productID <= 0 || skuID <= 0 {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	if groupPrice <= 0 || groupPrice > maxPrice {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	if groupSize < minGroupSize || groupSize > maxGroupSize {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	if ttlMinutes < minTeamTTLMinutes || ttlMinutes > maxTeamTTLMinutes {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	if startTime == nil || endTime == nil || endTime.Unix() <= startTime.Unix() {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}

	sku, err := service.Sku().GetByID(ctx, skuID)
	if err != nil {
		return "", err
	}
	if sku == nil || sku.Status != skuv1.StatusEnabled {
		return "", codes.New(codes.CodeGroupBuySkuUnavailable)
	}
	if sku.ProductId != productID {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	product, err := service.Product().GetByID(ctx, productID)
	if err != nil {
		return "", err
	}
	if product == nil || product.Status != productv1.StatusOnShelf {
		return "", codes.New(codes.CodeGroupBuySkuUnavailable)
	}
	// 拼团价不得高于 SKU 现价：否则订单会出现负优惠，破坏订单金额语义。
	if groupPrice > sku.Price {
		return "", codes.New(codes.CodeGroupBuyPriceInvalid)
	}
	return trimmed, nil
}

// activityTimeValue 把请求时间转为业务时区（dashboard.timezone）的 MySQL DATETIME 字面量。
//
// 不能直接把 *gtime.Time 交给 gdb 写库：gdb 会按 UTC 格式化时间，而连接层 time_zone 已设为
// 业务时区（boot.databaseTimeZoneExtra），直接写入会让活动时间窗与 SQL NOW() 相差 8 小时。
func activityTimeValue(ctx context.Context, t *gtime.Time) (string, error) {
	if t == nil {
		return "", codes.New(codes.CodeGroupBuyInvalidArgument)
	}
	name, err := config.String(ctx, config.KeyDashboardTimezone)
	if err != nil {
		return "", err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return "", codes.Wrap(codes.CodeInternalError, fmt.Errorf("加载业务时区 %q: %w", name, err))
	}
	return t.Time.In(loc).Format("2006-01-02 15:04:05"), nil
}

// activityOpenInTx 在事务内用 MySQL NOW() 判定活动是否可开团：
// 启用且 start_time <= NOW() < end_time（左闭右开）。
func activityOpenInTx(ctx context.Context, tx gdb.TX, activityID int64) (bool, error) {
	count, err := tx.Model("group_buy_activities").Ctx(ctx).
		Where("id", activityID).
		Where("status", activityStatusEnabled).
		Where("start_time <= NOW()").
		Where("end_time > NOW()").
		Count()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("判定拼团活动有效性: %w", err))
	}
	return count > 0, nil
}

// parseActivityStatus 把 API 字符串枚举解析为 DB TINYINT。
func parseActivityStatus(raw string) (int, error) {
	switch strings.TrimSpace(raw) {
	case v1.ActivityStatusEnabled:
		return activityStatusEnabled, nil
	case v1.ActivityStatusDisabled:
		return activityStatusDisabled, nil
	default:
		return 0, codes.New(codes.CodeInvalidArgument)
	}
}

// parseTeamStatus 把 API 字符串枚举解析为 DB TINYINT。
func parseTeamStatus(raw string) (int, error) {
	switch strings.TrimSpace(raw) {
	case v1.TeamStatusPending:
		return teamStatusPending, nil
	case v1.TeamStatusSuccess:
		return teamStatusSuccess, nil
	case v1.TeamStatusFailed:
		return teamStatusFailed, nil
	case v1.TeamStatusExpired:
		return teamStatusExpired, nil
	default:
		return 0, codes.New(codes.CodeInvalidArgument)
	}
}

// parsePage 解析分页参数：page<1 或 size 越界返回 400（与后台订单/退款一致）。
func parsePage(page, size *int) (int, int, error) {
	p := defaultPage
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	sz := defaultSize
	if size != nil {
		if *size < 1 || *size > maxSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		sz = *size
	}
	return p, sz, nil
}

// ---------- 内部：转换 ----------

// toActivity 把活动行与摘要转换为对外结构。
func toActivity(row *activityRow, meta *activityMeta) *v1.Activity {
	activity := &v1.Activity{
		Id:             row.Id,
		ProductId:      row.ProductId,
		SkuId:          row.SkuId,
		Name:           row.Name,
		GroupPrice:     row.GroupPrice,
		GroupSize:      row.GroupSize,
		TeamTtlMinutes: row.TeamTtlMinutes,
		StartTime:      row.StartTime,
		EndTime:        row.EndTime,
		Status:         activityStatusToString(row.Status),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if meta != nil {
		activity.SkuName = meta.SkuName
		activity.SkuPrice = meta.SkuPrice
		activity.ProductName = meta.ProductName
		activity.ProductMainImage = meta.ProductMainImage
		activity.OngoingTeams = meta.OngoingTeams
	}
	return activity
}

// toTeam 把团行转换为对外结构。
func toTeam(row *teamRow) *v1.Team {
	return &v1.Team{
		Id:           row.Id,
		ActivityId:   row.ActivityId,
		LeaderUserId: row.LeaderUserId,
		GroupPrice:   row.GroupPrice,
		RequiredSize: row.RequiredSize,
		JoinedCount:  row.JoinedCount,
		Status:       teamStatusToString(row.Status),
		ExpireAt:     row.ExpireAt,
		ClosedAt:     row.ClosedAt,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

// activityStatusToString 把 DB 状态转为 API 字符串枚举。
func activityStatusToString(status int) string {
	if status == activityStatusDisabled {
		return v1.ActivityStatusDisabled
	}
	return v1.ActivityStatusEnabled
}

// teamStatusToString 把 DB 团状态转为 API 字符串枚举。
func teamStatusToString(status int) string {
	switch status {
	case teamStatusSuccess:
		return v1.TeamStatusSuccess
	case teamStatusFailed:
		return v1.TeamStatusFailed
	case teamStatusExpired:
		return v1.TeamStatusExpired
	default:
		return v1.TeamStatusPending
	}
}

// participantStatusToString 把参与状态转为 API 字符串枚举。
func participantStatusToString(status int) string {
	if status == participantStatusActive {
		return v1.ParticipantStatusActive
	}
	return v1.ParticipantStatusQuit
}

// ---------- 内部：通用 ----------

// duplicateKeyName 返回 MySQL 1062 错误的键名（用于把唯一约束冲突映射为业务错误码）。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}

// uniquePositive 去重并过滤非正数，避免空 IN 与重复参数。
func uniquePositive(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
