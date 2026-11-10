// Package bargain 实现砍价业务：后台活动管理、前台活动浏览、用户发起砍价、好友助力、
// 砍价成功/失败/过期状态推进与实例/助力记录查询。
//
// 事实来源为单一 MySQL（复用 B1-03-03 的 bargain_activities / bargain_instances / bargain_help_records）：
//   - 一人一活动至多一个进行中实例：bargain_instances.active_key 生成列 + uk_active_instance 唯一索引兜底；
//   - 同一用户对同一实例至多助力一次：uk_instance_helper 唯一索引；
//   - 助力与实例金额/次数推进在同一事务内完成，先 UPDATE 条件闸门、后 INSERT 助力记录，撞唯一键整体回滚；
//   - 价格下限由条件更新（current_price > floor_price + GREATEST 语义）与 Go 侧 cut 计算共同保证；
//   - 过期采用「读取路径懒推进」（status=10 且 expire_at<=NOW() → 40），重复推进无副作用。
//
// 不引入 Redis/MQ/分布式锁，也不改写订单金额公式：砍价成功后的下单联动留给后续集成任务。
package bargain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/bargain/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 实例状态（DB TINYINT，见 docs/design/marketing.md §3.4 状态机）。
const (
	instanceInProgress = 10 // 进行中
	instanceSuccess    = 20 // 砍价成功（终态）
	instanceFailed     = 30 // 砍价失败（终态）
	instanceExpired    = 40 // 已过期（终态）
)

// 活动状态（DB TINYINT）：仅表达运营启停，时间窗由 start_time/end_time 派生。
const (
	activityEnabled  = 1
	activityDisabled = 0
)

const (
	// maxNameLen 活动名最大字符数（trim 后）。
	maxNameLen = 64
	// maxAmount 金额上限（整数分，= ¥999,999.99，与 SKU/秒杀价/优惠券一致）。
	maxAmount = 99_999_999
	// defaultPageSize 分页默认每页数量。
	defaultPageSize = 20
	// maxPageSize 分页每页数量上限。
	maxPageSize = 100
)

type sBargain struct{}

func init() {
	service.RegisterBargain(New())
}

// New 创建并返回砍价服务实现。
func New() *sBargain {
	return &sBargain{}
}

// activityRow 是 bargain_activities 联合商品/SKU 展示字段的一行。
type activityRow struct {
	Id               int64       `json:"id"`
	ProductId        int64       `json:"product_id"`
	SkuId            int64       `json:"sku_id"`
	Name             string      `json:"name"`
	StartPrice       int64       `json:"start_price"`
	FloorPrice       int64       `json:"floor_price"`
	HelpAmount       int64       `json:"help_amount"`
	MaxHelpCount     int64       `json:"max_help_count"`
	StartTime        *gtime.Time `json:"start_time"`
	EndTime          *gtime.Time `json:"end_time"`
	Status           int         `json:"status"`
	ProductName      string      `json:"product_name"`
	ProductMainImage string      `json:"product_main_image"`
	SkuName          string      `json:"sku_name"`
	CreatedAt        *gtime.Time `json:"created_at"`
	UpdatedAt        *gtime.Time `json:"updated_at"`
	// IsJoinable 仅前台详情使用：SQL 侧按 status + [start_time, end_time) 判定。
	IsJoinable bool `json:"is_joinable"`
}

// instanceRow 是 bargain_instances 联合活动/商品/SKU 展示字段的一行。
type instanceRow struct {
	Id               int64       `json:"id"`
	ActivityId       int64       `json:"activity_id"`
	UserId           int64       `json:"user_id"`
	StartPrice       int64       `json:"start_price"`
	FloorPrice       int64       `json:"floor_price"`
	CurrentPrice     int64       `json:"current_price"`
	HelpedCount      int64       `json:"helped_count"`
	MaxHelpCount     int64       `json:"max_help_count"`
	Status           int         `json:"status"`
	ExpireAt         *gtime.Time `json:"expire_at"`
	CompletedAt      *gtime.Time `json:"completed_at"`
	OrderId          *int64      `json:"order_id"`
	CreatedAt        *gtime.Time `json:"created_at"`
	UpdatedAt        *gtime.Time `json:"updated_at"`
	ActivityName     string      `json:"activity_name"`
	ProductId        int64       `json:"product_id"`
	SkuId            int64       `json:"sku_id"`
	ProductName      string      `json:"product_name"`
	ProductMainImage string      `json:"product_main_image"`
	SkuName          string      `json:"sku_name"`
}

