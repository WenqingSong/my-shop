// Package member 实现「会员等级与会员权益」业务逻辑：
//   - 等级配置（member_levels）：前台只读查询、后台增删改（唯一性 + 序号/门槛同向递增）；
//   - 用户会员（user_memberships）：当前等级、累计成长值、升级时间；一人一行，只升不降；
//   - 成长值流水（member_growth_logs）：append-only、幂等键唯一、与余额变更同事务；
//   - 订单/退款联动：按支付成功发放成长值并推进等级、退款成功按原发放额扣减（均幂等）。
//
// 数值口径：成长值 = 已支付订单实付金额（整数分），1 分消费 = 1 成长值；
// 折扣率 discount_rate_bp 为万分比（10000=原价）。积分账本（user_points_*）与本模块完全隔离。
package member

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/member/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// levelStatusEnabled 是 member_levels.status 的启用值。
	levelStatusEnabled = 1

	// orderStatusPaid 是 orders.status 的「已支付」值：只有已支付订单发放成长值。
	orderStatusPaid = 20

	// noDiscountRateBp 表示不打折（原价），也是「无适用等级」时的生效折扣率。
	noDiscountRateBp = 10000

	// maxLevelNameLen 是等级名称最大字符数（与 member_levels.name VARCHAR(64) 对齐）。
	maxLevelNameLen = 64

	// 成长值流水语义类型（member_growth_logs.change_type）。
	changeTypeOrderReward  = "order_reward"
	changeTypeRefundDeduct = "refund_deduct"

	// 成长值流水业务类型（member_growth_logs.biz_type）。
	bizTypeOrder  = "order"
	bizTypeRefund = "refund"

	// growthKeyOrderRewardPrefix 是订单发放成长值的幂等键前缀：
	// 完整键 growth:order_reward:<order_id>，见 docs/design/member.md §4。
	growthKeyOrderRewardPrefix = "growth:order_reward:"
	// growthKeyRefundDeductPrefix 是退款扣减成长值的幂等键前缀：
	// 完整键 growth:refund_deduct:<refund_id>。
	growthKeyRefundDeductPrefix = "growth:refund_deduct:"

	// 分页默认值与上限（与项目既有 clamp 风格一致）。
	defaultPage = 1
	defaultSize = 20
	maxSize     = 100
)

type sMember struct{}

func init() {
	service.RegisterMember(New())
}

// New 创建并返回会员服务实现。
func New() *sMember {
	return &sMember{}
}

// levelRow 是 member_levels 表的一条记录。
type levelRow struct {
	Id              int64       `json:"id"`
	LevelNo         int         `json:"level_no"`
	Name            string      `json:"name"`
	GrowthThreshold int64       `json:"growth_threshold"`
	DiscountRateBp  int         `json:"discount_rate_bp"`
	Status          int         `json:"status"`
	CreatedAt       *gtime.Time `json:"created_at"`
	UpdatedAt       *gtime.Time `json:"updated_at"`
}

// membershipRow 是 user_memberships 表的一条记录。
type membershipRow struct {
	Id          int64       `json:"id"`
	UserId      int64       `json:"user_id"`
	LevelId     int64       `json:"level_id"`
	GrowthValue int64       `json:"growth_value"`
	UpgradedAt  *gtime.Time `json:"upgraded_at"`
}

// growthLogRow 是 member_growth_logs 表的一条记录。
type growthLogRow struct {
	Id             int64       `json:"id"`
	UserId         int64       `json:"user_id"`
	ChangeAmount   int64       `json:"change_amount"`
	GrowthBefore   int64       `json:"growth_before"`
	GrowthAfter    int64       `json:"growth_after"`
	ChangeType     string      `json:"change_type"`
	BizType        string      `json:"biz_type"`
	BizId          *int64      `json:"biz_id"`
	IdempotencyKey string      `json:"idempotency_key"`
	Remark         string      `json:"remark"`
	CreatedAt      *gtime.Time `json:"created_at"`
}

// orderGrowthSnapshot 是发放成长值所需的订单事实（只读快照）。
type orderGrowthSnapshot struct {
	Id          int64 `json:"id"`
	UserId      int64 `json:"user_id"`
	Status      int   `json:"status"`
	TotalAmount int64 `json:"total_amount"`
}

