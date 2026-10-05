// Package review 实现「商品评价」业务逻辑：提交（购买资格校验 + 服务端归属绑定）、
// 每个订单项最多一次（唯一约束兜底）、公开列表与实时汇总、本人归属隔离、管理员下架。
package review

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/review/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 评价状态（DB TINYINT）。
const (
	statusPublished = 1
	statusTakenDown = 2
	statusDeleted   = 3
)

// 订单状态：只有「已完成」可评价（40 为瞬时态，不持久化）。
const orderStatusCompleted = 50

const (
	maxContentLen = 500 // 内容最大字符数（trim 后）
	defaultPage   = 1
	defaultSize   = 20
	maxSize       = 100
)

type sReview struct{}

func init() {
	service.RegisterReview(New())
}

// New 创建并返回商品评价服务实现。
func New() *sReview {
	return &sReview{}
}

// reviewRow 是 reviews 表的一条记录。
type reviewRow struct {
	Id          int64       `json:"id"`
	UserId      int64       `json:"user_id"`
	OrderItemId int64       `json:"order_item_id"`
	ProductId   int64       `json:"product_id"`
	SkuId       int64       `json:"sku_id"`
	Rating      int         `json:"rating"`
	Content     string      `json:"content"`
	Status      int         `json:"status"`
	CreatedAt   *gtime.Time `json:"created_at"`
}

// orderItemEligibility 是购买资格校验的联查结果（order_items + orders）。
type orderItemEligibility struct {
	OrderId     int64 `json:"order_id"`
	ProductId   int64 `json:"product_id"`
	SkuId       int64 `json:"sku_id"`
	UserId      int64 `json:"user_id"`
	OrderStatus int   `json:"order_status"`
}

// Create 提交评价：校验输入 → 校验购买资格（订单项归属 + 订单已完成）→ 单条 INSERT。
// 归属字段全部由服务端从 Principal.UserID 与订单项快照推导；唯一约束兜底并发重复。
func (s *sReview) Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error) {
	if req.OrderItemId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	rating, err := validateRating(req.Rating)
	if err != nil {
		return nil, err
	}
	content, err := validateContent(req.Content)
	if err != nil {
		return nil, err
	}

	item, err := s.loadOwnedCompletedItem(ctx, userID, req.OrderItemId)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, codes.New(codes.CodeReviewNotEligible)
	}

	id, err := g.DB().Model("reviews").Ctx(ctx).Data(g.Map{
		"user_id":       userID,
		"order_item_id": req.OrderItemId,
		"product_id":    item.ProductId,
		"sku_id":        item.SkuId,
		"rating":        rating,
		"content":       content,
		"status":        statusPublished,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeReviewAlreadyExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入评价: %w", err))
	}

	row, err := s.findRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入评价后未找到记录"))
	}
	return &v1.CreateRes{Review: *s.toReview(row)}, nil
}

// ListProduct 商品公开评价列表 + 汇总（仅 published，实时聚合，按 id 倒序）。
func (s *sReview) ListProduct(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	agg, err := s.aggregate(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	var rows []*reviewRow
	if err := g.DB().Model("reviews").Ctx(ctx).
		Where("product_id", req.Id).
		Where("status", statusPublished).
		Order("id DESC").
		Page(page, size).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品评价列表: %w", err))
	}

	items := make([]*v1.ReviewItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toReviewItem(r))
	}
	return &v1.ListRes{
		Items:     items,
		Page:      page,
		Size:      size,
		Total:     agg.Count,
		AvgRating: agg.AvgRating,
		Count:     agg.Count,
	}, nil
}

// MyList 本人全部评价（含全部状态，按 id 倒序，分页）。
func (s *sReview) MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	model := g.DB().Model("reviews").Ctx(ctx).Where("user_id", userID)
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计本人评价: %w", err))
	}
	var rows []*reviewRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询本人评价: %w", err))
	}

	items := make([]*v1.Review, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toReview(r))
	}
	return &v1.MyListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// Update 修改本人评价：条件更新 WHERE id AND user_id AND status=published，核对 RowsAffected。
func (s *sReview) Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	rating, err := validateRating(req.Rating)
	if err != nil {
		return nil, err
	}
	content, err := validateContent(req.Content)
	if err != nil {
		return nil, err
	}

	result, err := g.DB().Model("reviews").Ctx(ctx).
		Where("id", req.Id).
		Where("user_id", userID).
		Where("status", statusPublished).
		Data(g.Map{"rating": rating, "content": content}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改评价: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeReviewNotFound)
	}

	row, err := s.findRow(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改评价后未找到记录"))
	}
	return &v1.UpdateRes{Review: *s.toReview(row)}, nil
}