// helpRow 是 bargain_help_records 联合助力者昵称/头像的一行。
type helpRow struct {
	Id              int64       `json:"id"`
	InstanceId      int64       `json:"instance_id"`
	HelperUserId    int64       `json:"helper_user_id"`
	HelperNickname  string      `json:"helper_nickname"`
	HelperAvatarUrl string      `json:"helper_avatar_url"`
	CutAmount       int64       `json:"cut_amount"`
	PriceBefore     int64       `json:"price_before"`
	PriceAfter      int64       `json:"price_after"`
	CreatedAt       *gtime.Time `json:"created_at"`
}

// activityInput 是活动创建/编辑的规范化输入。
type activityInput struct {
	Name         string
	ProductId    int64
	SkuId        int64
	StartPrice   int64
	FloorPrice   int64
	HelpAmount   int64
	MaxHelpCount int64
	StartTime    *gtime.Time
	EndTime      *gtime.Time
}

// AdminCreate 创建砍价活动：参数规范化后落库，商品/SKU 必须存在且 SKU 属于该商品。
func (s *sBargain) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error) {
	in, err := normalizeActivityInput(req.Name, req.ProductId, req.SkuId, req.StartPrice, req.FloorPrice,
		req.HelpAmount, req.MaxHelpCount, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	if err := validateProductSku(ctx, in.ProductId, in.SkuId); err != nil {
		return nil, err
	}

	id, err := g.DB().Model("bargain_activities").Ctx(ctx).Data(g.Map{
		"product_id":     in.ProductId,
		"sku_id":         in.SkuId,
		"name":           in.Name,
		"start_price":    in.StartPrice,
		"floor_price":    in.FloorPrice,
		"help_amount":    in.HelpAmount,
		"max_help_count": in.MaxHelpCount,
		"start_time":     in.StartTime,
		"end_time":       in.EndTime,
		"status":         activityEnabled,
	}).InsertAndGetId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入砍价活动: %w", err))
	}

	row, err := s.findActivity(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建砍价活动后未找到记录"))
	}
	return &v1.AdminCreateRes{Activity: *toActivity(row)}, nil
}

// AdminUpdate 编辑砍价活动。product_id/sku_id 不可变更：已创建实例以活动为展示与价格来源，
// 改绑商品会让在途砍价指向另一件商品；需要换商品时另建活动。
func (s *sBargain) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error) {
	in, err := normalizeActivityInput(req.Name, req.ProductId, req.SkuId, req.StartPrice, req.FloorPrice,
		req.HelpAmount, req.MaxHelpCount, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	existing, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	if existing.ProductId != in.ProductId || existing.SkuId != in.SkuId {
		// 关联商品/SKU 是活动的身份，编辑不允许改绑。
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}

	if _, err := g.DB().Model("bargain_activities").Ctx(ctx).Where("id", req.Id).Data(g.Map{
		"name":           in.Name,
		"start_price":    in.StartPrice,
		"floor_price":    in.FloorPrice,
		"help_amount":    in.HelpAmount,
		"max_help_count": in.MaxHelpCount,
		"start_time":     in.StartTime,
		"end_time":       in.EndTime,
	}).Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新砍价活动: %w", err))
	}

	row, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	return &v1.AdminUpdateRes{Activity: *toActivity(row)}, nil
}

// AdminStatus 启用/停用活动（幂等）。停用只阻止新发起，已创建实例仍按自身快照与 expire_at 推进。
func (s *sBargain) AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (*v1.AdminStatusRes, error) {
	if req.Status != activityEnabled && req.Status != activityDisabled {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	existing, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	if _, err := g.DB().Model("bargain_activities").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{"status": req.Status}).
		Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新砍价活动状态: %w", err))
	}
	row, err := s.findActivity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	return &v1.AdminStatusRes{Activity: *toActivity(row)}, nil
}