// Levels 查询启用中的等级配置，按 level_no 升序（前台只读）。
func (s *sMember) Levels(ctx context.Context) (*v1.LevelsRes, error) {
	var rows []*levelRow
	if err := g.DB().Model("member_levels").Ctx(ctx).
		Where("status", levelStatusEnabled).
		Order("level_no ASC").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询会员等级配置: %w", err))
	}
	items := make([]*v1.MemberLevel, 0, len(rows))
	for _, r := range rows {
		items = append(items, toMemberLevel(r))
	}
	return &v1.LevelsRes{Items: items}, nil
}

// My 查询指定用户的当前会员等级、成长值与下一等级进度。
// 用户没有 user_memberships 档案时按「成长值 0」解析适用等级：不写库、不隐式建档。
func (s *sMember) My(ctx context.Context, userID int64) (*v1.MyRes, error) {
	m, err := s.buildMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &v1.MyRes{Membership: *m}, nil
}

// GrowthLogs 分页查询成长值流水（仅本人，按 id 倒序）。
func (s *sMember) GrowthLogs(ctx context.Context, userID int64, req *v1.GrowthLogsReq) (*v1.GrowthLogsRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	total, err := g.DB().Model("member_growth_logs").Ctx(ctx).Where("user_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计成长值流水: %w", err))
	}

	var rows []*growthLogRow
	if err := g.DB().Model("member_growth_logs").Ctx(ctx).
		Where("user_id", userID).
		Order("id DESC").
		Page(page, size).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询成长值流水: %w", err))
	}

	items := make([]*v1.GrowthLog, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.GrowthLog{
			Id:           r.Id,
			ChangeAmount: r.ChangeAmount,
			GrowthBefore: r.GrowthBefore,
			GrowthAfter:  r.GrowthAfter,
			ChangeType:   r.ChangeType,
			BizType:      r.BizType,
			BizId:        r.BizId,
			Remark:       r.Remark,
			CreatedAt:    r.CreatedAt,
		})
	}
	return &v1.GrowthLogsRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// AdminListLevels 查询全部等级配置（含停用），按 level_no 升序。
func (s *sMember) AdminListLevels(ctx context.Context) (*v1.AdminListLevelsRes, error) {
	var rows []*levelRow
	if err := g.DB().Model("member_levels").Ctx(ctx).Order("level_no ASC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询会员等级配置: %w", err))
	}
	items := make([]*v1.MemberLevel, 0, len(rows))
	for _, r := range rows {
		items = append(items, toMemberLevel(r))
	}
	return &v1.AdminListLevelsRes{Items: items}, nil
}

// AdminCreateLevel 创建等级配置。
//
// 校验（全部在 Logic 层完成，避免 gvalid 失败穿透为 500）：
//   - level_no 正整数；name trim 后 1~64 字符；growth_threshold ≥ 0；discount_rate_bp ∈ [1,10000]；status ∈ {0,1}；
//   - level_no / growth_threshold 由唯一键机械保证（1062 → 20004）；
//   - 写入后配置必须满足「按 level_no 升序时 growth_threshold 严格递增」（否则 20002 并回滚）。
//
// 并发：事务内先对全部等级行加锁再校验单调性，避免两个并发写入各自合法、合起来非法。
func (s *sMember) AdminCreateLevel(ctx context.Context, req *v1.AdminCreateLevelReq) (*v1.AdminCreateLevelRes, error) {
	name, err := validateLevelInput(req.LevelNo, req.Name, req.GrowthThreshold, req.DiscountRateBp, req.Status)
	if err != nil {
		return nil, err
	}

	var created *levelRow
	txErr := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		res, e := tx.Model("member_levels").Ctx(ctx).Data(g.Map{
			"level_no":         req.LevelNo,
			"name":             name,
			"growth_threshold": req.GrowthThreshold,
			"discount_rate_bp": req.DiscountRateBp,
			"status":           req.Status,
		}).Insert()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				return codes.Wrap(codes.CodeMemberLevelDuplicate, fmt.Errorf("写入等级配置命中唯一键 %s: %w", key, e))
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入等级配置: %w", e))
		}
		id, e := res.LastInsertId()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("获取等级配置 id: %w", e))
		}
		if e := requireMonotonicLevels(ctx, tx); e != nil {
			return e
		}
		var rows []*levelRow
		if e := tx.Model("member_levels").Ctx(ctx).Where("id", id).Scan(&rows); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读回等级配置: %w", e))
		}
		if len(rows) == 0 {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建等级配置后未找到记录"))
		}
		created = rows[0]
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return &v1.AdminCreateLevelRes{MemberLevel: *toMemberLevel(created)}, nil
}

