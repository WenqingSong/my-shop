// Package coupon 实现「平台通用固定金额满减券」业务逻辑：模板管理（后台）、领取/我的券（前台）
// 与订单联动（预占/核销/释放，tx 感知）。事实来源单一 MySQL；并发正确性依赖条件更新 + 唯一约束兜底。
package coupon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/coupon/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 用户券状态（DB TINYINT，见 docs/design/coupon.md 状态机）。
const (
	statusAvailable = 10
	statusLocked    = 20
	statusUsed      = 30
)

// 模板状态（DB TINYINT）。
const (
	templateStatusActive  = 1
	templateStatusStopped = 0
)

const (
	// maxNameLen 模板名最大字符数（trim 后）。
	maxNameLen = 64
	// maxAmount 金额上限（整数分，= ¥999,999.99，与 SKU/秒杀价一致）。
	maxAmount = 99_999_999
)

type sCoupon struct{}

func init() {
	service.RegisterCoupon(New())
}

// New 创建并返回优惠券服务实现。
func New() *sCoupon {
	return &sCoupon{}
}

// template 是 coupon_templates 表的一条记录。
type template struct {
	Id              int64       `json:"id"`
	Name            string      `json:"name"`
	ThresholdAmount int64       `json:"threshold_amount"`
	DiscountAmount  int64       `json:"discount_amount"`
	TotalQuantity   int64       `json:"total_quantity"`
	IssuedQuantity  int64       `json:"issued_quantity"`
	ClaimStartTime  *gtime.Time `json:"claim_start_time"`
	ClaimEndTime    *gtime.Time `json:"claim_end_time"`
	UseStartTime    *gtime.Time `json:"use_start_time"`
	UseEndTime      *gtime.Time `json:"use_end_time"`
	Status          int         `json:"status"`
	CreatedAt       *gtime.Time `json:"created_at"`
	UpdatedAt       *gtime.Time `json:"updated_at"`
}

// userCoupon 是 user_coupons 表的一条记录（含模板关键字段快照，供「我的券」展示）。
type userCoupon struct {
	Id         int64       `json:"id"`
	UserId     int64       `json:"user_id"`
	TemplateId int64       `json:"template_id"`
	Status     int         `json:"status"`
	OrderId    *int64      `json:"order_id"`
	ReceivedAt *gtime.Time `json:"received_at"`
	LockedAt   *gtime.Time `json:"locked_at"`
	UsedAt     *gtime.Time `json:"used_at"`
	// 模板关键字段（ListMine LEFT JOIN 填充）。
	Name            string      `json:"name"`
	ThresholdAmount int64       `json:"threshold_amount"`
	DiscountAmount  int64       `json:"discount_amount"`
	UseStartTime    *gtime.Time `json:"use_start_time"`
	UseEndTime      *gtime.Time `json:"use_end_time"`
}

// AdminCreate 创建优惠券模板：校验满减门槛/减免金额/时间/总量后落库。
func (s *sCoupon) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	if req.ThresholdAmount < 0 || req.ThresholdAmount > maxAmount {
		return nil, codes.New(codes.CodeCouponTemplateInvalid)
	}
	if req.DiscountAmount <= 0 || req.DiscountAmount > maxAmount {
		return nil, codes.New(codes.CodeCouponTemplateInvalid)
	}
	if req.TotalQuantity <= 0 {
		return nil, codes.New(codes.CodeCouponTemplateInvalid)
	}
	if err := validateTimeRange(req.ClaimStartTime, req.ClaimEndTime); err != nil {
		return nil, err
	}
	if err := validateTimeRange(req.UseStartTime, req.UseEndTime); err != nil {
		return nil, err
	}

	id, err := g.DB().Model("coupon_templates").Ctx(ctx).Data(g.Map{
		"name":             name,
		"threshold_amount": req.ThresholdAmount,
		"discount_amount":  req.DiscountAmount,
		"total_quantity":   req.TotalQuantity,
		"issued_quantity":  0,
		"claim_start_time": req.ClaimStartTime,
		"claim_end_time":   req.ClaimEndTime,
		"use_start_time":   req.UseStartTime,
		"use_end_time":     req.UseEndTime,
		"status":           templateStatusActive,
	}).InsertAndGetId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入优惠券模板: %w", err))
	}

	tmpl, err := s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建模板后未找到记录"))
	}
	return &v1.AdminCreateRes{Template: *toTemplate(tmpl)}, nil
}