// AdminList 后台活动分页列表：支持状态与商品精确筛选，按 id 倒序。
func (s *sBargain) AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error) {
	page, size, err := parsePagination(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	if req.Status != nil && *req.Status != activityEnabled && *req.Status != activityDisabled {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.ProductId != nil && *req.ProductId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	// 每次重建模型：Count 与分页查询共用同一组条件，但不共享可变模型状态。
	build := func() *gdb.Model {
		m := g.DB().Ctx(ctx).Model("bargain_activities a").
			LeftJoin("products p", "p.id = a.product_id").
			LeftJoin("skus sk", "sk.id = a.sku_id")
		if req.Status != nil {
			m = m.Where("a.status", *req.Status)
		}
		if req.ProductId != nil {
			m = m.Where("a.product_id", *req.ProductId)
		}
		return m
	}
	total, err := build().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计砍价活动: %w", err))
	}
	var rows []*activityRow
	if err := build().Fields(activityFields()).Order("a.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价活动列表: %w", err))
	}
	items := make([]*v1.Activity, 0, len(rows))
	for _, r := range rows {
		items = append(items, toActivity(r))
	}
	return &v1.AdminListRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminDetail 后台活动详情（任意状态可见）。
func (s *sBargain) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	row, err := s.findActivity(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	return &v1.AdminDetailRes{Activity: *toActivity(row)}, nil
}

// List 前台可参与活动列表：仅 status=1 且当前处于 [start_time, end_time)，按 id 倒序。
func (s *sBargain) List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error) {
	page, size, err := parsePagination(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	if req.ProductId != nil && *req.ProductId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	build := func() *gdb.Model {
		m := g.DB().Ctx(ctx).Model("bargain_activities a").
			LeftJoin("products p", "p.id = a.product_id").
			LeftJoin("skus sk", "sk.id = a.sku_id").
			Where("a.status", activityEnabled).
			Where("a.start_time <= NOW()").
			Where("a.end_time > NOW()")
		if req.ProductId != nil {
			m = m.Where("a.product_id", *req.ProductId)
		}
		return m
	}
	total, err := build().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计可参与砍价活动: %w", err))
	}
	var rows []*activityRow
	if err := build().Fields(activityFields()).Order("a.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询可参与砍价活动: %w", err))
	}
	items := make([]*v1.Activity, 0, len(rows))
	for _, r := range rows {
		items = append(items, toActivity(r))
	}
	return &v1.ListRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// Detail 前台活动详情：is_joinable 由 SQL 侧用同一 NOW() 判定，避免 Go↔MySQL 时区漂移。
func (s *sBargain) Detail(ctx context.Context, id int64) (*v1.DetailRes, error) {
	var rows []*activityRow
	if err := g.DB().Ctx(ctx).Model("bargain_activities a").
		LeftJoin("products p", "p.id = a.product_id").
		LeftJoin("skus sk", "sk.id = a.sku_id").
		Fields(activityFields(), "(a.status = 1 AND a.start_time <= NOW() AND a.end_time > NOW()) AS is_joinable").
		Where("a.id", id).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价活动详情: %w", err))
	}
	if len(rows) == 0 {
		return nil, codes.New(codes.CodeBargainActivityNotFound)
	}
	return &v1.DetailRes{Activity: *toActivity(rows[0]), IsJoinable: rows[0].IsJoinable}, nil
}

// Start 发起砍价：单条 INSERT ... SELECT 从活动行快照价格与截止时间，
// 把「活动是否存在/可参与」与「写入实例」合并为一个原子语句，避免先查后写的 TOCTOU。
// 并发重复发起由 uk_active_instance 唯一索引兜底（1062 → 21005）。
func (s *sBargain) Start(ctx context.Context, userID, activityID int64) (*v1.StartRes, error) {
	if userID <= 0 || activityID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	result, err := g.DB().Exec(ctx,
		"INSERT INTO bargain_instances "+
			"(activity_id, user_id, start_price, floor_price, current_price, helped_count, max_help_count, status, expire_at) "+
			"SELECT id, ?, start_price, floor_price, start_price, 0, max_help_count, ?, end_time "+
			"FROM bargain_activities "+
			"WHERE id = ? AND status = ? AND start_time <= NOW() AND end_time > NOW()",
		userID, instanceInProgress, activityID, activityEnabled,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeBargainInstanceExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建砍价实例: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取砍价实例写入结果: %w", err))
	}
	if affected == 0 {
		return nil, s.classifyStartRejection(ctx, activityID)
	}
	instanceID, err := result.LastInsertId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取砍价实例 id: %w", err))
	}

	inst, err := s.loadInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建砍价实例后未找到记录"))
	}
	return &v1.StartRes{Instance: *toInstance(inst)}, nil
}