// AdminUpdateLevel 全量更新等级配置（校验规则与创建一致；等级不存在返回 20001）。
func (s *sMember) AdminUpdateLevel(ctx context.Context, req *v1.AdminUpdateLevelReq) (*v1.AdminUpdateLevelRes, error) {
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeMemberLevelNotFound)
	}
	name, err := validateLevelInput(req.LevelNo, req.Name, req.GrowthThreshold, req.DiscountRateBp, req.Status)
	if err != nil {
		return nil, err
	}

	var updated *levelRow
	txErr := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 先锁全表等级行：同一把锁同时保护「行存在性」「唯一键」「单调性」三件事。
		if e := lockAllLevels(ctx, tx); e != nil {
			return e
		}
		res, e := tx.Model("member_levels").Ctx(ctx).
			Where("id", req.Id).
			Data(g.Map{
				"level_no":         req.LevelNo,
				"name":             name,
				"growth_threshold": req.GrowthThreshold,
				"discount_rate_bp": req.DiscountRateBp,
				"status":           req.Status,
			}).Update()
		if e != nil {
			if key := duplicateKeyName(e); key != "" {
				return codes.Wrap(codes.CodeMemberLevelDuplicate, fmt.Errorf("更新等级配置命中唯一键 %s: %w", key, e))
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新等级配置: %w", e))
		}
		affected, e := res.RowsAffected()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取等级配置更新结果: %w", e))
		}
		if affected == 0 {
			// MySQL 在「值完全相同」时也返回 0 行，因此必须再确认行是否存在。
			exists, e := tx.Model("member_levels").Ctx(ctx).Where("id", req.Id).Count()
			if e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("确认等级配置存在性: %w", e))
			}
			if exists == 0 {
				return codes.New(codes.CodeMemberLevelNotFound)
			}
		}
		if e := requireMonotonicLevels(ctx, tx); e != nil {
			return e
		}
		var rows []*levelRow
		if e := tx.Model("member_levels").Ctx(ctx).Where("id", req.Id).Scan(&rows); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读回等级配置: %w", e))
		}
		if len(rows) == 0 {
			return codes.New(codes.CodeMemberLevelNotFound)
		}
		updated = rows[0]
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return &v1.AdminUpdateLevelRes{MemberLevel: *toMemberLevel(updated)}, nil
}

// AdminDeleteLevel 删除等级配置。
// 被 user_memberships.level_id 引用时由 FK RESTRICT 拒绝（1451 → 20003），不静默改绑用户等级。
func (s *sMember) AdminDeleteLevel(ctx context.Context, id int64) (*v1.AdminDeleteLevelRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeMemberLevelNotFound)
	}
	res, err := g.DB().Model("member_levels").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		if isForeignKeyConstraintError(err) {
			return nil, codes.Wrap(codes.CodeMemberLevelInUse, fmt.Errorf("删除等级配置 %d 被用户会员记录引用: %w", id, err))
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除等级配置: %w", err))
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取等级配置删除结果: %w", err))
	}
	if affected == 0 {
		return nil, codes.New(codes.CodeMemberLevelNotFound)
	}
	return &v1.AdminDeleteLevelRes{}, nil
}

// AdminMember 后台查询指定用户的会员信息。
func (s *sMember) AdminMember(ctx context.Context, userID int64) (*v1.AdminMemberRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	m, err := s.buildMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &v1.AdminMemberRes{Membership: *m, HasRecord: m.LevelId != nil}, nil
}