// Delete 删除本人评价：条件更新 published → deleted，核对 RowsAffected。
func (s *sReview) Delete(ctx context.Context, userID, id int64) (*v1.DeleteRes, error) {
	result, err := g.DB().Model("reviews").Ctx(ctx).
		Where("id", id).
		Where("user_id", userID).
		Where("status", statusPublished).
		Data(g.Map{"status": statusDeleted}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除评价: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeReviewNotFound)
	}

	row, err := s.findRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除评价后未找到记录"))
	}
	return &v1.DeleteRes{Review: *s.toReview(row)}, nil
}

// TakeDown 下架违规评价（仅管理员，经 RequirePermission("review:take_down")）：
// 条件更新 published → taken_down，核对 RowsAffected；不存在/已非 published 统一 10001。
func (s *sReview) TakeDown(ctx context.Context, id int64) (*v1.TakeDownRes, error) {
	result, err := g.DB().Model("reviews").Ctx(ctx).
		Where("id", id).
		Where("status", statusPublished).
		Data(g.Map{"status": statusTakenDown}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("下架评价: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeReviewNotFound)
	}

	row, err := s.findRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("下架评价后未找到记录"))
	}
	return &v1.TakeDownRes{Review: *s.toReview(row)}, nil
}

// loadOwnedCompletedItem 联查订单项与订单，校验「订单项属于当前用户且订单已完成」。
// 不存在、非本人、或订单未完成均返回 nil（上层统一 10002，不泄露具体原因）。
func (s *sReview) loadOwnedCompletedItem(ctx context.Context, userID, orderItemID int64) (*orderItemEligibility, error) {
	var rows []*orderItemEligibility
	if err := g.DB().Model("order_items oi").Ctx(ctx).
		InnerJoin("orders o", "o.id = oi.order_id").
		Fields("oi.order_id", "oi.product_id", "oi.sku_id", "o.user_id", "o.status AS order_status").
		Where("oi.id", orderItemID).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询订单项购买资格: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	item := rows[0]
	if item.UserId != userID || item.OrderStatus != orderStatusCompleted {
		return nil, nil
	}
	return item, nil
}

// aggregate 实时聚合某商品的已发布评价：条数 + 平均分（1 位小数）。
func (s *sReview) aggregate(ctx context.Context, productID int64) (*reviewAggregate, error) {
	record, err := g.DB().Model("reviews").Ctx(ctx).
		Fields("COUNT(*) AS cnt", "COALESCE(SUM(rating), 0) AS total").
		Where("product_id", productID).
		Where("status", statusPublished).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计商品评价汇总: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return &reviewAggregate{}, nil
	}
	cnt := record["cnt"].Int()
	total := record["total"].Int64()
	avg := 0.0
	if cnt > 0 {
		avg = round1(float64(total) / float64(cnt))
	}
	return &reviewAggregate{Count: cnt, AvgRating: avg}, nil
}

// reviewAggregate 是某商品已发布评价的汇总结果。
type reviewAggregate struct {
	Count     int
	AvgRating float64
}

// findRow 按 id 查询评价行，未命中返回 nil。
func (s *sReview) findRow(ctx context.Context, id int64) (*reviewRow, error) {
	var rows []*reviewRow
	if err := g.DB().Model("reviews").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询评价: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// toReview 将评价行转换为完整对外结构。
func (s *sReview) toReview(r *reviewRow) *v1.Review {
	return &v1.Review{
		Id:          r.Id,
		UserId:      r.UserId,
		OrderItemId: r.OrderItemId,
		ProductId:   r.ProductId,
		SkuId:       r.SkuId,
		Rating:      r.Rating,
		Content:     r.Content,
		Status:      statusToString(r.Status),
		CreatedAt:   r.CreatedAt,
	}
}

// toReviewItem 将评价行转换为公开条目结构。
func (s *sReview) toReviewItem(r *reviewRow) *v1.ReviewItem {
	return &v1.ReviewItem{
		Id:        r.Id,
		ProductId: r.ProductId,
		SkuId:     r.SkuId,
		Rating:    r.Rating,
		Content:   r.Content,
		UserId:    r.UserId,
		CreatedAt: r.CreatedAt,
	}
}

// validateRating 校验星级为 1-5 整数，越界返回 10004。
func validateRating(rating int) (int, error) {
	if rating < 1 || rating > 5 {
		return 0, codes.New(codes.CodeReviewInvalidInput)
	}
	return rating, nil
}

// validateContent 校验内容 trim 后非空且不超过 500 字符，返回规范化内容。
func validateContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", codes.New(codes.CodeReviewInvalidInput)
	}
	if utf8.RuneCountInString(content) > maxContentLen {
		return "", codes.New(codes.CodeReviewInvalidInput)
	}
	return content, nil
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(status int) string {
	switch status {
	case statusTakenDown:
		return v1.StatusTakenDown
	case statusDeleted:
		return v1.StatusDeleted
	default:
		return v1.StatusPublished
	}
}

// normalizePage 归一化分页参数：page/size 下限 1，size 上限 maxSize。
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

// round1 四舍五入到 1 位小数。
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发提交同一 order_item_id 时，uk_order_item 唯一约束是最终兜底。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