// classifyStartRejection 区分「活动不存在」与「活动当前不可参与」。
func (s *sBargain) classifyStartRejection(ctx context.Context, activityID int64) error {
	n, err := g.DB().Model("bargain_activities").Ctx(ctx).Where("id", activityID).Count()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("判定砍价活动不可参与原因: %w", err))
	}
	if n == 0 {
		return codes.New(codes.CodeBargainActivityNotFound)
	}
	return codes.New(codes.CodeBargainActivityUnavailable)
}

// MyInstances 查询当前用户的砍价记录：先懒推进本人已过期实例，再分页查询。
func (s *sBargain) MyInstances(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	page, size, err := parsePagination(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	if err := s.expireInstancesOfUser(ctx, userID); err != nil {
		return nil, err
	}

	build := func() *gdb.Model {
		return g.DB().Ctx(ctx).Model("bargain_instances i").
			LeftJoin("bargain_activities a", "a.id = i.activity_id").
			LeftJoin("products p", "p.id = a.product_id").
			LeftJoin("skus sk", "sk.id = a.sku_id").
			Where("i.user_id", userID)
	}
	total, err := build().Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计我的砍价记录: %w", err))
	}
	var rows []*instanceRow
	if err := build().Fields(instanceFields()).Order("i.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询我的砍价记录: %w", err))
	}
	items := make([]*v1.Instance, 0, len(rows))
	for _, r := range rows {
		items = append(items, toInstance(r))
	}
	return &v1.MyListRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// InstanceDetail 查询实例详情：任何登录用户均可查看（好友需要打开分享链接助力）。
func (s *sBargain) InstanceDetail(ctx context.Context, id int64) (*v1.InstanceDetailRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if err := s.expireInstance(ctx, id); err != nil {
		return nil, err
	}
	inst, err := s.loadInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, codes.New(codes.CodeBargainInstanceNotFound)
	}
	return &v1.InstanceDetailRes{Instance: *toInstance(inst)}, nil
}

// Help 好友助力：单事务内完成「锁定实例行 → 资格校验 → 条件推进价格 → 写助力记录 → 达底价置成功」。
//
// 并发语义：
//   - SELECT ... FOR UPDATE 先串行化同一实例上的所有助力，读到的 current_price/helped_count 是最新已提交值；
//   - UPDATE 仍带全量条件闸门（status/次数/价格下限/未过期），即使锁语义变化也不会突破不变量；
//   - 助力记录插入撞 uk_instance_helper（1062）→ 返回 21008 并整体回滚，价格与次数一并回滚；
//   - 最后一次助力只砍剩余差额（cut = min(help_amount, current_price - floor_price)），价格永不低于底价。
func (s *sBargain) Help(ctx context.Context, helperID, instanceID int64) (*v1.HelpRes, error) {
	if helperID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if instanceID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	var helpID int64
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inst, err := lockInstanceInTx(ctx, tx, instanceID)
		if err != nil {
			return err
		}
		if inst == nil {
			return codes.New(codes.CodeBargainInstanceNotFound)
		}
		if inst.UserId == helperID {
			return codes.New(codes.CodeBargainSelfHelpForbidden)
		}
		if inst.Status != instanceInProgress || inst.Expired {
			return codes.New(codes.CodeBargainHelpUnavailable)
		}
		if inst.HelpedCount >= inst.MaxHelpCount {
			return codes.New(codes.CodeBargainHelpUnavailable)
		}

		helpAmount, err := activityHelpAmountInTx(ctx, tx, inst.ActivityId)
		if err != nil {
			return err
		}
		cut := calcCutAmount(helpAmount, inst.CurrentPrice, inst.FloorPrice)
		if cut <= 0 {
			return codes.New(codes.CodeBargainHelpUnavailable)
		}
		priceAfter := inst.CurrentPrice - cut

		result, err := tx.Ctx(ctx).Exec(
			"UPDATE bargain_instances SET current_price = current_price - ?, helped_count = helped_count + 1 "+
				"WHERE id = ? AND status = ? AND helped_count < max_help_count "+
				"AND current_price > floor_price AND expire_at > NOW()",
			cut, instanceID, instanceInProgress,
		)
		if err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进砍价实例金额: %w", err))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeBargainHelpUnavailable)
		}

		helpID, err = tx.Model("bargain_help_records").Ctx(ctx).Data(g.Map{
			"instance_id":    instanceID,
			"helper_user_id": helperID,
			"cut_amount":     cut,
			"price_before":   inst.CurrentPrice,
			"price_after":    priceAfter,
		}).InsertAndGetId()
		if err != nil {
			if isDuplicateKeyError(err) {
				// 撞 uk_instance_helper：整体回滚，价格与次数不落库。
				return codes.New(codes.CodeBargainHelpDuplicated)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入砍价助力记录: %w", err))
		}

		if priceAfter <= inst.FloorPrice {
			if _, err := tx.Ctx(ctx).Exec(
				"UPDATE bargain_instances SET status = ?, completed_at = NOW() WHERE id = ? AND status = ?",
				instanceSuccess, instanceID, instanceInProgress,
			); err != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进砍价实例成功状态: %w", err))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	inst, err := s.loadInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("助力后未找到砍价实例"))
	}
	help, err := s.findHelpRecord(ctx, helpID)
	if err != nil {
		return nil, err
	}
	return &v1.HelpRes{Instance: *toInstance(inst), Help: help}, nil
}