// DiscountRateBp 返回用户下单时生效的折扣率（万分比）。
//
// 语义（见 docs/design/member.md §5）：
//   - 有会员档案：直接取档案当前等级的 discount_rate_bp（等级后续被停用也不影响已入会用户）；
//   - 无会员档案：按成长值 0 解析适用等级；仍无适用等级时返回 10000（不打折）。
//
// 只读：不建档、不改成长值，保证下单失败/幂等重放不产生会员副作用。
func (s *sMember) DiscountRateBp(ctx context.Context, userID int64) (int, error) {
	if userID <= 0 {
		return noDiscountRateBp, codes.New(codes.CodeInvalidArgument)
	}
	var rows []*struct {
		LevelId        int64 `json:"level_id"`
		DiscountRateBp int   `json:"discount_rate_bp"`
	}
	if err := g.DB().Model("user_memberships", "m").Ctx(ctx).
		LeftJoin("member_levels l", "l.id = m.level_id").
		Fields("m.level_id, l.discount_rate_bp").
		Where("m.user_id", userID).
		Scan(&rows); err != nil {
		return noDiscountRateBp, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户会员折扣: %w", err))
	}
	if len(rows) > 0 && rows[0].LevelId > 0 {
		return clampDiscountRate(rows[0].DiscountRateBp), nil
	}

	level, err := findLevelForGrowth(ctx, g.DB().Model("member_levels"), 0)
	if err != nil {
		return noDiscountRateBp, err
	}
	if level == nil {
		return noDiscountRateBp, nil
	}
	return clampDiscountRate(level.DiscountRateBp), nil
}

// AccrueForOrderInTx 在既有事务内按订单实付金额发放成长值并推进等级。
//
// 事务边界：不自开事务，全部读写使用调用方 tx；订单状态推进、成长值余额、成长值流水同事务提交。
// 幂等：member_growth_logs.uk_idempotency + 确定性键 growth:order_reward:<order_id>，
// 重复支付回调命中 1062 视为已处理（返回 nil，不重复加成长值）。
// 并发：先 INSERT ... ON DUPLICATE KEY 建档，再 SELECT ... FOR UPDATE 锁档案行，
// 使同一用户的并发成长值变更串行化，growth_before/growth_after 不会交错。
// 未配置等级：平台还没配任何可用等级时按 no-op 跳过（记 warn 日志，不阻塞支付）。
func (s *sMember) AccrueForOrderInTx(ctx context.Context, tx gdb.TX, orderID int64) error {
	if orderID <= 0 {
		return codes.New(codes.CodeInvalidArgument)
	}

	var orders []*orderGrowthSnapshot
	if err := tx.Model("orders").Ctx(ctx).
		Fields("id, user_id, status, total_amount").
		Where("id", orderID).
		Scan(&orders); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取订单 %d 用于成长值发放: %w", orderID, err))
	}
	if len(orders) == 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("订单 %d 不存在，无法发放成长值", orderID))
	}
	order := orders[0]
	// 只有已支付订单产生成长值：未支付/已取消订单即使被误调用也不发放。
	if order.Status != orderStatusPaid || order.TotalAmount <= 0 {
		return nil
	}

	// 成长值口径 = 实付金额（整数分）。0 元订单不产生成长值，也不写 0 变动流水。
	amount := order.TotalAmount
	if amount > math.MaxInt64/2 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("订单 %d 金额 %d 超出成长值处理范围", orderID, amount))
	}

	baseLevel, err := findLevelForGrowth(ctx, tx.Model("member_levels"), 0)
	if err != nil {
		return err
	}
	if baseLevel == nil {
		logx.Warning(ctx, "member_level_not_configured", "未配置可用会员等级，跳过成长值发放", logx.Fields{
			"order_id": orderID,
			"user_id":  order.UserId,
		})
		return nil
	}

	// 建档（并发安全）：无档案时以「成长值 0 适用等级」初始化；已有档案不改任何字段。
	if _, err := tx.Ctx(ctx).Exec(
		`INSERT INTO user_memberships (user_id, level_id, growth_value) VALUES (?, ?, 0)
		 ON DUPLICATE KEY UPDATE id = id`, order.UserId, baseLevel.Id); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("初始化用户会员档案: %w", err))
	}

	// 行锁：同一用户的并发成长值变更串行化。
	var memberships []*membershipRow
	if err := tx.Model("user_memberships").Ctx(ctx).
		Where("user_id", order.UserId).
		LockUpdate().
		Scan(&memberships); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定用户会员档案: %w", err))
	}
	if len(memberships) == 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("用户 %d 会员档案缺失，无法发放成长值", order.UserId))
	}
	current := memberships[0]

	before := current.GrowthValue
	after := before + amount

	// 幂等闸门：唯一键冲突表示该订单的成长值已发放过。
	if _, err := tx.Model("member_growth_logs").Ctx(ctx).Data(g.Map{
		"user_id":         order.UserId,
		"change_amount":   amount,
		"growth_before":   before,
		"growth_after":    after,
		"change_type":     changeTypeOrderReward,
		"biz_type":        bizTypeOrder,
		"biz_id":          orderID,
		"idempotency_key": growthKeyOrderRewardPrefix + itoa(orderID),
		"remark":          "订单支付成功发放成长值",
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入成长值流水(订单 %d): %w", orderID, err))
	}

	// 等级判定：只升不降，只有存在更高（或首个更匹配）的启用等级时才更新 level_id。
	target := current.LevelId
	if level, e := findLevelForGrowth(ctx, tx.Model("member_levels"), after); e != nil {
		return e
	} else if level != nil && level.Id != current.LevelId {
		target = level.Id
	}

	data := g.Map{"growth_value": after}
	if target != current.LevelId {
		data["level_id"] = target
		data["upgraded_at"] = gdb.Raw("NOW()")
	}
	result, err := tx.Model("user_memberships").Ctx(ctx).
		Where("user_id", order.UserId).
		Where("growth_value", before).
		Data(data).
		Update()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新会员成长值: %w", err))
	}
	if affected, e := result.RowsAffected(); e != nil || affected == 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新会员成长值未命中（user_id=%d growth_before=%d）", order.UserId, before))
	}

	logx.Info(ctx, "member_growth_accrued", "订单成长值发放完成", logx.Fields{
		"user_id":      order.UserId,
		"order_id":     orderID,
		"growth_delta": amount,
		"growth_after": after,
		"level_id":     target,
		"level_up":     target != current.LevelId,
	})
	return nil
}