// AdminList 查询全部模板（按 id 倒序）。
func (s *sCoupon) AdminList(ctx context.Context) (*v1.AdminListRes, error) {
	var rows []*template
	if err := g.DB().Model("coupon_templates").Ctx(ctx).Order("id DESC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询优惠券模板列表: %w", err))
	}
	items := make([]*v1.Template, 0, len(rows))
	for _, r := range rows {
		items = append(items, toTemplate(r))
	}
	return &v1.AdminListRes{Items: items}, nil
}

// AdminStop 停止新领取：status=1 → 0（不影响已领取券），幂等。
func (s *sCoupon) AdminStop(ctx context.Context, id int64) (*v1.AdminStopRes, error) {
	tmpl, err := s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, codes.New(codes.CodeCouponTemplateNotFound)
	}
	if tmpl.Status == templateStatusActive {
		if _, err := g.DB().Model("coupon_templates").Ctx(ctx).
			Where("id", id).
			Where("status", templateStatusActive).
			Data(g.Map{"status": templateStatusStopped}).
			Update(); err != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("停止领取: %w", err))
		}
	}
	tmpl, err = s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, codes.New(codes.CodeCouponTemplateNotFound)
	}
	return &v1.AdminStopRes{Template: *toTemplate(tmpl)}, nil
}

// Claim 用户领取优惠券：单事务「原子闸门 +1 → 插入券实例」；每用户每模板至多一张、总量不超发、
// 并发不重复/不超发（条件更新 + uk_user_template 唯一约束兜底）。
func (s *sCoupon) Claim(ctx context.Context, userID, templateID int64) (*v1.ClaimRes, error) {
	if templateID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	// 预检：模板存在性与「已停止」快速失败；时间窗口与总量由事务内原子闸门权威判定（SQL NOW()）。
	tmpl, err := s.findTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, codes.New(codes.CodeCouponTemplateNotFound)
	}
	if tmpl.Status == templateStatusStopped {
		return nil, codes.New(codes.CodeCouponClaimUnavailable)
	}

	var couponID int64
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 原子闸门：条件更新 issued_quantity + 1（status/总量/领取时间窗口均用 SQL 判定，避免 Go↔MySQL 时区漂移）。
		result, e := tx.Ctx(ctx).Exec(
			"UPDATE coupon_templates SET issued_quantity = issued_quantity + 1 "+
				"WHERE id = ? AND status = ? AND issued_quantity < total_quantity "+
				"AND claim_start_time <= NOW() AND claim_end_time > NOW()",
			templateID, templateStatusActive,
		)
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("领取计数: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			// 区分「不可领取（未开始/已结束/已停止）」与「总量已满」。
			return s.classifyClaimUnavailable(ctx, tx, templateID)
		}

		id, e := tx.Model("user_coupons").Ctx(ctx).Data(g.Map{
			"user_id":     userID,
			"template_id": templateID,
			"status":      statusAvailable,
			"received_at": gdb.Raw("NOW()"),
		}).InsertAndGetId()
		if e != nil {
			if isDuplicateKeyError(e) {
				return codes.New(codes.CodeCouponAlreadyClaimed) // 命中 uk_user_template，回滚使 issued_quantity 不落库
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入用户券: %w", e))
		}
		couponID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.loadClaimRes(ctx, userID, couponID)
}

// classifyClaimUnavailable 原子闸门未命中时区分错误码：模板仍可领（状态+时间窗口）→ 19006（已领完）；
// 否则 → 19005（未开始/已结束/已停止，不可领取）。
func (s *sCoupon) classifyClaimUnavailable(ctx context.Context, tx gdb.TX, templateID int64) error {
	n, err := tx.Model("coupon_templates").Ctx(ctx).
		Where("id", templateID).
		Where("status", templateStatusActive).
		Where("claim_start_time <= NOW()").
		Where("claim_end_time > NOW()").
		Count()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("判定领取失败原因: %w", err))
	}
	if n == 0 {
		return codes.New(codes.CodeCouponClaimUnavailable)
	}
	return codes.New(codes.CodeCouponClaimSoldOut)
}