// Helps 查询实例的助力记录（按 id 倒序，带助力者昵称/头像展示字段）。
func (s *sBargain) Helps(ctx context.Context, instanceID int64) (*v1.HelpsRes, error) {
	if instanceID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("bargain_instances").Ctx(ctx).Where("id", instanceID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价实例存在性: %w", err))
	}
	if n == 0 {
		return nil, codes.New(codes.CodeBargainInstanceNotFound)
	}

	var rows []*helpRow
	if err := g.DB().Ctx(ctx).Model("bargain_help_records h").
		Fields(
			"h.id", "h.instance_id", "h.helper_user_id", "h.cut_amount",
			"h.price_before", "h.price_after", "h.created_at",
			"COALESCE(u.nickname, '') AS helper_nickname",
			"COALESCE(u.avatar_url, '') AS helper_avatar_url",
		).
		LeftJoin("users u", "u.id = h.helper_user_id").
		Where("h.instance_id", instanceID).
		Order("h.id DESC").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价助力记录: %w", err))
	}
	items := make([]*v1.HelpRecord, 0, len(rows))
	for _, r := range rows {
		items = append(items, toHelpRecord(r))
	}
	return &v1.HelpsRes{Items: items}, nil
}

// Abandon 发起者放弃砍价：进行中 → 失败（终态），active_key 变 NULL 后允许重新发起。
// 终态实例不能被放弃（21009），非本人/不存在统一 21004。
func (s *sBargain) Abandon(ctx context.Context, userID, instanceID int64) (*v1.AbandonRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if instanceID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	// 先懒推进过期，避免把「已过了截止时间但状态仍为进行中」的实例当作放弃成功。
	if err := s.expireInstance(ctx, instanceID); err != nil {
		return nil, err
	}

	result, err := g.DB().Model("bargain_instances").Ctx(ctx).
		Where("id", instanceID).
		Where("user_id", userID).
		Where("status", instanceInProgress).
		Data(g.Map{"status": instanceFailed}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("放弃砍价: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		n, err := g.DB().Model("bargain_instances").Ctx(ctx).
			Where("id", instanceID).
			Where("user_id", userID).
			Count()
		if err != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("判定放弃砍价失败原因: %w", err))
		}
		if n == 0 {
			return nil, codes.New(codes.CodeBargainInstanceNotFound)
		}
		return nil, codes.New(codes.CodeBargainInvalidStatusTransition)
	}

	inst, err := s.loadInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, codes.New(codes.CodeBargainInstanceNotFound)
	}
	return &v1.AbandonRes{Instance: *toInstance(inst)}, nil
}