// DeductForRefundInTx 在既有事务内扣减该订单已发放的成长值（退款成功）。
//
// 幂等：键 growth:refund_deduct:<refund_id>，重复调用 1062 视为已处理。
// 语义：按「该订单当初发放的成长值」原额扣减，不足时按当前余额扣减（不出现负值），
// 扣减不回退 level_id（等级只升不降，见 docs/design/member.md §5）。
// 订单从未发放过成长值时 no-op，不伪造流水。
func (s *sMember) DeductForRefundInTx(ctx context.Context, tx gdb.TX, orderID, refundID int64) error {
	if orderID <= 0 || refundID <= 0 {
		return codes.New(codes.CodeInvalidArgument)
	}

	var awarded []*growthLogRow
	if err := tx.Model("member_growth_logs").Ctx(ctx).
		Fields("id, user_id, change_amount").
		Where("idempotency_key", growthKeyOrderRewardPrefix+itoa(orderID)).
		Scan(&awarded); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取订单 %d 的成长值发放流水: %w", orderID, err))
	}
	if len(awarded) == 0 || awarded[0].ChangeAmount <= 0 {
		return nil
	}
	userID := awarded[0].UserId
	want := awarded[0].ChangeAmount

	var memberships []*membershipRow
	if err := tx.Model("user_memberships").Ctx(ctx).
		Where("user_id", userID).
		LockUpdate().
		Scan(&memberships); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定用户会员档案: %w", err))
	}
	if len(memberships) == 0 {
		logx.Warning(ctx, "member_refund_without_membership", "退款扣减成长值但用户无会员档案", logx.Fields{
			"order_id":  orderID,
			"refund_id": refundID,
			"user_id":   userID,
		})
		return nil
	}
	before := memberships[0].GrowthValue
	deduct := want
	if deduct > before {
		deduct = before
	}
	if deduct <= 0 {
		logx.Warning(ctx, "member_refund_growth_already_zero", "退款扣减成长值但当前余额为 0", logx.Fields{
			"order_id":  orderID,
			"refund_id": refundID,
			"user_id":   userID,
		})
		return nil
	}
	after := before - deduct

	if _, err := tx.Model("member_growth_logs").Ctx(ctx).Data(g.Map{
		"user_id":         userID,
		"change_amount":   -deduct,
		"growth_before":   before,
		"growth_after":    after,
		"change_type":     changeTypeRefundDeduct,
		"biz_type":        bizTypeRefund,
		"biz_id":          refundID,
		"idempotency_key": growthKeyRefundDeductPrefix + itoa(refundID),
		"remark":          "订单退款成功扣减成长值",
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入成长值扣减流水(退款 %d): %w", refundID, err))
	}

	result, err := tx.Model("user_memberships").Ctx(ctx).
		Where("user_id", userID).
		Where("growth_value", before).
		Data(g.Map{"growth_value": after}).
		Update()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("扣减会员成长值: %w", err))
	}
	if affected, e := result.RowsAffected(); e != nil || affected == 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("扣减会员成长值未命中（user_id=%d growth_before=%d）", userID, before))
	}

	logx.Info(ctx, "member_growth_reversed", "退款扣减成长值完成", logx.Fields{
		"user_id":      userID,
		"order_id":     orderID,
		"refund_id":    refundID,
		"growth_delta": -deduct,
		"growth_after": after,
	})
	return nil
}