// ListMine 查询当前用户的优惠券（含模板关键字段快照，按 id 倒序）。
func (s *sCoupon) ListMine(ctx context.Context, userID int64) (*v1.ListMineRes, error) {
	var rows []*userCoupon
	if err := g.DB().Ctx(ctx).Model("user_coupons c").
		Fields(
			"c.id", "c.user_id", "c.template_id", "c.status", "c.order_id",
			"c.received_at", "c.locked_at", "c.used_at",
			"t.name", "t.threshold_amount", "t.discount_amount", "t.use_start_time", "t.use_end_time",
		).
		LeftJoin("coupon_templates t", "t.id = c.template_id").
		Where("c.user_id", userID).
		Order("c.id DESC").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询我的优惠券: %w", err))
	}
	items := make([]*v1.UserCoupon, 0, len(rows))
	for _, r := range rows {
		items = append(items, toUserCoupon(r))
	}
	return &v1.ListMineRes{Items: items}, nil
}

// GetDiscount 校验券归属/使用期/满减门槛并返回优惠金额（只读，不落库）。
// 券不存在或不属于当前用户 → 19003；不在使用期/未达门槛 → 19007。
// 注意：券的「可用状态（available）」不在此处校验，而是由下单事务内的 LockInTx 原子闸门（status=available 条件更新）
// 权威判定——既避免「先查再写」的竞态，也保证幂等重试时已预占的券不会在预检阶段被误拒。
func (s *sCoupon) GetDiscount(ctx context.Context, userID, couponID, originalAmount int64) (int64, error) {
	c, err := s.findCoupon(ctx, userID, couponID)
	if err != nil {
		return 0, err
	}
	if c == nil {
		return 0, codes.New(codes.CodeCouponNotFound)
	}

	tmpl, err := s.findTemplate(ctx, c.TemplateId)
	if err != nil {
		return 0, err
	}
	if tmpl == nil {
		return 0, codes.New(codes.CodeCouponUnavailable)
	}

	// 使用期校验（SQL NOW()，避免 Go↔MySQL 时区漂移）。
	inWindow, err := s.templateUsableNow(ctx, tmpl.Id)
	if err != nil {
		return 0, err
	}
	if !inWindow {
		return 0, codes.New(codes.CodeCouponUnavailable)
	}

	// 满减门槛校验。
	if originalAmount < tmpl.ThresholdAmount {
		return 0, codes.New(codes.CodeCouponUnavailable)
	}

	// 优惠金额 = min(减免金额, 原始金额)，不大于原始金额（INV-C06）。
	discount := tmpl.DiscountAmount
	if discount > originalAmount {
		discount = originalAmount
	}
	return discount, nil
}

// LockInTx 在既有事务内原子预占券：available → locked（写入 order_id/locked_at）。
// RowsAffected=0（券并发被占/已不可用）→ 19007，供下单整体回滚。
func (s *sCoupon) LockInTx(ctx context.Context, tx gdb.TX, userID, couponID, orderID int64) error {
	result, err := tx.Ctx(ctx).Exec(
		"UPDATE user_coupons SET status = ?, order_id = ?, locked_at = NOW() "+
			"WHERE id = ? AND user_id = ? AND status = ?",
		statusLocked, orderID, couponID, userID, statusAvailable,
	)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("预占优惠券: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return codes.New(codes.CodeCouponUnavailable)
	}
	return nil
}

// MarkUsedInTx 在既有事务内核销券：locked → used（终态，写 used_at）。
// RowsAffected=0 属不可达防御分支（下单已原子预占、重复支付不重复核销），静默返回 nil。
func (s *sCoupon) MarkUsedInTx(ctx context.Context, tx gdb.TX, couponID int64) error {
	if _, err := tx.Ctx(ctx).Exec(
		"UPDATE user_coupons SET status = ?, used_at = NOW() WHERE id = ? AND status = ?",
		statusUsed, couponID, statusLocked,
	); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("核销优惠券: %w", err))
	}
	return nil
}

// ReleaseInTx 在既有事务内释放订单占用的券：locked → available（清空 order_id/locked_at）。
// 订单未占券或券已释放时为 no-op（RowsAffected=0 静默返回 nil，只生效一次）。
func (s *sCoupon) ReleaseInTx(ctx context.Context, tx gdb.TX, orderID int64) error {
	if _, err := tx.Ctx(ctx).Exec(
		"UPDATE user_coupons SET status = ?, order_id = NULL, locked_at = NULL "+
			"WHERE order_id = ? AND status = ?",
		statusAvailable, orderID, statusLocked,
	); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("释放优惠券: %w", err))
	}
	return nil
}