// findActivity 按 id 查询活动（含商品/SKU 展示字段），未命中返回 nil。
func (s *sBargain) findActivity(ctx context.Context, id int64) (*activityRow, error) {
	var rows []*activityRow
	if err := g.DB().Ctx(ctx).Model("bargain_activities a").
		LeftJoin("products p", "p.id = a.product_id").
		LeftJoin("skus sk", "sk.id = a.sku_id").
		Fields(activityFields()).
		Where("a.id", id).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询砍价活动: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// loadInstance 按 id 查询实例（含活动/商品/SKU 展示字段），未命中返回 nil。
func (s *sBargain) loadInstance(ctx context.Context, id int64) (*instanceRow, error) {
	var rows []*instanceRow
	if err := g.DB().Ctx(ctx).Model("bargain_instances i").
		LeftJoin("bargain_activities a", "a.id = i.activity_id").
		LeftJoin("products p", "p.id = a.product_id").
		LeftJoin("skus sk", "sk.id = a.sku_id").
		Fields(instanceFields()).
		Where("i.id", id).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询砍价实例: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findHelpRecord 查询单条助力记录（含助力者展示字段）。
func (s *sBargain) findHelpRecord(ctx context.Context, id int64) (*v1.HelpRecord, error) {
	var rows []*helpRow
	if err := g.DB().Ctx(ctx).Model("bargain_help_records h").
		Fields(
			"h.id", "h.instance_id", "h.helper_user_id", "h.cut_amount",
			"h.price_before", "h.price_after", "h.created_at",
			"COALESCE(u.nickname, '') AS helper_nickname",
			"COALESCE(u.avatar_url, '') AS helper_avatar_url",
		).
		LeftJoin("users u", "u.id = h.helper_user_id").
		Where("h.id", id).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价助力记录: %w", err))
	}
	if len(rows) == 0 {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("助力后未找到助力记录"))
	}
	return toHelpRecord(rows[0]), nil
}

// expireInstance 懒推进单个实例：status=10 且 expire_at<=NOW() → 40。
// 条件更新保证重复执行无副作用（第二次 RowsAffected=0），也不改 completed_at。
func (s *sBargain) expireInstance(ctx context.Context, id int64) error {
	if _, err := g.DB().Model("bargain_instances").Ctx(ctx).
		Where("id", id).
		Where("status", instanceInProgress).
		Where("expire_at <= NOW()").
		Data(g.Map{"status": instanceExpired}).
		Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进过期砍价实例: %w", err))
	}
	return nil
}

// expireInstancesOfUser 懒推进当前用户全部已过期实例（一条条件更新，避免逐行往返）。
func (s *sBargain) expireInstancesOfUser(ctx context.Context, userID int64) error {
	if _, err := g.DB().Model("bargain_instances").Ctx(ctx).
		Where("user_id", userID).
		Where("status", instanceInProgress).
		Where("expire_at <= NOW()").
		Data(g.Map{"status": instanceExpired}).
		Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进用户过期砍价实例: %w", err))
	}
	return nil
}

// lockedInstance 是事务内 FOR UPDATE 读到的实例关键字段。
type lockedInstance struct {
	Id           int64 `json:"id"`
	ActivityId   int64 `json:"activity_id"`
	UserId       int64 `json:"user_id"`
	CurrentPrice int64 `json:"current_price"`
	FloorPrice   int64 `json:"floor_price"`
	HelpedCount  int64 `json:"helped_count"`
	MaxHelpCount int64 `json:"max_help_count"`
	Status       int   `json:"status"`
	// Expired 由 SQL NOW() 判定，避免 Go↔MySQL 时区漂移。
	Expired bool `json:"expired"`
}