// buildMembership 组装用户会员信息：当前等级、成长值、下一等级进度。
// 无档案时按成长值 0 解析等级，返回的 LevelId/LevelNo 仍为 null（区分「未建档」与「建档于最低等级」）。
func (s *sMember) buildMembership(ctx context.Context, userID int64) (*v1.Membership, error) {
	out := &v1.Membership{UserId: userID, DiscountRateBp: noDiscountRateBp}

	var rows []*struct {
		LevelId        int64       `json:"level_id"`
		GrowthValue    int64       `json:"growth_value"`
		UpgradedAt     *gtime.Time `json:"upgraded_at"`
		LevelNo        int         `json:"level_no"`
		LevelName      string      `json:"level_name"`
		DiscountRateBp int         `json:"discount_rate_bp"`
	}
	if err := g.DB().Model("user_memberships", "m").Ctx(ctx).
		LeftJoin("member_levels l", "l.id = m.level_id").
		Fields("m.level_id, m.growth_value, m.upgraded_at, l.level_no, l.name AS level_name, l.discount_rate_bp").
		Where("m.user_id", userID).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户会员信息: %w", err))
	}

	growth := int64(0)
	if len(rows) > 0 {
		growth = rows[0].GrowthValue
		out.GrowthValue = growth
		out.UpgradedAt = rows[0].UpgradedAt
		levelID := rows[0].LevelId
		levelNo := rows[0].LevelNo
		out.LevelId = &levelID
		out.LevelNo = &levelNo
		out.LevelName = rows[0].LevelName
		out.DiscountRateBp = clampDiscountRate(rows[0].DiscountRateBp)
	} else {
		// 未建档：按成长值 0 解析当前适用等级（只读，不创建档案）。
		level, err := findLevelForGrowth(ctx, g.DB().Model("member_levels"), 0)
		if err != nil {
			return nil, err
		}
		if level != nil {
			out.LevelName = level.Name
			out.DiscountRateBp = clampDiscountRate(level.DiscountRateBp)
		}
	}

	next, err := findNextLevel(ctx, growth)
	if err != nil {
		return nil, err
	}
	if next != nil {
		nextID := next.Id
		nextThreshold := next.GrowthThreshold
		out.NextLevelId = &nextID
		out.NextLevelName = next.Name
		out.NextThreshold = &nextThreshold
	}
	return out, nil
}