// findTemplate 按 id 查询模板，未命中返回 nil。
func (s *sCoupon) findTemplate(ctx context.Context, id int64) (*template, error) {
	var rows []*template
	if err := g.DB().Model("coupon_templates").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询模板: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findCoupon 按 id + user_id 查询券，未命中返回 nil（区分不存在/非本人不在本层）。
func (s *sCoupon) findCoupon(ctx context.Context, userID, couponID int64) (*userCoupon, error) {
	var rows []*userCoupon
	if err := g.DB().Model("user_coupons").Ctx(ctx).Where("id", couponID).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询券: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// templateUsableNow 用 SQL NOW() 判定模板当前是否处于使用有效期（避免 Go↔MySQL 时区漂移）。
func (s *sCoupon) templateUsableNow(ctx context.Context, templateID int64) (bool, error) {
	n, err := g.DB().Model("coupon_templates").Ctx(ctx).
		Where("id", templateID).
		Where("use_start_time <= NOW()").
		Where("use_end_time > NOW()").
		Count()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询模板使用期: %w", err))
	}
	return n > 0, nil
}

// loadClaimRes 领取成功后读回券（含模板快照）并包装响应。
func (s *sCoupon) loadClaimRes(ctx context.Context, userID, couponID int64) (*v1.ClaimRes, error) {
	c, err := s.loadCouponWithTemplate(ctx, userID, couponID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("领取后未找到券"))
	}
	return &v1.ClaimRes{UserCoupon: *toUserCoupon(c)}, nil
}

// loadCouponWithTemplate 联查单张券 + 模板关键字段，未命中返回 nil。
func (s *sCoupon) loadCouponWithTemplate(ctx context.Context, userID, couponID int64) (*userCoupon, error) {
	var rows []*userCoupon
	if err := g.DB().Ctx(ctx).Model("user_coupons c").
		Fields(
			"c.id", "c.user_id", "c.template_id", "c.status", "c.order_id",
			"c.received_at", "c.locked_at", "c.used_at",
			"t.name", "t.threshold_amount", "t.discount_amount", "t.use_start_time", "t.use_end_time",
		).
		LeftJoin("coupon_templates t", "t.id = c.template_id").
		Where("c.id", couponID).
		Where("c.user_id", userID).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询券详情: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// validateName trim 后校验非空与长度，返回规范化后的模板名。
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeCouponTemplateInvalid)
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeCouponTemplateInvalid)
	}
	return name, nil
}

// validateTimeRange 校验起止时间均非空且开始早于结束。
func validateTimeRange(start, end *gtime.Time) error {
	if start == nil || end == nil {
		return codes.New(codes.CodeCouponTemplateInvalid)
	}
	if end.Unix() <= start.Unix() {
		return codes.New(codes.CodeCouponTemplateInvalid)
	}
	return nil
}

// statusToString 将 DB 券状态映射为 API 字符串枚举。
func statusToString(s int) string {
	switch s {
	case statusLocked:
		return v1.StatusLocked
	case statusUsed:
		return v1.StatusUsed
	default:
		return v1.StatusAvailable
	}
}

// toTemplate 将内部模板记录转换为对外结构。
func toTemplate(r *template) *v1.Template {
	return &v1.Template{
		Id:              r.Id,
		Name:            r.Name,
		ThresholdAmount: r.ThresholdAmount,
		DiscountAmount:  r.DiscountAmount,
		TotalQuantity:   r.TotalQuantity,
		IssuedQuantity:  r.IssuedQuantity,
		ClaimStartTime:  r.ClaimStartTime,
		ClaimEndTime:    r.ClaimEndTime,
		UseStartTime:    r.UseStartTime,
		UseEndTime:      r.UseEndTime,
		Status:          r.Status,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

// toUserCoupon 将内部券记录（含模板快照）转换为对外结构。
func toUserCoupon(r *userCoupon) *v1.UserCoupon {
	return &v1.UserCoupon{
		Id:              r.Id,
		TemplateId:      r.TemplateId,
		Status:          statusToString(r.Status),
		OrderId:         r.OrderId,
		ReceivedAt:      r.ReceivedAt,
		LockedAt:        r.LockedAt,
		UsedAt:          r.UsedAt,
		Name:            r.Name,
		ThresholdAmount: r.ThresholdAmount,
		DiscountAmount:  r.DiscountAmount,
		UseStartTime:    r.UseStartTime,
		UseEndTime:      r.UseEndTime,
	}
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