// lockInstanceInTx 在事务内以 FOR UPDATE 锁定实例行；未命中返回 nil。
// 行锁把同一实例上的并发助力串行化，保证读到的价格/次数是最新已提交值。
func lockInstanceInTx(ctx context.Context, tx gdb.TX, instanceID int64) (*lockedInstance, error) {
	var rows []*lockedInstance
	if err := tx.Ctx(ctx).Model("bargain_instances").
		Fields("id", "activity_id", "user_id", "current_price", "floor_price", "helped_count",
			"max_help_count", "status", "(expire_at <= NOW()) AS expired").
		Where("id", instanceID).
		LockUpdate().
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定砍价实例: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// activityHelpAmountInTx 读取活动当前的每次助力名义砍减额。
// help_amount 不在实例上快照（DDL 事实：实例只快照 start/floor/max_help_count/expire_at），
// 因此助力额以活动当前配置为准；价格边界仍由实例 floor_price 快照保证。
func activityHelpAmountInTx(ctx context.Context, tx gdb.TX, activityID int64) (int64, error) {
	var rows []struct {
		HelpAmount int64 `json:"help_amount"`
	}
	if err := tx.Ctx(ctx).Model("bargain_activities").
		Fields("help_amount").
		Where("id", activityID).
		Scan(&rows); err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询砍价活动助力额: %w", err))
	}
	if len(rows) == 0 {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("砍价实例引用的活动不存在: activity_id=%d", activityID))
	}
	return rows[0].HelpAmount, nil
}

// activityFields 返回活动联查字段（联表别名固定为 a/p/sk）。
func activityFields() string {
	return "a.id, a.product_id, a.sku_id, a.name, a.start_price, a.floor_price, a.help_amount, " +
		"a.max_help_count, a.start_time, a.end_time, a.status, a.created_at, a.updated_at, " +
		"COALESCE(p.name, '') AS product_name, COALESCE(p.main_image, '') AS product_main_image, " +
		"COALESCE(sk.name, '') AS sku_name"
}

// instanceFields 返回实例联查字段（联表别名固定为 i/a/p/sk）。
func instanceFields() string {
	return "i.id, i.activity_id, i.user_id, i.start_price, i.floor_price, i.current_price, " +
		"i.helped_count, i.max_help_count, i.status, i.expire_at, i.completed_at, i.order_id, " +
		"i.created_at, i.updated_at, " +
		"COALESCE(a.name, '') AS activity_name, COALESCE(a.product_id, 0) AS product_id, " +
		"COALESCE(a.sku_id, 0) AS sku_id, COALESCE(p.name, '') AS product_name, " +
		"COALESCE(p.main_image, '') AS product_main_image, COALESCE(sk.name, '') AS sku_name"
}