// findLevelForGrowth 返回「已启用且 growth_threshold ≤ growth」中门槛最高的等级；不存在返回 nil。
// model 由调用方给出（事务内用 tx.Model），保证同事务读到一致的配置。
func findLevelForGrowth(ctx context.Context, model *gdb.Model, growth int64) (*levelRow, error) {
	var rows []*levelRow
	if err := model.Ctx(ctx).
		Where("status", levelStatusEnabled).
		Where("growth_threshold <= ?", growth).
		Order("growth_threshold DESC").
		Limit(1).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("解析会员等级（成长值 %d）: %w", growth, err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findNextLevel 返回门槛高于当前成长值的最近一个启用等级（用于展示升级进度）。
func findNextLevel(ctx context.Context, growth int64) (*levelRow, error) {
	var rows []*levelRow
	if err := g.DB().Model("member_levels").Ctx(ctx).
		Where("status", levelStatusEnabled).
		Where("growth_threshold > ?", growth).
		Order("growth_threshold ASC").
		Limit(1).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("解析下一会员等级: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// lockAllLevels 在事务内锁定全部等级配置行，串行化配置写入（保护唯一键与单调性校验）。
func lockAllLevels(ctx context.Context, tx gdb.TX) error {
	var rows []*levelRow
	if err := tx.Model("member_levels").Ctx(ctx).
		Fields("id").
		Order("id ASC").
		LockUpdate().
		Scan(&rows); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定会员等级配置: %w", err))
	}
	return nil
}

// requireMonotonicLevels 校验「按 level_no 升序时 growth_threshold 严格递增」。
// 调用方必须已在本事务内锁定等级行，否则并发写入可能绕过校验。
func requireMonotonicLevels(ctx context.Context, tx gdb.TX) error {
	var rows []*levelRow
	if err := tx.Model("member_levels").Ctx(ctx).
		Fields("id, level_no, growth_threshold").
		Order("level_no ASC").
		Scan(&rows); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取会员等级配置用于单调性校验: %w", err))
	}
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if cur.GrowthThreshold <= prev.GrowthThreshold {
			return codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf(
				"等级门槛必须随 level_no 严格递增：level_no=%d(门槛 %d) 与 level_no=%d(门槛 %d) 冲突",
				prev.LevelNo, prev.GrowthThreshold, cur.LevelNo, cur.GrowthThreshold))
		}
	}
	return nil
}

// validateLevelInput 校验等级配置入参，返回 trim 后的名称。
func validateLevelInput(levelNo int, name string, threshold int64, discountRateBp, status int) (string, error) {
	if levelNo <= 0 || levelNo > math.MaxUint16 {
		return "", codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf("level_no 必须为 1~%d 的正整数", math.MaxUint16))
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maxLevelNameLen {
		return "", codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf("等级名称必须为 1~%d 字符", maxLevelNameLen))
	}
	if threshold < 0 {
		return "", codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf("成长值门槛不能为负数"))
	}
	if discountRateBp < 1 || discountRateBp > noDiscountRateBp {
		return "", codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf("折扣率必须在 1~%d 之间", noDiscountRateBp))
	}
	if status != v1.LevelStatusEnabled && status != v1.LevelStatusDisabled {
		return "", codes.Wrap(codes.CodeMemberLevelInvalid, fmt.Errorf("状态只能为 0（停用）或 1（启用）"))
	}
	return trimmed, nil
}

// clampDiscountRate 把折扣率夹到合法区间，防御脏配置导致订单金额越界。
func clampDiscountRate(bp int) int {
	if bp <= 0 || bp > noDiscountRateBp {
		return noDiscountRateBp
	}
	return bp
}

// toMemberLevel 把行结构转换为对外 DTO。
func toMemberLevel(r *levelRow) *v1.MemberLevel {
	return &v1.MemberLevel{
		Id:              r.Id,
		LevelNo:         r.LevelNo,
		Name:            r.Name,
		GrowthThreshold: r.GrowthThreshold,
		DiscountRateBp:  r.DiscountRateBp,
		Status:          r.Status,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

// normalizePage 归一化分页参数（clamp 风格，与 favorite/review 一致）。
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = defaultPage
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}

// itoa 把 id 转为十进制字符串（幂等键拼接用）。
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

// isDuplicateKeyError 判断是否为 MySQL 唯一键冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// isForeignKeyConstraintError 判断是否为 MySQL 外键约束失败（1451 父行被引用 / 1452 子行缺失）。
func isForeignKeyConstraintError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1451 || mysqlErr.Number == 1452
}

// duplicateKeyName 提取唯一键冲突中的索引名（便于区分 level_no / growth_threshold）。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		return ""
	}
	msg := mysqlErr.Message
	// MySQL 8.0 文本形如：Duplicate entry '1' for key 'member_levels.uk_level_no'
	idx := strings.Index(msg, "for key '")
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len("for key '"):]
	if end := strings.Index(rest, "'"); end >= 0 {
		return rest[:end]
	}
	return ""
}