// normalizeActivityInput 校验并规范化活动参数（价格/时间/助力参数/可达成性）。
func normalizeActivityInput(name string, productID, skuID, startPrice, floorPrice, helpAmount, maxHelpCount int64,
	startTime, endTime *gtime.Time) (*activityInput, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if productID <= 0 || skuID <= 0 {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if startPrice < 2 || startPrice > maxAmount {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if floorPrice < 1 || floorPrice >= startPrice {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if helpAmount < 1 || helpAmount > maxAmount {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if maxHelpCount < 1 {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	// 可达成性：助力次数上限必须足以从 start_price 砍到 floor_price，否则活动永远无法成功。
	if required := helpsRequired(startPrice-floorPrice, helpAmount); required > maxHelpCount {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if startTime == nil || endTime == nil {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if endTime.Unix() <= startTime.Unix() {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	if endTime.Unix() <= time.Now().Unix() {
		return nil, codes.New(codes.CodeBargainActivityInvalid)
	}
	return &activityInput{
		Name:         name,
		ProductId:    productID,
		SkuId:        skuID,
		StartPrice:   startPrice,
		FloorPrice:   floorPrice,
		HelpAmount:   helpAmount,
		MaxHelpCount: maxHelpCount,
		StartTime:    startTime,
		EndTime:      endTime,
	}, nil
}

// helpsRequired 返回砍完 gap 至少需要的助力次数（向上取整）；gap<=0 视为 0 次。
func helpsRequired(gap, helpAmount int64) int64 {
	if gap <= 0 {
		return 0
	}
	if helpAmount <= 0 {
		return gap // 非法助力额：让调用方必然判定不可达成
	}
	return (gap + helpAmount - 1) / helpAmount
}

// calcCutAmount 计算本次实际砍减额：min(help_amount, current_price - floor_price)。
// 最后一次助力只砍剩余差额，保证 price_after 恰好落在 floor_price 且永不为负。
func calcCutAmount(helpAmount, currentPrice, floorPrice int64) int64 {
	if helpAmount <= 0 || currentPrice <= floorPrice {
		return 0
	}
	remaining := currentPrice - floorPrice
	if helpAmount > remaining {
		return remaining
	}
	return helpAmount
}

// validateProductSku 校验商品存在、SKU 存在且属于该商品。
// 归属错误与不存在统一返回 21002，避免在活动配置错误上叠加枚举面。
func validateProductSku(ctx context.Context, productID, skuID int64) error {
	exists, err := service.Product().Exists(ctx, productID)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验砍价活动商品: %w", err))
	}
	if !exists {
		return codes.New(codes.CodeBargainActivityInvalid)
	}
	sku, err := service.Sku().GetByID(ctx, skuID)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验砍价活动 SKU: %w", err))
	}
	if sku == nil || sku.ProductId != productID {
		return codes.New(codes.CodeBargainActivityInvalid)
	}
	return nil
}

// parsePagination 解析分页参数：默认 1/20，size 上限 100；非法值返回 1001。
func parsePagination(pageRaw, sizeRaw *int) (page, size int, err error) {
	page = 1
	if pageRaw != nil {
		if *pageRaw < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		page = *pageRaw
	}
	size = defaultPageSize
	if sizeRaw != nil {
		if *sizeRaw < 1 || *sizeRaw > maxPageSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		size = *sizeRaw
	}
	return page, size, nil
}

// statusToString 将 DB 实例状态映射为 API 字符串枚举。
func statusToString(status int) string {
	switch status {
	case instanceSuccess:
		return v1.StatusSuccess
	case instanceFailed:
		return v1.StatusFailed
	case instanceExpired:
		return v1.StatusExpired
	default:
		return v1.StatusInProgress
	}
}

// toActivity 将内部活动记录转换为对外结构。
func toActivity(r *activityRow) *v1.Activity {
	return &v1.Activity{
		Id:               r.Id,
		ProductId:        r.ProductId,
		SkuId:            r.SkuId,
		Name:             r.Name,
		StartPrice:       r.StartPrice,
		FloorPrice:       r.FloorPrice,
		HelpAmount:       r.HelpAmount,
		MaxHelpCount:     r.MaxHelpCount,
		StartTime:        r.StartTime,
		EndTime:          r.EndTime,
		Status:           r.Status,
		ProductName:      r.ProductName,
		ProductMainImage: r.ProductMainImage,
		SkuName:          r.SkuName,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// toInstance 将内部实例记录转换为对外结构（剩余目标金额由快照价格派生）。
func toInstance(r *instanceRow) *v1.Instance {
	remaining := r.CurrentPrice - r.FloorPrice
	if remaining < 0 {
		remaining = 0
	}
	return &v1.Instance{
		Id:               r.Id,
		ActivityId:       r.ActivityId,
		UserId:           r.UserId,
		Status:           statusToString(r.Status),
		StartPrice:       r.StartPrice,
		FloorPrice:       r.FloorPrice,
		CurrentPrice:     r.CurrentPrice,
		RemainingAmount:  remaining,
		HelpedCount:      r.HelpedCount,
		MaxHelpCount:     r.MaxHelpCount,
		ExpireAt:         r.ExpireAt,
		CompletedAt:      r.CompletedAt,
		OrderId:          r.OrderId,
		ActivityName:     r.ActivityName,
		ProductId:        r.ProductId,
		SkuId:            r.SkuId,
		ProductName:      r.ProductName,
		ProductMainImage: r.ProductMainImage,
		SkuName:          r.SkuName,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// toHelpRecord 将内部助力记录转换为对外结构。
func toHelpRecord(r *helpRow) *v1.HelpRecord {
	return &v1.HelpRecord{
		Id:              r.Id,
		InstanceId:      r.InstanceId,
		HelperUserId:    r.HelperUserId,
		HelperNickname:  r.HelperNickname,
		HelperAvatarUrl: r.HelperAvatarUrl,
		CutAmount:       r.CutAmount,
		PriceBefore:     r.PriceBefore,
		PriceAfter:      r.PriceAfter,
		CreatedAt:       r.CreatedAt,
	}
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
